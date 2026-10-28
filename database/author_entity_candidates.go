package database

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"gopds-api/internal/authorentity"

	"github.com/go-pg/pg/v10"
)

// The merge candidates of an entity pass (migration 63, plan E4a): the card
// fields of the sample books the build reads after choosing the samples,
// and the candidates it writes as a diff in its completing transaction.

// mergeCardPage is how many books one card-field read asks for.
const mergeCardPage = 5000

// mergeCardBookRow is one sample book's card fields.
type mergeCardBookRow struct {
	ID         int64    `pg:"id"`
	Year       string   `pg:"year"`
	Lang       string   `pg:"lang"`
	Genres     []string `pg:"genres,array"`
	Series     []string `pg:"series,array"`
	Annotation string   `pg:"annotation"`
}

// mergeCardBooksQuery reads the card fields of authorentity.MergeCardSources
// (the title the build reads with the books): the year of the current
// snapshot else the docdate, the language, the genre codes, the series
// names, the annotation already cut.
const mergeCardBooksQuery = `
SELECT b.id, coalesce(nullif(s.source_year, ''), b.docdate) AS year, b.lang, left(b.annotation, ?) AS annotation,
	coalesce((SELECT array_agg(g.genre ORDER BY g.genre) FROM public.opds_catalog_bgenre bg
		JOIN public.opds_catalog_genre g ON g.id = bg.genre_id WHERE bg.book_id = b.id), '{}') AS genres,
	coalesce((SELECT array_agg(se.ser ORDER BY se.ser) FROM public.opds_catalog_bseries bs
		JOIN public.opds_catalog_series se ON se.id = bs.ser_id WHERE bs.book_id = b.id), '{}') AS series
FROM public.opds_catalog_book b
LEFT JOIN public.book_metadata_snapshot s ON s.book_id = b.id AND s.is_current
WHERE b.id = ANY(?::bigint[])`

// mergeCardAnnotationRunes is where the read cuts an annotation: the card's
// own cut, so no longer text is held.
const mergeCardAnnotationRunes = 300

// LoadMergeCardBooks reads the card fields of the books, a page at a time.
func LoadMergeCardBooks(ctx context.Context, db pg.DBI, ids []int64) (map[int64]authorentity.CardBookFields, error) {
	out := make(map[int64]authorentity.CardBookFields, len(ids))
	for start := 0; start < len(ids); start += mergeCardPage {
		page := ids[start:min(start+mergeCardPage, len(ids))]
		var rows []mergeCardBookRow
		if _, err := db.QueryContext(ctx, &rows, mergeCardBooksQuery, mergeCardAnnotationRunes, pg.Array(page)); err != nil {
			return nil, fmt.Errorf("reading the card fields of the sample books: %w", err)
		}
		for _, r := range rows {
			out[r.ID] = authorentity.CardBookFields{Year: r.Year, Lang: r.Lang, Genres: r.Genres, Series: r.Series,
				Annotation: r.Annotation}
		}
	}
	return out, nil
}

// MergeCandidateDiff is what a pass changed in the stored census: the
// candidates and member rows inserted, the candidates deleted (their members
// go with them), the ones kept as they were, the sample rows replaced, and
// all the rows those statements wrote. An unchanged pass writes none.
type MergeCandidateDiff struct {
	Inserted        int `json:"inserted"`
	Deleted         int `json:"deleted"`
	Kept            int `json:"kept"`
	MembersInserted int `json:"members_inserted"`
	SampleInserted  int `json:"sample_inserted"`
	SampleDeleted   int `json:"sample_deleted"`
	RowsWritten     int `json:"rows_written"`
}

// mergeCensusLock serializes the census writers ("E4CENSUS"): a census is
// written after its pass has committed, so two passes' censuses could
// otherwise interleave or land out of order.
const mergeCensusLock int64 = 0x453443454e535553

// ErrMergeCensusSuperseded is the census of a pass that a later pass has
// completed after: the newer pass's census is the current one.
var ErrMergeCensusSuperseded = errors.New("a later entity pass completed: its census is the current one")

