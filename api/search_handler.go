package api

import (
	"errors"
	"net/http"
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
	if q.IncludeHidden && !c.GetBool("is_superuser") {
		q.IncludeHidden = false
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
	case errors.Is(err, services.ErrEmptyQuery), errors.Is(err, services.ErrInvalidPagination):
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
