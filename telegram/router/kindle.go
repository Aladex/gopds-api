package router

import (
	"context"
	"strings"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/services/kindlesend"
	"gopds-api/telegram/cbv2"
	"gopds-api/telegram/screens"
)

// Send to Kindle from the card (plan.md P1-8, decisions Q1): «Kindle» on the
// card while the account has a Kindle address, a confirmation in the same
// panel, then the send's progress and outcome there too. The send is the
// site's (services/kindlesend): the same queue and limits, and one claim per
// reader and book whichever of them asked.

// kindleStatuses are the status screen's pages: progress, then the outcomes.
// Stored in nodes: append only.
var kindleStatuses = []string{
	screens.KindleSending, "",
	kindlesend.CodeNotConfigured, kindlesend.CodeNotFound, kindlesend.CodeTooLarge,
	kindlesend.CodeConversionFailed, kindlesend.CodeSendFailed, kindlesend.CodeInProgress, kindlesend.CodeBusy,
	screens.KindleUnavailable,
}

// kindlePage is a status's page.
func kindlePage(code string) int32 {
	for i, c := range kindleStatuses {
		if c == code {
			return int32(i) // #nosec G115 -- a short list
		}
	}
	return kindlePage(kindlesend.CodeSendFailed)
}

// kindleSetupURL is the site's Kindle setup, "" without a site.
func (r *Router) kindleSetupURL() string {
	if r.cfg.SiteURL == "" {
		return ""
	}
	return strings.TrimRight(r.cfg.SiteURL, "/") + "/?profile=kindle"
}

// kindleAddress is the account's Kindle address, "" when none is set.
func kindleAddress(b *Bot) (string, error) {
	target, err := database.GetKindleTarget(b.Binding.UserID)
	return target.Email, err
}

// kindleConfirmPainter reads the confirmation: the book, still handed out,
// and the address it would go to.
func (r *Router) kindleConfirmPainter(ctx context.Context, b *Bot, node *models.TelegramNode) (render, string, bool) {
	book, err := database.GetBookCard(ctx, node.EntityID)
	if isNoRows(err) || err == nil && !visible(book) {
		return nil, screens.CopyUnavailable, false
	}
	if err != nil {
		return nil, screens.CopyLibraryDown, false
	}
	address, err := kindleAddress(b)
	if err != nil {
		return nil, screens.CopyLibraryDown, false
	}
	return func(k *keys) screens.Screen {
		entity, parent := uint64(node.EntityID), uint64(node.ParentID) // #nosec G115 -- serial ids
		back := k.env(cbv2.Envelope{Op: cbv2.OpBack, ReturnID: parent})
		if address == "" {
			return screens.KindleStatusScreen(book.Title, kindlesend.CodeNotConfigured, r.kindleSetupURL(), back, k.home())
		}
		return screens.KindleConfirmScreen(book.Title, address,
			k.env(cbv2.Envelope{Op: cbv2.OpKindleSend, EntityID: entity, ReturnID: parent}), back)
	}, "", true
}

// kindleStatusPainter reads a send's progress or outcome.
func (r *Router) kindleStatusPainter(ctx context.Context, node *models.TelegramNode) (render, string, bool) {
	title := ""
	if book, err := database.GetBookCard(ctx, node.EntityID); err == nil {
		title = book.Title
	}
	code := kindlesend.CodeSendFailed
	if node.Page >= 0 && int(node.Page) < len(kindleStatuses) {
		code = kindleStatuses[node.Page]
	}
	return func(k *keys) screens.Screen {
		back := k.env(cbv2.Envelope{Op: cbv2.OpBack, ReturnID: uint64(node.ParentID)}) // #nosec G115 -- serial ids
		return screens.KindleStatusScreen(title, code, r.kindleSetupURL(), back, k.home())
	}, "", true
}

// kindleStatus is the status node of a send of bookID from the card parent.
func kindleStatus(userID, bookID, parent int64, code string) *models.TelegramNode {
	return &models.TelegramNode{UserID: userID, Screen: int16(ScrKindleStatus), EntityID: bookID, ParentID: parent,
		Page: kindlePage(code)}
}

