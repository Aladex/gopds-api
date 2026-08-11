package services

// preview_service.go is the entry point of the preview pipeline. Given a
// book id and a reader, it decides whether the reader may see the book, and
// only then reaches for the archive. Everything past the load — cache,
// singleflight, parsing, cutting, rendering — is layered on top in later
// phases; this file owns the gate, not the work.
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
//     only possible against a loader that records its own calls. The real
//     adapter moves to utils in a later step; the interface is the contract
//     from day one so the rest of the pipeline programs against it.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sync/singleflight"

	"gopds-api/internal/converter"
	"gopds-api/models"
)

// formatFB2 is the only book format the preview pipeline reads today.
const formatFB2 = "fb2"

// defaultMaxConcurrentBuilds is the ceiling on simultaneous cold builds.
// Production reads this from config; tests override through the constructor.
const defaultMaxConcurrentBuilds = 4

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
	// allows. Checked after parsing, because only the parser knows the count.
	ErrTooManyBinaries = errors.New("preview: too many image binaries")

	// ErrBinariesTooLarge: the total decoded weight of all binaries exceeds
	// the gate. Checked after parsing, for the same reason.
	ErrBinariesTooLarge = errors.New("preview: image binaries exceed the total weight gate")
)

// PreviewLimits are the input gates that protect the pipeline from books that
// would tie up memory or time. Values are read from config in production;
// tests override through the constructor.
type PreviewLimits struct {
	// MaxFB2Bytes caps the raw FB2 payload. Checked before parsing.
	MaxFB2Bytes int
	// MaxBinaries caps the number of <binary> elements. Checked after
	// parsing, because only the parser knows the count.
	MaxBinaries int
	// MaxBinariesBytes caps the total decoded weight of all binaries.
	MaxBinariesBytes int
}

// defaultPreviewLimits returns the limits derived from the phase-0 catalog
// measurement: max FB2 31 MB, max binaries 519, max binary weight 22 MB.
const (
	defaultMaxFB2Bytes      = 32 << 20 // 32 MB
	defaultMaxBinaries      = 1000
	defaultMaxBinariesBytes = 32 << 20 // 32 MB
)

func defaultPreviewLimits() PreviewLimits {
	return PreviewLimits{
		MaxFB2Bytes:      defaultMaxFB2Bytes,
		MaxBinaries:      defaultMaxBinaries,
		MaxBinariesBytes: defaultMaxBinariesBytes,
	}
}

// parseForGates is the package-level indirection over converter.ParseFB2Complete
// so tests can assert "parse was not called" for the size gate.
var parseForGates = converter.ParseFB2Complete

// ArchiveLoader produces the raw FB2 bytes of one file from a zip archive on
// disk. The contract is one file per call; caching, singleflight and error
// wrapping live above this interface, not inside it.
type ArchiveLoader interface {
	Load(ctx context.Context, archivePath, fileName string) ([]byte, error)
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

	sf  singleflight.Group
	sem chan struct{}
}

// NewPreviewService wires the service. maxConcurrent sets the ceiling on
// simultaneous cold builds; limits sets the input gates (FB2 size, binary
// count and weight). Both fall back to safe defaults when zero.
func NewPreviewService(books BookRepo, loader ArchiveLoader, cache PreviewCache, maxConcurrent int, limits PreviewLimits) *PreviewService {
	if maxConcurrent <= 0 {
		maxConcurrent = defaultMaxConcurrentBuilds
	}
	if limits.MaxFB2Bytes <= 0 {
		limits = defaultPreviewLimits()
	}
	return &PreviewService{
		books:         books,
		loader:        loader,
		cache:         cache,
		renderVersion: renderVersionPrefix,
		ttl:           cacheKeyTTL,
		limits:        limits,
		sem:           make(chan struct{}, maxConcurrent),
	}
}

// Load is the single entry point. It resolves the book, checks visibility
// and format on the request's context, then either returns a cached entry
// or kicks off a singleflighted cold build.
//
// The build runs on its own context (context.Background), not on the
// request's context. This is deliberate: if every waiter cancels, the
// build still completes and writes to the cache — the next reader gets a
// warm hit. The request context gates only the wait (through DoChan +
// select), not the work.
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

	key := buildCacheKey(book.MD5, s.renderVersion)

	// Cache hit: manifest AND first chunk.
	if manifest, gerr := s.cache.GetManifest(ctx, key); gerr == nil {
		if _, cerr := s.cache.GetChunk(ctx, key, 0); cerr == nil {
			return manifest, nil
		}
	}

	// Cache miss → singleflight. DoChan returns a channel so a waiter can
	// abandon the wait without aborting the build.
	//
	// The build runs on context.Background(), NOT on the request's ctx.
	// This is deliberate (plan tests 8, 9): if every waiter cancels, the
	// build still completes and writes to the cache — the next reader gets
	// a warm hit. Passing ctx here would tie the build's lifetime to the
	// first caller's request, and a single cancel would abort the work
	// every waiter is sharing.
	ch := s.sf.DoChan(key, func() (interface{}, error) {
		return s.buildAndCache(context.Background(), key, book)
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

// buildAndCache is the single-flight body: it acquires a build slot, loads
// the archive, and writes the result to the cache. buildCtx is the context
// the work runs under — the caller passes context.Background() so the build
// survives request cancellation.
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

	// The build context is passed by the caller (context.Background()).
	// If all waiters cancel, the build still completes and writes to cache.

	data, err := s.loader.Load(buildCtx, book.Path, book.FileName)
	if err != nil {
		return nil, fmt.Errorf("preview: load archive: %w", err)
	}

	// Gate 1: FB2 size — checked before parsing, so an oversized file
	// never reaches the parser.
	if len(data) > s.limits.MaxFB2Bytes {
		return nil, fmt.Errorf("%w: %d bytes, cap is %d",
			ErrFB2TooLarge, len(data), s.limits.MaxFB2Bytes)
	}

	// Parse to check binary limits. readCover=false: the cover is not
	// needed for the gate, and decoding it is wasted work.
	doc, _, perr := parseForGates(buildCtx, data, false)
	if perr != nil {
		return nil, fmt.Errorf("preview: parse for gate check: %w", perr)
	}

	// Gate 2: binary count.
	if len(doc.Binary) > s.limits.MaxBinaries {
		return nil, fmt.Errorf("%w: %d, cap is %d",
			ErrTooManyBinaries, len(doc.Binary), s.limits.MaxBinaries)
	}

	// Gate 3: total decoded weight of all binaries.
	var binBytes int
	for _, b := range doc.Binary {
		binBytes += len(b.Data)
	}
	if binBytes > s.limits.MaxBinariesBytes {
		return nil, fmt.Errorf("%w: %d bytes, cap is %d",
			ErrBinariesTooLarge, binBytes, s.limits.MaxBinariesBytes)
	}

	// Chunks first, manifest last — see the cache layer for why.
	if err := s.cache.PutChunk(buildCtx, key, 0, data, s.ttl); err != nil {
		return nil, fmt.Errorf("preview: cache chunk: %w", err)
	}
	if err := s.cache.PutManifest(buildCtx, key, data, s.ttl); err != nil {
		return nil, fmt.Errorf("preview: cache manifest: %w", err)
	}

	return data, nil
}

// visibleTo reports whether a reader with the given superuser flag may open
// the book.
func visibleTo(book *models.Book, isSuperUser bool) bool {
	if isSuperUser {
		return true
	}
	return book.Approved && !book.DuplicateHidden
}
