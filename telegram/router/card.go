package router

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/telegram/cbv2"
	"gopds-api/telegram/delivery"
	"gopds-api/telegram/screens"

	"github.com/go-pg/pg/v10"
	tgbot "github.com/go-telegram/bot/models"
)

var errNoRows = pg.ErrNoRows

// visible is the library's rule for handing a book out.
func visible(b *models.Book) bool { return b.Approved && !b.DuplicateHidden }

// cardNode is the node of a book's card opened from parent within view.
func cardNode(userID, viewID, bookID, parent int64) *models.TelegramNode {
	return &models.TelegramNode{UserID: userID, ViewID: viewID, Screen: int16(ScrBook), EntityID: bookID, ParentID: parent}
}

// cardPainter reads a book's card.
func (r *Router) cardPainter(ctx context.Context, b *Bot, node *models.TelegramNode) (render, string, bool) {
	book, toast := r.readCard(ctx, node)
	if book == nil {
		return nil, toast, false
	}
	paint, err := r.paintCard(ctx, b, node, book)
	if err != nil {
		return nil, screens.CopyLibraryDown, false
	}
	return paint, "", true
}

// cardSlow is an open the log tells at info, not only at debug: slow enough
// for the reader to wait on it.
const cardSlow = 300 * time.Millisecond

// openCard is a title pressed: the card in the panel. The press is answered
// as soon as the book is read — before the card is drawn — so its spinner
// stops at once. A card whose cover Telegram does not have yet for this bot
// is shown first without it («⏳ Открываю карточку…»), since the upload is
// the slow part, then in full.
func (r *Router) openCard(ctx context.Context, b *Bot, q *tgbot.CallbackQuery, panel *models.TelegramPanel, env *cbv2.Envelope) {
	clock := cardClock{start: time.Now(), wait: heldFor(ctx)}
	node := cardNode(b.Binding.UserID, int64(env.ViewID), int64(env.EntityID), int64(env.ReturnID)) // #nosec G115 -- decoded ids fit
	book, toast := r.readCard(ctx, node)
	var paint render
	if book != nil {
		var err error
		if paint, err = r.paintCard(ctx, b, node, book); err != nil {
			toast = screens.CopyLibraryDown
		}
	}
	if paint == nil {
		r.answer(ctx, b, q, toast)
		return
	}
	clock.read = time.Now()
	r.answer(ctx, b, q, "")
	clock.answered = time.Now()
	if err := upsertNode(ctx, node); err != nil {
		return
	}
	var opening render
	if r.richFor(ctx, b) && (panel.IsHome || panel.Mode == models.TelegramPanelRich) && r.coldCover(ctx, b, book) {
		clock.cold = true
		opening = r.paintOpening(ctx, b, node, book, &clock)
	}
	full := func(k *keys) screens.Screen {
		clock.painting = time.Now()
		return paint(k)
	}
	r.moveOpening(ctx, b, panel, panel.Revision, node, opening, full)
	clock.log(b.Ref, book.ID)
}

// readCard reads the book of a card node, or the toast saying why not.
func (r *Router) readCard(ctx context.Context, node *models.TelegramNode) (book *models.Book, toast string) {
	book, err := database.GetBookCard(ctx, node.EntityID)
	if err != nil {
		if errors.Is(err, errNoRows) {
			return nil, screens.CopyUnavailable
		}
		return nil, screens.CopyLibraryDown
	}
	if !visible(book) {
		return nil, screens.CopyUnavailable
	}
	r.creditBook(ctx, book)
	return book, ""
}

// paintOpening paints the card being made: the book's title and authors,
// the card's way back and «Домой».
func (r *Router) paintOpening(
	ctx context.Context, b *Bot, node *models.TelegramNode, book *models.Book, clock *cardClock,
) render {
	label := backLabel(ctx, b, node.ParentID)
	return func(k *keys) screens.Screen {
		clock.opening = time.Now()
		var back *screens.Button
		if label != "" {
			parent := uint64(node.ParentID) // #nosec G115 -- serial ids
			back = &screens.Button{Text: label, Env: k.env(cbv2.Envelope{Op: cbv2.OpBack, ReturnID: parent})}
		}
		return screens.CardOpeningScreen(book, back, k.home())
	}
}

// cardClock times one open of a card: how long the update waited for its
// chat, read the card, answered the press, got the panel ready (its node,
// its kind, the cover's state, the move), showed the card opening (with the
// chat action), and drew it (with the cover's upload, when cold).
type cardClock struct {
	start, read, answered, opening, painting time.Time
	wait                                     time.Duration
	cold                                     bool
}

