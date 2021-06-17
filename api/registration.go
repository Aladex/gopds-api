package api

import (
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"gopds-api/config"
	"gopds-api/database"
	"gopds-api/email"
	"gopds-api/httputil"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/sessions"
	"net/http"
)

// Registration creates a new user
// Auth godoc
// @Summary creates a new user
// @Description creates a new user
// @Tags login
// @Accept  json
// @Produce  json
// @Param  body body models.RegisterRequest true "User Data"
// @Success 201 {object} string
// @Failure 409 {object} httputil.HTTPError
// @Failure 400 {object} httputil.HTTPError
// @Router /register [post]
func Registration(c *gin.Context) {
	var newUser models.RegisterRequest
	if err := c.ShouldBindJSON(&newUser); err == nil {
		if !newUser.CheckValues() {
			httputil.NewError(c, http.StatusBadRequest, errors.New("bad_form"))
			return
		}

		_, err := database.CheckInvite(newUser.Invite)
		if err != nil {
			httputil.NewError(c, http.StatusBadRequest, errors.New("bad_invite"))
			return
		}

		err = database.CreateUser(newUser)
		if err != nil {
			httputil.NewError(c, http.StatusConflict, errors.New("user_exists"))
			return
		}

		token := sessions.GenerateTokenPassword(newUser.Login)

		registrationMessage := email.SendType{
			Title: config.AppConfig.GetString("email.messages.registration.title"),
			Token: fmt.Sprintf("%s/activate/%s",
				config.AppConfig.GetString("project_domain"),
				token,
			),
			Button:  config.AppConfig.GetString("email.messages.registration.button"),
			Message: config.AppConfig.GetString("email.messages.registration.message"),
			Email:   newUser.Email,
			Subject: config.AppConfig.GetString("email.messages.registration.subject"),
			Thanks:  config.AppConfig.GetString("email.messages.registration.thanks"),
		}

		go func() {
			err := email.SendActivationEmail(registrationMessage)
			if err != nil {
				logging.CustomLog.Println(err)
			}
		}()

		c.JSON(201, "user_created")
		return
	}
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
}
