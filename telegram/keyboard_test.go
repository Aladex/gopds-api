package telegram

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetMainKeyboard(t *testing.T) {
	keyboard := GetMainKeyboard()

	assert.NotNil(t, keyboard)
	assert.True(t, keyboard.ResizeKeyboard, "Keyboard should be resizable")
	assert.False(t, keyboard.OneTimeKeyboard, "Keyboard should be persistent")
	assert.NotEmpty(t, keyboard.ReplyKeyboard, "Keyboard should have buttons")
}

func TestGetCommandFromButtonText(t *testing.T) {
	tests := []struct {
		name          string
		buttonText    string
		expectedCmd   string
		expectedFound bool
	}{
		{
			name:          "Search button",
			buttonText:    "🔍 Поиск",
			expectedCmd:   "/search",
			expectedFound: true,
		},
		{
			name:          "Favorites button",
			buttonText:    "⭐ Избранное",
			expectedCmd:   "/favorites",
			expectedFound: true,
		},
		{
			name:          "Author button",
			buttonText:    "👤 Автор",
			expectedCmd:   "/a",
			expectedFound: true,
		},
		{
			name:          "Book button",
			buttonText:    "📚 Книга",
			expectedCmd:   "/b",
			expectedFound: true,
		},
		{
			name:          "Unknown button",
			buttonText:    "Unknown",
			expectedCmd:   "",
			expectedFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, found := GetCommandFromButtonText(tt.buttonText)
			assert.Equal(t, tt.expectedFound, found, "Found status should match")
			if found {
				assert.Equal(t, tt.expectedCmd, cmd, "Command should match")
			}
		})
	}
}

func TestRemoveKeyboard(t *testing.T) {
	keyboard := RemoveKeyboard()

	assert.NotNil(t, keyboard)
	assert.True(t, keyboard.RemoveKeyboard, "RemoveKeyboard flag should be true")
}
