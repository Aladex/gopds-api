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

// init Initialize telegram users map and channel
func init() {
	TelegramUsers.Users = make(map[int64]models.User)
	// Create channel for users
	TelegramUsers.UserChannel = make(chan models.User)
	// Create goroutine for getting users from channel
	go GetUserFromChannel(TelegramUsers.UserChannel)
}

// TelegramUsers Telegram users map
var TelegramUsers = TgUsers{
	Users: make(map[int64]models.User),
}

// TgUsers struct for storing users
type TgUsers struct {
	Users map[int64]models.User
	// mutex for protecting Users
	Mu          sync.Mutex
	UserChannel chan models.User
}

// GetUserFromChannel Get user from channel and send message to telegram
func GetUserFromChannel(channel chan models.User) {
	// Check if something in channel
	for {
		select {
		case user := <-channel:
			UpdateTgUser(&user)
			// If page is 0, send message with keyboard
			if user.TelegramRequest.Page == 0 {
				baseChat, err := telegram.TgSearchType(user)
				if err != nil {
					logging.CustomLog.Println(err)
					continue
				}
				go telegram.SendCommand(user.BotToken, baseChat)
			} else {
				// Send message without keyboard
				// Create book filters
				bookFilters := CreateBookFiltersFromMessage(user)

				// Create tg message from book filters
				tgMessage, err := telegram.TgBooksList(user, bookFilters)
				if err != nil {
					logging.CustomLog.Println(err)
					continue
				}
				go telegram.SendCommand(user.BotToken, tgMessage)
			}
		}
	}
}

// DefaultApiErrorHandler Default error handler
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
			user.TelegramRequest.Page = 0
			user.TelegramRequest.Request = ""
			if err != nil {
				DefaultApiErrorHandler(c, err)
				return
			}
			// Send user to channel
			TelegramUsers.UserChannel <- user
			// Send response to API user
			c.JSON(http.StatusOK, gin.H{
				"message": "ok",
			})
		default:
			user.TelegramRequest.Request = telegramMessage.(telegram.TelegramCommand).Message.Text
			user.TelegramRequest.Page = 0
			// Send user to channel
			TelegramUsers.UserChannel <- user
			c.JSON(http.StatusOK, gin.H{
				"message": "ok",
			})
		}
	case telegram.CallbackMessage:
		// if message is callback
		switch telegramMessage.(telegram.CallbackMessage).CallbackQuery.Data {
		case "next":
			tgUser := TelegramUsers.Users[int64(user.TelegramID)]
			tgUser.TelegramRequest.Page++
			// Send user to channel
			TelegramUsers.UserChannel <- tgUser
			c.JSON(http.StatusOK, gin.H{
				"message": "ok",
			})
		case "prev":
			tgUser := TelegramUsers.Users[int64(user.TelegramID)]
			tgUser.TelegramRequest.Page--
			// Send user to channel
			TelegramUsers.UserChannel <- tgUser
			c.JSON(http.StatusOK, gin.H{
				"message": "ok",
			})
		case "search_by_title":
			// Get last request from user
			user.TelegramRequest.Request = TelegramUsers.Users[int64(user.TelegramID)].TelegramRequest.Request
			user.TelegramRequest.Page = 1
			// Send user to channel
			TelegramUsers.UserChannel <- user
			c.JSON(http.StatusOK, gin.H{
				"message": "ok",
			})
		case "search_by_author":
			user.TelegramRequest.Page = 1
			tgMessage, err = telegram.TgAuthorsList(user, CreateAuthorFiltersFromMessage(user))
			if err != nil {
				DefaultApiErrorHandler(c, err)
				return
			}
			go telegram.SendCommand(user.BotToken, tgMessage)
			c.JSON(http.StatusOK, gin.H{
				"message": "ok",
			})
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
