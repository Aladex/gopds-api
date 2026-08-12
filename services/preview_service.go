package services

// preview_service.go is the entry point of the preview pipeline. Given a
// book id and a reader, it decides whether the reader may see the book, and
// only then reaches for the archive. Past the load, the cold build runs the
// full pipeline — parse, image preparation, chunking, rendering — and
// publishes the result to the cache in the order the phase-3 invariant
// demands: chunks, then prepared images, and the manifest last.
//
// Two dependencies cross the package boundary on purpose, and both are
// interfaces defined here (not in the packages they abstract):
//
//   - BookRepo hides database.GetBook. The real function returns a value
//     type and a database error; preview only needs to know "found / not
//     found / broken", so the narrow interface returns a pointer (nil for
//     not-found) and lets the service translate it into a typed reason.
//
//   - ArchiveLoader hides utils' zip extraction. Tests in this phase must
//     assert that the archive was not touched for a hidden book, which is
//     only possible against a loader that records its own calls. The
//     production implementations of both interfaces live in
//     preview_adapters.go, next to the contracts they satisfy.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sync/singleflight"

	"gopds-api/config"
	"gopds-api/internal/converter"
	"gopds-api/models"
)

// formatFB2 is the only book format the preview pipeline reads today.
const formatFB2 = "fb2"

// defaultMaxConcurrentBuilds is the ceiling on simultaneous cold builds.
// Production reads this from config; tests override through the constructor.
const defaultMaxConcurrentBuilds = 2

// defaultBuildTimeout bounds one cold build. Production reads this from
// config (preview.build_timeout); tests override through the constructor.
const defaultBuildTimeout = 2 * time.Minute

// Typed refusals. They are distinct on purpose: the caller (an HTTP handler
// in phase 4) maps each to a different status, and conflating "not found"
// with "not visible" with "wrong format" would erase exactly the signal a
// reader or operator needs.
var (
	// ErrBookNotFound: there is no book row with this id.
	ErrBookNotFound = errors.New("preview: book not found")

	// ErrBookNotVisible: the book exists but the reader may not open it.
	ErrBookNotVisible = errors.New("preview: book is not visible to this reader")

	// ErrUnsupportedFormat: the book is stored in a format the preview
	// pipeline does not read. Today that is "anything but fb2".
	ErrUnsupportedFormat = errors.New("preview: book format is not supported for preview")

	// ErrTooManyBuilds: the number of simultaneous cold builds has reached
	// the configured ceiling.
	ErrTooManyBuilds = errors.New("preview: too many concurrent builds, try again shortly")

	// ErrArchiveFileNotFound: the archive opened, but the FB2 file the book
	// row points to is not inside it. Distinct from "book not found" (no
	// catalog row) and from "empty book" (the file exists but has no text).
	ErrArchiveFileNotFound = errors.New("preview: file not found in archive")

	// ErrFB2TooLarge: the FB2 payload exceeds the size gate. Checked before
	// parsing, so a 500 MB file never reaches the parser.
	ErrFB2TooLarge = errors.New("preview: FB2 payload exceeds the size gate")

	// ErrTooManyBinaries: the book carries more binary images than the gate
	// allows. The parser stops at the exceeding <binary> element — the count
	// is enforced during the parse, not after it.
	ErrTooManyBinaries = errors.New("preview: too many image binaries")

	// ErrTooManyNodes: the book carries more element nodes than the gate
	// allows. The parser stops at the exceeding element — the count is
	// enforced during the parse, not after it.
	ErrTooManyNodes = errors.New("preview: too many element nodes")

	// ErrPreparedImagesTooLarge: the total weight of prepared preview images
	// exceeds the gate. Enforced inside BuildPreviewImages, between one
	// prepare and the next — the prepared bytes are what live in memory and
	// in Redis, so the ceiling is on the result of transcoding, not on the
	// source binaries, and the build stops at the first payload that crosses
	// it instead of holding the whole over-budget set before refusing.
	ErrPreparedImagesTooLarge = errors.New("preview: prepared images exceed the total weight gate")
)

