package telegram

import (
	tgbot "github.com/go-telegram/bot/models"
)

// KeyboardButton represents a button text and its command
type KeyboardButton struct {
	Text    string
	Command string
}

var (
	btnSearch      = KeyboardButton{Text: "🔍 Поиск", Command: "/search"}
	btnFavorites   = KeyboardButton{Text: "⭐ Избранное", Command: "/favorites"}
	btnAuthor      = KeyboardButton{Text: "👤 Автор", Command: "/a"}
	btnBook        = KeyboardButton{Text: "📚 Книга", Command: "/b"}
	btnCollections = KeyboardButton{Text: "📦 Подборки", Command: "/collections"}
	btnDonate      = KeyboardButton{Text: "❤️ Поддержать", Command: "/donate"}
)

// GetMainKeyboard returns the main Reply Keyboard with basic commands
func GetMainKeyboard() *tgbot.ReplyKeyboardMarkup {
	keyboard := &tgbot.ReplyKeyboardMarkup{
		Keyboard: [][]tgbot.KeyboardButton{
			kbRow(btnSearch, btnFavorites),
			kbRow(btnAuthor, btnBook),
			kbRow(btnCollections, btnDonate),
		},
		ResizeKeyboard: true,
		IsPersistent:   true,
	}
	return keyboard
}

func kbRow(btns ...KeyboardButton) []tgbot.KeyboardButton {
	row := make([]tgbot.KeyboardButton, len(btns))
	for i, b := range btns {
		row[i] = tgbot.KeyboardButton{Text: b.Text}
	}
	return row
}

// GetCommandFromButtonText returns the command associated with the button text
func GetCommandFromButtonText(text string) (string, bool) {
	buttons := []KeyboardButton{
		btnSearch,
		btnFavorites,
		btnAuthor,
		btnBook,
		btnCollections,
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
func RemoveKeyboard() *tgbot.ReplyKeyboardRemove {
	return &tgbot.ReplyKeyboardRemove{
		RemoveKeyboard: true,
	}
}
