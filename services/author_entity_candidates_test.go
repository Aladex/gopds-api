package services_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authorentity"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The merge candidates of an entity pass (migration 63, plan E4a): written
// as a diff in the completing transaction, and every input of their cards
// marks a build (§4.10) while the build's own writes mark none.

func (c *entityCatalog) count(query string) int {
	c.t.Helper()
	var n int
	_, err := c.db.QueryOne(pg.Scan(&n), query)
	require.NoError(c.t, err, query)
	return n
}

// marks are the untaken marks: the passes to do.
func (c *entityCatalog) marks() int {
	return c.count(`SELECT count(*) FROM author_autopilot_mark WHERE build_run_id IS NULL`)
}

// build runs a whole pass and returns its stored report.
func (c *entityCatalog) build() services.AuthorEntityBuildReport {
	c.t.Helper()
	ctx := context.Background()
	run, err := services.NewAuthorEntityAdmin(c.db).Start(ctx, 0)
	require.NoError(c.t, err)
	done := runToCompletion(c.t, services.NewAuthorEntityBuildLoop(c.db, services.AuthorEntityBuildConfig{}), c.db, run.ID)
	var rep services.AuthorEntityBuildReport
	require.NoError(c.t, json.Unmarshal(done.Report, &rep))
	require.NoError(c.t, database.SettleAuthorAutopilotMarks(ctx, c.db))
	return rep
}

// seedCandidates is a swap pair, «Стивен Кинг» and «Кинг Стивен», and a
// bucket book, «Алексей Иванов» beside «Алексей Викторович Иванов».
func (c *entityCatalog) seedCandidates() (smallSide byte) {
	c.credit(c.book("Оно"), 0x61, "Кинг", "Стивен")
	c.credit(c.book("Сияние"), 0x61, "Кинг", "Стивен")
	c.credit(c.book("Рассказы"), 0x62, "Стивен", "Кинг")
	c.credit(c.book("Общага-на-Крови"), 0x63, "Иванов", "Алексей", "Викторович")
	c.credit(c.book("Психология"), 0x64, "Иванов", "Алексей")
	return 0x62
}

// A pass writes its candidates, their members and its sample; the next pass
// over the same catalog keeps every row and changes nothing else; a change
// that moves a card replaces just that candidate. A pass writes no mark.
func TestTheBuildWritesItsMergeCandidatesAsADiff(t *testing.T) {
	c := newEntityCatalog(t)
	small := c.seedCandidates()
	first := c.build()
	assert.Equal(t, 2, first.Candidates.Candidates)
	assert.Equal(t, 1, first.Candidates.ByKind["swap"]["Cyrl"].Candidates)
	assert.Equal(t, 1, first.Candidates.ByKind["bucket"]["Cyrl"].Candidates)
	assert.Equal(t, database.MergeCandidateDiff{Inserted: 2, MembersInserted: 4, SampleInserted: 2, RowsWritten: 8},
		first.CandidateDiff)
	assert.Equal(t, 2, c.count(`SELECT count(*) FROM author_entity_merge_candidate`))
	assert.Equal(t, 1, c.count(`SELECT count(*) FROM author_entity_merge_candidate
		WHERE item_kind = 'book' AND credit_id IS NOT NULL AND credit_ordinal = 0`))
	assert.Equal(t, 4, c.count(`SELECT count(*) FROM author_entity_merge_candidate_member`), "one fingerprint a side")
	assert.Equal(t, 2, c.count(`SELECT count(*) FROM author_entity_merge_candidate_sample WHERE digest IS NOT NULL`))
	assert.Zero(t, c.marks(), "the pass marked a pass to do")

	// An unchanged pass writes no row at all (review r1, P2).
	second := c.build()
	assert.Equal(t, database.MergeCandidateDiff{Kept: 2}, second.CandidateDiff)
	assert.Zero(t, second.Changes)
	assert.Equal(t, 2, c.count(`SELECT count(*) FROM author_entity_merge_candidate_sample`), "one sample, not one per pass")
	assert.Equal(t, 2, c.count(`SELECT count(*) FROM author_entity_merge_candidate_sample
		WHERE build_run_id = (SELECT min(id) FROM author_entity_build_run)`), "an unchanged sample row is not rewritten")
	assert.Zero(t, c.marks())

	// The small side gains a book: its card moves.
	c.credit(c.book("Ещё рассказы"), small, "Стивен", "Кинг")
	assert.Positive(t, c.marks())
	third := c.build()
	assert.Equal(t, database.MergeCandidateDiff{Inserted: 1, Deleted: 1, Kept: 1, MembersInserted: 2, SampleInserted: 1,
		SampleDeleted: 1, RowsWritten: 6}, third.CandidateDiff)
	assert.Equal(t, 2, c.count(`SELECT count(*) FROM author_entity_merge_candidate`))
	assert.Zero(t, c.marks())
}

