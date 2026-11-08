package database

import (
	"context"
	"testing"

	"gopds-api/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The site's search lists one hit per card: the card sits at its best file's
// place and shows its best-ranked admitted file. Without the flag the search
// is exactly the per-file one OPDS and the bot use. The titles are made up
// so the catalog the tests run beside matches nothing.

func searchIDs(page models.BookSearchPage) []int64 {
	return cardIDs(page.Books)
}

func TestSearchBooksGroupCards(t *testing.T) {
	f := variantSchema(t)
	entity := f.entity("Квармышев")
	title := "Кварглышная трилогия"
	books := []int64{
		f.variantBook(title, []string{entity}, withDoc("kv-a", "1.0"), addedAgo(3)),
		f.variantBook(title, []string{entity}, withDoc("kv-b", "1.0"), addedAgo(2)),
		f.variantBook(title, []string{entity}, withDoc("kv-c", "1.0"), addedAgo(1)),
	}
	other := f.variantBook("Кварглышная трилогия: приложения", []string{entity})
	f.rebuild(books[0], other)
	// A fresh upload the read model has no row for yet is a card of its own.
	fresh := f.variantBook("Кварглышная трилогия, комментарии", []string{f.entity("Другой")})
	rank1 := books[0]
	for _, id := range books {
		if f.member(id).Rank == 1 {
			rank1 = id
		}
	}
	repo := NewPGSearchRepository(f.tx)
	req := models.BookSearchRequest{Query: "кварглышная трилогия", Limit: 10}

	page, err := repo.SearchBooks(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, 5, page.Total)
	assert.Len(t, page.Books, 5)
	assert.Nil(t, page.Variants, "no grouping, no variants")

	req.GroupCards = true
	page, err = repo.SearchBooks(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, 3, page.Total, "totals count cards")
	assert.ElementsMatch(t, []int64{rank1, other, fresh}, searchIDs(page))
	assert.Nil(t, page.Variants[fresh])
	require.NotNil(t, page.Variants[rank1])
	assert.Equal(t, 3, page.Variants[rank1].Count)
	assert.Equal(t, models.BookVariantsVariants, page.Variants[rank1].Kind)
	assert.Nil(t, page.Variants[other], "a single book carries nothing")
}

func TestSearchBooksGroupShowsBestAdmittedWhenDefaultMisses(t *testing.T) {
	f := variantSchema(t)
	entity := f.entity("Квармышев")
	// The tag is stripped from the card's key, so the two files are one card;
	// the default (with a cover) does not carry the searched word, the tagged
	// file does.
	plain := f.variantBook("Кварглыш", []string{entity}, withDoc("kv-plain", "1.0"), addedAgo(2))
	f.exec(`UPDATE opds_catalog_book SET cover = true WHERE id = ?`, plain)
	tagged := f.variantBook("Кварглыш [Зюзябрамсонвельт]", []string{entity}, withDoc("kv-tag", "1.0"), addedAgo(1))
	f.rebuild(plain)
	require.Equal(t, f.member(plain).GroupKey, f.member(tagged).GroupKey)
	require.Equal(t, 1, f.member(plain).Rank)

	page, err := NewPGSearchRepository(f.tx).SearchBooks(context.Background(),
		models.BookSearchRequest{Query: "зюзябрамсонвельт", Limit: 10, GroupCards: true})
	require.NoError(t, err)
	assert.Equal(t, []int64{tagged}, searchIDs(page), "the matching file is shown, not the default")
	require.NotNil(t, page.Variants[tagged])
	assert.Equal(t, 2, page.Variants[tagged].Count)
}

// A pinned book with the flag is its card: the book itself, with its count.
func TestSearchBooksGroupCardsPinnedBook(t *testing.T) {
	f := variantSchema(t)
	entity := f.entity("Квармышев")
	files := []int64{
		f.variantBook("Кварглыш", []string{entity}, withDoc("kv-1", "1.0"), addedAgo(2)),
		f.variantBook("Кварглыш", []string{entity}, withDoc("kv-2", "1.0"), addedAgo(1)),
	}
	f.rebuild(files[0])

	page, err := NewPGSearchRepository(f.tx).SearchBooks(context.Background(),
		models.BookSearchRequest{ExactBookID: files[1], Limit: 10, GroupCards: true})
	require.NoError(t, err)
	assert.Equal(t, []int64{files[1]}, searchIDs(page))
	require.NotNil(t, page.Variants[files[1]])
	assert.Equal(t, 2, page.Variants[files[1]].Count)
}

// A grouped hit hidden after the page was ranked is left out when the page is
// read, as on the lists; the per-file search keeps its old behavior for the
// moderator's widened requests.
func TestSearchBooksGroupCardsReadsVisibleOnly(t *testing.T) {
	f := variantSchema(t)
	entity := f.entity("Квармышев")
	a := f.variantBook("Кварглыш невидимый", []string{entity})
	b := f.variantBook("Кварглыш видимый", []string{entity})
	f.rebuild(a, b)
	repo := NewPGSearchRepository(f.tx)

	afterBooksRanked = func() {
		afterBooksRanked = nil
		f.exec(`UPDATE opds_catalog_book SET duplicate_hidden = true WHERE id = ?`, a)
	}
	t.Cleanup(func() { afterBooksRanked = nil })
	page, err := repo.SearchBooks(context.Background(),
		models.BookSearchRequest{Query: "кварглыш", Limit: 10, GroupCards: true})
	require.NoError(t, err)
	assert.Equal(t, []int64{b}, searchIDs(page))
}
