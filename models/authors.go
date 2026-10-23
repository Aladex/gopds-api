package models

import (
	"encoding/json"
	"fmt"

	"gopds-api/telegram/ru"

	"github.com/google/uuid"
)

// AuthorFilters filters for authors list
type AuthorFilters struct {
	Limit  int    `form:"limit" json:"limit"`
	Offset int    `form:"offset" json:"offset"`
	Author string `form:"author" json:"author"`
	Lang   string `form:"lang" json:"lang"`
}

// AuthorRequest request for an object of author for search bar
type AuthorRequest struct {
	ID AuthorID `json:"author_id" form:"author_id" swaggertype:"string"`
}

// AuthorID is an author's ID as a client sends it: a legacy catalog author's
// number, or an author entity's UUID string. Which one names an author is
// the identity's call (services.ParseAuthorRef); here it is only text.
type AuthorID string

// UnmarshalJSON takes a JSON number or a JSON string.
func (a *AuthorID) UnmarshalJSON(raw []byte) error {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		*a = AuthorID(text)
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return fmt.Errorf("author_id must be a number or a string: %w", err)
	}
	*a = AuthorID(number.String())
	return nil
}

// UnknownAuthorName is the legacy author a scan links to a book whose file
// names no author. It is a placeholder, not a name anyone gave the book.
const UnknownAuthorName = "Автор неизвестен"

// AuthorDisplay is one name of a book's author line as the book card shows
// it. LegacyAuthorID is the catalog author the name links to, and is nil
// when no legacy author of the book is that person: the name is then shown
// without a link.
//
// EntityIDs are the author entities the name names (design E, §1.2), in
// segment order, from the same read as the line: empty when it names none
// or names several people one of whom has none (§6 item 5). No surface
// shows them; under the entity identity a surface turns them into Entities
// and drops LegacyAuthorID (WithEntities).
type AuthorDisplay struct {
	Name           string         `json:"name"`
	LegacyAuthorID *int64         `json:"legacy_author_id,omitempty"`
	Entities       []AuthorEntity `json:"entities,omitempty"`
	EntityIDs      []string       `json:"-"`
}

// AuthorEntity is an author entity as readers are shown it: its ID, the name
// on its page and, when another live entity carries the same name, a label
// that tells the namesakes apart (design E, §1.1, §6 item 3a). The label is
// the dominant genre of its books (Label) when no namesake shares it; when
// one does — most often one person the build has not yet merged — it is
// the number of its books instead (LabelBooks), which says nothing untrue.
// BooksCount is filled only by a search, which counts the books it would
// open.
type AuthorEntity struct {
	ID         uuid.UUID `json:"id"`
	FullName   string    `json:"full_name"`
	Label      string    `json:"label,omitempty"`
	LabelBooks int       `json:"label_books,omitempty"`
	BooksCount int       `json:"books_count,omitempty"`
}

// Title is the entity's name with its namesake label, as a surface that
// shows plain text writes it.
func (e *AuthorEntity) Title() string {
	switch {
	case e.Label != "":
		return e.FullName + " · " + e.Label
	case e.LabelBooks > 0:
		return e.FullName + " · " + ru.Books(e.LabelBooks)
	}
	return e.FullName
}

// WithEntities is the line as the entity identity shows it: every name links
// to the live entities it names, all of them or none, and to no legacy
// author. entities are the live entities the page's lines name.
func WithEntities(line []AuthorDisplay, entities map[uuid.UUID]AuthorEntity) []AuthorDisplay {
	out := make([]AuthorDisplay, len(line))
	for i, a := range line {
		out[i] = AuthorDisplay{Name: a.Name, EntityIDs: a.EntityIDs}
		linked := make([]AuthorEntity, 0, len(a.EntityIDs))
		for _, raw := range a.EntityIDs {
			id, err := uuid.Parse(raw)
			e, ok := entities[id]
			if err != nil || !ok {
				linked = nil
				break
			}
			linked = append(linked, e)
		}
		if len(linked) > 0 {
			out[i].Entities = linked
		}
	}
	return out
}

// AuthorDisplaySource says which layer a book's author line comes from.
type AuthorDisplaySource string

// The two sources of an author line.
const (
	// AuthorDisplayLayer is the author credits of the book's current
	// metadata snapshot, in file order.
	AuthorDisplayLayer AuthorDisplaySource = "layer"
	// AuthorDisplayLegacy is the catalog authors linked to the book.
	AuthorDisplayLegacy AuthorDisplaySource = "legacy"
)

// AuthorDisplayFallback says why a book's author line stayed on the legacy
// authors; it is empty for a line from the layer.
type AuthorDisplayFallback string

// Why an author line stays on the legacy authors.
const (
	// AuthorDisplayNoCredits: the book has no current snapshot, or the
	// snapshot names no author.
	AuthorDisplayNoCredits AuthorDisplayFallback = "no_credits"
	// AuthorDisplayLegacyUnmatched: the book's legacy authors carry a word
	// none of its credits has — an administrator's edit, most likely — so the
	// catalog keeps its say.
	AuthorDisplayLegacyUnmatched AuthorDisplayFallback = "legacy_unmatched"
	// AuthorDisplayGluedCredit: one author credit names two or more of the
	// book's legacy authors — the file wrote several people into one field —
	// so the catalog, which has them apart, keeps its say.
	AuthorDisplayGluedCredit AuthorDisplayFallback = "glued_credit"
)

// BookAuthorDisplay is a book's author line and where it comes from.
type BookAuthorDisplay struct {
	Source   AuthorDisplaySource
	Fallback AuthorDisplayFallback
	Authors  []AuthorDisplay
}

// LegacyAuthorLine is the book's legacy authors as an author line, each name
// linking to its own catalog author.
func (b *Book) LegacyAuthorLine() []AuthorDisplay {
	authors := make([]AuthorDisplay, len(b.Authors))
	for i, a := range b.Authors {
		id := a.ID
		authors[i] = AuthorDisplay{Name: a.FullName, LegacyAuthorID: &id}
	}
	return authors
}
