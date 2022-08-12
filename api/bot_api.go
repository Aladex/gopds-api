package api

import (
	"errors"
	"github.com/gin-gonic/gin"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/telegram"
	"net/http"
)

func TokenApiEndpoint(c *gin.Context) {
	botToken := c.Param("id")
	user, err := database.GetUserByToken(botToken)
	if err != nil {
		httputil.NewError(c, http.StatusNotFound, errors.New("user_is_not_found"))
		return
	}
	var telegramCmd telegram.TelegramCommand

	if err := c.ShouldBindJSON(&telegramCmd); err == nil {
		if user.TelegramID == 0 {
			user.TelegramID = telegramCmd.Message.From.ID
			user.Password = ""
			_, err := database.ActionUser(models.AdminCommandToUser{Action: "update", User: user})

			if err != nil {
				logging.CustomLog.Println(err)
				httputil.NewError(c, http.StatusBadRequest, errors.New("bad request"))
				return
			}
		}
	}
	tgMessage := telegram.NewBaseChat(int64(user.TelegramID), "")
	if telegramCmd.Message.Text == "/start" {
		tgMessage, err = telegram.TelegramBooksList(user, models.BookFilters{
			Limit:      5,
			Offset:     0,
			Title:      "",
			Author:     0,
			Series:     0,
			Lang:       "",
			Fav:        false,
			UnApproved: false,
		})
		if err != nil {
			logging.CustomLog.Println(err)
			httputil.NewError(c, http.StatusBadRequest, errors.New("bad request"))
			return
		}
	}

	go telegram.SendCommand(user.BotToken, tgMessage)
	c.JSON(200, "Ok")
}
