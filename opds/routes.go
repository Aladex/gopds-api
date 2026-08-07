package opds

import (
	"net/http"

	"gopds-api/services"

	"github.com/gin-gonic/gin"
)

// SetupOpdsRoutes sets up the opds routes. The search feeds go through the
// shared search service; navigation, new-books, collections and download
// handlers keep their existing direct paths.
func SetupOpdsRoutes(r *gin.RouterGroup, search services.PublicSearch) {
	searchHandler := &SearchHandler{Search: search}

	r.GET("/", func(c *gin.Context) { c.Redirect(http.StatusMovedPermanently, "/opds/new/0/0") })
	r.GET("/new/:page/:author", GetNewBooks)
	r.GET("/favorites/:page", GetNewBooks)

	// Global search
	r.GET("/search", Search)
	r.GET("/books", searchHandler.Books)
	r.GET("/search-author", searchHandler.Authors)

	// Languages navigation
	r.GET("/languages", GetLanguages)
	r.GET("/lang/:lang", GetLanguageRoot)
	r.GET("/lang/:lang/books/:page", GetBooksByLanguage)
	r.GET("/lang/:lang/search", SearchByLanguage)
	r.GET("/lang/:lang/search-books", searchHandler.BooksByLanguage)
	r.GET("/lang/:lang/search-authors", searchHandler.AuthorsByLanguage)
	r.GET("/lang/:lang/author/:author/:page", GetAuthorBooksByLanguage)

	// Collections navigation
	r.GET("/collections/:page", GetCollections)
	r.GET("/collection/:id/:page", GetCollectionBooks)

	// Download
	r.GET("/get/:format/:id", DownloadBook)
}
