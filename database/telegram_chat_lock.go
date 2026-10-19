package database

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"gopds-api/models"
)

// ErrChatBusy means a chat's lock was not free within the wait.
var ErrChatBusy = errors.New("telegram chat busy")

// chatLockPoll is how often a waiter tries a held chat lock again.
const chatLockPoll = 15 * time.Millisecond

// LockTelegramChat takes the chat's transition lock (migration 47) for at
// most hold, waiting at most wait for it. The returned function releases it;
// a holder that never does is outlived by the lock's expiry.
func LockTelegramChat(ctx context.Context, botRef, chatID int64, hold, wait time.Duration) (func(), error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, err
	}
	holder := hex.EncodeToString(token[:])
	deadline := time.Now().Add(wait)
	for {
		res, err := db.ExecContext(ctx, `
			INSERT INTO telegram_chat_lock (bot_ref, chat_id, holder, expires_at)
			VALUES (?, ?, ?, now() + make_interval(secs => ?))
			ON CONFLICT (bot_ref, chat_id) DO UPDATE
			SET holder = EXCLUDED.holder, expires_at = EXCLUDED.expires_at
			WHERE telegram_chat_lock.expires_at < now()`,
			botRef, chatID, holder, hold.Seconds())
		if err != nil {
			return nil, err
		}
		if res.RowsAffected() == 1 {
			return func() {
				_, _ = db.ExecContext(context.WithoutCancel(ctx),
					`DELETE FROM telegram_chat_lock WHERE bot_ref = ? AND chat_id = ? AND holder = ?`, botRef, chatID, holder)
			}, nil
		}
		if time.Now().After(deadline) {
			return nil, ErrChatBusy
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(chatLockPoll):
		}
	}
}

// AddTelegramRepaint records a repaint owed.
func AddTelegramRepaint(ctx context.Context, r *models.TelegramPanelRepaint) error {
	_, err := db.ModelContext(ctx, r).Insert()
	return err
}

// TelegramRepaints are the repaints owed in one chat, oldest first.
func TelegramRepaints(ctx context.Context, botRef, chatID int64) ([]models.TelegramPanelRepaint, error) {
	var out []models.TelegramPanelRepaint
	err := db.ModelContext(ctx, &out).Where("bot_ref = ? AND chat_id = ?", botRef, chatID).Order("id").Select()
	return out, err
}

// DropTelegramRepaint forgets a repaint that ran.
func DropTelegramRepaint(ctx context.Context, id int64) error {
	_, err := db.ExecContext(context.WithoutCancel(ctx), `DELETE FROM telegram_panel_repaint WHERE id = ?`, id)
	return err
}

// TelegramChatRef names one bot's chat.
type TelegramChatRef struct {
	BotRef int64
	ChatID int64
}

// StaleTelegramRepaintChats are the chats with a repaint owed for longer
// than age, and forgets repaints older than drop.
func StaleTelegramRepaintChats(ctx context.Context, age, drop time.Duration) ([]TelegramChatRef, error) {
	if _, err := db.ExecContext(ctx, `DELETE FROM telegram_panel_repaint WHERE created_at < now() - make_interval(secs => ?)`,
		drop.Seconds()); err != nil {
		return nil, err
	}
	var out []TelegramChatRef
	_, err := db.QueryContext(ctx, &out, `
		SELECT DISTINCT bot_ref, chat_id FROM telegram_panel_repaint
		WHERE created_at < now() - make_interval(secs => ?)`, age.Seconds())
	return out, err
}
