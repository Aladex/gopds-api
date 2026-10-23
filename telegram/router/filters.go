package router

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/telegram/cbv2"
	"gopds-api/telegram/ru"
	"gopds-api/telegram/screens"
	"gopds-api/telegram/tgapi"

	"github.com/go-telegram/bot"
	tgbot "github.com/go-telegram/bot/models"
)

// The filters (plan-ui.md §1.4): the language screen, the author filter and
// the searches within an author or the favorites. A filter never changes a
// view: it makes a new one, whose first page's «Назад» is the page it came
// from.

// The kinds of prompt, stored in telegram_prompt.kind.
const (
	promptAuthorFilter int16 = 1 + iota
	promptAuthorSearch
	promptFavoritesSearch
)

// The prompts' states.
const (
	promptOpen      = "open"
	promptAnswered  = "answered"
	promptCancelled = "cancelled" //nolint:misspell // the stored state (migration 46)
)

// promptTTL is how long a prompt waits for its reply.
const promptTTL = 15 * time.Minute

// authorPicks is how many authors a reply to the author filter offers.
const authorPicks = 5

// languagePickerPager is OpLanguagePicker's Arg for the «Другой язык» list.
const languagePickerPager = 1

// languageNode is the language screen of a view, or of the settings when
// viewID is 0; page 0 is the short list, page n+1 the pager's page n.
func languageNode(userID, viewID, parent int64, pager bool, page uint32) *models.TelegramNode {
	node := &models.TelegramNode{UserID: userID, ViewID: viewID, Screen: int16(ScrLanguage), ParentID: parent}
	if viewID == 0 {
		node.Screen = int16(ScrDefaultLanguage)
	}
	if pager {
		node.Page = int32(page) + 1 // #nosec G115 -- pages are small
	}
	return node
}

// languagePainter reads a language screen.
func (r *Router) languagePainter(ctx context.Context, b *Bot, node *models.TelegramNode) (render, string, bool) {
	settings := Screen(node.Screen) == ScrDefaultLanguage
	var current string
	if settings {
		ui, err := database.GetTelegramUserUI(ctx, b.Binding.UserID)
		if err != nil {
			return nil, screens.CopyLibraryDown, false
		}
		current = ui.SearchLang
	} else {
		view, toast, ok := viewOf(ctx, b, node.ViewID)
		if !ok {
			return nil, toast, false
		}
		current = view.Lang
	}
	var codes []string
	if node.Page > 0 {
		codes = existingLanguages()
	}
	back := r.backTo(ctx, b, node.ParentID)
	return func(k *keys) screens.Screen {
		viewID, parent := uint64(node.ViewID), uint64(node.ParentID) // #nosec G115 -- serial ids
		set := func(code string) *cbv2.Envelope {
			return k.env(cbv2.Envelope{Op: cbv2.OpSetLanguage, ViewID: viewID, ReturnID: parent, Arg: ru.LangID(code)})
		}
		picker := func(pager uint8, page int) *cbv2.Envelope {
			return k.env(cbv2.Envelope{Op: cbv2.OpLanguagePicker, ViewID: viewID, ReturnID: parent, Arg: pager,
				Page: uint32(page)}) // #nosec G115 -- pages are small
		}
		backButton := screens.Button{Text: screens.ButtonHome, Env: k.home()}
		if b := back(k); b != nil {
			backButton = *b
		}
		if node.Page == 0 {
			return screens.LanguageScreen(current, settings, &screens.LanguageKeys{
				Set: set, Other: picker(languagePickerPager, 0), Back: backButton,
			})
		}
		return screens.LanguagePagerScreen(codes, int(node.Page)-1, current, set,
			func(p int) *cbv2.Envelope { return picker(languagePickerPager, p) },
			screens.Button{Text: screens.ButtonBack, Env: picker(0, 0)})
	}, "", true
}

// existingLanguages are the languages the catalog has books in, most books
// first, that a button can name.
func existingLanguages() []string {
	var codes []string
	for _, l := range database.GetLanguages() {
		if code := strings.ToLower(strings.TrimSpace(l.Lang)); ru.KnownLanguage(code) {
			codes = append(codes, code)
		}
	}
	return codes
}

// setLanguage applies a language choice: to a view, as a new view; to the
// settings, as the account's search language.
func (r *Router) setLanguage(ctx context.Context, b *Bot, panel *models.TelegramPanel, env *cbv2.Envelope) string {
	code := ru.LangCode(env.Arg)
	if env.Arg != 0 && code == "" {
		return screens.CopyUnsupported
	}
	if env.ViewID == 0 {
		if err := database.SetTelegramSearchLang(ctx, b.Binding.UserID, code); err != nil {
			return failedWrite(b, "saving the search language", err)
		}
		return r.back(ctx, b, panel, int64(env.ReturnID)) // #nosec G115 -- decoded ids fit
	}
	viewID, from := int64(env.ViewID), int64(env.ReturnID) // #nosec G115 -- decoded ids fit
	return r.refine(ctx, b, panel, viewID, from, func(v *models.TelegramView) { v.Lang = code })
}

