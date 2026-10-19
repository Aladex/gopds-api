package router

import (
	"context"
	"errors"

	"gopds-api/database"
	"gopds-api/models"
	"gopds-api/telegram/cbv2"
	"gopds-api/telegram/screens"
)

// More screens (plan.md §2.4 numbering, then plan-ui.md's). Stored; never
// reuse or renumber.
const (
	ScrBookAuthors Screen = 6 + iota
	ScrAuthor
	ScrSeries
	ScrCollections
	ScrCollection
	ScrFavorites
	_ // ScrSuggestions: suggestions live on the empty results page
	ScrLanguage
	_ // ScrAuthorPick: author picks live in their prompt
	ScrSettings
	ScrFormat
	ScrDefaultLanguage
	_ // ScrConnection: on the site
	_ // ScrHelp
	_ // ScrFormatHelp
	ScrDonate
	ScrCardStyle
	ScrInline
)

// More view kinds (plan.md §2.4).
const (
	ViewAuthorBooks int16 = 2 + iota
	ViewAuthorSearch
	ViewSeriesBooks
	_ // ViewCollections: the list of collections needs no view
	ViewCollectionBooks
	ViewFavorites
	ViewFavoritesSearch
)

// screenOfView is the screen a book-list view is shown on.
func screenOfView(kind int16) Screen {
	switch kind {
	case ViewAuthorBooks, ViewAuthorSearch:
		return ScrAuthor
	case ViewSeriesBooks:
		return ScrSeries
	case ViewCollectionBooks:
		return ScrCollection
	case ViewFavorites, ViewFavoritesSearch:
		return ScrFavorites
	}
	return ScrResultsBooks
}

// painter reads what a node shows and returns its renderer. A false third
// value means the node cannot be shown; the string is then the toast that
// says why. A painter may move the node to another page (a list that
// shrank), and returns the toast that says so.
func (r *Router) painter(ctx context.Context, b *Bot, node *models.TelegramNode) (render, string, bool) {
	//exhaustive:ignore // a notice is never shown again
	switch Screen(node.Screen) {
	case ScrHome:
		return r.paintHome(false, r.inlineOn(ctx, b)), "", true
	case ScrResultsBooks, ScrResultsAuthors:
		return r.resultsPainter(ctx, b, node)
	case ScrBook:
		return r.cardPainter(ctx, b, node)
	case ScrBookAnnotation:
		return r.annotationPainter(ctx, node)
	case ScrAuthor, ScrSeries, ScrCollection, ScrFavorites:
		return r.listPainter(ctx, b, node)
	case ScrCollections:
		return r.collectionsPainter(ctx, node)
	case ScrBookAuthors:
		return r.bookAuthorsPainter(ctx, node)
	case ScrLanguage, ScrDefaultLanguage:
		return r.languagePainter(ctx, b, node)
	case ScrSettings, ScrFormat, ScrCardStyle, ScrInline, ScrDonate:
		return r.settingsPainter(ctx, b, node)
	}
	return nil, screens.CopyExpired, false
}

// show moves a panel to node and returns the toast.
func (r *Router) show(ctx context.Context, b *Bot, panel *models.TelegramPanel, expected int32, node *models.TelegramNode) string {
	paint, toast, ok := r.painter(ctx, b, node)
	if !ok {
		return toast
	}
	if err := upsertNode(ctx, node); err != nil {
		return screens.CopyLibraryDown
	}
	if !r.movePanel(ctx, b, panel, expected, node, paint) {
		return screens.CopyScreenChanged
	}
	return toast
}

// open sends node as a new panel and returns the toast.
func (r *Router) open(ctx context.Context, b *Bot, chatID int64, node *models.TelegramNode) string {
	paint, toast, ok := r.painter(ctx, b, node)
	if !ok {
		return toast
	}
	if err := upsertNode(ctx, node); err != nil {
		return screens.CopyLibraryDown
	}
	r.sendPanel(ctx, b, chatID, node, paint, false)
	return toast
}

// backTo is the way back to a node, labeled by what the node is; it paints
// nil when the node is home or none, since «Домой» is always there.
func (r *Router) backTo(ctx context.Context, b *Bot, parentID int64) func(k *keys) *screens.Button {
	label := backLabel(ctx, b, parentID)
	return func(k *keys) *screens.Button {
		if label == "" {
			return nil
		}
		return &screens.Button{Text: label, Env: k.env(cbv2.Envelope{Op: cbv2.OpBack, ReturnID: uint64(parentID)})} // #nosec G115 -- serial ids
	}
}

// backLabel names the way back to a node; "" when there is none to offer.
func backLabel(ctx context.Context, b *Bot, parentID int64) string {
	if parentID == 0 {
		return ""
	}
	parent, err := database.GetTelegramNode(ctx, b.Binding.UserID, parentID)
	if err != nil {
		return ""
	}
	//exhaustive:ignore // the rest go back with «← Назад»
	switch Screen(parent.Screen) {
	case ScrHome:
		return ""
	case ScrResultsBooks, ScrResultsAuthors:
		return screens.ButtonToResults
	case ScrBook:
		return screens.ButtonToBook
	case ScrCollections:
		return screens.ButtonToCollections
	case ScrAuthor:
		return screens.ButtonToAuthor
	case ScrSeries:
		return screens.ButtonToSeries
	case ScrCollection:
		return screens.ButtonToCollection
	case ScrFavorites:
		return screens.ButtonToFavorites
	case ScrBookAuthors:
		return screens.ButtonToAuthors
	case ScrSettings:
		return screens.ButtonToSettings
	}
	return screens.ButtonBack
}

// newView records a view; v gets its id.
func newView(ctx context.Context, v *models.TelegramView) error {
	return database.CreateTelegramView(ctx, v)
}

// viewOf loads a node's view; the toast when it cannot be read.
func viewOf(ctx context.Context, b *Bot, id int64) (*models.TelegramView, string, bool) {
	view, err := database.GetTelegramView(ctx, b.Binding.UserID, id)
	if err != nil {
		return nil, expiredOrDown(err), false
	}
	return view, "", true
}

// isGone reports an object that is not there.
func isGone(err error) bool { return errors.Is(err, errNoRows) }
