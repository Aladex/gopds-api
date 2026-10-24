package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopds-api/internal/authorentity"
	"gopds-api/internal/wikidata"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// The Wikidata reference of the author entity build (migration 60, design E,
// phase E4w): an extract is fetched chunk by chunk, each chunk with its rows
// in a transaction of its own, so a failed chunk is redone alone and a fetch
// goes on from the chunks it lacks. Once every chunk is in, the extract is
// published under its content ID in one short transaction — the build reads
// the published extract only — or, when the published extract has the same
// content, discarded: the same data never changes a row. The rows of every
// other extract are deleted after that, in transactions of their own.

// Extract statuses.
const (
	WikidataFetching   = "fetching"
	WikidataFailed     = "failed"
	WikidataPublished  = "published"
	WikidataDuplicate  = "duplicate"
	WikidataSuperseded = "superseded"
	WikidataAbandoned  = "abandoned"
)

// WikidataExtract is one fetch of the reference.
type WikidataExtract struct {
	ID         int64           `pg:"id" json:"id"`
	ContentID  *string         `pg:"content_id" json:"content_id,omitempty"`
	Status     string          `pg:"status" json:"status"`
	Source     string          `pg:"source" json:"source"`
	Manifest   json.RawMessage `pg:"manifest,type:jsonb" json:"-"`
	People     int             `pg:"people,use_zero" json:"people"`
	Names      int             `pg:"names,use_zero" json:"names"`
	Works      int             `pg:"works,use_zero" json:"works"`
	LastError  *string         `pg:"last_error" json:"last_error,omitempty"`
	StartedAt  time.Time       `pg:"started_at" json:"started_at"`
	FinishedAt *time.Time      `pg:"finished_at" json:"finished_at,omitempty"`
	// ChunksDone are the chunks written; ChunksTotal how many an extract
	// has.
	ChunksDone  int `pg:"chunks_done,use_zero" json:"chunks_done"`
	ChunksTotal int `pg:"-" json:"chunks_total"`
}

const wikidataExtractColumns = `e.id, e.content_id, e.status, e.source, e.manifest, e.people, e.names, e.works,
	e.last_error, e.started_at, e.finished_at,
	(SELECT count(*) FROM public.wikidata_extract_chunk c WHERE c.extract_id = e.id) AS chunks_done`

// ErrWikidataFetchActive refuses a second fetch while one is running.
var ErrWikidataFetchActive = errors.New("database: a wikidata fetch is already active")

// ErrWikidataEmptyChunk is a chunk without a row.
var ErrWikidataEmptyChunk = errors.New("database: the wikidata chunk has no rows")

// ErrWikidataLeaseLost is a fetch another worker took over.
var ErrWikidataLeaseLost = errors.New("database: the wikidata fetch lease was lost")

func (e *WikidataExtract) withTotal() *WikidataExtract {
	e.ChunksTotal = len(wikidata.Chunks())
	return e
}

