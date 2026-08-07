package database

import (
	"errors"
	"testing"

	"gopds-api/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetBooksRefusesTextSearch pins the boundary this phase draws: the list
// path has no text search, and a request that carries a needle must fail
// rather than quietly return the whole catalog ordered by date. The check
// runs before any query, so it needs no database.
func TestGetBooksRefusesTextSearch(t *testing.T) {
	for _, title := range []string{"война", "  толстой  "} {
		books, count, err := GetBooks(1, models.BookFilters{Title: title, Limit: 5})
		assert.True(t, errors.Is(err, ErrTextSearchUnsupported),
			"title %q must be refused, got err=%v", title, err)
		assert.Nil(t, books)
		assert.Zero(t, count)
	}

	// Whitespace is not text: an all-blank title is an empty one.
	requireDatabase(t)
	_, _, err := GetBooks(1, models.BookFilters{Title: "   ", Limit: 1})
	assert.NoError(t, err, "a blank title is an ordinary list, not a search")
}

// TestGetBooksServesOrdinaryLists is the regression that guards the deletion
// of the old title path: every list the catalog draws without a needle must
// still answer, and must still honor its scope.
func TestGetBooksServesOrdinaryLists(t *testing.T) {
	requireDatabase(t)

	const userID = 1

	newest, total, err := GetBooks(userID, models.BookFilters{Limit: 5})
	require.NoError(t, err, "the plain new-books list must answer")
	assert.Positive(t, total, "the test catalog is expected to hold books")
	assert.LessOrEqual(t, len(newest), 5)
	require.NotEmpty(t, newest, "the plain list must return rows")

	t.Run("author scope", func(t *testing.T) {
		var authorID int
		for _, b := range newest {
			if len(b.Authors) > 0 {
				authorID = int(b.Authors[0].ID)
				break
			}
		}
		if authorID == 0 {
			t.Skip("no authored book among the newest rows")
		}

		books, count, err := GetBooks(userID, models.BookFilters{Author: authorID, Limit: 10})
		require.NoError(t, err)
		assert.Positive(t, count, "the author owns at least the book we took the id from")
		for _, b := range books {
			var found bool
			for _, a := range b.Authors {
				if int(a.ID) == authorID {
					found = true
					break
				}
			}
			assert.True(t, found, "book %d escaped the author scope", b.ID)
		}
	})

	t.Run("series scope", func(t *testing.T) {
		var seriesID int
		for _, b := range newest {
			if len(b.Series) > 0 {
				seriesID = int(b.Series[0].ID)
				break
			}
		}
		if seriesID == 0 {
			t.Skip("no serial book among the newest rows")
		}

		books, count, err := GetBooks(userID, models.BookFilters{Series: seriesID, Limit: 10})
		require.NoError(t, err)
		assert.Positive(t, count)
		for _, b := range books {
			var found bool
			for _, s := range b.Series {
				if int(s.ID) == seriesID {
					found = true
					break
				}
			}
			assert.True(t, found, "book %d escaped the series scope", b.ID)
		}
	})

	// The remaining scopes may legitimately be empty in a given dump, so they
	// assert that the query runs and stays inside its budget, not that it
	// finds rows. An error here is the regression worth catching.
	for _, scope := range []struct {
		name    string
		filters models.BookFilters
	}{
		{"favorites", models.BookFilters{Fav: true, Limit: 5}},
		{"collection", models.BookFilters{Collection: 1, Limit: 5}},
		{"curated collection", models.BookFilters{CuratedCollection: 1, Limit: 5}},
		{"genre", models.BookFilters{Genre: 1, Limit: 5}},
		{"language", models.BookFilters{Lang: "ru", Limit: 5}},
		{"moderation queue", models.BookFilters{UnApproved: true, Limit: 5}},
	} {
		t.Run(scope.name, func(t *testing.T) {
			books, count, err := GetBooks(userID, scope.filters)
			require.NoError(t, err)
			assert.GreaterOrEqual(t, count, 0)
			assert.LessOrEqual(t, len(books), scope.filters.Limit)
		})
	}
}

// TestGetBooksFailsClosedOnAnEmptyScope pins the safe reading of a scope that
// resolves to nothing. Three of the four junction scopes used to skip their
// own filter when they found no book, so "this author's books" for an author
// with none answered with the newest hundred books in the catalog. Only the
// curated-collection scope failed closed; now they all do.
func TestGetBooksFailsClosedOnAnEmptyScope(t *testing.T) {
	requireDatabase(t)

	// An id far above anything the dump holds: the scope exists as a request
	// and resolves to no book, which is exactly the case that used to widen.
	const missing = 2_000_000_000

	for _, scope := range []struct {
		name    string
		filters models.BookFilters
	}{
		{"author", models.BookFilters{Author: missing, Limit: 10}},
		{"series", models.BookFilters{Series: missing, Limit: 10}},
		{"collection", models.BookFilters{Collection: missing, Limit: 10}},
		{"curated collection", models.BookFilters{CuratedCollection: missing, Limit: 10}},
		{"genre", models.BookFilters{Genre: missing, Limit: 10}},
	} {
		t.Run(scope.name, func(t *testing.T) {
			books, count, err := GetBooks(1, scope.filters)
			require.NoError(t, err)
			assert.Empty(t, books, "a scope with no books must not widen to the catalog")
			assert.Zero(t, count)
		})
	}
}

// TestGetBooksClampsTheWindow keeps the ordinary list from being asked for an
// unbounded page, which is what made the old path expensive.
func TestGetBooksClampsTheWindow(t *testing.T) {
	requireDatabase(t)

	for _, limit := range []int{0, 500} {
		books, _, err := GetBooks(1, models.BookFilters{Limit: limit})
		require.NoError(t, err)
		assert.LessOrEqual(t, len(books), 100, "limit %d must clamp to 100", limit)
	}
}

// TestUpdateBook tests the UpdateBook function
func TestUpdateBook(t *testing.T) {
	if db == nil {
		t.Skip("Database connection not available")
	}

	// Test updating title only
	t.Run("Update title only", func(t *testing.T) {
		// First, get a book to update (using filters to find any book)
		filters := models.BookFilters{
			Limit:  1,
			Offset: 0,
		}
		books, _, err := GetBooks(1, filters)
		if err != nil || len(books) == 0 {
			t.Skip("No books available for testing")
		}

		originalBook := books[0]
		newTitle := "Test Updated Title"

		updateReq := models.BookUpdateRequest{
			ID:    originalBook.ID,
			Title: &newTitle,
		}

		updatedBook, err := UpdateBook(updateReq)
		assert.NoError(t, err, "UpdateBook should not return error")
		assert.Equal(t, newTitle, updatedBook.Title, "Title should be updated")
		assert.Equal(t, originalBook.Lang, updatedBook.Lang, "Lang should remain unchanged")
		assert.Equal(t, originalBook.Approved, updatedBook.Approved, "Approved should remain unchanged")

		// Restore original title
		restoreReq := models.BookUpdateRequest{
			ID:    originalBook.ID,
			Title: &originalBook.Title,
		}
		_, err = UpdateBook(restoreReq)
		assert.NoError(t, err, "Should be able to restore original title")
	})

	// Test updating multiple fields
	t.Run("Update multiple fields", func(t *testing.T) {
		filters := models.BookFilters{
			Limit:  1,
			Offset: 0,
		}
		books, _, err := GetBooks(1, filters)
		if err != nil || len(books) == 0 {
			t.Skip("No books available for testing")
		}

		originalBook := books[0]
		newTitle := "Test Title Multiple"
		newAnnotation := "Test annotation"
		approved := false

		updateReq := models.BookUpdateRequest{
			ID:         originalBook.ID,
			Title:      &newTitle,
			Annotation: &newAnnotation,
			Approved:   &approved,
		}

		updatedBook, err := UpdateBook(updateReq)
		assert.NoError(t, err, "UpdateBook should not return error")
		assert.Equal(t, newTitle, updatedBook.Title, "Title should be updated")
		assert.Equal(t, newAnnotation, updatedBook.Annotation, "Annotation should be updated")
		assert.Equal(t, approved, updatedBook.Approved, "Approved should be updated")
		assert.Equal(t, originalBook.Lang, updatedBook.Lang, "Lang should remain unchanged")

		// Restore original values
		restoreReq := models.BookUpdateRequest{
			ID:         originalBook.ID,
			Title:      &originalBook.Title,
			Annotation: &originalBook.Annotation,
			Approved:   &originalBook.Approved,
		}
		_, err = UpdateBook(restoreReq)
		assert.NoError(t, err, "Should be able to restore original values")
	})

	// Test updating non-existent book
	t.Run("Update non-existent book", func(t *testing.T) {
		newTitle := "Test Title"
		updateReq := models.BookUpdateRequest{
			ID:    999999999, // Non-existent ID
			Title: &newTitle,
		}

		_, err := UpdateBook(updateReq)
		assert.Error(t, err, "Updating non-existent book should return error")
	})

	// Test with empty update request (no fields to update)
	t.Run("Empty update request", func(t *testing.T) {
		filters := models.BookFilters{
			Limit:  1,
			Offset: 0,
		}
		books, _, err := GetBooks(1, filters)
		if err != nil || len(books) == 0 {
			t.Skip("No books available for testing")
		}

		originalBook := books[0]

		updateReq := models.BookUpdateRequest{
			ID: originalBook.ID,
			// No fields to update
		}

		updatedBook, err := UpdateBook(updateReq)
		assert.NoError(t, err, "Empty update should not return error")
		assert.Equal(t, originalBook.Title, updatedBook.Title, "Title should remain unchanged")
		assert.Equal(t, originalBook.Lang, updatedBook.Lang, "Lang should remain unchanged")
	})
}