// LockMergeCensus takes the census lock for the rest of tx and refuses a
// pass that a later completed pass has superseded. Run ids grow with their
// start and one pass is active at a time, so a later completed run is a
// newer census than this one.
func LockMergeCensus(ctx context.Context, tx pg.DBI, runID int64) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(?)`, mergeCensusLock); err != nil {
		return fmt.Errorf("taking the census lock: %w", err)
	}
	var later bool
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&later), `SELECT EXISTS (SELECT 1 FROM public.author_entity_build_run
		WHERE status = 'completed' AND id > ?)`, runID); err != nil {
		return fmt.Errorf("looking for a later completed pass: %w", err)
	}
	if later {
		return ErrMergeCensusSuperseded
	}
	return nil
}

// mergeCandidatePage is how many candidates one statement writes.
const mergeCandidatePage = 2000

// WriteMergeCandidates writes the pass's census as a diff against the stored
// one: new digests are inserted with their members, gone ones deleted, and
// the sample rows that changed replaced. A kept row is not touched.
func WriteMergeCandidates(ctx context.Context, tx pg.DBI, runID int64, plan *authorentity.Plan) (MergeCandidateDiff, error) {
	var diff MergeCandidateDiff
	var stored []string
	if _, err := tx.QueryContext(ctx, pg.Scan(pg.Array(&stored)), `
		SELECT coalesce(array_agg(encode(digest, 'hex')), '{}') FROM public.author_entity_merge_candidate`); err != nil {
		return diff, fmt.Errorf("reading the stored merge candidates: %w", err)
	}
	want := map[string]bool{}
	var fresh []authorentity.MergeCandidate
	have := make(map[string]bool, len(stored))
	for _, d := range stored {
		have[d] = true
	}
	cands := plan.MergeCandidates()
	for i := range cands {
		c := &cands[i]
		d := hex.EncodeToString(c.Digest[:])
		want[d] = true
		if have[d] {
			diff.Kept++
		} else {
			fresh = append(fresh, *c)
		}
	}
	var gone []string
	for _, d := range stored {
		if !want[d] {
			gone = append(gone, d)
		}
	}
	for start := 0; start < len(gone); start += mergeCandidatePage {
		if _, err := tx.ExecContext(ctx, `DELETE FROM public.author_entity_merge_candidate
			WHERE digest = ANY(SELECT decode(d, 'hex') FROM unnest(?::text[]) AS d)`,
			pg.Array(gone[start:min(start+mergeCandidatePage, len(gone))])); err != nil {
			return diff, fmt.Errorf("deleting the gone merge candidates: %w", err)
		}
	}
	for start := 0; start < len(fresh); start += mergeCandidatePage {
		members, insertErr := insertMergeCandidates(ctx, tx, runID, fresh[start:min(start+mergeCandidatePage, len(fresh))])
		if insertErr != nil {
			return diff, insertErr
		}
		diff.MembersInserted += members
	}
	diff.Inserted, diff.Deleted = len(fresh), len(gone)
	var err error
	diff.SampleInserted, diff.SampleDeleted, err = writeMergeCandidateSample(ctx, tx, runID, plan.MergeCandidateSample())
	diff.RowsWritten = diff.Inserted + diff.Deleted + diff.MembersInserted + diff.SampleInserted + diff.SampleDeleted
	return diff, err
}

// insertMergeCandidates inserts new candidates with their members.
func insertMergeCandidates(ctx context.Context, tx pg.DBI, runID int64, cands []authorentity.MergeCandidate) (int, error) {
	var digest, kind, script, item, sideA, sideB, members, input []string
	var credit []int64
	var ordinal []int
	var mDigest, mSide, mFP, mVersion []string
	var mOrdinal []int
	for i := range cands {
		c := &cands[i]
		d := hex.EncodeToString(c.Digest[:])
		digest = append(digest, d)
		kind, script, item = append(kind, c.Kind), append(script, c.Script), append(item, c.ItemKind)
		sideA, sideB = append(sideA, c.SideA.String()), append(sideB, c.SideB.String())
		members = append(members, hex.EncodeToString(c.MembersSHA256[:]))
		input = append(input, hex.EncodeToString(c.InputSHA256[:]))
		// A pair's credit columns are NULL: the statement reads them for a
		// book item only.
		credit, ordinal = append(credit, c.CreditID), append(ordinal, c.CreditOrdinal)
		for side, keys := range map[string][]authorentity.FPKey{"a": c.MembersA, "b": c.MembersB} {
			for _, k := range keys {
				mDigest, mSide = append(mDigest, d), append(mSide, side)
				mFP, mVersion = append(mFP, hex.EncodeToString([]byte(k.Fingerprint))), append(mVersion, k.ExtractorVersion)
				mOrdinal = append(mOrdinal, k.Ordinal)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO public.author_entity_merge_candidate (digest, kind, script, item_kind, side_a_entity, side_b_entity,
			credit_id, credit_ordinal, members_sha256, input_sha256, first_build_run_id)
		SELECT decode(d, 'hex'), k, s, i, a, b, CASE WHEN i = 'book' THEN c END, CASE WHEN i = 'book' THEN o END,
			decode(m, 'hex'), decode(n, 'hex'), ?
		FROM unnest(?::text[], ?::text[], ?::text[], ?::text[], ?::uuid[], ?::uuid[], ?::bigint[], ?::smallint[],
			?::text[], ?::text[]) AS u(d, k, s, i, a, b, c, o, m, n)`,
		runID, pg.Array(digest), pg.Array(kind), pg.Array(script), pg.Array(item), pg.Array(sideA),
		pg.Array(sideB), pg.Array(credit), pg.Array(ordinal), pg.Array(members), pg.Array(input)); err != nil {
		return 0, fmt.Errorf("inserting the new merge candidates: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO public.author_entity_merge_candidate_member (digest, side, source_fingerprint, extractor_version, ordinal)
		SELECT decode(d, 'hex'), s, decode(f, 'hex'), v, o
		FROM unnest(?::text[], ?::text[], ?::text[], ?::text[], ?::smallint[]) AS u(d, s, f, v, o)`,
		pg.Array(mDigest), pg.Array(mSide), pg.Array(mFP), pg.Array(mVersion), pg.Array(mOrdinal)); err != nil {
		return 0, fmt.Errorf("inserting the merge candidates' members: %w", err)
	}
	return len(mDigest), nil
}

// keySep joins a side's Go keys into one array element; no key holds it.
const keySep = "\x1f"

// sampleRow is one sample row as the statements read and write it: its key
// and the canonical rest, compared to decide whether it changed.
type sampleRow struct {
	Kind    string `pg:"kind"`
	Script  string `pg:"script"`
	Reason  string `pg:"reason"`
	Members string `pg:"members"`
	EntityA string `pg:"entity_a"`
	EntityB string `pg:"entity_b"`
	Credit  int64  `pg:"credit"`
	KeysA   string `pg:"keys_a"`
	KeysB   string `pg:"keys_b"`
	Digest  string `pg:"digest"`
}

func (r *sampleRow) key() string { return r.Kind + "|" + r.Script + "|" + r.Reason + "|" + r.Members }

// writeMergeCandidateSample replaces the stored sample rows that are not the
// pass's, as they are, with the pass's: a row the pass keeps unchanged is
// not touched.
func writeMergeCandidateSample(
	ctx context.Context, tx pg.DBI, runID int64, rows []authorentity.MergeCandidateSample,
) (inserted, deleted int, err error) {
	var stored []sampleRow
	if _, err = tx.QueryContext(ctx, &stored, `
		SELECT kind, script, reason, encode(members_sha256, 'hex') AS members, entity_a::text, entity_b::text,
			coalesce(credit_id, 0) AS credit, array_to_string(go_keys_a, ?) AS keys_a,
			array_to_string(go_keys_b, ?) AS keys_b, coalesce(encode(digest, 'hex'), '') AS digest
		FROM public.author_entity_merge_candidate_sample`, keySep, keySep); err != nil {
		return 0, 0, fmt.Errorf("reading the stored merge candidate sample: %w", err)
	}
	want := make(map[string]sampleRow, len(rows))
	for i := range rows {
		r := &rows[i]
		row := sampleRow{Kind: r.Kind, Script: r.Script, Reason: r.Reason, Members: hex.EncodeToString(r.MembersSHA256[:]),
			EntityA: r.EntityA.String(), EntityB: r.EntityB.String(), Credit: r.CreditID,
			KeysA: strings.Join(r.KeysA, keySep), KeysB: strings.Join(r.KeysB, keySep)}
		if r.Reason == authorentity.SampleCandidate {
			row.Digest = hex.EncodeToString(r.Digest[:])
		}
		want[row.key()] = row
	}
	var drop, add []sampleRow
	have := make(map[string]bool, len(stored))
	for i := range stored {
		s := &stored[i]
		if w, ok := want[s.key()]; ok && w == *s {
			have[s.key()] = true
			continue
		}
		drop = append(drop, *s)
	}
	for _, k := range sortedRowKeys(want) {
		if !have[k] {
			add = append(add, want[k])
		}
	}
	if dropErr := deleteSampleRows(ctx, tx, drop); dropErr != nil {
		return 0, 0, dropErr
	}
	if addErr := insertSampleRows(ctx, tx, runID, add); addErr != nil {
		return 0, 0, addErr
	}
	return len(add), len(drop), nil
}

// deleteSampleRows deletes the sample rows by their keys.
func deleteSampleRows(ctx context.Context, tx pg.DBI, drop []sampleRow) error {
	if len(drop) == 0 {
		return nil
	}
	var kind, script, reason, members []string
	for i := range drop {
		kind, script = append(kind, drop[i].Kind), append(script, drop[i].Script)
		reason, members = append(reason, drop[i].Reason), append(members, drop[i].Members)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM public.author_entity_merge_candidate_sample s
		USING unnest(?::text[], ?::text[], ?::text[], ?::text[]) AS d(k, sc, r, m)
		WHERE s.kind = d.k AND s.script = d.sc AND s.reason = d.r AND s.members_sha256 = decode(d.m, 'hex')`,
		pg.Array(kind), pg.Array(script), pg.Array(reason), pg.Array(members)); err != nil {
		return fmt.Errorf("deleting the changed merge candidate sample rows: %w", err)
	}
	return nil
}

