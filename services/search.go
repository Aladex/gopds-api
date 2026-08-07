package services

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"gopds-api/logging"
	"gopds-api/models"

	"github.com/sirupsen/logrus"
)

// ErrEmptyQuery is a search request with no text and no navigation target.
var ErrEmptyQuery = errors.New("search query is empty")

// ErrInvalidPagination is a negative offset: a client bug, not a filter.
var ErrInvalidPagination = errors.New("invalid pagination")

// Page sizing for search results. An unnamed or unusable limit falls back to
// the default, which is also the ceiling — the same 100 the previous book and
// author paths clamped to.
const (
	defaultSearchLimit = 100
	maxSearchLimit     = 100
)

// allLanguages is the language code meaning "the whole library". It must stay
// in sync with database.AllLanguages, which the ordinary list path compares
// against; the service cannot import database, so the value is repeated here.
const allLanguages = "all"

// SearchRepository is the storage port the service drives.
type SearchRepository interface {
	SearchBooks(ctx context.Context, req models.BookSearchRequest) (models.BookSearchPage, error)
	SearchAuthors(ctx context.Context, req models.AuthorSearchRequest) (models.AuthorSearchPage, error)
}

// PublicSearch is the adapter-facing search surface: REST handlers, bots and
// other adapters receive this, never the concrete service.
type PublicSearch interface {
	SearchBooks(ctx context.Context, req models.BookSearchRequest) (models.BookSearchPage, error)
	SearchAuthors(ctx context.Context, req models.AuthorSearchRequest) (models.AuthorSearchPage, error)
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
func (s *SearchService) SearchBooks(ctx context.Context, req models.BookSearchRequest) (models.BookSearchPage, error) {
	start := time.Now()
	req.Query = strings.TrimSpace(req.Query)
	req.Language = normalizeLanguage(req.Language)
	if req.Query == "" && req.ExactBookID <= 0 {
		err := ErrEmptyQuery
		logCompletion("books", req.Query, req.Language, bookScope(req), 0, 0, "", err, start)
		return models.BookSearchPage{}, err
	}
	var err error
	if req.Limit, req.Offset, err = normalizePagination(req.Limit, req.Offset); err != nil {
		logCompletion("books", req.Query, req.Language, bookScope(req), 0, 0, "", err, start)
		return models.BookSearchPage{}, err
	}
	page, err := s.repo.SearchBooks(ctx, req)
	logCompletion("books", req.Query, req.Language, bookScope(req), len(page.Books), page.Total, page.QueryHash, err, start)
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
		logCompletion("authors", req.Query, req.Language, "none", 0, 0, "", err, start)
		return models.AuthorSearchPage{}, err
	}
	var err error
	if req.Limit, req.Offset, err = normalizePagination(req.Limit, req.Offset); err != nil {
		logCompletion("authors", req.Query, req.Language, "none", 0, 0, "", err, start)
		return models.AuthorSearchPage{}, err
	}
	page, err := s.repo.SearchAuthors(ctx, req)
	logCompletion("authors", req.Query, req.Language, "none", len(page.Authors), page.Total, page.QueryHash, err, start)
	return page, err
}

// normalizePagination clamps the limit into the usable range and rejects a
// negative offset. Zero and oversized limits both become the default page
// size, which matches what the previous list paths did with them.
func normalizePagination(limit, offset int) (int, int, error) {
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
		queryHash = "unavailable"
	}
	logging.WithFields(logrus.Fields{
		"mode":        mode,
		"query_runes": runeLength(query),
		"query_hash":  queryHash,
		"language":    language,
		"scope":       scope,
		"returned":    returned,
		"total":       total,
		"duration_ms": time.Since(start).Milliseconds(),
		"error_class": errorClass(err),
	}).Info("search completed")
}

// bookScope names the first active scope in a fixed precedence, keeping the
// log field single-valued when a request combines several.
func bookScope(req models.BookSearchRequest) string {
	switch {
	case req.ExactBookID > 0:
		return "exact_book_id"
	case req.AuthorID > 0:
		return "author"
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
		return "none"
	}
}

// errorClass buckets the outcome for dashboards: validation rejections are
// client bugs, cancellation is the caller walking away, and everything else
// is the repository's failure.
func errorClass(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, ErrEmptyQuery), errors.Is(err, ErrInvalidPagination):
		return "validation"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		return "repository"
	}
}
