package services_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"gopds-api/database"
	"gopds-api/internal/wikidata"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The Wikidata reference (phase E4w) end to end over a scratch catalog: a
// fetch from a fake endpoint is published, the build merges on it, the same
// extract fetched again changes nothing, and a fetch whose chunk failed goes
// on from the chunks it has.

// fakeEndpoint serves an extract's chunks: the rows given per chunk, and a
// filler row in every other chunk (no chunk of a real extract is empty).
type fakeEndpoint struct {
	rows   map[string][]string
	fail   map[string]bool
	opened []string
}

func (f *fakeEndpoint) Name() string { return "https://fake.invalid/sparql" }

func (f *fakeEndpoint) Open(_ context.Context, c wikidata.Chunk) (io.ReadCloser, error) {
	f.opened = append(f.opened, c.Name)
	if f.fail[c.Name] {
		return nil, fmt.Errorf("fetching chunk %s: 503 Service Unavailable", c.Name)
	}
	rows := f.rows[c.Name]
	if len(rows) == 0 {
		switch c.Kind {
		case wikidata.KindPeople:
			rows = []string{item("Q1") + "\t\t"}
		case wikidata.KindWorks:
			rows = []string{item("Q1") + "\t" + item("Q2") + "\t" + `"Filler"@en`}
		default:
			rows = []string{item("Q1") + "\t" + `"Filler Name"@en`}
		}
	}
	return io.NopCloser(strings.NewReader("?p\t?x\n" + strings.Join(rows, "\n") + "\n")), nil
}

func item(qid string) string { return "<http://www.wikidata.org/entity/" + qid + ">" }

// kingName is a name of Stephen King's item.
func kingName(text, lang string) string { return item("Q39829") + "\t\"" + text + "\"@" + lang }

// kingEndpoint is Stephen King's item: his Russian and English labels.
func kingEndpoint() *fakeEndpoint {
	return &fakeEndpoint{rows: map[string][]string{
		"people":   {item("Q39829") + "\t\"1947\"^^<http://www.w3.org/2001/XMLSchema#int>\t"},
		"label_ru": {kingName("Стивен Кинг", "ru")},
		"label_en": {kingName("Stephen King", "en")},
		"works":    {item("Q39829") + "\t" + item("Q212585") + "\t" + `"Оно"@ru`},
	}}
}

// fetch starts a fetch and runs the worker over it.
func fetch(t *testing.T, db *pg.DB, src wikidata.Source) *database.WikidataExtract {
	t.Helper()
	ctx := context.Background()
	ext, err := database.StartWikidataFetch(ctx, db, 0, src.Name())
	require.NoError(t, err)
	loop := services.NewWikidataFetchLoop(db, src, services.WikidataFetchConfig{})
	worked, err := loop.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, worked)
	latest, _, err := database.LatestWikidataExtracts(ctx, db)
	require.NoError(t, err)
	require.Equal(t, ext.ID, latest.ID)
	return latest
}

// wikidataRows are the reference's rows, extract by extract.
func wikidataRows(t *testing.T, db *pg.DB) string {
	t.Helper()
	var out string
	_, err := db.QueryOne(pg.Scan(&out), `SELECT coalesce(string_agg(r, ';' ORDER BY r), '') FROM (
		SELECT format('n:%s:%s:%s:%s:%s:%s', extract_id, key, qid, kind, lang, label) AS r FROM wikidata_name
		UNION ALL SELECT format('p:%s:%s:%s:%s', extract_id, qid, born, died) FROM wikidata_person
		UNION ALL SELECT format('w:%s:%s:%s:%s:%s', extract_id, qid, work_qid, title_key, title) FROM wikidata_work) x`)
	require.NoError(t, err)
	return out
}

func buildOnce(t *testing.T, c *entityCatalog) *database.AuthorEntityBuildRun {
	t.Helper()
	run, err := services.NewAuthorEntityAdmin(c.db).Start(context.Background(), 0)
	require.NoError(t, err)
	return runToCompletion(t, services.NewAuthorEntityBuildLoop(c.db, services.AuthorEntityBuildConfig{}), c.db, run.ID)
}

