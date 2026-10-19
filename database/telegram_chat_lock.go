package database

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
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