// insertSampleRows inserts the sample rows the pass writes.
func insertSampleRows(ctx context.Context, tx pg.DBI, runID int64, add []sampleRow) error {
	if len(add) == 0 {
		return nil
	}
	var kind, script, reason, members, entityA, entityB, keysA, keysB, digest []string
	var credit []int64
	for i := range add {
		r := &add[i]
		kind, script, reason = append(kind, r.Kind), append(script, r.Script), append(reason, r.Reason)
		members, entityA, entityB = append(members, r.Members), append(entityA, r.EntityA), append(entityB, r.EntityB)
		credit, keysA, keysB = append(credit, r.Credit), append(keysA, r.KeysA), append(keysB, r.KeysB)
		digest = append(digest, r.Digest)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO public.author_entity_merge_candidate_sample (build_run_id, kind, script, reason, members_sha256,
			entity_a, entity_b, credit_id, go_keys_a, go_keys_b, digest)
		SELECT ?, k, sc, r, decode(m, 'hex'), a, b, nullif(c, 0), string_to_array(ka, ?), string_to_array(kb, ?),
			decode(nullif(d, ''), 'hex')
		FROM unnest(?::text[], ?::text[], ?::text[], ?::text[], ?::uuid[], ?::uuid[], ?::bigint[], ?::text[],
			?::text[], ?::text[]) AS u(k, sc, r, m, a, b, c, ka, kb, d)`,
		runID, keySep, keySep, pg.Array(kind), pg.Array(script), pg.Array(reason), pg.Array(members),
		pg.Array(entityA), pg.Array(entityB), pg.Array(credit), pg.Array(keysA), pg.Array(keysB),
		pg.Array(digest)); err != nil {
		return fmt.Errorf("writing the merge candidate sample: %w", err)
	}
	return nil
}

// sortedRowKeys are a map's keys in order, so the writes do not depend on
// map order.
func sortedRowKeys(m map[string]sampleRow) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// MergeCandidateDigests are the digests of the stored candidates: the
// census of the last committed pass.
func MergeCandidateDigests(ctx context.Context, db pg.DBI) (map[string]bool, error) {
	var digests [][]byte
	if _, err := db.QueryContext(ctx, &digests, `SELECT digest FROM author_entity_merge_candidate`); err != nil {
		return nil, fmt.Errorf("reading the stored merge candidates: %w", err)
	}
	out := make(map[string]bool, len(digests))
	for _, d := range digests {
		out[string(d)] = true
	}
	return out, nil
}