func TestAPublishedExtractMergesAndTheSameExtractAgainChangesNothing(t *testing.T) {
	c := newEntityCatalog(t)
	ru := c.book("Оно")
	ru2 := c.book("Мизери")
	en := c.book("It")
	c.credit(ru, 0x61, "Кинг", "Стивен")
	c.credit(ru2, 0x61, "Кинг", "Стивен")
	c.credit(en, 0x62, "King", "Stephen")
	first := buildOnce(t, c)
	require.Positive(t, first.Changes)
	require.NotEqual(t, entityOf(t, c.db, 0x61, c.bookOf(ru)), entityOf(t, c.db, 0x62, c.bookOf(en)),
		"no evidence yet: two entities")

	ext := fetch(t, c.db, kingEndpoint())
	require.Equal(t, database.WikidataPublished, ext.Status)
	require.NotNil(t, ext.ContentID)
	assert.Equal(t, len(wikidata.Chunks()), ext.ChunksDone)
	assert.Equal(t, 1, ext.People, "King")
	rows := wikidataRows(t, c.db)

	built := buildOnce(t, c)
	assert.Positive(t, built.Changes)
	kingRU := entityOf(t, c.db, 0x61, c.bookOf(ru))
	assert.Equal(t, kingRU, entityOf(t, c.db, 0x62, c.bookOf(en)), "one entity on the evidence")
	rule := "wikidata:Q39829@" + *ext.ContentID
	var method, stored string
	_, err := c.db.QueryOne(pg.Scan(&method, &stored), `SELECT method, rule FROM author_entity_assignment
		WHERE source_fingerprint = ?`, bytes.Repeat([]byte{0x62}, 32))
	require.NoError(t, err)
	assert.Equal(t, "evidence", method)
	assert.Equal(t, rule, stored)
	var logged string
	_, err = c.db.QueryOne(pg.Scan(&method, &logged), `SELECT method, evidence->>'rule' FROM author_entity_merge_log
		WHERE to_id = ?`, kingRU)
	require.NoError(t, err)
	assert.Equal(t, "evidence", method, "the English entity merged on evidence")
	assert.Equal(t, rule, logged)
	var qid, from string
	_, err = c.db.QueryOne(pg.Scan(&qid, &from), `SELECT ref, extract FROM author_entity_reference
		WHERE entity_id = ? AND source = 'wikidata'`, kingRU)
	require.NoError(t, err)
	assert.Equal(t, "Q39829", qid, "the entity keeps its item")
	assert.Equal(t, *ext.ContentID, from)
	var rep services.AuthorEntityBuildReport
	require.NoError(t, json.Unmarshal(built.Report, &rep))
	assert.Equal(t, *ext.ContentID, rep.Wikidata.Extract)
	assert.Equal(t, 1, rep.Wikidata.Merges["cross_script"])

	// The same data fetched again: discarded, not a row changes, and the
	// build writes nothing.
	again := fetch(t, c.db, kingEndpoint())
	assert.Equal(t, database.WikidataDuplicate, again.Status)
	assert.Equal(t, *ext.ContentID, *again.ContentID)
	assert.Equal(t, rows, wikidataRows(t, c.db), "a duplicate extract leaves the reference as it was")
	_, published, err := database.LatestWikidataExtracts(context.Background(), c.db)
	require.NoError(t, err)
	assert.Equal(t, ext.ID, published.ID)
	assert.Zero(t, buildOnce(t, c).Changes)

	// New content replaces the published extract and its rows; the build
	// takes the new extract's rule.
	changed := kingEndpoint()
	changed.rows["alias_ru"] = []string{kingName("Стивен Эдвин Кинг", "ru")}
	next := fetch(t, c.db, changed)
	assert.Equal(t, database.WikidataPublished, next.Status)
	assert.NotEqual(t, *ext.ContentID, *next.ContentID)
	var oldRows int
	_, err = c.db.QueryOne(pg.Scan(&oldRows), `SELECT count(*) FROM wikidata_name WHERE extract_id <> ?`, next.ID)
	require.NoError(t, err)
	assert.Zero(t, oldRows, "the replaced extracts' rows are gone")
	rebuilt := buildOnce(t, c)
	assert.Positive(t, rebuilt.Changes, "the rule names the new extract")
	assert.Zero(t, buildOnce(t, c).Changes)
	_, err = c.db.QueryOne(pg.Scan(&from), `SELECT extract FROM author_entity_reference WHERE entity_id = ?`, kingRU)
	require.NoError(t, err)
	assert.Equal(t, *next.ContentID, from)

	// An administrator's pinned item stays whatever the evidence says.
	_, err = c.db.Exec(`UPDATE author_entity_reference SET ref = 'Q1', pinned = true WHERE entity_id = ?`, kingRU)
	require.NoError(t, err)
	assert.Zero(t, buildOnce(t, c).Changes)
	_, err = c.db.QueryOne(pg.Scan(&qid), `SELECT ref FROM author_entity_reference WHERE entity_id = ?`, kingRU)
	require.NoError(t, err)
	assert.Equal(t, "Q1", qid)
}

