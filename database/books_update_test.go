package database

import (
	"testing"
	"time"

	"gopds-api/models"
)

// These pin the two ways an update could rewrite a column nobody asked it to.
//
// go-pg reads an Update with no Set as "write every column", so a request that
// named no field rewrote the whole row from the struct in memory; and a zero
// value is written as NULL unless the field says use_zero, so the row it wrote
// carried NULL where the schema demands NOT NULL. Together they meant an empty
// update failed outright, and a book that was merely unapproved could not be
// rewritten at all.

// readBook fetches a book straight from the table, past any list filtering.
func readBook(t *testing.T, id int64) models.Book {
	t.Helper()

	var book models.Book
	if err := db.Model(&book).Where("id = ?", id).Select(); err != nil {
		t.Fatalf("reading book %d: %v", id, err)
	}
	return book
}

// aBook finds one book to work on, including the hidden and unapproved ones the
// catalog listing never returns — which are exactly the rows that used to fail.
func aBook(t *testing.T, where string) models.Book {
	t.Helper()

	var book models.Book
	err := db.Model(&book).Where(where).Limit(1).Select()
	if err != nil {
		t.Skipf("no book matching %s to test with: %v", where, err)
	}
	return book
}

func TestUpdateBookWithNoFieldsChangesNothing(t *testing.T) {
	requireDatabase(t)

	before := aBook(t, "1 = 1")

	if _, err := UpdateBook(models.BookUpdateRequest{ID: before.ID}); err != nil {
		t.Fatalf("an update naming no field: %v", err)
	}

	after := readBook(t, before.ID)
	switch {
	case after.Title != before.Title:
		t.Errorf("title changed: %q became %q", before.Title, after.Title)
	case after.Lang != before.Lang:
		t.Errorf("lang changed: %q became %q", before.Lang, after.Lang)
	case after.Annotation != before.Annotation:
		t.Error("annotation changed")
	case after.Approved != before.Approved:
		t.Errorf("approved changed: %v became %v", before.Approved, after.Approved)
	case after.DuplicateHidden != before.DuplicateHidden:
		t.Errorf("duplicate_hidden changed: %v became %v",
			before.DuplicateHidden, after.DuplicateHidden)
	case after.MD5 != before.MD5:
		t.Error("md5 changed")
	case after.Path != before.Path:
		t.Error("path changed")
	}
}

// The flags are NOT NULL, and false is the value that used to be written as
// NULL. A book carrying false in both is the row that could not be updated.
func TestUpdateBookKeepsFalseFlags(t *testing.T) {
	requireDatabase(t)

	before := aBook(t, "approved = false AND duplicate_hidden = false")

	title := before.Title + " (touched)"
	updated, err := UpdateBook(models.BookUpdateRequest{ID: before.ID, Title: &title})
	if err != nil {
		t.Fatalf("updating the title of an unapproved book: %v", err)
	}

	// Restore before asserting, so a failure does not leave the row renamed.
	original := before.Title
	if _, err := UpdateBook(models.BookUpdateRequest{
		ID:    before.ID,
		Title: &original,
	}); err != nil {
		t.Fatalf("restoring the title: %v", err)
	}

	if updated.Title != title {
		t.Errorf("title was not updated: %q", updated.Title)
	}

	after := readBook(t, before.ID)
	if after.Approved {
		t.Error("approved was flipped by an update that never named it")
	}
	if after.DuplicateHidden {
		t.Error("duplicate_hidden was flipped by an update that never named it")
	}
	if after.Title != original {
		t.Errorf("the title was left as %q", after.Title)
	}
}

// approved carries DEFAULT true in the schema, so a column go-pg leaves out of
// an INSERT comes back as approved. Without use_zero it left the column out
// whenever the field was false, which quietly published a book that was
// inserted unapproved.
func TestInsertBookKeepsApprovedFalse(t *testing.T) {
	requireDatabase(t)

	book := models.Book{
		Path:         "test-approved-false.zip",
		Format:       "fb2",
		FileName:     "test-approved-false.fb2",
		RegisterDate: time.Now(),
		Title:        "Approved false on insert",
		MD5:          "0000000000000000000000000000dead",
		Approved:     false,
	}

	if _, err := db.Model(&book).Insert(); err != nil {
		t.Fatalf("inserting an unapproved book: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Model(&models.Book{}).Where("id = ?", book.ID).Delete(); err != nil {
			t.Errorf("removing the test book: %v", err)
		}
	})

	if stored := readBook(t, book.ID); stored.Approved {
		t.Error("a book inserted unapproved came back approved")
	}
}

// Setting a flag to false has to mean false, not the absence of a value.
func TestUpdateBookCanSetAFlagToFalse(t *testing.T) {
	requireDatabase(t)

	before := aBook(t, "approved = true")

	off := false
	if _, err := UpdateBook(models.BookUpdateRequest{
		ID:       before.ID,
		Approved: &off,
	}); err != nil {
		t.Fatalf("turning approved off: %v", err)
	}

	after := readBook(t, before.ID)

	on := true
	if _, err := UpdateBook(models.BookUpdateRequest{
		ID:       before.ID,
		Approved: &on,
	}); err != nil {
		t.Fatalf("turning approved back on: %v", err)
	}

	if after.Approved {
		t.Error("approved stayed on after being set to false")
	}
	if restored := readBook(t, before.ID); !restored.Approved {
		t.Error("approved was not restored")
	}
}
