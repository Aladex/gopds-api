package api

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/models"
	"gopds-api/utils"
	"net/http"
)

type passwordToken struct {
	Token    string `json:"token" form:"token" bindings:"required"`
	Password string `json:"password" form:"password"`
}

func CheckPasswordToken(c *gin.Context) {
	var token passwordToken
	if err := c.ShouldBindWith(&token, binding.Query); err == nil {
		if utils.CheckTokenPassword(token.Token) == "" {
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
		username := utils.CheckTokenPassword(token.Token)
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
		c.JSON(200, selfUser)
		return
	}
}
