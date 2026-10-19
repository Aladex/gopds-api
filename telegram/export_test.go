package telegram

import (
	"context"
	"errors"

	"gopds-api/database"

	tgbot "github.com/go-telegram/bot/models"
)

// DispatchWithLease hands an update to the served bot of ref as a holder of
// lease would, for tests of what a holder that lost its lease may still do.
func (bm *BotManager) DispatchWithLease(ref int64, update *tgbot.Update, lease database.InboxLease) {
	b, err := bm.botByRef(context.Background(), ref)
	if err != nil || b == nil {
		panic("no served bot")
	}
	bm.handling.Add(1)
	bm.dispatch(context.Background(), b, update, lease)
}

// FailReplyMarkClears makes every per-call reply-mark clear fail, as a
// database hiccup would, until the returned function restores it.
func FailReplyMarkClears() (restore func()) {
	previous := clearReplyMark
	clearReplyMark = func(context.Context, database.InboxLease) error { return errors.New("injected failure") }
	return func() { clearReplyMark = previous }
}
