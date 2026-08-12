package services

// preview_wiring_test.go proves that configuration reaches the service's
// behavior — not its fields. Each test builds the service through
// NewPreviewServiceFromConfig, the same constructor the application uses,
// with one non-standard value, and asserts the behavior that value is
// supposed to produce. A mutation that replaces the config read with a
// constant must fail the corresponding test; a test that still passes after
// that mutation is decorative.

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopds-api/config"
	"gopds-api/models"
)

// wiringBook is a minimal visible fb2 book for the wiring tests.
func wiringBook(id int64, md5 string) *models.Book {
	return &models.Book{
		ID: id, Format: formatFB2, Approved: true,
		MD5: md5, Path: "x.zip", FileName: "y.fb2",
	}
}

// The configured FB2 gate must be the one that fires: a book one byte over
// the configured cap is refused, while the same book under the default cap
// builds. The second half is what keeps the test honest — without it, a
// broken pipeline that refuses everything would look like a working gate.
func TestPreviewWiring_FB2SizeGateComesFromConfig(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{1: wiringBook(1, "abc")}}

	tight := config.PreviewConfig{MaxFB2Bytes: len(minimalFB2) - 1}
	svc := NewPreviewServiceFromConfig(&tight, repo, &fakeArchiveLoader{data: []byte(minimalFB2)}, newMockCache())
	_, err := svc.Load(context.Background(), 1, false)
	if !errors.Is(err, ErrFB2TooLarge) {
		t.Fatalf("err = %v, want ErrFB2TooLarge — the configured cap is %d, the book is %d bytes",
			err, tight.MaxFB2Bytes, len(minimalFB2))
	}

	svc = NewPreviewServiceFromConfig(&config.PreviewConfig{}, repo, &fakeArchiveLoader{data: []byte(minimalFB2)}, newMockCache())
	if _, err := svc.Load(context.Background(), 1, false); err != nil {
		t.Fatalf("default config refused the book: %v — the refusal above must come from the configured gate, not from a broken pipeline", err)
	}
}

// The configured node gate must be the one that fires: a book over the
// configured cap is refused, while the same book under the default cap
// builds — the second half proves the refusal comes from the gate, not from
// a broken pipeline.
func TestPreviewWiring_NodeGateComesFromConfig(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{1: wiringBook(1, "abc")}}
	// minimalFB2 holds exactly 4 element nodes; one more paragraph makes 5.
	fiveNodes := `<?xml version="1.0"?>` +
		`<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">` +
		`<body><section><p>t</p><p>u</p></section></body></FictionBook>`

	tight := config.PreviewConfig{MaxNodes: 4}
	svc := NewPreviewServiceFromConfig(&tight, repo, &fakeArchiveLoader{data: []byte(fiveNodes)}, newMockCache())
	_, err := svc.Load(context.Background(), 1, false)
	if !errors.Is(err, ErrTooManyNodes) {
		t.Fatalf("err = %v, want ErrTooManyNodes — the configured cap is 4, the book has 5 nodes", err)
	}

	svc = NewPreviewServiceFromConfig(&config.PreviewConfig{}, repo, &fakeArchiveLoader{data: []byte(fiveNodes)}, newMockCache())
	if _, err := svc.Load(context.Background(), 1, false); err != nil {
		t.Fatalf("default config refused the book: %v — the refusal above must come from the configured gate", err)
	}
}

// The configured cold-build timeout must bound the build: a loader that only
// answers to context cancellation is cut off after the configured 50 ms.
func TestPreviewWiring_BuildTimeoutComesFromConfig(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{1: wiringBook(1, "abc")}}
	loader := &barrierArchiveLoader{data: []byte(minimalFB2), entered: make(chan struct{}, 1), release: make(chan struct{})}
	cfg := config.PreviewConfig{BuildTimeout: 50 * time.Millisecond}
	svc := NewPreviewServiceFromConfig(&cfg, repo, loader, newMockCache())

	done := make(chan loadResult, 1)
	go func() {
		data, err := svc.Load(context.Background(), 1, false)
		done <- loadResult{data, err}
	}()
	waitSignal(t, loader.entered, "the stuck build to reach the loader")

	res := awaitLoadResult(t, done, "the configured timeout to cut off the build")
	if !errors.Is(res.err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded after the configured 50 ms", res.err)
	}
}

// The configured build ceiling must be the one that refills the semaphore:
// with a ceiling of 1, a second cold build (a different book, so a different
// flight) is refused while the first holds the slot.
func TestPreviewWiring_ConcurrencyCeilingComesFromConfig(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: wiringBook(1, "aaa"),
		2: wiringBook(2, "bbb"),
	}}
	release := make(chan struct{})
	loader := &barrierArchiveLoader{data: []byte(minimalFB2), entered: make(chan struct{}, 2), release: release}
	cfg := config.PreviewConfig{MaxConcurrentBuilds: 1}
	svc := NewPreviewServiceFromConfig(&cfg, repo, loader, newMockCache())

	book1Done := make(chan loadResult, 1)
	go func() {
		data, err := svc.Load(context.Background(), 1, false)
		book1Done <- loadResult{data, err}
	}()
	waitSignal(t, loader.entered, "book 1 to hold the only build slot")

	if _, err := svc.Load(context.Background(), 2, false); !errors.Is(err, ErrTooManyBuilds) {
		t.Fatalf("err = %v, want ErrTooManyBuilds — the configured ceiling is 1 and book 1 holds the slot", err)
	}

	close(release)
	if res := awaitLoadResult(t, book1Done, "book 1 to finish"); res.err != nil {
		t.Fatalf("book 1: %v", res.err)
	}
}

// The configured TTL must be the one stamped on the published entry: the
// mock cache records the TTL the service passes to PutManifest, so the
// assertion is on the wire value, not on a struct field.
func TestPreviewWiring_CacheTTLComesFromConfig(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{1: wiringBook(1, "abc")}}
	cache := newMockCache()
	cfg := config.PreviewConfig{CacheTTL: 7 * time.Hour}
	svc := NewPreviewServiceFromConfig(&cfg, repo, &fakeArchiveLoader{data: []byte(minimalFB2)}, cache)

	if _, err := svc.Load(context.Background(), 1, false); err != nil {
		t.Fatalf("cold build: %v", err)
	}
	if got := cache.manifestTTL; got != 7*time.Hour {
		t.Fatalf("manifest TTL = %v, want the configured 7h — a constant here means the cache_ttl key is decorative", got)
	}
}

// An empty preview section must behave exactly as the pipeline's own
// defaults: 24 h TTL, default gates, default ceiling. This pins the
// zero-means-default contract NewPreviewServiceFromConfig relies on.
func TestPreviewWiring_EmptyConfigFallsBackToDefaults(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{1: wiringBook(1, "abc")}}
	cache := newMockCache()
	svc := NewPreviewServiceFromConfig(&config.PreviewConfig{}, repo, &fakeArchiveLoader{data: []byte(minimalFB2)}, cache)

	if _, err := svc.Load(context.Background(), 1, false); err != nil {
		t.Fatalf("cold build under defaults: %v", err)
	}
	if got := cache.manifestTTL; got != cacheKeyTTL {
		t.Fatalf("manifest TTL = %v, want the default %v", got, cacheKeyTTL)
	}
}
