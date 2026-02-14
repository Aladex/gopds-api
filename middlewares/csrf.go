package middlewares

import (
	"crypto/rand"
	"encoding/hex"
	"gopds-api/utils"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// CSRFMiddleware provides CSRF protection
func CSRFMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip CSRF for GET, HEAD, OPTIONS requests
		if c.Request.Method == "GET" || c.Request.Method == "HEAD" || c.Request.Method == "OPTIONS" {
			c.Next()
			return
		}

		// Get CSRF token from header
		csrfToken := c.GetHeader("X-CSRF-Token")

		// Get stored CSRF token from cookie
		storedToken, err := c.Cookie("csrf_token")
		if err != nil || storedToken == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "CSRF token missing"})
			return
		}

		// Compare tokens
		if csrfToken != storedToken {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "CSRF token invalid"})
			return
		}

		c.Next()
	}
}

// GenerateCSRFToken generates a new CSRF token
func GenerateCSRFToken() string {
	bytes := make([]byte, 32)
	_, err := rand.Read(bytes)
	if err != nil {
		// Fallback to less secure method if crypto/rand fails
		return utils.GetRandomString(32)
	}
	return hex.EncodeToString(bytes)
}

// SetCSRFToken sets CSRF token as cookie
func SetCSRFToken(c *gin.Context) {
	token := GenerateCSRFToken()
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("csrf_token", token, 3600, "/", "", !viper.GetBool("app.devel_mode"), false) // httpOnly=false for JS access
	c.JSON(http.StatusOK, gin.H{"csrf_token": token})
}