// log tells the open's times, at info when it was slow.
func (c *cardClock) log(ref, bookID int64) {
	end := time.Now()
	total := end.Sub(c.start) + c.wait
	opening := time.Duration(0)
	if c.cold && !c.opening.IsZero() && !c.painting.IsZero() {
		opening = c.painting.Sub(c.opening)
	}
	draw, prepare := time.Duration(0), time.Duration(0)
	if !c.painting.IsZero() {
		draw = end.Sub(c.painting)
		first := c.painting
		if c.cold && !c.opening.IsZero() {
			first = c.opening
		}
		prepare = first.Sub(c.answered)
	}
	ms := func(d time.Duration) time.Duration { return d.Round(time.Millisecond) }
	line := fmt.Sprintf("Telegram bot %d: card of book %d opened in %s "+
		"(chat wait %s, read %s, answer %s, prepare %s, opening %s, card %s, cold cover %t)",
		ref, bookID, ms(total), ms(c.wait), ms(c.read.Sub(c.start)), ms(c.answered.Sub(c.read)),
		ms(prepare), ms(opening), ms(draw), c.cold)
	if total > cardSlow {
		logging.Info(line)
		return
	}
	logging.Debug(line)
}

// sendCard opens a book's card in a new panel: from a document's «Открыть
// карточку».
func (r *Router) sendCard(ctx context.Context, b *Bot, chatID, bookID int64) string {
	return r.open(ctx, b, chatID, cardNode(b.Binding.UserID, 0, bookID, 0))
}

// paintCard reads what a card shows besides the book and paints it.
func (r *Router) paintCard(ctx context.Context, b *Bot, node *models.TelegramNode, book *models.Book) (render, error) {
	ui, err := database.GetTelegramUserUI(ctx, b.Binding.UserID)
	if err != nil {
		return nil, err
	}
	fav, err := database.IsFavoriteBook(ctx, b.Binding.UserID, book.ID)
	if err != nil {
		return nil, err
	}
	formats, err := database.PreparingFormats(ctx, b.Binding.ID, book.ID)
	if err != nil {
		return nil, err
	}
	preparing := make(map[string]bool, len(formats))
	for _, f := range formats {
		preparing[f] = true
	}
	// A format on its way from the bot's own file is being sent, not prepared.
	sending := map[string]bool{}
	if len(formats) > 0 {
		var cached []string
		if cached, err = database.CachedTelegramFormats(ctx, b.Ref, book.ID, book.MD5, delivery.ConverterRev); err != nil {
			return nil, err
		}
		for _, f := range cached {
			sending[f] = preparing[f]
		}
	}
	back := backLabel(ctx, b, node.ParentID)
	kindle, err := kindleAddress(b)
	if err != nil {
		return nil, err
	}
	return func(k *keys) screens.Screen {
		card := &screens.Card{
			Book: book, Rich: k.rich, Favorite: fav, DefaultFormat: ui.DefaultFormat,
			Preparing: preparing, Sending: sending, SiteURL: r.cfg.SiteURL, BackText: back, Kindle: kindle != "",
		}
		if k.rich {
			card.Cover = r.cover(ctx, b, book)
		}
		entity, view, parent, self := uint64(book.ID), uint64(node.ViewID), uint64(node.ParentID), uint64(node.ID) // #nosec G115 -- serial ids
		screen, _ := screens.CardScreen(card, &screens.CardKeys{
			Download: func(format string) *cbv2.Envelope {
				return k.env(cbv2.Envelope{Op: cbv2.OpDownload, EntityID: entity, ViewID: view, ReturnID: parent,
					Arg: screens.FormatArg(format)})
			},
			Favorite: func(want bool) *cbv2.Envelope {
				arg := uint8(0)
				if want {
					arg = 1
				}
				return k.env(cbv2.Envelope{Op: cbv2.OpSetFavorite, EntityID: entity, ViewID: view, ReturnID: parent,
					Arg: arg})
			},
			Annotation: k.env(cbv2.Envelope{Op: cbv2.OpAnnotation, EntityID: entity, ViewID: view, ReturnID: self}),
			Author: func(id int64) *cbv2.Envelope {
				return k.env(cbv2.Envelope{Op: cbv2.OpOpenAuthor, EntityID: uint64(id), ViewID: view, ReturnID: self}) // #nosec G115 -- serial ids
			},
			BookAuthors: k.env(cbv2.Envelope{Op: cbv2.OpBookAuthors, EntityID: entity, ViewID: view, ReturnID: self}),
			Kindle:      k.env(cbv2.Envelope{Op: cbv2.OpKindle, EntityID: entity, ViewID: view, ReturnID: self}),
			Series: func(id int64) *cbv2.Envelope {
				return k.env(cbv2.Envelope{Op: cbv2.OpOpenSeries, EntityID: uint64(id), ViewID: view, ReturnID: self}) // #nosec G115 -- serial ids
			},
			Back: k.env(cbv2.Envelope{Op: cbv2.OpBack, ReturnID: parent}),
			Home: k.home(),
		})
		return screen
	}, nil
}

