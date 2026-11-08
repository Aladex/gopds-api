package database

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A grouped list holds one pooled connection for its snapshot. Whatever its
// scope reads off the package handle must be read before that connection is
// taken: a request that holds one connection and waits for a second starves
// the pool, and with the pool full every such request times out.

// poolScopes is a favorites list and a curated collection, each holding one
// card of three files, committed; the fixture's own transaction is closed so
// the lists' pool below is the only one in use.
func poolScopes(t *testing.T) (f *cardsFixture, user, curated int64) {
	t.Helper()
	f = cardsDB(t)
	files := f.group3(f.entity("Толкин"), "Хоббит")
	user = f.user()
	f.star(user, files[1])
	f.star(user, files[2])
	curated = f.id()
	f.exec(`INSERT INTO book_collections (id, name, is_public, is_curated) VALUES (?, 'Сказки', true, true)`, curated)
	f.exec(`INSERT INTO book_collection_items (collection_id, book_id, external_title, match_status, position)
		VALUES (?, ?, 'Хоббит', ?, 1)`, curated, files[2], models.MatchStatusManual)
	require.NoError(t, f.tx.Commit())
	return f, user, curated
}

// smallPool points the package handle at a pool of size connections that
// gives up waiting for one quickly, for the rest of the test.
func smallPool(t *testing.T, f *cardsFixture, size int) *pg.DB {
	t.Helper()
	opt := *f.scratch.Options()
	opt.PoolSize = size
	opt.MinIdleConns = 0
	opt.PoolTimeout = 2 * time.Second
	small := pg.Connect(&opt)
	t.Cleanup(func() { _ = small.Close() })
	SetDB(small)
	return small
}

func TestBookCardsScopesFitOneConnection(t *testing.T) {
	f, user, curated := poolScopes(t)
	smallPool(t, f, 1)

	for name, filters := range map[string]models.BookFilters{
		"favorites": {Fav: true, Limit: 10},
		"curated":   {CuratedCollection: curated, Limit: 10},
	} {
		t.Run(name, func(t *testing.T) {
			books, _, total, err := GetBookCards(context.Background(), user, filters)
			require.NoError(t, err, "the scope is read before the snapshot takes the only connection")
			assert.Equal(t, 1, total)
			assert.Len(t, books, 1)
		})
	}
}

// afterBegin holds every grouped read right after its transaction began,
// until n of them have begun, so n requests each hold a connection at once.
type afterBegin struct {
	n       int
	mu      sync.Mutex
	arrived int
	all     chan struct{}
}

func (h *afterBegin) BeforeQuery(ctx context.Context, ev *pg.QueryEvent) (context.Context, error) {
	query, err := ev.FormattedQuery()
	if err != nil || !strings.HasPrefix(string(query), "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ") {
		return ctx, nil //nolint:nilerr // a query the hook cannot read is not one it waits on
	}
	h.mu.Lock()
	h.arrived++
	if h.arrived == h.n {
		close(h.all)
	}
	h.mu.Unlock()
	select {
	case <-h.all:
	case <-time.After(5 * time.Second):
	}
	return ctx, nil
}

func (h *afterBegin) AfterQuery(context.Context, *pg.QueryEvent) error { return nil }

func TestBookCardsConcurrentFavoritesFillThePool(t *testing.T) {
	f, user, _ := poolScopes(t)
	small := smallPool(t, f, 2)
	hook := &afterBegin{n: 2, all: make(chan struct{})}
	small.AddQueryHook(hook)

	errs := make([]error, 2)
	totals := make([]int, 2)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, totals[i], errs[i] = GetBookCards(context.Background(), user, models.BookFilters{Fav: true, Limit: 10})
		}(i)
	}
	wg.Wait()
	select {
	case <-hook.all:
	default:
		t.Fatal("the two requests never held their snapshots at once")
	}
	for i := range errs {
		require.NoError(t, errs[i], "request %d: two snapshots fill a pool of two and need nothing more", i)
		assert.Equal(t, 1, totals[i])
	}
}
