package api

import (
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/spf13/viper"
	"gopds-api/database"
	"gopds-api/email"
	"gopds-api/httputil"
	"gopds-api/models"
	"gopds-api/sessions"
	"log"
	"net/http"
)

type passwordToken struct {
	Token    string `json:"token" form:"token" bindings:"required"`
	Password string `json:"password" form:"password"`
}

func CheckPasswordToken(c *gin.Context) {
	var token passwordToken
	if err := c.ShouldBindWith(&token, binding.Query); err == nil {
		if sessions.CheckTokenPassword(token.Token) == "" {
			httputil.NewError(c, http.StatusNotFound, errors.New("invalid_token"))
			return
		}
		c.JSON(200, "valid_token")
	}
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
}

func ChangeUserState(c *gin.Context) {
	var token passwordToken
	if err := c.ShouldBindJSON(&token); err == nil {
		username := sessions.CheckTokenPassword(token.Token)
		if username == "" {
			httputil.NewError(c, http.StatusNotFound, errors.New("invalid_token"))
			return
		}
		u := models.LoginRequest{
			Login:    username,
			Password: token.Password,
		}
		_, dbUser, err := database.CheckUser(u)
		if err != nil {
			httputil.NewError(c, http.StatusBadRequest, errors.New("invalid_user"))
			return
		}

		if !dbUser.Active {
			dbUser.Password = ""
		} else {
			if len(token.Password) < 8 {
				httputil.NewError(c, http.StatusBadRequest, errors.New("bad_password"))
				return
			}
			dbUser.Password = token.Password
		}
		dbUser.Active = true

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

		resetMessage := email.SendType{
			Title: viper.GetString("email.reset.title"),
			Token: fmt.Sprintf("%s/change-password/%s",
				viper.GetString("project_domain"),
				token.Token,
			),
			Button:  viper.GetString("email.reset.button"),
			Message: viper.GetString("email.reset.message"),
			Email:   dbUser.Email,
			Subject: viper.GetString("email.reset.subject"),
			Thanks:  viper.GetString("email.reset.thanks"),
		}

		go func() {
			err := email.SendActivationEmail(resetMessage)
			if err != nil {
				log.Println(err)
			}
		}()

		c.JSON(200, selfUser)
		return
	}
}
