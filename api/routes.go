package api

import "github.com/gin-gonic/gin"

// SetupBookRoutes sets up routes for books
func SetupBookRoutes(r *gin.RouterGroup) {
	r.GET("/list", GetBooks)
	r.GET("/get/:format/:id", CdnBookGenerate)
	r.GET("/langs", GetLangs)
	r.GET("/self-user", SelfUser)
	r.POST("/change-me", ChangeUser)
	r.GET("/authors", GetAuthors)
	r.POST("/author", GetAuthor)
	r.POST("/upload-book", UploadBook)
	r.POST("/file", GetBookFile)
	r.POST("/fav", FavBook)
}

// SetupTelegramRoutes sets up the admin routes
func SetupTelegramRoutes(r *gin.RouterGroup) {
	r.POST("/telegram/:id", TokenApiEndpoint)
}

// StatusCheck returns status of the service
func StatusCheck(c *gin.Context) {
	c.JSON(200, gin.H{"status": "ok"})
}
