package database

import (
	"context"
	"strings"
	"testing"
	"time"

	"gopds-api/internal/authorentity"
	"gopds-api/internal/authornorm"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The author entity tables (migration 56) and what a build pass reads and
// writes through them.

// entityBook is a book with a current snapshot, a title and its series.
func (f *authorSchemaFixture) entityBook(title string, series ...int64) (book, snapshot int64) {
	f.t.Helper()
	book = f.book()
	f.exec(`UPDATE opds_catalog_book SET title = ? WHERE id = ?`, title, book)
	for _, s := range series {
		f.exec(`INSERT INTO opds_catalog_bseries (ser_no, book_id, ser_id) VALUES (0, ?, ?)`, book, s)
	}
	return book, f.snapshot(&snapshotSpec{book: book})
}

// entityCredit writes an author credit of the snapshot whose fingerprint is
// fill, selected with a person result of the parts; one result per
// fingerprint.
func (f *authorSchemaFixture) entityCredit(snapshot int64, fill byte, family, given string, additional ...string) int64 {
	f.t.Helper()
	fp := fingerprint(32, fill)
	display := strings.Join(append(append([]string{given}, additional...), family), " ")
	credit := f.returningID(`INSERT INTO book_contributor_credit
		(snapshot_id, role, position, source_first_name, source_last_name, source_display_name, source_fingerprint)
		VALUES (?, 'author', 0, ?, ?, ?, ?) RETURNING id`, snapshot, given, family, display, fp)
	var result int64
	_, err := f.tx.Query(pg.Scan(&result), `SELECT id FROM contributor_normalization_result
		WHERE source_fingerprint = ? AND method = 'rules' ORDER BY id LIMIT 1`, fp)
	require.NoError(f.t, err)
	if result == 0 {
		result = f.returningID(`INSERT INTO contributor_normalization_result
			(source_fingerprint, normalization_key, extractor_version, normalizer_version, result_schema_version,
			 method, kind, status, decision_class, family_name, given_name, additional_names,
			 display_name, sort_name, search_key, script)
			VALUES (?, ?, 'extractor-v1', 'normalizer-v1', 'result-v1', 'rules', 'person', 'normalized',
			 'structured_person', ?, ?, coalesce(?::text[], '{}'), ?, ?, ?, 'Cyrl') RETURNING id`,
			fp, keyFor(string(fp), "extractor-v1", "normalizer-v1"), family, given, pg.Array(additional),
			display, display, authornorm.SearchKey(display))
	}
	f.exec(selectionInsert, credit, fp, "selected", result, "automatic", nil, "4", nil, nil)
	return credit
}

// entitySchema is a fixture with the selection policy the credits rest on.
func entitySchema(t *testing.T) *authorSchemaFixture {
	f := withAuthorSchemaTx(t)
	f.registerPair("4", "structured_person", "normalizer-v1")
	// The tables hold what other tests committed: the build reads them all.
	f.exec(`TRUNCATE author_entity_credit_assignment, author_entity_assignment, author_entity_name,
		author_entity_merge_log, author_entity_build_run, author_entity CASCADE`)
	return f
}

// pass plans over what the fixture holds and writes it.
func (f *authorSchemaFixture) pass() (*authorentity.Plan, int, []AuthorEntityUnitSkip) {
	f.t.Helper()
	ctx := context.Background()
	in, err := LoadAuthorEntityInput(ctx, f.tx)
	require.NoError(f.t, err)
	in.Old, err = LoadAuthorEntityState(ctx, f.tx)
	require.NoError(f.t, err)
	p := authorentity.Build(in)
	changes, skipped, err := ApplyAuthorEntityUnits(ctx, f.tx, 1, p.Components())
	require.NoError(f.t, err)
	return p, changes, skipped
}

// fixtureCredits narrows a loaded input to the fixture's credits: the
// database may hold other tests' committed rows.
func fixtureCredits(in *authorentity.Input, ids ...int64) map[int64]authorentity.Credit {
	want := map[int64]bool{}
	for _, id := range ids {
		want[id] = true
	}
	out := map[int64]authorentity.Credit{}
	for _, c := range in.Credits {
		if want[c.ID] {
			out[c.ID] = c
		}
	}
	return out
}

// The input is the selected author credits of the current snapshots with
// their results, books, series, namesake tags and legacy matches; a credit
// of an old snapshot, an unselected one and a translator are not.
func TestTheBuildReadsTheSelectedAuthorCreditsOfCurrentSnapshots(t *testing.T) {
	f := entitySchema(t)
	book, snap := f.entityBook("Война и мир", 701, 702)
	tolstoy := f.entityCredit(snap, 0xe1, "Толстой", "Лев", "Николаевич")
	tagged := f.returningID(`INSERT INTO book_contributor_credit
		(snapshot_id, role, position, source_first_name, source_last_name, source_display_name, source_fingerprint)
		VALUES (?, 'author', 1, 'Алексей', 'Иванов (2)', 'Алексей Иванов (2)', ?) RETURNING id`, snap, fingerprint(32, 0xe2))
	f.exec(`INSERT INTO contributor_normalization_result
		(source_fingerprint, normalization_key, extractor_version, normalizer_version, result_schema_version,
		 method, kind, status, decision_class, family_name, given_name, display_name, sort_name, search_key, script)
		VALUES (?, ?, 'extractor-v1', 'normalizer-v1', 'result-v1', 'rules', 'person', 'normalized',
		 'structured_person', 'Иванов', 'Алексей', 'Алексей Иванов', 'Иванов Алексей', 'алексей иванов', 'Cyrl')`,
		fingerprint(32, 0xe2), keyFor(string(fingerprint(32, 0xe2)), "extractor-v1", "normalizer-v1"))
	var result int64
	_, err := f.tx.QueryOne(pg.Scan(&result), `SELECT id FROM contributor_normalization_result WHERE source_fingerprint = ?`,
		fingerprint(32, 0xe2))
	require.NoError(t, err)
	f.exec(selectionInsert, tagged, fingerprint(32, 0xe2), "selected", result, "automatic", nil, "4", nil, nil)
	review := f.displayCredit(snap, "author", 2, "Коллектив авторов", 0xe3)
	f.exec(selectionInsert, review, fingerprint(32, 0xe3), "review", nil, nil, nil, nil, nil, nil)
	f.displayCredit(snap, "translator", 3, "Переводчик Тот", 0xe4)
	old, oldSnap := f.entityBook("Старое издание")
	f.exec(`UPDATE book_metadata_snapshot SET is_current = false WHERE id = ?`, oldSnap)
	gone := f.entityCredit(oldSnap, 0xe5, "Толстой", "Лев", "Николаевич")
	legacy := f.legacyAuthorOf(book, "Толстой Лев")
	f.exec(`INSERT INTO book_author_display (book_id, position, display, sort_key, search_key, source, legacy_author_id, credit_id)
		VALUES (?, 0, 'Лев Николаевич Толстой', 'толстой', 'лев николаевич толстой', 'layer', ?, ?)`, book, legacy, tolstoy)
	_ = old

	in, err := LoadAuthorEntityInput(context.Background(), f.tx)
	require.NoError(t, err)
	got := fixtureCredits(in, tolstoy, tagged, review, gone)
	require.Len(t, got, 2)
	c := got[tolstoy]
	assert.Equal(t, book, c.BookID)
	assert.Equal(t, "Толстой", c.Family)
	assert.Equal(t, []string{"Николаевич"}, c.Additional)
	assert.Equal(t, "extractor-v1", c.ExtractorVersion)
	assert.Equal(t, string(fingerprint(32, 0xe1)), c.Fingerprint)
	assert.Equal(t, 0, c.Tag)
	assert.Equal(t, 2, got[tagged].Tag, "the namesake tag of the source fields")
	assert.Equal(t, authorentity.Book{Title: "Война и мир", Series: []int64{701, 702}}, in.Books[book])
	assert.Equal(t, legacy, in.Legacy[tolstoy])

	unselected, err := CountUnselectedAuthorCredits(context.Background(), f.tx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, unselected, 1, "the review credit is counted")
}

// A pass writes the entities, assignments and names; a second pass over the
// same rows writes nothing and keeps every ID.
func TestABuildPassIsIdempotentOverTheDatabase(t *testing.T) {
	f := entitySchema(t)
	_, s1 := f.entityBook("Мост", 801)
	_, s2 := f.entityBook("Река", 801)
	_, s3 := f.entityBook("Лес")
	full := f.entityCredit(s1, 0xd1, "Иванов", "Алексей", "Викторович")
	moved := f.entityCredit(s2, 0xd2, "Иванов", "Алексей")
	bucket := f.entityCredit(s3, 0xd2, "Иванов", "Алексей")

	first, changes, skipped := f.pass()
	require.Empty(t, skipped)
	require.Positive(t, changes)
	assert.Equal(t, first.EntityOf(full), first.EntityOf(moved), "series evidence moves the short book")
	assert.NotEqual(t, first.EntityOf(full), first.EntityOf(bucket))

	for _, id := range []uuid.UUID{first.EntityOf(full), first.EntityOf(bucket)} {
		assert.Equal(t, 1, f.count(`SELECT count(*) FROM author_entity WHERE id = ?`, id))
	}
	assert.Equal(t, 2, f.count(`SELECT count(*) FROM author_entity_assignment
		WHERE source_fingerprint IN (?, ?) AND method = 'deterministic' AND rule = 'D1'`,
		fingerprint(32, 0xd1), fingerprint(32, 0xd2)))
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM author_entity_credit_assignment
		WHERE credit_id IN (?, ?, ?)`, full, moved, bucket))
	var rule string
	_, err := f.tx.QueryOne(pg.Scan(&rule), `SELECT rule FROM author_entity_credit_assignment WHERE credit_id = ?`, moved)
	require.NoError(t, err)
	assert.Equal(t, "D3:series", rule)

	second, changes, skipped := f.pass()
	assert.Empty(t, skipped)
	assert.Zero(t, changes, "a second pass writes nothing")
	assert.Empty(t, second.Units)
	for _, id := range []int64{full, moved, bucket} {
		assert.Equal(t, first.EntityOf(id), second.EntityOf(id))
	}
}

// A component the database refuses (here a one-unit one) is skipped alone,
// by its ID and SQLSTATE; the rest of the batch is written.
func TestAUnitTheDatabaseRefusesIsSkippedAlone(t *testing.T) {
	f := entitySchema(t)
	_, s1 := f.entityBook("Мост")
	_, s2 := f.entityBook("Река")
	poisoned := f.entityCredit(s1, 0xc1, "Отравленный", "Иван", "Петрович")
	fine := f.entityCredit(s2, 0xc2, "Петров", "Иван", "Сергеевич")
	f.exec(`ALTER TABLE author_entity ADD CONSTRAINT poisoned_entity
		CHECK (display_name <> 'Иван Петрович Отравленный') NOT VALID`)

	p, _, skipped := f.pass()
	require.Len(t, skipped, 1)
	assert.Equal(t, p.EntityOf(poisoned), skipped[0].EntityID)
	assert.Equal(t, "23514", skipped[0].SQLState)
	assert.Equal(t, fingerprint(32, 0xc1), skipped[0].Fingerprint)
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM author_entity WHERE id = ?`, p.EntityOf(fine)))
	assert.Zero(t, f.count(`SELECT count(*) FROM author_entity WHERE id = ?`, p.EntityOf(poisoned)))
}

