package opds

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"gopds-api/database"
	"gopds-api/internal/scanfixture"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A book's variants as OPDS serves them: every feed that lists books reads
// the page's entry details in one call, and the card's other files are a
// related feed of one entry per file, unknown and hidden books answered like
// a path nothing serves.

// fakeEntries plays the page-entries reader: it records the pages it is
// asked for and answers a fixed detail per book.
type fakeEntries struct {
	calls   [][]int64
	details map[int64]models.BookEntryDetail
}

func (f *fakeEntries) BookEntryDetails(_ context.Context, ids []int64) (map[int64]models.BookEntryDetail, error) {
	f.calls = append(f.calls, append([]int64(nil), ids...))
	return f.details, nil
}

var _ BookEntries = (*fakeEntries)(nil)

// variantsOfStub stands for database.BookVariantsOf in the tests without a
// database; restore puts the real one back when the test ends.
func variantsOfStub(
	t *testing.T, fn func(ctx context.Context, bookID, userID int64) ([]models.Book, []models.BookVariant, error),
) {
	t.Helper()
	stored := bookVariantsOf
	bookVariantsOf = fn
	t.Cleanup(func() { bookVariantsOf = stored })
}

// detailLink is one link of an entry as these tests read it.
type detailLink struct {
	Href  string `xml:"href,attr"`
	Rel   string `xml:"rel,attr"`
	Type  string `xml:"type,attr"`
	Title string `xml:"title,attr"`
}

// detailEntry is one entry of a feed these tests read whole.
type detailEntry struct {
	ID      string       `xml:"id"`
	Title   string       `xml:"title"`
	Summary string       `xml:"summary"`
	Links   []detailLink `xml:"link"`
}

// detailFeed is a feed as these tests read it: its own title and links, and
// its entries whole.
type detailFeed struct {
	Title   string        `xml:"title"`
	ID      string        `xml:"id"`
	Links   []detailLink  `xml:"link"`
	Entries []detailEntry `xml:"entry"`
}

// parseDetailFeed reads a served feed body into a detailFeed.
func parseDetailFeed(t *testing.T, rec *httptest.ResponseRecorder) detailFeed {
	t.Helper()
	var feed detailFeed
	require.NoError(t, xml.Unmarshal(rec.Body.Bytes(), &feed), "body=%s", rec.Body.String())
	return feed
}

func TestOpdsSearchFeedReadsThePageEntryDetailsOnce(t *testing.T) {
	books := cannedBooks(3)
	search := &fakePublicSearch{booksPage: models.BookSearchPage{Books: books, Total: 3, Limit: 10}}
	entries := &fakeEntries{details: map[int64]models.BookEntryDetail{
		1000: {Translators: []string{"Нора Галь"}, VariantCount: 2},
	}}
	r := gin.New()
	SetupOpdsRoutes(r.Group("/opds"), search, nil, nil, entries)

	rec := doGET(t, r, "/opds/books?title=x")
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Equal(t, [][]int64{{1000, 1001, 1002}}, entries.calls, "one read for the whole page, no N+1")
	feed := parseDetailFeed(t, rec)
	require.Len(t, feed.Entries, 3)
	assert.Equal(t, "Book 1000 (пер. Нора Галь)", feed.Entries[0].Title)
	assert.Equal(t, "Book 1001", feed.Entries[1].Title, "a book without translators keeps its title")
}

