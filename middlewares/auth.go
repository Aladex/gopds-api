package middlewares

import (
	"context"
	"errors"
	"gopds-api/sessions"
	"net/http"
	"time"

	"gopds-api/models"
	"gopds-api/utils"

	"github.com/gin-gonic/gin"
)

// validateToken simplifies token validation by consolidating error handling.
func validateToken(token string) (string, int64, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// If token not in Redis, return error
	_, err := sessions.CheckSessionKeyInRedis(ctx, token)
	if err != nil {
		return "", 0, false, errors.New("invalid_session")
	}

	username, dbID, isSuperUser, tokenType, err := utils.CheckTokenWithType(token)
	if err != nil {
		return "", 0, false, errors.New("invalid_session")
	}

	// If token is expired and it's an access token, try to refresh
	if tokenType == "access" {
		// Check if token is close to expiry (within 2 minutes)
		// If so, this will be handled by the frontend calling refresh endpoint
		err = sessions.UpdateSessionKey(ctx, models.LoggedInUser{User: username, Token: &token})
		if err != nil {
			return "", 0, false, errors.New("session_update_failed")
		}
	}

	return username, dbID, isSuperUser, nil
}

// abortWithStatus simplifies error responses.
func abortWithStatus(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": message})
}

// AuthMiddleware checks if user is logged in and sets username in context.
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		var token string
		var err error

		// Try to get token from header or cookie
		authHeader := c.GetHeader("Authorization")
		if authHeader != "" {
			token = authHeader
		} else {
			token, err = c.Cookie("token")
			if err != nil || token == "" {
				abortWithStatus(c, http.StatusUnauthorized, "required_token")
				return
			}
		}

		// Validate token
		username, dbID, _, err := validateToken(token)
		if err != nil {
			abortWithStatus(c, http.StatusUnauthorized, err.Error())
			return
		}

		// Set username and user_id in context
		c.Set("username", username)
		c.Set("user_id", dbID)
		c.Next()
	}
}
