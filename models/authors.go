package models

// AuthorFilters filters for authors list
type AuthorFilters struct {
	Limit  int    `form:"limit" json:"limit"`
	Offset int    `form:"offset" json:"offset"`
	Author string `form:"author" json:"author"`
	Lang   string `form:"lang" json:"lang"`
}

// AuthorRequest request for an object of author for search bar
type AuthorRequest struct {
	ID int64 `json:"author_id" form:"author_id"`
}

// UnknownAuthorName is the legacy author a scan links to a book whose file
// names no author. It is a placeholder, not a name anyone gave the book.
const UnknownAuthorName = "Автор неизвестен"

// AuthorDisplay is one name of a book's author line as the book card shows
// it. LegacyAuthorID is the catalog author the name links to, and is nil
// when no legacy author of the book is that person: the name is then shown
// without a link.
type AuthorDisplay struct {
	Name           string `json:"name"`
	LegacyAuthorID *int64 `json:"legacy_author_id,omitempty"`
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
)

// BookAuthorDisplay is a book's author line and where it comes from.
type BookAuthorDisplay struct {
	Source   AuthorDisplaySource
	Fallback AuthorDisplayFallback
	Authors  []AuthorDisplay
}
