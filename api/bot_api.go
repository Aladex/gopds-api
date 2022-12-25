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
	"sync"
)

type TgUsers struct {
	Users map[int64]models.User
	// mutex for protecting Users
	Mu sync.Mutex
}

var TelegramUsers = TgUsers{
	Users: make(map[int64]models.User),
}

func DefaultApiErrorHandler(c *gin.Context, err error) {
	logging.CustomLog.Println(err)
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad request"))
}

type UserRequest struct {
	User          models.User `json:"user"`
	RequestString string      `json:"request_string"`
	LastResponse  string      `json:"last_response"`
}

func PageNumToLimitOffset(pageNum int) (int, int) {
	if pageNum == 0 {
		return 5, 0
	} else {
		return 5, pageNum * 5
	}
}

// CreateBookFiltersFromMessage Create model models.BookFilters from telegram message
func CreateBookFiltersFromMessage(user models.User) models.BookFilters {
	limit, offset := PageNumToLimitOffset(user.TelegramRequest.Page)
	var bookFilters models.BookFilters
	bookFilters.Limit = limit
	bookFilters.Offset = offset
	bookFilters.Title = user.TelegramRequest.Request
	bookFilters.Author = 0
	bookFilters.Series = 0
	bookFilters.Lang = ""
	bookFilters.Fav = false
	bookFilters.UnApproved = false

	return bookFilters
}

// CreateAuthorFiltersFromMessage Create model models.AuthorFilters from telegram message
func CreateAuthorFiltersFromMessage(user models.User) models.AuthorFilters {
	limit, offset := PageNumToLimitOffset(user.TelegramRequest.Page)
	var authorFilters models.AuthorFilters
	authorFilters.Limit = limit
	authorFilters.Offset = offset
	authorFilters.Author = user.TelegramRequest.Request
	return authorFilters
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
			tgMessage, err = telegram.TgBooksList(user, CreateBookFiltersFromMessage(user))
			if err != nil {
				DefaultApiErrorHandler(c, err)
				return
			}
		default:
			user.TelegramRequest.Request = telegramCmd.Message.Text
			user.TelegramRequest.Page = 0
			go func(tgUsers *TgUsers) {
				tgUsers.Mu.Lock()
				defer tgUsers.Mu.Unlock()
				tgUsers.Users[int64(user.TelegramID)] = user
			}(&TelegramUsers)

			if err != nil {
				DefaultApiErrorHandler(c, err)
				return
			}
		}
		// Send message to telegram
		if user.TelegramRequest.Page != 0 {
			go telegram.SendCommand(user.BotToken, tgMessage)
		} else {
			baseChat, err := telegram.TgSearchType(user)
			if err != nil {
				DefaultApiErrorHandler(c, err)
				return
			}
			go telegram.SendCommand(user.BotToken, baseChat)
		}

		if err != nil {
			DefaultApiErrorHandler(c, err)
		}

	} else if err := c.ShouldBindJSON(&telegramCallback); err == nil {
		tgMessage.InlineMessageID = telegramCallback.CallbackQuery.InlineMessageID
	} else {
		httputil.NewError(c, http.StatusBadRequest, errors.New("bad request"))
		return
	}

}
