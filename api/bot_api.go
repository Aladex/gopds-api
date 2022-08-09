package api

import (
	"errors"
	"github.com/gin-gonic/gin"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/logging"
	"gopds-api/models"
	"net/http"
)

type TelegramCommand struct {
	UpdateID int `json:"update_id"`
	Message  struct {
		Date int `json:"date"`
		Chat struct {
			LastName  string `json:"last_name"`
			ID        int    `json:"id"`
			Type      string `json:"type"`
			FirstName string `json:"first_name"`
			Username  string `json:"username"`
		} `json:"chat"`
		MessageID int `json:"message_id"`
		From      struct {
			LastName  string `json:"last_name"`
			ID        int    `json:"id"`
			FirstName string `json:"first_name"`
			Username  string `json:"username"`
		} `json:"from"`
		Text string `json:"text"`
	} `json:"message"`
}

func TokenApiEndpoint(c *gin.Context) {
	botToken := c.Param("id")
	user, err := database.GetUserByToken(botToken)
	if err != nil {
		c.JSON(500, err)
		return
	}
	var telegramCmd TelegramCommand

	if err := c.ShouldBindJSON(&telegramCmd); err == nil {
		if user.TelegramID == 0 {
			user.TelegramID = telegramCmd.Message.From.ID
			_, err := database.ActionUser(models.AdminCommandToUser{Action: "update", User: user})
			if err != nil {
				logging.CustomLog.Println(err)
				httputil.NewError(c, http.StatusBadRequest, errors.New("bad request"))
				return
			}
		}
	}
}
