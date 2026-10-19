package router

import (
	"context"
	"errors"
	"strings"

	"gopds-api/database"
	"gopds-api/models"
	"gopds-api/telegram/cbv2"
	"gopds-api/telegram/ru"
	"gopds-api/telegram/screens"
)

// The book lists of plan-ui.md §1.5–1.7: an author's books, a series, a
// collection, the favorites, and a search within an author or the
// favorites. Each is a view; its pages are nodes of that view.

// errCollectionGone is a collection withdrawn while its list was open.
var errCollectionGone = errors.New("collection withdrawn")

// The lists' own words.
const (
	copyNoAuthorBooks  = "Книг этого автора нет."
	copyNoSeriesBooks  = "Книг этой серии нет."
	copyNoMatches      = "Ничего не найдено. Попробуй часть названия."
	copyEmptyFavorites = "В избранном пока нет книг"
	copyFavoritesHint1 = "Открой книгу и нажми «☆ В избранное»."
	copyFavoritesHint2 = "Найти книгу можно по названию или автору — просто напиши их сюда."
	copyOtherLanguage  = "Попробуй другой язык."
)

// bookPage is one page of a list and the rows it adds below its pager.
type bookPage struct {
	list  *screens.BookList
	extra func(k *keys, self, viewID uint64) [][]screens.Button
}

// listPainter reads a page of a book list. A page left empty by a catalog
// change opens the last one.
func (r *Router) listPainter(ctx context.Context, b *Bot, node *models.TelegramNode) (render, string, bool) {
	view, toast, ok := viewOf(ctx, b, node.ViewID)
	if !ok {
		return nil, toast, false
	}
	page := int(node.Page)
	got, err := r.loadList(ctx, b, view, page)
	if err == nil && page > 0 && len(got.list.Books) == 0 && got.list.Total > 0 {
		page = screens.Pages(got.list.Total) - 1
		got, err = r.loadList(ctx, b, view, page)
		toast = screens.CopyListChanged
	}
	back := r.backTo(ctx, b, node.ParentID)
	switch {
	case errors.Is(err, errCollectionGone):
		return func(k *keys) screens.Screen {
			return screens.Notice(screens.CopyCollectionGone, k.home()).WithBack(back(k))
		}, "", true
	case err != nil:
		return nil, expiredOrDown(err), false
	}
	node.Page = int32(page) // #nosec G115 -- pages are small
	return func(k *keys) screens.Screen {
		viewID, parent, self := uint64(view.ID), uint64(node.ParentID), uint64(node.ID) // #nosec G115 -- serial ids
		var extra [][]screens.Button
		if got.extra != nil {
			extra = got.extra(k, self, viewID)
		}
		return screens.BookListScreen(got.list, &screens.ListKeys{
			Page: func(p int) *cbv2.Envelope {
				return k.env(cbv2.Envelope{Op: cbv2.OpPage, ViewID: viewID, Page: uint32(p), ReturnID: parent}) // #nosec G115 -- pages are small
			},
			Book: func(id int64) *cbv2.Envelope {
				return k.env(cbv2.Envelope{Op: cbv2.OpOpenBook, EntityID: uint64(id), ViewID: viewID, ReturnID: self}) // #nosec G115 -- serial ids
			},
			Extra: extra,
			Back:  back(k),
			Home:  k.home(),
		})
	}, toast, true
}

// languageRow is a list's «Язык» button.
func languageRow(k *keys, self, viewID uint64, lang string) []screens.Button {
	label := screens.ButtonLanguage
	if lang != "" {
		label += ": " + ru.Language(lang)
	}
	return []screens.Button{{Text: label, Env: k.env(cbv2.Envelope{Op: cbv2.OpLanguagePicker, ViewID: viewID, ReturnID: self})}}
}

