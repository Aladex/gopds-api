package services

// preview_service_test.go pins the gate: visibility, format and existence
// checks all fire before the archive is touched. Each test uses a mock
// loader that counts its own calls — the assertion is "the disk was not
// touched", not just "an error came back". A future change that reorders
// the checks (e.g. loads before checking visibility) would still produce
// the right error but would have done the work, and only the call count
// catches that.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"gopds-api/internal/converter"
	"gopds-api/internal/parser"
	"gopds-api/models"
)

// minimalFB2 is the smallest valid FictionBook document the parser accepts:
// a root <FictionBook> element with one <body> containing one <section>. Used
// by tests that need the build pipeline to succeed without caring about the
// content — every test fixture that feeds the loader uses this instead of a
// bare tag, because the gate-checking parse step rejects anything that is
// not a real FB2.
const minimalFB2 = `<?xml version="1.0"?>` +
	`<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">` +
	`<body><section><p>t</p></section></body></FictionBook>`

// fakeBookRepo is an in-memory stand-in for BookRepo. It returns (nil, nil)
// for a missing id, matching the contract: "not found" is nil without an
// error, not an error the caller has to disentangle from "database is down".
type fakeBookRepo struct {
	books map[int64]*models.Book
	err   error // if set, every GetBook call fails
}

func (r *fakeBookRepo) GetBook(bookID int64) (*models.Book, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.books[bookID], nil
}

// fakeArchiveLoader counts how many times Load was called. That count is the
// assertion in every test below: it has to be zero for any refusal, because
// the refusal must fire before the loader.
type fakeArchiveLoader struct {
	calls int
	data  []byte
	err   error
}

func (l *fakeArchiveLoader) Load(_ context.Context, _, _ string) ([]byte, error) {
	l.calls++
	return l.data, l.err
}

// mockPreviewCache is an in-memory PreviewCache for tests. It stores
// manifests and chunks in maps, counts calls, and records the order of Put
// operations so test 13 can assert "chunks before manifest". A mutex
// guards every field because concurrent goroutines (singleflight tests)
// call Get/Put simultaneously — without it, the race detector flags every
// map access.
type mockPreviewCache struct {
	mu              sync.Mutex
	pingErr         error
	manifests       map[string][]byte
	chunks          map[string]map[int][]byte
	putOrder        []string
	getManifestKeys []string
	getChunkKeys    []string
}

func newMockCache() *mockPreviewCache {
	return &mockPreviewCache{
		manifests: map[string][]byte{},
		chunks:    map[string]map[int][]byte{},
	}
}

func (c *mockPreviewCache) Ping(_ context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pingErr
}

func (c *mockPreviewCache) GetManifest(_ context.Context, key string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.getManifestKeys = append(c.getManifestKeys, key)
	data, ok := c.manifests[key]
	if !ok {
		return nil, ErrCacheMiss
	}
	return data, nil
}

func (c *mockPreviewCache) PutManifest(_ context.Context, key string, data []byte, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.putOrder = append(c.putOrder, "manifest:"+key)
	c.manifests[key] = data
	return nil
}

func (c *mockPreviewCache) GetChunk(_ context.Context, key string, index int) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.getChunkKeys = append(c.getChunkKeys, key)
	chunks, ok := c.chunks[key]
	if !ok {
		return nil, ErrCacheMiss
	}
	data, ok := chunks[index]
	if !ok {
		return nil, ErrCacheMiss
	}
	return data, nil
}

func (c *mockPreviewCache) PutChunk(_ context.Context, key string, index int, data []byte, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.putOrder = append(c.putOrder, "chunk:"+key)
	if c.chunks[key] == nil {
		c.chunks[key] = map[int][]byte{}
	}
	c.chunks[key][index] = data
	return nil
}

// A hidden book must not touch the archive. The assertion is not "an error
// came back" — that could happen after the disk was read. It is "the loader
// was called zero times", which proves the check fired before any work.
func TestPreviewService_HiddenBookDoesNotTouchArchive(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, DuplicateHidden: true, Approved: true},
	}}
	loader := &fakeArchiveLoader{}
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits())

	_, err := svc.Load(context.Background(), 1, false)
	if !errors.Is(err, ErrBookNotVisible) {
		t.Fatalf("err = %v, want ErrBookNotVisible", err)
	}
	if loader.calls != 0 {
		t.Errorf("loader was called %d times for a hidden book, want 0 — "+
			"the visibility check must fire before the archive is touched",
			loader.calls)
	}
}