func TestAFailedChunkFailsTheFetchAndTheNextStartGoesOn(t *testing.T) {
	c := newEntityCatalog(t)
	ctx := context.Background()
	src := kingEndpoint()
	src.fail = map[string]bool{"label_en": true}
	ext := fetch(t, c.db, src)
	assert.Equal(t, database.WikidataFailed, ext.Status)
	require.NotNil(t, ext.LastError)
	assert.Contains(t, *ext.LastError, "503")
	assert.Equal(t, 3, ext.ChunksDone, "people, label_ru and alias_ru were written")
	_, published, err := database.LatestWikidataExtracts(ctx, c.db)
	require.NoError(t, err)
	assert.Nil(t, published)

	src.fail, src.opened = nil, nil
	resumed := fetch(t, c.db, src)
	assert.Equal(t, ext.ID, resumed.ID, "the failed fetch goes on")
	assert.Equal(t, database.WikidataPublished, resumed.Status)
	assert.NotContains(t, src.opened, "people", "the chunks written are not fetched again")
	assert.Equal(t, "label_en", src.opened[0])
	assert.Len(t, src.opened, len(wikidata.Chunks())-3)

	_, err = database.StartWikidataFetch(ctx, c.db, 0, src.Name())
	require.NoError(t, err)
	_, err = database.StartWikidataFetch(ctx, c.db, 0, src.Name())
	require.ErrorIs(t, err, database.ErrWikidataFetchActive)
}

// A response that is not the query's — no row of it parses — fails the
// chunk instead of publishing an empty reference.
func TestAnEmptyChunkFailsTheFetch(t *testing.T) {
	c := newEntityCatalog(t)
	ext := fetch(t, c.db, &emptyEndpoint{})
	assert.Equal(t, database.WikidataFailed, ext.Status)
	assert.Zero(t, ext.ChunksDone)
}

type emptyEndpoint struct{}

func (emptyEndpoint) Name() string { return "https://empty.invalid/sparql" }

func (emptyEndpoint) Open(context.Context, wikidata.Chunk) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("<html>busy</html>\n")), nil
}

// Review r1, finding 5: cleaning up after a publish never takes the rows of
// the failed fetch a start would resume — it would resume past chunks whose
// rows are gone. A failed fetch no start resumes any more is abandoned, its
// chunk records gone with its rows.
func TestPruningKeepsTheResumableFailedFetch(t *testing.T) {
	c := newEntityCatalog(t)
	ctx := context.Background()
	first := fetch(t, c.db, kingEndpoint())
	require.Equal(t, database.WikidataPublished, first.Status)

	changed := kingEndpoint()
	changed.rows["alias_ru"] = []string{kingName("Стивен Эдвин Кинг", "ru")}
	changed.fail = map[string]bool{"label_en": true}
	failed := fetch(t, c.db, changed)
	require.Equal(t, database.WikidataFailed, failed.Status)
	// The cleanup of the first publish runs late.
	_, err := database.PruneWikidataExtracts(ctx, c.db)
	require.NoError(t, err)

	changed.fail = nil
	resumed := fetch(t, c.db, changed)
	require.Equal(t, failed.ID, resumed.ID)
	require.Equal(t, database.WikidataPublished, resumed.Status)
	assert.Equal(t, 1, resumed.People, "the people chunk written before the failure is still there")
	var labels int
	_, err = c.db.QueryOne(pg.Scan(&labels), `SELECT count(*) FROM wikidata_name
		WHERE extract_id = ? AND kind = 'label' AND lang = 'ru'`, resumed.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, labels, "the Russian labels written before the failure are still there")
}

