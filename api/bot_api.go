package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/logging"
	"gopds-api/models"
	"net/http"
)

// NewInlineKeyboardRow creates an inline keyboard row with buttons.
func NewInlineKeyboardRow(buttons ...InlineKeyboardButton) []InlineKeyboardButton {
	var row []InlineKeyboardButton

	row = append(row, buttons...)

	return row
}

// NewInlineKeyboardMarkup creates a new inline keyboard.
func NewInlineKeyboardMarkup(rows ...[]InlineKeyboardButton) InlineKeyboardMarkup {
	var keyboard [][]InlineKeyboardButton

	keyboard = append(keyboard, rows...)

	return InlineKeyboardMarkup{
		InlineKeyboard: keyboard,
	}
}

// NewInlineKeyboardButtonData creates an inline keyboard button with text
// and data for a callback.
func NewInlineKeyboardButtonData(text, data string) InlineKeyboardButton {
	return InlineKeyboardButton{
		Text:         text,
		CallbackData: &data,
	}
}

// InlineKeyboardButton represents one button of an inline keyboard. You must
// use exactly one of the optional fields.
//
// Note that some values are references as even an empty string
// will change behavior.
//
// CallbackGame, if set, MUST be first button in first row.
type InlineKeyboardButton struct {
	// Text label text on the button
	Text         string  `json:"text"`
	CallbackData *string `json:"callback_data,omitempty"`
}

// InlineKeyboardMarkup represents an inline keyboard that appears right next to
// the message it belongs to.
type InlineKeyboardMarkup struct {
	// InlineKeyboard array of button rows, each represented by an Array of
	// InlineKeyboardButton objects
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

type BaseChat struct {
	ChatID int64 `json:"chat_id"` // required
	//ChannelUsername          string `json:"channel_username"`
	//ProtectContent           bool `json:"protect_content"`
	//ReplyToMessageID         int `json:"reply_to_message_id"`
	ReplyMarkup interface{} `json:"reply_markup"`
	//DisableNotification      bool `json:"disable_notification"`
	//AllowSendingWithoutReply bool `json:"allow_sending_without_reply"`
	Text string `json:"text"`
}

// MessageConfig Message represents a message.
type MessageConfig struct {
	BaseChat              `json:"base_chat"`
	Text                  string `json:"text"`
	ParseMode             string `json:"parse_mode"`
	DisableWebPagePreview bool   `json:"disable_web_page_preview"`
}

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

func SendCommand(token string, m BaseChat) {
	b, err := json.Marshal(m)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(string(b))
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(b))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
}

func TokenApiEndpoint(c *gin.Context) {
	botToken := c.Param("id")
	user, err := database.GetUserByToken(botToken)
	if err != nil {
		httputil.NewError(c, http.StatusNotFound, errors.New("user_is_not_found"))
		return
	}
	var telegramCmd TelegramCommand

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

	var numericKeyboard = NewInlineKeyboardMarkup(
		NewInlineKeyboardRow(
			NewInlineKeyboardButtonData("2", "2"),
			NewInlineKeyboardButtonData("3", "3"),
		),
		NewInlineKeyboardRow(
			NewInlineKeyboardButtonData("4", "4"),
			NewInlineKeyboardButtonData("5", "5"),
			NewInlineKeyboardButtonData("6", "6"),
		),
	)
	m := BaseChat{
		ChatID: int64(user.TelegramID),
		//ReplyToMessageID: 0,
		Text:        "test",
		ReplyMarkup: numericKeyboard,
	}
	go SendCommand(user.BotToken, m)
	c.JSON(200, "Ok")
}