// The database layer never replaces a manual row, whatever a unit says.
func TestAUnitNeverReplacesAManualRow(t *testing.T) {
	f := entitySchema(t)
	_, s1 := f.entityBook("Мост")
	f.entityCredit(s1, 0xb1, "Петров", "Иван", "Сергеевич")
	manual := uuid.Must(uuid.NewV7())
	f.exec(`INSERT INTO author_entity (id, kind, display_name, sort_name, search_key, name_pinned)
		VALUES (?, 'person', 'Иван Петров (2)', 'Петров Иван (2)', 'иван петров 2', true)`, manual)
	f.exec(`INSERT INTO author_entity_assignment (source_fingerprint, extractor_version, ordinal, entity_id, method, rule)
		VALUES (?, 'extractor-v1', 0, ?, 'manual', 'manual')`, fingerprint(32, 0xb1), manual)

	unit := authorentity.Unit{EntityID: uuid.Must(uuid.NewV7()),
		Entity: &authorentity.EntityRow{Kind: "person", DisplayName: "Иван Сергеевич Петров", SortName: "Петров",
			SearchKey: "иван сергеевич петров"},
		Fingerprints: []authorentity.AssignmentRow{{Key: authorentity.FPKey{Fingerprint: string(fingerprint(32, 0xb1)),
			ExtractorVersion: "extractor-v1"}, Method: authorentity.MethodDeterministic, Rule: authorentity.RuleD1}},
		DeleteFingerprints: []authorentity.FPKey{{Fingerprint: string(fingerprint(32, 0xb1)), ExtractorVersion: "extractor-v1"}},
	}
	unit.Fingerprints[0].EntityID = unit.EntityID
	_, skipped, err := ApplyAuthorEntityUnits(context.Background(), f.tx, 1, [][]authorentity.Unit{{unit}})
	require.NoError(t, err)
	require.Empty(t, skipped)
	var row struct {
		EntityID uuid.UUID `pg:"entity_id,type:uuid"`
		Method   string
	}
	_, err = f.tx.QueryOne(&row, `SELECT entity_id, method FROM author_entity_assignment WHERE source_fingerprint = ?`,
		fingerprint(32, 0xb1))
	require.NoError(t, err)
	assert.Equal(t, manual, row.EntityID)
	assert.Equal(t, "manual", row.Method)
}