// refine shows page one of a copy of a view with one filter changed, on the
// screen of the page it came from — which stays its «Назад».
func (r *Router) refine(ctx context.Context, b *Bot, panel *models.TelegramPanel, viewID, fromID int64,
	change func(*models.TelegramView)) string {
	node, toast := r.refined(ctx, b, viewID, fromID, change)
	if node == nil {
		return toast
	}
	return r.show(ctx, b, panel, panel.Revision, node)
}

// refined makes the refined view and its first page's node.
func (r *Router) refined(ctx context.Context, b *Bot, viewID, fromID int64,
	change func(*models.TelegramView)) (node *models.TelegramNode, toast string) {
	old, toast, ok := viewOf(ctx, b, viewID)
	if !ok {
		return nil, toast
	}
	from, err := database.GetTelegramNode(ctx, b.Binding.UserID, fromID)
	if err != nil {
		return nil, expiredOrDown(err)
	}
	v := &models.TelegramView{UserID: old.UserID, Kind: old.Kind, Query: old.Query, Lang: old.Lang,
		AuthorID: old.AuthorID, SeriesID: old.SeriesID, CollectionID: old.CollectionID, Favorites: old.Favorites}
	change(v)
	if err := newView(ctx, v); err != nil {
		return nil, screens.CopyLibraryDown
	}
	screen := from.Screen
	if from.ViewID != old.ID {
		screen = int16(screenOfView(v.Kind))
	}
	return &models.TelegramNode{UserID: v.UserID, ViewID: v.ID, Screen: screen, ParentID: from.ID}, ""
}

// failedWrite logs a write that failed, and is its toast.
func failedWrite(b *Bot, what string, err error) string {
	if !errors.Is(err, database.ErrInboxLeaseLost) {
		logging.Warnf("Telegram bot %d: %s: %v", b.Ref, what, err)
	}
	return screens.CopyLibraryDown
}

// ask sends a prompt: a plain message with force_reply and «Отмена»,
// recorded with the panel it answers to.
func (r *Router) ask(ctx context.Context, b *Bot, panel *models.TelegramPanel, kind int16,
	question func(cancel *cbv2.Envelope) screens.Screen) string {
	msg, err := r.sendPrompt(ctx, b, panel.ChatID, question)
	if err != nil {
		return failedWrite(b, "asking a question", err)
	}
	p := &models.TelegramPrompt{
		BotRef: b.Ref, ChatID: panel.ChatID, PromptMessageID: int64(msg.ID), UserID: b.Binding.UserID, Kind: kind,
		SourceMessageID: panel.MessageID, SourceNodeID: panel.NodeID, SourceRevision: panel.Revision,
		State: promptOpen, ExpiresAt: time.Now().Add(promptTTL),
	}
	if err := database.CreateTelegramPrompt(ctx, p); err != nil {
		return failedWrite(b, "recording a question", err)
	}
	return ""
}

// sendPrompt sends a question whose reply box opens by itself. Its buttons
// carry revision 0: a prompt is not a panel.
func (r *Router) sendPrompt(ctx context.Context, b *Bot, chatID int64,
	question func(cancel *cbv2.Envelope) screens.Screen) (*tgbot.Message, error) {
	k := &keys{b: b}
	screen := question(k.env(cbv2.Envelope{Op: cbv2.OpCancelPrompt}))
	markup, err := r.markup(b, chatID, screen.Keys)
	if err != nil {
		return nil, err
	}
	markup.ForceReply = true
	return b.Client.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID, Text: screen.Doc.Plain(), ParseMode: tgbot.ParseModeHTML,
		LinkPreviewOptions: noPreview(), ReplyMarkup: markup,
	})
}

// askAuthor opens the author filter of a search.
func (r *Router) askAuthor(ctx context.Context, b *Bot, panel *models.TelegramPanel, viewID int64) string {
	view, toast, ok := viewOf(ctx, b, viewID)
	if !ok {
		return toast
	}
	return r.ask(ctx, b, panel, promptAuthorFilter, func(cancel *cbv2.Envelope) screens.Screen {
		return screens.PromptAuthorFilter(view.Query, cancel)
	})
}

// askAuthorSearch opens the search within an author's books.
func (r *Router) askAuthorSearch(ctx context.Context, b *Bot, panel *models.TelegramPanel, viewID int64) string {
	view, toast, ok := viewOf(ctx, b, viewID)
	if !ok {
		return toast
	}
	author, err := r.authorOf(ctx, view.AuthorID)
	if errors.Is(err, errAuthorGone) {
		return screens.CopyAuthorGone
	}
	if err != nil {
		return expiredOrDown(err)
	}
	return r.ask(ctx, b, panel, promptAuthorSearch, func(cancel *cbv2.Envelope) screens.Screen {
		return screens.PromptAuthorSearch(author.name, cancel)
	})
}

