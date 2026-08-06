package models

// BookSearchRequest carries a validated book search from an adapter to the
// repository. Adapters never pass gin.Context, URL params or Telegram objects
// past this boundary.
type BookSearchRequest struct {
	Query               string
	AuthorQuery         string
	ExactBookID         int64
	UserID              int64
	Language            string
	AuthorID            int64
	SeriesID            int64
	GenreID             int64
	CollectionID        int64
	CuratedCollectionID int64
	Favorites           bool
	Unapproved          bool
	IncludeHidden       bool
	Limit               int
	Offset              int
}

// BookSearchPage is one ranked page plus the uncapped exact total computed
// before pagination. QueryHash correlates log lines without the raw query.
type BookSearchPage struct {
	Books     []Book
	Total     int
	Limit     int
	Offset    int
	QueryHash string
}

// AuthorSearchRequest carries a validated author search.
type AuthorSearchRequest struct {
	Query    string
	Language string
	Limit    int
	Offset   int
}

// AuthorSearchPage is one author page plus the exact total.
type AuthorSearchPage struct {
	Authors   []Author
	Total     int
	Limit     int
	Offset    int
	QueryHash string
}

// SuggestionKind selects which autocomplete lanes answer a request.
type SuggestionKind string

const (
	SuggestionAll    SuggestionKind = "all"
	SuggestionBook   SuggestionKind = "title"
	SuggestionAuthor SuggestionKind = "author"
)

// SuggestionRequest carries a validated autocomplete request.
type SuggestionRequest struct {
	Query    string
	Kind     SuggestionKind
	Language string
	AuthorID int64
	Limit    int
}

// SuggestionResult is the compact autocomplete projection.
type SuggestionResult struct {
	Suggestions []AutocompleteSuggestion
	QueryHash   string
}