// A merge marks the absorbed entity and logs it with the pass and the
// shared books.
func TestAMergeIsMarkedAndLogged(t *testing.T) {
	f := entitySchema(t)
	_, s1 := f.entityBook("А")
	_, s2 := f.entityBook("Б")
	_, s3 := f.entityBook("В")
	big := f.entityCredit(s1, 0xa1, "Толстой", "Лев", "Николаевич")
	f.entityCredit(s2, 0xa1, "Толстой", "Лев", "Николаевич")
	small := f.entityCredit(s3, 0xa2, "Толстой", "Лев", "Ильич")
	first, _, _ := f.pass()
	survivor, absorbed := first.EntityOf(big), first.EntityOf(small)
	// fp 0xa2 now reads as the same patronymic: one entity.
	f.exec(`ALTER TABLE contributor_normalization_result DISABLE TRIGGER USER`)
	f.exec(`UPDATE contributor_normalization_result SET additional_names = '{Николаевич}' WHERE source_fingerprint = ?`,
		fingerprint(32, 0xa2))
	f.exec(`ALTER TABLE contributor_normalization_result ENABLE TRIGGER USER`)

	second, _, skipped := f.pass()
	require.Empty(t, skipped)
	assert.Equal(t, survivor, second.EntityOf(small))
	var merged string
	_, err := f.tx.QueryOne(pg.Scan(&merged), `SELECT coalesce(merged_into::text, '') FROM author_entity WHERE id = ?`, absorbed)
	require.NoError(t, err)
	assert.Equal(t, survivor.String(), merged)
	var shared int
	_, err = f.tx.QueryOne(pg.Scan(&shared), `SELECT (evidence->>'shared_books')::int FROM author_entity_merge_log
		WHERE from_id = ? AND to_id = ? AND method = 'deterministic' AND (evidence->>'build_run_id')::bigint = 1`,
		absorbed, survivor)
	require.NoError(t, err)
	assert.Equal(t, 1, shared)
}