// StartWikidataFetch records a fetch from source: the newest extract again
// when its fetch failed (it goes on from the chunks it has), a new one
// otherwise. A fetch already running refuses it.
func StartWikidataFetch(ctx context.Context, db *pg.DB, actor int64, source string) (*WikidataExtract, error) {
	var by interface{}
	if actor > 0 {
		by = actor
	}
	var out []WikidataExtract
	err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		out = nil
		var newest []WikidataExtract
		if _, err := tx.QueryContext(ctx, &newest, `SELECT `+wikidataExtractColumns+`
			FROM public.wikidata_extract e ORDER BY e.id DESC LIMIT 1 FOR UPDATE`); err != nil {
			return err
		}
		switch {
		case len(newest) > 0 && newest[0].Status == WikidataFetching:
			resume, err := takeOverStaleFetch(ctx, tx, &newest[0], source)
			if err != nil || resume {
				out = newest
				return err
			}
		case len(newest) > 0 && newest[0].Status == WikidataFailed && newest[0].Source == source:
			_, err := tx.QueryContext(ctx, &out, `
				UPDATE public.wikidata_extract e SET status = 'fetching', last_error = NULL, finished_at = NULL,
					lease_owner = NULL, lease_expires_at = NULL
				WHERE e.id = ? RETURNING `+wikidataExtractColumns, newest[0].ID)
			return err
		}
		_, err := tx.QueryContext(ctx, &out, `
			INSERT INTO public.wikidata_extract AS e (source, created_by_user_id) VALUES (?, ?)
			ON CONFLICT ((true)) WHERE status = 'fetching' DO NOTHING
			RETURNING `+wikidataExtractColumns, source, by)
		return err
	})
	if err != nil {
		if errors.Is(err, ErrWikidataFetchActive) {
			return nil, err
		}
		var pgErr pg.Error
		if errors.As(err, &pgErr) && pgErr.Field('C') == "23505" {
			return nil, ErrWikidataFetchActive
		}
		return nil, fmt.Errorf("starting a wikidata fetch: %w", err)
	}
	if len(out) == 0 {
		return nil, ErrWikidataFetchActive
	}
	return out[0].withTotal(), nil
}

// wikidataStaleStart is how long a fetch nobody ever claimed counts as
// running.
const wikidataStaleStart = 15 * time.Minute

// takeOverStaleFetch decides a start beside a running fetch: one whose
// worker still holds it (or that was started a moment ago) refuses the
// start; one whose worker stopped is resumed by a start from its own source,
// and failed by a start from another — the chunks of one extract always come
// from one source.
func takeOverStaleFetch(ctx context.Context, tx *pg.Tx, ext *WikidataExtract, source string) (resume bool, err error) {
	var stale bool
	if _, err = tx.QueryOneContext(ctx, pg.Scan(&stale), `
		SELECT coalesce(lease_expires_at < now(),
			lease_owner IS NULL AND started_at < now() - ? * interval '1 millisecond')
		FROM public.wikidata_extract WHERE id = ?`, wikidataStaleStart.Milliseconds(), ext.ID); err != nil {
		return false, err
	}
	switch {
	case !stale:
		return false, ErrWikidataFetchActive
	case ext.Source == source:
		return true, nil
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE public.wikidata_extract SET status = 'failed', last_error = 'its worker stopped; replaced by a fetch from another source',
			finished_at = now(), lease_owner = NULL, lease_expires_at = NULL
		WHERE id = ?`, ext.ID)
	return false, err
}

// ClaimWikidataFetch takes the running fetch of source for owner for lease:
// one nobody holds, one whose lease ran out, or one owner holds already. A
// fetch of another source is never taken: its bytes and its provenance are
// that source's. None is nil.
func ClaimWikidataFetch(
	ctx context.Context, db pg.DBI, owner uuid.UUID, lease time.Duration, source string,
) (*WikidataExtract, error) {
	var out []WikidataExtract
	if _, err := db.QueryContext(ctx, &out, `
		UPDATE public.wikidata_extract e SET lease_owner = ?0, lease_expires_at = now() + ?1 * interval '1 millisecond'
		WHERE e.id = (
			SELECT id FROM public.wikidata_extract
			WHERE status = 'fetching' AND source = ?2
				AND (lease_owner IS NULL OR lease_expires_at < now() OR lease_owner = ?0)
			FOR UPDATE SKIP LOCKED)
		RETURNING `+wikidataExtractColumns, owner, lease.Milliseconds(), source); err != nil {
		return nil, fmt.Errorf("claiming the wikidata fetch: %w", err)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out[0].withTotal(), nil
}

// WikidataChunksDone are the chunks an extract has.
func WikidataChunksDone(ctx context.Context, db pg.DBI, extractID int64) (map[string]bool, error) {
	var names []string
	if _, err := db.QueryContext(ctx, &names, `
		SELECT name FROM public.wikidata_extract_chunk WHERE extract_id = ?`, extractID); err != nil {
		return nil, fmt.Errorf("reading the wikidata chunks: %w", err)
	}
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out, nil
}

// lockWikidataFetch locks owner's fetch in tx and renews the lease.
func lockWikidataFetch(ctx context.Context, tx *pg.Tx, extractID int64, owner uuid.UUID, lease time.Duration) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE public.wikidata_extract SET lease_expires_at = now() + ? * interval '1 millisecond'
		WHERE id = ? AND status = 'fetching' AND lease_owner = ?`, lease.Milliseconds(), extractID, owner)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return ErrWikidataLeaseLost
	}
	return nil
}