// marksOf is how many marks one statement in its own transaction left.
func (c *entityCatalog) marksOf(query string, params ...interface{}) int {
	c.t.Helper()
	c.exec(`DELETE FROM author_autopilot_mark`)
	c.exec(query, params...)
	return c.marks()
}

// Each card input and candidate input marks a build once its writer
// commits; the same value written again, a column not on the card or a
// genre title marks nothing; the build's own automatic rows mark nothing.
func TestEveryCardInputMarksABuild(t *testing.T) {
	c := newEntityCatalog(t)
	snap := c.book("Оно", 701)
	c.credit(snap, 0x71, "Кинг", "Стивен")
	book := c.bookOf(snap)
	c.exec(`INSERT INTO opds_catalog_genre (id, genre, title) VALUES (901, 'sf_horror', 'Ужасы')`)
	c.exec(`INSERT INTO opds_catalog_bgenre (genre_id, book_id) VALUES (901, ?)`, book)
	c.build()
	var result int64
	_, err := c.db.QueryOne(pg.Scan(&result), `SELECT result_id FROM book_contributor_credit_selection LIMIT 1`)
	require.NoError(t, err)

	for _, tc := range []struct {
		name  string
		query string
		args  []interface{}
		want  int
	}{
		// Result rows are immutable; a new one is an input.
		{"a result inserted", `INSERT INTO contributor_normalization_result (source_fingerprint, normalization_key,
			extractor_version, normalizer_version, result_schema_version, method, kind, status, decision_class,
			family_name, given_name, additional_names, display_name, sort_name, search_key, script)
			SELECT decode(repeat('ab', 32), 'hex'), decode(repeat('cd', 32), 'hex'), extractor_version,
				normalizer_version, result_schema_version, method, kind, status, decision_class, family_name,
				given_name, additional_names, display_name, sort_name, search_key, script
			FROM contributor_normalization_result WHERE id = ?`, []interface{}{result}, 1},
		{"a snapshot no longer current", `UPDATE book_metadata_snapshot SET is_current = false WHERE id = ?`, []interface{}{snap}, 1},
		{"a snapshot current again", `UPDATE book_metadata_snapshot SET is_current = true WHERE id = ?`, []interface{}{snap}, 1},
		{"a snapshot flag written as it was", `UPDATE book_metadata_snapshot SET is_current = is_current WHERE id = ?`, []interface{}{snap}, 0},
		{"a book's title", `UPDATE opds_catalog_book SET title = 'Оно [СИ]' WHERE id = ?`, []interface{}{book}, 1},
		{"a book's annotation", `UPDATE opds_catalog_book SET annotation = 'Дерри' WHERE id = ?`, []interface{}{book}, 1},
		{"a book's docdate", `UPDATE opds_catalog_book SET docdate = '1986' WHERE id = ?`, []interface{}{book}, 1},
		{"a book's language", `UPDATE opds_catalog_book SET lang = 'en' WHERE id = ?`, []interface{}{book}, 1},
		{"a book's title written as it was", `UPDATE opds_catalog_book SET title = title WHERE id = ?`, []interface{}{book}, 0},
		{"a book's cover", `UPDATE opds_catalog_book SET cover = NOT coalesce(cover, false) WHERE id = ?`, []interface{}{book}, 0},
		{"a book's md5", `UPDATE opds_catalog_book SET md5 = md5(md5) WHERE id = ?`, []interface{}{book}, 0},
		{"a book's approval", `UPDATE opds_catalog_book SET approved = NOT approved WHERE id = ?`, []interface{}{book}, 0},
		{"a book hidden as a duplicate", `UPDATE opds_catalog_book SET duplicate_hidden = NOT duplicate_hidden
			WHERE id = ?`, []interface{}{book}, 0},
		{"a genre link", `INSERT INTO opds_catalog_bgenre (genre_id, book_id) VALUES (901, ?)`, []interface{}{book}, 1},
		{"a genre link deleted", `DELETE FROM opds_catalog_bgenre
			WHERE book_id = ? AND id = (SELECT max(id) FROM opds_catalog_bgenre)`, []interface{}{book}, 1},
		{"a genre code", `UPDATE opds_catalog_genre SET genre = 'sf_horror_classic' WHERE id = 901`, nil, 1},
		{"a genre title", `UPDATE opds_catalog_genre SET title = 'Хоррор' WHERE id = 901`, nil, 0},
		{"a series link", `INSERT INTO opds_catalog_bseries (ser_no, book_id, ser_id) VALUES (2, ?, 701)`, []interface{}{book}, 1},
		{"a series name", `UPDATE opds_catalog_series SET ser = 'Тёмная башня' WHERE id = 701`, nil, 1},
		{"a series language", `UPDATE opds_catalog_series SET lang_code = 2 WHERE id = 701`, nil, 0},
		{"an automatic fingerprint row", `UPDATE author_entity_assignment SET rule = 'D2' WHERE method <> 'manual'`, nil, 0},
		{"a manual fingerprint row", `UPDATE author_entity_assignment SET method = 'manual', rule = 'manual'`, nil, 1},
		{"a manual credit row", `INSERT INTO author_entity_credit_assignment (credit_id, entity_id, method, rule)
			SELECT c.id, NULL, 'manual', 'manual' FROM book_contributor_credit c LIMIT 1`, nil, 1},
		{"a Wikidata extract fetching", `INSERT INTO wikidata_extract (source) VALUES ('fixture')`, nil, 0},
		{"a Wikidata extract published", `UPDATE wikidata_extract SET status = 'published', content_id = 'wd-0123456789abcdef',
			manifest = '{}' WHERE status = 'fetching'`, nil, 1},
		{"a Wikidata extract's other column", `UPDATE wikidata_extract SET people = people + 1`, nil, 0},
		{"a book added", `INSERT INTO opds_catalog_book (id, filename, path, format, registerdate, docdate, lang, title,
			annotation, md5) VALUES (99001, 'x.fb2', 'fixture.zip', 'fb2', now(), '', 'ru', 'Новая', '', 'x')`, nil, 1},
		{"a book deleted", `DELETE FROM opds_catalog_book WHERE id = 99001`, nil, 1},
		{"a series link deleted", `DELETE FROM opds_catalog_bseries WHERE ser_no = 2`, nil, 1},
		{"nothing deleted", `DELETE FROM opds_catalog_bseries WHERE false`, nil, 0},
	} {
		assert.Equal(t, tc.want, c.marksOf(tc.query, tc.args...), tc.name)
	}
}

