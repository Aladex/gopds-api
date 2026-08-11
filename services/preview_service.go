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

	"gopds-api/models"
)

// formatFB2 is the only book format the preview pipeline reads today.
// Named as a constant because it appears in a check here and in test
// fixtures — a magic string repeated four times is exactly what goconst
// flags, and a rename would otherwise touch each occurrence by hand.
const formatFB2 = "fb2"

// Typed refusals. They are distinct on purpose: the caller (an HTTP handler
// in phase 4) maps each to a different status, and conflating "not found"
// with "not visible" with "wrong format" would erase exactly the signal a
// reader or operator needs.
var (
	// ErrBookNotFound: there is no book row with this id.
	ErrBookNotFound = errors.New("preview: book not found")

	// ErrBookNotVisible: the book exists but the reader may not open it.
	// A reader only sees approved, non-hidden books; a superuser bypasses
	// both gates. Surfacing "not visible" instead of "not found" is a
	// deliberate choice: the consumer is an authenticated handler, not the
	// anonymous web — and even for the web, a 404 vs 403 distinction
	// matters for the UI (open vs "ask admin").
	ErrBookNotVisible = errors.New("preview: book is not visible to this reader")

	// ErrUnsupportedFormat: the book is stored in a format the preview
	// pipeline does not read. Today that is "anything but fb2".
	ErrUnsupportedFormat = errors.New("preview: book format is not supported for preview")
)

// ArchiveLoader produces the raw FB2 bytes of one file from a zip archive on
// disk. The contract is one file per call; caching, singleflight and error
// wrapping live above this interface, not inside it.
type ArchiveLoader interface {
	Load(ctx context.Context, archivePath, fileName string) ([]byte, error)
}

// BookRepo is the narrow slice of database operations preview needs. Keeping
// it narrow means tests fake four lines, not the whole ORM; it also means
// the service cannot accidentally grow dependencies on the database package
// without first widening this interface.
type BookRepo interface {
	// GetBook returns the book with the given id, or (nil, nil) when no
	// such book exists. A non-nil error means the lookup itself failed,
	// which is a different outcome from "the book is not in the catalog".
	GetBook(bookID int64) (*models.Book, error)
}

// PreviewService is the long-lived object that owns the preview pipeline.
// Construction is cheap; the dependencies (a book repo and an archive
// loader) are the things that take wiring, and they arrive through the
// constructor so the service never reaches for globals.
type PreviewService struct {
	books  BookRepo
	loader ArchiveLoader
}

// NewPreviewService wires the service. The constructor takes interfaces on
// purpose: production wires in a database-backed BookRepo and a zip-backed
// ArchiveLoader, tests wire in doubles — and neither has to know which.
func NewPreviewService(books BookRepo, loader ArchiveLoader) *PreviewService {
	return &PreviewService{books: books, loader: loader}
}

// Load is the single entry point of this step. It resolves the book, checks
// that the reader may see it, refuses anything but fb2, and only then asks
// the loader for bytes. Every refusal is typed; every success returns the
// FB2 bytes that the rest of the pipeline will parse.
//
// The reader identity is a single boolean today. The plan adds authors and a
// per-reader history in later steps, but visibility does not need them, and
// pulling a full user object through this signature would promise more than
// the gate uses.
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
	return s.loader.Load(ctx, book.Path, book.FileName)
}

// visibleTo reports whether a reader with the given superuser flag may open
// the book. The catalog's visibility rule is "approved AND not hidden";
// superuser bypasses both. Splitting this out keeps Load readable and gives
// a stable place for the rule if the policy grows.
func visibleTo(book *models.Book, isSuperUser bool) bool {
	if isSuperUser {
		return true
	}
	return book.Approved && !book.DuplicateHidden
}
