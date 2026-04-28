package telegram

import (
	"testing"

	tgbot "github.com/go-telegram/bot/models"
	"github.com/stretchr/testify/assert"
)

func TestGetMainKeyboard(t *testing.T) {
	keyboard := GetMainKeyboard()

	assert.NotNil(t, keyboard)
	assert.True(t, keyboard.ResizeKeyboard, "Keyboard should be resizable")
	assert.Len(t, keyboard.Keyboard, 3, "Keyboard should have 3 rows")

	assert.Equal(t, "🔍 Поиск", keyboard.Keyboard[0][0].Text)
	assert.Equal(t, "⭐ Избранное", keyboard.Keyboard[0][1].Text)
	assert.Equal(t, "👤 Автор", keyboard.Keyboard[1][0].Text)
	assert.Equal(t, "📚 Книга", keyboard.Keyboard[1][1].Text)
	assert.Equal(t, "📦 Подборки", keyboard.Keyboard[2][0].Text)
	assert.Equal(t, "❤️ Поддержать", keyboard.Keyboard[2][1].Text)
}

func TestGetCommandFromButtonText(t *testing.T) {
	tests := []struct {
		name          string
		buttonText    string
		expectedCmd   string
		expectedFound bool
	}{
		{"Search button", "🔍 Поиск", "/search", true},
		{"Favorites button", "⭐ Избранное", "/favorites", true},
		{"Author button", "👤 Автор", "/a", true},
		{"Book button", "📚 Книга", "/b", true},
		{"Collections button", "📦 Подборки", "/collections", true},
		{"Donate button", "❤️ Поддержать", "/donate", true},
		{"Unknown button", "абракадабра", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, found := GetCommandFromButtonText(tt.buttonText)
			assert.Equal(t, tt.expectedFound, found)
			if found {
				assert.Equal(t, tt.expectedCmd, cmd)
			}
		})
	}
}

func TestRemoveKeyboard(t *testing.T) {
	keyboard := RemoveKeyboard()

	assert.NotNil(t, keyboard)
	_ = keyboard // type is *tgbot.ReplyKeyboardRemove
	var _ *tgbot.ReplyKeyboardRemove = keyboard
}
