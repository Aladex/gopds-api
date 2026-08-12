package services

// preview_build_test.go pins the cold-build pipeline: what lands in the cache
// (rendered HTML, prepared images, a JSON manifest — never the raw book), in
// which order, and what the revision invalidates.
//
// The rule these tests exist under: a test that compares the cached bytes
// with the loader's input proves nothing about the transformation. Every
// assertion here is against a property the raw FB2 cannot have — HTML markup,
// a JSON manifest, a prepared image payload — so a build that stores the
// input verbatim fails them all.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"regexp"
	"strings"
	"testing"
	"time"

	"gopds-api/internal/converter"
	"gopds-api/models"
)

// revisionShape is the contract the revision must satisfy: it goes into a URL
// path segment through converter.NewPreviewImageBase, which admits exactly
// [A-Za-z0-9._-] and no "..". The test asks the constructor itself, not just
// the regex, because the constructor is the authority the handler will route
// against.
var revisionShape = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// The revision is deterministic, passes the image-base gate, and changes when
// ANY of its three inputs changes: the book fingerprint, the render version,
// or any knob of the image policy. The MaxSide case is the one the design
// calls out by name: a new per-side cap must not let old prepared images
// match new HTML.
func TestBuildRevision(t *testing.T) {
	policy := converter.PreviewImagePolicy{MaxBytes: 1 << 20, MaxPixels: 32 << 20, MaxSide: 4096}

	base := buildRevision("md5-one", "v1", policy)
	if base == "" {
		t.Fatal("revision is empty")
	}
	if !revisionShape.MatchString(base) || strings.Contains(base, "..") {
		t.Errorf("revision %q does not pass the path-segment shape", base)
	}
	if _, err := converter.NewPreviewImageBase(1, base); err != nil {
		t.Errorf("NewPreviewImageBase refused the revision: %v", err)
	}
	if got := buildRevision("md5-one", "v1", policy); got != base {
		t.Errorf("same inputs produced %q, want %q — the revision must be deterministic", got, base)
	}

	cases := []struct {
		name   string
		policy converter.PreviewImagePolicy
	}{
		{"MaxSide", converter.PreviewImagePolicy{MaxBytes: 1 << 20, MaxPixels: 32 << 20, MaxSide: 2048}},
		{"MaxBytes", converter.PreviewImagePolicy{MaxBytes: 2 << 20, MaxPixels: 32 << 20, MaxSide: 4096}},
		{"MaxPixels", converter.PreviewImagePolicy{MaxBytes: 1 << 20, MaxPixels: 16 << 20, MaxSide: 4096}},
	}
	if got := buildRevision("md5-two", "v1", policy); got == base {
		t.Errorf("a different book MD5 kept the revision %q", got)
	}
	if got := buildRevision("md5-one", "v2", policy); got == base {
		t.Errorf("a different render version kept the revision %q", got)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildRevision("md5-one", "v1", tc.policy); got == base {
				t.Errorf("a different %s kept the revision %q — old prepared images would match new HTML",
					tc.name, got)
			}
		})
	}
}

// The manifest is a JSON document, not the book: it carries the revision, the
// chunk count, the table of contents and the prepared-image references, and
// round-trips without loss. A manifest that still held the raw FB2 would fail
// json.Unmarshal — which is exactly the failure the old build hid by storing
// the input and comparing it against itself.
func TestPreviewManifest_JSONRoundTrip(t *testing.T) {
	m := PreviewManifest{
		Revision:   "0123456789abcdef",
		ChunkCount: 3,
		TOC: []PreviewTOCEntry{
			{Title: "ГЛАВА", Depth: 1, Chunk: 0, Anchor: "pv0-ch1"},
			{Title: "ПОДРАЗДЕЛ", Depth: 2, Chunk: 1, Anchor: "pv1-ch1a"},
		},
		Images: []PreviewImageRef{
			{Ordinal: 1, MIME: "image/png", Bytes: 1234},
			{Ordinal: 2, MIME: "image/jpeg", Bytes: 5678},
		},
	}

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(data, []byte("FictionBook")) || bytes.Contains(data, []byte("<?xml")) {
		t.Fatalf("the manifest serialized book content — it must be a JSON index, not the book")
	}

	var back PreviewManifest
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Revision != m.Revision || back.ChunkCount != m.ChunkCount {
		t.Errorf("header fields lost: got %+v", back)
	}
	if len(back.TOC) != 2 || back.TOC[0].Anchor != "pv0-ch1" || back.TOC[1].Depth != 2 {
		t.Errorf("TOC lost: got %+v", back.TOC)
	}
	if len(back.Images) != 2 || back.Images[1].MIME != "image/jpeg" || back.Images[0].Bytes != 1234 {
		t.Errorf("image refs lost: got %+v", back.Images)
	}
}

