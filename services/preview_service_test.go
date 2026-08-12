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
	"encoding/base64"
	"errors"
	"strings"
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

func (l *fakeArchiveLoader) Load(_ context.Context, _, _ string, _ int64) ([]byte, error) {
	l.calls++
	return l.data, l.err
}

// mockPreviewCache is an in-memory PreviewCache for tests. It stores
// manifests, chunks and images in maps, counts calls, and records the order
// of Put operations so test 13 can assert "chunks before manifest" and the
// build tests can assert "manifest last". putImageErr simulates a backend
// write failure on images; getManifestErr and getChunkErr simulate a broken
// backend on reads (distinct from ErrCacheMiss — the service must refuse,
// not rebuild). A mutex guards every field because concurrent goroutines
// (singleflight tests) call Get/Put simultaneously — without it, the race
// detector flags every map access.
type mockPreviewCache struct {
	mu             sync.Mutex
	pingErr        error
	putImageErr    error
	getManifestErr error
	getChunkErr    error
	manifests      map[string][]byte
	// manifestTTL records the TTL passed to the last PutManifest: the wiring
	// tests assert on the value that crossed the interface, not on a service
	// field, so a TTL read from nowhere cannot fake it.
	manifestTTL       time.Duration
	chunks            map[string]map[int][]byte
	images            map[string]map[int]mockImage
	putOrder          []string
	getManifestKeys   []string
	getChunkKeys      []string
	manifestWritten   chan struct{}
	getManifestSignal chan struct{}
}

// mockImage is one stored prepared image: payload and the MIME kept next to
// it, mirroring the hash layout of the Redis implementation.
type mockImage struct {
	payload []byte
	mime    string
}

func newMockCache() *mockPreviewCache {
	return &mockPreviewCache{
		manifests: map[string][]byte{},
		chunks:    map[string]map[int][]byte{},
		images:    map[string]map[int]mockImage{},
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
	if c.getManifestSignal != nil {
		close(c.getManifestSignal)
		c.getManifestSignal = nil
	}
	if c.getManifestErr != nil {
		return nil, c.getManifestErr
	}
	data, ok := c.manifests[key]
	if !ok {
		return nil, ErrCacheMiss
	}
	return data, nil
}

func (c *mockPreviewCache) PutManifest(_ context.Context, key string, data []byte, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.putOrder = append(c.putOrder, "manifest:"+key)
	c.manifests[key] = data
	c.manifestTTL = ttl
	if c.manifestWritten != nil {
		select {
		case c.manifestWritten <- struct{}{}:
		default:
		}
	}
	return nil
}

func (c *mockPreviewCache) GetChunk(_ context.Context, key string, index int) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.getChunkKeys = append(c.getChunkKeys, key)
	if c.getChunkErr != nil {
		return nil, c.getChunkErr
	}
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

func (c *mockPreviewCache) GetImage(_ context.Context, key string, ordinal int) (payload []byte, mime string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	images, ok := c.images[key]
	if !ok {
		return nil, "", ErrCacheMiss
	}
	img, ok := images[ordinal]
	if !ok {
		return nil, "", ErrCacheMiss
	}
	return img.payload, img.mime, nil
}

func (c *mockPreviewCache) PutImage(_ context.Context, key string, ordinal int, payload []byte, mime string, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.putOrder = append(c.putOrder, "image:"+key)
	if c.putImageErr != nil {
		return c.putImageErr
	}
	if c.images[key] == nil {
		c.images[key] = map[int]mockImage{}
	}
	c.images[key][ordinal] = mockImage{payload: payload, mime: mime}
	return nil
}

// armGetManifestSignal returns a channel that is closed on the next
// GetManifest call. It turns "the request has checked the cache" into an
// event the test can wait on, instead of a sleep that hopes the check
// already happened.
func (c *mockPreviewCache) armGetManifestSignal() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.getManifestSignal = make(chan struct{})
	return c.getManifestSignal
}

// A hidden book must not touch the archive. The assertion is not "an error
// came back" — that could happen after the disk was read. It is "the loader
// was called zero times", which proves the check fired before any work.
func TestPreviewService_HiddenBookDoesNotTouchArchive(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, DuplicateHidden: true, Approved: true},
	}}
	loader := &fakeArchiveLoader{}
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits(), 0, 0)

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
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits(), 0, 0)

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
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits(), 0, 0)

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
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits(), 0, 0)

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
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits(), 0, 0)

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
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits(), 0, 0)

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
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits(), 0, 0)

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
	svc := NewPreviewService(repo, loader, cache, 4, defaultPreviewLimits(), 0, 0)

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
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits(), 0, 0)

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
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits(), 0, 0)

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
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits(), 0, 0)

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
	// passes buildCacheKey output to both GetManifest and GetChunk. The
	// key is asked of the service, not re-derived in the test.
	svc := NewPreviewService(repo, loader, cache, 4, defaultPreviewLimits(), 0, 0)
	key := buildCacheKey(1, "abc", svc.revision(repo.books[1]))
	cache.manifests[key] = []byte("stale-manifest")

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
//
// These tests assert on scheduling facts, not on timing hopes. Every "the
// build is in flight", "the waiter has checked the cache" and "the build may
// proceed" is an explicit event — a channel signal — so the test outcome
// cannot depend on how the scheduler happened to interleave goroutines
// during a sleep. A test that guesses the interleaving with time.Sleep can
// let a mutation survive simply because the scheduler landed differently
// that run.

