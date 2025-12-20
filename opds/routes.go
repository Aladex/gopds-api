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
	r.GET("/languages", GetLanguages)
	r.GET("/lang/:lang/:page", GetBooksByLanguage)
	r.GET("/search", Search)
	r.GET("/books", GetBooks)
	r.GET("/search-author", GetAuthor)
	r.GET("/get/:format/:id", DownloadBook)
}
