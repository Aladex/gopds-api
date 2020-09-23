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
		res, _, err := database.CheckUser(models.LoginRequest{
			Login:    user,
			Password: password,
		})
		if !hasAuth || !res || err != nil {
			c.Writer.Header().Set("WWW-Authenticate", "Basic realm=Restricted")
			c.Status(401)
			c.Abort()
			return
		}
	}
}
