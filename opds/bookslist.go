package opds

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/opdsutils"
	"gopds-api/services"
)

func hasNextPage(limit, currentPage, totalCount int) bool {
	totalPages := totalCount / limit
	return currentPage < totalPages
}

// Feeds serves the OPDS feeds that list books outside search: the newest
// books (all, by author, favorites), the books of a language and the books of
// a collection. AuthorLines gives each listed book its author line; nil shows
// the legacy authors. Entities, when set, names authors by their entities
// (SetupOpdsRoutes).
type Feeds struct {
	AuthorLines *services.AuthorLines
	Entities    AuthorEntityReader
}

// AuthorEntityReader reads one live author entity (database).
type AuthorEntityReader interface {
	AuthorEntity(ctx context.Context, id uuid.UUID) (models.AuthorEntity, error)
}

// feedAuthor is the author a feed is of, read from its path under the
// feeds' identity: the ID as the path spells it, for the feed's own links,
// and the author it names. An ID that names no author under the identity —
// the other identity's, or a gone entity — answers 404 and reports false.
// Text that is no ID at all is no author, as the feeds always read it.
func (f *Feeds) feedAuthor(c *gin.Context, raw string) (string, services.AuthorRef, *models.AuthorEntity, bool) {
	ref, err := services.ParseAuthorRef(raw, f.Entities != nil)
	switch {
	case errors.Is(err, services.ErrInvalidAuthorID):
		return "0", services.AuthorRef{}, nil, true
	case errors.Is(err, services.ErrAuthorNotFound):
		authorNotFound(c)
		return "", ref, nil, false
	}
	if ref.Entity == uuid.Nil {
		return strconv.FormatInt(ref.Legacy, 10), ref, nil, true
	}
	entity, err := f.Entities.AuthorEntity(c.Request.Context(), ref.Entity)
	switch {
	case errors.Is(err, database.ErrAuthorEntityNotFound):
		authorNotFound(c)
		return "", ref, nil, false
	case err != nil:
		logging.Error(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return "", ref, nil, false
	}
	return ref.Entity.String(), ref, &entity, true
}

// authorNotFound answers a feed of an author that is not there as a path
// nothing serves is answered.
func authorNotFound(c *gin.Context) {
	c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
}

// GetNewBooks serves the newest books, of one author or the reader's favorites.
func (f *Feeds) GetNewBooks(c *gin.Context) {
	filters := models.BookFilters{Limit: opdsPageSize}
	userID := c.GetInt64("user_id")

	hf, err := database.HaveFavs(userID)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	filters.Fav = c.FullPath() == "/opds/favorites/:page"

	// An author's own feed is the first page of its newest books.
	page := c.Param("page")
	if c.FullPath() == "/opds/author/:author" {
		page = "0"
	}
	pageNum, err := strconv.Atoi(page)
	if err != nil {
		logging.Error(err)
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	authorID, author, _, ok := f.feedAuthor(c, c.Param("author"))
	if !ok {
		return
	}
	if pageNum > 0 {
		filters.Offset = pageNum * 10
	}

	if author.Legacy > 0 {
		filters.Author = int(author.Legacy)
	}
	filters.AuthorEntity = author.Entity

	books, tc, err := database.GetBooks(userID, filters)
	if err != nil {
		logging.Error(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	rootLinks := newBooksLinks(filters.Fav, pageNum, authorID, hasNextPage(filters.Limit, pageNum, tc))
	feedId := fmt.Sprintf("tag:root:new:%d:%s", pageNum, authorID)
	if filters.Fav {
		feedId = fmt.Sprintf("tag:root:favorites:%d", pageNum)
	}

	feed := &opdsutils.Feed{Title: "Лепробиблиотека", Id: feedId, Links: rootLinks, Updated: time.Now(),
		Items: []*opdsutils.Item{}}

	// Show navigation items only on the root page (page 0, no author filter, not favorites)
	if !filters.Fav && pageNum == 0 && filters.Author == 0 && filters.AuthorEntity == uuid.Nil {
		feed.Items = append(feed.Items, rootNavigationItems(hf)...)
	}

	feed.Items = append(feed.Items, bookItems(c, f.AuthorLines, books, isKoreader(c))...)

	atom, err := feed.ToAtom()
	if err != nil {
		logging.Error(err)
	}

	c.Data(200, "application/atom+xml;charset=utf-8", []byte(atom))
}

// Names and addresses the root page's navigation shares with the feeds it
// leads to.
const (
	titleFavorites     = "Избранное"
	titleCollections   = "Подборки"
	titleLanguageBooks = "Книги по языкам"
	hrefLanguages      = "/opds/languages"
	hrefCollections    = "/opds/collections/0"
)

// rootNavigationItems are the navigation entries of the root page: the
// reader's favorites when they have any, the languages and the collections.
func rootNavigationItems(haveFavorites bool) []*opdsutils.Item {
	var items []*opdsutils.Item
	if haveFavorites {
		items = append(items, navigationItem(titleFavorites, "/opds/favorites/0", "tag:nav:favorites", titleFavorites))
	}
	return append(items,
		navigationItem("По языкам", hrefLanguages, "tag:nav:languages", titleLanguageBooks),
		navigationItem(titleCollections, hrefCollections, "tag:nav:collections", "Подборки книг"))
}

// navigationItem is a navigation entry leading to the catalog feed at href.
func navigationItem(title, href, id, content string) *opdsutils.Item {
	return &opdsutils.Item{
		Title:   title,
		Link:    []opdsutils.Link{{Href: href, Type: typeOpdsCatalog}},
		Id:      id,
		Updated: time.Now(),
		Content: content,
	}
}

// newBooksLinks are the feed links of a page of the newest books (of an
// author, or the favorites): start, search, and the next page while pages
// remain.
func newBooksLinks(favorites bool, pageNum int, authorID string, hasNext bool) []opdsutils.Link {
	links := globalSearchLinks()
	if !hasNext {
		return links
	}
	next := fmt.Sprintf("/opds/new/%d/%s", pageNum+1, authorID)
	if favorites {
		next = fmt.Sprintf("/opds/favorites/%d", pageNum+1)
	}
	return append(links, opdsutils.Link{Href: next, Rel: relNext, Type: typeOpdsCatalog})
}
