package router

import (
	"context"
	"errors"
	"sync"
	"time"
	"unicode/utf8"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/telegram/cbv2"
	"gopds-api/telegram/screens"

	"github.com/go-telegram/bot"
	tgbot "github.com/go-telegram/bot/models"
)

// search answers typed text: a new view, and a new results panel for it
// (plan-ui.md §1.2). It never takes over the pinned home panel.
func (r *Router) search(ctx context.Context, b *Bot, chatID int64, query string, tab uint8) {
	stop := r.typing(ctx, b, chatID)
	defer stop()

	ui, err := database.GetTelegramUserUI(ctx, b.Binding.UserID)
	if err != nil {
		logging.Warnf("Telegram bot %d: reading the account's settings: %v", b.Ref, err)
		r.sendPlainText(ctx, b, chatID, screens.CopyLibraryDown)
		return
	}
	view := &models.TelegramView{UserID: b.Binding.UserID, Kind: ViewSearch, Query: query, Lang: ui.SearchLang}
	if err := database.CreateTelegramView(ctx, view); err != nil {
		logging.Warnf("Telegram bot %d: recording a search: %v", b.Ref, err)
		r.sendPlainText(ctx, b, chatID, screens.CopyLibraryDown)
		return
	}
	node := &models.TelegramNode{UserID: b.Binding.UserID, ViewID: view.ID, Screen: int16(tabScreen(tab))}
	if toast := r.open(ctx, b, chatID, node); toast != "" {
		r.sendPlainText(ctx, b, chatID, toast)
	}
}

// typing shows «typing» once the search has taken TypingAfter; the returned
// function stops it.
func (r *Router) typing(ctx context.Context, b *Bot, chatID int64) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-time.After(r.cfg.TypingAfter):
			_, _ = b.Client.SendChatAction(ctx, &bot.SendChatActionParams{ChatID: chatID, Action: tgbot.ChatActionTyping})
		}
	}()
	return func() { close(done) }
}

// searchBranches are books and authors, searched at once.
const searchBranches = 2

func tabScreen(tab uint8) Screen {
	if tab == 1 {
		return ScrResultsAuthors
	}
	return ScrResultsBooks
}

// runSearch runs both branches of a view's search for one page of tab: the
// tab's page, and the other tab's total. A page left empty by a catalog
// change is replaced by the last page; the second result says so.
func (r *Router) runSearch(ctx context.Context, view *models.TelegramView, tab uint8, page int) (*screens.Results, bool) {
	res := r.searchPage(ctx, view, tab, page)
	moved := false
	shown, total := len(res.Books), res.BookTotal
	if tab == 1 {
		shown, total = len(res.Authors), res.AuthorTotal
	}
	if page > 0 && shown == 0 && total > 0 {
		res = r.searchPage(ctx, view, tab, screens.Pages(total)-1)
		moved = true
	}
	return res, moved
}

func (r *Router) searchPage(ctx context.Context, view *models.TelegramView, tab uint8, page int) *screens.Results {
	res := &screens.Results{Query: view.Query, Lang: view.Lang, Tab: tab, Page: page}
	bookLimit, bookOffset := 1, 0
	authorLimit, authorOffset := 1, 0
	if tab == 0 {
		bookLimit, bookOffset = screens.PageSize, page*screens.PageSize
	} else {
		authorLimit, authorOffset = screens.PageSize, page*screens.PageSize
	}
	var wg sync.WaitGroup
	wg.Add(searchBranches)
	go func() {
		defer wg.Done()
		bctx, cancel := context.WithTimeout(ctx, r.cfg.SearchTimeout)
		defer cancel()
		got, err := r.cfg.Search.SearchBooks(bctx, models.BookSearchRequest{
			Query: view.Query, Language: view.Lang, AuthorID: view.AuthorID, Limit: bookLimit, Offset: bookOffset,
		})
		if err != nil {
			logging.Warnf("Telegram v2: book search failed: %v", err)
			res.BooksFailed = true
			return
		}
		res.BookTotal = got.Total
		if tab == 0 {
			res.Books = got.Books
			r.creditAuthors(bctx, res.Books)
		}
	}()
	go func() {
		defer wg.Done()
		actx, cancel := context.WithTimeout(ctx, r.cfg.SearchTimeout)
		defer cancel()
		got, err := r.cfg.Search.SearchAuthors(actx, models.AuthorSearchRequest{
			Query: view.Query, Language: view.Lang, Limit: authorLimit, Offset: authorOffset,
		})
		if err != nil {
			logging.Warnf("Telegram v2: author search failed: %v", err)
			res.AuthorsFailed = true
			return
		}
		res.AuthorTotal = got.Total
		if tab == 1 {
			res.Authors = got.Authors
		}
	}()
	wg.Wait()
	return res
}