// PreviewLimits are the input gates that protect the pipeline from books that
// would tie up memory or time. Values are read from config in production;
// tests override through the constructor.
type PreviewLimits struct {
	// MaxFB2Bytes caps the raw FB2 payload. Enforced while reading the
	// archive entry: the loader never pulls more than the cap plus one byte.
	MaxFB2Bytes int
	// MaxBinaries caps the number of <binary> elements. Enforced during the
	// parse: the parser stops at the exceeding element.
	MaxBinaries int
	// MaxNodes caps the number of element nodes. Enforced during the parse:
	// the parser stops at the exceeding element.
	MaxNodes int
	// MaxPreparedImageBytes caps the total weight of prepared preview
	// images (sum of len(Payload) across the prepared set). Enforced by
	// BuildPreviewImages during preparation: transcoding changes the size,
	// and the prepared bytes are what the cache and the reader's memory
	// actually carry, so the build stops at the first payload that crosses
	// the cap rather than preparing the rest first.
	MaxPreparedImageBytes int
}

// defaultPreviewLimits returns the limits re-derived from the full-catalog
// census (537 628 books). The phase-0 sample of 488 systematically
// under-reported.
const (
	// The numbers live in config: keeping a second copy here would let the
	// production default and the fallback drift apart, and a mutation of
	// this one would pass every test.
	defaultMaxFB2Bytes           = config.PreviewMaxFB2Bytes
	defaultMaxBinaries           = config.PreviewMaxBinaries
	defaultMaxNodes              = config.PreviewMaxNodes
	defaultMaxPreparedImageBytes = config.PreviewMaxPreparedImageBytes
)

func defaultPreviewLimits() PreviewLimits {
	return PreviewLimits{
		MaxFB2Bytes:           defaultMaxFB2Bytes,
		MaxBinaries:           defaultMaxBinaries,
		MaxNodes:              defaultMaxNodes,
		MaxPreparedImageBytes: defaultMaxPreparedImageBytes,
	}
}

// parseForGates is the package-level indirection over
// converter.ParseFB2CompleteLimited so tests can assert "parse was not
// called" for the size gate.
var parseForGates = converter.ParseFB2CompleteLimited

// ArchiveLoader produces the raw FB2 bytes of one file from a zip archive on
// disk. The contract is one file per call; caching, singleflight and error
// wrapping live above this interface, not inside it.
//
// maxBytes bounds the work, not just the result: an implementation must not
// read more than maxBytes+1 bytes of the entry and must refuse anything
// bigger with an error matching ErrFB2TooLarge, so an oversized book is
// rejected without ever being unpacked whole into memory. A non-positive
// maxBytes disables the bound.
type ArchiveLoader interface {
	Load(ctx context.Context, archivePath, fileName string, maxBytes int64) ([]byte, error)
}

// BookRepo is the narrow slice of database operations preview needs.
type BookRepo interface {
	GetBook(bookID int64) (*models.Book, error)
}

// PreviewService is the long-lived object that owns the preview pipeline.
type PreviewService struct {
	books  BookRepo
	loader ArchiveLoader
	cache  PreviewCache

	renderVersion string
	ttl           time.Duration
	limits        PreviewLimits
	chunkPolicy   converter.PreviewPolicy
	imagePolicy   converter.PreviewImagePolicy

	buildTimeout time.Duration
	svcCtx       context.Context
	shutdown     context.CancelFunc

	sf  singleflight.Group
	sem chan struct{}
}

// Default output budgets. Phase 0 measured only the input side; the output
// ceilings are safe defaults to be tuned by the phase-6 measurement, and the
// image values are the historical defaults the converter tests already pin.
const (
	defaultMaxChunkBytes  = 64 << 10 // 64 KB of rendered HTML per portion
	defaultImageMaxBytes  = 1 << 20  // 1 MB per prepared image
	defaultImageMaxPixels = 32 << 20 // 32 MP per canvas
	defaultImageMaxSide   = 4096     // per-side cap, mirrors fb2image.maxDimension
)

func defaultPreviewChunkPolicy() converter.PreviewPolicy {
	return converter.PreviewPolicy{MaxChunkBytes: defaultMaxChunkBytes}
}

func defaultPreviewImagePolicy() converter.PreviewImagePolicy {
	return converter.PreviewImagePolicy{
		MaxBytes:  defaultImageMaxBytes,
		MaxPixels: defaultImageMaxPixels,
		MaxSide:   defaultImageMaxSide,
	}
}

