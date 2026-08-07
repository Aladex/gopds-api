package services

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"gopds-api/logging"
	"gopds-api/models"
)

// Structured log field names, and the closed vocabularies two of them carry.
// Named because dashboards query them: a typo in a literal is a field that
// silently stops matching.
const (
	fieldMode       = "mode"
	fieldQueryRunes = "query_runes"
	fieldQueryHash  = "query_hash"
	fieldLanguage   = "language"
	fieldScope      = "scope"
	fieldReturned   = "returned"
	fieldTotal      = "total"
	fieldDuration   = "duration_ms"
	fieldErrorClass = "error_class"

	modeBooks   = "books"
	modeAuthors = "authors"
	modeSuggest = "suggestions"

	scopeNone   = "none"
	scopeAuthor = "author"

	classValidation = "validation"
	classCanceled   = "canceled"
	classRepository = "repository"

	hashUnavailable = "unavailable"
)

// ErrEmptyQuery is a search request with no text and no navigation target.
var ErrEmptyQuery = errors.New("search query is empty")

// ErrInvalidPagination is a negative offset: a client bug, not a filter.
var ErrInvalidPagination = errors.New("invalid pagination")

// ErrInvalidSuggestionKind is a suggestion request for a lane that does not
// exist: a client bug, not a filter.
var ErrInvalidSuggestionKind = errors.New("unknown suggestion kind")

// Page sizing for search results. An unnamed or unusable limit falls back to
// the default, which is also the ceiling — the same 100 the previous book and
// author paths clamped to.
const (
	defaultSearchLimit = 100
	maxSearchLimit     = 100
)

// Suggestion picker sizing. The compact picker never answers more than this,
// and an unnamed limit takes the same ceiling; the repository splits the
// budget between the lanes. Keep in sync with the picker geometry in
// database/search_repository.go — the service cannot import database, so the
// value is repeated here.
const maxSuggestionLimit = 15

// minSuggestionRunes is the shortest prefix worth a fuzzy lookup. Shorter
// input is the reader still typing; the service answers with an empty picker
// without touching the repository.
const minSuggestionRunes = 3

// allLanguages is the language code meaning "the whole library". It must stay
// in sync with database.AllLanguages, which the ordinary list path compares
// against; the service cannot import database, so the value is repeated here.
const allLanguages = "all"

// SearchRepository is the storage port the service drives.
type SearchRepository interface {
	SearchBooks(ctx context.Context, req models.BookSearchRequest) (models.BookSearchPage, error)
	SearchAuthors(ctx context.Context, req models.AuthorSearchRequest) (models.AuthorSearchPage, error)
	Suggestions(ctx context.Context, req models.SuggestionRequest) (models.SuggestionResult, error)
}

// PublicSearch is the adapter-facing search surface: REST handlers, bots and
// other adapters receive this, never the concrete service.
type PublicSearch interface {
	SearchBooks(ctx context.Context, req models.BookSearchRequest) (models.BookSearchPage, error)
	SearchAuthors(ctx context.Context, req models.AuthorSearchRequest) (models.AuthorSearchPage, error)
	Suggestions(ctx context.Context, req models.SuggestionRequest) (models.SuggestionResult, error)
}

var _ PublicSearch = (*SearchService)(nil)

// SearchService validates and normalizes search requests before they reach
// the repository. It does not rank or filter rows — that is the database's
// job — it owns the boundary rules: trimming, pagination, language codes.
type SearchService struct {
	repo SearchRepository
}

// NewSearchService wires the service to a repository.
func NewSearchService(repo SearchRepository) *SearchService {
	return &SearchService{repo: repo}
}

// SearchBooks validates one book search and passes the normalized request on.
//
//nolint:gocritic // the port takes the request by value; this implements it
func (s *SearchService) SearchBooks(ctx context.Context, req models.BookSearchRequest) (models.BookSearchPage, error) {
	start := time.Now()
	req.Query = strings.TrimSpace(req.Query)
	req.Language = normalizeLanguage(req.Language)
	if req.Query == "" && req.ExactBookID <= 0 {
		err := ErrEmptyQuery
		logCompletion(modeBooks, req.Query, req.Language, bookScope(req), 0, 0, "", err, start)
		return models.BookSearchPage{}, err
	}
	var err error
	if req.Limit, req.Offset, err = normalizePagination(req.Limit, req.Offset); err != nil {
		logCompletion(modeBooks, req.Query, req.Language, bookScope(req), 0, 0, "", err, start)
		return models.BookSearchPage{}, err
	}
	// Visibility is decided here, on the one path every client shares: only a
	// request that declared a moderator may widen what it sees. Callers report
	// identity; they never get the last word on these two flags, and a client
	// that forgets the rule cannot silently show more.
	if !req.Moderator {
		req.Unapproved = false
		req.IncludeHidden = false
	}
	page, err := s.repo.SearchBooks(ctx, req)
	logCompletion(modeBooks, req.Query, req.Language, bookScope(req), len(page.Books), page.Total, page.QueryHash, err, start)
	return page, err
}

// SearchAuthors validates one author search and passes the normalized request
// on. Authors have no exact-ID escape hatch: no text, no search.
func (s *SearchService) SearchAuthors(ctx context.Context, req models.AuthorSearchRequest) (models.AuthorSearchPage, error) {
	start := time.Now()
	req.Query = strings.TrimSpace(req.Query)
	req.Language = normalizeLanguage(req.Language)
	if req.Query == "" {
		err := ErrEmptyQuery
		logCompletion(modeAuthors, req.Query, req.Language, scopeNone, 0, 0, "", err, start)
		return models.AuthorSearchPage{}, err
	}
	var err error
	if req.Limit, req.Offset, err = normalizePagination(req.Limit, req.Offset); err != nil {
		logCompletion(modeAuthors, req.Query, req.Language, scopeNone, 0, 0, "", err, start)
		return models.AuthorSearchPage{}, err
	}
	page, err := s.repo.SearchAuthors(ctx, req)
	logCompletion(modeAuthors, req.Query, req.Language, scopeNone, len(page.Authors), page.Total, page.QueryHash, err, start)
	return page, err
}

