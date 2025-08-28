package api

import (
	"gopds-api/models"
	"os"

	"github.com/gin-gonic/gin"
)

// SetupBookRoutes sets up routes for books
func SetupBookRoutes(r *gin.RouterGroup) {
	r.GET("/list", GetBooks)
	r.GET("/get/:format/:id", GetBookFile)
	r.GET("/langs", GetLangs)
	r.GET("/self-user", SelfUser)
	r.GET("/getsigned/:format/:id", GetSignedBookUrl)
	r.GET("/autocomplete", Autocomplete)
	r.POST("/change-me", ChangeUser)
	r.GET("/authors", GetAuthors)
	r.POST("/author", GetAuthor)
	r.POST("/file", GetBookFile)
	r.POST("/fav", FavBook)
	r.GET("/ws", WebsocketHandler)
}

// SetupAuthRoutes sets up routes for authentication (public routes)
func SetupAuthRoutes(r *gin.RouterGroup) {
	r.POST("/login", AuthCheck)
	r.POST("/register", Registration)
	r.GET("/csrf-token", GetCSRFToken)
	r.POST("/refresh-token", RefreshToken)
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