// barrierArchiveLoader is a loader the test drives by hand: every Load call
// signals entered, then blocks until release is closed or the build context
// is canceled. That turns "the build is in flight" and "the build may
// proceed" into explicit events. It also records the context it received, so
// the detachment tests can inspect the build context while the build is
// provably mid-flight.
//
// The context branch is not decoration: without it, a canceled build context
// would have no observable effect — the loader would return data regardless,
// and every test asserting "the build survived / died with cancellation"
// would pass trivially.
type barrierArchiveLoader struct {
	mu       sync.Mutex
	calls    int
	data     []byte
	entered  chan struct{} // one signal per Load call; buffered so mutants cannot deadlock the test
	release  <-chan struct{}
	buildCtx context.Context
}

func (l *barrierArchiveLoader) Load(ctx context.Context, _, _ string, _ int64) ([]byte, error) {
	l.mu.Lock()
	l.calls++
	l.buildCtx = ctx
	l.mu.Unlock()

	l.entered <- struct{}{}
	select {
	case <-l.release:
		return l.data, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (l *barrierArchiveLoader) getCalls() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

func (l *barrierArchiveLoader) getBuildCtx() context.Context {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buildCtx
}

// staleMissCache replays the singleflight window from the review: a request
// reads the cache while it is cold, but reaches the flight only after a
// concurrent build has finished and published. armStaleMiss picks the next
// GetManifest call as that read: the call blocks until the test releases it
// and then answers ErrCacheMiss no matter what the map holds by then — a
// read that began on a cold cache, answered late.
type staleMissCache struct {
	*mockPreviewCache
	mu      sync.Mutex
	armed   bool
	gated   chan struct{}
	release chan struct{}
}

func newStaleMissCache() *staleMissCache {
	return &staleMissCache{
		mockPreviewCache: newMockCache(),
		gated:            make(chan struct{}),
		release:          make(chan struct{}),
	}
}

// armStaleMiss marks the next GetManifest call as the stale read and returns
// a channel closed when that call arrives.
func (c *staleMissCache) armStaleMiss() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.armed = true
	return c.gated
}

// releaseStaleMiss lets the gated read return its pinned miss.
func (c *staleMissCache) releaseStaleMiss() { close(c.release) }

func (c *staleMissCache) GetManifest(ctx context.Context, key string) ([]byte, error) {
	c.mu.Lock()
	if c.armed {
		c.armed = false
		close(c.gated)
		c.mu.Unlock()
		<-c.release
		return nil, ErrCacheMiss
	}
	c.mu.Unlock()
	return c.mockPreviewCache.GetManifest(ctx, key)
}

// loadResult carries one Load call's outcome through a channel.
type loadResult struct {
	data []byte
	err  error
}

// waitSignal blocks until ch fires. The timeout is a watchdog against a hung
// build, not a scheduling assumption: on correct code every wait in these
// tests is guaranteed by a barrier, so the timeout can only fire when the
// code under test is broken.
func waitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// awaitLoadResult is waitSignal for Load outcomes.
func awaitLoadResult(t *testing.T, ch <-chan loadResult, what string) loadResult {
	t.Helper()
	select {
	case res := <-ch:
		return res
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		return loadResult{}
	}
}

// Test 6: N concurrent requests for the same cold book trigger exactly one
// loader call. The barrier (the leader is inside the loader, the cache still
// cold) guarantees the requests overlap the build; the loader count is then
// pinned by construction: every request either joins the single flight,
// reads the warm cache after the publish, or — having read the cold cache
// before the publish but reaching the flight after it ended — is absorbed by
// the leader's re-check. No interleaving produces a second archive open.
func TestPreviewService_ConcurrentRequestsTriggerOneLoad(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "abc", Path: "/x", FileName: "y.fb2"},
	}}
	const N = 10
	release := make(chan struct{})
	loader := &barrierArchiveLoader{data: []byte(minimalFB2), entered: make(chan struct{}, 2*N), release: release}
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits(), 0, 0)

	var wg sync.WaitGroup
	results := make([]loadResult, N)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(idx int) {
			defer wg.Done()
			data, err := svc.Load(context.Background(), 1, false)
			results[idx] = loadResult{data, err}
		}(i)
	}

	// Barrier: the winning build is inside the loader — the archive is open
	// and the cache is still cold, so every other request is provably racing
	// an in-flight build rather than a warm cache.
	waitSignal(t, loader.entered, "the first build to reach the loader")
	close(release)
	wg.Wait()

	for i, res := range results {
		if res.err != nil {
			t.Errorf("request %d: %v", i, res.err)
		}
		if len(res.data) == 0 {
			t.Errorf("request %d: empty payload", i)
		}
	}
	if got := loader.getCalls(); got != 1 {
		t.Errorf("loader.calls = %d, want 1 — %d concurrent requests must share one archive open",
			got, N)
	}
}