// Suggestions validates one autocomplete request and passes the normalized
// request on. Two boundary rules differ from the search paths: an empty kind
// means the combined picker, and a prefix shorter than three runes is the
// reader still typing — answered with an empty picker, not an error, and
// never worth a repository round trip. The picker is a list, so it is always
// a non-nil slice, even when the repository handed back nothing.
func (s *SearchService) Suggestions(ctx context.Context, req models.SuggestionRequest) (models.SuggestionResult, error) {
	start := time.Now()
	req.Query = strings.TrimSpace(req.Query)
	req.Language = normalizeLanguage(req.Language)
	if req.Kind == "" {
		req.Kind = models.SuggestionAll
	}
	switch req.Kind {
	case models.SuggestionAll, models.SuggestionBook, models.SuggestionAuthor:
	default:
		err := ErrInvalidSuggestionKind
		logCompletion(modeSuggest, req.Query, req.Language, string(req.Kind), 0, 0, "", err, start)
		return models.SuggestionResult{}, err
	}
	if runeLength(req.Query) < minSuggestionRunes {
		empty := models.SuggestionResult{Suggestions: []models.AutocompleteSuggestion{}}
		logCompletion(modeSuggest, req.Query, req.Language, string(req.Kind), 0, 0, "", nil, start)
		return empty, nil
	}
	req.Limit = normalizeSuggestionLimit(req.Limit)
	result, err := s.repo.Suggestions(ctx, req)
	if err != nil {
		logCompletion(modeSuggest, req.Query, req.Language, string(req.Kind), 0, 0, "", err, start)
		return models.SuggestionResult{}, err
	}
	if result.Suggestions == nil {
		result.Suggestions = []models.AutocompleteSuggestion{}
	}
	logCompletion(modeSuggest, req.Query, req.Language, string(req.Kind), len(result.Suggestions), 0, result.QueryHash, nil, start)
	return result, nil
}

// normalizeSuggestionLimit clamps the picker to its compact size: an unnamed
// or oversized limit becomes the picker ceiling.
func normalizeSuggestionLimit(limit int) int {
	if limit <= 0 || limit > maxSuggestionLimit {
		return maxSuggestionLimit
	}
	return limit
}

// normalizePagination clamps the limit into the usable range and rejects a
// negative offset. Zero and oversized limits both become the default page
// size, which matches what the previous list paths did with them.
func normalizePagination(limit, offset int) (newLimit, newOffset int, err error) {
	if offset < 0 {
		return 0, 0, ErrInvalidPagination
	}
	return normalizeLimit(limit), offset, nil
}

// normalizeLimit maps an unusable limit to the default page size and clamps
// the rest to the maximum.
func normalizeLimit(limit int) int {
	if limit <= 0 || limit > maxSearchLimit {
		return defaultSearchLimit
	}
	return limit
}

// normalizeLanguage turns the whole-library code into no filter at all, the
// same reading the ordinary book list gives it.
func normalizeLanguage(lang string) string {
	if lang == allLanguages {
		return ""
	}
	return lang
}

// runeLength counts runes, not bytes: len("ёж") is 4 bytes but 2 runes, and
// any byte-based gate misreads Cyrillic.
func runeLength(s string) int {
	return utf8.RuneCountInString(s)
}

// logCompletion emits the single completion entry every service call ends
// with. The query text itself never appears — only its rune count and the
// correlation hash the database computed; without a returned hash the field
// is "unavailable", because a second, divergent normalizer in Go would
// correlate with nothing.
func logCompletion(mode, query, language, scope string, returned, total int, queryHash string, err error, start time.Time) {
	if queryHash == "" {
		queryHash = hashUnavailable
	}
	logging.WithFields(logging.Fields{
		fieldMode:       mode,
		fieldQueryRunes: runeLength(query),
		fieldQueryHash:  queryHash,
		fieldLanguage:   language,
		fieldScope:      scope,
		fieldReturned:   returned,
		fieldTotal:      total,
		fieldDuration:   time.Since(start).Milliseconds(),
		fieldErrorClass: errorClass(err),
	}).Info("search completed")
}

// bookScope names the first active scope in a fixed precedence, keeping the
// log field single-valued when a request combines several.
//
//nolint:gocritic // reads one request; copying it is cheaper than the pointer discipline it would force on callers
func bookScope(req models.BookSearchRequest) string {
	switch {
	case req.ExactBookID > 0:
		return "exact_book_id"
	case req.AuthorID > 0:
		return scopeAuthor
	case req.SeriesID > 0:
		return "series"
	case req.GenreID > 0:
		return "genre"
	case req.CuratedCollectionID > 0:
		return "curated_collection"
	case req.CollectionID > 0:
		return "collection"
	case req.Favorites:
		return "favorites"
	default:
		return scopeNone
	}
}

// errorClass buckets the outcome for dashboards: validation rejections are
// client bugs, cancellation is the caller walking away, and everything else
// is the repository's failure.
func errorClass(err error) string {
	switch {
	case err == nil:
		return scopeNone
	case errors.Is(err, ErrEmptyQuery), errors.Is(err, ErrInvalidPagination), errors.Is(err, ErrInvalidSuggestionKind):
		return classValidation
	case errors.Is(err, context.Canceled):
		return classCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		return classRepository
	}
}
