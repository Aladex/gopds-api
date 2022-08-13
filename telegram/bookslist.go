package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"gopds-api/database"
	"gopds-api/models"
	"net/http"
	"strings"
)

func NewBaseChat(chatID int64, text string) BaseChat {
	return BaseChat{
		ChatID:    chatID,
		Text:      text,
		ParseMode: "HTML",
	}
}

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
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
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

type TelegramPages struct {
	Prev int
	Next int
}

func BothPages(filters models.BookFilters, totalCount int) []InlineKeyboardButton {
	booksPages := []InlineKeyboardButton{}
	currentPage := (filters.Offset / filters.Limit) + 1
	totalPages := totalCount / filters.Limit

	if filters.Offset >= 10 {
		pp := fmt.Sprintf(`{ "page": %d }`, currentPage-1)
		booksPages = append(booksPages, InlineKeyboardButton{
			Text:         "<<",
			CallbackData: &pp,
		})
	}

	if filters.Offset/5 < totalPages {
		np := fmt.Sprintf(`{ "page": %d }`, currentPage+1)
		booksPages = append(booksPages, InlineKeyboardButton{
			Text:         ">>",
			CallbackData: &np,
		})
	}

	return booksPages
}

func CreateKeyboard(filters models.BookFilters, books []models.Book, tc int) InlineKeyboardMarkup {
	buttons := []InlineKeyboardButton{}
	for i, b := range books {
		callBack := fmt.Sprintf(`{ "book_id": %d }`, b.ID)
		buttons = append(buttons, InlineKeyboardButton{
			Text:         fmt.Sprintf("%d", i+1),
			CallbackData: &callBack,
		})
	}
	booksKeyboard := NewInlineKeyboardRow(buttons...)
	pagesInlineKeyboard := BothPages(filters, tc)
	return NewInlineKeyboardMarkup(booksKeyboard, pagesInlineKeyboard)

}

func TelegramBooksList(user models.User, filters models.BookFilters) (BaseChat, error) {
	m := NewBaseChat(int64(user.TelegramID), "")
	booksTxt := []string{}
	books, tc, err := database.GetBooks(user.ID, filters)
	if err != nil {
		return m, err
	}
	for i, b := range books {
		authors := []string{}
		for _, a := range b.Authors {
			authors = append(authors, a.FullName)
		}
		booksTxt = append(booksTxt, fmt.Sprintf(`%d. <b>Название:</b> %s
<b>Автор:</b> %s
<b>Дата документа:</b> %s
<b>Дата добавления:</b> %s
`, i+1, b.Title, strings.Join(authors, ", "), b.DocDate, b.RegisterDate.Format("2006-01-02")))
	}
	m.Text = strings.Join(booksTxt, "\n")
	m.ReplyMarkup = CreateKeyboard(filters, books, tc)
	return m, nil
}
