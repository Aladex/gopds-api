package api

import (
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"gopds-api/database"
	"gopds-api/email"
	"gopds-api/httputil"
	"gopds-api/models"
	"gopds-api/sessions"
	"log"
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

		resetMessage := email.SendType{
			Title: viper.GetString("email.registration.title"),
			Token: fmt.Sprintf("%s/activate/%s",
				viper.GetString("project_domain"),
				token,
			),
			Button:  viper.GetString("email.registration.button"),
			Message: viper.GetString("email.registration.message"),
			Email:   newUser.Email,
			Subject: viper.GetString("email.registration.subject"),
			Thanks:  viper.GetString("email.registration.thanks"),
		}

		go func() {
			err := email.SendActivationEmail(resetMessage)
			if err != nil {
				log.Println(err)
			}
		}()

		c.JSON(201, "user_created")
		return
	}
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
}