// promptButton handles a press on a prompt: «Отмена», an author picked from
// the list, «Ввести другое имя».
func (r *Router) promptButton(ctx context.Context, b *Bot, msg *tgbot.Message, env *cbv2.Envelope) string {
	p, err := database.GetTelegramPrompt(ctx, b.Ref, msg.Chat.ID, int64(msg.ID))
	if err != nil {
		return expiredOrDown(err)
	}
	//exhaustive:ignore // a prompt has these three buttons
	switch env.Op {
	case cbv2.OpCancelPrompt:
		return r.closePrompt(ctx, b, p, promptCancelled, screens.CopyPromptCancelled)
	case cbv2.OpPickAuthor:
		if toast := r.closePrompt(ctx, b, p, promptAnswered, screens.CopyPromptDone); toast != "" {
			return toast
		}
		node, toast := r.refined(ctx, b, r.sourceView(ctx, b, p), p.SourceNodeID,
			func(v *models.TelegramView) { v.AuthorID = int64(env.EntityID) }) // #nosec G115 -- decoded ids fit
		if node == nil {
			return toast
		}
		node.Screen = int16(ScrResultsBooks)
		return r.landOnSource(ctx, b, p, node)
	case cbv2.OpPromptAuthor:
		if !promptIsOpen(p) {
			return screens.CopyPromptClosed
		}
		view, toast, ok := viewOf(ctx, b, r.sourceView(ctx, b, p))
		if !ok {
			return toast
		}
		next, err := r.sendPrompt(ctx, b, p.ChatID, func(cancel *cbv2.Envelope) screens.Screen {
			return screens.PromptAuthorFilter(view.Query, cancel)
		})
		if err != nil {
			return failedWrite(b, "asking again", err)
		}
		if _, err := database.CloseTelegramPrompt(ctx, p, promptCancelled); err != nil {
			return failedWrite(b, "closing a question", err)
		}
		r.editPrompt(ctx, b, p, screens.CopyPromptAgain)
		again := *p
		again.PromptMessageID, again.State, again.ExpiresAt = int64(next.ID), promptOpen, time.Now().Add(promptTTL)
		if err := database.CreateTelegramPrompt(ctx, &again); err != nil {
			return failedWrite(b, "recording a question", err)
		}
		return ""
	}
	return screens.CopyUnsupported
}

// promptIsOpen reports a prompt still waiting for its reply.
func promptIsOpen(p *models.TelegramPrompt) bool {
	return p.State == promptOpen && time.Now().Before(p.ExpiresAt)
}

// closePrompt closes an open prompt as state and turns it into text; the
// toast when it was closed already.
func (r *Router) closePrompt(ctx context.Context, b *Bot, p *models.TelegramPrompt, state, text string) string {
	closed, err := database.CloseTelegramPrompt(ctx, p, state)
	if err != nil {
		return failedWrite(b, "closing a question", err)
	}
	if !closed {
		return screens.CopyPromptClosed
	}
	r.editPrompt(ctx, b, p, text)
	return ""
}

// editPrompt replaces a prompt's text and takes its buttons away.
func (r *Router) editPrompt(ctx context.Context, b *Bot, p *models.TelegramPrompt, text string) {
	if _, err := b.Client.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID: p.ChatID, MessageID: int(p.PromptMessageID), Text: text, LinkPreviewOptions: noPreview(),
	}); err != nil {
		if class, _ := tgapi.Classify(err); class != tgapi.ClassNotModified {
			logging.Warnf("Telegram bot %d: editing a question: %v", b.Ref, tgapi.RedactURL(err))
		}
	}
}

// sourceView is the view of the panel a prompt answers to; 0 when it is
// gone.
func (r *Router) sourceView(ctx context.Context, b *Bot, p *models.TelegramPrompt) int64 {
	node, err := database.GetTelegramNode(ctx, b.Binding.UserID, p.SourceNodeID)
	if err != nil {
		return 0
	}
	return node.ViewID
}

// landOnSource shows a prompt's answer on the panel the prompt was asked
// from, if it still shows what it showed then; otherwise as a new panel.
func (r *Router) landOnSource(ctx context.Context, b *Bot, p *models.TelegramPrompt, node *models.TelegramNode) string {
	source, err := database.GetTelegramPanel(ctx, b.Ref, p.ChatID, p.SourceMessageID)
	if err == nil {
		paint, toast, ok := r.painter(ctx, b, node)
		if !ok {
			return toast
		}
		if err := upsertNode(ctx, node); err != nil {
			return screens.CopyLibraryDown
		}
		if r.movePanel(ctx, b, source, p.SourceRevision, node, paint) {
			return toast
		}
	}
	return r.open(ctx, b, p.ChatID, node)
}

