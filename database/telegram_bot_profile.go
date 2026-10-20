package database

import (
	"context"

	"github.com/go-pg/pg/v10"
)

// TelegramBotProfiled reports whether the library's look was applied to the
// bot.
func TelegramBotProfiled(ctx context.Context, botRef int64) (bool, error) {
	var profiled bool
	_, err := db.QueryOneContext(ctx, pg.Scan(&profiled),
		`SELECT EXISTS (SELECT 1 FROM telegram_bot_profile WHERE bot_ref = ?)`, botRef)
	return profiled, err
}

// MarkTelegramBotProfiled records that the library's look was applied to the
// bot now.
func MarkTelegramBotProfiled(ctx context.Context, botRef int64) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO telegram_bot_profile (bot_ref) VALUES (?)
		ON CONFLICT (bot_ref) DO UPDATE SET applied_at = now()`, botRef)
	return err
}
