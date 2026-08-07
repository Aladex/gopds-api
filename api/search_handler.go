package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

// SearchHandler is the REST adapter of the search service. It binds query
// strings, enforces the visibility gate and translates outcomes into HTTP;
// ranking, filtering and validation all live below this boundary.
type SearchHandler struct {
	Search services.PublicSearch
}

// bookListQuery is the list endpoint's query string: the long-standing list
// filters plus an exact-ID pin, which navigation links use to open one book
// directly.
type bookListQuery struct {
	models.BookFilters
	BookID int64 `form:"book_id"`
}

// maxListLimit is the ordinary list's own page-size clamp, mirrored here
// because the page count has to be drawn from the same limit the rows were
// cut with, and the database function applies it without handing it back.
const maxListLimit = 100

// Books method for retrieving books from the database and returning them in JSON format
// Auth godoc
// @Summary Retrieve books from the database
// @Description Get the list of books from the database and return them in JSON format
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param  limit query int true "Limit"
// @Param  offset query int true "Offset"
// @Param  title query string false "Title of the book"
// @Param  author query int false "Author ID"
// @Param  book_id query int false "Exact book ID"
// @Tags books
// @Accept  json
// @Produce  json
// @Success 200 {object} ExportAnswer "List of books and length"
// @Failure 400 {object} httputil.HTTPError "Bad request"
// @Failure 500 {object} httputil.HTTPError "Internal server error"
// @Failure 403 {object} httputil.HTTPError "Forbidden"
// @Router /api/books/list [get]
func (h *SearchHandler) Books(c *gin.Context) {
	var q bookListQuery
	if err := c.ShouldBindWith(&q, binding.Query); err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
		return
	}
	// Both of these widen what the request may see, so both belong to whoever
	// moderates rather than to whoever asks. They are cleared before the branch
	// below, so the search and the ordinary list cannot disagree about who sees
	// what.
	//
	// include_hidden was gated from the first version; unapproved was not, and
	// any signed-in reader could ask for the moderation queue by hand. No screen
	// has ever offered it — which is why it went unnoticed, not why it was safe.
	if !c.GetBool("is_superuser") {
		q.IncludeHidden = false
		q.UnApproved = false
	}
	userID := c.GetInt64("user_id")

	if strings.TrimSpace(q.Title) != "" || q.BookID > 0 {
		page, err := h.Search.SearchBooks(c.Request.Context(), models.BookSearchRequest{
			Query:               q.Title,
			ExactBookID:         q.BookID,
			UserID:              userID,
			Language:            q.Lang,
			AuthorID:            int64(q.Author),
			SeriesID:            int64(q.Series),
			GenreID:             int64(q.Genre),
			CollectionID:        q.Collection,
			CuratedCollectionID: q.CuratedCollection,
			Favorites:           q.Fav,
			Unapproved:          q.UnApproved,
			IncludeHidden:       q.IncludeHidden,
			Limit:               q.Limit,
			Offset:              q.Offset,
		})
		if err != nil {
			mapSearchError(c, err)
			return
		}
		c.JSON(http.StatusOK, ExportAnswer{Books: page.Books, Length: pageCount(page.Total, page.Limit)})
		return
	}

	// No search target: the ordinary list keeps its existing path and semantics
	// (favorites ordering, collection ordering, users-favorites mode).
	books, count, err := database.GetBooks(userID, q.BookFilters)
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, ExportAnswer{Books: books, Length: pageCount(count, effectiveListLimit(q.Limit))})
}

// Authors method for retrieving the list of authors on the search page
// Auth godoc
// @Summary Retrieve authors list for the search page
// @Description Get a list of authors available on the search page
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param  limit query int true "Limit"
// @Param  offset query int true "Offset"
// @Param  author query string false "Author name"
// @Accept  json
// @Produce  json
// @Success 200 {object} AuthorAnswer
// @Failure 400 {object} httputil.HTTPError "Bad request"
// @Failure 500 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Router /api/books/authors [get]
func (h *SearchHandler) Authors(c *gin.Context) {
	var filters models.AuthorFilters
	if err := c.ShouldBindWith(&filters, binding.Query); err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
		return
	}

	page, err := h.Search.SearchAuthors(c.Request.Context(), models.AuthorSearchRequest{
		Query:    filters.Author,
		Language: filters.Lang,
		Limit:    filters.Limit,
		Offset:   filters.Offset,
	})
	if err != nil {
		mapSearchError(c, err)
		return
	}
	c.JSON(http.StatusOK, AuthorAnswer{Authors: page.Authors, Length: pageCount(page.Total, page.Limit)})
}

// mapSearchError translates the service boundary into HTTP: validation
// rejections are the caller's bug (400), everything else is ours (500). The
// body always goes through httputil — a raw Go error marshals as {} and tells
// the client nothing.
func mapSearchError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, services.ErrEmptyQuery),
		errors.Is(err, services.ErrInvalidPagination),
		errors.Is(err, services.ErrInvalidSuggestionKind):
		httputil.NewError(c, http.StatusBadRequest, err)
	default:
		httputil.NewError(c, http.StatusInternalServerError, err)
	}
}

// effectiveListLimit mirrors the clamp the ordinary list applies internally.
// The raw value cannot be trusted with the division pageCount does: the
// previous handler divided by it directly and panicked whenever a client
// omitted the parameter.
func effectiveListLimit(limit int) int {
	if limit <= 0 || limit > maxListLimit {
		return maxListLimit
	}
	return limit
}

// AutocompleteResponse struct for autocomplete suggestions
type AutocompleteResponse struct {
	Suggestions []models.AutocompleteSuggestion `json:"suggestions"`
}

// Autocomplete method for getting search suggestions
// Auth godoc
// @Summary Get autocomplete suggestions for search
// @Description Get autocomplete suggestions for books and authors based on query
// @Tags books
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param query query string true "Search query"
// @Param type query string false "Search type: 'title', 'author', or 'all' (default)"
// @Param author query string false "Author ID for filtering results"
// @Param lang query string false "Language for filtering results"
// @Accept  json
// @Produce  json
// @Success 200 {object} AutocompleteResponse "List of suggestions"
// @Failure 400 {object} httputil.HTTPError "Bad request"
// @Router /api/books/autocomplete [get]
func (h *SearchHandler) Autocomplete(c *gin.Context) {
	query := c.Query("query")
	if query == "" {
		httputil.NewError(c, http.StatusBadRequest, errors.New("query parameter is required"))
		return
	}

	// The kind travels raw: naming a lane that does not exist is the service's
	// validation call, and this adapter does not second-guess it.
	searchType := c.DefaultQuery("type", string(models.SuggestionAll))

	var authorID int64
	if raw := c.Query("author"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			httputil.NewError(c, http.StatusBadRequest, errors.New("author must be a numeric id"))
			return
		}
		authorID = parsed
	}

	result, err := h.Search.Suggestions(c.Request.Context(), models.SuggestionRequest{
		Query:    query,
		Kind:     models.SuggestionKind(searchType),
		Language: c.Query("lang"),
		AuthorID: authorID,
	})
	if err != nil {
		mapSearchError(c, err)
		return
	}
	// The picker is a list: it serializes as [], never null. The service
	// already guarantees this; the adapter keeps the guarantee on its own edge.
	if result.Suggestions == nil {
		result.Suggestions = []models.AutocompleteSuggestion{}
	}
	c.JSON(http.StatusOK, AutocompleteResponse{Suggestions: result.Suggestions})
}
