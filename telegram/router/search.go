package router

import (
	"context"
	"errors"
	"sync"
	"time"

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
	res, _ := r.runSearch(ctx, view, tab, 0)
	node := &models.TelegramNode{UserID: b.Binding.UserID, ViewID: view.ID, Screen: int16(tabScreen(tab))}
	if err := upsertNode(ctx, node); err != nil {
		logging.Warnf("Telegram bot %d: recording a results node: %v", b.Ref, err)
		return
	}
	r.sendPanel(ctx, b, chatID, node, r.paintResults(view, node, res), false)
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
			Query: view.Query, Language: view.Lang, Limit: bookLimit, Offset: bookOffset,
		})
		if err != nil {
			logging.Warnf("Telegram v2: book search failed: %v", err)
			res.BooksFailed = true
			return
		}
		res.BookTotal = got.Total
		if tab == 0 {
			res.Books = got.Books
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

// paintResults paints a results page whose node is node.
func (r *Router) paintResults(view *models.TelegramView, node *models.TelegramNode, res *screens.Results) render {
	return func(k *keys) screens.Screen {
		viewID := uint64(view.ID) // #nosec G115 -- serial ids
		return screens.ResultsScreen(res, &screens.ResultsKeys{
			Tab: func(tab uint8) *cbv2.Envelope {
				return k.env(cbv2.Envelope{Op: cbv2.OpTab, ViewID: viewID, Arg: tab})
			},
			RetryTab: func(tab uint8) *cbv2.Envelope {
				return k.env(cbv2.Envelope{Op: cbv2.OpRetryBranch, ViewID: viewID, Arg: tab})
			},
			RetryAll: k.env(cbv2.Envelope{Op: cbv2.OpRetrySearch, ViewID: viewID, Arg: res.Tab}),
			Page: func(page int) *cbv2.Envelope {
				return k.env(cbv2.Envelope{Op: cbv2.OpPage, ViewID: viewID, Page: uint32(page), Arg: res.Tab}) // #nosec G115 -- pages are small
			},
			Book: func(id int64) *cbv2.Envelope {
				return k.env(cbv2.Envelope{Op: cbv2.OpOpenBook, EntityID: uint64(id), ViewID: viewID, // #nosec G115 -- serial ids
					ReturnID: uint64(node.ID)}) // #nosec G115 -- serial ids
			},
			Home: k.home(),
		})
	}
}

// showResults moves a panel to page of view's tab.
func (r *Router) showResults(
	ctx context.Context, b *Bot, panel *models.TelegramPanel, expected int32, viewID int64, tab uint8, page int,
) string {
	view, err := database.GetTelegramView(ctx, b.Binding.UserID, viewID)
	if err != nil {
		return expiredOrDown(err)
	}
	res, moved := r.runSearch(ctx, view, tab, page)
	node := &models.TelegramNode{UserID: b.Binding.UserID, ViewID: view.ID, Screen: int16(tabScreen(tab)),
		Page: int32(res.Page)} // #nosec G115 -- pages are small
	if err := upsertNode(ctx, node); err != nil {
		return screens.CopyLibraryDown
	}
	if !r.movePanel(ctx, b, panel, expected, node, r.paintResults(view, node, res)) {
		return screens.CopyScreenChanged
	}
	if moved {
		return screens.CopyListChanged
	}
	return ""
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