// A superuser opens an unapproved, hidden book; a regular reader does not.
// Both assertions use the same book and the same loader: the superuser's
// call reaches the loader (calls == 1), the reader's does not (calls == 0,
// after reset).
func TestPreviewService_SuperUserOpensUnapprovedAndHidden(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: false, DuplicateHidden: true, MD5: "abc123"},
	}}
	loader := &fakeArchiveLoader{data: []byte(minimalFB2)}
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits())

	// Superuser: the gate passes, the loader fires.
	if _, err := svc.Load(context.Background(), 1, true); err != nil {
		t.Fatalf("superuser: %v", err)
	}
	if loader.calls != 1 {
		t.Fatalf("superuser: loader.calls = %d, want 1", loader.calls)
	}

	// Regular reader: the gate refuses, the loader is not touched.
	loader.calls = 0
	_, err := svc.Load(context.Background(), 1, false)
	if !errors.Is(err, ErrBookNotVisible) {
		t.Fatalf("reader: err = %v, want ErrBookNotVisible", err)
	}
	if loader.calls != 0 {
		t.Errorf("reader: loader.calls = %d, want 0", loader.calls)
	}
}

// A non-fb2 book is refused with its own typed reason, and the archive is
// not touched. The format check fires after visibility — an unapproved
// epub still gets ErrBookNotVisible, not ErrUnsupportedFormat — but before
// the loader.
func TestPreviewService_NonFB2IsRefusedWithoutLoading(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: "epub", Approved: true, DuplicateHidden: false},
	}}
	loader := &fakeArchiveLoader{}
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits())

	_, err := svc.Load(context.Background(), 1, false)
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("err = %v, want ErrUnsupportedFormat", err)
	}
	if loader.calls != 0 {
		t.Errorf("loader was called %d times for a non-fb2 book, want 0", loader.calls)
	}
}

// A missing book (no row in the catalog) is refused with its own typed
// reason. GetBook returns (nil, nil) — "not found" is not an error in the
// database sense, it is an empty result. The service translates that into
// ErrBookNotFound so the caller can distinguish "no such book" from
// "database is broken".
func TestPreviewService_MissingBookIsRefused(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{}}
	loader := &fakeArchiveLoader{}
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits())

	_, err := svc.Load(context.Background(), 999, false)
	if !errors.Is(err, ErrBookNotFound) {
		t.Fatalf("err = %v, want ErrBookNotFound", err)
	}
	if loader.calls != 0 {
		t.Errorf("loader was called %d times for a missing book, want 0", loader.calls)
	}
}

// An unapproved book that is NOT hidden is still refused for a regular
// reader. This pins the Approved half of visibleTo independently: without
// this case, the mutation "drop the Approved check" is invisible, because
// every other fixture that is unapproved is also hidden — and the hidden
// half blocks first.
func TestPreviewService_UnapprovedButNotHiddenIsRefused(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: false, DuplicateHidden: false},
	}}
	loader := &fakeArchiveLoader{}
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits())

	_, err := svc.Load(context.Background(), 1, false)
	if !errors.Is(err, ErrBookNotVisible) {
		t.Fatalf("err = %v, want ErrBookNotVisible", err)
	}
	if loader.calls != 0 {
		t.Errorf("loader was called %d times for an unapproved book, want 0", loader.calls)
	}
}

// A database error (not "not found", but "broken") must propagate as an
// error, not silently fall through to ErrBookNotFound or to the loader. The
// nil-check after GetBook would catch a nil book, but a database error that
// returns (nil, err) is a different failure: the lookup itself failed, and
// reporting it as "not found" would hide a broken database from the caller.
func TestPreviewService_DatabaseErrorIsPropagated(t *testing.T) {
	dbErr := errors.New("connection refused")
	repo := &fakeBookRepo{err: dbErr}
	loader := &fakeArchiveLoader{}
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits())

	_, err := svc.Load(context.Background(), 1, false)
	if err == nil {
		t.Fatal("expected an error from a broken database")
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("err = %v, want the database error in the chain", err)
	}
	if errors.Is(err, ErrBookNotFound) {
		t.Errorf("a database error must not be masked as ErrBookNotFound")
	}
	if loader.calls != 0 {
		t.Errorf("loader was called despite a database error")
	}
}

