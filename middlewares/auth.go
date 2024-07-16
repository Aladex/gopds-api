package middlewares

import (
	"errors"
	"gopds-api/sessions"
	"net/http"

	"github.com/gin-gonic/gin"
	"gopds-api/models"
	"gopds-api/utils"
)

// validateToken simplifies token validation by consolidating error handling.
func validateToken(token string) (string, int64, error) {
	username, dbID, err := utils.CheckToken(token)
	if err != nil {
		return "", 0, err
	}

	if !sessions.CheckSessionKey(models.LoggedInUser{User: username, Token: &token}) {
		return "", 0, errors.New("invalid_session")
	}

	go sessions.SetSessionKey(models.LoggedInUser{User: username, Token: &token})
	return username, dbID, nil
}

// abortWithStatus simplifies error responses.
func abortWithStatus(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": message})
}

// AuthMiddleware checks if user is logged in and sets username in context.
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		userToken := c.GetHeader("Authorization")
		if userToken == "" {
			abortWithStatus(c, http.StatusUnauthorized, "required_token")
			return
		}

		username, dbID, err := validateToken(userToken)
		if err != nil {
			abortWithStatus(c, http.StatusUnauthorized, err.Error())
			return
		}

		c.Set("username", username)
		c.Set("user_id", dbID)
		c.Next()
	}
}

// TokenMiddleware checks if a valid token is provided in query parameters.
func TokenMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		var token models.LinkToken
		if err := c.ShouldBindQuery(&token); err != nil || token.Token == "" {
			abortWithStatus(c, http.StatusBadRequest, "required_token")
			return
		}

		username, _, err := validateToken(token.Token)
		if err != nil {
			abortWithStatus(c, http.StatusUnauthorized, err.Error())
			return
		}

		c.Set("username", username)
		c.Next()
	}
}
