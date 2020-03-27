package api

import (
	"errors"
	"github.com/gin-gonic/gin"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/models"
	"gopds-api/sessions"
	"gopds-api/utils"
	"log"
	"net/http"
)

// AuthCheck Returns an user and token for header
// Auth godoc
// @Summary Returns an user and token for header
// @Description Login method for token generation
// @Tags login
// @Accept  json
// @Produce  json
// @Param  body body models.User true "Login Data"
// @Success 200 {object} models.LoggedInUser
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /login [post]
func AuthCheck(c *gin.Context) {
	var user models.User
	if err := c.ShouldBindJSON(&user); err == nil {
		res, err := database.CheckUser(user)
		if err != nil {
			httputil.NewError(c, http.StatusForbidden, err)
			return
		}
		switch res {
		case true:
			userToken, err := utils.CreateToken(user.Login)
			if err != nil {
				httputil.NewError(c, http.StatusForbidden, err)
				return
			}
			thisUser := models.LoggedInUser{
				User:  user.Login,
				Token: userToken,
			}
			sessions.SetSessionKey(thisUser)
			c.JSON(200, thisUser)
		default:
			httputil.NewError(c, http.StatusForbidden, errors.New("bad password"))
			return
		}
	} else {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}
}

// LogOut Метод разлогина
// Auth godoc
// @Summary Метод разлогина
// @Description Метод разлогина
// @Tags login
// @Accept  json
// @Produce  json
// @Success 200 {object} models.LoggedInUser
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Router /logout [get]
func LogOut(c *gin.Context) {
	userToken := c.Request.Header.Get("Authorization")
	username, err := utils.CheckToken(userToken)
	if err != nil {
		log.Printf("%s with token %s tried to logout", username, userToken)
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}
	sessions.DeleteSessionKey(models.LoggedInUser{
		User:  username,
		Token: userToken,
	})
	c.JSON(200, gin.H{"result": "ok"})
}

// SelfUser Метод для получения информации по пользователю
// Auth godoc
// @Summary Метод для получения информации по пользователю
// @Description Метод для получения информации по пользователю
// @Tags login
// @Accept  json
// @Produce  json
// @Success 200 {object} models.LoggedInUser
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Router /self-user [get]
func SelfUser(c *gin.Context) {
	userToken := c.Request.Header.Get("Authorization")
	username, err := utils.CheckToken(userToken)
	if err != nil {
		log.Printf("%s with token %s tried to get username", username, userToken)
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(200, models.LoggedInUser{
		User: username,
	})
}
