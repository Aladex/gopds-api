package services_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// moveFixture is a written name of two people: «Алексей Толстой» on three
// books, built, its read model drained.
type moveFixture struct {
	c       *entityCatalog
	admin   *services.AuthorEntityAdmin
	credits []int64
}

func newMoveFixture(t *testing.T) *moveFixture {
	t.Helper()
	c := newEntityCatalog(t)
	for _, title := range []string{"Князь Серебряный", "Аэлита", "Гиперболоид"} {
		c.credit(c.book(title), 0x71, "Толстой", "Алексей")
	}
	c.build()
	f := &moveFixture{c: c, admin: services.NewAuthorEntityAdmin(c.db)}
	_, err := c.db.Query(&f.credits, `SELECT id FROM book_contributor_credit ORDER BY id`)
	require.NoError(t, err)
	require.Len(t, f.credits, 3)
	f.drain()
	c.exec(`DELETE FROM author_entity_summary_dirty`)
	return f
}

// drain runs the read model's marks out, as its worker does.
func (f *moveFixture) drain() {
	f.c.t.Helper()
	require.NoError(f.c.t, f.c.db.RunInTransaction(context.Background(), func(tx *pg.Tx) error {
		_, err := database.DrainAuthorDisplayMarks(context.Background(), tx, 1000)
		return err
	}))
}

// entityOfCredit is the entity the credit's own row names, or its
// fingerprint's.
func (f *moveFixture) entityOfCredit(credit int64) string {
	f.c.t.Helper()
	var id string
	_, err := f.c.db.QueryOne(pg.Scan(&id), `SELECT coalesce(ca.entity_id, a.entity_id)::text
		FROM book_contributor_credit c JOIN book_metadata_snapshot s ON s.id = c.snapshot_id
		LEFT JOIN author_entity_credit_assignment ca ON ca.credit_id = c.id AND ca.ordinal = 0
		LEFT JOIN author_entity_assignment a ON a.source_fingerprint = c.source_fingerprint
			AND a.extractor_version = s.extractor_version AND a.ordinal = 0
		WHERE c.id = ?`, credit)
	require.NoError(f.c.t, err)
	return id
}

// A hand move creates the card, marks both authors' summaries through the
// read model, survives every build, and its revoke hands the book back and
// lets the card go (D5).
func TestAHandMoveSurvivesABuildAndCanBeRevoked(t *testing.T) {
	f := newMoveFixture(t)
	ctx := context.Background()
	home := f.entityOfCredit(f.credits[1])
	card, err := f.admin.MoveCredits(ctx, &database.CreditMove{Credits: f.credits[:1],
		NewName: "Алексей Константинович Толстой"})
	require.NoError(t, err)
	assert.Equal(t, card.String(), f.entityOfCredit(f.credits[0]))
	assert.Positive(t, f.c.marks(), "the move asks for a pass")

	f.drain()
	assert.Equal(t, 2, f.c.count(`SELECT count(*) FROM author_entity_summary_dirty`), "both authors' summaries")

	f.c.build()
	f.c.build()
	assert.Equal(t, card.String(), f.entityOfCredit(f.credits[0]))
	assert.Equal(t, home, f.entityOfCredit(f.credits[1]))
	moves, err := f.admin.CreditMoves(ctx)
	require.NoError(t, err)
	require.Len(t, moves, 1)
	assert.Equal(t, f.credits[0], moves[0].CreditID)
	assert.Equal(t, card, moves[0].EntityID)

	n, err := f.admin.RevokeCreditMoves(ctx, f.credits[:1])
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	f.c.build()
	assert.Equal(t, home, f.entityOfCredit(f.credits[0]))
	assert.Zero(t, f.c.count(`SELECT count(*) FROM author_entity WHERE id = '`+card.String()+`' AND merged_into IS NULL`),
		"a card only the move held goes")
}

// A move is all or none: a bad credit leaves no row and no card.
func TestAHandMoveIsAllOrNone(t *testing.T) {
	f := newMoveFixture(t)
	ctx := context.Background()
	cards := f.c.count(`SELECT count(*) FROM author_entity`)
	_, err := f.admin.MoveCredits(ctx, &database.CreditMove{Credits: []int64{f.credits[0], 999999}, NewName: "Новый"})
	require.ErrorIs(t, err, database.ErrCreditMoveCredits)
	_, err = f.admin.MoveCredits(ctx, &database.CreditMove{Credits: f.credits[:1]})
	require.ErrorIs(t, err, database.ErrCreditMoveTarget)
	assert.Zero(t, f.c.count(`SELECT count(*) FROM author_entity_credit_assignment WHERE method = 'manual'`))
	assert.Equal(t, cards, f.c.count(`SELECT count(*) FROM author_entity`))

	_, err = f.admin.RevokeCreditMoves(ctx, f.credits[:1])
	require.ErrorIs(t, err, database.ErrCreditMoveNotLive)
}

