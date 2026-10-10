package opds

import (
	"net/http"

	"gopds-api/services"

	"github.com/gin-gonic/gin"
)

// SetupOpdsRoutes sets up the opds routes. The search feeds go through the
// shared search service; navigation, new-books, collections and download
// handlers keep their existing direct paths. Every feed that lists books
// gives them their author lines from lines; nil shows the legacy authors.
func SetupOpdsRoutes(r *gin.RouterGroup, search services.PublicSearch, lines *services.AuthorLines) {
	searchHandler := &SearchHandler{Search: search, AuthorLines: lines}
	feeds := &Feeds{AuthorLines: lines}

	r.GET("/", func(c *gin.Context) { c.Redirect(http.StatusMovedPermanently, "/opds/new/0/0") })
	r.GET("/new/:page/:author", feeds.GetNewBooks)
	r.GET("/favorites/:page", feeds.GetNewBooks)

	// Global search
	r.GET("/search", Search)
	r.GET("/books", searchHandler.Books)
	r.GET("/search-author", searchHandler.Authors)

	// Languages navigation
	r.GET("/languages", GetLanguages)
	r.GET("/lang/:lang", GetLanguageRoot)
	r.GET("/lang/:lang/books/:page", feeds.GetBooksByLanguage)
	r.GET("/lang/:lang/search", SearchByLanguage)
	r.GET("/lang/:lang/search-books", searchHandler.BooksByLanguage)
	r.GET("/lang/:lang/search-authors", searchHandler.AuthorsByLanguage)
	r.GET("/lang/:lang/author/:author/:page", feeds.GetAuthorBooksByLanguage)

	// Collections navigation
	r.GET("/collections/:page", GetCollections)
	r.GET("/collection/:id/:page", feeds.GetCollectionBooks)

	// Download
	r.GET("/get/:format/:id", DownloadBook)
}
