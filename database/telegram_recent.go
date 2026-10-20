package database

import (
	"context"
	"time"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// Recent sends (plan.md P1-3): the books Telegram confirmed it delivered to
// an account's chat, newest first, one row per book.
const (
	// TelegramRecentLimit is how many books the list keeps.
	TelegramRecentLimit = 50
	// TelegramRecentAge is how long a send stays in it.
	TelegramRecentAge = 90 * 24 * time.Hour
)

// recordRecentInTx puts a confirmed send at the top of the account's recent
// sends, inside the caller's transaction — unless the account keeps none.
func recordRecentInTx(ctx context.Context, tx *pg.Tx, userID, bookID int64, format string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO telegram_recent (user_id, book_id, format, sent_at)
		SELECT ?0, ?1, ?2, now()
		WHERE NOT EXISTS (SELECT 1 FROM telegram_user_settings WHERE user_id = ?0 AND NOT keep_recent)
		ON CONFLICT (user_id, book_id) DO UPDATE SET format = EXCLUDED.format, sent_at = EXCLUDED.sent_at`,
		userID, bookID, format)
	return err
}

// ListTelegramRecent is one page of the account's recent sends, newest
// first, within the list's limits; and how many there are.
func ListTelegramRecent(ctx context.Context, userID int64, offset, limit int) ([]models.TelegramRecent, int, error) {
	var total int
	if _, err := db.QueryOneContext(ctx, pg.Scan(&total), `
		SELECT least(count(*), ?) FROM telegram_recent
		WHERE user_id = ? AND sent_at > now() - make_interval(secs => ?)`,
		TelegramRecentLimit, userID, TelegramRecentAge.Seconds()); err != nil {
		return nil, 0, err
	}
	if offset >= total {
		return nil, total, nil
	}
	limit = min(limit, total-offset)
	var recent []models.TelegramRecent
	_, err := db.QueryContext(ctx, &recent, `
		SELECT user_id, book_id, format, sent_at FROM telegram_recent
		WHERE user_id = ? AND sent_at > now() - make_interval(secs => ?)
		ORDER BY sent_at DESC, book_id DESC OFFSET ? LIMIT ?`,
		userID, TelegramRecentAge.Seconds(), offset, limit)
	return recent, total, err
}

// ClearTelegramRecent empties the account's recent sends. The documents in
// the chat stay where they are.
func ClearTelegramRecent(ctx context.Context, userID int64) error {
	return db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if err := guardInboxLease(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM telegram_recent WHERE user_id = ?`, userID)
		return err
	})
}

// SetTelegramKeepRecent turns the keeping of recent sends on or off. Off
// keeps the list as it is: clearing it is a separate choice.
func SetTelegramKeepRecent(ctx context.Context, userID int64, keep bool) error {
	return db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if err := guardInboxLease(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO telegram_user_settings (user_id, keep_recent) VALUES (?, ?)
			ON CONFLICT (user_id) DO UPDATE SET keep_recent = EXCLUDED.keep_recent, updated_at = now()`, userID, keep)
		return err
	})
}

// PruneTelegramRecent is the janitor's part: sends older than the list's age,
// and every account's sends past its newest TelegramRecentLimit, go.
func PruneTelegramRecent(ctx context.Context) error {
	_, err := db.ExecContext(ctx, `
		DELETE FROM telegram_recent r
		WHERE r.sent_at < now() - make_interval(secs => ?)
		   OR (r.user_id, r.book_id) IN (
			SELECT user_id, book_id FROM (
				SELECT user_id, book_id,
				       row_number() OVER (PARTITION BY user_id ORDER BY sent_at DESC, book_id DESC) AS n
				FROM telegram_recent) ranked
			WHERE n > ?)`, TelegramRecentAge.Seconds(), TelegramRecentLimit)
	return err
}