func TestOpdsBookVariantsFeedListsTheCardFilesInCardOrder(t *testing.T) {
	books := cannedBooks(2)
	// Reversed against the id order, so the test tells the card's order from
	// the feed's habit of listing by id.
	ordered := []models.Book{books[1], books[0]}
	variantsOfStub(t, func(context.Context, int64, int64) ([]models.Book, []models.BookVariant, error) {
		return ordered, nil, nil
	})
	r := gin.New()
	SetupOpdsRoutes(r.Group("/opds"), &fakePublicSearch{}, nil, nil, nil)

	rec := doGET(t, r, "/opds/book/1000/variants")
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	feed := parseDetailFeed(t, rec)

	require.Len(t, feed.Entries, 2)
	assert.Equal(t, "1001", feed.Entries[0].ID, "the card's order, the default first")
	assert.Equal(t, "1000", feed.Entries[1].ID)
	assert.Equal(t, "Другие переводы и редакции: Book 1000", feed.Title)
	assert.Equal(t, "tag:book:1000:variants", feed.ID)

	// The entries are the acquisition entries every feed lists.
	for _, e := range feed.Entries {
		hrefs := map[string]bool{}
		for _, l := range e.Links {
			if l.Rel == "http://opds-spec.org/acquisition/open-access" {
				hrefs[l.Type] = true
			}
		}
		assert.True(t, hrefs["application/fb2+xml"], "%s keeps its fb2 link", e.ID)
		assert.True(t, hrefs["application/epub+zip"], "%s keeps its epub link", e.ID)
		assert.True(t, hrefs["application/x-mobipocket-ebook"], "%s keeps its mobi link", e.ID)
	}

	// The feed's own links lead out like the other feeds'.
	rels := map[string]string{}
	for _, l := range feed.Links {
		rels[l.Rel] = l.Href
	}
	assert.Equal(t, "/opds", rels["start"])
	assert.Equal(t, "/opds/new/0/0", rels["up"])
	assert.Contains(t, rels, "search")
}

func TestOpdsBookVariantsFeedGivesEntriesTheirDetails(t *testing.T) {
	books := cannedBooks(2)
	ordered := []models.Book{books[0], books[1]}
	variantsOfStub(t, func(context.Context, int64, int64) ([]models.Book, []models.BookVariant, error) {
		return ordered, nil, nil
	})
	entries := &fakeEntries{details: map[int64]models.BookEntryDetail{
		1000: {Translators: []string{"Нора Галь"}},
		1001: {Translators: []string{"Рита Райт-Ковалёва"}, TranslatorsMore: true, VariantCount: 2},
	}}
	r := gin.New()
	SetupOpdsRoutes(r.Group("/opds"), &fakePublicSearch{}, nil, nil, entries)

	rec := doGET(t, r, "/opds/book/1000/variants")
	require.Equal(t, http.StatusOK, rec.Code)
	feed := parseDetailFeed(t, rec)
	require.Len(t, feed.Entries, 2)
	assert.Equal(t, "Book 1000 (пер. Нора Галь)", feed.Entries[0].Title)
	assert.Equal(t, "Перевод: Нора Галь", feed.Entries[0].Summary)
	assert.Equal(t, "Book 1001 (пер. Рита Райт-Ковалёва)", feed.Entries[1].Title)
	assert.Equal(t, "Перевод: Рита Райт-Ковалёва и др.", feed.Entries[1].Summary)
}

func TestOpdsBookVariantsFeedUnknownBookIsNotFound(t *testing.T) {
	variantsOfStub(t, func(context.Context, int64, int64) ([]models.Book, []models.BookVariant, error) {
		return nil, nil, pg.ErrNoRows
	})
	r := gin.New()
	SetupOpdsRoutes(r.Group("/opds"), &fakePublicSearch{}, nil, nil, nil)

	rec := doGET(t, r, "/opds/book/999/variants")
	assert.Equal(t, http.StatusNotFound, rec.Code, "the same JSON 404 the author feeds answer")
	assert.JSONEq(t, `{"error":"not found"}`, rec.Body.String())
}