// --- Cold-build pipeline -----------------------------------------------------

// tinyPNG encodes a real 8x8 PNG, so the build tests feed the pipeline a
// payload the image preparation actually accepts — not magic-byte forgeries.
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 0x40, G: 0x50, B: 0x60, A: 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// fb2WithImage builds a small valid FictionBook: two titled sections, one
// referencing the binary picture img1. The binary payload is base64, as in a
// real book.
func fb2WithImage(t *testing.T) string {
	t.Helper()
	return `<?xml version="1.0"?>` +
		`<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0" xmlns:xlink="http://www.w3.org/1999/xlink">` +
		`<body>` +
		`<section id="s1"><title><p>ПЕРВАЯ ГЛАВА</p></title>` +
		`<p>ТЕКСТ ПЕРВОЙ ГЛАВЫ</p>` +
		`<image xlink:href="#img1"/>` +
		`</section>` +
		`<section id="s2"><title><p>ВТОРАЯ ГЛАВА</p></title>` +
		`<p>ТЕКСТ ВТОРОЙ ГЛАВЫ</p>` +
		`</section>` +
		`</body>` +
		`<binary id="img1" content-type="image/png">` + base64.StdEncoding.EncodeToString(tinyPNG(t)) + `</binary>` +
		`</FictionBook>`
}

// buildBookRepo returns a repo holding one approved, visible fb2 book. The
// MD5 is fixed ("abc") because every build test derives the cache key from it
// through the service's revision; a test that needs another fingerprint builds
// its own repo.
func buildBookRepo() *fakeBookRepo {
	return &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, MD5: "abc", Path: "/x", FileName: "book.fb2"},
	}}
}

// loadManifest runs one cold build and returns the manifest the service
// returned, decoded. It fails the test on any build error.
func loadManifest(t *testing.T, svc *PreviewService) PreviewManifest {
	t.Helper()
	data, err := svc.Load(context.Background(), 1, false)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var manifest PreviewManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("the service returned something that is not a JSON manifest: %v\npayload: %.120s", err, data)
	}
	return manifest
}

// The cached portion is the rendered HTML, not the book. The old build stored
// the raw FB2 and compared it against itself; this test asserts properties
// the raw bytes cannot have: HTML markup in, XML declaration and the
// FictionBook root out. Mutation "store data instead of the render" fails it.
func TestPreviewBuild_CacheHoldsHTMLNotBook(t *testing.T) {
	repo := buildBookRepo()
	loader := &fakeArchiveLoader{data: []byte(fb2WithImage(t))}
	cache := newMockCache()
	svc := NewPreviewService(repo, loader, cache, 4, defaultPreviewLimits(), 0, 0)

	manifest := loadManifest(t, svc)
	key := buildCacheKey(1, "abc", svc.revision(repo.books[1]))

	if manifest.ChunkCount < 1 {
		t.Fatalf("manifest declares %d chunks", manifest.ChunkCount)
	}
	if manifest.Revision != svc.revision(repo.books[1]) {
		t.Errorf("manifest revision = %q, want the build revision %q", manifest.Revision, svc.revision(repo.books[1]))
	}

	chunk, err := cache.GetChunk(context.Background(), key, 0)
	if err != nil {
		t.Fatalf("GetChunk(0): %v", err)
	}
	html := string(chunk)
	if bytes.Equal(chunk, loader.data) {
		t.Fatal("the cached chunk IS the raw FB2 — the build stored its input instead of the render")
	}
	if strings.Contains(html, "<?xml") || strings.Contains(html, "FictionBook") {
		t.Errorf("the cached chunk carries book XML — it must be rendered HTML:\n%.200s", html)
	}
	if !strings.Contains(html, "<p>") {
		t.Errorf("the cached chunk has no paragraph markup — is it HTML at all?\n%.200s", html)
	}
	if !strings.Contains(html, "ТЕКСТ ПЕРВОЙ ГЛАВЫ") {
		t.Errorf("the chapter text did not reach the cached HTML")
	}

	// The TOC points at real anchors: each entry's anchor is an id in one of
	// the cached chunks, and the titles survived.
	if len(manifest.TOC) != 2 {
		t.Fatalf("manifest TOC has %d entries, want 2 (the two titled sections): %+v", len(manifest.TOC), manifest.TOC)
	}
	for _, entry := range manifest.TOC {
		if entry.Title != "ПЕРВАЯ ГЛАВА" && entry.Title != "ВТОРАЯ ГЛАВА" {
			t.Errorf("unexpected TOC title %q", entry.Title)
		}
		if entry.Anchor == "" {
			t.Errorf("TOC entry %q has no anchor, but its section carries an id", entry.Title)
			continue
		}
		chunkHTML, cerr := cache.GetChunk(context.Background(), key, entry.Chunk)
		if cerr != nil {
			t.Errorf("TOC entry %q points at chunk %d, which is not cached: %v", entry.Title, entry.Chunk, cerr)
			continue
		}
		if !strings.Contains(string(chunkHTML), `id="`+entry.Anchor+`"`) {
			t.Errorf("TOC anchor %q for %q is not an id in chunk %d — the TOC points nowhere",
				entry.Anchor, entry.Title, entry.Chunk)
		}
	}
}

