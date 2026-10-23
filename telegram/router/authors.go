package router

import (
	"context"
	"errors"

	"gopds-api/database"
	"gopds-api/models"

	"github.com/google/uuid"
)

// The bot's authors under the two identities (authors.identity, design E
// §4.2). A bot ID of an author fits a signed 64-bit field of a button
// (telegram/cbv2), which an entity UUID does not: under the entity identity
// the bot names an entity by its handle (database.TelegramAuthorHandles),
// every handle above any legacy author ID. A button or a list made under one
// identity therefore names no author under the other, and the bot says the
// author is not found — never another author's books.

// errAuthorGone is a bot ID that names no author under the identity in
// force: the other identity's, a handle of an entity gone or merged.
var errAuthorGone = errors.New("author not found")

// botAuthor is the author a bot ID names: its name for the screens, and the
// legacy author or the entity a list of its books is scoped by.
type botAuthor struct {
	name   string
	legacy int64
	entity uuid.UUID
}

// authorOf reads the author a bot ID names under the identity in force. A
// legacy ID of no author keeps the error it always had.
func (r *Router) authorOf(ctx context.Context, id int64) (botAuthor, error) {
	if !r.cfg.Entities {
		if id >= database.TelegramAuthorHandleBase {
			return botAuthor{}, errAuthorGone
		}
		a, err := database.GetTelegramAuthor(ctx, id)
		if err != nil {
			return botAuthor{}, err
		}
		return botAuthor{name: a.FullName, legacy: id}, nil
	}
	if id < database.TelegramAuthorHandleBase {
		return botAuthor{}, errAuthorGone
	}
	entityID, err := database.TelegramAuthorByHandle(ctx, id)
	if isNoRows(err) {
		return botAuthor{}, errAuthorGone
	}
	if err != nil {
		return botAuthor{}, err
	}
	e, err := database.AuthorEntityByID(ctx, database.GetDB(), entityID)
	if errors.Is(err, database.ErrAuthorEntityNotFound) {
		return botAuthor{}, errAuthorGone
	}
	if err != nil {
		return botAuthor{}, err
	}
	return botAuthor{name: e.Title(), entity: entityID}, nil
}

// entityAuthors are entities as the screens list authors: by their handles,
// with their names and labels.
func (r *Router) entityAuthors(ctx context.Context, entities []models.AuthorEntity) ([]models.Author, error) {
	if len(entities) == 0 {
		return nil, nil
	}
	ids := make([]uuid.UUID, len(entities))
	for i := range entities {
		ids[i] = entities[i].ID
	}
	handles, err := database.TelegramAuthorHandles(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]models.Author, 0, len(entities))
	for i := range entities {
		if h, ok := handles[entities[i].ID]; ok {
			out = append(out, models.Author{ID: h, FullName: entities[i].Title(), BooksCount: entities[i].BooksCount})
		}
	}
	return out, nil
}

// entityBookAuthors gives a credited book (creditBook) the authors its card
// opens under the entity identity: each entity its line links to, once, in
// line order — a name of several people gives each of them. A name linked
// to none opens nothing; the catalog's legacy authors are not offered.
func (r *Router) entityBookAuthors(ctx context.Context, book *models.Book) error {
	var entities []models.AuthorEntity
	seen := map[uuid.UUID]bool{}
	for _, a := range book.AuthorLine {
		for _, e := range a.Entities {
			if !seen[e.ID] {
				seen[e.ID] = true
				entities = append(entities, e)
			}
		}
	}
	authors, err := r.entityAuthors(ctx, entities)
	if err != nil {
		book.Authors = nil
		return err
	}
	book.Authors = authors
	return nil
}