// creditOf is the one credit of the fingerprint fill.
func creditOf(c *entityCatalog, fill byte) int64 {
	c.t.Helper()
	var id int64
	_, err := c.db.QueryOne(pg.Scan(&id), `SELECT id FROM book_contributor_credit WHERE source_fingerprint = ?`,
		bytes.Repeat([]byte{fill}, 32))
	require.NoError(c.t, err)
	return id
}

// A list under review is refused whether or not a build has reached it yet:
// admission reads the input, not what a build wrote.
func TestAHandMoveRefusesAListBeforeAndAfterItsFirstBuild(t *testing.T) {
	c := newEntityCatalog(t)
	c.credit(c.book("Known one"), 0x71, "Толстой", "Алексей")
	c.credit(c.book("Known two"), 0x72, "Петров", "Иван")
	c.credit(c.book("Two writers"), 0x73, "Толстой, Иван Петров", "Алексей")
	c.exec(`UPDATE book_contributor_credit_selection SET state = 'review', basis = NULL, policy_version = NULL
		WHERE source_fingerprint = ?`, bytes.Repeat([]byte{0x73}, 32))
	list := creditOf(c, 0x73)
	admin := services.NewAuthorEntityAdmin(c.db)
	ctx := context.Background()

	_, err := admin.MoveCredits(ctx, &database.CreditMove{Credits: []int64{list}, NewName: "Single target"})
	require.ErrorIs(t, err, database.ErrCreditMoveCredits, "before the first build")
	assert.Zero(t, c.count(`SELECT count(*) FROM author_entity_credit_assignment WHERE method = 'manual'`))

	c.build()
	_, err = admin.MoveCredits(ctx, &database.CreditMove{Credits: []int64{list}, NewName: "Single target"})
	require.ErrorIs(t, err, database.ErrCreditMoveCredits, "after the first build")
}