// digests are the stored candidates' digests, sorted.
func (c *entityCatalog) digests() []string {
	c.t.Helper()
	var out []string
	_, err := c.db.Query(&out, `SELECT encode(digest, 'hex') FROM author_entity_merge_candidate ORDER BY 1`)
	require.NoError(c.t, err)
	return out
}

// Review r1, P1: the census only observes the build and never holds it. A
// card read, a seal or a candidate write that fails leaves the pass to
// complete with its entity changes; the stored census stays as it was, the
// report names the closed class, and the next pass does the census again.
func TestTheCensusNeverHoldsTheBuild(t *testing.T) {
	for _, step := range []string{services.AuthorEntityCensusCardLoad, services.AuthorEntityCensusSeal,
		services.AuthorEntityCensusWrite} {
		t.Run(step, func(t *testing.T) {
			c := newEntityCatalog(t)
			small := c.seedCandidates()
			first := c.build()
			require.Equal(t, 2, first.Candidates.Candidates)
			stored := c.digests()

			// A new author (an entity to write) and a book that moves a card.
			c.credit(c.book("Пикник на обочине"), 0x65, "Стругацкий", "Аркадий", "Натанович")
			c.credit(c.book("Ещё рассказы"), small, "Стивен", "Кинг")
			restore := services.SetAuthorEntityCensusHook(func(at string) error {
				if at == step {
					return errors.New("a census step that fails")
				}
				return nil
			})
			second := c.build()
			restore()
			assert.Positive(t, second.Changes, "the build did not land")
			assert.Equal(t, map[string]string{
				services.AuthorEntityCensusCardLoad: services.AuthorEntityCensusErrorCardLoad,
				services.AuthorEntityCensusSeal:     services.AuthorEntityCensusErrorSeal,
				services.AuthorEntityCensusWrite:    services.AuthorEntityCensusErrorWrite,
			}[step], second.CandidateError)
			assert.Equal(t, stored, c.digests(), "the failed census changed the stored one")
			assert.Zero(t, c.marks(), "the failed census marked a pass to do")

			third := c.build()
			assert.Empty(t, third.CandidateError)
			assert.Equal(t, 1, third.CandidateDiff.Inserted, "the next pass did not redo the census")
			assert.Equal(t, 1, third.CandidateDiff.Deleted)
		})
	}
}

