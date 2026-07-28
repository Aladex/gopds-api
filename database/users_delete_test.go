package database

import (
	"fmt"
	"testing"
	"time"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// Deleting a user used to fail for anybody who had ever used the service:
// favorites, votes and collections all reference auth_user with no delete
// rule, so the row would not go and the admin saw a raw constraint violation.
//
// These tests build a user with one of each, delete them, and check both that
// it worked and that nothing of theirs was left behind — and, just as
// important, that nothing of anyone else's went with them.

// makeUser creates a throwaway account and returns its id.
func makeUser(t *testing.T, name string) int64 {
	t.Helper()
	user := models.User{
		Login:      name,
		Password:   "x",
		Email:      name + "@example.test",
		DateJoined: time.Now(),
	}
	if _, err := db.Model(&user).Insert(); err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}
	return user.ID
}

// makeCollection creates a collection owned by the given user, or an ownerless
// curated one when ownerID is nil, and returns its id.
func makeCollection(t *testing.T, name string, ownerID *int64, curated bool) int64 {
	t.Helper()
	collection := models.BookCollection{
		Name:      name,
		IsCurated: curated,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	collection.UserID = ownerID
	if _, err := db.Model(&collection).Insert(); err != nil {
		t.Fatalf("creating collection %s: %v", name, err)
	}
	return collection.ID
}

func countRows(t *testing.T, table, where string, args ...interface{}) int {
	t.Helper()
	var n int
	_, err := db.QueryOne(pg.Scan(&n), fmt.Sprintf("SELECT count(*) FROM %s WHERE %s", table, where), args...)
	if err != nil {
		t.Fatalf("counting %s: %v", table, err)
	}
	return n
}

func TestDeleteUserRemovesWhatBelongsToThem(t *testing.T) {
	requireDatabase(t)

	stamp := time.Now().UnixNano()
	id := makeUser(t, fmt.Sprintf("delete-me-%d", stamp))
	collectionID := makeCollection(t, fmt.Sprintf("own-%d", stamp), &id, false)

	// One of everything that used to block the delete.
	if _, err := db.Exec(
		`INSERT INTO favorite_books (user_id, book_id) SELECT ?, id FROM opds_catalog_book LIMIT 1`, id,
	); err != nil {
		t.Fatalf("adding a favorite: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO collection_votes (user_id, collection_id, vote) VALUES (?, ?, true)`, id, collectionID,
	); err != nil {
		t.Fatalf("adding a vote: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO book_collection_books (book_collection_id, book_id) SELECT ?, id FROM opds_catalog_book LIMIT 1`,
		collectionID,
	); err != nil {
		t.Fatalf("adding a book to the collection: %v", err)
	}

	if err := DeleteUser(fmt.Sprint(id)); err != nil {
		t.Fatalf("deleting a user with favorites, votes and a collection: %v", err)
	}

	for _, check := range []struct {
		table string
		where string
		arg   interface{}
	}{
		{"auth_user", "id = ?", id},
		{"favorite_books", "user_id = ?", id},
		{"collection_votes", "user_id = ?", id},
		{"book_collections", "user_id = ?", id},
		{"book_collection_books", "book_collection_id = ?", collectionID},
	} {
		if n := countRows(t, check.table, check.where, check.arg); n != 0 {
			t.Errorf("%s still holds %d row(s) for the deleted user", check.table, n)
		}
	}
}

func TestDeleteUserLeavesTheLibraryAlone(t *testing.T) {
	requireDatabase(t)

	stamp := time.Now().UnixNano()
	id := makeUser(t, fmt.Sprintf("delete-me-too-%d", stamp))
	other := makeUser(t, fmt.Sprintf("innocent-%d", stamp))

	// Curated collections are library content and belong to nobody. Another
	// reader's collection belongs to them.
	curated := makeCollection(t, fmt.Sprintf("curated-%d", stamp), nil, true)
	theirs := makeCollection(t, fmt.Sprintf("theirs-%d", stamp), &other, false)

	// The user being deleted voted on both.
	for _, target := range []int64{curated, theirs} {
		if _, err := db.Exec(
			`INSERT INTO collection_votes (user_id, collection_id, vote) VALUES (?, ?, true)`, id, target,
		); err != nil {
			t.Fatalf("adding a vote: %v", err)
		}
	}

	if err := DeleteUser(fmt.Sprint(id)); err != nil {
		t.Fatalf("deleting the user: %v", err)
	}

	if n := countRows(t, "book_collections", "id = ?", curated); n != 1 {
		t.Error("a curated collection was removed with the user who voted on it")
	}
	if n := countRows(t, "book_collections", "id = ?", theirs); n != 1 {
		t.Error("another reader's collection was removed with the user who voted on it")
	}
	if n := countRows(t, "auth_user", "id = ?", other); n != 1 {
		t.Error("another reader was removed")
	}

	// Only the departing user's votes go.
	if n := countRows(t, "collection_votes", "user_id = ?", id); n != 0 {
		t.Errorf("%d vote(s) of the deleted user survived", n)
	}

	// Clean up what this test made.
	_, _ = db.Exec(`DELETE FROM book_collections WHERE id IN (?, ?)`, curated, theirs)
	_, _ = db.Exec(`DELETE FROM auth_user WHERE id = ?`, other)
}
