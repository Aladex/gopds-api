package router

import (
	"context"
	"errors"

	"gopds-api/database"
	"gopds-api/models"
	"gopds-api/telegram/cbv2"
	"gopds-api/telegram/screens"
)

// A card's files in the bot (design/variants-bot/PLAN.md): the card's
// button, its variant list on the same panel, one document's revisions under
// it, and a pick that opens the card on the file picked. Everything here
// reads cards only while telegram.cards is on and the card read model has
// been walked once.

// variantsOfDocument is OpVariants' Arg for one document's revisions.
const variantsOfDocument = 1

// cardsOn says the bot lists cards: switched on, and the read model walked.
func (r *Router) cardsOn(ctx context.Context) bool {
	return r.cfg.Cards && r.ready.Ready(ctx, func(ctx context.Context) (bool, error) {
		return database.BookVariantsReady(ctx, database.GetDB())
	})
}

// cardOf is what a card shows of its card besides the book: the star, the
// card's when cards are on, and its files when it has more than one. A
// single book reads one statement, as the star alone did; a card of several
// files reads the same few statements however many files it holds.
func (r *Router) cardOf(ctx context.Context, userID, bookID int64) (bool, *screens.CardVariants, error) {
	if !r.cardsOn(ctx) {
		fav, err := database.IsFavoriteBook(ctx, userID, bookID)
		return fav, nil, err
	}
	card, err := database.TelegramCardOf(ctx, userID, bookID)
	if err != nil || card.Files <= 1 {
		return card.Fav, nil, err
	}
	variants, err := r.cardVariants(ctx, userID, bookID)
	switch {
	case errors.Is(err, errNoRows):
		// Gone between the two reads: the card is shown as it was read.
		return card.Fav, nil, nil
	case err != nil:
		return false, nil, err
	case len(variants.Rows) <= 1:
		return card.Fav, nil, nil
	}
	return card.Fav, variants, nil
}

// cardVariants reads the visible files of the card shown on bookID, with the
// source fields their labels are made of. A book that is not visible is
// errNoRows.
func (r *Router) cardVariants(ctx context.Context, userID, bookID int64) (*screens.CardVariants, error) {
	books, variants, err := database.BookVariantsOf(ctx, bookID, userID)
	if err != nil {
		return nil, err
	}
	var details map[int64]models.BookSourceDetail
	if r.cfg.Sources != nil {
		ids := make([]int64, len(books))
		for i := range books {
			ids[i] = books[i].ID
		}
		if details, err = r.cfg.Sources.BookSourceDetails(ctx, ids); err != nil {
			return nil, err
		}
	}
	rows := make([]screens.VariantRow, len(books))
	for i := range books {
		v, d := &variants[i], details[books[i].ID]
		rows[i] = screens.VariantRow{Book: &books[i], Lineage: v.Lineage, Latest: v.Latest, Rank: v.Rank, Tag: v.Tag,
			DocVersion: v.DocVersion, Default: v.Default, Translators: d.Translators, TranslatorsMore: d.TranslatorsMore}
		if d.Publisher != nil {
			rows[i].Publisher = *d.Publisher
		}
	}
	return &screens.CardVariants{Rows: rows, ShownID: bookID}, nil
}

// variantsNode is a page of a card's variant list opened from parent: the
// card shown on entity, or, for one document, its newest file in entity and
// the variant list as parent.
func variantsNode(userID, viewID, entity, parent int64, page int, document bool) *models.TelegramNode {
	screen := ScrBookVariants
	if document {
		screen = ScrBookRevisions
	}
	return &models.TelegramNode{UserID: userID, ViewID: viewID, Screen: int16(screen), EntityID: entity,
		Page: int32(page), ParentID: parent} // #nosec G115 -- pages are small
}

// variantsPainter reads a page of a variant list. The list is the card's
// node's child; one document's list is the variant list's. A pick opens the
// card on the file with the card's own way back, so «Назад» from it returns
// to the list the card came from.
func (r *Router) variantsPainter(ctx context.Context, b *Bot, node *models.TelegramNode) (render, string, bool) {
	if !r.cardsOn(ctx) {
		return nil, screens.CopyUnsupported, false
	}
	userID := b.Binding.UserID
	document := Screen(node.Screen) == ScrBookRevisions
	shownID, cardNodeID := node.EntityID, node.ParentID
	if document {
		full, err := database.GetTelegramNode(ctx, userID, node.ParentID)
		if err != nil {
			return nil, expiredOrDown(err), false
		}
		shownID, cardNodeID = full.EntityID, full.ParentID
	}
	var cardParent int64
	if card, err := database.GetTelegramNode(ctx, userID, cardNodeID); err == nil {
		cardParent = card.ParentID
	}
	variants, err := r.cardVariants(ctx, userID, shownID)
	switch {
	case errors.Is(err, errNoRows):
		return nil, screens.CopyUnavailable, false
	case err != nil:
		return nil, screens.CopyLibraryDown, false
	}
	list := &screens.VariantsList{Card: variants, Page: int(node.Page)}
	if document {
		for i := range variants.Rows {
			if variants.Rows[i].Book.ID == node.EntityID {
				list.Lineage = variants.Rows[i].Lineage
			}
		}
		if list.Lineage == "" {
			// The document left the card: its list is not there any more.
			return nil, screens.CopyUnavailable, false
		}
	}
	toast := ""
	if pages := list.VariantPages(); list.Page >= pages {
		list.Page, toast = pages-1, screens.CopyListChanged
	}
	node.Page = int32(list.Page) // #nosec G115 -- pages are small
	back := r.backTo(ctx, b, node.ParentID)
	return func(k *keys) screens.Screen {
		viewID, parent, self := uint64(node.ViewID), uint64(node.ParentID), uint64(node.ID) // #nosec G115 -- serial ids
		listParent, entity := uint64(cardParent), uint64(node.EntityID)                     // #nosec G115 -- serial ids
		arg := uint8(0)
		if document {
			arg = variantsOfDocument
		}
		return screens.VariantsScreen(list, &screens.VariantsKeys{
			Pick: func(id int64) *cbv2.Envelope {
				return k.env(cbv2.Envelope{Op: cbv2.OpOpenBook, EntityID: uint64(id), // #nosec G115 -- serial ids
					ViewID: viewID, ReturnID: listParent})
			},
			Fold: func(head int64) *cbv2.Envelope {
				return k.env(cbv2.Envelope{Op: cbv2.OpVariants, EntityID: uint64(head), ViewID: viewID, // #nosec G115 -- serial ids
					Arg: variantsOfDocument, ReturnID: self})
			},
			Page: func(p int) *cbv2.Envelope {
				return k.env(cbv2.Envelope{Op: cbv2.OpVariants, EntityID: entity, ViewID: viewID, Arg: arg,
					Page: uint32(p), ReturnID: parent}) // #nosec G115 -- pages are small
			},
			Back: back(k),
			Home: k.home(),
		})
	}, toast, true
}
