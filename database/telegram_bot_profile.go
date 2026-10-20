package database

import (
	"context"
	"time"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
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
// lease, under token. It is false when the look was applied already, or
// another setup holds a live claim: either way this one applies nothing.
func ClaimTelegramBotProfile(ctx context.Context, botRef int64, token uuid.UUID, lease time.Duration) (bool, error) {
	res, err := db.ExecContext(ctx, `
		INSERT INTO telegram_bot_profile (bot_ref, claimed_until, claim_token)
		VALUES (?, now() + make_interval(secs => ?), ?)
		ON CONFLICT (bot_ref) DO UPDATE SET claimed_until = EXCLUDED.claimed_until, claim_token = EXCLUDED.claim_token
		WHERE telegram_bot_profile.applied_at IS NULL
		  AND (telegram_bot_profile.claimed_until IS NULL OR telegram_bot_profile.claimed_until < now())`,
		botRef, lease.Seconds(), token)
	if err != nil {
		return false, err
	}
	return res.RowsAffected() == 1, nil
}

// ReleaseTelegramBotProfile gives up the claim of token, whose setup failed:
// the next check tries again. A claim taken over since is not touched.
func ReleaseTelegramBotProfile(ctx context.Context, botRef int64, token uuid.UUID) error {
	_, err := db.ExecContext(ctx, `
		UPDATE telegram_bot_profile SET claimed_until = NULL, claim_token = NULL
		WHERE bot_ref = ? AND claim_token = ? AND applied_at IS NULL`, botRef, token)
	return err
}

// CompleteTelegramBotProfile records the look applied by the setup holding
// token. It is false when the claim is no longer that setup's: it ran out
// and another took it, and only the holder's word counts.
func CompleteTelegramBotProfile(ctx context.Context, botRef int64, token uuid.UUID) (bool, error) {
	res, err := db.ExecContext(ctx, `
		UPDATE telegram_bot_profile SET applied_at = now(), claimed_until = NULL, claim_token = NULL
		WHERE bot_ref = ? AND claim_token = ? AND applied_at IS NULL`, botRef, token)
	if err != nil {
		return false, err
	}
	return res.RowsAffected() == 1, nil
}

// MarkTelegramBotProfiled records that the library's look was applied to the
// bot now, at its owner's request.
func MarkTelegramBotProfiled(ctx context.Context, botRef int64) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO telegram_bot_profile (bot_ref, applied_at) VALUES (?, now())
		ON CONFLICT (bot_ref) DO UPDATE SET applied_at = now(), claimed_until = NULL, claim_token = NULL`, botRef)
	return err
}