// Test 7: when the build ceiling is full, a request for a different book
// gets ErrTooManyBuilds, not a silent queue. The barrier proves book 1 holds
// the only slot before book 2 is asked — no sleep guesses the takeover.
func TestPreviewService_BuildCeilingRefusesWithErrTooManyBuilds(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "aaa", Path: "/x", FileName: "a.fb2"},
		2: {ID: 2, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "bbb", Path: "/x", FileName: "b.fb2"},
	}}
	release := make(chan struct{})
	loader := &barrierArchiveLoader{data: []byte(minimalFB2), entered: make(chan struct{}, 2), release: release}
	svc := NewPreviewService(repo, loader, newMockCache(), 1, defaultPreviewLimits(), 0, 0) // ceiling = 1

	book1Done := make(chan loadResult, 1)
	go func() {
		data, err := svc.Load(context.Background(), 1, false)
		book1Done <- loadResult{data, err}
	}()

	// Barrier: book 1's build holds the only slot — proven, not assumed.
	waitSignal(t, loader.entered, "book 1 to hold the build slot")

	// Book 2 is a different key → different singleflight → semaphore full.
	_, err := svc.Load(context.Background(), 2, false)
	if !errors.Is(err, ErrTooManyBuilds) {
		t.Fatalf("err = %v, want ErrTooManyBuilds", err)
	}

	close(release)
	if res := awaitLoadResult(t, book1Done, "book 1 to finish"); res.err != nil {
		t.Fatalf("book 1: %v", res.err)
	}
}

// Test 8: canceling one waiter does not abort the build for the others. A
// leads the flight (it is inside the loader when B starts); B has checked
// the cold cache when A is canceled, so B is at the flight's doorstep. The
// assertions do not depend on whether B joined the flight or arrived just
// after: a joined B gets the flight result, a late B is absorbed by the
// leader's re-check — both are the correct outcome, and what this test pins
// is that canceling A kills neither path. That the build itself runs
// detached from A's context is pinned deterministically by tests 9 and 10.
func TestPreviewService_CancelOneWaiterOthersStillGetResult(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "abc", Path: "/x", FileName: "y.fb2"},
	}}
	release := make(chan struct{})
	loader := &barrierArchiveLoader{data: []byte(minimalFB2), entered: make(chan struct{}, 2), release: release}
	cache := newMockCache()
	svc := NewPreviewService(repo, loader, cache, 4, defaultPreviewLimits(), 0, 0)

	ctxA, cancelA := context.WithCancel(context.Background())

	aDone := make(chan loadResult, 1)
	go func() {
		data, err := svc.Load(ctxA, 1, false)
		aDone <- loadResult{data, err}
	}()

	// Barrier 1: A's build is inside the loader, so A leads the flight.
	waitSignal(t, loader.entered, "A's build to reach the loader")

	// Barrier 2: B has read the (still cold) cache; its next step is the
	// flight. Arm the signal before B starts so no check can be missed.
	bChecked := cache.armGetManifestSignal()
	bDone := make(chan loadResult, 1)
	go func() {
		data, err := svc.Load(context.Background(), 1, false)
		bDone <- loadResult{data, err}
	}()
	waitSignal(t, bChecked, "B's cache check")

	// A goes away mid-build. The work is shared, so it must not die with A.
	cancelA()
	if res := awaitLoadResult(t, aDone, "the canceled waiter to return"); !errors.Is(res.err, context.Canceled) {
		t.Fatalf("waiter A: err = %v, want context.Canceled", res.err)
	}

	close(release)
	res := awaitLoadResult(t, bDone, "waiter B to receive the build result")
	if res.err != nil {
		t.Errorf("waiter B: err = %v, want nil — canceling A must not abort the build", res.err)
	}
	if len(res.data) == 0 {
		t.Error("waiter B: empty payload")
	}
}

// Test 9: if all waiters cancel, the build still completes and writes to the
// cache — the next reader gets a warm hit. The cancel lands only after the
// build is provably inside the loader: canceling earlier would race the
// request's own entry, and a request canceled before its build starts must
// not start one at all (that gate has its own test below).
func TestPreviewService_AllWaitersCancelBuildStillCompletes(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "abc", Path: "/x", FileName: "y.fb2"},
	}}
	release := make(chan struct{})
	loader := &barrierArchiveLoader{data: []byte(minimalFB2), entered: make(chan struct{}, 1), release: release}
	cache := newMockCache()
	cache.manifestWritten = make(chan struct{}, 1)
	svc := NewPreviewService(repo, loader, cache, 4, defaultPreviewLimits(), 0, 0)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan loadResult, 1)
	go func() {
		data, err := svc.Load(ctx, 1, false)
		done <- loadResult{data, err}
	}()

	// Barrier: the build is inside the loader BEFORE the waiter goes away.
	waitSignal(t, loader.entered, "the build to reach the loader")
	cancel()

	// The only waiter is gone; its Load returns context.Canceled…
	if res := awaitLoadResult(t, done, "the canceled waiter to return"); !errors.Is(res.err, context.Canceled) {
		t.Fatalf("waiter: err = %v, want context.Canceled", res.err)
	}

	// …but the build runs detached: released, it must finish and publish.
	close(release)
	waitSignal(t, cache.manifestWritten, "the detached build to publish the manifest")

	// The key is asked of the service (its revision covers the MD5, the
	// render version and the image policy), not re-derived in the test.
	key := buildCacheKey(1, "abc", svc.revision(repo.books[1]))
	if _, err := cache.GetManifest(context.Background(), key); err != nil {
		t.Errorf("cache miss after all waiters canceled: %v — the build must complete and write to cache", err)
	}
}

