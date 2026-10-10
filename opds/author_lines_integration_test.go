package opds

import (
	"encoding/xml"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"gopds-api/database"
	"gopds-api/internal/scanfixture"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The same scanned books with the author line read from the author layer:
// the feeds pinned byte for byte, and every feed that lists books showing the
// same line for the same book.

// lineAuthor is one <author> of an entry and the related link its name has.
type lineAuthor struct {
	Name    string
	URI     string
	Related string
}

type lineEntry struct {
	ID    string `xml:"id"`
	Links []struct {
		Href  string `xml:"href,attr"`
		Rel   string `xml:"rel,attr"`
		Title string `xml:"title,attr"`
	} `xml:"link"`
	Authors []struct {
		Name string `xml:"name"`
		URI  string `xml:"uri"`
	} `xml:"author"`
}

// entryAuthors reads the author line of the book's entry in a feed: each
// <author> with the related link that names it, if any.
func entryAuthors(t *testing.T, feed string, bookID int64) []lineAuthor {
	t.Helper()
	var parsed struct {
		Entries []lineEntry `xml:"entry"`
	}
	require.NoError(t, xml.Unmarshal([]byte(feed), &parsed))
	for _, e := range parsed.Entries {
		if e.ID != strconv.FormatInt(bookID, 10) {
			continue
		}
		related := map[string]string{}
		for _, l := range e.Links {
			if l.Rel == "related" {
				related[strings.TrimPrefix(l.Title, "Все книги: ")] = l.Href
			}
		}
		out := make([]lineAuthor, len(e.Authors))
		for i, a := range e.Authors {
			out[i] = lineAuthor{Name: a.Name, URI: a.URI, Related: related[a.Name]}
		}
		assert.Len(t, related, len(e.Links)-countNonRelated(e), "every related link names an author of the line")
		return out
	}
	t.Fatalf("book %d is not in the feed", bookID)
	return nil
}

func countNonRelated(e lineEntry) int {
	n := 0
	for _, l := range e.Links {
		if l.Rel != "related" {
			n++
		}
	}
	return n
}

func TestScanCharacterizationAtomFromTheAuthorLayer(t *testing.T) {
	ch := scanCharacterization(t)
	r := ch.router(&services.AuthorLines{Lookup: database.NewPGBookSourceRepository(ch.db)})

	for _, c := range goldenFeeds {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, ch.golden(t, c.golden+".layer.atom.tmpl"), ch.get(t, r, c.path(ch)))
		})
	}

	// The book whose catalog names the legacy scan glued by index: the file's
	// four authors in file order, the one catalog author it names linked to
	// its books, the three the catalog lost shown with no link at all.
	tolstoy := strconv.FormatInt(ch.authors["Толстой лев"], 10)
	multi := ch.books[scanfixture.EntryMultiAuthor]
	want := []lineAuthor{
		{Name: "лев Николаевич Толстой", URI: "/opds/author/" + tolstoy, Related: "/opds/new/0/" + tolstoy},
		{Name: "пушкин"},
		{Name: "Анна"},
		{Name: "Аноним"},
	}
	newest := ch.get(t, r, "/opds/new/0/0")
	assert.Equal(t, want, entryAuthors(t, newest, multi))

	// Every other feed that lists the book shows the same line.
	collection := ch.collectionOf(t, multi)
	for _, path := range []string{
		"/opds/lang/ru/books/0",
		"/opds/lang/ru/author/" + tolstoy + "/0",
		"/opds/collection/" + strconv.FormatInt(collection, 10) + "/0",
		"/opds/books?title=" + url.QueryEscape("война"),
		"/opds/lang/ru/search-books?title=" + url.QueryEscape("война"),
	} {
		t.Run(path, func(t *testing.T) {
			assert.Equal(t, want, entryAuthors(t, ch.get(t, r, path), multi))
		})
	}
}

// collectionOf makes a public curated collection holding the book.
func (ch *characterization) collectionOf(t *testing.T, bookID int64) int64 {
	t.Helper()
	var id int64
	_, err := ch.db.QueryOne(pg.Scan(&id), `INSERT INTO book_collections (name, is_curated, is_public)
		VALUES ('Подборка', true, true) RETURNING id`)
	require.NoError(t, err)
	_, err = ch.db.Exec(`INSERT INTO book_collection_items
		(collection_id, book_id, external_title, match_status, position) VALUES (?, ?, 'x', 'manual', 1)`, id, bookID)
	require.NoError(t, err)
	return id
}