// An approved, non-hidden book passes the visibility gate for a regular
// reader and reaches the loader. This pins the happy path: without it, the
// mutation "refuse everything" (visibleTo always returns false) would be
// invisible — every other fixture expects a refusal, and a function that
// always refuses would pass them all.
func TestPreviewService_ApprovedNotHiddenPassesVisibility(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "def456"},
	}}
	loader := &fakeArchiveLoader{data: []byte(minimalFB2)}
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits())

	if _, err := svc.Load(context.Background(), 1, false); err != nil {
		t.Fatalf("an approved, non-hidden book must pass visibility: %v", err)
	}
	if loader.calls != 1 {
		t.Errorf("loader.calls = %d, want 1 — the gate must pass and reach the archive",
			loader.calls)
	}
}

// --- Cache tests (plan 3.1 tests 3, 5, 11, 12, 13) -------------------------

// Test 3: cache unavailable → typed refusal, loader untouched.
func TestPreviewService_CacheUnavailableRefusesWithoutLoading(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "abc"},
	}}
	loader := &fakeArchiveLoader{data: []byte(minimalFB2)}
	cache := newMockCache()
	cache.pingErr = errors.New("redis is down")
	svc := NewPreviewService(repo, loader, cache, 4, defaultPreviewLimits())

	_, err := svc.Load(context.Background(), 1, false)
	if !errors.Is(err, ErrCacheUnavailable) {
		t.Fatalf("err = %v, want ErrCacheUnavailable", err)
	}
	if loader.calls != 0 {
		t.Errorf("loader was called %d times despite cache being down, want 0", loader.calls)
	}
}

// Test 5: second request for the same book does not open the archive.
func TestPreviewService_SecondRequestDoesNotOpenArchive(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "abc", Path: "/x", FileName: "y.fb2"},
	}}
	loader := &fakeArchiveLoader{data: []byte(minimalFB2)}
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits())

	// First: cache miss, loader fires once.
	if _, err := svc.Load(context.Background(), 1, false); err != nil {
		t.Fatalf("first load: %v", err)
	}
	if loader.calls != 1 {
		t.Fatalf("first: loader.calls = %d, want 1", loader.calls)
	}

	// Second: cache hit, loader NOT called again.
	if _, err := svc.Load(context.Background(), 1, false); err != nil {
		t.Fatalf("second load: %v", err)
	}
	if loader.calls != 1 {
		t.Errorf("second: loader.calls = %d, want 1 — the archive must not be opened twice for the same book",
			loader.calls)
	}
}

// Test 11: different MD5 produces a different key. The second book must
// miss and call the loader; reloading the first must hit.
func TestPreviewService_DifferentMD5ProducesDifferentKey(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "aaa", Path: "/x", FileName: "a.fb2"},
		2: {ID: 2, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "bbb", Path: "/x", FileName: "b.fb2"},
	}}
	loader := &fakeArchiveLoader{data: []byte(minimalFB2)}
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits())

	if _, err := svc.Load(context.Background(), 1, false); err != nil {
		t.Fatalf("book1: %v", err)
	}
	if loader.calls != 1 {
		t.Fatalf("book1: loader.calls = %d, want 1", loader.calls)
	}

	if _, err := svc.Load(context.Background(), 2, false); err != nil {
		t.Fatalf("book2: %v", err)
	}
	if loader.calls != 2 {
		t.Errorf("book2: loader.calls = %d, want 2 — different MD5 must miss", loader.calls)
	}

	if _, err := svc.Load(context.Background(), 1, false); err != nil {
		t.Fatalf("book1 reload: %v", err)
	}
	if loader.calls != 2 {
		t.Errorf("book1 reload: loader.calls = %d, want 2 — same MD5 must hit", loader.calls)
	}
}

// Test 12: a book without MD5 is refused with its own typed reason.
func TestPreviewService_EmptyMD5IsRefused(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: ""},
	}}
	loader := &fakeArchiveLoader{}
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits())

	_, err := svc.Load(context.Background(), 1, false)
	if !errors.Is(err, ErrEmptyMD5) {
		t.Fatalf("err = %v, want ErrEmptyMD5", err)
	}
	if loader.calls != 0 {
		t.Errorf("loader was called for a book without MD5, want 0")
	}
}

