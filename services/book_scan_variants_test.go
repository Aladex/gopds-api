package services

import (
	"testing"
	"time"

	"gopds-api/internal/scanfixture"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A scanned book's card is queued with the book, in the scan's transaction:
// until the variants read model has a row for it, the feed finds a new book
// only through that mark, whatever ids committed before or after it.
func TestScanMarksTheBooksCard(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	scanner := NewBookScanService(t.TempDir(), t.TempDir(), NewLanguageDetector(false, 5*time.Second), false, nil)
	books := scanfixture.Ingest(t, t.TempDir(), time.Now(), scanner.ProcessBook)
	require.NotEmpty(t, books)

	var marked []int64
	_, err := db.Query(&marked, `SELECT book_id FROM book_variant_dirty ORDER BY book_id`)
	require.NoError(t, err)
	for name, id := range books {
		assert.Contains(t, marked, id, "%s is queued for its card", name)
	}
}
