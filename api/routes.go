package api

import (
	"os"

	"gopds-api/middlewares"
	"gopds-api/models"

	"github.com/gin-gonic/gin"
)

// SetupBookRoutes sets up routes for books. The search-backed endpoints are
// methods of the injected handler; the rest stay on their package functions.
func SetupBookRoutes(r *gin.RouterGroup, search *SearchHandler) {
	r.GET("/list", search.Books)
	r.GET("/get/:format/:id", GetBookFile)
	r.HEAD("/get/:format/:id", HeadBookFile)
	r.GET("/langs", GetLangs)
	r.GET("/self-user", SelfUser)
	r.GET("/theme", GetThemePreference)
	r.GET("/getsigned/:format/:id", GetSignedBookUrl)
	r.GET("/autocomplete", search.Autocomplete)
	r.POST("/change-me", middlewares.CSRFMiddleware(), ChangeUser)
	r.POST("/theme", middlewares.CSRFMiddleware(), SetThemePreference)
	r.GET("/authors", search.Authors)
	r.POST("/author", GetAuthor)
	r.POST("/file", GetBookFile)
	r.POST("/fav", middlewares.CSRFMiddleware(), FavBook)
}

// SetupAuthRoutes sets up routes for authentication (public routes)
func SetupAuthRoutes(r *gin.RouterGroup) {
	r.POST("/login", middlewares.LoginRateLimitMiddleware(), AuthCheck)
	r.POST("/register", Registration)
	r.GET("/csrf-token", GetCSRFToken)
	r.GET("/init", InitSession)
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