// Test 13: a manifest without any chunks is treated as a miss, not an empty
// book. Also verifies the write order: chunks first, manifest last.
func TestPreviewService_ManifestWithoutChunkIsCacheMissAndChunksWrittenFirst(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "abc", Path: "/x", FileName: "y.fb2"},
	}}
	loader := &fakeArchiveLoader{data: []byte(minimalFB2)}
	cache := newMockCache()

	// Simulate a stale state: manifest exists but no chunks. The mock
	// stores by the raw key (no manifest/chunk prefix) — the service
	// passes buildCacheKey output to both GetManifest and GetChunk.
	key := buildCacheKey("abc", renderVersionPrefix)
	cache.manifests[key] = []byte("stale-manifest")

	svc := NewPreviewService(repo, loader, cache, 4, defaultPreviewLimits())
	if _, err := svc.Load(context.Background(), 1, false); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loader.calls != 1 {
		t.Fatalf("loader.calls = %d, want 1 — manifest without chunks must be a miss", loader.calls)
	}

	// Verify write order: every PutManifest must come after at least one
	// PutChunk. If PutManifest is first, a crash between the two leaves a
	// stale manifest visible before any chunk is stored.
	chunkSeen := false
	for _, op := range cache.putOrder {
		if len(op) >= 5 && op[:5] == "chunk" {
			chunkSeen = true
		}
		if len(op) >= 8 && op[:8] == "manifest" && !chunkSeen {
			t.Errorf("PutManifest was called before any PutChunk — chunks must be written first; order: %v",
				cache.putOrder)
			break
		}
	}
}

// --- Concurrency tests (plan 3.1 tests 6, 7, 8, 9, 10) --------------------

// slowArchiveLoader is a loader that sleeps before returning, so concurrent
// requests arrive while the first build is still in flight. Without the
// delay, the first goroutine could finish before the others start, and
// singleflight would never coalesce — the test would pass trivially.
//
// It also records the context it received, so test 10 can verify the build
// context is detached from the request context.
type slowArchiveLoader struct {
	mu       sync.Mutex
	calls    int
	data     []byte
	delay    time.Duration
	buildCtx context.Context
}

func (l *slowArchiveLoader) Load(ctx context.Context, _, _ string) ([]byte, error) {
	l.mu.Lock()
	l.calls++
	l.buildCtx = ctx
	l.mu.Unlock()

	// Respect the context: if it is canceled during the delay, abort
	// immediately and return ctx.Err(). Without this, a canceled build
	// context has no observable effect — the loader sleeps and returns
	// data regardless, and every test that asserts "the build survived
	// cancellation" passes trivially, whether the context was detached
	// or not.
	select {
	case <-time.After(l.delay):
		return l.data, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (l *slowArchiveLoader) getCalls() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

// Test 6: N concurrent requests for the same cold book trigger exactly one
// loader call (singleflight).
func TestPreviewService_ConcurrentRequestsTriggerOneLoad(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "abc", Path: "/x", FileName: "y.fb2"},
	}}
	loader := &slowArchiveLoader{data: []byte(minimalFB2), delay: 50 * time.Millisecond}
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits())

	const N = 10
	var wg sync.WaitGroup
	errs := make([]error, N)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = svc.Load(context.Background(), 1, false)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("request %d: %v", i, err)
		}
	}
	if got := loader.getCalls(); got != 1 {
		t.Errorf("loader.calls = %d, want 1 — singleflight must coalesce %d concurrent requests into one archive open",
			got, N)
	}
}

// Test 7: when the build ceiling is full, a request for a different book
// gets ErrTooManyBuilds, not a silent queue.
func TestPreviewService_BuildCeilingRefusesWithErrTooManyBuilds(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "aaa", Path: "/x", FileName: "a.fb2"},
		2: {ID: 2, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "bbb", Path: "/x", FileName: "b.fb2"},
	}}
	loader := &slowArchiveLoader{data: []byte(minimalFB2), delay: 50 * time.Millisecond}
	svc := NewPreviewService(repo, loader, newMockCache(), 1, defaultPreviewLimits()) // ceiling = 1

	// Occupy the only slot with book 1.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = svc.Load(context.Background(), 1, false)
	}()

	// Wait for book 1's build to start.
	time.Sleep(10 * time.Millisecond)

	// Book 2 is a different key → different singleflight → semaphore full.
	_, err := svc.Load(context.Background(), 2, false)
	if !errors.Is(err, ErrTooManyBuilds) {
		t.Fatalf("err = %v, want ErrTooManyBuilds", err)
	}

	wg.Wait()
}