// Deleting a book takes its credits' decisions with them: the layer delete
// is not blocked by the entity tables.
func TestDeletingABookTakesItsCreditDecisions(t *testing.T) {
	f := entitySchema(t)
	_, s1 := f.entityBook("Мост", 901)
	book, s2 := f.entityBook("Река", 901)
	f.entityCredit(s1, 0x91, "Иванов", "Алексей", "Викторович")
	moved := f.entityCredit(s2, 0x92, "Иванов", "Алексей")
	f.pass()
	require.Equal(t, 1, f.count(`SELECT count(*) FROM author_entity_credit_assignment WHERE credit_id = ?`, moved))
	f.exec(`SELECT author_layer_delete_books(ARRAY[?]::bigint[])`, book)
	assert.Zero(t, f.count(`SELECT count(*) FROM author_entity_credit_assignment WHERE credit_id = ?`, moved))
}

// A pass is pending, then running under one worker's lease, then completed
// with its report; a second pass while one is active is refused.
func TestTheBuildRunLifecycle(t *testing.T) {
	f := entitySchema(t)
	ctx := context.Background()
	admin := f.user()
	run, err := StartAuthorEntityBuild(ctx, f.tx, admin)
	require.NoError(t, err)
	assert.Equal(t, AuthorEntityRunPending, run.Status)
	_, err = StartAuthorEntityBuild(ctx, f.tx, admin)
	require.ErrorIs(t, err, ErrAuthorEntityBuildActive)

	owner, other := uuid.New(), uuid.New()
	claimed, err := ClaimAuthorEntityBuild(ctx, f.tx, owner, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.Equal(t, run.ID, claimed.ID)
	assert.Equal(t, AuthorEntityRunRunning, claimed.Status)
	held, err := ClaimAuthorEntityBuild(ctx, f.tx, other, time.Minute)
	require.NoError(t, err)
	assert.Nil(t, held, "another worker's lease holds the pass")

	require.NoError(t, RecordAuthorEntityProgress(ctx, f.tx, run.ID, owner, time.Minute,
		AuthorEntityProgress{Stage: AuthorEntityStageApply, UnitsTotal: 10, UnitsDone: 5, Changes: 7}))
	err = RecordAuthorEntityProgress(ctx, f.tx, run.ID, other, time.Minute, AuthorEntityProgress{Stage: AuthorEntityStageApply})
	require.ErrorIs(t, err, ErrAuthorEntityLeaseLost)

	require.NoError(t, CompleteAuthorEntityBuild(ctx, f.tx, run.ID, owner,
		AuthorEntityProgress{Stage: AuthorEntityStageReport, UnitsTotal: 10, UnitsDone: 10, Changes: 9},
		[]byte(`{"entities": 3}`)))
	latest, completed, err := LatestAuthorEntityBuilds(ctx, f.tx)
	require.NoError(t, err)
	require.NotNil(t, latest)
	require.NotNil(t, completed)
	assert.Equal(t, run.ID, latest.ID)
	assert.Equal(t, run.ID, completed.ID)
	assert.Equal(t, 10, latest.UnitsDone)
	assert.Equal(t, int64(9), latest.Changes)
	assert.JSONEq(t, `{"entities": 3}`, string(completed.Report))

	next, err := StartAuthorEntityBuild(ctx, f.tx, 0)
	require.NoError(t, err, "a completed pass leaves room for the next")
	latest, completed, err = LatestAuthorEntityBuilds(ctx, f.tx)
	require.NoError(t, err)
	assert.Equal(t, next.ID, latest.ID)
	assert.Equal(t, run.ID, completed.ID)
}

// A selected collective — a name and no person parts, the shape the
// normalizer gives anything but a person — is read with its kind and built
// into an entity of kind collective.
func TestASelectedCollectiveIsBuiltFromTheDatabase(t *testing.T) {
	f := entitySchema(t)
	_, snap := f.entityBook("Выпуск 1")
	credit := f.returningID(`INSERT INTO book_contributor_credit
		(snapshot_id, role, position, source_last_name, source_display_name, source_fingerprint)
		VALUES (?, 'author', 0, 'Журнал Наука', 'Журнал Наука', ?) RETURNING id`, snap, fingerprint(32, 0x81))
	result := f.returningID(`INSERT INTO contributor_normalization_result
		(source_fingerprint, normalization_key, extractor_version, normalizer_version, result_schema_version,
		 method, kind, status, decision_class, display_name, sort_name, search_key, script)
		VALUES (?, ?, 'extractor-v1', 'normalizer-v1', 'result-v1', 'rules', 'collective', 'normalized',
		 'structured_person', 'Журнал Наука', 'Журнал Наука', 'журнал наука', 'Cyrl') RETURNING id`,
		fingerprint(32, 0x81), keyFor(string(fingerprint(32, 0x81)), "extractor-v1", "normalizer-v1"))
	f.exec(selectionInsert, credit, fingerprint(32, 0x81), "selected", result, "automatic", nil, "4", nil, nil)

	p, _, skipped := f.pass()
	require.Empty(t, skipped)
	id := p.EntityOf(credit)
	require.NotEqual(t, uuid.Nil, id)
	var kind string
	_, err := f.tx.QueryOne(pg.Scan(&kind), `SELECT kind FROM author_entity WHERE id = ?`, id)
	require.NoError(t, err)
	assert.Equal(t, "collective", kind)
}