// The number of stored portions equals the number the manifest declares.
func TestPreviewBuild_StoredChunkCountMatchesManifest(t *testing.T) {
	// A tight chunk ceiling and a repetitive body force several portions.
	paras := ""
	for i := 0; i < 40; i++ {
		paras += `<p>АБЗАЦ НОМЕР ` + strings.Repeat("длинный ", 6) + `</p>`
	}
	fb2 := `<?xml version="1.0"?>` +
		`<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">` +
		`<body><section><title><p>ГЛАВА</p></title>` + paras + `</section></body></FictionBook>`

	repo := buildBookRepo()
	loader := &fakeArchiveLoader{data: []byte(fb2)}
	cache := newMockCache()
	svc := NewPreviewService(repo, loader, cache, 4, defaultPreviewLimits(), 0, 0)
	svc.chunkPolicy = converter.PreviewPolicy{MaxChunkBytes: 512}

	manifest := loadManifest(t, svc)
	key := buildCacheKey(1, "abc", svc.revision(repo.books[1]))

	if manifest.ChunkCount < 2 {
		t.Fatalf("the fixture must produce several chunks, manifest declares %d", manifest.ChunkCount)
	}
	cache.mu.Lock()
	stored := len(cache.chunks[key])
	cache.mu.Unlock()
	if stored != manifest.ChunkCount {
		t.Errorf("cached %d chunks, manifest declares %d — a reader paging to the end would hit a miss",
			stored, manifest.ChunkCount)
	}
	for i := 0; i < manifest.ChunkCount; i++ {
		if _, err := cache.GetChunk(context.Background(), key, i); err != nil {
			t.Errorf("chunk %d of %d is not retrievable: %v", i, manifest.ChunkCount, err)
		}
	}
}

