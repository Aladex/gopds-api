package opds

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gopds-api/httputil"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/opdsutils"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

// SearchHandler is the OPDS adapter of the search service: the global and
// language-scoped book/author searches. It binds query strings, derives
// offsets from pages and renders Atom feeds; ranking, totals and the
// visibility rule live below this boundary. OPDS sits behind BasicAuth, which
// has no superuser notion, so no OPDS request ever declares a moderator.
type SearchHandler struct {
	Search services.PublicSearch
}

// opdsPageSize is the feed page every OPDS list in this package serves.
const opdsPageSize = 10

// atomContentType is the content type every OPDS feed in this package serves.
const atomContentType = "application/atom+xml;charset=utf-8"

// clampPage defends the offset arithmetic: a missing or negative page is the
// first page, as it always was.
func clampPage(page int) int {
	if page < 0 {
		return 0
	}
	return page
}

// hasNextSearchPage reports whether the page after this one can hold anything.
// The old floor-division helper advertised a next page on exact multiples;
// this comparison does not, because offset+items == total means the reader is
// already looking at the last row.
func hasNextSearchPage(offset, items, total int) bool {
	return offset+items < total
}

// nextSearchHref builds the rel="next" target through url.Values, so Unicode
// and spaces are escaped exactly once and the current query survives.
func nextSearchHref(path, param, needle string, page int) string {
	values := url.Values{}
	values.Set(param, needle)
	values.Set("page", strconv.Itoa(page+1))
	return path + "?" + values.Encode()
}

// nextSearchLink prepends or appends the rel="next" link when the boundary
// says a next page exists.
func nextSearchLink(links []opdsutils.Link, href string, prepend bool) []opdsutils.Link {
	link := opdsutils.Link{
		Href: href,
		Rel:  "next",
		Type: "application/atom+xml;profile=opds-catalog",
	}
	if prepend {
		return append([]opdsutils.Link{link}, links...)
	}
	return append(links, link)
}

// globalSearchLinks are the links every global search feed carries, in the
// order they always had.
func globalSearchLinks() []opdsutils.Link {
	return []opdsutils.Link{
		{
			Href: "/opds",
			Rel:  "start",
			Type: "application/atom+xml;profile=opds-catalog",
		},
		{
			Href: "/opds-opensearch.xml",
			Rel:  "search",
			Type: "application/opensearchdescription+xml",
		},
		{
			Href: "/opds/search?searchTerms={searchTerms}",
			Rel:  "search",
			Type: "application/atom+xml",
		},
	}
}

// mapOpdsSearchError translates the service boundary into HTTP: a validation
// rejection is the caller's bug (400), everything else is ours (500). Neither
// is ever converted into the not-found feed — that feed belongs to empty
// results only.
func mapOpdsSearchError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, services.ErrEmptyQuery), errors.Is(err, services.ErrInvalidPagination):
		httputil.NewError(c, http.StatusBadRequest, err)
	default:
		httputil.NewError(c, http.StatusInternalServerError, err)
	}
}

// renderFeed serializes the feed and answers. A serialization failure is a
// 500, never the not-found feed.
func renderFeed(c *gin.Context, feed *opdsutils.Feed) {
	atom, err := feed.ToAtom()
	if err != nil {
		logging.Errorf("Error converting feed to Atom: %v", err)
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}
	c.Data(http.StatusOK, atomContentType, []byte(atom))
}

// bookItems renders book rows as acquisition entries, keeping the KOReader
// annotation truncation the feeds always had.
func bookItems(c *gin.Context, books []models.Book) []*opdsutils.Item {
	items := []*opdsutils.Item{}
	isKoreader := strings.Contains(c.GetHeader("User-Agent"), "KOReader")
	for i := range books {
		bookItem := opdsutils.CreateItem(books[i], isKoreader)
		items = append(items, &bookItem)
	}
	return items
}

// authorItems renders author rows as navigation entries pointing at that
// author's books, global or inside the language.
func authorItems(authors []models.Author, browseHref func(id int64) string) []*opdsutils.Item {
	items := []*opdsutils.Item{}
	for _, a := range authors {
		items = append(items, &opdsutils.Item{
			Title: a.FullName,
			Link: []opdsutils.Link{
				{
					Href: browseHref(a.ID),
					Type: "application/atom+xml;profile=opds-catalog",
				},
			},
			Id:      fmt.Sprintf("tag:author:%d", a.ID),
			Updated: time.Now(),
			Content: a.FullName,
		})
	}
	return items
}

// Books serves the global book search feed: /opds/books?title=&page=.
func (h *SearchHandler) Books(c *gin.Context) {
	var filters OpdsBooksSearch
	if err := c.ShouldBindWith(&filters, binding.Query); err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
		return
	}
	page := clampPage(filters.Page)
	offset := page * opdsPageSize

	result, err := h.Search.SearchBooks(c.Request.Context(), models.BookSearchRequest{
		Query:  filters.Title,
		UserID: c.GetInt64("user_id"),
		Limit:  opdsPageSize,
		Offset: offset,
	})
	if err != nil {
		mapOpdsSearchError(c, err)
		return
	}
	if len(result.Books) == 0 {
		c.Data(http.StatusOK, atomContentType, []byte(notFound))
		return
	}

	links := globalSearchLinks()
	if hasNextSearchPage(offset, len(result.Books), result.Total) {
		links = nextSearchLink(links, nextSearchHref("/opds/books", "title", filters.Title, page), true)
	}

	renderFeed(c, &opdsutils.Feed{
		Title:   "Поиск книг",
		Id:      fmt.Sprintf("tag:search:books:%s:%d", url.QueryEscape(filters.Title), page),
		Links:   links,
		Updated: time.Now(),
		Items:   bookItems(c, result.Books),
	})
}

