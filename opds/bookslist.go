package opds

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

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
// the legacy authors.
type Feeds struct {
	AuthorLines *services.AuthorLines
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

	pageNum, err := strconv.Atoi(c.Param("page"))
	if err != nil {
		logging.Error(err)
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	authorID, err := strconv.Atoi(c.Param("author"))
	if err != nil {
		authorID = 0
	}
	if pageNum > 0 {
		filters.Offset = pageNum * 10
	}

	if authorID > 0 {
		filters.Author = authorID
	}

	books, tc, err := database.GetBooks(userID, filters)
	if err != nil {
		logging.Error(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	rootLinks := newBooksLinks(filters.Fav, pageNum, authorID, hasNextPage(filters.Limit, pageNum, tc))
	feedId := fmt.Sprintf("tag:root:new:%d:%d", pageNum, authorID)
	if filters.Fav {
		feedId = fmt.Sprintf("tag:root:favorites:%d", pageNum)
	}

	feed := &opdsutils.Feed{Title: "Лепробиблиотека", Id: feedId, Links: rootLinks, Updated: time.Now(),
		Items: []*opdsutils.Item{}}

	// Show navigation items only on the root page (page 0, no author filter, not favorites)
	if !filters.Fav && pageNum == 0 && filters.Author == 0 {
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
func newBooksLinks(favorites bool, pageNum, authorID int, hasNext bool) []opdsutils.Link {
	links := globalSearchLinks()
	if !hasNext {
		return links
	}
	next := fmt.Sprintf("/opds/new/%d/%d", pageNum+1, authorID)
	if favorites {
		next = fmt.Sprintf("/opds/favorites/%d", pageNum+1)
	}
	return append(links, opdsutils.Link{Href: next, Rel: relNext, Type: typeOpdsCatalog})
}
