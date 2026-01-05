package database

import (
	"gopds-api/models"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestEnhancedSearchComparison tests enhanced book search and compares with autocomplete
func TestEnhancedSearchComparison(t *testing.T) {
	// Skip test if no database connection available
	if db == nil {
		t.Skip("Database connection not available")
	}

	testCases := []struct {
		name  string
		query string
		limit int
	}{
		{
			name:  "Search Harry Potter",
			query: "гарри поттер",
			limit: 5,
		},
		{
			name:  "Search War and Peace",
			query: "война мир",
			limit: 5,
		},
		{
			name:  "Search Crime and Punishment",
			query: "преступление наказание",
			limit: 5,
		},
		{
			name:  "Search Master and Margarita",
			query: "мастер маргарита",
			limit: 5,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Test autocomplete suggestions
			suggestions, err := GetAutocompleteSuggestions(tc.query, "all", "", "")
			assert.NoError(t, err, "GetAutocompleteSuggestions should not return error")

			// Test enhanced book search
			filters := models.BookFilters{
				Title:  tc.query,
				Limit:  tc.limit,
				Offset: 0,
			}

			books, count, err := GetBooks(1, filters) // user_id = 1
			assert.NoError(t, err, "GetBooks should not return error")
			assert.GreaterOrEqual(t, count, 0, "Count should be non-negative")
			assert.LessOrEqual(t, len(books), tc.limit, "Returned books should not exceed limit")

			// Check that if there are results, they are relevant
			if len(books) > 0 {
				firstBook := books[0]
				assert.NotEmpty(t, firstBook.Title, "First book should have a title")

				// Logging for result analysis
				t.Logf("Query: '%s'", tc.query)
				t.Logf("Autocomplete suggestions count: %d", len(suggestions))
				t.Logf("Books found: %d", count)
				t.Logf("Books returned: %d", len(books))

				if len(suggestions) > 0 {
					t.Logf("First suggestion: [%s] %s", suggestions[0].Type, suggestions[0].Value)
				}

				if len(books) > 0 {
					authors := ""
					if len(books[0].Authors) > 0 {
						authors = " - " + books[0].Authors[0].FullName
					}
					t.Logf("First book: %s%s", books[0].Title, authors)
				}
			}
		})
	}
}

// TestBookScoringFunction tests book scoring function
func TestBookScoringFunction(t *testing.T) {
	testCases := []struct {
		title    string
		query    string
		expected int // minimum expected score
	}{
		{
			title:    "Гарри Поттер",
			query:    "гарри поттер",
			expected: 1000, // exact match
		},
		{
			title:    "Гарри Поттер и философский камень",
			query:    "гарри поттер",
			expected: 500, // starts with query
		},
		{
			title:    "Приключения Гарри Поттера",
			query:    "гарри поттер",
			expected: 100, // contains query
		},
		{
			title:    "Война и мир",
			query:    "война мир",
			expected: 200, // partial match
		},
	}

	for _, tc := range testCases {
		t.Run(tc.title, func(t *testing.T) {
			score := calculateBookScore(tc.title, tc.query)
			assert.GreaterOrEqual(t, score, tc.expected,
				"Score for '%s' with query '%s' should be at least %d, got %d",
				tc.title, tc.query, tc.expected, score)
			t.Logf("Title: '%s', Query: '%s', Score: %d", tc.title, tc.query, score)
		})
	}
}

// TestSearchWithFilters tests search with additional filters
func TestSearchWithFilters(t *testing.T) {
	if db == nil {
		t.Skip("Database connection not available")
	}

	// Test search with language filter
	t.Run("Search with language filter", func(t *testing.T) {
		filters := models.BookFilters{
			Title:  "война",
			Lang:   "ru",
			Limit:  10,
			Offset: 0,
		}

		books, count, err := GetBooks(1, filters)
		assert.NoError(t, err, "Search with language filter should not return error")
		assert.GreaterOrEqual(t, count, 0, "Count should be non-negative")

		// Check that all returned books are in the correct language
		for _, book := range books {
			if book.Lang != "" {
				assert.Equal(t, "ru", book.Lang, "Book language should match filter")
			}
		}

		t.Logf("Books found with language filter: %d", count)
	})

	// Test search without title filter (regular logic)
	t.Run("Search without title filter", func(t *testing.T) {
		filters := models.BookFilters{
			Limit:  5,
			Offset: 0,
		}

		books, count, err := GetBooks(1, filters)
		assert.NoError(t, err, "Search without title should not return error")
		assert.GreaterOrEqual(t, count, 0, "Count should be non-negative")
		assert.LessOrEqual(t, len(books), 5, "Returned books should not exceed limit")

		t.Logf("Books found without title filter: %d", count)
	})
}

// BenchmarkEnhancedSearch benchmark for enhanced search performance testing
func BenchmarkEnhancedSearch(b *testing.B) {
	if db == nil {
		b.Skip("Database connection not available")
	}

	filters := models.BookFilters{
		Title:  "гарри поттер",
		Limit:  10,
		Offset: 0,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := GetBooks(1, filters)
		if err != nil {
			b.Fatalf("GetBooks failed: %v", err)
		}
	}
}

// BenchmarkAutocomplete benchmark for autocomplete
func BenchmarkAutocomplete(b *testing.B) {
	if db == nil {
		b.Skip("Database connection not available")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := GetAutocompleteSuggestions("гарри поттер", "all", "", "")
		if err != nil {
			b.Fatalf("GetAutocompleteSuggestions failed: %v", err)
		}
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