// wikidataStatementTimeout bounds every statement of an import: a chunk is
// a few hundred thousand rows.
const wikidataStatementTimeout = "10min"

// ImportWikidataChunk writes one chunk of owner's fetch from r — its SPARQL
// TSV — with its record, in one transaction: the rows stream through COPY,
// never all in memory, and are keyed by public.search_normalize, the
// function the entity names are keyed by.
func ImportWikidataChunk(
	ctx context.Context, db *pg.DB, extractID int64, owner uuid.UUID, lease time.Duration, c wikidata.Chunk, r io.Reader,
) (wikidata.Parsed, error) {
	var parsed wikidata.Parsed
	err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT set_config('statement_timeout', ?, true)`,
			wikidataStatementTimeout); err != nil {
			return err
		}
		if err := lockWikidataFetch(ctx, tx, extractID, owner, lease); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE wikidata_stage
			(qid text, born integer, died integer, name text, work text) ON COMMIT DROP`); err != nil {
			return err
		}
		var err error
		if parsed, err = copyChunk(tx, c, r); err != nil {
			return err
		}
		if parsed.Rows == 0 {
			// Every chunk of the extract has rows: an empty one is a
			// response that is not the query's.
			return ErrWikidataEmptyChunk
		}
		if insertErr := insertChunk(ctx, tx, extractID, c); insertErr != nil {
			return insertErr
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO public.wikidata_extract_chunk (extract_id, name, query, rows, skipped, sha256)
			VALUES (?, ?, ?, ?, ?, ?)`, extractID, c.Name, c.Query, parsed.Rows, parsed.Skipped, parsed.SHA256)
		return err
	})
	if err != nil {
		return parsed, fmt.Errorf("importing wikidata chunk %s: %w", c.Name, err)
	}
	return parsed, nil
}

// copyChunk streams the parsed rows into the stage table.
func copyChunk(tx *pg.Tx, c wikidata.Chunk, r io.Reader) (wikidata.Parsed, error) {
	pr, pw := io.Pipe()
	type result struct {
		parsed wikidata.Parsed
		err    error
	}
	done := make(chan result, 1)
	go func() {
		var line strings.Builder
		p, err := wikidata.Parse(c, r, func(row wikidata.Row) error {
			line.Reset()
			line.WriteString(copyField(row.QID))
			line.WriteByte('\t')
			line.WriteString(copyInt(row.Born))
			line.WriteByte('\t')
			line.WriteString(copyInt(row.Died))
			line.WriteByte('\t')
			line.WriteString(copyField(row.Name))
			line.WriteByte('\t')
			line.WriteString(copyField(row.Work))
			line.WriteByte('\n')
			_, werr := io.WriteString(pw, line.String())
			return werr
		})
		_ = pw.CloseWithError(err)
		done <- result{p, err}
	}()
	_, copyErr := tx.CopyFrom(pr, `COPY wikidata_stage (qid, born, died, name, work) FROM STDIN`)
	// A failed COPY stops reading: the parser's next write fails, and it
	// returns.
	_ = pr.CloseWithError(errors.New("copy ended"))
	res := <-done
	if copyErr != nil {
		return res.parsed, copyErr
	}
	return res.parsed, res.err
}

// copyNull is NULL in COPY's text format.
const copyNull = `\N`

// copyField is a value in COPY's text format; empty is NULL.
func copyField(s string) string {
	if s == "" {
		return copyNull
	}
	return copyEscaper.Replace(s)
}

var copyEscaper = strings.NewReplacer(`\`, `\\`, "\t", `\t`, "\n", `\n`, "\r", `\r`)

func copyInt(n int) string {
	if n == 0 {
		return copyNull
	}
	return strconv.Itoa(n)
}

// insertChunk moves the stage into the chunk's table; a row it already has
// (two names of one key) is one row.
func insertChunk(ctx context.Context, tx *pg.Tx, extractID int64, c wikidata.Chunk) error {
	var err error
	switch c.Kind {
	case wikidata.KindPeople:
		_, err = tx.ExecContext(ctx, `
			INSERT INTO public.wikidata_person (extract_id, qid, born, died)
			SELECT ?, qid, born, died FROM wikidata_stage
			ON CONFLICT DO NOTHING`, extractID)
	case wikidata.KindWorks:
		_, err = tx.ExecContext(ctx, `
			INSERT INTO public.wikidata_work (extract_id, qid, work_qid, title_key, title)
			SELECT ?, qid, work, k, name FROM (SELECT qid, work, name, public.search_normalize(name) AS k FROM wikidata_stage) s
			WHERE k <> ''
			ON CONFLICT DO NOTHING`, extractID)
	default:
		_, err = tx.ExecContext(ctx, `
			INSERT INTO public.wikidata_name (extract_id, key, qid, kind, lang, label)
			SELECT ?0, k, qid, ?1, ?2, CASE WHEN ?1 = 'label' THEN name END
			FROM (SELECT qid, name, public.search_normalize(name) AS k FROM wikidata_stage) s
			WHERE k <> ''
			ON CONFLICT DO NOTHING`, extractID, c.Kind, c.Lang)
	}
	return err
}

// PublishWikidataExtract ends owner's fetch once every chunk is in: it is
// published under its content ID and replaces the published extract, or —
// when the published extract has the same content — it is a duplicate and
// the published one stays. The status it ended in is returned; the rows of
// the extracts that are not published are deleted by PruneWikidataExtracts.
func PublishWikidataExtract(
	ctx context.Context, db *pg.DB, extractID int64, owner uuid.UUID, lease time.Duration,
) (string, error) {
	status := ""
	err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if err := lockWikidataFetch(ctx, tx, extractID, owner, lease); err != nil {
			return err
		}
		m, err := wikidataManifest(ctx, tx, extractID)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(m)
		if err != nil {
			return err
		}
		content := m.ContentID()
		var same int
		if _, err = tx.QueryOneContext(ctx, pg.Scan(&same), `
			SELECT count(*) FROM public.wikidata_extract WHERE status = 'published' AND content_id = ?`,
			content); err != nil {
			return err
		}
		status = WikidataPublished
		if same > 0 {
			status = WikidataDuplicate
		} else if _, err = tx.ExecContext(ctx, `
			UPDATE public.wikidata_extract SET status = 'superseded' WHERE status = 'published'`); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE public.wikidata_extract e SET status = ?, content_id = ?, manifest = ?::jsonb,
				people = (SELECT count(*) FROM public.wikidata_person WHERE extract_id = e.id),
				names = (SELECT count(*) FROM public.wikidata_name WHERE extract_id = e.id),
				works = (SELECT count(DISTINCT (qid, work_qid)) FROM public.wikidata_work WHERE extract_id = e.id),
				finished_at = now(), lease_owner = NULL, lease_expires_at = NULL
			WHERE id = ?`, status, content, string(raw), extractID)
		return err
	})
	if err != nil {
		return "", fmt.Errorf("publishing the wikidata extract: %w", err)
	}
	return status, nil
}

