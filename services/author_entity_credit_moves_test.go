package services_test

import (
	"context"
	"testing"

	"gopds-api/database"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
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