// Review r1, P3: every declared (table, column) of the card is read by the
// build and marked by its trigger. For each one a semantic change of the
// column, in its own transaction, leaves a mark and moves the stored census;
// a declared source without a case here fails the test, so a new card field
// cannot be declared, triggered and then silently never read.
func TestEveryDeclaredCardSourceIsReadAndMarked(t *testing.T) {
	type cardCase struct {
		// setup runs before the first pass; change is the semantic change.
		setup, change string
	}
	// The small side «Кинг Стивен» is fingerprint 0x62 on the book "Рассказы".
	const (
		smallFP   = `decode(repeat('62', 32), 'hex')`
		smallBook = `(SELECT id FROM opds_catalog_book WHERE title = 'Рассказы')`
		smallSnap = `(SELECT id FROM book_metadata_snapshot WHERE book_id = ` + smallBook + ` AND is_current)`
		// A result like the small side's selected one, under another key (the
		// fixture's own keys are the fingerprint's bytes inverted), with
		// the given display name and nickname, selected in its place.
		repoint = `WITH r AS (INSERT INTO contributor_normalization_result (source_fingerprint, normalization_key,
			extractor_version, normalizer_version, result_schema_version, method, kind, status, decision_class,
			family_name, given_name, additional_names, display_name, sort_name, search_key, script, nickname)
			SELECT source_fingerprint, decode(repeat('e7', 32), 'hex'), extractor_version, normalizer_version,
				result_schema_version, method, kind, status, decision_class, family_name, given_name, additional_names,
				%s, sort_name, search_key, script, %s
			FROM contributor_normalization_result WHERE source_fingerprint = ` + smallFP + ` RETURNING id)
			UPDATE book_contributor_credit_selection SET result_id = (SELECT id FROM r)
			WHERE source_fingerprint = ` + smallFP
	)
	cases := map[[2]string]cardCase{
		{"book_contributor_credit_selection", "result_id"}: {change: fmt.Sprintf(repoint, `'Кинг, Стивен'`, `nickname`)},
		{"book_contributor_credit_selection", "state"}: {change: `UPDATE book_contributor_credit_selection
			SET state = 'review', basis = NULL, policy_version = NULL WHERE source_fingerprint = ` + smallFP},
		{"contributor_normalization_result", "display_name"}: {change: fmt.Sprintf(repoint, `'Кинг Стивен Эдвин'`, `nickname`)},
		{"contributor_normalization_result", "nickname"}:     {change: fmt.Sprintf(repoint, `display_name`, `'Ричард Бахман'`)},
		{"book_metadata_snapshot", "is_current"}: {change: `UPDATE book_metadata_snapshot SET is_current = false
			WHERE id = ` + smallSnap},
		// A source year arrives with a new snapshot (snapshot rows are
		// immutable): the re-scanned file, with the same credit.
		{"book_metadata_snapshot", "source_year"}: {change: `
			WITH old AS (UPDATE book_metadata_snapshot SET is_current = false WHERE id = ` + smallSnap + ` RETURNING *),
			snap AS (INSERT INTO book_metadata_snapshot (book_id, book_md5, extractor_version, origin, outcome,
				archive_path, entry_name, source_year)
				SELECT book_id, md5(book_md5), extractor_version, origin, outcome, archive_path, entry_name, '1978'
				FROM old
				RETURNING id),
			credit AS (INSERT INTO book_contributor_credit (snapshot_id, role, position, source_first_name,
				source_last_name, source_display_name, source_fingerprint)
				SELECT snap.id, c.role, c.position, c.source_first_name, c.source_last_name, c.source_display_name,
					c.source_fingerprint FROM snap, book_contributor_credit c JOIN old ON c.snapshot_id = old.id
				RETURNING id, source_fingerprint)
			INSERT INTO book_contributor_credit_selection (credit_id, source_fingerprint, state, result_id, basis,
				policy_version)
			SELECT credit.id, credit.source_fingerprint, 'selected', s.result_id, 'automatic', '4'
			FROM credit JOIN book_contributor_credit_selection s ON s.source_fingerprint = credit.source_fingerprint
			LIMIT 1`},
		{"opds_catalog_book", "id"}: {change: `
			WITH b AS (INSERT INTO opds_catalog_book (id, filename, path, format, registerdate, docdate, lang, title,
				annotation, md5) VALUES (7, '7.fb2', 'fixture.zip', 'fb2', now(), '', 'ru', 'Ранние рассказы', '',
				md5('7')) RETURNING id),
			snap AS (INSERT INTO book_metadata_snapshot (book_id, book_md5, extractor_version, origin, outcome,
				archive_path, entry_name) SELECT id, md5('7'), 'extractor-v1', 'live', 'extracted', 'fixture.zip',
				'7.fb2' FROM b RETURNING id),
			credit AS (INSERT INTO book_contributor_credit (snapshot_id, role, position, source_first_name,
				source_last_name, source_display_name, source_fingerprint)
				SELECT id, 'author', 0, 'Кинг', 'Стивен', 'Кинг Стивен', ` + smallFP + ` FROM snap RETURNING id)
			INSERT INTO book_contributor_credit_selection (credit_id, source_fingerprint, state, result_id, basis,
				policy_version)
			SELECT credit.id, ` + smallFP + `, 'selected', s.result_id, 'automatic', '4'
			FROM credit, book_contributor_credit_selection s WHERE s.source_fingerprint = ` + smallFP + ` LIMIT 1`},
		{"opds_catalog_book", "title"}:      {change: `UPDATE opds_catalog_book SET title = 'Рассказы [СИ]' WHERE id = ` + smallBook},
		{"opds_catalog_book", "docdate"}:    {change: `UPDATE opds_catalog_book SET docdate = '1985' WHERE id = ` + smallBook},
		{"opds_catalog_book", "lang"}:       {change: `UPDATE opds_catalog_book SET lang = 'en' WHERE id = ` + smallBook},
		{"opds_catalog_book", "annotation"}: {change: `UPDATE opds_catalog_book SET annotation = 'Дерри' WHERE id = ` + smallBook},
		{"opds_catalog_bgenre", "book_id"}: {setup: `INSERT INTO opds_catalog_genre (id, genre, title) VALUES (901, 'horror', '')`,
			change: `INSERT INTO opds_catalog_bgenre (genre_id, book_id) VALUES (901, ` + smallBook + `)`},
		{"opds_catalog_bgenre", "genre_id"}: {setup: `INSERT INTO opds_catalog_genre (id, genre, title)
			VALUES (901, 'horror', ''), (902, 'thriller', '');
			INSERT INTO opds_catalog_bgenre (genre_id, book_id) VALUES (901, ` + smallBook + `)`,
			change: `UPDATE opds_catalog_bgenre SET genre_id = 902 WHERE genre_id = 901`},
		{"opds_catalog_genre", "genre"}: {setup: `INSERT INTO opds_catalog_genre (id, genre, title) VALUES (901, 'horror', '');
			INSERT INTO opds_catalog_bgenre (genre_id, book_id) VALUES (901, ` + smallBook + `)`,
			change: `UPDATE opds_catalog_genre SET genre = 'sf_horror' WHERE id = 901`},
		{"opds_catalog_bseries", "book_id"}: {setup: `INSERT INTO opds_catalog_series (id, ser, lang_code) VALUES (801, 'Тёмная башня', 1)`,
			change: `INSERT INTO opds_catalog_bseries (ser_no, book_id, ser_id) VALUES (1, ` + smallBook + `, 801)`},
		{"opds_catalog_bseries", "ser_id"}: {setup: `INSERT INTO opds_catalog_series (id, ser, lang_code)
			VALUES (801, 'Тёмная башня', 1), (802, 'Бегущий человек', 1);
			INSERT INTO opds_catalog_bseries (ser_no, book_id, ser_id) VALUES (1, ` + smallBook + `, 801)`,
			change: `UPDATE opds_catalog_bseries SET ser_id = 802 WHERE ser_id = 801`},
		{"opds_catalog_series", "ser"}: {setup: `INSERT INTO opds_catalog_series (id, ser, lang_code) VALUES (801, 'Тёмная башня', 1);
			INSERT INTO opds_catalog_bseries (ser_no, book_id, ser_id) VALUES (1, ` + smallBook + `, 801)`,
			change: `UPDATE opds_catalog_series SET ser = 'Тёмная Башня' WHERE id = 801`},
	}
	declared := map[[2]string]bool{}
	for _, sources := range authorentity.MergeCardSources {
		for _, s := range sources {
			for _, col := range s.Columns {
				declared[[2]string{s.Table, col}] = true
			}
		}
	}
	for source := range declared {
		assert.Contains(t, cases, source, "a declared card source has no case")
	}
	for source, tc := range cases {
		require.True(t, declared[source], "a case for an undeclared source %v", source)
		t.Run(source[0]+"."+source[1], func(t *testing.T) {
			c := newEntityCatalog(t)
			c.seedCandidates()
			if tc.setup != "" {
				c.exec(tc.setup)
			}
			c.build()
			before := c.digests()
			require.Len(t, before, 2)
			assert.Positive(t, c.marksOf(tc.change), "the change marked no pass")
			c.build()
			assert.NotEqual(t, before, c.digests(), "the change did not move the census")
		})
	}
}

