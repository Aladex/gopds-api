package middlewares

import (
	"github.com/gin-gonic/gin"
	"gopds-api/database"
	"gopds-api/utils"
)

// AuthMiddleware Мидлварь для проверки токена пользователя в методах GET и POST
func AdminMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		userToken := c.Request.Header.Get("Authorization")

		if userToken == "" {
			c.JSON(401, "token is required")
			c.Abort()
			return
		}
		username, err := utils.CheckToken(userToken)
		if err != nil {
			c.JSON(401, "token is invalid")
			c.Abort()
			return
		}

		if !database.GetSuperUserRole(username) {
			c.JSON(403, "restricted zone")
			c.Abort()
			return
		}
		c.Next()
	}
}
