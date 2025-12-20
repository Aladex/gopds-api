package opds

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

// SetupOpdsRoutes sets up the opds routes
func SetupOpdsRoutes(r *gin.RouterGroup) {
	r.GET("/", func(c *gin.Context) { c.Redirect(http.StatusMovedPermanently, "/opds/new/0/0") })
	r.GET("/new/:page/:author", GetNewBooks)
	r.GET("/favorites/:page", GetNewBooks)

	// Global search
	r.GET("/search", Search)
	r.GET("/books", GetBooks)
	r.GET("/search-author", GetAuthor)

	// Languages navigation
	r.GET("/languages", GetLanguages)
	r.GET("/lang/:lang", GetLanguageRoot)
	r.GET("/lang/:lang/books/:page", GetBooksByLanguage)
	r.GET("/lang/:lang/search", SearchByLanguage)
	r.GET("/lang/:lang/search-books", SearchBooksByLanguage)
	r.GET("/lang/:lang/search-authors", SearchAuthorsByLanguage)
	r.GET("/lang/:lang/author/:author/:page", GetAuthorBooksByLanguage)

	// Download
	r.GET("/get/:format/:id", DownloadBook)
}
