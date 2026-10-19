package router

import (
	"context"
	"errors"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/telegram/cbv2"
	"gopds-api/telegram/screens"
	"gopds-api/telegram/tgapi"

	"github.com/go-telegram/bot"
	tgbot "github.com/go-telegram/bot/models"
)

// callback handles a button press. Buttons of the old interface go back to
// the legacy handlers (false). Every v2 press is answered, on every path.
//
// The checks run in plan.md §2.5's order: shape and version, tag (which binds
// the button to this bot, owner and chat), generation, the panel and its
// revision, then the action.
func (r *Router) callback(ctx context.Context, b *Bot, q *tgbot.CallbackQuery) bool {
	if !cbv2.IsV2(q.Data) {
		return false
	}
	msg := q.Message.Message
	if msg == nil || b.Binding == nil || q.From.ID != b.Binding.TGUserID || msg.Chat.Type != tgbot.ChatTypePrivate {
		r.answer(ctx, b, q, screens.CopyButtonDenied)
		return true
	}
	r.ensureNoCommands(ctx, b)
	env, err := cbv2.Decode(q.Data, b.NavKey, cbv2.BindCtx{TGBotID: b.TGBotID, OwnerTGID: b.Binding.TGUserID, ChatID: msg.Chat.ID})
	if err != nil {
		r.answer(ctx, b, q, screens.CopyButtonDenied)
		return true
	}
	if int32(env.Generation) != b.Binding.Generation {
		r.answer(ctx, b, q, screens.CopyButtonDenied)
		return true
	}
	if env.Revision == cbv2.DocumentRevision {
		r.answer(ctx, b, q, r.documentButton(ctx, b, msg.Chat.ID, &env))
		return true
	}
	panel, err := database.GetTelegramPanel(ctx, b.Ref, msg.Chat.ID, int64(msg.ID))
	if err != nil {
		r.answer(ctx, b, q, expiredOrDown(err))
		return true
	}
	if int32(env.Revision) != panel.Revision {
		r.answer(ctx, b, q, screens.CopyScreenChanged)
		return true
	}
	if env.Op == cbv2.OpDownload {
		toast, answer := r.download(ctx, b, q, panel, &env)
		if answer {
			r.answer(ctx, b, q, toast)
		}
		return true
	}
	r.answer(ctx, b, q, r.act(ctx, b, panel, &env))
	return true
}

// act runs a panel's action and returns its toast.
func (r *Router) act(ctx context.Context, b *Bot, panel *models.TelegramPanel, env *cbv2.Envelope) string {
	userID := b.Binding.UserID
	//exhaustive:ignore // the ops of later slices fall through to «unsupported»
	switch env.Op {
	case cbv2.OpHome:
		return r.goHome(ctx, b, panel)
	case cbv2.OpTab:
		return r.showResults(ctx, b, panel, panel.Revision, int64(env.ViewID), env.Arg, 0) // #nosec G115 -- decoded ids fit
	case cbv2.OpPage, cbv2.OpRetryBranch, cbv2.OpRetrySearch:
		return r.showResults(ctx, b, panel, panel.Revision, int64(env.ViewID), env.Arg, int(env.Page)) // #nosec G115 -- decoded ids fit
	case cbv2.OpOpenBook:
		node := cardNode(userID, int64(env.ViewID), int64(env.EntityID), int64(env.ReturnID)) // #nosec G115 -- decoded ids fit
		return r.showCard(ctx, b, panel, panel.Revision, node)
	case cbv2.OpSetFavorite:
		return r.setFavorite(ctx, b, panel, env)
	case cbv2.OpAnnotation, cbv2.OpAnnotationPage:
		bookID, parent := int64(env.EntityID), int64(env.ReturnID) // #nosec G115 -- decoded ids fit
		return r.showAnnotation(ctx, b, panel, panel.Revision, bookID, parent, int(env.Page))
	case cbv2.OpBack:
		return r.back(ctx, b, panel, int64(env.ReturnID)) // #nosec G115 -- decoded ids fit
	}
	return screens.CopyUnsupported
}

// back shows the node a «Назад» points at.
func (r *Router) back(ctx context.Context, b *Bot, panel *models.TelegramPanel, nodeID int64) string {
	if nodeID == 0 {
		return r.goHome(ctx, b, panel)
	}
	node, err := database.GetTelegramNode(ctx, b.Binding.UserID, nodeID)
	if err != nil {
		return expiredOrDown(err)
	}
	//exhaustive:ignore // an annotation or a notice is never a «Назад» target
	switch Screen(node.Screen) {
	case ScrHome:
		return r.goHome(ctx, b, panel)
	case ScrResultsBooks, ScrResultsAuthors:
		tab := uint8(0)
		if Screen(node.Screen) == ScrResultsAuthors {
			tab = 1
		}
		return r.showResults(ctx, b, panel, panel.Revision, node.ViewID, tab, int(node.Page))
	case ScrBook:
		return r.showCard(ctx, b, panel, panel.Revision, node)
	}
	return screens.CopyExpired
}

// goHome turns the panel into the home screen, and is «Домой»'s pin check.
func (r *Router) goHome(ctx context.Context, b *Bot, panel *models.TelegramPanel) string {
	node := &models.TelegramNode{UserID: b.Binding.UserID, Screen: int16(ScrHome)}
	if err := upsertNode(ctx, node); err != nil {
		return screens.CopyLibraryDown
	}
	if !r.movePanel(ctx, b, panel, panel.Revision, node, func(*keys) screens.Screen { return screens.Home(false) }) {
		return screens.CopyScreenChanged
	}
	r.checkPin(ctx, b, panel.ChatID)
	return ""
}

// documentButton handles a button on a document: «Открыть карточку».
func (r *Router) documentButton(ctx context.Context, b *Bot, chatID int64, env *cbv2.Envelope) string {
	if env.Op != cbv2.OpOpenBook {
		return screens.CopyUnsupported
	}
	return r.sendCard(ctx, b, chatID, int64(env.EntityID)) // #nosec G115 -- decoded ids fit
}

// answer acknowledges a press, with a toast when text is not empty.
func (r *Router) answer(ctx context.Context, b *Bot, q *tgbot.CallbackQuery, text string) {
	if _, err := b.Client.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: q.ID, Text: text}); err != nil {
		if !errors.Is(err, context.Canceled) {
			logging.Debugf("Telegram bot %d: answering a press: %v", b.Ref, tgapi.RedactURL(err))
		}
	}
}
