package api

import (
	"errors"
	"fmt"
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
// strings, reports the caller's identity and translates outcomes into HTTP;
// ranking, filtering, validation and the visibility rule all live below this
// boundary.
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
	// Both of these flags widen what the request may see, so both belong to
	// whoever moderates rather than to whoever asks. The search branch no
	// longer clears them here: it forwards them raw together with the
	// caller's identity, and the service — the one path every client shares —
	// decides. include_hidden was gated from the first version; unapproved was
	// not, and any signed-in reader could ask for the moderation queue by
	// hand. No screen has ever offered it — which is why it went unnoticed,
	// not why it was safe.
	moderator := c.GetBool("is_superuser")
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
			Moderator:           moderator,
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

	// The ordinary list runs below the service, so the gate is repeated here
	// rather than inherited from it: the two branches must not disagree about
	// who sees what.
	if !moderator {
		q.IncludeHidden = false
		q.UnApproved = false
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

// The query-string names of the lists a picker can be confined to.
const (
	paramTrue     = "true"
	paramAuthor   = "author"
	paramSeries   = "series"
	paramGenre    = "genre"
	paramShelf    = "collection"
	paramCurated  = "curated_collection"
	paramFavorite = "fav"
)

// suggestionScope reads the reader's current list off the query string.
func suggestionScope(c *gin.Context) (models.SuggestionRequest, error) {
	req := models.SuggestionRequest{Favorites: c.Query(paramFavorite) == paramTrue}
	for _, field := range []struct {
		param string
		into  *int64
	}{
		{paramAuthor, &req.AuthorID},
		{paramSeries, &req.SeriesID},
		{paramGenre, &req.GenreID},
		{paramShelf, &req.CollectionID},
		{paramCurated, &req.CuratedCollectionID},
	} {
		raw := c.Query(field.param)
		if raw == "" {
			continue
		}
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return models.SuggestionRequest{}, fmt.Errorf("%s must be a numeric id", field.param)
		}
		*field.into = parsed
	}
	return req, nil
}

// Autocomplete method for getting search suggestions
// Auth godoc
// @Summary Get autocomplete suggestions for search
// @Description Get autocomplete suggestions for books and authors based on query
// @Tags books
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param query query string true "Search query"
// @Param type query string false "Search type: 'title', 'author', or 'all' (default)"
// @Param author query string false "Author ID the reader is browsing"
// @Param series query string false "Series ID the reader is browsing"
// @Param genre query string false "Genre ID the reader is browsing"
// @Param collection query string false "Collection ID the reader is browsing"
// @Param curated_collection query string false "Curated collection ID the reader is browsing"
// @Param fav query string false "Confine suggestions to the reader's favorites"
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

	// The list the reader is standing in, so the picker offers titles they can
	// actually reach from it. Each id is optional and each must be a number:
	// a scope nobody can parse is a client bug, not a wider search.
	scope, err := suggestionScope(c)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}
	scope.Query = query
	scope.Kind = models.SuggestionKind(searchType)
	scope.Language = c.Query("lang")
	scope.UserID = c.GetInt64("user_id")

	result, err := h.Search.Suggestions(c.Request.Context(), scope)
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
