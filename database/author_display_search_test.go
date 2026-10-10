package database

import (
	"context"
	"testing"

	"gopds-api/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Search reads the author line's names from the read model as well as the
// legacy names: a book is found by its author's patronymic, which only the
// layer has, and by a name the catalog never linked. Authors stay legacy
// entities — an author search or the picker's author lane finds a legacy
// author through the names linked to it, and a name linked to no one is
// found by book search only.

// displayName seeds a read-model row for a fixture book: a linked name when
// legacy is non-zero, an unlinked name from the layer otherwise.
func (f *searchFixture) displayName(book int64, name string, legacy int64) {
	f.t.Helper()
	source, credit := "layer", f.id()
	var legacyID interface{}
	if legacy != 0 {
		legacyID = legacy
	}
	// A linked name here always says more than its catalog name: that is
	// what the model is searched for.
	f.exec(`INSERT INTO book_author_display
		(book_id, position, display, sort_key, search_key, source, legacy_author_id, credit_id, extends_legacy)
		VALUES (?, 0, ?, lower(?), public.search_normalize(?), ?, ?, ?, ?)`,
		book, name, name, name, source, legacyID, credit, legacy != 0)
	if legacy != 0 {
		f.exec(`INSERT INTO book_author_display_name (legacy_author_id, search_key, books)
			VALUES (?, public.search_normalize(?), 1)
			ON CONFLICT (legacy_author_id, search_key) DO UPDATE SET books = book_author_display_name.books + 1`,
			legacy, name)
	}
}

func TestBookSearchByAuthorReadsTheLineNamesToo(t *testing.T) {
	withSearchFixture(t, func(f *searchFixture) {
		f.displayName(f.BookIDs["exact"], "Лев Зурабкарлович Толстой", f.AuthorIDs["tolstoy"])
		f.displayName(f.BookIDs["substring"], "Мастер Йодаквист", 0)
		repo := NewPGSearchRepository(f.tx)
		search := func(authorQuery string) []int64 {
			t.Helper()
			page, err := repo.SearchBooks(context.Background(), models.BookSearchRequest{
				Query: "Война и мир", AuthorQuery: authorQuery, UserID: f.UserIDs["reader"], Language: "fx", Limit: 50,
			})
			require.NoError(t, err)
			ids := make([]int64, len(page.Books))
			for i := range page.Books {
				ids[i] = page.Books[i].ID
			}
			assert.Equal(t, len(ids), page.Total, authorQuery)
			return ids
		}

		// Only the layer knows the patronymic.
		assert.Equal(t, []int64{f.BookIDs["exact"]}, search("Зурабкарлович"))
		assert.Equal(t, []int64{f.BookIDs["exact"]}, search("лев зурабкарлович толстой"))
		// A name the catalog never linked.
		assert.Equal(t, []int64{f.BookIDs["substring"]}, search("мастер йодаквист"))
		// The legacy names still find what they found.
		assert.Contains(t, search("толстой"), f.BookIDs["prefix"])
	})
}

func TestAuthorSearchFindsALegacyAuthorThroughItsLinkedNames(t *testing.T) {
	withSearchFixture(t, func(f *searchFixture) {
		tolstoy := f.AuthorIDs["tolstoy"]
		f.displayName(f.BookIDs["exact"], "Лев Зурабкарлович Толстой", tolstoy)
		f.displayName(f.BookIDs["prefix"], "Лев Зурабкарлович Толстой", tolstoy)
		f.displayName(f.BookIDs["substring"], "Мастер Йодаквист", 0)
		repo := NewPGSearchRepository(f.tx)

		legacy, err := repo.SearchAuthors(context.Background(), models.AuthorSearchRequest{Query: "толстой", Limit: 10})
		require.NoError(t, err)
		page, err := repo.SearchAuthors(context.Background(), models.AuthorSearchRequest{Query: "зурабкарлович", Limit: 10})
		require.NoError(t, err)
		// The catalog may hold loose fuzzy neighbors of the query; the entity
		// the name belongs to ranks first, once, however many books name it.
		require.NotEmpty(t, page.Authors)
		assert.Equal(t, tolstoy, page.Authors[0].ID)
		assert.Equal(t, 1, countID(authorIDs(page.Authors), tolstoy), "one entity, however many books name it")
		assert.Equal(t, "Толстой Лев", page.Authors[0].FullName, "the entity keeps its catalog name")
		assert.Equal(t, booksCountOf(legacy.Authors, tolstoy), page.Authors[0].BooksCount,
			"the count is the books the entity's page opens")

		// Found both by its catalog name and by the name that extends it: one
		// row still, at its count.
		both, err := repo.SearchAuthors(context.Background(), models.AuthorSearchRequest{Query: "толстой", Limit: 10})
		require.NoError(t, err)
		assert.Equal(t, 1, countID(authorIDs(both.Authors), tolstoy))
		assert.Equal(t, booksCountOf(legacy.Authors, tolstoy), booksCountOf(both.Authors, tolstoy))

		unlinked, err := repo.SearchAuthors(context.Background(), models.AuthorSearchRequest{Query: "мастер йодаквист", Limit: 10})
		require.NoError(t, err)
		assert.NotContains(t, authorNames(unlinked.Authors), "Мастер Йодаквист",
			"a name linked to no one is not an author entity")
	})
}

func TestThePickersAuthorLaneFindsALegacyAuthorThroughItsLinkedNames(t *testing.T) {
	withSearchFixture(t, func(f *searchFixture) {
		tolstoy := f.AuthorIDs["tolstoy"]
		f.displayName(f.BookIDs["exact"], "Лев Зурабкарлович Толстой", tolstoy)
		f.displayName(f.BookIDs["substring"], "Мастер Йодаквист", 0)
		repo := NewPGSearchRepository(f.tx)

		result, err := repo.Suggestions(context.Background(), models.SuggestionRequest{
			Query: "зурабкарлович", Kind: models.SuggestionAuthor, Language: "fx",
		})
		require.NoError(t, err)
		require.NotEmpty(t, result.Suggestions)
		assert.Equal(t, "author", result.Suggestions[0].Type)
		assert.Equal(t, tolstoy, result.Suggestions[0].ID)
		assert.Equal(t, "Толстой Лев", result.Suggestions[0].Value)

		result, err = repo.Suggestions(context.Background(), models.SuggestionRequest{
			Query: "мастер йодаквист", Kind: models.SuggestionAuthor, Language: "fx",
		})
		require.NoError(t, err)
		for _, s := range result.Suggestions {
			assert.NotEqual(t, "Мастер Йодаквист", s.Value, "a name linked to no one is not an author entity")
		}
	})
}

func countID(ids []int64, id int64) int {
	n := 0
	for _, x := range ids {
		if x == id {
			n++
		}
	}
	return n
}

func authorNames(authors []models.Author) []string {
	names := make([]string, len(authors))
	for i := range authors {
		names[i] = authors[i].FullName
	}
	return names
}

func booksCountOf(authors []models.Author, id int64) int {
	for _, a := range authors {
		if a.ID == id {
			return a.BooksCount
		}
	}
	return -1
}
