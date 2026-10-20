package profile

import (
	"bytes"
	"image/jpeg"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The texts fit Telegram's limits (512 and 120 characters) and name no
// commands: the bot has none.
func TestTheDescriptions(t *testing.T) {
	assert.LessOrEqual(t, utf8.RuneCountInString(Description), 512)
	assert.LessOrEqual(t, utf8.RuneCountInString(ShortDescription), 120)
	for _, text := range []string{Description, ShortDescription} {
		assert.NotContains(t, text, "/", "no slash commands")
		assert.NotEmpty(t, strings.TrimSpace(text))
	}
}

// The avatar is what a static profile photo must be: a square JPG.
func TestTheAvatar(t *testing.T) {
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(avatar))
	require.NoError(t, err)
	assert.Equal(t, cfg.Width, cfg.Height, "square")
	assert.GreaterOrEqual(t, cfg.Width, 512)
}