// Test 10: the build context is detached from the request context and yet
// bounded. Both halves are inspected while the build is provably mid-flight
// — blocked inside the loader — so the canceled request context cannot race
// the inspection.
func TestPreviewService_BuildContextIsDetachedFromRequest(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "abc", Path: "/x", FileName: "y.fb2"},
	}}
	release := make(chan struct{})
	loader := &barrierArchiveLoader{data: []byte(minimalFB2), entered: make(chan struct{}, 1), release: release}
	cache := newMockCache()
	cache.manifestWritten = make(chan struct{}, 1)
	svc := NewPreviewService(repo, loader, cache, 4, defaultPreviewLimits(), 0, 0)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan loadResult, 1)
	go func() {
		data, err := svc.Load(ctx, 1, false)
		done <- loadResult{data, err}
	}()

	waitSignal(t, loader.entered, "the build to reach the loader")
	cancel()

	bctx := loader.getBuildCtx()
	if bctx == nil {
		t.Fatal("buildCtx is nil — Load was not called")
	}
	if bctx == ctx {
		t.Error("build context IS the request context — it must be detached")
	}
	if bctx.Err() != nil {
		t.Errorf("build context died with the request: %v — it must survive request cancellation", bctx.Err())
	}
	// Detached does not mean unbounded: the context must carry the cold-build
	// deadline, or a hung dependency would pin a build slot forever.
	if _, ok := bctx.Deadline(); !ok {
		t.Error("build context has no deadline — a hung loader or Redis would pin a build slot forever")
	}

	close(release)
	// The canceled waiter may return long before the detached build finishes
	// — that early return is the point of detachment. The test must wait for
	// the build itself, not just the waiter: exiting with the flight still
	// running would let the next test's package-level fixtures (parseForGates)
	// race the still-running build. The manifest is the build's last write.
	waitSignal(t, cache.manifestWritten, "the detached build to publish the manifest")
	awaitLoadResult(t, done, "the waiter to return")
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
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits(), 0, 0)

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
	tightLimits := PreviewLimits{MaxFB2Bytes: 100, MaxBinaries: 1500, MaxPreparedImageBytes: 48 << 20}

	parseCalls := 0
	prev := parseForGates
	parseForGates = func(
		ctx context.Context, data []byte, readCover bool, limits converter.FB2ParseLimits,
	) (*converter.FB2Document, *parser.BookFile, converter.FB2ParseStats, error) {
		parseCalls++
		return prev(ctx, data, readCover, limits)
	}
	defer func() { parseForGates = prev }()

	svc := NewPreviewService(repo, loader, newMockCache(), 4, tightLimits, 0, 0)
	_, err := svc.Load(context.Background(), 1, false)
	if !errors.Is(err, ErrFB2TooLarge) {
		t.Fatalf("err = %v, want ErrFB2TooLarge", err)
	}
	if parseCalls != 0 {
		t.Errorf("parse was called %d times, want 0 — the size gate must fire before parsing", parseCalls)
	}
}

// Gate 2: more binaries than the limit allows. The parser itself stops at
// the exceeding <binary> element; the service maps its typed refusal.
func TestPreviewService_TooManyBinariesIsRefused(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "abc", Path: "/x", FileName: "bins.fb2"},
	}}
	loader := &fakeArchiveLoader{data: []byte(fb2WithBinaries)} // 2 binaries
	oneBinaryLimit := PreviewLimits{MaxFB2Bytes: 64 << 20, MaxBinaries: 1, MaxPreparedImageBytes: 48 << 20}

	svc := NewPreviewService(repo, loader, newMockCache(), 4, oneBinaryLimit, 0, 0)
	_, err := svc.Load(context.Background(), 1, false)
	if !errors.Is(err, ErrTooManyBinaries) {
		t.Fatalf("err = %v, want ErrTooManyBinaries", err)
	}
}

