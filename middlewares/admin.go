package middlewares

import (
	"github.com/gin-gonic/gin"
	"gopds-api/database"
	"net/http"
	"strings"
)

// AdminMiddleware checks for admin privileges using the Authorization token.
func AdminMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		var token string
		var err error

		// Try to get token from header or cookie
		authHeader := c.GetHeader("Authorization")
		if authHeader != "" {
			token = authHeader
		} else {
			// Если токена в заголовке нет, попробуем получить токен из куки
			token, err = c.Cookie("token")
			if err != nil || token == "" {
				abortWithStatus(c, http.StatusUnauthorized, "required_token")
				return
			}
		}

		// Validate token
		username, dbID, err := validateToken(token)
		if err != nil {
			abortWithStatus(c, http.StatusUnauthorized, err.Error())
			return
		}
		dbUser, err := database.GetUser(strings.ToLower(username))
		if err != nil || !dbUser.IsSuperUser {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}

		// Set username and user_id in context
		c.Set("username", username)
		c.Set("user_id", dbID)
		c.Next()
	}
}