// Only a current selected person is movable, however its row came to be.
func TestAHandMoveRefusesACreditTheInputDoesNotHoldAsASelectedPerson(t *testing.T) {
	for _, tc := range []struct {
		name  string
		spoil string
	}{
		{"review", `UPDATE book_contributor_credit_selection SET state = 'review', basis = NULL, policy_version = NULL
			WHERE credit_id = ?`},
		{"unresolved", `UPDATE book_contributor_credit_selection SET state = 'unresolved', basis = NULL, unresolved_reason = 'llm_refused',
			policy_version = NULL WHERE credit_id = ?`},
		{"not current", `UPDATE book_metadata_snapshot SET is_current = false
			WHERE id = (SELECT snapshot_id FROM book_contributor_credit WHERE id = ?)`},
		{"a collective", `UPDATE contributor_normalization_result SET kind = 'collective',
			decision_class = 'collective', family_name = NULL, given_name = NULL WHERE id =
			(SELECT result_id FROM book_contributor_credit_selection WHERE credit_id = ?)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMoveFixture(t)
			f.c.exec(`ALTER TABLE contributor_normalization_result DISABLE TRIGGER USER`)
			f.c.exec(tc.spoil, f.credits[0])
			f.c.exec(`ALTER TABLE contributor_normalization_result ENABLE TRIGGER USER`)
			_, err := f.admin.MoveCredits(context.Background(),
				&database.CreditMove{Credits: f.credits[:2], NewName: "Новый"})
			require.ErrorIs(t, err, database.ErrCreditMoveCredits)
			assert.Zero(t, f.c.count(`SELECT count(*) FROM author_entity_credit_assignment WHERE method = 'manual'`))
		})
	}
}

// A selection change in flight decides the admission: the move waits for it
// and then reads what it left.
func TestAHandMoveWaitsForASelectionInFlight(t *testing.T) {
	f := newMoveFixture(t)
	ctx := context.Background()
	tx, err := f.c.db.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Close() }()
	_, err = tx.Exec(`UPDATE book_contributor_credit_selection SET state = 'review', basis = NULL, policy_version = NULL
		WHERE credit_id = ?`, f.credits[0])
	require.NoError(t, err)

	moved := make(chan error, 1)
	go func() {
		_, err := f.admin.MoveCredits(ctx, &database.CreditMove{Credits: f.credits[:1], NewName: "Новый"})
		moved <- err
	}()
	require.Eventually(t, func() bool {
		return f.c.count(`SELECT count(*) FROM pg_locks WHERE NOT granted`) > 0
	}, 5*time.Second, 10*time.Millisecond, "the move did not wait for the selection")
	require.NoError(t, tx.Commit())
	require.ErrorIs(t, <-moved, database.ErrCreditMoveCredits)
}

// A move inside a build's window is refused: the plan is made and would
// merge the target away, whichever the plan's decisions are. This is the
// review's interleaving, a deterministic merge and no applied decision.
func TestAHandMoveIsRefusedWhileABuildIsActive(t *testing.T) {
	f := newSplitAndMerge(t)
	c := f.c
	c.credit(c.book("Unrelated book"), 0x74, "Сидоров", "Сергей")
	credit := creditOf(c, 0x74)
	f.evolve()
	target := uuid.MustParse(f.idX)
	admin := services.NewAuthorEntityAdmin(c.db)
	ctx := context.Background()

	var duringPlan error
	restore := services.SetAuthorEntityHooks(func() {
		_, duringPlan = admin.MoveCredits(ctx, &database.CreditMove{Credits: []int64{credit}, Entity: &target})
	}, nil)
	defer restore()
	c.build()
	require.ErrorIs(t, duringPlan, database.ErrAuthorEntityBuildActive)
	assert.Zero(t, c.count(`SELECT count(*) FROM author_entity_credit_assignment WHERE method = 'manual'`))
	assert.Equal(t, 1, c.count(`SELECT count(*) FROM author_entity WHERE id = '`+f.idX+`' AND merged_into = '`+f.idY+`'`),
		"the build this move stayed out of merges X")

	run, err := admin.Start(ctx, 0)
	require.NoError(t, err)
	live := uuid.MustParse(f.idY)
	_, err = admin.MoveCredits(ctx, &database.CreditMove{Credits: []int64{credit}, Entity: &live})
	require.ErrorIs(t, err, database.ErrAuthorEntityBuildActive, "a pending pass is a build too")
	runToCompletion(t, services.NewAuthorEntityBuildLoop(c.db, services.AuthorEntityBuildConfig{}), c.db, run.ID)
}

// A build that starts while a move is past its check waits for the move, so
// its plan holds the manual row and keeps the target: nothing merges it
// away, in this pass or the next.
func TestABuildStartWaitsForAHandMoveAndKeepsItsTarget(t *testing.T) {
	f := newSplitAndMerge(t)
	c := f.c
	c.credit(c.book("Unrelated book"), 0x74, "Сидоров", "Сергей")
	credit := creditOf(c, 0x74)
	f.evolve()
	target := uuid.MustParse(f.idX)
	admin := services.NewAuthorEntityAdmin(c.db)
	ctx := context.Background()

	tx, err := c.db.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Close() }()
	_, err = database.MoveCredits(ctx, tx, &database.CreditMove{Credits: []int64{credit}, Entity: &target})
	require.NoError(t, err)

	type started struct {
		run *database.AuthorEntityBuildRun
		err error
	}
	start := make(chan started, 1)
	go func() {
		run, err := admin.Start(ctx, 0)
		start <- started{run, err}
	}()
	require.Eventually(t, func() bool { return waitingOnStartLock(t, c.db) }, 5*time.Second, 10*time.Millisecond,
		"the start did not wait for the move")
	require.NoError(t, tx.Commit())
	got := <-start
	require.NoError(t, got.err)
	runToCompletion(t, services.NewAuthorEntityBuildLoop(c.db, services.AuthorEntityBuildConfig{}), c.db, got.run.ID)
	require.NoError(t, database.SettleAuthorAutopilotMarks(ctx, c.db))
	c.build()

	assert.Equal(t, 1, c.count(`SELECT count(*) FROM author_entity WHERE id = '`+f.idX+`' AND merged_into IS NULL`),
		"the move's target stays live")
	assert.Equal(t, 1, c.count(`SELECT count(*) FROM author_entity_credit_assignment
		WHERE method = 'manual' AND entity_id = '`+f.idX+`'`))
}
