package middlewares

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gopds-api/database"
	"gopds-api/models"
)

// BasicAuth Get the Basic Authentication credentials
func BasicAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		user, password, hasAuth := c.Request.BasicAuth()

		if !hasAuth {
			abortWithAuthRequired(c)
			return
		}

		res, dbUser, err := database.CheckUser(models.LoginRequest{Login: user, Password: password})
		if err != nil || !res {
			abortWithAuthRequired(c)
			return
		}

		c.Set("username", user)
		c.Set("user_id", dbUser.ID)
		c.Next()
	}
}

func abortWithAuthRequired(c *gin.Context) {
	c.Writer.Header().Set("WWW-Authenticate", "Basic realm=Restricted")
	c.AbortWithStatus(http.StatusUnauthorized)
}