// Gate 4: more element nodes than the limit allows. The parser stops at the
// exceeding element (the converter tests pin the stop); here the service must
// map the typed refusal. The boundary half keeps the comparison strict: a
// book of exactly the cap builds.
func TestPreviewService_TooManyNodesIsRefused(t *testing.T) {
	// minimalFB2 holds exactly 4 element nodes (FictionBook, body, section,
	// p); one more paragraph pushes the document to 5.
	fiveNodes := `<?xml version="1.0"?>` +
		`<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">` +
		`<body><section><p>t</p><p>u</p></section></body></FictionBook>`
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "abc", Path: "/x", FileName: "nodes.fb2"},
	}}
	tightLimits := PreviewLimits{MaxFB2Bytes: 64 << 20, MaxBinaries: 1500, MaxNodes: 4, MaxPreparedImageBytes: 48 << 20}

	svc := NewPreviewService(repo, &fakeArchiveLoader{data: []byte(fiveNodes)}, newMockCache(), 4, tightLimits, 0, 0)
	_, err := svc.Load(context.Background(), 1, false)
	if !errors.Is(err, ErrTooManyNodes) {
		t.Fatalf("err = %v, want ErrTooManyNodes", err)
	}

	svc = NewPreviewService(repo, &fakeArchiveLoader{data: []byte(minimalFB2)}, newMockCache(), 4, tightLimits, 0, 0)
	if _, err := svc.Load(context.Background(), 1, false); err != nil {
		t.Fatalf("a book of exactly the node cap must pass: %v", err)
	}
}

// A binary the markup never references must not be prepared: it would burn
// the decode, take memory, cache space and a manifest slot, and could push
// the build over the total budget although no HTML points at it. The fixture
// carries two valid PNGs and references one; the manifest and the cache must
// hold exactly that one. Kills the mutation "prepare every binary the book
// carries" — under it the manifest would list both.
func TestPreviewService_UnreferencedBinaryIsNotPrepared(t *testing.T) {
	fb2 := `<?xml version="1.0"?>` +
		`<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0" xmlns:xlink="http://www.w3.org/1999/xlink">` +
		`<body><section>` +
		`<p>ТЕКСТ</p>` +
		`<image xlink:href="#used"/>` +
		`</section></body>` +
		`<binary id="used" content-type="image/png">` + base64.StdEncoding.EncodeToString(tinyPNG(t)) + `</binary>` +
		`<binary id="stowaway" content-type="image/png">` + base64.StdEncoding.EncodeToString(tinyPNG(t)) + `</binary>` +
		`</FictionBook>`

	repo := buildBookRepo()
	loader := &fakeArchiveLoader{data: []byte(fb2)}
	cache := newMockCache()
	svc := NewPreviewService(repo, loader, cache, 2, defaultPreviewLimits(), 0, 0)

	manifest := loadManifest(t, svc)
	if len(manifest.Images) != 1 {
		t.Fatalf("manifest declares %d images, want 1 — the unreferenced binary must not be prepared", len(manifest.Images))
	}

	key := buildCacheKey(1, "abc", svc.revision(repo.books[1]))
	if _, _, err := cache.GetImage(context.Background(), key, 1); err != nil {
		t.Errorf("the referenced image is not cached under ordinal 1: %v", err)
	}
	if _, _, err := cache.GetImage(context.Background(), key, 2); !errors.Is(err, ErrCacheMiss) {
		t.Errorf("ordinal 2: err = %v, want ErrCacheMiss — the unreferenced binary must not reach the cache", err)
	}
}

// Gate 3: total weight of prepared preview images exceeds the limit. The
// fixture carries one valid PNG and one corrupt binary ("not an image at
// all"). Preparation refuses the corrupt one, so the prepared set is smaller
// in both count and total bytes than the source binary set — that gap is what
// catches a mutation that checks source binaries instead of prepared images.
func TestPreviewService_PreparedImagesTooHeavyIsRefused(t *testing.T) {
	fb2 := `<?xml version="1.0"?>` +
		`<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0" xmlns:xlink="http://www.w3.org/1999/xlink">` +
		`<body><section>` +
		`<p>ТЕКСТ</p>` +
		`<image xlink:href="#good"/>` +
		`<image xlink:href="#junk"/>` +
		`</section></body>` +
		`<binary id="good" content-type="image/png">` + base64.StdEncoding.EncodeToString(tinyPNG(t)) + `</binary>` +
		`<binary id="junk" content-type="image/png">` + base64.StdEncoding.EncodeToString([]byte("not an image at all")) + `</binary>` +
		`</FictionBook>`

	// Discover the prepared payload size with a permissive build.
	probeSvc := NewPreviewService(buildBookRepo(), &fakeArchiveLoader{data: []byte(fb2)}, newMockCache(), 2, defaultPreviewLimits(), 0, 0)
	probeManifest := loadManifest(t, probeSvc)
	var preparedBytes int
	for _, ref := range probeManifest.Images {
		preparedBytes += ref.Bytes
	}
	if preparedBytes == 0 {
		t.Fatal("probe build produced no prepared images — fixture is broken")
	}

	repo := buildBookRepo()
	loader := &fakeArchiveLoader{data: []byte(fb2)}
	cache := newMockCache()
	tightLimits := PreviewLimits{
		MaxFB2Bytes:           64 << 20,
		MaxBinaries:           1500,
		MaxPreparedImageBytes: preparedBytes - 1,
	}
	svc := NewPreviewService(repo, loader, cache, 2, tightLimits, 0, 0)

	_, err := svc.Load(context.Background(), 1, false)
	if !errors.Is(err, ErrPreparedImagesTooLarge) {
		t.Fatalf("err = %v, want ErrPreparedImagesTooLarge", err)
	}

	// Nothing was written: the gate fires before any cache write.
	key := buildCacheKey(1, "abc", svc.revision(repo.books[1]))
	cache.mu.Lock()
	manifestCount := len(cache.manifests)
	chunkCount := len(cache.chunks)
	imageCount := len(cache.images)
	cache.mu.Unlock()
	if manifestCount != 0 {
		t.Errorf("manifest was written for key %s despite the prepared-image gate refusal", key)
	}
	if chunkCount != 0 {
		t.Errorf("chunks were written despite the prepared-image gate refusal")
	}
	if imageCount != 0 {
		t.Errorf("images were written despite the prepared-image gate refusal")
	}
}

