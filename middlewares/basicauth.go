package middlewares

import (
	"github.com/gin-gonic/gin"
	"gopds-api/database"
	"gopds-api/models"
)

// BasicAuth Get the Basic Authentication credentials
func BasicAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		user, password, hasAuth := c.Request.BasicAuth()

		if !hasAuth {
			c.Writer.Header().Set("WWW-Authenticate", "Basic realm=Restricted")
			c.Status(401)
			c.Abort()
			return
		}

		if res, _, err := database.CheckUser(models.LoginRequest{user, password}); err != nil || !res {
			c.Writer.Header().Set("WWW-Authenticate", "Basic realm=Restricted")
			c.Status(401)
			c.Abort()
			return
		}
	}
}
