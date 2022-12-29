package telegram

import (
	"bytes"
	"fmt"
	"gopds-api/database"
	"gopds-api/models"
	"text/template"
)

type TelegramAuthor struct {
	EmojiNum string        `json:"emoji_num"`
	Author   models.Author `json:"author"`
}

// TgAuthorsList returns a list of authors. It is used to display a list of authors in the telegram bot.
func TgAuthorsList(user models.User, filters models.AuthorFilters) error {
	m := NewBaseChat(int64(user.TelegramID), "")
	var telegramAuthorsList []TelegramAuthor
	authors, tc, err := database.GetAuthors(filters)
	if err != nil {
		return err
	}
	for i, a := range authors {
		telegramAuthorsList = append(telegramAuthorsList, TelegramAuthor{
			EmojiNum: DefaultNumToEmoji(i + 1),
			Author:   a,
		})
	}
	// Process the template to fill in the authors
	t, err := template.New("authors").Parse(TelegramAuthorListTemplate)
	if err != nil {
		return err
	}
	var tpl bytes.Buffer
	err = t.Execute(&tpl, telegramAuthorsList)
	if err != nil {
		return err
	}
	m.Text = tpl.String()
	m.ReplyMarkup = CreateAuthorKeyboard(filters, authors, tc)
	err = SendCommand(user.BotToken, m)

	return nil
}

func CreateAuthorKeyboard(filters models.AuthorFilters, authors []models.Author, tc int) InlineKeyboardMarkup {
	var buttons []InlineKeyboardButton
	for i, a := range authors {
		callBack := fmt.Sprintf(`{ "author_id": %d }`, a.ID)
		buttons = append(buttons, InlineKeyboardButton{
			Text:         DefaultNumToEmoji(i + 1),
			CallbackData: &callBack,
		})
	}
	authorsKeyboard := NewInlineKeyboardRow(buttons...)
	pagesInlineKeyboard := AuthorPages(filters, tc)
	return NewInlineKeyboardMarkup(authorsKeyboard, pagesInlineKeyboard)
}

func AuthorPages(filters models.AuthorFilters, totalCount int) []InlineKeyboardButton {
	var buttons []InlineKeyboardButton

	// Get page number from filters limit and offset
	page := filters.Offset / filters.Limit

	// Get total pages
	totalPages := totalCount / filters.Limit

	// If total pages is 0, set it to 1
	if totalPages == 0 {
		totalPages = 1
	}

	// If page is 0, disable prev button
	if page != 0 {
		buttons = append(buttons, NewInlineKeyboardButtonData(DefaultNavigationEmoji("prev"), "prev"))
	}

	// If page is last page, disable next button
	if (totalCount - totalPages*filters.Limit) > 0 {
		buttons = append(buttons, NewInlineKeyboardButtonData(DefaultNavigationEmoji("next"), "next"))
	}
	return buttons
}