// Gate 3 boundary: a book whose prepared images total exactly the limit
// passes. This pins the comparison as strict (> not >=): one byte over
// refuses, exactly at the cap builds. The same fixture as above ensures
// the source-binary total is larger than the prepared total — so a mutation
// that checks source binaries instead fails here (the source total exceeds
// the cap, the prepared total does not).
func TestPreviewService_PreparedImagesAtLimitPasses(t *testing.T) {
	fb2 := `<?xml version="1.0"?>` +
		`<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0" xmlns:xlink="http://www.w3.org/1999/xlink">` +
		`<body><section>` +
		`<p>ТЕКСТ</p>` +
		`<image xlink:href="#good"/>` +
		`<image xlink:href="#junk"/>` +
		`</section></body>` +
		`<binary id="good" content-type="image/png">` + base64.StdEncoding.EncodeToString(tinyPNG(t)) + `</binary>` +
		`<binary id="junk" content-type="image/png">` + base64.StdEncoding.EncodeToString([]byte("not an image at all")) + `</binary>` +
		`</FictionBook>`

	// Discover the prepared payload size.
	probeSvc := NewPreviewService(buildBookRepo(), &fakeArchiveLoader{data: []byte(fb2)}, newMockCache(), 2, defaultPreviewLimits(), 0, 0)
	probeManifest := loadManifest(t, probeSvc)
	var preparedBytes int
	for _, ref := range probeManifest.Images {
		preparedBytes += ref.Bytes
	}

	repo := buildBookRepo()
	loader := &fakeArchiveLoader{data: []byte(fb2)}
	cache := newMockCache()
	atLimit := PreviewLimits{
		MaxFB2Bytes:           64 << 20,
		MaxBinaries:           1500,
		MaxPreparedImageBytes: preparedBytes,
	}
	svc := NewPreviewService(repo, loader, cache, 2, atLimit, 0, 0)

	if _, err := svc.Load(context.Background(), 1, false); err != nil {
		t.Fatalf("a book at exactly the prepared-image limit must pass: %v", err)
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
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits(), 0, 0)

	data, err := svc.Load(context.Background(), 1, false)
	if err != nil {
		t.Fatalf("a book within all gates must pass: %v", err)
	}
	if len(data) == 0 {
		t.Error("empty payload for an accepted book")
	}
}

// --- Cache failure vs cache miss (review blocker 1) -------------------------

// A broken cache is not a cold cache. A GetManifest answering with a backend
// error must refuse the request: falling through to a build would turn a
// Redis outage into a full archive unpack — the most expensive operation the
// service has — at exactly the moment the infrastructure is least able to
// bear it. The proof is the loader counter, not the error text: a branch
// swap that still returned the right error after reading the archive would
// pass a text assertion.
func TestPreviewService_ManifestReadFailureIsNotAMiss(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "abc", Path: "/x", FileName: "y.fb2"},
	}}
	loader := &fakeArchiveLoader{data: []byte(minimalFB2)}
	cache := newMockCache()
	// A plain untyped error stands for a broken backend; it is NOT ErrCacheMiss.
	cache.getManifestErr = errors.New("connection reset by peer")
	svc := NewPreviewService(repo, loader, cache, 4, defaultPreviewLimits(), 0, 0)

	_, err := svc.Load(context.Background(), 1, false)
	if loader.calls != 0 {
		t.Errorf("loader.calls = %d, want 0 — a broken cache must refuse the request, not fall through to a build",
			loader.calls)
	}
	if !errors.Is(err, ErrCacheUnavailable) {
		t.Errorf("err = %v, want ErrCacheUnavailable", err)
	}
}

