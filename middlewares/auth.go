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
			c.JSON(401, "required_token")
			c.Abort()
			return
		}
		username, dbID, err := utils.CheckToken(userToken)
		if err != nil {
			c.JSON(401, "invalid_token")
			c.Abort()
			return
		}
		thisUser := models.LoggedInUser{
			User:  username,
			Token: &userToken,
		}
		if !sessions.CheckSessionKey(thisUser) {
			c.JSON(401, "invalid_session")
			c.Abort()
			return
		}
		go sessions.SetSessionKey(thisUser)
		c.Set("username", username)
		c.Set("user_id", dbID)
		c.Next()
	}
}
