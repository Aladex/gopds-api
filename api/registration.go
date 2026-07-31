package api

import (
	"errors"
	"fmt"
	"net/http"

	"gopds-api/database"
	"gopds-api/email"
	"gopds-api/httputil"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/sessions"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// Registration creates a new user
// Auth godoc
// @Summary Create a new user
// @Description Register a new user
// @Tags login
// @Accept  json
// @Produce  json
// @Param  body body models.RegisterRequest true "User Data"
// @Success 201 {object} string "User created successfully"
// @Failure 409 {object} httputil.HTTPError "Conflict - user already exists"
// @Failure 400 {object} httputil.HTTPError "Bad request - invalid input parameters"
// @Router /api/register [post]
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
			// The wording comes from the configured language; only
			// the address and the link are this request's to know.
			URL: fmt.Sprintf("%s/activate/%s",
				viper.GetString("project_url"),
				token,
			),
			Email: newUser.Email,
		}

		go func() {
			err := email.SendActivationEmail(registrationMessage)
			if err != nil {
				logging.Error(err)
			}
		}()

		c.JSON(201, "user_created")
		return
	}
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
}
