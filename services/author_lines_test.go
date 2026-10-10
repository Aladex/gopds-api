package services

import (
	"context"
	"errors"
	"testing"

	"gopds-api/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The author lines of a page of books, for the surfaces that list books
// without the web card's JSON: OPDS feeds and the bot. One lookup per page;
// without one, or when it fails, every book shows its legacy authors.

type recordingAuthorLookup struct {
	calls [][]int64
	lines map[int64]models.BookAuthorDisplay
	err   error
}

func (l *recordingAuthorLookup) BookAuthorDisplay(_ context.Context, ids []int64) (map[int64]models.BookAuthorDisplay, error) {
	l.calls = append(l.calls, append([]int64(nil), ids...))
	return l.lines, l.err
}

func ptr(id int64) *int64 { return &id }

var pageOfTwo = []models.Book{
	{ID: 1, Authors: []models.Author{{ID: 10, FullName: "Петров Иван"}, {ID: 11, FullName: "Сидорова Анна"}}},
	{ID: 2, Authors: []models.Author{{ID: 12, FullName: "Гоголь Николай"}}},
}

var legacyOfPageOfTwo = [][]models.AuthorDisplay{
	{{Name: "Петров Иван", LegacyAuthorID: ptr(10)}, {Name: "Сидорова Анна", LegacyAuthorID: ptr(11)}},
	{{Name: "Гоголь Николай", LegacyAuthorID: ptr(12)}},
}

func TestAuthorLinesAreTheLegacyAuthorsWhenSwitchedOff(t *testing.T) {
	var off *AuthorLines
	assert.Equal(t, legacyOfPageOfTwo, off.Page(context.Background(), pageOfTwo))
	assert.Equal(t, legacyOfPageOfTwo, (&AuthorLines{}).Page(context.Background(), pageOfTwo))
}

func TestAuthorLinesAskOncePerPageAndKeepBookOrder(t *testing.T) {
	lookup := &recordingAuthorLookup{lines: map[int64]models.BookAuthorDisplay{
		1: {Source: models.AuthorDisplayLayer, Authors: []models.AuthorDisplay{
			{Name: "Анна Сидорова", LegacyAuthorID: ptr(11)}, {Name: "Мастер"},
		}},
		2: {Source: models.AuthorDisplayLegacy, Fallback: models.AuthorDisplayNoCredits,
			Authors: []models.AuthorDisplay{{Name: "Гоголь Николай", LegacyAuthorID: ptr(12)}}},
	}}
	got := (&AuthorLines{Lookup: lookup}).Page(context.Background(), pageOfTwo)

	require.Len(t, lookup.calls, 1)
	assert.Equal(t, []int64{1, 2}, lookup.calls[0])
	assert.Equal(t, [][]models.AuthorDisplay{
		{{Name: "Анна Сидорова", LegacyAuthorID: ptr(11)}, {Name: "Мастер"}},
		{{Name: "Гоголь Николай", LegacyAuthorID: ptr(12)}},
	}, got)
}

// A book the lookup did not answer for keeps its legacy authors.
func TestAuthorLinesFallBackPerBookTheLookupMissed(t *testing.T) {
	lookup := &recordingAuthorLookup{lines: map[int64]models.BookAuthorDisplay{
		2: {Source: models.AuthorDisplayLayer, Authors: []models.AuthorDisplay{{Name: "Николай Гоголь", LegacyAuthorID: ptr(12)}}},
	}}
	got := (&AuthorLines{Lookup: lookup}).Page(context.Background(), pageOfTwo)
	assert.Equal(t, legacyOfPageOfTwo[0], got[0])
	assert.Equal(t, []models.AuthorDisplay{{Name: "Николай Гоголь", LegacyAuthorID: ptr(12)}}, got[1])
}

// The line is an enrichment of a page that is already read: when the lookup
// fails, the page is served with the legacy authors rather than not at all.
func TestAuthorLinesFallBackToLegacyWhenTheLookupFails(t *testing.T) {
	// Whatever a failed lookup handed back alongside its error is not used.
	lookup := &recordingAuthorLookup{err: errors.New("connection refused"), lines: map[int64]models.BookAuthorDisplay{
		1: {Source: models.AuthorDisplayLayer, Authors: []models.AuthorDisplay{{Name: "Половина"}}},
	}}
	assert.Equal(t, legacyOfPageOfTwo, (&AuthorLines{Lookup: lookup}).Page(context.Background(), pageOfTwo))
}

func TestAuthorLinesOfAnEmptyPageAskNothing(t *testing.T) {
	lookup := &recordingAuthorLookup{}
	assert.Empty(t, (&AuthorLines{Lookup: lookup}).Page(context.Background(), nil))
	assert.Empty(t, lookup.calls)
}
