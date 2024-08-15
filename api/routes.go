package api

import (
	"github.com/gin-gonic/gin"
	"gopds-api/models"
	"os"
)

// SetupBookRoutes sets up routes for books
func SetupBookRoutes(r *gin.RouterGroup) {
	r.GET("/list", GetBooks)
	r.GET("/get/:format/:id", GetBookFile)
	r.GET("/langs", GetLangs)
	r.GET("/self-user", SelfUser)
	r.GET("/getsigned/:format/:id", GetSignedBookUrl)
	r.POST("/change-me", ChangeUser)
	r.GET("/authors", GetAuthors)
	r.POST("/author", GetAuthor)
	r.POST("/file", GetBookFile)
	r.POST("/fav", FavBook)
	r.GET("/collections", GetCollections)
	r.POST("/create-collection", CreateCollection)
	r.POST("/add-to-collection", AddBookToCollection)
	r.POST("/remove-from-collection", RemoveBookFromCollection)
	r.GET("/:id/collections", GetBookCollections)
	r.POST("/update-book-position", UpdateBookPositionInCollection)
	r.POST("/update-collection/:id", UpdateCollection)
	r.GET("/collection/:id", GetCollection)
}

// SetupLogoutRoute sets up routes for logout and session management
func SetupLogoutRoute(r *gin.RouterGroup) {
	r.GET("/logout", LogOut)
	r.GET("/drop-sessions", DropAllSessions)
}

// StatusCheck returns the status of the service
// Auth godoc
// @Summary Check service status
// @Description Returns the current status of the service
// @Tags api
// @Produce  json
// @Success 200 {object} models.Result "Result"
// @Router /api/status [get]
func StatusCheck(c *gin.Context) {
	osVersion, err := os.ReadFile("version")
	result := "dev-version"
	if err == nil {
		result = string(osVersion)
	}
	c.JSON(200, models.Result{
		Result: result,
		Error:  nil,
	})
}