// Every <img> the cached HTML references is a prepared image in the cache,
// under the same revision the URL carries. This is the phase-3 invariant:
// by the time the manifest exists, every src is answerable.
func TestPreviewBuild_EveryReferencedImageIsCached(t *testing.T) {
	repo := buildBookRepo()
	loader := &fakeArchiveLoader{data: []byte(fb2WithImage(t))}
	cache := newMockCache()
	svc := NewPreviewService(repo, loader, cache, 4, defaultPreviewLimits(), 0, 0)

	manifest := loadManifest(t, svc)
	key := buildCacheKey(1, "abc", svc.revision(repo.books[1]))

	if len(manifest.Images) != 1 {
		t.Fatalf("manifest declares %d images, want 1 (the valid png)", len(manifest.Images))
	}

	srcPattern := regexp.MustCompile(`<img src="([^"]+)"`)
	found := 0
	for i := 0; i < manifest.ChunkCount; i++ {
		chunk, err := cache.GetChunk(context.Background(), key, i)
		if err != nil {
			t.Fatalf("GetChunk(%d): %v", i, err)
		}
		for _, m := range srcPattern.FindAllStringSubmatch(string(chunk), -1) {
			found++
			src := m[1]
			// The address carries the build's own revision — the handler
			// routes on it, so it must match what the manifest declares.
			// The shape itself is not spelled here: models.PreviewImageURL
			// owns it, and comparing against that function is what keeps the
			// printed address and the registered route from drifting apart.
			if !strings.Contains(src, "revision="+manifest.Revision) {
				t.Errorf("src %q does not carry the manifest revision %q", src, manifest.Revision)
			}
			ordinal := 0
			for n := 1; n < 500; n++ {
				if src == models.PreviewImageURL(repo.books[1].ID, manifest.Revision, n) {
					ordinal = n
					break
				}
			}
			if ordinal <= 0 {
				t.Errorf("src %q is not an address models.PreviewImageURL produces", src)
				continue
			}
			payload, mime, gerr := cache.GetImage(context.Background(), key, ordinal)
			if gerr != nil {
				t.Errorf("the HTML references %q but no image is cached under ordinal %d: %v — a published broken img",
					src, ordinal, gerr)
				continue
			}
			if mime != "image/png" {
				t.Errorf("cached image MIME = %q, want image/png", mime)
			}
			if len(payload) == 0 {
				t.Errorf("cached image %d has an empty payload", ordinal)
			}
			if manifest.Images[0].Bytes != len(payload) {
				t.Errorf("manifest declares %d bytes, cache holds %d", manifest.Images[0].Bytes, len(payload))
			}
		}
	}
	if found == 0 {
		t.Error("no <img> found in the cached HTML — the picture reference did not survive the build")
	}
}

// A binary that image policy refuses is not a build failure: the book opens,
// the picture stays a placeholder, and the manifest lists only the prepared
// image. Only a CACHE WRITE failure is a build error (next test).
func TestPreviewBuild_RefusedImageKeepsPlaceholder(t *testing.T) {
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

	repo := buildBookRepo()
	loader := &fakeArchiveLoader{data: []byte(fb2)}
	cache := newMockCache()
	svc := NewPreviewService(repo, loader, cache, 4, defaultPreviewLimits(), 0, 0)

	manifest := loadManifest(t, svc)
	key := buildCacheKey(1, "abc", svc.revision(repo.books[1]))

	if len(manifest.Images) != 1 {
		t.Errorf("manifest declares %d images, want exactly 1 (the junk binary must be refused, not fatal)", len(manifest.Images))
	}
	chunk, err := cache.GetChunk(context.Background(), key, 0)
	if err != nil {
		t.Fatalf("GetChunk(0): %v", err)
	}
	if !strings.Contains(string(chunk), "[image]") {
		t.Errorf("the refused image left no placeholder in the HTML — a silent loss")
	}
}

// A failed image write refuses the build and publishes NO manifest. The
// invariant: the manifest is the promise that every referenced resource is
// cached; writing it after a failed image write would publish a broken img
// on purpose.
func TestPreviewBuild_ImageWriteFailurePublishesNoManifest(t *testing.T) {
	repo := buildBookRepo()
	loader := &fakeArchiveLoader{data: []byte(fb2WithImage(t))}
	cache := newMockCache()
	cache.putImageErr = errors.New("redis write failed")
	svc := NewPreviewService(repo, loader, cache, 4, defaultPreviewLimits(), 0, 0)

	_, err := svc.Load(context.Background(), 1, false)
	if err == nil {
		t.Fatal("a failed image write must refuse the build")
	}

	key := buildCacheKey(1, "abc", svc.revision(repo.books[1]))
	if _, gerr := cache.GetManifest(context.Background(), key); !errors.Is(gerr, ErrCacheMiss) {
		t.Errorf("manifest present after a failed image write (err = %v) — the build published a promise it broke", gerr)
	}
}

