package middlewares

import (
	"github.com/gin-gonic/gin"
	"gopds-api/database"
	"gopds-api/utils"
	"strings"
)

// AdminMiddleware мидлварь для проверки админских прав
func AdminMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		userToken := c.Request.Header.Get("Authorization")

		if userToken == "" {
			c.JSON(401, "required_token")
			c.Abort()
			return
		}
		username, err := utils.CheckToken(userToken)
		if err != nil {
			c.JSON(401, "invalid_token")
			c.Abort()
			return
		}
		dbUser, err := database.GetUser(strings.ToLower(username))
		if err != nil {
			c.JSON(403, "restricted_zone")
			c.Abort()
			return
		}

		if !dbUser.IsSuperUser {
			c.JSON(403, "restricted_zone")
			c.Abort()
			return
		}
		c.Next()
	}
}
