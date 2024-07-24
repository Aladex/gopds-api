package api

import (
	"github.com/gin-gonic/gin"
	"gopds-api/models"
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
}

// StatusCheck returns the status of the service
// Auth godoc
// @Summary Check service status
// @Description Returns the current status of the service
// @Tags status
// @Produce  json
// @Success 200 {object} models.Result "Service is running"
// @Router /status [get]
func StatusCheck(c *gin.Context) {
	c.JSON(200, models.Result{
		Result: "Service is running",
		Error:  nil,
	})
}
