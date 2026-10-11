package database

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The anchor-candidate query must not get a nested-loop plan: on the
// production catalog the planner estimates its filtered credits at one row
// and nests loops over the CTE scans, and the query never finished (30 min);
// without nested loops it takes seconds. The guard is enable_nestloop = off
// in effect while the query runs — on whatever connection runs it — and
// restored afterwards.

// nestloopProbe records enable_nestloop as it is when the anchor query is
// sent, on the query's own connection or transaction.
type nestloopProbe struct {
	mu      sync.Mutex
	seen    []string
	probing bool
}

func (p *nestloopProbe) BeforeQuery(ctx context.Context, ev *pg.QueryEvent) (context.Context, error) {
	q, err := ev.FormattedQuery()
	if err != nil || !strings.Contains(string(q), "f_as_first") {
		return ctx, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.probing {
		return ctx, nil
	}
	p.probing = true
	var setting string
	_, probeErr := ev.DB.QueryOneContext(ctx, pg.Scan(&setting), `SHOW enable_nestloop`)
	p.probing = false
	if probeErr != nil {
		setting = "probe failed: " + probeErr.Error()
	}
	p.seen = append(p.seen, setting)
	return ctx, nil
}

func (p *nestloopProbe) AfterQuery(context.Context, *pg.QueryEvent) error { return nil }

func TestAuthorLLMAnchorCandidatesRunWithoutNestedLoops(t *testing.T) {
	requireDatabase(t)
	ctx := context.Background()
	conn := pg.Connect(db.Options())
	t.Cleanup(func() { _ = conn.Close() })
	probe := &nestloopProbe{}
	conn.AddQueryHook(probe)

	// On a pool: the query runs in a transaction of its own.
	_, err := AuthorLLMAnchorCandidates(ctx, conn, 1, 95)
	require.NoError(t, err)

	// Inside a caller's transaction (the read-only sampling check): the same
	// guard, and the caller's own value comes back for its next statements —
	// whatever it was, not the default.
	for _, prior := range []string{"on", "off"} {
		require.NoError(t, conn.RunInTransaction(ctx, func(tx *pg.Tx) error {
			if _, txErr := tx.Exec(`SET TRANSACTION READ ONLY`); txErr != nil {
				return txErr
			}
			if _, txErr := tx.Exec(`SET LOCAL enable_nestloop = ` + prior); txErr != nil {
				return txErr
			}
			if _, txErr := AuthorLLMAnchorCandidates(ctx, tx, 1, 95); txErr != nil {
				return txErr
			}
			var after string
			if _, txErr := tx.QueryOne(pg.Scan(&after), `SHOW enable_nestloop`); txErr != nil {
				return txErr
			}
			assert.Equal(t, prior, after, "the caller's enable_nestloop was not restored")
			return nil
		}), prior)
	}
	assert.Equal(t, []string{"off", "off", "off"}, probe.seen, "the anchor query ran with nested loops")

	var pooled string
	_, err = conn.QueryOne(pg.Scan(&pooled), `SHOW enable_nestloop`)
	require.NoError(t, err)
	assert.Equal(t, "on", pooled, "the guard leaked into the pool's session")
}
