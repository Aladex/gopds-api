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
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// bookIdRegex Regex for book id
const bookIdRegex = `^get_book_(\d+)`

// bookFormatDownloadRegex Regex for book format download
const bookFormatDownloadRegex = `^download_book_fb2|epub|zip|mobi_(\d+)`

// Compile regex for book id
var bookIdRegexCompiled = regexp.MustCompile(bookIdRegex)

// Compile regex for book format download
var bookFormatDownloadRegexCompiled = regexp.MustCompile(bookFormatDownloadRegex)

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
			// switch by type of request
			switch user.TelegramRequest.MessageType {
			case "message":
				baseChat, err := telegram.TgSearchType(user)
				if err != nil {
					logging.CustomLog.Println(err)
					continue
				}
				go func() {
					err = telegram.SendCommand(user.BotToken, baseChat)
					if err != nil {
						logging.CustomLog.Println(err)
					}
				}()
			case "callback":
				switch user.TelegramRequest.RequestType {
				case "author":
					authorFilters := CreateAuthorFiltersFromMessage(user)

					// Get authors and send to telegram
					go func() {
						err := telegram.TgAuthorsList(user, authorFilters)
						if err != nil {
							logging.CustomLog.Println(err)
						}
					}()
				case "book":
					bookFilters := CreateBookFiltersFromMessage(user)

					// Get books and send to telegram
					go func() {
						err := telegram.TgBooksList(user, bookFilters)
						if err != nil {
							logging.CustomLog.Println(err)
						}
					}()
				case "get_book":
					// Get book and send to telegram full book info with download buttons
					book, err := database.GetBook(user.TelegramRequest.BookID)
					if err != nil {
						logging.CustomLog.Println(err)
						continue
					}
					go func() {
						tgBook, err := telegram.TgBook(&book)
						if err != nil {
							logging.CustomLog.Println(err)
						}
						m := telegram.NewBaseChat(int64(user.TelegramID), "")
						m.Text = tgBook
						// Create keyboard with formats of book
						m.ReplyMarkup = telegram.CreateBookFileFormatMarkup(&book)

						err = telegram.SendCommand(user.BotToken, m)
						if err != nil {
							logging.CustomLog.Println(err)
						}
						// Set user to default state
						user.TelegramRequest = models.UserTelegramRequest{}
						UpdateTgUser(&user)
					}()
				}
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
		user.TelegramRequest.Page = 0
		messageText := telegramMessage.(telegram.TelegramCommand).Message.Text
		if messageText == "/start" {
			user.TelegramRequest.Request = ""
		} else {
			user.TelegramRequest.Request = messageText
		}
		user.TelegramRequest.MessageType = "message"
		// Send user to channel
		TelegramUsers.UserChannel <- user
		// Send response to API user
		c.JSON(http.StatusOK, gin.H{
			"message": "ok",
		})
	case telegram.CallbackMessage:
		// if message is callback
		// Set type of message to callback
		tgUser := TelegramUsers.Users[int64(user.TelegramID)]
		tgUser.TelegramRequest.MessageType = "callback"
		callbackData := telegramMessage.(telegram.CallbackMessage).CallbackQuery.Data
		// Check regex for book id
		isBookID := bookIdRegexCompiled.MatchString(callbackData)
		isDownload := bookFormatDownloadRegexCompiled.MatchString(callbackData)

		switch callbackData {
		case "next":
			tgUser.TelegramRequest.Page++
			// Send user to channel
			TelegramUsers.UserChannel <- tgUser
			c.JSON(http.StatusOK, gin.H{
				"message": "ok",
			})
		case "prev":
			tgUser.TelegramRequest.Page--
			// Send user to channel
			TelegramUsers.UserChannel <- tgUser
			c.JSON(http.StatusOK, gin.H{
				"message": "ok",
			})
		case "search_by_title":
			// Send user to channel
			tgUser.TelegramRequest.Page = 1
			tgUser.TelegramRequest.RequestType = "book"
			TelegramUsers.UserChannel <- tgUser
			c.JSON(http.StatusOK, gin.H{
				"message": "ok",
			})
		case "search_by_author":
			tgUser.TelegramRequest.Page = 1
			tgUser.TelegramRequest.RequestType = "author"
			TelegramUsers.UserChannel <- tgUser
			c.JSON(http.StatusOK, gin.H{
				"message": "ok",
			})
		default:
			if isBookID {
				// Unmarshal book id
				bookID, err := strconv.Atoi(strings.Split(callbackData, "_")[2])
				if err != nil {
					DefaultApiErrorHandler(c, err)
					return
				}
				// Get book from database by converted book id
				book, err := database.GetBook(int64(bookID))
				if err != nil {
					DefaultApiErrorHandler(c, err)
					return
				}
				// Send book to user
				tgUser.TelegramRequest.Page = 0
				tgUser.TelegramRequest.Request = ""
				tgUser.TelegramRequest.RequestType = "get_book"
				tgUser.TelegramRequest.BookID = book.ID
				TelegramUsers.UserChannel <- tgUser
				c.JSON(http.StatusOK, gin.H{
					"message": "ok",
				})
			} else if isDownload {
				// Get file format from callback data
				fileFormat := strings.Split(callbackData, "_")[2]
				// Get book from database by converted book id
				bookID := strings.Split(callbackData, "_")[3]
				// convert book id to int64
				bookIDInt64, err := strconv.ParseInt(bookID, 10, 64)

				book, err := database.GetBook(bookIDInt64)
				if err != nil {
					DefaultApiErrorHandler(c, err)
					return
				}
				err = telegram.SendBookFile(fileFormat, user, book)
				if err != nil {
					DefaultApiErrorHandler(c, err)
					return
				}
			}
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