// Test 8: canceling one waiter does not abort the build for the others.
// Waiter A must be the singleflight leader (first to call DoChan) so
// that its context is the one the build closure captures — if the build
// were on A's context, canceling A would abort the work B is waiting on.
func TestPreviewService_CancelOneWaiterOthersStillGetResult(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "abc", Path: "/x", FileName: "y.fb2"},
	}}
	loader := &slowArchiveLoader{data: []byte(minimalFB2), delay: 50 * time.Millisecond}
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits())

	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB := context.Background()

	var wg sync.WaitGroup
	var errA, errB error
	var dataB []byte

	// Start A first — it becomes the singleflight leader.
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, errA = svc.Load(ctxA, 1, false)
	}()

	// Give A time to enter DoChan before B arrives.
	time.Sleep(10 * time.Millisecond)

	// Start B — it waits on A's flight.
	wg.Add(1)
	go func() {
		defer wg.Done()
		dataB, errB = svc.Load(ctxB, 1, false)
	}()

	// Cancel A while the build is still in flight.
	time.Sleep(10 * time.Millisecond)
	cancelA()
	wg.Wait()

	if !errors.Is(errA, context.Canceled) {
		t.Errorf("waiter A: err = %v, want context.Canceled", errA)
	}
	if errB != nil {
		t.Errorf("waiter B: err = %v, want nil — canceling A must not abort the build", errB)
	}
	if len(dataB) == 0 {
		t.Error("waiter B: empty payload")
	}
}

// Test 9: if all waiters cancel, the build still completes and writes to
// the cache — the next request gets a warm hit.
func TestPreviewService_AllWaitersCancelBuildStillCompletes(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "abc", Path: "/x", FileName: "y.fb2"},
	}}
	loader := &slowArchiveLoader{data: []byte(minimalFB2), delay: 50 * time.Millisecond}
	cache := newMockCache()
	svc := NewPreviewService(repo, loader, cache, 4, defaultPreviewLimits())

	ctx, cancel := context.WithCancel(context.Background())

	go func() { _, _ = svc.Load(ctx, 1, false) }()
	cancel()

	// Wait for the build to finish (delay + margin).
	time.Sleep(100 * time.Millisecond)

	key := buildCacheKey("abc", renderVersionPrefix)
	if _, err := cache.GetManifest(context.Background(), key); err != nil {
		t.Errorf("cache miss after all waiters canceled: %v — the build must complete and write to cache", err)
	}
}

// Test 10: the build context is detached from the request context. The
// loader receives a context whose cancellation is independent of the
// caller's — otherwise canceling the request would abort the work.
func TestPreviewService_BuildContextIsDetachedFromRequest(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "abc", Path: "/x", FileName: "y.fb2"},
	}}
	loader := &slowArchiveLoader{data: []byte(minimalFB2), delay: 20 * time.Millisecond}
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits())

	ctx, cancel := context.WithCancel(context.Background())
	_, _ = svc.Load(ctx, 1, false)
	cancel()

	loader.mu.Lock()
	bctx := loader.buildCtx
	loader.mu.Unlock()

	if bctx == nil {
		t.Fatal("buildCtx is nil — Load was not called")
	}
	if bctx == ctx {
		t.Error("build context is the request context — it must be detached")
	}
	if bctx.Err() != nil {
		t.Errorf("build context is canceled after request cancel: %v — it must survive request cancellation", bctx.Err())
	}
}

// --- Input gates (plan 3.1 test 14 + phase-2 review gates) ----------------

// fb2WithBinaries carries two <binary> elements, each 3 bytes when
// base64-decoded ("AAAA" → 0x000000, "BBBB" → 0x041081). Used to test
// the count and weight gates with known, small values.
const fb2WithBinaries = `<?xml version="1.0"?><FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">` +
	`<body><section><p>t</p></section></body>` +
	`<binary id="b1" content-type="image/png">AAAA</binary>` +
	`<binary id="b2" content-type="image/png">BBBB</binary>` +
	`</FictionBook>`

