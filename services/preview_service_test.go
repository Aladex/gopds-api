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
	"testing"

	"gopds-api/models"
)

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

// A hidden book must not touch the archive. The assertion is not "an error
// came back" — that could happen after the disk was read. It is "the loader
// was called zero times", which proves the check fired before any work.
func TestPreviewService_HiddenBookDoesNotTouchArchive(t *testing.T) {
	repo := &fakeBookRepo{books: map[int64]*models.Book{
		1: {ID: 1, Format: formatFB2, DuplicateHidden: true, Approved: true},
	}}
	loader := &fakeArchiveLoader{}
	svc := NewPreviewService(repo, loader)

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
		1: {ID: 1, Format: formatFB2, Approved: false, DuplicateHidden: true},
	}}
	loader := &fakeArchiveLoader{data: []byte("<FictionBook/>")}
	svc := NewPreviewService(repo, loader)

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
	svc := NewPreviewService(repo, loader)

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
	svc := NewPreviewService(repo, loader)

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
	svc := NewPreviewService(repo, loader)

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
	svc := NewPreviewService(repo, loader)

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
		1: {ID: 1, Format: formatFB2, Approved: true, DuplicateHidden: false},
	}}
	loader := &fakeArchiveLoader{data: []byte("<FictionBook/>")}
	svc := NewPreviewService(repo, loader)

	if _, err := svc.Load(context.Background(), 1, false); err != nil {
		t.Fatalf("an approved, non-hidden book must pass visibility: %v", err)
	}
	if loader.calls != 1 {
		t.Errorf("loader.calls = %d, want 1 — the gate must pass and reach the archive",
			loader.calls)
	}
}
