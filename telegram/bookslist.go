package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"gopds-api/database"
	"gopds-api/models"
	"net/http"
	"text/template"
)

type TelegramBook struct {
	EmojiNum     string          `json:"emoji_num"`
	Title        string          `json:"title"`
	Authors      []models.Author `json:"authors"`
	DocDate      string          `json:"doc_date"`
	RegisterDate string          `json:"register_date"`
}

// TelegramBookListTemplate - template for telegram book list
const TelegramBookListTemplate = `{{range $i, $b := .}}{{$b.EmojiNum}}<b>Название:</b> {{$b.Title}}
<b>Авторы:</b> {{ range $j, $a := $b.Authors }}{{if $j}}, {{end}}{{$a.FullName}}{{end}} 
<b>Дата документа:</b> {{$b.DocDate}}
<b>Дата добавления</b> {{$b.RegisterDate}}

{{end}}`

// DefaultNumToEmoji Convert number to emoji
func DefaultNumToEmoji(num int) string {
	switch num {
	case 1:
		return "1️⃣"
	case 2:
		return "2️⃣"
	case 3:
		return "3️⃣"
	case 4:
		return "4️⃣"
	case 5:
		return "5️⃣"
	case 6:
		return "6️⃣"
	case 7:
		return "7️⃣"
	case 8:
		return "8️⃣"
	case 9:
		return "9️⃣"
	case 10:
		return "🔟"
	default:
		return ""
	}
}

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

// CallbackMessage CallbackQuery represents an incoming callback query from a callback button in an inline keyboard.
type CallbackMessage struct {
	UpdateID      int `json:"update_id"`
	CallbackQuery struct {
		ID   string `json:"id"`
		From struct {
			LastName  string `json:"last_name"`
			Type      string `json:"type"`
			ID        int    `json:"id"`
			FirstName string `json:"first_name"`
			Username  string `json:"username"`
		} `json:"from"`
		Data            string `json:"data"`
		InlineMessageID string `json:"inline_message_id"`
	} `json:"callback_query"`
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
	Text            string `json:"text"`
	ParseMode       string `json:"parse_mode"`
	MessageID       int    `json:"message_id,omitempty"`
	InlineMessageID string `json:"inline_message_id,omitempty"`
}

// MessageConfig Message represents a message.
type MessageConfig struct {
	BaseChat              `json:"base_chat"`
	Text                  string `json:"text"`
	ParseMode             string `json:"parse_mode"`
	DisableWebPagePreview bool   `json:"disable_web_page_preview"`
}

// TelegramCommand returns a list of books. It is used to display a list of books in the telegram bot.
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

// TelegramPages returns a page of books. It is used to display a page of books in the telegram bot.
type TelegramPages struct {
	Prev int
	Next int
}

func BothPages(filters models.BookFilters, totalCount int) []InlineKeyboardButton {
	var booksPages []InlineKeyboardButton
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
	var buttons []InlineKeyboardButton
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

func TgBooksList(user models.User, filters models.BookFilters) (BaseChat, error) {
	m := NewBaseChat(int64(user.TelegramID), "")
	var telegramBooksList []TelegramBook
	books, tc, err := database.GetBooks(user.ID, filters)
	if err != nil {
		return m, err
	}
	for i, b := range books {
		telegramBooksList = append(telegramBooksList, TelegramBook{
			EmojiNum:     DefaultNumToEmoji(i + 1),
			Title:        b.Title,
			Authors:      b.Authors,
			DocDate:      b.DocDate,
			RegisterDate: b.RegisterDate.Format("2006-01-02"),
		})
	}
	// Process the template to fill in the books
	t, err := template.New("books").Parse(TelegramBookListTemplate)
	if err != nil {
		return m, err
	}
	var tpl bytes.Buffer
	err = t.Execute(&tpl, telegramBooksList)
	if err != nil {
		return m, err
	}
	m.Text = tpl.String()
	m.ReplyMarkup = CreateKeyboard(filters, books, tc)
	return m, nil
}