// NewPreviewService wires the service. maxConcurrent sets the ceiling on
// simultaneous cold builds; limits sets the input gates (FB2 size, binary
// count and weight); buildTimeout bounds one cold build; ttl is the lifetime
// stamped on every published cache entry. Each falls back to its safe
// default independently when zero — per field, so an operator overriding one
// gate does not silently reset the others.
func NewPreviewService(
	books BookRepo, loader ArchiveLoader, cache PreviewCache,
	maxConcurrent int, limits PreviewLimits, buildTimeout, ttl time.Duration,
) *PreviewService {
	if maxConcurrent <= 0 {
		maxConcurrent = defaultMaxConcurrentBuilds
	}
	if limits.MaxFB2Bytes <= 0 {
		limits.MaxFB2Bytes = defaultMaxFB2Bytes
	}
	if limits.MaxBinaries <= 0 {
		limits.MaxBinaries = defaultMaxBinaries
	}
	if limits.MaxNodes <= 0 {
		limits.MaxNodes = defaultMaxNodes
	}
	if limits.MaxPreparedImageBytes <= 0 {
		limits.MaxPreparedImageBytes = defaultMaxPreparedImageBytes
	}
	if buildTimeout <= 0 {
		buildTimeout = defaultBuildTimeout
	}
	if ttl <= 0 {
		ttl = cacheKeyTTL
	}
	svcCtx, shutdown := context.WithCancel(context.Background())
	return &PreviewService{
		books:         books,
		loader:        loader,
		cache:         cache,
		renderVersion: renderVersionPrefix,
		ttl:           ttl,
		limits:        limits,
		chunkPolicy:   defaultPreviewChunkPolicy(),
		imagePolicy:   defaultPreviewImagePolicy(),
		buildTimeout:  buildTimeout,
		svcCtx:        svcCtx,
		shutdown:      shutdown,
		sem:           make(chan struct{}, maxConcurrent),
	}
}

// Shutdown stops the service: every in-flight cold build is canceled through
// the service context, and builds that start afterwards fail immediately.
// Without it, a build hung on a dependency would outlive the server stop,
// pinning its build slot and its singleflight key.
func (s *PreviewService) Shutdown() {
	s.shutdown()
}

// PreviewManifest is the JSON document the cache holds as the book's entry
// point. It is an index, never the book: the revision the build ran under,
// the number of portions, the table of contents, and one reference per
// prepared image. A reader discovers the preview through it, so it is
// published last — after every byte it references is already in the cache.
type PreviewManifest struct {
	Revision   string            `json:"revision"`
	ChunkCount int               `json:"chunk_count"`
	TOC        []PreviewTOCEntry `json:"toc"`
	Images     []PreviewImageRef `json:"images"`
}

// PreviewTOCEntry is one row of the manifest's table of contents: the
// section's visible title, its depth, the portion it opens in, and the
// chunk-local anchor the renderer emitted for it (empty when the section
// carries no id and therefore has no anchor to jump to).
type PreviewTOCEntry struct {
	Title  string `json:"title"`
	Depth  int    `json:"depth"`
	Chunk  int    `json:"chunk"`
	Anchor string `json:"anchor,omitempty"`
}

// PreviewImageRef is the manifest's record of one prepared image: its ordinal
// (the address the HTML references), the MIME the preparation decided on, and
// the payload size, so a reader can budget before fetching.
type PreviewImageRef struct {
	Ordinal int    `json:"ordinal"`
	MIME    string `json:"mime"`
	Bytes   int    `json:"bytes"`
}

// buildRevision derives the opaque revision that ties a book's prepared
// images to the rendered HTML referencing them. Three inputs go in:
//
//   - the book fingerprint (MD5): a re-scanned book must not serve the old
//     cutting;
//   - the render version: a renderer bump must not serve chunks rendered
//     under the old rules;
//   - every image-policy knob that changes the prepared bytes (MaxBytes,
//     MaxPixels, MaxSide): a new per-side cap must not let images prepared
//     under the old cap answer the URLs of the new HTML.
//
// The output is a truncated hash: short, opaque, and inside the character
// set converter.NewPreviewImageBase accepts for a URL path segment.
func buildRevision(md5, renderVersion string, imagePolicy converter.PreviewImagePolicy) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%d",
		md5, renderVersion, imagePolicy.MaxBytes, imagePolicy.MaxPixels, imagePolicy.MaxSide)))
	return hex.EncodeToString(sum[:])[:16]
}