// Authors serves the global author search feed: /opds/search-author?name=&page=.
func (h *SearchHandler) Authors(c *gin.Context) {
	var filters OpdsAuthorSearch
	if err := c.ShouldBindWith(&filters, binding.Query); err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
		return
	}
	page := clampPage(filters.Page)
	offset := page * opdsPageSize

	result, err := h.Search.SearchAuthors(c.Request.Context(), models.AuthorSearchRequest{
		Query:  filters.Name,
		Limit:  opdsPageSize,
		Offset: offset,
	})
	if err != nil {
		mapOpdsSearchError(c, err)
		return
	}
	if len(result.Authors) == 0 {
		c.Data(http.StatusOK, atomContentType, []byte(notFound))
		return
	}

	links := globalSearchLinks()
	if hasNextSearchPage(offset, len(result.Authors), result.Total) {
		links = nextSearchLink(links, nextSearchHref("/opds/search-author", "name", filters.Name, page), true)
	}

	renderFeed(c, &opdsutils.Feed{
		Title:   "Поиск автора",
		Id:      fmt.Sprintf("tag:search:authors:%s:%d", url.QueryEscape(filters.Name), page),
		Links:   links,
		Updated: time.Now(),
		Items: authorItems(result.Authors, func(id int64) string {
			return fmt.Sprintf("/opds/new/0/%d", id)
		}),
	})
}

// BooksByLanguage serves the language book search feed:
// /opds/lang/:lang/search-books?title=&page=.
func (h *SearchHandler) BooksByLanguage(c *gin.Context) {
	lang := c.Param("lang")
	var filters OpdsBooksSearch
	if err := c.ShouldBindWith(&filters, binding.Query); err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
		return
	}
	page := clampPage(filters.Page)
	offset := page * opdsPageSize

	result, err := h.Search.SearchBooks(c.Request.Context(), models.BookSearchRequest{
		Query:    filters.Title,
		UserID:   c.GetInt64("user_id"),
		Language: lang,
		Limit:    opdsPageSize,
		Offset:   offset,
	})
	if err != nil {
		mapOpdsSearchError(c, err)
		return
	}
	if len(result.Books) == 0 {
		c.Data(http.StatusOK, atomContentType, []byte(notFound))
		return
	}

	links := langLinks(lang)
	if hasNextSearchPage(offset, len(result.Books), result.Total) {
		path := fmt.Sprintf("/opds/lang/%s/search-books", lang)
		links = nextSearchLink(links, nextSearchHref(path, "title", filters.Title, page), false)
	}

	renderFeed(c, &opdsutils.Feed{
		Title:   fmt.Sprintf("Поиск книг: %s", getLangName(lang)),
		Id:      fmt.Sprintf("tag:lang:%s:search:books:%s:%d", lang, url.QueryEscape(filters.Title), page),
		Links:   links,
		Updated: time.Now(),
		Items:   bookItems(c, result.Books),
	})
}

// AuthorsByLanguage serves the language author search feed:
// /opds/lang/:lang/search-authors?name=&page=.
func (h *SearchHandler) AuthorsByLanguage(c *gin.Context) {
	lang := c.Param("lang")
	var filters OpdsAuthorSearch
	if err := c.ShouldBindWith(&filters, binding.Query); err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
		return
	}
	page := clampPage(filters.Page)
	offset := page * opdsPageSize

	result, err := h.Search.SearchAuthors(c.Request.Context(), models.AuthorSearchRequest{
		Query:    filters.Name,
		Language: lang,
		Limit:    opdsPageSize,
		Offset:   offset,
	})
	if err != nil {
		mapOpdsSearchError(c, err)
		return
	}
	if len(result.Authors) == 0 {
		c.Data(http.StatusOK, atomContentType, []byte(notFound))
		return
	}

	links := langLinks(lang)
	if hasNextSearchPage(offset, len(result.Authors), result.Total) {
		path := fmt.Sprintf("/opds/lang/%s/search-authors", lang)
		links = nextSearchLink(links, nextSearchHref(path, "name", filters.Name, page), false)
	}

	renderFeed(c, &opdsutils.Feed{
		Title:   fmt.Sprintf("Поиск авторов: %s", getLangName(lang)),
		Id:      fmt.Sprintf("tag:lang:%s:search:authors:%s:%d", lang, url.QueryEscape(filters.Name), page),
		Links:   links,
		Updated: time.Now(),
		Items: authorItems(result.Authors, func(id int64) string {
			return fmt.Sprintf("/opds/lang/%s/author/%d/0", lang, id)
		}),
	})
}
