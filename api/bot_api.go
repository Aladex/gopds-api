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

func DefaultApiErrorHandler(c *gin.Context, err error) {
	logging.CustomLog.Println(err)
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad request"))
}

type UserRequest struct {
	Username      string `json:"username"`
	RequestString string `json:"request_string"`
	LastResponse  string `json:"last_response"`
}

// CreateBookFiltersFromMessage Create model models.BookFilters from telegram message
func CreateBookFiltersFromMessage(message string) models.BookFilters {
	var bookFilters models.BookFilters
	bookFilters.Limit = 5
	bookFilters.Offset = 0
	bookFilters.Title = message
	bookFilters.Author = 0
	bookFilters.Series = 0
	bookFilters.Lang = ""
	bookFilters.Fav = false
	bookFilters.UnApproved = false

	return bookFilters
}

func TokenApiEndpoint(c *gin.Context) {
	botToken := c.Param("id")
	user, err := database.GetUserByToken(botToken)
	if err != nil {
		httputil.NewError(c, http.StatusNotFound, errors.New("user_is_not_found"))
		return
	}
	var telegramCmd telegram.TelegramCommand
	var telegramCallback telegram.CallbackMessage

	tgMessage := telegram.NewBaseChat(int64(user.TelegramID), "")

	if err := c.ShouldBindJSON(&telegramCmd); err == nil {
		if user.TelegramID == 0 {
			user.TelegramID = telegramCmd.Message.From.ID
			user.Password = ""
			_, err := database.ActionUser(models.AdminCommandToUser{Action: "update", User: user})

			if err != nil {
				DefaultApiErrorHandler(c, err)
			}
		}
		// Case of telegram message and callback
		switch telegramCmd.Message.Text {
		case "/start":
			tgMessage, err = telegram.TgBooksList(user, CreateBookFiltersFromMessage(telegramCmd.Message.Text))
			if err != nil {
				DefaultApiErrorHandler(c, err)
				return
			}
		default:
			tgMessage, err = telegram.TgBooksList(user, CreateBookFiltersFromMessage(telegramCmd.Message.Text))
			if err != nil {
				DefaultApiErrorHandler(c, err)
				return
			}
		}
		// Send message to telegram
		go telegram.SendCommand(user.BotToken, tgMessage)
		if err != nil {
			DefaultApiErrorHandler(c, err)
		}

	} else if err := c.ShouldBindJSON(&telegramCallback); err == nil {
		tgMessage.InlineMessageID = telegramCallback.CallbackQuery.InlineMessageID
	} else {
		httputil.NewError(c, http.StatusBadRequest, errors.New("bad request"))
		return
	}

	//if telegramCmd.Message.Text == "/start" {
	//	tgMessage, err = telegram.TgBooksList(user, models.BookFilters{
	//		Limit:      5,
	//		Offset:     0,
	//		Title:      "",
	//		Author:     0,
	//		Series:     0,
	//		Lang:       "",
	//		Fav:        false,
	//		UnApproved: false,
	//	})
	//	if err != nil {
	//		logging.CustomLog.Println(err)
	//		httputil.NewError(c, http.StatusBadRequest, errors.New("bad request"))
	//		return
	//	}
	//}

}
