package opds

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

// SetupOpdsRoutes настраивает маршруты для OPDS каталога
func SetupOpdsRoutes(r *gin.RouterGroup) {
	r.GET("/", func(c *gin.Context) { c.Redirect(http.StatusMovedPermanently, "/opds/new/0/0") })
	r.GET("/new/:page/:author", GetNewBooks)
	r.GET("/favorites/:page", GetNewBooks) // Предполагается, что GetNewBooks подходит и для избранного
	r.GET("/search", Search)
	r.GET("/books", GetBooks)
	r.GET("/search-author", GetAuthor)
	r.GET("/get/:format/:id", DownloadBook)
}