// wikidataManifest is an extract's manifest from its chunks, in the order
// of wikidata.Chunks; a chunk missing is wikidata.ErrIncomplete.
func wikidataManifest(ctx context.Context, tx *pg.Tx, extractID int64) (*wikidata.Manifest, error) {
	var chunks []struct {
		Name    string `pg:"name"`
		Query   string `pg:"query"`
		Rows    int    `pg:"rows"`
		Skipped int    `pg:"skipped"`
		SHA256  string `pg:"sha256"`
	}
	if _, err := tx.QueryContext(ctx, &chunks, `
		SELECT name, query, rows, skipped, sha256 FROM public.wikidata_extract_chunk WHERE extract_id = ?`,
		extractID); err != nil {
		return nil, err
	}
	byName := map[string]int{}
	for i := range chunks {
		byName[chunks[i].Name] = i
	}
	var started time.Time
	var source string
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&started, &source), `
		SELECT started_at, source FROM public.wikidata_extract WHERE id = ?`, extractID); err != nil {
		return nil, err
	}
	m := &wikidata.Manifest{Source: source, Endpoint: source, ExtractedAt: started.UTC()}
	for _, c := range wikidata.Chunks() {
		i, ok := byName[c.Name]
		if !ok {
			return nil, wikidata.ErrIncomplete
		}
		ch := chunks[i]
		m.Chunks = append(m.Chunks, wikidata.ChunkManifest{Name: ch.Name, Query: ch.Query, Rows: ch.Rows,
			Skipped: ch.Skipped, SHA256: ch.SHA256})
	}
	return m, nil
}

