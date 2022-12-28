package api

import (
	"encoding/json"
	"errors"
	"fmt"
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
		return 5, pageNum*5 - 5
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

func UpdateTgUser(user *models.User) {
	TelegramUsers.Mu.Lock()
	TelegramUsers.Users[int64(user.TelegramID)] = *user
	TelegramUsers.Mu.Unlock()
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

	tgMessage := telegram.NewBaseChat(int64(user.TelegramID), "")

	// get message from request
	message, err := c.GetRawData()
	if err != nil {
		DefaultApiErrorHandler(c, err)
		return
	}
	// unmarshal message
	telegramMessage, err := UnmarshalTelegramMessage(message)
	if err != nil {
		DefaultApiErrorHandler(c, err)
		return
	}
	// get type of message
	switch telegramMessage.(type) {
	case telegram.TelegramCommand:
		// if message is command
		switch telegramMessage.(telegram.TelegramCommand).Message.Text {
		case "/start":
			// Send first 5 books
			tgMessage, err = telegram.TgBooksList(user, CreateBookFiltersFromMessage(user))
			user.TelegramRequest.Page = 0
			user.TelegramRequest.Request = ""
			UpdateTgUser(&user)
			if err != nil {
				DefaultApiErrorHandler(c, err)
				return
			}
		default:
			user.TelegramRequest.Request = telegramMessage.(telegram.TelegramCommand).Message.Text
			user.TelegramRequest.Page = 0
			UpdateTgUser(&user)

			// Answer to user that the type of search string
			tgMessage, err = telegram.TgSearchType(user)

			if err != nil {
				DefaultApiErrorHandler(c, err)
				return
			}

			go telegram.SendCommand(user.BotToken, tgMessage)
		}
		if user.TelegramRequest.Page == 0 {
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
	case telegram.CallbackMessage:
		// if message is callback
		switch telegramMessage.(telegram.CallbackMessage).CallbackQuery.Data {
		case "next":
			tgUser := TelegramUsers.Users[int64(user.TelegramID)]
			tgUser.TelegramRequest.Page++
			UpdateTgUser(&user)
			filters := CreateBookFiltersFromMessage(tgUser)
			tgMessage, err = telegram.TgBooksList(tgUser, filters)
			if err != nil {
				DefaultApiErrorHandler(c, err)
				return
			}
			go telegram.SendCommand(user.BotToken, tgMessage)

		case "prev":
			tgUser := TelegramUsers.Users[int64(user.TelegramID)]
			tgUser.TelegramRequest.Page--
			UpdateTgUser(&user)
			filters := CreateBookFiltersFromMessage(tgUser)
			tgMessage, err = telegram.TgBooksList(tgUser, filters)
			if err != nil {
				DefaultApiErrorHandler(c, err)
				return
			}
			go telegram.SendCommand(user.BotToken, tgMessage)
		case "search_by_title":
			// Get last request from user
			user.TelegramRequest.Request = TelegramUsers.Users[int64(user.TelegramID)].TelegramRequest.Request
			user.TelegramRequest.Page = 1
			UpdateTgUser(&user)
			// Fins user from TgUsers

			tgUser := TelegramUsers.Users[int64(user.TelegramID)]

			tgMessage, err = telegram.TgBooksList(user, CreateBookFiltersFromMessage(tgUser))
			if err != nil {
				DefaultApiErrorHandler(c, err)
				return
			}
			go telegram.SendCommand(user.BotToken, tgMessage)
		case "search_by_author":
			user.TelegramRequest.Page = 1
			tgMessage, err = telegram.TgAuthorsList(user, CreateAuthorFiltersFromMessage(user))
			if err != nil {
				DefaultApiErrorHandler(c, err)
				return
			}
			go telegram.SendCommand(user.BotToken, tgMessage)
		}
	default:
		httputil.NewError(c, http.StatusBadRequest, errors.New("bad request"))
		return
	}
}

// UnmarshalTelegramMessage Unmarshal and get type of telegram message
func UnmarshalTelegramMessage(message []byte) (interface{}, error) {
	var telegramCmd telegram.TelegramCommand
	var telegramCallback telegram.CallbackMessage
	if err := json.Unmarshal(message, &telegramCmd); err == nil && telegramCmd.Message.MessageID != 0 {
		return telegramCmd, nil
	} else if err = json.Unmarshal(message, &telegramCallback); err == nil && telegramCallback.CallbackQuery.Id != "" {
		return telegramCallback, nil
	} else {
		return nil, errors.New(fmt.Sprintf("can't unmarshal message: %s", err))
	}
}
