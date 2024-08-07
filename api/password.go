package api

import (
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"gopds-api/database"
	"gopds-api/email"
	"gopds-api/httputil"
	"gopds-api/models"
	"gopds-api/sessions"
	"net/http"
)

type changeAnswer struct {
	Message string `json:"message"`
}

type passwordToken struct {
	Token    string `json:"token" form:"token" bindings:"required"`
	Password string `json:"password" form:"password"`
}

type passwordChangeRequest struct {
	Email string `json:"email" form:"email" bindings:"required"`
}

// TokenValidation check for token validation
// Auth godoc
// @Summary Check for token validation
// @Description Validate the provided token
// @Tags login
// @Accept  json
// @Produce  json
// @Param  body body passwordToken true "Token Info"
// @Success 200 {object} string
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /api/token [post]
func TokenValidation(c *gin.Context) {
	var token passwordToken
	if err := c.ShouldBindJSON(&token); err == nil {
		username := sessions.CheckTokenPassword(token.Token)
		if username == "" {
			httputil.NewError(c, http.StatusNotFound, errors.New("invalid_token"))
			return
		}
		c.JSON(200, "valid_token")
		return
	}
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
}

// ChangeRequest method for initiating a change request
// Auth godoc
// @Summary Initiate a change request
// @Description Initiate a change request for password reset
// @Tags login
// @Accept  json
// @Produce  json
// @Param  body body passwordChangeRequest true "User info"
// @Success 200 {object} changeAnswer
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /api/change-request [post]
func ChangeRequest(c *gin.Context) {
	var changeRequest passwordChangeRequest
	if err := c.ShouldBindJSON(&changeRequest); err == nil {
		dbUser, err := database.UserObject(changeRequest.Email)
		if err != nil {
			httputil.NewError(c, http.StatusBadRequest, errors.New("invalid_user"))
			return
		}
		token := sessions.GenerateTokenPassword(dbUser.Login)

		registrationMessage := email.SendType{
			Title: viper.GetString("email.messages.reset.title"),
			Token: fmt.Sprintf("%s/change-password/%s",
				viper.GetString("project_domain"),
				token,
			),
			Button:  viper.GetString("email.messages.reset.button"),
			Message: viper.GetString("email.messages.reset.message"),
			Email:   dbUser.Email,
			Subject: viper.GetString("email.messages.reset.subject"),
			Thanks:  viper.GetString("email.messages.reset.thanks"),
		}

		go func() {
			err := email.SendActivationEmail(registrationMessage)
			if err != nil {
				logrus.Println(err)
			}
		}()

		c.JSON(200, changeAnswer{
			"token_created",
		})
		return
	}
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
}

// ChangeUserState method for changing user state
// Auth godoc
// @Summary Change user state
// @Description Change user state based on the provided token and password
// @Tags login
// @Accept  json
// @Produce  json
// @Param  body body passwordToken true "User info"
// @Success 200 {object} models.LoggedInUser
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /api/change-password [post]
func ChangeUserState(c *gin.Context) {
	var token passwordToken
	if err := c.ShouldBindJSON(&token); err == nil {
		username := sessions.CheckTokenPassword(token.Token)
		if username == "" {
			httputil.NewError(c, http.StatusNotFound, errors.New("invalid_token"))
			return
		}

		dbUser, err := database.UserObject(username)
		if err != nil {
			httputil.NewError(c, http.StatusBadRequest, errors.New("invalid_user"))
			return
		}

		// Check password length
		if len(token.Password) > 0 && len(token.Password) < 8 {
			httputil.NewError(c, http.StatusBadRequest, errors.New("bad_password"))
			return
		}

		// Update password and activate user
		dbUser.Password = token.Password
		dbUser.Active = true

		// Update user in database
		updatedUser, err := database.ActionUser(models.AdminCommandToUser{
			Action: "update",
			User:   dbUser,
		})
		if err != nil {
			logrus.Printf("failed to update user: %v", err)
			c.JSON(500, err)
			return
		}

		// Check if user is active
		if !updatedUser.Active {
			httputil.NewError(c, http.StatusInternalServerError, errors.New("user_not_activated"))
			return
		}

		selfUser := models.LoggedInUser{
			User:        updatedUser.Login,
			FirstName:   updatedUser.FirstName,
			LastName:    updatedUser.LastName,
			IsSuperuser: &updatedUser.IsSuperUser,
		}

		go sessions.DeleteTokenPassword(token.Token)

		c.JSON(200, selfUser)
		return
	}
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
}