// sendKindle is «Отправить»: the book, re-checked; the reader's claim on it
// in the shared queue — a second press, or the site, gets «уже отправляется»;
// the panel turned to «Отправляю…»; and the send, in the background.
func (r *Router) sendKindle(ctx context.Context, b *Bot, panel *models.TelegramPanel, env *cbv2.Envelope) string {
	bookID, parent := int64(env.EntityID), int64(env.ReturnID) // #nosec G115 -- decoded ids fit
	book, err := database.GetBookCard(ctx, bookID)
	if isNoRows(err) || err == nil && !visible(book) {
		return screens.CopyUnavailable
	}
	if err != nil {
		return screens.CopyLibraryDown
	}
	userID := b.Binding.UserID
	key := kindlesend.Key{UserID: userID, BookID: bookID}
	if code := r.kindle.Queue.Admit(key); code != "" {
		return r.show(ctx, b, panel, panel.Revision, kindleStatus(userID, bookID, parent, code))
	}
	node := kindleStatus(userID, bookID, parent, screens.KindleSending)
	if toast := r.show(ctx, b, panel, panel.Revision, node); toast != "" {
		r.kindle.Queue.Leave(key)
		return toast
	}
	job := &kindleJob{key: key, botRef: b.Ref, chatID: panel.ChatID, messageID: panel.MessageID, nodeID: node.ID,
		parent: parent, bindingID: b.Binding.ID, generation: b.Binding.Generation}
	r.background.Add(1)
	// The send outlives the update: its lease ends when the press is
	// answered, and the outcome is told without it.
	go r.runKindle(context.WithoutCancel(ctx), job)
	return ""
}

// kindleJob is one send from a card, and where its outcome goes.
type kindleJob struct {
	key                       kindlesend.Key
	botRef, chatID, messageID int64
	nodeID, parent            int64
	bindingID                 int64
	generation                int32
}

// runKindle sends, then tells the outcome. The send no longer stands when
// the bot was relinked meanwhile — nobody is left to tell — or the book is
// no longer handed out. Both are checked before each wait and once more
// after the conversion, right before the mail; a read that fails stops it.
func (r *Router) runKindle(ctx context.Context, job *kindleJob) {
	defer r.background.Done()
	ctx = database.WithoutInboxLease(ctx)
	check := func() string {
		b, err := r.bots.V2Bot(ctx, job.botRef)
		if err != nil {
			return kindlesend.CodeSendFailed
		}
		if !job.owns(b) {
			return kindlesend.OutcomeDropped
		}
		book, err := database.GetBookCard(ctx, job.key.BookID)
		switch {
		case isNoRows(err) || err == nil && !visible(book):
			return screens.KindleUnavailable
		case err != nil:
			return kindlesend.CodeSendFailed
		}
		return ""
	}
	code := r.kindle.RunGuarded(job.key.UserID, job.key.BookID, check, check, nil)
	// Released before the outcome is told, as on the site: asked again the
	// moment it is seen, the book is not still «on its way».
	r.kindle.Queue.Leave(job.key)
	if code != kindlesend.OutcomeDropped {
		r.tellKindle(ctx, job, code)
	}
}

// owns reports the bot still served for the binding the send was asked
// under.
func (job *kindleJob) owns(b *Bot) bool {
	return b != nil && b.Binding != nil && b.Binding.ID == job.bindingID && b.Binding.Generation == job.generation
}

// tellKindle shows a send's outcome on its panel, if the panel still shows
// its progress; otherwise, or when the chat stays busy, as a message.
func (r *Router) tellKindle(ctx context.Context, job *kindleJob, code string) {
	unlock, lockErr := database.LockTelegramChat(ctx, job.botRef, job.chatID, r.cfg.ChatHold, r.cfg.ChatWait)
	if lockErr == nil {
		defer unlock()
	}
	b, err := r.bots.V2Bot(ctx, job.botRef)
	if err != nil || !job.owns(b) {
		return
	}
	if lockErr == nil {
		panel, err := database.GetTelegramPanel(ctx, job.botRef, job.chatID, job.messageID)
		if err == nil && panel.NodeID == job.nodeID {
			node := kindleStatus(b.Binding.UserID, job.key.BookID, job.parent, code)
			if toast := r.show(ctx, b, panel, panel.Revision, node); toast == "" {
				return
			}
		}
	} else {
		logging.Infof("Telegram bot %d: a Kindle outcome goes out as a message: %v", job.botRef, lockErr)
	}
	r.sendPlainText(ctx, b, job.chatID, screens.KindleText(code))
}