// Review r2, P1: census writes are ordered by their runs. The census is
// written after its pass has committed, so a newer pass can complete in
// between. Held before it writes, an older run's census never replaces the
// newer one's: it is superseded. Held while it writes, a newer run's census
// waits for it and lands after it.
func TestAnOlderCensusNeverReplacesANewerOne(t *testing.T) {
	for _, hold := range []string{"start", "locked"} {
		t.Run("held at "+hold, func(t *testing.T) {
			c := newEntityCatalog(t)
			small := c.seedCandidates()
			c.build()
			ctx := context.Background()
			admin := services.NewAuthorEntityAdmin(c.db)

			// Pass A, with a card that moves, is held after its apply.
			c.credit(c.book("Ещё рассказы"), small, "Стивен", "Кинг")
			runA, err := admin.Start(ctx, 0)
			require.NoError(t, err)
			held, release := make(chan struct{}), make(chan struct{})
			restore := services.SetAuthorEntityCensusHoldHook(func(run int64, step string) {
				if run == runA.ID && step == hold {
					close(held)
					<-release
				}
			})
			defer restore()
			doneA := make(chan error, 1)
			go func() {
				_, runErr := services.NewAuthorEntityBuildLoop(c.db, services.AuthorEntityBuildConfig{}).RunOnce(ctx)
				doneA <- runErr
			}()
			<-held

			// Pass B, over another change, runs on another worker.
			c.credit(c.book("Повести"), small, "Стивен", "Кинг")
			runB, err := admin.Start(ctx, 0)
			require.NoError(t, err)
			doneB := make(chan struct{})
			go func() {
				defer close(doneB)
				runToCompletion(t, services.NewAuthorEntityBuildLoop(c.db, services.AuthorEntityBuildConfig{}), c.db, runB.ID)
			}()
			if hold == "locked" {
				// B's census waits for A's lock (or, without one, lands at once).
				require.Eventually(t, func() bool {
					select {
					case <-doneB:
						return true
					default:
					}
					return c.count(`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`) > 0
				}, 30*time.Second, 20*time.Millisecond)
			} else {
				<-doneB
			}
			close(release)
			require.NoError(t, <-doneA)
			<-doneB

			// The stored census is B's: a pass over the same inputs finds
			// nothing to change.
			again := c.build()
			assert.Zero(t, again.CandidateDiff.Inserted, "an older census replaced the newer one")
			assert.Zero(t, again.CandidateDiff.Deleted, "an older census replaced the newer one")
			if hold == "start" {
				assert.Equal(t, services.AuthorEntityCensusErrorSuperseded, c.reportOf(runA.ID).CandidateError)
			}
		})
	}
}