// The same distinction for the chunk probe: the manifest reads fine, but the
// first-chunk read is broken. Treating it as a miss would rebuild over a
// healthy manifest.
func TestPreviewService_ChunkReadFailureIsNotAMiss(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "abc", Path: "/x", FileName: "y.fb2"},
	}}
	loader := &fakeArchiveLoader{data: []byte(minimalFB2)}
	cache := newMockCache()
	svc := NewPreviewService(repo, loader, cache, 4, defaultPreviewLimits(), 0, 0)

	// The manifest is present; only the chunk read fails (not a miss).
	key := buildCacheKey(1, "abc", svc.revision(repo.books[1]))
	cache.manifests[key] = []byte("present-manifest")
	cache.getChunkErr = errors.New("connection reset by peer")

	_, err := svc.Load(context.Background(), 1, false)
	if loader.calls != 0 {
		t.Errorf("loader.calls = %d, want 0 — a broken chunk read must refuse the request, not fall through to a build",
			loader.calls)
	}
	if !errors.Is(err, ErrCacheUnavailable) {
		t.Errorf("err = %v, want ErrCacheUnavailable", err)
	}
}

// --- Leader re-check (review blocker 2) --------------------------------------

// The pre-flight cache check cannot close the window by itself: a request
// that saw a miss may reach the flight only after a concurrent build has
// finished and published. The leader must check the cache again inside the
// flight, before taking a build slot — otherwise it rebuilds a book that is
// already cached.
//
// The test replays that exact interleaving without any timing assumption:
// the staleMissCache holds B's read until A's build is fully published, then
// answers the miss B would have seen when its read began. A's Load has
// returned before B is released, so A's flight is provably torn down and B
// provably leads a new one.
func TestPreviewService_LeaderRechecksCacheBeforeRebuilding(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "abc", Path: "/x", FileName: "y.fb2"},
	}}
	release := make(chan struct{})
	loader := &barrierArchiveLoader{data: []byte(minimalFB2), entered: make(chan struct{}, 2), release: release}
	cache := newStaleMissCache()
	svc := NewPreviewService(repo, loader, cache, 4, defaultPreviewLimits(), 0, 0)

	// A builds the book from cold.
	aDone := make(chan loadResult, 1)
	go func() {
		data, err := svc.Load(context.Background(), 1, false)
		aDone <- loadResult{data, err}
	}()

	// Barrier 1: A's build is inside the loader — the archive is open and
	// the cache is still cold.
	waitSignal(t, loader.entered, "A's build to reach the loader")

	// B starts now; its pre-flight read of the cold cache is held before the
	// answer. Arm the gate before B starts so no read can slip past it.
	gated := cache.armStaleMiss()
	bDone := make(chan loadResult, 1)
	go func() {
		data, err := svc.Load(context.Background(), 1, false)
		bDone <- loadResult{data, err}
	}()

	// Barrier 2: B's read has begun on a cold cache and is held there.
	waitSignal(t, gated, "B's pre-flight cache read")

	// Let A finish: the build publishes and the flight ends.
	close(release)
	if res := awaitLoadResult(t, aDone, "A's build to finish"); res.err != nil {
		t.Fatalf("A: %v", res.err)
	}

	// B's stale read now answers "miss". B reaches a NEW flight for a book
	// that is already cached; only the leader's re-check saves it from
	// opening the archive again.
	cache.releaseStaleMiss()

	res := awaitLoadResult(t, bDone, "B to be served")
	if res.err != nil {
		t.Fatalf("B: %v", res.err)
	}
	if len(res.data) == 0 {
		t.Error("B: empty payload")
	}
	if got := loader.getCalls(); got != 1 {
		t.Errorf("loader.calls = %d, want 1 — B's miss was answered by A's finished build; "+
			"the flight leader must re-check the cache before rebuilding", got)
	}
}

// --- Bounded build context (review blocker 3) --------------------------------

// A cold build runs detached from its readers, but not unbounded: a loader
// that never answers must be cut off by the cold-build timeout, and the
// build slot must come back. The loader here answers only to context
// cancellation (release is never closed), so nothing but the timeout can end
// the build.
func TestPreviewService_StuckLoaderBuildTimesOut(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "abc", Path: "/x", FileName: "y.fb2"},
	}}
	loader := &barrierArchiveLoader{data: []byte(minimalFB2), entered: make(chan struct{}, 2), release: make(chan struct{})}
	// Ceiling 1: if the timed-out build kept its slot, the second request
	// below would see ErrTooManyBuilds instead of reaching the loader.
	svc := NewPreviewService(repo, loader, newMockCache(), 1, defaultPreviewLimits(), 50*time.Millisecond, 0)

	first := make(chan loadResult, 1)
	go func() {
		data, err := svc.Load(context.Background(), 1, false)
		first <- loadResult{data, err}
	}()
	waitSignal(t, loader.entered, "the stuck build to reach the loader")

	res := awaitLoadResult(t, first, "the cold-build timeout to cut off the stuck build")
	if !errors.Is(res.err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", res.err)
	}

	// The slot must come back with the timed-out build: a second request
	// reaches the loader and hits the timeout again, not the ceiling.
	second := make(chan loadResult, 1)
	go func() {
		data, err := svc.Load(context.Background(), 1, false)
		second <- loadResult{data, err}
	}()
	waitSignal(t, loader.entered, "the second build to reach the loader")
	res = awaitLoadResult(t, second, "the second build to time out")
	if !errors.Is(res.err, context.DeadlineExceeded) {
		t.Fatalf("second build: err = %v, want context.DeadlineExceeded "+
			"(ErrTooManyBuilds would mean the timed-out build kept its slot)", res.err)
	}
}

