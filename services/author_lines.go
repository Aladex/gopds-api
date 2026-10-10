package services

import (
	"context"

	"gopds-api/logging"
	"gopds-api/models"
)

// AuthorDisplayLookup reads the author line of a page of books in one call.
type AuthorDisplayLookup interface {
	BookAuthorDisplay(ctx context.Context, bookIDs []int64) (map[int64]models.BookAuthorDisplay, error)
}

// AuthorLines gives the books of a page their author lines for the surfaces
// that list books outside the web card: OPDS feeds and the bot. A nil
// AuthorLines, or one without a lookup, is the read path switched off: every
// book shows its legacy authors.
type AuthorLines struct {
	Lookup AuthorDisplayLookup
}

// Page returns the author line of each book, in the order of books, asking
// the lookup once for the whole page. A book the lookup has no line for keeps
// its legacy authors, and so does every book when the lookup fails: the page
// is already read, and its old line is better than no page.
func (a *AuthorLines) Page(ctx context.Context, books []models.Book) [][]models.AuthorDisplay {
	var lines map[int64]models.BookAuthorDisplay
	if a != nil && a.Lookup != nil && len(books) > 0 {
		ids := make([]int64, len(books))
		for i := range books {
			ids[i] = books[i].ID
		}
		var err error
		if lines, err = a.Lookup.BookAuthorDisplay(ctx, ids); err != nil {
			logging.Errorf("Reading the author lines of %d books, showing their catalog authors: %v", len(ids), err)
			lines = nil
		}
	}
	out := make([][]models.AuthorDisplay, len(books))
	for i := range books {
		if line, ok := lines[books[i].ID]; ok {
			out[i] = line.Authors
		} else {
			out[i] = books[i].LegacyAuthorLine()
		}
	}
	return out
}