// loadList reads one page of a view's books.
//
//nolint:funlen // one case per kind of list, each its header and buttons
func (r *Router) loadList(ctx context.Context, b *Bot, view *models.TelegramView, page int) (*bookPage, error) {
	userID := b.Binding.UserID
	filters := models.BookFilters{Lang: view.Lang, Limit: screens.PageSize, Offset: page * screens.PageSize}
	list := &screens.BookList{Page: page}
	withLanguage := func(k *keys, self, viewID uint64) [][]screens.Button {
		return [][]screens.Button{languageRow(k, self, viewID, view.Lang)}
	}
	out := &bookPage{list: list, extra: withLanguage}
	var err error
	//exhaustive:ignore // a typed search is a results page, not a list
	switch view.Kind {
	case ViewAuthorBooks:
		author, aerr := database.GetTelegramAuthor(ctx, view.AuthorID)
		if aerr != nil {
			return nil, aerr
		}
		filters.Author, filters.Sort = int(view.AuthorID), models.BookSortTitle
		list.Books, list.Total, err = database.GetBooks(userID, filters)
		list.Header = []screens.Block{screens.Title{Text: author.FullName}, screens.Line{Text: screens.CountLine(list.Total, view.Lang)}}
		list.Sub, list.Empty = screens.SeriesSub, emptyIn(view.Lang, copyNoAuthorBooks)
		out.extra = func(k *keys, self, viewID uint64) [][]screens.Button {
			row := []screens.Button{{Text: screens.ButtonSearchAuthor,
				Env: k.env(cbv2.Envelope{Op: cbv2.OpPromptAuthorSearch, ViewID: viewID, ReturnID: self})}}
			return [][]screens.Button{append(row, languageRow(k, self, viewID, view.Lang)...)}
		}
	case ViewAuthorSearch:
		author, aerr := database.GetTelegramAuthor(ctx, view.AuthorID)
		if aerr != nil {
			return nil, aerr
		}
		err = r.searchList(ctx, list, &models.BookSearchRequest{Query: view.Query, AuthorID: view.AuthorID, Language: view.Lang,
			Limit: filters.Limit, Offset: filters.Offset})
		list.Header = []screens.Block{screens.Line{Text: "Поиск у автора: " + author.FullName + " — «" + view.Query + "»"},
			screens.Line{Text: screens.CountLine(list.Total, view.Lang)}}
		list.Sub, list.Empty = screens.SeriesSub, emptyIn(view.Lang, copyNoMatches)
	case ViewSeriesBooks:
		series, serr := database.GetTelegramSeries(ctx, view.SeriesID)
		if serr != nil {
			return nil, serr
		}
		filters.Series, filters.Sort = int(view.SeriesID), models.BookSortSeries
		list.Books, list.Total, err = database.GetBooks(userID, filters)
		list.Header = []screens.Block{screens.Title{Text: "Серия «" + series.Ser + "»"},
			screens.Line{Text: screens.CountLine(list.Total, view.Lang)}}
		list.Number, list.Sub, list.Empty = screens.SeriesNumber(view.SeriesID), screens.AuthorsLine, emptyIn(view.Lang, copyNoSeriesBooks)
	case ViewCollectionBooks:
		c, cerr := database.GetPublicCuratedCollection(ctx, view.CollectionID)
		if isGone(cerr) {
			return nil, errCollectionGone
		}
		if cerr != nil {
			return nil, cerr
		}
		filters.CuratedCollection = view.CollectionID
		list.Books, list.Total, err = database.GetBooks(userID, filters)
		list.Header = []screens.Block{screens.Title{Text: c.Name}, screens.Line{Text: screens.CountLine(list.Total, view.Lang)}}
		list.Sub, list.Empty = screens.AuthorsLine, emptyIn(view.Lang, screens.CopyNoCollectionBooks)
		out.extra = func(k *keys, self, viewID uint64) [][]screens.Button {
			rows := withLanguage(k, self, viewID)
			if strings.HasPrefix(c.SourceURL, "https://") {
				rows = append(rows, []screens.Button{{Text: screens.ButtonSource, URL: c.SourceURL}})
			}
			return rows
		}
	case ViewFavorites:
		filters.Fav, filters.Lang = true, ""
		list.Books, list.Total, err = database.GetBooks(userID, filters)
		list.Header = []screens.Block{screens.Title{Text: "Избранное"}, screens.Line{Text: ru.Books(list.Total)}}
		list.Sub = screens.AuthorsLine
		list.Header, list.Empty = favoritesHeader(list.Total, list.Header)
		out.extra = func(k *keys, self, _ uint64) [][]screens.Button {
			if list.Total == 0 {
				return nil
			}
			return [][]screens.Button{{{Text: screens.ButtonSearchFavs, Env: k.env(cbv2.Envelope{Op: cbv2.OpPromptFavSearch, ReturnID: self})}}}
		}
	case ViewFavoritesSearch:
		err = r.searchList(ctx, list, &models.BookSearchRequest{Query: view.Query, Favorites: true, UserID: userID,
			Limit: filters.Limit, Offset: filters.Offset})
		list.Header = []screens.Block{screens.Line{Text: "Поиск в избранном: «" + view.Query + "»"}, screens.Line{Text: ru.Books(list.Total)}}
		list.Sub, list.Empty = screens.AuthorsLine, []screens.Block{screens.Line{Text: copyNoMatches}}
		out.extra = func(k *keys, _, _ uint64) [][]screens.Button {
			return [][]screens.Button{{{Text: screens.ButtonAllFavorites, Env: k.env(cbv2.Envelope{Op: cbv2.OpFavorites})}}}
		}
	default:
		return nil, errNoRows
	}
	if err != nil {
		return nil, err
	}
	r.creditAuthors(ctx, list.Books)
	return out, nil
}

