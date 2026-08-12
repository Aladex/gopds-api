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
	"gopds-api/utils"
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

// ZipArchiveLoader is the production ArchiveLoader. It reads the FB2 entry
// through utils.BookProcessor — the same code path the download handler
// serves from — so the preview parses exactly the bytes a reader would
// download.
type ZipArchiveLoader struct {
	// filesPath is the catalog's files root; book rows carry archive paths
	// relative to it.
	filesPath string
}

// NewZipArchiveLoader wires the loader against the catalog's files root.
func NewZipArchiveLoader(filesPath string) *ZipArchiveLoader {
	return &ZipArchiveLoader{filesPath: filesPath}
}

// Load returns the raw FB2 bytes of one archive entry. The context is
// accepted to satisfy the contract but cannot interrupt the blocking file
// read — bounding that read is a separate piece of work; what bounds the
// wait today is the cold-build timeout around the pipeline.
func (l *ZipArchiveLoader) Load(_ context.Context, archivePath, fileName string) ([]byte, error) {
	rc, err := utils.NewBookProcessor(fileName, filepath.Join(l.filesPath, archivePath)).FB2()
	if err != nil {
		if errors.Is(err, utils.ErrBookNotInArchive) {
			return nil, fmt.Errorf("%w: %s in %s", ErrArchiveFileNotFound, fileName, archivePath)
		}
		return nil, err
	}
	defer func() {
		if cerr := rc.Close(); cerr != nil {
			logging.Errorf("preview: failed to close book reader: %v", cerr)
		}
	}()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("preview: read %s in %s: %w", fileName, archivePath, err)
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
			MaxFB2Bytes:      cfg.MaxFB2Bytes,
			MaxBinaries:      cfg.MaxBinaries,
			MaxBinariesBytes: cfg.MaxBinariesBytes,
		},
		cfg.BuildTimeout,
		cfg.CacheTTL,
	)
}
