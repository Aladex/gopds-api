package api

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/models"
	"gopds-api/sessions"
	"gopds-api/utils"
	"net/http"
	"strings"
	"time"
)

// DropAllSessions method for dropping all sessions from Redis
// Auth godoc
// @Summary Drop all sessions from Redis
// @Description Remove all sessions from Redis
// @Tags login
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept  json
// @Produce  json
// @Success 200 {object} models.LoggedInUser
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Router /api/drop-sessions [get]
func DropAllSessions(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	userToken, username := c.Request.Header.Get("Authorization"), c.GetString("username")

	if err := sessions.DeleteSessionKey(ctx, models.LoggedInUser{User: strings.ToLower(username), Token: &userToken}); err != nil {
		httputil.NewError(c, http.StatusForbidden, err)
		return
	}

	go sessions.DropAllSessions(userToken)
	c.JSON(http.StatusOK, gin.H{"result": "ok"})
}

// AuthCheck method for returning a user and token for header
// Auth godoc
// @Summary Return user and token for header
// @Description Login method for token generation
// @Tags login
// @Accept  json
// @Produce  json
// @Param  body body models.LoginRequest true "Login Data"
// @Success 200 {object} models.LoggedInUser
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /api/login [post]
func AuthCheck(c *gin.Context) {
	var user models.LoginRequest
	if err := c.ShouldBindJSON(&user); err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	res, dbUser, err := database.CheckUser(user)
	if err != nil || !dbUser.Active {
		status := http.StatusForbidden
		var errMsg error
		if err != nil {
			errMsg = errors.New("bad_credentials")
		} else {
			errMsg = errors.New("user not active")
		}
		httputil.NewError(c, status, errMsg)
		return
	}

	if !res {
		httputil.NewError(c, http.StatusForbidden, errors.New("bad password"))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

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

	if err := sessions.SetSessionKey(ctx, thisUser); err != nil {
		httputil.NewError(c, http.StatusForbidden, err)
		return
	}

	go database.LoginDateSet(&dbUser)
	c.JSON(200, thisUser)
}

// LogOut method for logging out the user
// Auth godoc
// @Summary Log out user
// @Description Log out the user by invalidating their session
// @Tags login
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept  json
// @Produce  json
// @Success 200 {object} models.LoggedInUser
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Router /api/logout [get]
func LogOut(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	userToken := c.Request.Header.Get("Authorization")
	username := c.GetString("username")
	err := sessions.DeleteSessionKey(ctx, models.LoggedInUser{
		User:  strings.ToLower(username),
		Token: &userToken,
	})

	if err != nil {
		httputil.NewError(c, http.StatusForbidden, err)
		return
	}

	c.JSON(200, gin.H{"result": "ok"})
}

// SelfUser method for retrieving user information by token
// Auth godoc
// @Summary Get user information by token
// @Description Retrieve user information using the provided token
// @Tags login
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept  json
// @Produce  json
// @Success 200 {object} models.LoggedInUser
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Router /api/books/self-user [get]
func SelfUser(c *gin.Context) {
	username := strings.ToLower(c.GetString("username"))
	dbUser, err := database.GetUser(username)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	if hf, err := database.HaveFavs(dbUser.ID); err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
	} else {
		c.JSON(http.StatusOK, models.LoggedInUser{
			User:        dbUser.Login,
			BooksLang:   dbUser.BooksLang,
			IsSuperuser: &dbUser.IsSuperUser,
			FirstName:   dbUser.FirstName,
			LastName:    dbUser.LastName,
			HaveFavs:    &hf,
		})
	}
}

// ChangeUser method for updating user information by token
// Auth godoc
// @Summary Update user information
// @Description Update user information based on the provided token
// @Tags users
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept  json
// @Produce  json
// @Param  body body models.SelfUserChangeRequest true "User update information"
// @Success 200 {object} models.LoggedInUser
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /api/books/change-me [post]
func ChangeUser(c *gin.Context) {
	var userNewData models.SelfUserChangeRequest
	if err := c.ShouldBindJSON(&userNewData); err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	username := c.GetString("username")
	u := models.LoginRequest{Login: username, Password: userNewData.Password}
	result, dbUser, err := database.CheckUser(u)

	if err != nil || !result && len(userNewData.Password) > 0 {
		httputil.NewError(c, http.StatusForbidden, errors.New("auth_error"))
		return
	}

	if len(userNewData.Password) > 0 {
		if len(userNewData.NewPassword) < 8 {
			httputil.NewError(c, http.StatusBadRequest, errors.New("bad_password"))
			return
		}
		if userNewData.NewPassword == userNewData.Password {
			httputil.NewError(c, http.StatusBadRequest, errors.New("same_password"))
			return
		}
	}

	dbUser.FirstName = userNewData.FirstName
	dbUser.LastName = userNewData.LastName
	dbUser.Password = userNewData.NewPassword
	dbUser.BooksLang = userNewData.BooksLang

	if _, err := database.ActionUser(models.AdminCommandToUser{Action: "update", User: dbUser}); err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	if hf, err := database.HaveFavs(dbUser.ID); err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	} else {
		c.JSON(http.StatusOK, models.LoggedInUser{
			User:        dbUser.Login,
			FirstName:   dbUser.FirstName,
			LastName:    dbUser.LastName,
			IsSuperuser: &dbUser.IsSuperUser,
			HaveFavs:    &hf,
		})
	}
}
