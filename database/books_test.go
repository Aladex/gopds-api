package database

import (
	"gopds-api/models"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestEnhancedSearchComparison тестирует улучшенный поиск книг и сравнивает с autocomplete
func TestEnhancedSearchComparison(t *testing.T) {
	// Пропускаем тест если нет подключения к БД
	if db == nil {
		t.Skip("Database connection not available")
	}

	testCases := []struct {
		name  string
		query string
		limit int
	}{
		{
			name:  "Поиск Гарри Поттер",
			query: "гарри поттер",
			limit: 5,
		},
		{
			name:  "Поиск Война и мир",
			query: "война мир",
			limit: 5,
		},
		{
			name:  "Поиск Преступление и наказание",
			query: "преступление наказание",
			limit: 5,
		},
		{
			name:  "Поиск Мастер и Маргарита",
			query: "мастер маргарита",
			limit: 5,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Тест autocomplete suggestions
			suggestions, err := GetAutocompleteSuggestions(tc.query, "all", "", "")
			assert.NoError(t, err, "GetAutocompleteSuggestions should not return error")

			// Тест улучшенного поиска книг
			filters := models.BookFilters{
				Title:  tc.query,
				Limit:  tc.limit,
				Offset: 0,
			}

			books, count, err := GetBooks(1, filters) // user_id = 1
			assert.NoError(t, err, "GetBooks should not return error")
			assert.GreaterOrEqual(t, count, 0, "Count should be non-negative")
			assert.LessOrEqual(t, len(books), tc.limit, "Returned books should not exceed limit")

			// Проверяем, что если есть результаты, то они релевантны
			if len(books) > 0 {
				firstBook := books[0]
				assert.NotEmpty(t, firstBook.Title, "First book should have a title")

				// Логирование для анализа результатов
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

// TestBookScoringFunction тестирует функцию скоринга книг
func TestBookScoringFunction(t *testing.T) {
	testCases := []struct {
		title    string
		query    string
		expected int // минимальный ожидаемый скор
	}{
		{
			title:    "Гарри Поттер",
			query:    "гарри поттер",
			expected: 1000, // точное совпадение
		},
		{
			title:    "Гарри Поттер и философский камень",
			query:    "гарри поттер",
			expected: 500, // начинается с запроса
		},
		{
			title:    "Приключения Гарри Поттера",
			query:    "гарри поттер",
			expected: 100, // содержит запрос
		},
		{
			title:    "Война и мир",
			query:    "война мир",
			expected: 200, // частичное совпадение
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

// TestSearchWithFilters тестирует поиск с дополнительными фильтрами
func TestSearchWithFilters(t *testing.T) {
	if db == nil {
		t.Skip("Database connection not available")
	}

	// Тест поиска с языковым фильтром
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

		// Проверяем, что все возвращенные книги на правильном языке
		for _, book := range books {
			if book.Lang != "" {
				assert.Equal(t, "ru", book.Lang, "Book language should match filter")
			}
		}

		t.Logf("Books found with language filter: %d", count)
	})

	// Тест поиска без фильтра названия (обычная логика)
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

// BenchmarkEnhancedSearch бенчмарк для проверки производительности улучшенного поиска
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

// BenchmarkAutocomplete бенчмарк для autocomplete
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