// The manifest is the LAST write of the build: every chunk and every image
// lands before it. A manifest written earlier opens a window where a reader
// discovers the book before its bytes exist.
func TestPreviewBuild_ManifestIsWrittenLast(t *testing.T) {
	repo := buildBookRepo()
	loader := &fakeArchiveLoader{data: []byte(fb2WithImage(t))}
	cache := newMockCache()
	svc := NewPreviewService(repo, loader, cache, 4, defaultPreviewLimits(), 0, 0)

	manifest := loadManifest(t, svc)

	cache.mu.Lock()
	order := append([]string(nil), cache.putOrder...)
	cache.mu.Unlock()

	if len(order) == 0 {
		t.Fatal("no cache writes recorded")
	}
	last := order[len(order)-1]
	if !strings.HasPrefix(last, "manifest:") {
		t.Errorf("the last cache write is %q, want the manifest — it must be published after every byte it references; order: %v",
			last, order)
	}
	var chunks, images int
	for _, op := range order[:len(order)-1] {
		switch {
		case strings.HasPrefix(op, "chunk:"):
			chunks++
		case strings.HasPrefix(op, "image:"):
			images++
		case strings.HasPrefix(op, "manifest:"):
			t.Errorf("a manifest write landed mid-build; order: %v", order)
		}
	}
	if chunks != manifest.ChunkCount {
		t.Errorf("%d chunk writes, manifest declares %d chunks", chunks, manifest.ChunkCount)
	}
	if images != len(manifest.Images) {
		t.Errorf("%d image writes, manifest declares %d images", images, len(manifest.Images))
	}
}

// Changing the image policy (here: the per-side cap) changes the revision,
// and with it the cache key: the next request misses and rebuilds instead of
// serving the old cutting until TTL.
func TestPreviewBuild_ImagePolicyChangeInvalidatesCache(t *testing.T) {
	repo := buildBookRepo()
	loader := &fakeArchiveLoader{data: []byte(fb2WithImage(t))}
	cache := newMockCache()

	svc1 := NewPreviewService(repo, loader, cache, 4, defaultPreviewLimits(), 0, 0)
	manifest1 := loadManifest(t, svc1)
	if loader.calls != 1 {
		t.Fatalf("first build: loader.calls = %d, want 1", loader.calls)
	}

	svc2 := NewPreviewService(repo, loader, cache, 4, defaultPreviewLimits(), 0, 0)
	svc2.imagePolicy.MaxSide = 2048
	manifest2 := loadManifest(t, svc2)

	if manifest2.Revision == manifest1.Revision {
		t.Errorf("a MaxSide change kept the revision %q — old prepared images would match new HTML", manifest1.Revision)
	}
	if loader.calls != 2 {
		t.Errorf("after the policy change: loader.calls = %d, want 2 — the new revision must miss the old entry", loader.calls)
	}
}

// Two catalog rows can hold the same file: GetDuplicateGroups finds books
// by matching MD5, and books carry duplicate_hidden. The rendered HTML
// addresses images as /preview/{bookID}/{revision}/{n}, so an entry keyed by
// the hash alone would hand the second book the first one's picture URLs —
// every image pointing at a foreign id.
//
// The assertion is on the HTML each book gets, not on the keys: a key that
// differs but a render that still carries the other id would pass a key
// comparison and fail the reader.
func TestPreviewBuild_SameMD5DifferentBooksDoNotShareCache(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, Approved: true, MD5: "same", Path: "/x", FileName: "book.fb2"},
		2: {ID: 2, Format: formatFB2, Approved: true, MD5: "same", Path: "/x", FileName: "book.fb2"},
	}}
	loader := &fakeArchiveLoader{data: []byte(fb2WithImage(t))}
	cache := newMockCache()
	svc := NewPreviewService(repo, loader, cache, 4, defaultPreviewLimits(), 0, 0)

	htmlFor := func(bookID int64) string {
		raw, err := svc.Load(context.Background(), bookID, false)
		if err != nil {
			t.Fatalf("Load(%d): %v", bookID, err)
		}
		var m PreviewManifest
		if uerr := json.Unmarshal(raw, &m); uerr != nil {
			t.Fatalf("manifest for %d: %v", bookID, uerr)
		}
		chunk, cerr := cache.GetChunk(context.Background(), buildCacheKey(bookID, "same", m.Revision), 0)
		if cerr != nil {
			t.Fatalf("chunk for %d: %v", bookID, cerr)
		}
		return string(chunk)
	}

	first := htmlFor(1)
	second := htmlFor(2)

	if !strings.Contains(first, "/preview/1/") {
		t.Fatalf("book 1 renders no image URL of its own; got:\n%s", first)
	}
	if strings.Contains(second, "/preview/1/") {
		t.Errorf("book 2 was served book 1's picture URLs — the cache is keyed by content alone")
	}
	if !strings.Contains(second, "/preview/2/") {
		t.Errorf("book 2's HTML carries no /preview/2/ address; got:\n%s", second)
	}
	if loader.calls != 2 {
		t.Errorf("loader.calls = %d, want 2 — each catalog row needs its own build", loader.calls)
	}
}