// revision computes the revision for one book under the service's current
// render version and image policy.
func (s *PreviewService) revision(book *models.Book) string {
	return buildRevision(book.MD5, s.renderVersion, s.imagePolicy)
}

// Load is the single entry point. It resolves the book, checks visibility
// and format on the request's context, then either returns a cached entry
// or kicks off a singleflighted cold build.
//
// The build runs on its own context — a child of the service context with
// the cold-build timeout — not on the request's context. This is deliberate:
// if every waiter cancels, the build still completes and writes to the
// cache, so the next reader gets a warm hit. The request context gates only
// the wait (through DoChan + select), not the work. But detached does not
// mean unbounded: the timeout cuts off a hung loader or a hung Redis, and
// Shutdown cancels everything in flight — without those, a stuck build would
// pin its slot and its flight key forever, silently stopping all preview
// builds once the slots run out.
func (s *PreviewService) Load(ctx context.Context, bookID int64, isSuperUser bool) ([]byte, error) {
	book, err := s.books.GetBook(bookID)
	if err != nil {
		return nil, fmt.Errorf("preview: lookup book %d: %w", bookID, err)
	}
	if book == nil {
		return nil, fmt.Errorf("%w: book id %d", ErrBookNotFound, bookID)
	}
	if !visibleTo(book, isSuperUser) {
		return nil, fmt.Errorf("%w: book id %d", ErrBookNotVisible, bookID)
	}
	if book.Format != formatFB2 {
		return nil, fmt.Errorf("%w: format %q", ErrUnsupportedFormat, book.Format)
	}
	if book.MD5 == "" {
		return nil, fmt.Errorf("%w: book id %d", ErrEmptyMD5, bookID)
	}

	if perr := s.cache.Ping(ctx); perr != nil {
		return nil, fmt.Errorf("%w: %v", ErrCacheUnavailable, perr)
	}

	key := buildCacheKey(book.ID, book.MD5, s.revision(book))

	// Cache hit: manifest AND first chunk.
	if manifest, hit, cerr := s.cachedEntry(ctx, key); cerr != nil {
		return nil, cerr
	} else if hit {
		return manifest, nil
	}

	// A reader who already went away must not start a background build: the
	// work would run detached with nobody waiting for the result. Once a
	// build HAS started it completes regardless of cancellation (see the
	// flight body below) — this gate is only for work that has not begun.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Cache miss → singleflight. DoChan returns a channel so a waiter can
	// abandon the wait without aborting the build.
	ch := s.sf.DoChan(key, func() (interface{}, error) {
		// The build context descends from the service context, NOT from the
		// request's: the build must survive every waiter going away, but it
		// is still bounded by the cold-build timeout and by Shutdown.
		buildCtx, cancel := context.WithTimeout(s.svcCtx, s.buildTimeout)
		defer cancel()

		// Re-check the cache inside the flight, before taking a build slot:
		// between the miss above and this flight starting, a previous build
		// may have completed and published. Without the re-check, a request
		// that raced a finished build re-reads the archive for nothing.
		if manifest, hit, cerr := s.cachedEntry(buildCtx, key); cerr != nil {
			return nil, cerr
		} else if hit {
			return manifest, nil
		}

		return s.buildAndCache(buildCtx, key, book)
	})

	select {
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
		return res.Val.([]byte), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// cachedEntry returns the manifest if the cache holds a complete entry — the
// manifest AND the first chunk (a manifest without chunks is a stale remnant
// of an interrupted build and counts as a miss).
//
// The three outcomes are distinct on purpose: hit, miss, broken. A miss
// means "build it"; a broken backend means "refuse" — an infrastructure
// outage must never fall through to a cold build, the most expensive
// operation the service has, at the moment the system can least afford it.
// The cache contract is typed: an absent key is reported with an error
// matching ErrCacheMiss, and any other read error is treated here as backend
// unavailability, whatever its concrete type.
func (s *PreviewService) cachedEntry(ctx context.Context, key string) (manifest []byte, hit bool, err error) {
	manifest, err = s.cache.GetManifest(ctx, key)
	if err != nil {
		if errors.Is(err, ErrCacheMiss) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("%w: read manifest: %w", ErrCacheUnavailable, err)
	}
	if _, cerr := s.cache.GetChunk(ctx, key, 0); cerr != nil {
		if errors.Is(cerr, ErrCacheMiss) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("%w: read first chunk: %w", ErrCacheUnavailable, cerr)
	}
	return manifest, true, nil
}

// buildAndCache is the single-flight body: it acquires a build slot, loads
// the archive, and writes the result to the cache. buildCtx bounds the work:
// the caller passes a child of the service context carrying the cold-build
// timeout, so the build survives request cancellation but not a hung
// dependency or a service stop.
func (s *PreviewService) buildAndCache(buildCtx context.Context, key string, book *models.Book) ([]byte, error) {
	// Non-blocking semaphore: if the ceiling is reached, refuse rather
	// than queue. Every waiter for this key gets ErrTooManyBuilds through
	// singleflight — they do not wait.
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	default:
		return nil, fmt.Errorf("%w: key %s", ErrTooManyBuilds, key)
	}

	data, err := s.loader.Load(buildCtx, book.Path, book.FileName, int64(s.limits.MaxFB2Bytes))
	if err != nil {
		return nil, fmt.Errorf("preview: load archive: %w", err)
	}

	// Gate 1: FB2 size. The loader enforces this bound while reading — a
	// conforming loader never returns more than the cap — so this check is
	// the backstop that keeps the gate honest for loaders that do not
	// enforce it themselves (test fakes, future implementations). It costs
	// nothing: the bytes are already here.
	if len(data) > s.limits.MaxFB2Bytes {
		return nil, fmt.Errorf("%w: %d bytes, cap is %d",
			ErrFB2TooLarge, len(data), s.limits.MaxFB2Bytes)
	}

	// Parse under the work budget: the parser counts nodes and binaries as
	// it goes and stops at the first element over a cap, so a hostile book
	// is refused without its tree ever being built. readCover=false: the
	// cover is not needed for the gate, and decoding it is wasted work.
	doc, _, _, perr := parseForGates(buildCtx, data, false, converter.FB2ParseLimits{
		MaxNodes:    s.limits.MaxNodes,
		MaxBinaries: s.limits.MaxBinaries,
	})
	if perr != nil {
		// The parse-time refusals map onto the gates' own typed errors;
		// anything else is a broken book or a broken parser.
		switch {
		case errors.Is(perr, converter.ErrFB2BinaryLimit):
			return nil, fmt.Errorf("%w: cap is %d", ErrTooManyBinaries, s.limits.MaxBinaries)
		case errors.Is(perr, converter.ErrFB2NodeLimit):
			return nil, fmt.Errorf("%w: cap is %d", ErrTooManyNodes, s.limits.MaxNodes)
		}
		return nil, fmt.Errorf("preview: parse for gate check: %w", perr)
	}

	// The cold-build pipeline: the revision ties everything below together,
	// so it is computed once and shared by the image base, the manifest, and
	// (through the caller) the cache key.
	revision := s.revision(book)
	base, err := converter.NewPreviewImageBase(book.ID, revision)
	if err != nil {
		return nil, fmt.Errorf("preview: image base: %w", err)
	}
	// Only the binaries the document actually references reach preparation.
	// An unreferenced one — the cover among them, see converter.UsedBinaries —
	// would burn the decode, occupy memory, cache and a manifest slot, and
	// could push the build over the total budget although no markup points
	// at it.
	imageSet, err := converter.BuildPreviewImages(buildCtx, converter.UsedBinaries(doc), base, s.imagePolicy, s.limits.MaxPreparedImageBytes)
	if err != nil {
		// The total-weight gate fires inside the build, between one prepare
		// and the next, so the refusal also proves the work stopped early.
		if errors.Is(err, converter.ErrPreviewImagesTotalTooLarge) {
			return nil, fmt.Errorf("%w: %w", ErrPreparedImagesTooLarge, err)
		}
		return nil, fmt.Errorf("preview: build images: %w", err)
	}
	// Refusals (imageSet.Refusals) are NOT build errors: a picture that
	// cannot be shown stays a placeholder in the HTML and the book opens.
	// Only a failed cache write below refuses the build.
	prepared := imageSet.Images()

	projection := imageSet.Projection()

	chunks, err := converter.ChunkPreview(buildCtx, doc, projection, s.chunkPolicy)
	if err != nil {
		return nil, fmt.Errorf("preview: chunk: %w", err)
	}

	rendered := make([][]byte, len(chunks))
	for i, chunk := range chunks {
		// Check between renders: a timeout that fired during the parse
		// or an earlier render must not let the build keep rendering and
		// eventually publish a manifest past its deadline.
		if cerr := buildCtx.Err(); cerr != nil {
			return nil, fmt.Errorf("preview: render canceled at chunk %d: %w", i, cerr)
		}
		html, rerr := converter.RenderChunkHTML(chunk, projection, s.chunkPolicy)
		if rerr != nil {
			return nil, fmt.Errorf("preview: render chunk %d: %w", chunk.Index, rerr)
		}
		rendered[i] = []byte(html)
	}

	// The write order is the phase-3 invariant, not a style: every chunk,
	// then every prepared image, and only then the manifest. The manifest is
	// the promise that every <img> in the cached HTML has a prepared resource
	// of the same revision behind it; publishing it before a byte that
	// promise covers would open a window where the server hands out a broken
	// image on purpose. A failed write anywhere refuses the build — and with
	// no manifest written, the next request simply rebuilds.
	//
	// Each write is gated on buildCtx.Err(): a canceled build must not
	// publish partial work, and — critically — must not publish the
	// manifest after the deadline, because a late manifest is a promise
	// the build already broke.
	for i, html := range rendered {
		if cerr := buildCtx.Err(); cerr != nil {
			return nil, fmt.Errorf("preview: cache canceled at chunk %d: %w", i, cerr)
		}
		if werr := s.cache.PutChunk(buildCtx, key, i, html, s.ttl); werr != nil {
			return nil, fmt.Errorf("preview: cache chunk %d: %w", i, werr)
		}
	}
	for _, img := range prepared {
		if cerr := buildCtx.Err(); cerr != nil {
			return nil, fmt.Errorf("preview: cache canceled at image %d: %w", img.Ordinal, cerr)
		}
		if werr := s.cache.PutImage(buildCtx, key, img.Ordinal, img.Payload, img.MIME, s.ttl); werr != nil {
			return nil, fmt.Errorf("preview: cache image %d: %w", img.Ordinal, werr)
		}
	}

	manifest := PreviewManifest{
		Revision:   revision,
		ChunkCount: len(chunks),
		TOC:        buildPreviewTOC(chunks),
		Images:     make([]PreviewImageRef, 0, len(prepared)),
	}
	for _, img := range prepared {
		manifest.Images = append(manifest.Images, PreviewImageRef{
			Ordinal: img.Ordinal,
			MIME:    img.MIME,
			Bytes:   len(img.Payload),
		})
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("preview: marshal manifest: %w", err)
	}
	// The manifest is the build's final promise: every chunk and image is
	// cached, and the build is still within its budget. A check here, right
	// before the write, is what prevents a build that crossed its deadline
	// during the last image write from publishing a manifest for work that
	// the timeout was supposed to stop.
	if cerr := buildCtx.Err(); cerr != nil {
		return nil, fmt.Errorf("preview: manifest write canceled: %w", cerr)
	}
	if err := s.cache.PutManifest(buildCtx, key, manifestJSON, s.ttl); err != nil {
		return nil, fmt.Errorf("preview: cache manifest: %w", err)
	}

	return manifestJSON, nil
}

// buildPreviewTOC assembles the manifest's table of contents from the
// headings of every portion, in reading order.
func buildPreviewTOC(chunks []*converter.PreviewChunk) []PreviewTOCEntry {
	toc := make([]PreviewTOCEntry, 0)
	for _, chunk := range chunks {
		for _, h := range chunk.Headings() {
			toc = append(toc, PreviewTOCEntry{
				Title:  h.Title,
				Depth:  h.Depth,
				Chunk:  chunk.Index,
				Anchor: h.Anchor,
			})
		}
	}
	return toc
}

// visibleTo reports whether a reader with the given superuser flag may open
// the book.
func visibleTo(book *models.Book, isSuperUser bool) bool {
	if isSuperUser {
		return true
	}
	return book.Approved && !book.DuplicateHidden
}
