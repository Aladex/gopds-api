package services

// preview_adapters.go holds the production implementations of the two
// interfaces the preview pipeline programs against, plus the constructor the
// application calls.
//
// Both adapters are thin on purpose and live here — next to the interfaces —
// rather than in database/ or utils/: the translation each performs
// (pg.ErrNoRows into a nil book, a missing zip entry into
// ErrArchiveFileNotFound, the files-root prefix onto the catalog-relative
// path) is the preview pipeline's contract, not the wrapped packages'.

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/go-pg/pg/v10"

	"gopds-api/config"
	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/models"
)

// getBookByID is the package-level indirection over database.GetBook, on the
// same pattern as parseForGates: the database package exposes a free
// function over a package-global connection, so tests stub the function,
// not a field.
var getBookByID = database.GetBook

// CatalogBookRepo is the production BookRepo: the catalog's GetBook, with
// the ORM's no-rows translated into the contract's (nil, nil) — "no such
// book" is a normal answer, not a database failure.
type CatalogBookRepo struct{}

// NewCatalogBookRepo wires the repo over the database package's
// package-global connection, which main establishes before building the
// preview service.
func NewCatalogBookRepo() CatalogBookRepo { return CatalogBookRepo{} }

func (CatalogBookRepo) GetBook(bookID int64) (*models.Book, error) {
	book, err := getBookByID(bookID)
	if err != nil {
		if errors.Is(err, pg.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &book, nil
}

// ZipArchiveLoader is the production ArchiveLoader. It opens the zip itself
// rather than going through utils.BookProcessor: BookProcessor reads the
// whole entry into memory before returning, and a gate checked afterwards
// would spend exactly the memory it exists to protect. The entry selection
// is the same (exact name match) and the decompressed bytes are identical,
// so the preview still parses what a reader would download — it just never
// holds more than the cap plus one byte of it.
type ZipArchiveLoader struct {
	// filesPath is the catalog's files root; book rows carry archive paths
	// relative to it.
	filesPath string
}

// NewZipArchiveLoader wires the loader against the catalog's files root.
func NewZipArchiveLoader(filesPath string) *ZipArchiveLoader {
	return &ZipArchiveLoader{filesPath: filesPath}
}

// Load returns the raw FB2 bytes of one archive entry, refusing with
// ErrFB2TooLarge the moment the entry proves bigger than maxBytes. A
// non-positive maxBytes disables the bound.
//
// Two checks, cheapest first:
//
//   - The central directory's UncompressedSize64 is compared against the cap
//     before the entry is even opened. This is an OPTIMIZATION, not a
//     guarantee: the field is part of the untrusted archive and can lie in
//     either direction, so it may only refuse early, never admit.
//   - The read itself is bounded at maxBytes+1 bytes, so a lying catalog
//     cannot smuggle an oversized entry past the first check, and the
//     decompressed payload never exists whole in memory when it is refused.
//
// The context is accepted to satisfy the contract but cannot interrupt the
// blocking file read; what bounds the wait is the cold-build timeout around
// the pipeline.
func (l *ZipArchiveLoader) Load(_ context.Context, archivePath, fileName string, maxBytes int64) ([]byte, error) {
	r, err := zip.OpenReader(filepath.Join(l.filesPath, archivePath))
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := r.Close(); cerr != nil {
			logging.Errorf("preview: failed to close archive %s: %v", archivePath, cerr)
		}
	}()

	for _, f := range r.File {
		if f.Name != fileName {
			continue
		}
		if maxBytes > 0 && f.UncompressedSize64 > uint64(maxBytes) {
			return nil, fmt.Errorf("%w: %s in %s declares %d bytes, cap is %d",
				ErrFB2TooLarge, fileName, archivePath, f.UncompressedSize64, maxBytes)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, rerr := boundedRead(rc, maxBytes)
		if cerr := rc.Close(); cerr != nil {
			logging.Errorf("preview: failed to close book reader: %v", cerr)
		}
		if rerr != nil {
			return nil, fmt.Errorf("preview: read %s in %s: %w", fileName, archivePath, rerr)
		}
		return data, nil
	}
	return nil, fmt.Errorf("%w: %s in %s", ErrArchiveFileNotFound, fileName, archivePath)
}

// boundedRead is the package-level indirection over readBounded, on the same
// pattern as parseForGates. The bound is otherwise unobservable from outside:
// Go's zip reader enforces the declared size while streaming, so no archive
// can push more than the declared (already catalog-checked) bytes through
// f.Open — a test can only watch the read at this seam.
var boundedRead = readBounded

// readBounded reads at most limit+1 bytes from rc and refuses with
// ErrFB2TooLarge when the stream holds more. The +1 is what tells a payload
// of exactly the limit from one over it; a plain LimitReader(rc, limit)
// would truncate both to the same length and could not. A non-positive
// limit disables the bound. The gate's whole point is that the refusal is
// cheap: the oversized bytes are never pulled from the reader, so they are
// never allocated.
func readBounded(rc io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return io.ReadAll(rc)
	}
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: payload exceeds %d bytes", ErrFB2TooLarge, limit)
	}
	return data, nil
}

// NewPreviewServiceFromConfig builds the service from the preview section of
// the application configuration. It is the constructor the application
// calls; keeping it here — one hop from the service — is what makes "the
// configured value reaches the behavior" testable without booting main.
// Zero values fall back to the pipeline's own defaults, so an absent preview
// section behaves exactly as the hardcoded service did.
func NewPreviewServiceFromConfig(
	cfg *config.PreviewConfig,
	books BookRepo, loader ArchiveLoader, cache PreviewCache,
) *PreviewService {
	return NewPreviewService(
		books, loader, cache,
		cfg.MaxConcurrentBuilds,
		PreviewLimits{
			MaxFB2Bytes:           cfg.MaxFB2Bytes,
			MaxBinaries:           cfg.MaxBinaries,
			MaxNodes:              cfg.MaxNodes,
			MaxPreparedImageBytes: cfg.MaxPreparedImageBytes,
		},
		cfg.BuildTimeout,
		cfg.CacheTTL,
	)
}
