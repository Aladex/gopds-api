package telegram

import (
	"context"

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