// Test 14: the archive opened, but the file the book row points to is
// not inside it. This is a distinct failure from "book not found" (no
// catalog row) and from "empty book" (the file exists but has no text).
func TestPreviewService_MissingFileInArchiveIsRefused(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "abc", Path: "/x", FileName: "missing.fb2"},
	}}
	loader := &fakeArchiveLoader{err: ErrArchiveFileNotFound}
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits())

	_, err := svc.Load(context.Background(), 1, false)
	if !errors.Is(err, ErrArchiveFileNotFound) {
		t.Fatalf("err = %v, want ErrArchiveFileNotFound", err)
	}
}

// Gate 1: an FB2 over the size limit is refused before parsing. Proven by
// a parse-call counter: the parseForGates hook must not fire.
func TestPreviewService_OversizeFB2RefusesBeforeParsing(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "abc", Path: "/x", FileName: "big.fb2"},
	}}
	loader := &fakeArchiveLoader{data: make([]byte, 101)} // 101 bytes > 100 limit
	tightLimits := PreviewLimits{MaxFB2Bytes: 100, MaxBinaries: 1000, MaxBinariesBytes: 32 << 20}

	parseCalls := 0
	prev := parseForGates
	parseForGates = func(ctx context.Context, data []byte, readCover bool) (*converter.FB2Document, *parser.BookFile, error) {
		parseCalls++
		return prev(ctx, data, readCover)
	}
	defer func() { parseForGates = prev }()

	svc := NewPreviewService(repo, loader, newMockCache(), 4, tightLimits)
	_, err := svc.Load(context.Background(), 1, false)
	if !errors.Is(err, ErrFB2TooLarge) {
		t.Fatalf("err = %v, want ErrFB2TooLarge", err)
	}
	if parseCalls != 0 {
		t.Errorf("parse was called %d times, want 0 — the size gate must fire before parsing", parseCalls)
	}
}

// Gate 2: more binaries than the limit allows.
func TestPreviewService_TooManyBinariesIsRefused(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "abc", Path: "/x", FileName: "bins.fb2"},
	}}
	loader := &fakeArchiveLoader{data: []byte(fb2WithBinaries)} // 2 binaries
	oneBinaryLimit := PreviewLimits{MaxFB2Bytes: 32 << 20, MaxBinaries: 1, MaxBinariesBytes: 32 << 20}

	svc := NewPreviewService(repo, loader, newMockCache(), 4, oneBinaryLimit)
	_, err := svc.Load(context.Background(), 1, false)
	if !errors.Is(err, ErrTooManyBinaries) {
		t.Fatalf("err = %v, want ErrTooManyBinaries", err)
	}
}

// Gate 3: total decoded weight of binaries exceeds the limit.
func TestPreviewService_BinariesTooHeavyIsRefused(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "abc", Path: "/x", FileName: "heavy.fb2"},
	}}
	loader := &fakeArchiveLoader{data: []byte(fb2WithBinaries)} // 6 decoded bytes total
	tinyWeightLimit := PreviewLimits{MaxFB2Bytes: 32 << 20, MaxBinaries: 1000, MaxBinariesBytes: 4}

	svc := NewPreviewService(repo, loader, newMockCache(), 4, tinyWeightLimit)
	_, err := svc.Load(context.Background(), 1, false)
	if !errors.Is(err, ErrBinariesTooLarge) {
		t.Fatalf("err = %v, want ErrBinariesTooLarge", err)
	}
}

// Happy path: a book within all gates passes. Without this, the mutation
// "reject everything" would go unnoticed — every other fixture expects a
// refusal.
func TestPreviewService_BookWithinAllGatesPasses(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "abc", Path: "/x", FileName: "ok.fb2"},
	}}
	loader := &fakeArchiveLoader{data: []byte(fb2WithBinaries)}
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits())

	data, err := svc.Load(context.Background(), 1, false)
	if err != nil {
		t.Fatalf("a book within all gates must pass: %v", err)
	}
	if len(data) == 0 {
		t.Error("empty payload for an accepted book")
	}
}