// reply handles a reply to one of the bot's messages. It reports false when
// the message replied to is not a prompt: the text is then a search like any
// other.
func (r *Router) reply(ctx context.Context, b *Bot, msg *tgbot.Message) bool {
	p, err := database.GetTelegramPrompt(ctx, b.Ref, msg.Chat.ID, int64(msg.ReplyToMessage.ID))
	switch {
	case isNoRows(err):
		return false
	case err != nil:
		r.sendPlainText(ctx, b, msg.Chat.ID, screens.CopyLibraryDown)
		return true
	case !promptIsOpen(p):
		r.sendPlainText(ctx, b, msg.Chat.ID, screens.CopyPromptClosed)
		return true
	}
	text := strings.Join(strings.Fields(msg.Text), " ")
	switch {
	case text == "":
		r.sendPlainText(ctx, b, msg.Chat.ID, screens.CopyEmptyQuery)
		return true
	case utf8.RuneCountInString(text) > screens.MaxQuery:
		r.sendPlainText(ctx, b, msg.Chat.ID, screens.CopyTooLong)
		return true
	}
	if p.Kind == promptAuthorFilter {
		r.pickAuthor(ctx, b, p, text)
		return true
	}
	if toast := r.closePrompt(ctx, b, p, promptAnswered, screens.CopyPromptDone); toast != "" {
		r.sendPlainText(ctx, b, msg.Chat.ID, toast)
		return true
	}
	var v *models.TelegramView
	if p.Kind == promptFavoritesSearch {
		v = &models.TelegramView{Kind: ViewFavoritesSearch, Query: text, Favorites: true}
	} else {
		view, toast, ok := viewOf(ctx, b, r.sourceView(ctx, b, p))
		if !ok {
			r.sendPlainText(ctx, b, msg.Chat.ID, toast)
			return true
		}
		v = &models.TelegramView{Kind: ViewAuthorSearch, Query: text, AuthorID: view.AuthorID, Lang: view.Lang}
	}
	v.UserID = b.Binding.UserID
	if err := newView(ctx, v); err != nil {
		r.sendPlainText(ctx, b, msg.Chat.ID, failedWrite(b, "recording a search", err))
		return true
	}
	node := &models.TelegramNode{UserID: v.UserID, ViewID: v.ID, Screen: int16(screenOfView(v.Kind)), ParentID: p.SourceNodeID}
	if toast := r.landOnSource(ctx, b, p, node); toast != "" {
		r.sendPlainText(ctx, b, msg.Chat.ID, toast)
	}
	return true
}

// pickAuthor turns the author filter's prompt into the authors a reply
// matched. The prompt stays open: a pick closes it.
func (r *Router) pickAuthor(ctx context.Context, b *Bot, p *models.TelegramPrompt, name string) {
	sctx, cancel := context.WithTimeout(ctx, r.cfg.SearchTimeout)
	defer cancel()
	got, err := r.cfg.Search.SearchAuthors(sctx, models.AuthorSearchRequest{Query: name, Limit: authorPicks})
	if err == nil && r.cfg.Entities {
		got.Authors, err = r.entityAuthors(sctx, got.Entities)
	}
	if err != nil {
		logging.Warnf("Telegram v2: author search failed: %v", err)
		r.sendPlainText(ctx, b, p.ChatID, screens.CopySearchFailed)
		return
	}
	k := &keys{b: b}
	screen := screens.AuthorPickScreen(name, got.Authors,
		func(id int64) *cbv2.Envelope {
			return k.env(cbv2.Envelope{Op: cbv2.OpPickAuthor, EntityID: uint64(id)}) // #nosec G115 -- serial ids
		},
		k.env(cbv2.Envelope{Op: cbv2.OpPromptAuthor}), k.env(cbv2.Envelope{Op: cbv2.OpCancelPrompt}))
	markup, err := r.markup(b, p.ChatID, screen.Keys)
	if err != nil {
		logging.Warnf("Telegram bot %d: the pick list's buttons: %v", b.Ref, err)
		return
	}
	if _, err := b.Client.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID: p.ChatID, MessageID: int(p.PromptMessageID), Text: screen.Doc.Plain(), ParseMode: tgbot.ParseModeHTML,
		LinkPreviewOptions: noPreview(), ReplyMarkup: markup,
	}); err != nil {
		if class, _ := tgapi.Classify(err); class != tgapi.ClassNotModified {
			logging.Warnf("Telegram bot %d: showing the pick list: %v", b.Ref, tgapi.RedactURL(err))
		}
	}
}
