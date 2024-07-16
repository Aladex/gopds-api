package middlewares

import (
	"github.com/gin-gonic/gin"
	"gopds-api/database"
	"gopds-api/utils"
	"net/http"
	"strings"
)

// AdminMiddleware checks for admin privileges using the Authorization token.
func AdminMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		userToken := c.GetHeader("Authorization")
		if userToken == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "required_token"})
			return
		}

		username, _, err := utils.CheckToken(userToken)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid_token"})
			return
		}

		dbUser, err := database.GetUser(strings.ToLower(username))
		if err != nil || !dbUser.IsSuperUser {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "restricted_zone"})
			return
		}

		c.Next()
	}
}
