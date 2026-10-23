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
//
// Entities switches the lines to the entity identity (authors.identity): a
// name links to the entities it names and never to a legacy author.
type AuthorLines struct {
	Lookup   AuthorDisplayLookup
	Entities AuthorEntityLinks
}

// AuthorEntityLinks turns a page of author lines into the entity identity's
// (database.EntityLines).
type AuthorEntityLinks interface {
	EntityLines(ctx context.Context, lines [][]models.AuthorDisplay) ([][]models.AuthorDisplay, error)
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
	if a == nil || a.Entities == nil {
		return out
	}
	return EntityLinesOf(ctx, a.Entities, out)
}

// EntityLinesOf is a page of lines under the entity identity. When the
// entities cannot be read the names are shown without links: a legacy link
// would lead to an author the entity identity does not know.
func EntityLinesOf(ctx context.Context, links AuthorEntityLinks, lines [][]models.AuthorDisplay) [][]models.AuthorDisplay {
	linked, err := links.EntityLines(ctx, lines)
	if err == nil {
		return linked
	}
	logging.Errorf("Reading the author entities of %d author lines, showing them unlinked: %v", len(lines), err)
	plain := make([][]models.AuthorDisplay, len(lines))
	for i, line := range lines {
		plain[i] = models.WithEntities(line, nil)
	}
	return plain
}