// resultsPainter reads a results page: the node's tab and page of its view.
func (r *Router) resultsPainter(ctx context.Context, b *Bot, node *models.TelegramNode) (render, string, bool) {
	view, toast, ok := viewOf(ctx, b, node.ViewID)
	if !ok {
		return nil, toast, false
	}
	tab := uint8(0)
	if Screen(node.Screen) == ScrResultsAuthors {
		tab = 1
	}
	res, moved := r.runSearch(ctx, view, tab, int(node.Page))
	node.Page = int32(res.Page) // #nosec G115 -- pages are small
	r.decorate(ctx, view, res)
	if moved {
		toast = screens.CopyListChanged
	}
	return r.paintResults(view, node, res, r.backTo(ctx, b, node.ParentID)), toast, true
}

// suggestionMinRunes is the shortest query worth suggesting for.
const suggestionMinRunes = 3

// decorate adds what a results page shows besides its lists: the filtering
// author's name, and suggestions when nothing was found.
func (r *Router) decorate(ctx context.Context, view *models.TelegramView, res *screens.Results) {
	if view.AuthorID != 0 {
		if author, err := database.GetTelegramAuthor(ctx, view.AuthorID); err == nil {
			res.AuthorName = author.FullName
		}
	}
	empty := !res.BooksFailed && !res.AuthorsFailed && res.BookTotal == 0 && res.AuthorTotal == 0
	if !empty || utf8.RuneCountInString(view.Query) < suggestionMinRunes {
		return
	}
	sctx, cancel := context.WithTimeout(ctx, r.cfg.SearchTimeout)
	defer cancel()
	got, err := r.cfg.Search.Suggestions(sctx, models.SuggestionRequest{
		Query: view.Query, Kind: models.SuggestionAll, Language: view.Lang, Limit: suggestionMinRunes,
	})
	if err != nil {
		logging.Debugf("Telegram v2: suggestions failed: %v", err)
		return
	}
	for _, s := range got.Suggestions {
		if s.ID != 0 && (s.Type == suggestionAuthor || s.Type == suggestionBook) && len(res.Suggestions) < suggestionMinRunes {
			res.Suggestions = append(res.Suggestions, s)
		}
	}
}

// The kinds of suggestion a results page offers.
const (
	suggestionAuthor = "author"
	suggestionBook   = "book"
)

// paintResults paints a results page whose node is node.
func (r *Router) paintResults(view *models.TelegramView, node *models.TelegramNode, res *screens.Results,
	back func(*keys) *screens.Button) render {
	return func(k *keys) screens.Screen {
		viewID, parent, self := uint64(view.ID), uint64(node.ParentID), uint64(node.ID) // #nosec G115 -- serial ids
		at := func(op cbv2.Op, tab uint8, page int) *cbv2.Envelope {
			return k.env(cbv2.Envelope{Op: op, ViewID: viewID, Arg: tab, Page: uint32(page), ReturnID: parent}) // #nosec G115 -- pages are small
		}
		here := func(op cbv2.Op, entity uint64) *cbv2.Envelope {
			return k.env(cbv2.Envelope{Op: op, ViewID: viewID, EntityID: entity, ReturnID: self})
		}
		rk := &screens.ResultsKeys{
			Tab:      func(tab uint8) *cbv2.Envelope { return at(cbv2.OpTab, tab, 0) },
			RetryTab: func(tab uint8) *cbv2.Envelope { return at(cbv2.OpRetryBranch, tab, 0) },
			RetryAll: at(cbv2.OpRetrySearch, res.Tab, res.Page),
			Page:     func(page int) *cbv2.Envelope { return at(cbv2.OpPage, res.Tab, page) },
			Book:     func(id int64) *cbv2.Envelope { return here(cbv2.OpOpenBook, uint64(id)) }, // #nosec G115 -- serial ids
			Author:   here(cbv2.OpPromptAuthor, 0),
			Language: here(cbv2.OpLanguagePicker, 0),
			// «Искать на всех языках» is the language set to none.
			AllLanguages: here(cbv2.OpSetLanguage, 0),
			OpenAuthor:   func(id int64) *cbv2.Envelope { return here(cbv2.OpOpenAuthor, uint64(id)) }, // #nosec G115 -- serial ids
			Suggest: func(s *models.AutocompleteSuggestion) *cbv2.Envelope {
				if s.Type == suggestionAuthor {
					return here(cbv2.OpOpenAuthor, uint64(s.ID)) // #nosec G115 -- serial ids
				}
				return here(cbv2.OpOpenBook, uint64(s.ID)) // #nosec G115 -- serial ids
			},
			Back: back(k),
			Home: k.home(),
		}
		if view.AuthorID != 0 {
			rk.ClearAuthor = here(cbv2.OpClearAuthor, 0)
		}
		return screens.ResultsScreen(res, rk)
	}
}

// expiredOrDown is the answer to a node or view that could not be read.
func expiredOrDown(err error) string {
	if isNoRows(err) {
		return screens.CopyExpired
	}
	return screens.CopyLibraryDown
}

func isNoRows(err error) bool {
	return errors.Is(err, errNoRows)
}