// favoritesHeader is the favorites' title, or their empty state.
func favoritesHeader(total int, header []screens.Block) (head, empty []screens.Block) {
	if total > 0 {
		return header, nil
	}
	return []screens.Block{screens.Title{Text: copyEmptyFavorites}},
		[]screens.Block{screens.Line{Text: copyFavoritesHint1}, screens.Line{Text: copyFavoritesHint2}}
}

// emptyIn is a list's empty state; with a language, a hint to change it.
func emptyIn(lang, line string) []screens.Block {
	blocks := []screens.Block{screens.Line{Text: line}}
	if lang != "" {
		blocks = append(blocks, screens.Line{Text: copyOtherLanguage})
	}
	return blocks
}

// searchList fills a list from the search service.
func (r *Router) searchList(ctx context.Context, list *screens.BookList, req *models.BookSearchRequest) error {
	sctx, cancel := context.WithTimeout(ctx, r.cfg.SearchTimeout)
	defer cancel()
	got, err := r.cfg.Search.SearchBooks(sctx, *req)
	if err != nil {
		return err
	}
	list.Books, list.Total = got.Books, got.Total
	return nil
}

// collectionsPainter reads a page of the published collections.
func (r *Router) collectionsPainter(ctx context.Context, node *models.TelegramNode) (render, string, bool) {
	page, toast := int(node.Page), ""
	collections, total, err := database.ListPublicCuratedCollections(ctx, page+1, screens.PageSize)
	if err == nil && page > 0 && len(collections) == 0 && total > 0 {
		page = screens.Pages(total) - 1
		collections, total, err = database.ListPublicCuratedCollections(ctx, page+1, screens.PageSize)
		toast = screens.CopyListChanged
	}
	if err != nil {
		return nil, screens.CopyLibraryDown, false
	}
	node.Page = int32(page) // #nosec G115 -- pages are small
	return func(k *keys) screens.Screen {
		parent, self := uint64(node.ParentID), uint64(node.ID) // #nosec G115 -- serial ids
		return screens.CollectionsScreen(collections, total, page,
			func(id int64) *cbv2.Envelope {
				return k.env(cbv2.Envelope{Op: cbv2.OpOpenCollection, EntityID: uint64(id), ReturnID: self}) // #nosec G115 -- serial ids
			},
			func(p int) *cbv2.Envelope {
				return k.env(cbv2.Envelope{Op: cbv2.OpCollections, Page: uint32(p), ReturnID: parent}) // #nosec G115 -- pages are small
			},
			k.home())
	}, toast, true
}

// bookAuthorsPainter reads the authors of a book.
func (r *Router) bookAuthorsPainter(ctx context.Context, node *models.TelegramNode) (render, string, bool) {
	book, err := database.GetBookCard(ctx, node.EntityID)
	if err != nil || !visible(book) {
		return nil, screens.CopyUnavailable, false
	}
	return func(k *keys) screens.Screen {
		viewID, parent, self := uint64(node.ViewID), uint64(node.ParentID), uint64(node.ID) // #nosec G115 -- serial ids
		return screens.BookAuthorsScreen(book, func(id int64) *cbv2.Envelope {
			return k.env(cbv2.Envelope{Op: cbv2.OpOpenAuthor, EntityID: uint64(id), ViewID: viewID, ReturnID: self}) // #nosec G115 -- serial ids
		}, k.env(cbv2.Envelope{Op: cbv2.OpBack, ReturnID: parent}))
	}, "", true
}

// inheritLang is the language a list opened from view starts with: the
// view's, or the account's search language when there is no view.
func inheritLang(ctx context.Context, b *Bot, viewID int64) string {
	if viewID != 0 {
		if view, err := database.GetTelegramView(ctx, b.Binding.UserID, viewID); err == nil {
			return view.Lang
		}
	}
	if ui, err := database.GetTelegramUserUI(ctx, b.Binding.UserID); err == nil {
		return ui.SearchLang
	}
	return ""
}

// openList moves a panel to page one of a new list view.
func (r *Router) openList(ctx context.Context, b *Bot, panel *models.TelegramPanel, v *models.TelegramView, parent int64) string {
	v.UserID = b.Binding.UserID
	if err := newView(ctx, v); err != nil {
		return screens.CopyLibraryDown
	}
	node := &models.TelegramNode{UserID: v.UserID, ViewID: v.ID, Screen: int16(screenOfView(v.Kind)), ParentID: parent}
	return r.show(ctx, b, panel, panel.Revision, node)
}