func TestOpdsBookVariantsFeedBadIDIsBadRequest(t *testing.T) {
	r := gin.New()
	SetupOpdsRoutes(r.Group("/opds"), &fakePublicSearch{}, nil, nil, nil)

	rec := doGET(t, r, "/opds/book/abc/variants")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// The variants feed over a real scanned card: every visible file of the card
// is an entry, each linking the feed, and a hidden file drops out of both the
// entries and the counts.
func TestOPDSVariantsFeedServesTheWholeCard(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	card := scanfixture.NewVariantCard(t, db)
	// One file of the card credits a translator, so the feed's entries differ
	// the way the files do.
	var snapshot int64
	_, err := db.QueryOne(pg.Scan(&snapshot),
		`SELECT id FROM book_metadata_snapshot WHERE book_id = ? AND is_current`, card.Books[0])
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO book_contributor_credit
		(snapshot_id, role, position, source_display_name, source_fingerprint)
		VALUES (?, 'translator', 0, 'Джан Брунеро', sha256(convert_to('Джан Брунеро', 'UTF8')))`, snapshot)
	require.NoError(t, err)

	repo := database.NewPGBookSourceRepository(db)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user_id", int64(0)); c.Next() })
	SetupOpdsRoutes(r.Group("/opds"),
		services.NewSearchService(database.NewPGSearchRepository(db)),
		&services.AuthorLines{Lookup: repo}, nil, database.NewPGBookEntries(db))

	feedOf := func(book int64) detailFeed {
		t.Helper()
		rec := doGET(t, r, "/opds/book/"+strconv.FormatInt(book, 10)+"/variants")
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		if path := os.Getenv("OPDS_DUMP_FEED"); path != "" {
			require.NoError(t, os.WriteFile(path, rec.Body.Bytes(), 0o600))
		}
		return parseDetailFeed(t, rec)
	}

	feed := feedOf(card.Books[0])
	// The card's order, read straight from the read model.
	var want []string
	_, err = db.Query(&want, `SELECT m.book_id::text FROM book_variant_member AS m
		WHERE m.group_key = (SELECT group_key FROM book_variant_member WHERE book_id = ?)
		ORDER BY m.variant_rank, m.book_id`, card.Books[0])
	require.NoError(t, err)
	byID := map[string]*detailEntry{}
	for i := range feed.Entries {
		byID[feed.Entries[i].ID] = &feed.Entries[i]
	}
	require.Len(t, byID, 3)
	got := make([]string, len(feed.Entries))
	for i := range feed.Entries {
		got[i] = feed.Entries[i].ID
	}
	assert.Equal(t, want, got, "the entries sit in the card's order")

	// Every file's entry links the card's feed with its visible count.
	for _, id := range want {
		e := byID[id]
		found := false
		for _, l := range e.Links {
			if l.Rel == "related" && l.Href == "/opds/book/"+id+"/variants" {
				assert.Equal(t, "Другие переводы и редакции (3)", l.Title)
				found = true
			}
		}
		require.True(t, found, "entry %s links its card's feed", id)
	}

	// The translated file differs the way the files do.
	translatedID := strconv.FormatInt(card.Books[0], 10)
	assert.Equal(t, "Солярис (пер. Джан Брунеро)", byID[translatedID].Title)
	assert.Equal(t, "Перевод: Джан Брунеро", byID[translatedID].Summary)
	for _, id := range want {
		if id == translatedID {
			continue
		}
		assert.Equal(t, "Солярис", byID[id].Title, "%s credits no translator", id)
	}

	// A hidden file drops out of the entries and the counts alike.
	_, err = db.Exec(`UPDATE opds_catalog_book SET duplicate_hidden = true WHERE id = ?`, card.Books[2])
	require.NoError(t, err)
	feed = feedOf(card.Books[1])
	require.Len(t, feed.Entries, 2)
	for _, e := range feed.Entries {
		for _, l := range e.Links {
			if l.Rel == "related" && l.Href == "/opds/book/"+e.ID+"/variants" {
				assert.Equal(t, "Другие переводы и редакции (2)", l.Title, "the count drops with the file")
			}
		}
	}

	// The hidden book's own feed is a path nothing serves.
	rec := doGET(t, r, "/opds/book/"+strconv.FormatInt(card.Books[2], 10)+"/variants")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
