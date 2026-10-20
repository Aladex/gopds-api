package database

import (
	"context"
	"time"

	"github.com/go-pg/pg/v10"
)

// TelegramBotProfiled reports whether the library's look was applied to the
// bot.
func TelegramBotProfiled(ctx context.Context, botRef int64) (bool, error) {
	var profiled bool
	_, err := db.QueryOneContext(ctx, pg.Scan(&profiled),
		`SELECT EXISTS (SELECT 1 FROM telegram_bot_profile WHERE bot_ref = ? AND applied_at IS NOT NULL)`, botRef)
	return profiled, err
}

// ClaimTelegramBotProfile claims the automatic setup of the bot's look for
// lease. It is false when the look was applied already, or another setup
// holds the claim: either way this one applies nothing.
func ClaimTelegramBotProfile(ctx context.Context, botRef int64, lease time.Duration) (bool, error) {
	res, err := db.ExecContext(ctx, `
		INSERT INTO telegram_bot_profile (bot_ref, claimed_until) VALUES (?, now() + make_interval(secs => ?))
		ON CONFLICT (bot_ref) DO UPDATE SET claimed_until = EXCLUDED.claimed_until
		WHERE telegram_bot_profile.applied_at IS NULL
		  AND (telegram_bot_profile.claimed_until IS NULL OR telegram_bot_profile.claimed_until < now())`,
		botRef, lease.Seconds())
	if err != nil {
		return false, err
	}
	return res.RowsAffected() == 1, nil
}

// ReleaseTelegramBotProfile gives up a claim whose setup failed: the next
// check tries again.
func ReleaseTelegramBotProfile(ctx context.Context, botRef int64) error {
	_, err := db.ExecContext(ctx, `
		UPDATE telegram_bot_profile SET claimed_until = NULL WHERE bot_ref = ? AND applied_at IS NULL`, botRef)
	return err
}

// MarkTelegramBotProfiled records that the library's look was applied to the
// bot now.
func MarkTelegramBotProfiled(ctx context.Context, botRef int64) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO telegram_bot_profile (bot_ref, applied_at) VALUES (?, now())
		ON CONFLICT (bot_ref) DO UPDATE SET applied_at = now(), claimed_until = NULL`, botRef)
	return err
}
