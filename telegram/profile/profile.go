// Package profile is the library's look for a reader's personal bot (plan.md
// P1-2, concept §G.5): its avatar, and the descriptions Telegram shows before
// the first Start. A managed bot gets it when the manager sets it up; a bot
// connected by its token only when its owner asks on the site — it replaces
// the owner's own avatar and descriptions.
package profile

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// avatar is the site's logo, white on the header's dark grey, square, as a
// static profile photo must be: a JPG.
//
//go:embed avatar.jpg
var avatar []byte

// The bot's descriptions. They name no commands: the bot has none.
const (
	// Description is shown in the empty chat before Start.
	Description = "Личный бот библиотеки. Ищи книги по названию и автору, сохраняй избранное и получай файлы здесь. " +
		"Подключение — в профиле на сайте библиотеки."
	// ShortDescription is shown on the bot's profile and in shared links.
	ShortDescription = "Твоя библиотека: поиск книг, избранное и файлы в Telegram."
)

// avatarPart is the multipart name the avatar is uploaded under.
const avatarPart = "avatar"

// Apply gives the bot of client the library's avatar and descriptions, and
// checks Telegram keeps the descriptions by reading them back.
func Apply(ctx context.Context, client *bot.Bot) error {
	if _, err := client.SetMyDescription(ctx, &bot.SetMyDescriptionParams{Description: Description}); err != nil {
		return fmt.Errorf("setting the description: %w", err)
	}
	if _, err := client.SetMyShortDescription(ctx, &bot.SetMyShortDescriptionParams{ShortDescription: ShortDescription}); err != nil {
		return fmt.Errorf("setting the short description: %w", err)
	}
	if _, err := client.SetMyProfilePhoto(ctx, &bot.SetMyProfilePhotoParams{Photo: &models.InputProfilePhotoStatic{
		Photo: "attach://" + avatarPart, MediaAttachment: bytes.NewReader(avatar),
	}}); err != nil {
		return fmt.Errorf("setting the profile photo: %w", err)
	}
	description, err := client.GetMyDescription(ctx, &bot.GetMyDescriptionParams{})
	if err != nil {
		return fmt.Errorf("reading the description back: %w", err)
	}
	short, err := client.GetMyShortDescription(ctx, &bot.GetMyShortDescriptionParams{})
	if err != nil {
		return fmt.Errorf("reading the short description back: %w", err)
	}
	if description.Description != Description || short.ShortDescription != ShortDescription {
		return fmt.Errorf("telegram kept other descriptions")
	}
	return nil
}