// FailWikidataExtract ends owner's fetch as failed with a message that
// names no data: the chunks written stay for the next start.
func FailWikidataExtract(ctx context.Context, db pg.DBI, extractID int64, owner uuid.UUID, message string) error {
	if len(message) > maxWikidataError {
		message = message[:maxWikidataError]
	}
	_, err := db.ExecContext(ctx, `
		UPDATE public.wikidata_extract SET status = 'failed', last_error = ?, finished_at = now(),
			lease_owner = NULL, lease_expires_at = NULL
		WHERE id = ? AND status = 'fetching' AND lease_owner = ?`, strings.ToValidUTF8(message, ""), extractID, owner)
	if err != nil {
		return fmt.Errorf("failing the wikidata fetch: %w", err)
	}
	return nil
}

// maxWikidataError is the longest error message stored (migration 60).
const maxWikidataError = 500

// wikidataPruneBatch is how many rows one delete of PruneWikidataExtracts
// takes: a short transaction each.
const wikidataPruneBatch = 50000

// PruneWikidataExtracts deletes the rows of every extract nothing reads or
// resumes, a batch at a time, and returns how many it deleted: superseded and
// duplicate extracts, and failed ones a start can no longer resume — every
// failed extract but the newest, which it first marks abandoned with its
// chunk records gone, in one transaction, so no record ever stands for rows
// that are not there. The newest failed extract keeps its rows for the next
// start. The extracts' records stay.
func PruneWikidataExtracts(ctx context.Context, db *pg.DB) (int, error) {
	if err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		_, err := tx.ExecContext(ctx, `
			WITH gone AS (
				UPDATE public.wikidata_extract SET status = 'abandoned', finished_at = coalesce(finished_at, now())
				WHERE status = 'failed' AND id < (SELECT max(id) FROM public.wikidata_extract)
				RETURNING id)
			DELETE FROM public.wikidata_extract_chunk WHERE extract_id IN (SELECT id FROM gone)`)
		return err
	}); err != nil {
		return 0, fmt.Errorf("abandoning the failed wikidata extracts: %w", err)
	}
	total := 0
	for _, table := range []string{"wikidata_name", "wikidata_person", "wikidata_work"} {
		for {
			res, err := db.ExecContext(ctx, `
				DELETE FROM public.`+table+` WHERE ctid = ANY(ARRAY(
					SELECT t.ctid FROM public.`+table+` t
					JOIN public.wikidata_extract e ON e.id = t.extract_id
					WHERE e.status IN ('duplicate', 'superseded', 'abandoned') LIMIT ?))`, wikidataPruneBatch)
			if err != nil {
				return total, fmt.Errorf("pruning the wikidata extracts: %w", err)
			}
			total += res.RowsAffected()
			if res.RowsAffected() < wikidataPruneBatch {
				break
			}
		}
	}
	return total, nil
}

