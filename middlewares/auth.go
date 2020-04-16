package middlewares

import (
	"github.com/gin-gonic/gin"
	"gopds-api/models"
	"gopds-api/sessions"
	"gopds-api/utils"
)

// AuthMiddleware Мидлварь для проверки токена пользователя в методах GET и POST
func AuthMiddleware() gin.HandlerFunc {
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
		thisUser := models.LoggedInUser{
			User:  username,
			Token: &userToken,
		}
		if !sessions.CheckSessionKey(thisUser) {
			c.JSON(401, "session is invalid")
			c.Abort()
			return
		}
		go sessions.SetSessionKey(thisUser)
		c.Next()
	}
}