func TestAFailedFetchNoStartResumesIsAbandoned(t *testing.T) {
	c := newEntityCatalog(t)
	ctx := context.Background()
	src := kingEndpoint()
	src.fail = map[string]bool{"label_en": true}
	failed := fetch(t, c.db, src)
	require.Equal(t, database.WikidataFailed, failed.Status)
	// A fetch from another source starts beside it: the failed one is no
	// longer the newest, nothing resumes it.
	other := &dirLike{fakeEndpoint: kingEndpoint()}
	next := fetch(t, c.db, other)
	require.Equal(t, database.WikidataPublished, next.Status)
	_, err := database.PruneWikidataExtracts(ctx, c.db)
	require.NoError(t, err)
	var status string
	var chunks, rows int
	_, err = c.db.QueryOne(pg.Scan(&status, &chunks, &rows), `SELECT e.status,
		(SELECT count(*) FROM wikidata_extract_chunk WHERE extract_id = e.id),
		(SELECT count(*) FROM wikidata_name WHERE extract_id = e.id)
		FROM wikidata_extract e WHERE e.id = ?`, failed.ID)
	require.NoError(t, err)
	assert.Equal(t, database.WikidataAbandoned, status)
	assert.Zero(t, chunks)
	assert.Zero(t, rows)
}

// dirLike is the same data from another source.
type dirLike struct{ *fakeEndpoint }

func (*dirLike) Name() string { return "file:///operator/reference-extract" }

// Review r1, finding 6: a worker takes only fetches of its own source; a
// directory import is never finished from the network.
func TestAWorkerTakesOnlyItsOwnSourcesFetch(t *testing.T) {
	c := newEntityCatalog(t)
	ctx := context.Background()
	dir := &dirLike{fakeEndpoint: kingEndpoint()}
	ext, err := database.StartWikidataFetch(ctx, c.db, 0, dir.Name())
	require.NoError(t, err)

	network := kingEndpoint()
	worked, err := services.NewWikidataFetchLoop(c.db, network, services.WikidataFetchConfig{}).RunOnce(ctx)
	require.NoError(t, err)
	assert.False(t, worked, "the network worker leaves the directory import alone")
	assert.Empty(t, network.opened)

	worked, err = services.NewWikidataFetchLoop(c.db, dir, services.WikidataFetchConfig{}).RunOnce(ctx)
	require.NoError(t, err)
	assert.True(t, worked)
	latest, _, err := database.LatestWikidataExtracts(ctx, c.db)
	require.NoError(t, err)
	assert.Equal(t, ext.ID, latest.ID)
	assert.Equal(t, database.WikidataPublished, latest.Status)
	assert.Equal(t, dir.Name(), latest.Source)
}

// A fetch whose worker stopped blocks no one for ever: a start from its own
// source resumes it, one from another source fails it and starts anew.
func TestAStoppedFetchIsResumedOrReplaced(t *testing.T) {
	c := newEntityCatalog(t)
	ctx := context.Background()
	dir := &dirLike{fakeEndpoint: kingEndpoint()}
	stopped, err := database.StartWikidataFetch(ctx, c.db, 0, dir.Name())
	require.NoError(t, err)
	_, err = database.StartWikidataFetch(ctx, c.db, 0, dir.Name())
	require.ErrorIs(t, err, database.ErrWikidataFetchActive, "a fetch just started is running")

	// Its worker never came: long enough ago, it is stale.
	_, err = c.db.Exec(`UPDATE wikidata_extract SET started_at = now() - interval '1 hour' WHERE id = ?`, stopped.ID)
	require.NoError(t, err)
	again, err := database.StartWikidataFetch(ctx, c.db, 0, dir.Name())
	require.NoError(t, err)
	assert.Equal(t, stopped.ID, again.ID, "its own source resumes it")

	_, err = c.db.Exec(`UPDATE wikidata_extract SET lease_owner = gen_random_uuid(),
		lease_expires_at = now() - interval '1 minute' WHERE id = ?`, stopped.ID)
	require.NoError(t, err)
	network := kingEndpoint()
	fresh, err := database.StartWikidataFetch(ctx, c.db, 0, network.Name())
	require.NoError(t, err)
	assert.NotEqual(t, stopped.ID, fresh.ID)
	var status string
	_, err = c.db.QueryOne(pg.Scan(&status), `SELECT status FROM wikidata_extract WHERE id = ?`, stopped.ID)
	require.NoError(t, err)
	assert.Equal(t, database.WikidataFailed, status)
}
