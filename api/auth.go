package api

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/sessions"
	"gopds-api/utils"
	"net/http"
	"strings"
)

// DropAllSessions Метод сброса всех сессий пользователя
// Auth godoc
// @Summary Метод для сброса всех сессий пользователя
// @Description Метод для сброса всех сессий пользователя
// @Tags login
// @Accept  json
// @Produce  json
// @Success 200 {object} models.LoggedInUser
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Router /drop-sessions [get]
func DropAllSessions(c *gin.Context) {
	userToken := c.Request.Header.Get("Authorization")
	username := c.GetString("username")

	sessions.DeleteSessionKey(models.LoggedInUser{
		User:  strings.ToLower(username),
		Token: &userToken,
	})
	go sessions.DropAllSessions(userToken)
	c.JSON(200, gin.H{"result": "ok"})
}

// AuthCheck Returns an user and token for header
// Auth godoc
// @Summary Returns an user and token for header
// @Description Login method for token generation
// @Tags login
// @Accept  json
// @Produce  json
// @Param  body body models.LoginRequest true "Login Data"
// @Success 200 {object} models.LoggedInUser
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /login [post]
func AuthCheck(c *gin.Context) {
	var user models.LoginRequest
	if err := c.ShouldBindJSON(&user); err == nil {
		res, dbUser, err := database.CheckUser(user)
		if err != nil {
			logging.CustomLog.WithFields(logrus.Fields{
				"action":   "login",
				"result":   "user is not found",
				"username": user.Login,
			}).Info()
			httputil.NewError(c, http.StatusForbidden, errors.New("bad_credentials"))
			return
		}
		switch res {
		case true:
			userToken, err := utils.CreateToken(dbUser)
			if err != nil {
				httputil.NewError(c, http.StatusForbidden, err)
				return
			}
			hf, err := database.HaveFavs(dbUser.ID)

			if err != nil {
				httputil.NewError(c, http.StatusBadRequest, err)
				return
			}
			thisUser := models.LoggedInUser{
				User:        dbUser.Login,
				FirstName:   dbUser.FirstName,
				LastName:    dbUser.LastName,
				Token:       &userToken,
				IsSuperuser: &dbUser.IsSuperUser,
				HaveFavs:    &hf,
			}
			sessions.SetSessionKey(thisUser)
			go database.LoginDateSet(&dbUser)
			c.JSON(200, thisUser)
			return
		default:
			logging.CustomLog.WithFields(logrus.Fields{
				"action":   "login",
				"result":   "invalid password",
				"username": user.Login,
			}).Info()
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
	username := c.GetString("username")
	sessions.DeleteSessionKey(models.LoggedInUser{
		User:  strings.ToLower(username),
		Token: &userToken,
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
// @Router /books/self-user [get]
func SelfUser(c *gin.Context) {
	username := c.GetString("username")
	dbUser, err := database.GetUser(strings.ToLower(username))
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}
	hf, err := database.HaveFavs(dbUser.ID)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}
	selfUser := models.LoggedInUser{
		User:        dbUser.Login,
		BooksLang:   dbUser.BooksLang,
		IsSuperuser: &dbUser.IsSuperUser,
		FirstName:   dbUser.FirstName,
		LastName:    dbUser.LastName,
		HaveFavs:    &hf,
	}

	c.JSON(200, selfUser)
}

// ChangeUser метод для изменения объекта пользователя
// Auth godoc
// @Summary Returns an users object
// @Description user object
// @Tags users
// @Accept  json
// @Produce  json
// @Param  body body models.SelfUserChangeRequest true "User object"
// @Success 200 {object} models.LoggedInUser
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /books/change-me [post]
func ChangeUser(c *gin.Context) {
	username := c.GetString("username")
	var userNewData models.SelfUserChangeRequest
	if err := c.ShouldBindJSON(&userNewData); err == nil {
		u := models.LoginRequest{
			Login:    username,
			Password: userNewData.Password,
		}
		result, dbUser, err := database.CheckUser(u)

		if !result && len(userNewData.Password) > 0 || err != nil {
			httputil.NewError(c, http.StatusForbidden, errors.New("auth_error"))
			return
		}

		if len(userNewData.Password) > 0 && len(userNewData.NewPassword) < 8 {
			httputil.NewError(c, http.StatusBadRequest, errors.New("bad_password"))
			return
		}

		if len(userNewData.Password) > 0 && userNewData.NewPassword == userNewData.Password {
			httputil.NewError(c, http.StatusBadRequest, errors.New("same_password"))
			return
		}

		dbUser.FirstName = userNewData.FirstName
		dbUser.LastName = userNewData.LastName
		dbUser.Password = userNewData.NewPassword
		dbUser.BooksLang = userNewData.BooksLang

		user, err := database.ActionUser(models.AdminCommandToUser{
			Action: "update",
			User:   dbUser,
		})
		if err != nil {
			c.JSON(500, err)
			return
		}
		hf, err := database.HaveFavs(dbUser.ID)
		if err != nil {
			httputil.NewError(c, http.StatusBadRequest, err)
			return
		}
		selfUser := models.LoggedInUser{
			User:        user.Login,
			FirstName:   user.FirstName,
			LastName:    user.LastName,
			IsSuperuser: &user.IsSuperUser,
			HaveFavs:    &hf,
		}
		c.JSON(200, selfUser)
		return

	}

}
