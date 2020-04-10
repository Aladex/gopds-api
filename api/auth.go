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
	"strings"
)

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
			httputil.NewError(c, http.StatusForbidden, err)
			return
		}
		switch res {
		case true:
			userToken, err := utils.CreateToken(strings.ToLower(user.Login))
			if err != nil {
				httputil.NewError(c, http.StatusForbidden, err)
				return
			}
			thisUser := models.LoggedInUser{
				User:        strings.ToLower(user.Login),
				FirstName:   dbUser.FirstName,
				LastName:    dbUser.LastName,
				Token:       &userToken,
				IsSuperuser: &dbUser.IsSuperUser,
			}
			sessions.SetSessionKey(thisUser)
			go database.LoginDateSet(&dbUser)
			c.JSON(200, thisUser)
			return
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
// @Router /self-user [get]
func SelfUser(c *gin.Context) {
	userToken := c.Request.Header.Get("Authorization")
	username, err := utils.CheckToken(userToken)
	if err != nil {
		log.Printf("%s with token %s tried to get username", username, userToken)
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}
	selfUser := models.LoggedInUser{
		User: strings.ToLower(username),
	}
	dbUser, err := database.GetUser(strings.ToLower(username))
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}
	selfUser.IsSuperuser = &dbUser.IsSuperUser
	selfUser.FirstName = dbUser.FirstName
	selfUser.LastName = dbUser.LastName
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
	userToken := c.Request.Header.Get("Authorization")
	username, err := utils.CheckToken(userToken)
	if err != nil {
		log.Printf("%s with token %s tried to get username", username, userToken)
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	var userNewData models.SelfUserChangeRequest
	if err := c.ShouldBindJSON(&userNewData); err == nil {
		u := models.LoginRequest{
			Login:    username,
			Password: userNewData.Password,
		}
		result, dbUser, err := database.CheckUser(u)
		if !result && userNewData.Password != "" || err != nil {
			httputil.NewError(c, http.StatusForbidden, errors.New("bad login or password"))
			return
		} else if result && len(userNewData.NewPassword) > 7 {
			dbUser.Password = userNewData.NewPassword
		} else if userNewData.Password == "" {
			dbUser.Password = ""
		} else {
			httputil.NewError(c, http.StatusBadRequest, errors.New("new password is bad"))
			return
		}

		dbUser.FirstName = userNewData.FirstName
		dbUser.LastName = userNewData.LastName

		user, err := database.ActionUser(models.AdminCommandToUser{
			Action: "update",
			User:   dbUser,
		})
		if err != nil {
			c.JSON(500, err)
			return
		}
		selfUser := models.LoggedInUser{
			User:        user.Login,
			FirstName:   user.FirstName,
			LastName:    user.LastName,
			IsSuperuser: &user.IsSuperUser,
		}
		c.JSON(200, selfUser)
		return

	}

}
