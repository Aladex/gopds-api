package middlewares

import (
	"context"
	"errors"
	"fmt"
	"github.com/spf13/viper"
	"gopds-api/sessions"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gopds-api/models"
	"gopds-api/utils"
)

// validateToken simplifies token validation by consolidating error handling.
func validateToken(token string) (string, int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// If token not in Redis, return error
	_, err := sessions.CheckSessionKeyInRedis(ctx, token)
	if err != nil {
		return "", 0, errors.New("invalid_session")
	}

	username, dbID, err := utils.CheckToken(token)
	if err != nil {
		return "", 0, err
	}

	err = sessions.UpdateSessionKey(ctx, models.LoggedInUser{User: username, Token: &token})
	if err != nil {
		return "", 0, errors.New("invalid_session")
	}
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
		var signedURL models.SignedURL
		if err := c.ShouldBindQuery(&signedURL); err != nil {
			abortWithStatus(c, http.StatusBadRequest, "invalid_url")
			return
		}

		// Simplify current URL reconstruction
		currentURL := fmt.Sprintf("http://%s%s", c.Request.Host, c.Request.URL.Path)

		if !utils.VerifySignature(viper.GetString("secret_key"), currentURL, signedURL.Signature, signedURL.Expires) {
			abortWithStatus(c, http.StatusUnauthorized, "invalid_signature")
			return
		}
		c.Next()
	}
}
