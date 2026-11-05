package services_test

import (
	"sort"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/scanfixture"
	"gopds-api/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A database built by the migration files alone takes a scan of an FB2 with
// genres, and a list that loads the Genres relation reads them back. Nothing
// here adds a column: the migrations are the whole schema, as on a new install.
func TestFreshInstallScansGenresAndListsThem(t *testing.T) {
	scanfixture.ScratchDB(t)
	ids := scanfixture.Ingest(t, t.TempDir(), time.Now(), newCharacterizationScanner(t).ProcessBook)

	books, _, err := database.GetBooks(0, models.BookFilters{Limit: 100})
	require.NoError(t, err)

	var found *models.Book
	for i := range books {
		if books[i].ID == ids[scanfixture.EntryMultiAuthor] {
			found = &books[i]
		}
	}
	require.NotNil(t, found, "the scanned book is not in the list")
	var tags []string
	for _, genre := range found.Genres {
		tags = append(tags, genre.Genre)
		assert.Equal(t, genre.Genre, genre.Title, "a scan without an LLM titles a genre with its tag")
	}
	sort.Strings(tags)
	assert.Equal(t, []string{"prose_classic", "prose_history"}, tags)
}