// download asks for a file from the card (plan-ui.md §1.8): a delivery job,
// and the card painted again with the format marked as on its way.
func (r *Router) download(
	ctx context.Context, b *Bot, q *tgbot.CallbackQuery, panel *models.TelegramPanel, env *cbv2.Envelope,
) (toast string, answer bool) {
	format := screens.FormatOf(env.Arg)
	if format == "" || r.cfg.Delivery == nil {
		return screens.CopyUnsupported, true
	}
	job, admission, err := r.cfg.Delivery.Enqueue(ctx, &delivery.Request{
		UserID: b.Binding.UserID, BindingID: b.Binding.ID, BotRef: b.Ref, ChatID: panel.ChatID,
		BookID: int64(env.EntityID), Format: format, Origin: models.DeliveryFromBot, // #nosec G115 -- decoded ids fit
		TapKey:          fmt.Sprintf("v2:%d:%s", b.Ref, q.ID),
		OriginMessageID: panel.MessageID,
		OriginRevision:  panel.Revision,
	})
	switch {
	case errors.Is(err, database.ErrInboxLeaseLost):
		return "", false
	case errors.Is(err, delivery.ErrNotFound), errors.Is(err, delivery.ErrUnavailable):
		return screens.CopyUnavailable, true
	case errors.Is(err, delivery.ErrUnknownFormat):
		return screens.CopyUnsupported, true
	case err != nil:
		logging.Warnf("Telegram bot %d: enqueueing book %d as %s: %v", b.Ref, env.EntityID, format, err)
		return screens.CopyLibraryDown, true
	case admission == database.BusyAccount:
		return delivery.ToastBusyAccount, true
	case admission == database.BusyService:
		return delivery.ToastBusyService, true
	case admission == database.InProgress || admission == database.AlreadyTapped:
		return delivery.RepeatToast(&job, r.cfg.Delivery.Cached(ctx, b.Ref, job.BookID, format)), true
	}
	node := cardNode(b.Binding.UserID, int64(env.ViewID), int64(env.EntityID), int64(env.ReturnID)) // #nosec G115 -- decoded ids fit
	r.show(ctx, b, panel, panel.Revision, node)
	return delivery.StartToast(format, r.cfg.Delivery.Cached(ctx, b.Ref, int64(env.EntityID), format)), true // #nosec G115 -- decoded ids fit
}

// setFavorite puts the book in or out of favorites — the state the button
// asked for — and paints the card again.
func (r *Router) setFavorite(ctx context.Context, b *Bot, panel *models.TelegramPanel, env *cbv2.Envelope) string {
	want := env.Arg == 1
	bookID := int64(env.EntityID) // #nosec G115 -- decoded ids fit
	if err := database.SetFavoriteBook(ctx, b.Binding.UserID, bookID, want); err != nil {
		if !errors.Is(err, database.ErrInboxLeaseLost) {
			logging.Warnf("Telegram bot %d: changing a favorite: %v", b.Ref, err)
		}
		return screens.CopyFavFailed
	}
	node := cardNode(b.Binding.UserID, int64(env.ViewID), int64(env.EntityID), int64(env.ReturnID)) // #nosec G115 -- decoded ids fit
	r.show(ctx, b, panel, panel.Revision, node)
	if want {
		return screens.CopyFavAdded
	}
	return screens.CopyFavRemoved
}

// annotationNode is a page of a book's annotation; parent is the card's node.
func annotationNode(userID, bookID, parent int64, page int) *models.TelegramNode {
	return &models.TelegramNode{UserID: userID, Screen: int16(ScrBookAnnotation), EntityID: bookID,
		Page: int32(page), ParentID: parent} // #nosec G115 -- pages are small
}

// annotationPainter reads a page of a book's annotation.
func (r *Router) annotationPainter(ctx context.Context, node *models.TelegramNode) (render, string, bool) {
	book, err := database.GetBookCard(ctx, node.EntityID)
	if err != nil || !visible(book) {
		return nil, screens.CopyUnavailable, false
	}
	bookID, parent, page := node.EntityID, node.ParentID, int(node.Page)
	return func(k *keys) screens.Screen {
		pageEnv := func(p int) *cbv2.Envelope {
			return k.env(cbv2.Envelope{Op: cbv2.OpAnnotationPage, EntityID: uint64(bookID), // #nosec G115 -- serial ids
				Page: uint32(p), ReturnID: uint64(parent)}) // #nosec G115 -- small values
		}
		return screens.AnnotationScreen(book, page, pageEnv(page-1), pageEnv(page+1),
			k.env(cbv2.Envelope{Op: cbv2.OpBack, ReturnID: uint64(parent)}), k.home()) // #nosec G115 -- serial ids
	}, "", true
}
