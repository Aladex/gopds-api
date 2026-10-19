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
	msg := q.Message.Message
	if !cbv2.IsV2(q.Data) {
		return r.legacy(ctx, b, q, msg)
	}
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
	switch env.Revision {
	case cbv2.DocumentRevision:
		r.answer(ctx, b, q, r.documentButton(ctx, b, msg.Chat.ID, &env))
		return true
	case 0:
		r.answer(ctx, b, q, r.promptButton(ctx, b, msg, &env))
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
//
//nolint:gocyclo,funlen // one flat dispatch on the button's op
func (r *Router) act(ctx context.Context, b *Bot, panel *models.TelegramPanel, env *cbv2.Envelope) string {
	userID := b.Binding.UserID
	viewID, entity, parent := int64(env.ViewID), int64(env.EntityID), int64(env.ReturnID) // #nosec G115 -- decoded ids fit
	show := func(node *models.TelegramNode) string { return r.show(ctx, b, panel, panel.Revision, node) }
	//exhaustive:ignore // the ops of later slices fall through to «unsupported»
	switch env.Op {
	case cbv2.OpHome:
		return r.goHome(ctx, b, panel)
	case cbv2.OpTab, cbv2.OpRetryBranch, cbv2.OpRetrySearch:
		return show(&models.TelegramNode{UserID: userID, ViewID: viewID, Screen: int16(tabScreen(env.Arg)),
			Page: int32(env.Page), ParentID: parent}) // #nosec G115 -- pages are small
	case cbv2.OpPage:
		return r.page(ctx, b, panel, env)
	case cbv2.OpOpenBook:
		return show(cardNode(userID, viewID, entity, parent))
	case cbv2.OpSetFavorite:
		return r.setFavorite(ctx, b, panel, env)
	case cbv2.OpAnnotation, cbv2.OpAnnotationPage:
		return show(annotationNode(userID, entity, parent, int(env.Page)))
	case cbv2.OpBack:
		return r.back(ctx, b, panel, parent)
	case cbv2.OpOpenAuthor:
		return r.openList(ctx, b, panel, &models.TelegramView{Kind: ViewAuthorBooks, AuthorID: entity,
			Lang: inheritLang(ctx, b, viewID)}, parent)
	case cbv2.OpOpenSeries:
		return r.openList(ctx, b, panel, &models.TelegramView{Kind: ViewSeriesBooks, SeriesID: entity,
			Lang: inheritLang(ctx, b, viewID)}, parent)
	case cbv2.OpOpenCollection:
		// A collection is a curated list across languages: it opens whole.
		return r.openList(ctx, b, panel, &models.TelegramView{Kind: ViewCollectionBooks, CollectionID: entity}, parent)
	case cbv2.OpFavorites:
		return r.openList(ctx, b, panel, &models.TelegramView{Kind: ViewFavorites, Favorites: true}, parent)
	case cbv2.OpCollections:
		page := int32(env.Page) // #nosec G115 -- pages are small
		return show(&models.TelegramNode{UserID: userID, Screen: int16(ScrCollections), Page: page, ParentID: parent})
	case cbv2.OpBookAuthors:
		return show(&models.TelegramNode{UserID: userID, ViewID: viewID, Screen: int16(ScrBookAuthors), EntityID: entity, ParentID: parent})
	case cbv2.OpLanguagePicker:
		return show(languageNode(userID, viewID, parent, env.Arg == languagePickerPager, env.Page))
	case cbv2.OpSetLanguage:
		return r.setLanguage(ctx, b, panel, env)
	case cbv2.OpClearAuthor:
		return r.refine(ctx, b, panel, viewID, parent, func(v *models.TelegramView) { v.AuthorID = 0 })
	case cbv2.OpPromptAuthor:
		return r.askAuthor(ctx, b, panel, viewID)
	case cbv2.OpPromptAuthorSearch:
		return r.askAuthorSearch(ctx, b, panel, viewID)
	case cbv2.OpPromptFavSearch:
		return r.ask(ctx, b, panel, promptFavoritesSearch, screens.PromptFavoritesSearch)
	case cbv2.OpSettings:
		return r.openSettings(ctx, b, panel, env)
	case cbv2.OpSetFormat:
		return r.setFormat(ctx, b, panel, env)
	case cbv2.OpSetCardStyle:
		return r.setCardStyle(ctx, b, panel, env)
	}
	return screens.CopyUnsupported
}

// page moves a panel to another page of a view: of a search's tab, or of a
// list.
func (r *Router) page(ctx context.Context, b *Bot, panel *models.TelegramPanel, env *cbv2.Envelope) string {
	view, toast, ok := viewOf(ctx, b, int64(env.ViewID)) // #nosec G115 -- decoded ids fit
	if !ok {
		return toast
	}
	screen := screenOfView(view.Kind)
	if view.Kind == ViewSearch {
		screen = tabScreen(env.Arg)
	}
	return r.show(ctx, b, panel, panel.Revision, &models.TelegramNode{UserID: b.Binding.UserID, ViewID: view.ID,
		Screen: int16(screen), Page: int32(env.Page), ParentID: int64(env.ReturnID)}) // #nosec G115 -- decoded ids fit
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
	if Screen(node.Screen) == ScrHome {
		return r.goHome(ctx, b, panel)
	}
	return r.show(ctx, b, panel, panel.Revision, node)
}

// goHome turns the panel into the home screen, and is «Домой»'s pin check.
func (r *Router) goHome(ctx context.Context, b *Bot, panel *models.TelegramPanel) string {
	node := &models.TelegramNode{UserID: b.Binding.UserID, Screen: int16(ScrHome)}
	if err := upsertNode(ctx, node); err != nil {
		return screens.CopyLibraryDown
	}
	if !r.movePanel(ctx, b, panel, panel.Revision, node, r.paintHome(false)) {
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