// LatestWikidataExtracts are the newest extract and the published one
// (the same when the newest is published); either may be nil.
func LatestWikidataExtracts(ctx context.Context, db pg.DBI) (latest, published *WikidataExtract, err error) {
	var rows []WikidataExtract
	if _, err = db.QueryContext(ctx, &rows, `
		(SELECT `+wikidataExtractColumns+` FROM public.wikidata_extract e ORDER BY e.id DESC LIMIT 1)
		UNION ALL
		(SELECT `+wikidataExtractColumns+` FROM public.wikidata_extract e WHERE e.status = 'published')`); err != nil {
		return nil, nil, fmt.Errorf("reading the wikidata extracts: %w", err)
	}
	for i := range rows {
		r := rows[i].withTotal()
		if latest == nil {
			latest = r
			if r.Status == WikidataPublished {
				published = r
				break
			}
			continue
		}
		published = r
	}
	return latest, published, nil
}

// wikidataLoadPage is how many names or keys one read of the reference
// takes.
const wikidataLoadPage = 20000

// LoadWikidataReference reads the published extract as the build reads it,
// for the credits of in: their names' keys, the items those keys reach, the
// items' labels and the books titled like a notable work of one of them.
// No published extract is nil.
func LoadWikidataReference(ctx context.Context, db pg.DBI, in *authorentity.Input) (*authorentity.Reference, error) {
	var ext []WikidataExtract
	if _, err := db.QueryContext(ctx, &ext, `SELECT `+wikidataExtractColumns+`
		FROM public.wikidata_extract e WHERE e.status = 'published'`); err != nil {
		return nil, fmt.Errorf("reading the published wikidata extract: %w", err)
	}
	if len(ext) == 0 || ext[0].ContentID == nil {
		return nil, nil
	}
	id := ext[0].ID
	ref := &authorentity.Reference{Extract: *ext[0].ContentID, NameKey: map[string]string{},
		KeyItems: map[string][]authorentity.ItemName{}, Items: map[string]*authorentity.Item{}}
	if err := loadWikidataNameKeys(ctx, db, in, ref); err != nil {
		return nil, err
	}
	if err := loadWikidataKeyItems(ctx, db, id, ref); err != nil {
		return nil, err
	}
	if err := loadWikidataItems(ctx, db, id, ref); err != nil {
		return nil, err
	}
	// The rows of an extract are deleted only once another is published: an
	// extract still published was read whole.
	var status string
	if _, err := db.QueryOneContext(ctx, pg.Scan(&status), `
		SELECT status FROM public.wikidata_extract WHERE id = ?`, id); err != nil {
		return nil, fmt.Errorf("rereading the wikidata extract: %w", err)
	}
	if status != WikidataPublished {
		return nil, ErrWikidataReplaced
	}
	return ref, nil
}

// ErrWikidataReplaced is a reference another extract replaced while it was
// read: the build's attempt fails and is planned again.
var ErrWikidataReplaced = errors.New("database: the wikidata extract was replaced while it was read")

