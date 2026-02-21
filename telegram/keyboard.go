package telegram

import (
	tele "gopkg.in/telebot.v3"
)

// KeyboardButton represents a button text and its command
type KeyboardButton struct {
	Text    string
	Command string
}

var (
	// Main keyboard buttons
	btnSearch    = KeyboardButton{Text: "🔍 Поиск", Command: "/search"}
	btnFavorites = KeyboardButton{Text: "⭐ Избранное", Command: "/favorites"}
	btnAuthor    = KeyboardButton{Text: "👤 Автор", Command: "/a"}
	btnBook      = KeyboardButton{Text: "📚 Книга", Command: "/b"}
	btnDonate    = KeyboardButton{Text: "❤️ Поддержать", Command: "/donate"}
)

// GetMainKeyboard returns the main Reply Keyboard with basic commands
func GetMainKeyboard() *tele.ReplyMarkup {
	keyboard := &tele.ReplyMarkup{
		ResizeKeyboard:  true,
		OneTimeKeyboard: false,
	}

	row1 := keyboard.Row(
		keyboard.Text(btnSearch.Text),
		keyboard.Text(btnFavorites.Text),
	)
	row2 := keyboard.Row(
		keyboard.Text(btnAuthor.Text),
		keyboard.Text(btnBook.Text),
	)
	row3 := keyboard.Row(
		keyboard.Text(btnDonate.Text),
	)

	keyboard.Reply(row1, row2, row3)

	return keyboard
}

// GetCommandFromButtonText returns the command associated with the button text
func GetCommandFromButtonText(text string) (string, bool) {
	buttons := []KeyboardButton{
		btnSearch,
		btnFavorites,
		btnAuthor,
		btnBook,
		btnDonate,
	}

	for _, btn := range buttons {
		if btn.Text == text {
			return btn.Command, true
		}
	}

	return "", false
}

// RemoveKeyboard returns a keyboard markup that removes the keyboard
func RemoveKeyboard() *tele.ReplyMarkup {
	return &tele.ReplyMarkup{
		RemoveKeyboard: true,
	}
}