// Stopping the service must cancel in-flight builds; otherwise a build hung
// on a dependency would pin its slot and its flight key past the stop.
func TestPreviewService_ShutdownAbortsInFlightBuild(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "abc", Path: "/x", FileName: "y.fb2"},
	}}
	loader := &barrierArchiveLoader{data: []byte(minimalFB2), entered: make(chan struct{}, 1), release: make(chan struct{})}
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits(), 0, 0)

	done := make(chan loadResult, 1)
	go func() {
		data, err := svc.Load(context.Background(), 1, false)
		done <- loadResult{data, err}
	}()
	waitSignal(t, loader.entered, "the build to reach the loader")

	svc.Shutdown()

	res := awaitLoadResult(t, done, "Shutdown to abort the in-flight build")
	if !errors.Is(res.err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", res.err)
	}
}

// A reader who already went away must not start a background build: the work
// would run detached with nobody waiting for the result. The context is
// canceled before Load is even called, so the refusal is synchronous — no
// goroutine, no flight, no archive.
func TestPreviewService_CanceledRequestStartsNoBuild(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false, MD5: "abc", Path: "/x", FileName: "y.fb2"},
	}}
	release := make(chan struct{})
	defer close(release) // unblock the loader if a mutant ever starts a build
	loader := &barrierArchiveLoader{data: []byte(minimalFB2), entered: make(chan struct{}, 1), release: release}
	svc := NewPreviewService(repo, loader, newMockCache(), 4, defaultPreviewLimits(), 0, 0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the reader is already gone

	_, err := svc.Load(ctx, 1, false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := loader.getCalls(); got != 0 {
		t.Fatalf("loader.calls = %d, want 0 — a reader who already left must not start a background build", got)
	}
	// The mutant this guards against starts the flight in a fresh goroutine,
	// so the count above can lose the race against it. Correct code never
	// spawns that goroutine, so no signal can ever arrive here; the bounded
	// wait exists only to catch the mutant's loader call. It is a watchdog
	// for a negative, not a scheduling assumption.
	select {
	case <-loader.entered:
		t.Error("a background build started for a request that was already canceled")
	case <-time.After(200 * time.Millisecond):
	}
}

// TestBuildPreviewTOC_AnonymousSectionsHaveAnchors verifies that the service-level
// TOC carries non-empty anchors for sections without an id and that each anchor
// exists in the rendered HTML of the chunk it points to.
func TestBuildPreviewTOC_AnonymousSectionsHaveAnchors(t *testing.T) {
	textPara := func(text string) *converter.FB2Paragraph {
		return &converter.FB2Paragraph{
			Kind:    converter.ParagraphKindNormal,
			Text:    text,
			Content: []*converter.FB2InlineElement{{Type: converter.InlineTypeText, Content: text}},
		}
	}
	policy := converter.PreviewPolicy{MaxChunkBytes: 64 * 1024}
	doc := &converter.FB2Document{Body: &converter.FB2BodySection{
		Content: []*converter.FB2ContentItem{
			{Section: &converter.FB2BodySection{
				ID:      "part1",
				Title:   "Part One",
				Content: []*converter.FB2ContentItem{{Paragraph: textPara("text one")}},
			}},
			{Section: &converter.FB2BodySection{
				Title:   "Anonymous story",
				Content: []*converter.FB2ContentItem{{Paragraph: textPara("text two")}},
			}},
		},
	}}

	chunks, err := converter.ChunkPreview(context.Background(), doc, converter.PreviewImages{}, policy)
	if err != nil {
		t.Fatalf("ChunkPreview: %v", err)
	}
	toc := buildPreviewTOC(chunks)
	if len(toc) != 2 {
		t.Fatalf("expected 2 toc entries, got %d", len(toc))
	}
	if toc[0].Anchor != "pv-part1" {
		t.Errorf("first entry anchor = %q, want pv-part1", toc[0].Anchor)
	}
	if toc[1].Anchor == "" {
		t.Fatalf("anonymous section got an empty anchor")
	}

	for i, entry := range toc {
		if entry.Chunk < 0 || entry.Chunk >= len(chunks) {
			t.Fatalf("toc entry %d points to invalid chunk %d", i, entry.Chunk)
		}
		html, err := converter.RenderChunkHTML(chunks[entry.Chunk], converter.PreviewImages{}, policy)
		if err != nil {
			t.Fatalf("RenderChunkHTML chunk %d: %v", entry.Chunk, err)
		}
		if !strings.Contains(html, `id="`+entry.Anchor+`"`) {
			t.Errorf("toc entry %d anchor %q not found in chunk %d HTML", i, entry.Anchor, entry.Chunk)
		}
	}
}