// loadWikidataNameKeys keys every display name of the credits: the rules
// read no other name.
func loadWikidataNameKeys(ctx context.Context, db pg.DBI, in *authorentity.Input, ref *authorentity.Reference) error {
	seen := map[string]bool{}
	var names []string
	for i := range in.Credits {
		if n := in.Credits[i].DisplayName; n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for start := 0; start < len(names); start += wikidataLoadPage {
		page := names[start:min(start+wikidataLoadPage, len(names))]
		var rows []struct {
			Name string `pg:"name"`
			Key  string `pg:"key"`
		}
		if _, err := db.QueryContext(ctx, &rows, `
			SELECT n AS name, public.search_normalize(n) AS key FROM unnest(?::text[]) AS n`, pg.Array(page)); err != nil {
			return fmt.Errorf("keying the credit names: %w", err)
		}
		for _, r := range rows {
			if r.Key != "" {
				ref.NameKey[r.Name] = r.Key
			}
		}
	}
	return nil
}

// loadWikidataKeyItems reads the items every name key reaches.
func loadWikidataKeyItems(ctx context.Context, db pg.DBI, extractID int64, ref *authorentity.Reference) error {
	seen := map[string]bool{}
	var keys []string
	for _, k := range ref.NameKey {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for start := 0; start < len(keys); start += wikidataLoadPage {
		page := keys[start:min(start+wikidataLoadPage, len(keys))]
		var rows []struct {
			Key  string `pg:"key"`
			QID  string `pg:"qid"`
			Kind string `pg:"kind"`
		}
		if _, err := db.QueryContext(ctx, &rows, `
			SELECT DISTINCT key, qid, kind FROM public.wikidata_name
			WHERE extract_id = ? AND key = ANY(?::text[])
			ORDER BY key, qid, kind`, extractID, pg.Array(page)); err != nil {
			return fmt.Errorf("reading the wikidata names: %w", err)
		}
		for _, r := range rows {
			ref.KeyItems[r.Key] = append(ref.KeyItems[r.Key], authorentity.ItemName{QID: r.QID, Kind: r.Kind})
		}
	}
	return nil
}

// loadWikidataItems reads the labels and pseudonyms of every item reached:
// the Russian, English and "mul" labels are the name the item goes by.
func loadWikidataItems(ctx context.Context, db pg.DBI, extractID int64, ref *authorentity.Reference) error {
	var qids []string
	for _, names := range ref.KeyItems {
		for _, n := range names {
			if ref.Items[n.QID] == nil {
				ref.Items[n.QID] = &authorentity.Item{}
				qids = append(qids, n.QID)
			}
		}
	}
	for start := 0; start < len(qids); start += wikidataLoadPage {
		page := qids[start:min(start+wikidataLoadPage, len(qids))]
		var rows []struct {
			QID   string  `pg:"qid"`
			Key   string  `pg:"key"`
			Kind  string  `pg:"kind"`
			Lang  string  `pg:"lang"`
			Label *string `pg:"label"`
		}
		if _, err := db.QueryContext(ctx, &rows, `
			SELECT qid, key, kind, lang, label FROM public.wikidata_name
			WHERE extract_id = ? AND kind IN ('label', 'pseudonym') AND qid = ANY(?::text[])
			ORDER BY qid, kind, key, lang`, extractID, pg.Array(page)); err != nil {
			return fmt.Errorf("reading the wikidata labels: %w", err)
		}
		for _, r := range rows {
			addItemName(ref.Items[r.QID], r.Key, r.Kind, r.Lang, r.Label)
		}
	}
	return nil
}

// mainLabelLangs are the languages of the name an item goes by.
var mainLabelLangs = []string{wikidata.LangRussian, wikidata.LangEnglish, "mul"}

// addItemName adds one label or pseudonym row to an item.
func addItemName(it *authorentity.Item, key, kind, lang string, label *string) {
	if kind == authorentity.NamePseudonym {
		if !slices.Contains(it.PseudonymKeys, key) {
			it.PseudonymKeys = append(it.PseudonymKeys, key)
		}
		return
	}
	if !slices.Contains(it.LabelKeys, key) {
		it.LabelKeys = append(it.LabelKeys, key)
	}
	if slices.Contains(mainLabelLangs, lang) && !slices.Contains(it.MainLabelKeys, key) {
		it.MainLabelKeys = append(it.MainLabelKeys, key)
	}
	// The Russian label is shown, else the English one.
	if label != nil && (lang == wikidata.LangRussian || (it.Label == "" && lang == wikidata.LangEnglish)) {
		it.Label = *label
	}
}