// --- Context execution: manifest not published after cancellation ------------

// imageBlockCache wraps mockPreviewCache and blocks the first PutImage call
// until release is closed. This gives the test a deterministic window between
// "all chunks are cached" and "the manifest is about to be published" to
// cancel the build context. The mock cache ignores context, so PutImage
// succeeds after release regardless of the context state — which is exactly
// what isolates the buildAndCache ctx check as the only gate left.
type imageBlockCache struct {
	*mockPreviewCache
	entered chan struct{}
	release chan struct{}
}

func (c *imageBlockCache) PutImage(_ context.Context, key string, ordinal int, payload []byte, mime string, ttl time.Duration) error {
	select {
	case c.entered <- struct{}{}:
	default:
	}
	<-c.release
	return c.mockPreviewCache.PutImage(context.Background(), key, ordinal, payload, mime, ttl)
}

// A build whose context is canceled after all chunks and images are cached
// must NOT publish the manifest. The assertion is on the cache (manifest
// absent), not on the returned error: a mutation that removes the
// pre-manifest ctx check would still return the manifest through PutManifest,
// publishing a promise for work the timeout was supposed to stop.
func TestPreviewBuild_CanceledBuildPublishesNoManifest(t *testing.T) {
	repo := buildBookRepo()
	loader := &fakeArchiveLoader{data: []byte(fb2WithImage(t))}
	cache := &imageBlockCache{
		mockPreviewCache: newMockCache(),
		entered:          make(chan struct{}, 1),
		release:          make(chan struct{}),
	}
	svc := NewPreviewService(repo, loader, cache, 4, defaultPreviewLimits(), 0, 0)

	done := make(chan loadResult, 1)
	go func() {
		data, err := svc.Load(context.Background(), 1, false)
		done <- loadResult{data, err}
	}()

	// Barrier: the build has rendered, cached every chunk, and is now blocked
	// inside PutImage — past every write except the manifest.
	waitSignal(t, cache.entered, "the build to reach image writes")

	// Cancel the service context: this cancels the build context (a child of
	// svcCtx), so the pre-manifest ctx check must refuse before PutManifest.
	svc.Shutdown()

	// Release the blocked PutImage so the build goroutine can proceed to the
	// manifest check and exit.
	close(cache.release)

	res := awaitLoadResult(t, done, "the canceled build to return")
	if res.err == nil {
		t.Fatal("the build succeeded despite cancellation")
	}

	// The manifest must NOT be in the cache. This is the assertion mutation 3
	// ("remove pre-manifest ctx check") fails: without that check PutManifest
	// runs, the manifest lands in the cache, and GetManifest returns it.
	key := buildCacheKey(1, "abc", svc.revision(repo.books[1]))
	if _, err := cache.GetManifest(context.Background(), key); !errors.Is(err, ErrCacheMiss) {
		t.Errorf("manifest present after cancellation (err = %v) — the build published a promise for work the timeout was supposed to stop", err)
	}
}

// The seam between the layers. The parser hands a deadline out of its salvage
// path, and the HTTP layer answers a deadline with 503 and thirty seconds —
// both proven elsewhere. Neither proves that the deadline survives the
// service in between: dropping the %w on the parse error would keep those two
// tests green while the real pipeline lost the type and answered 500.
//
// This runs the real parser through the real service, on a book whose XML
// breaks early enough to reach the salvage path, with a context already past
// its deadline.
func TestPreviewService_DeadlineSurvivesTheParseWrapping(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><FictionBook><description><title-info>` +
		`<book-title>КНИГА</book-title></title-info></description><body><section>`)
	b.WriteString(`<p><![CDATA[ unterminated tail </p>`)
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&b, `<p>paragraph %d</p>`, i)
	}
	b.WriteString(`</section></body></FictionBook>`)

	repo := buildBookRepo()
	loader := &fakeArchiveLoader{data: []byte(b.String())}
	cache := newMockCache()

	// The build runs on the service context, not the request's, so the
	// deadline has to come from the service's own cold-build budget.
	svc := NewPreviewService(repo, loader, cache, 4, defaultPreviewLimits(), time.Millisecond, 0)

	_, err := svc.Load(context.Background(), 1, false)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v (%T), want a deadline — the type was lost between the parser and here", err, err)
	}
}