// reportOf is a run's stored report.
func (c *entityCatalog) reportOf(run int64) services.AuthorEntityBuildReport {
	c.t.Helper()
	var raw json.RawMessage
	_, err := c.db.QueryOne(pg.Scan(&raw), `SELECT report FROM author_entity_build_run WHERE id = ?`, run)
	require.NoError(c.t, err)
	var rep services.AuthorEntityBuildReport
	require.NoError(c.t, json.Unmarshal(raw, &rep))
	return rep
}

// Review r2, P2: a census write that fails is recorded in the report even
// when the caller's context ends meanwhile.
func TestACensusFailureIsRecordedPastACancelledCaller(t *testing.T) {
	c := newEntityCatalog(t)
	c.seedCandidates()
	run, err := services.NewAuthorEntityAdmin(c.db).Start(context.Background(), 0)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	restore := services.SetAuthorEntityCensusHook(func(step string) error {
		if step == services.AuthorEntityCensusWrite {
			cancel()
			return errors.New("a census write that fails")
		}
		return nil
	})
	defer restore()
	_, err = services.NewAuthorEntityBuildLoop(c.db, services.AuthorEntityBuildConfig{}).RunOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, services.AuthorEntityCensusErrorWrite, c.reportOf(run.ID).CandidateError)
}
