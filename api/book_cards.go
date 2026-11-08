package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/logging"
	"gopds-api/models"

	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// BookLists is what the book list reads its rows from: the per-file list
// every surface shares and, for the site only, the same scopes grouped into
// cards, with the variants of one card.
type BookLists interface {
	GetBooks(userID int64, filters models.BookFilters) ([]models.Book, int, error)
	GetBookCards(ctx context.Context, userID int64, filters models.BookFilters) ([]models.Book, []database.CardEntry, int, error)
	// BookVariantsReady says the card read model has been walked once.
	BookVariantsReady(ctx context.Context) (bool, error)
	BookVariantsOf(ctx context.Context, bookID, userID int64) ([]models.Book, []models.BookVariant, error)
}

// catalogLists is BookLists over the package database handle.
type catalogLists struct{}

//nolint:gocritic // the database list takes its filters by value
func (catalogLists) GetBooks(userID int64, filters models.BookFilters) ([]models.Book, int, error) {
	return database.GetBooks(userID, filters)
}

//nolint:gocritic // the database list takes its filters by value
func (catalogLists) GetBookCards(
	ctx context.Context, userID int64, filters models.BookFilters,
) ([]models.Book, []database.CardEntry, int, error) {
	return database.GetBookCards(ctx, userID, filters)
}

func (catalogLists) BookVariantsReady(ctx context.Context) (bool, error) {
	return database.BookVariantsReady(ctx, database.GetDB())
}

func (catalogLists) BookVariantsOf(ctx context.Context, bookID, userID int64) ([]models.Book, []models.BookVariant, error) {
	return database.BookVariantsOf(ctx, bookID, userID)
}

func (h *SearchHandler) lists() BookLists {
	if h.Lists != nil {
		return h.Lists
	}
	return catalogLists{}
}

// cardsReadyTTL is how long the handler trusts its answer about the read
// model's readiness; it only ever turns from false to true.
const cardsReadyTTL = 60 * time.Second

// readiness caches BookVariantsReady.
type readiness struct {
	mu      sync.Mutex
	ready   bool
	checked time.Time
}

// cardsReady says whether the read model may serve cards, asking at most once
// a minute. A failed check is not ready: the list falls back to files.
func (h *SearchHandler) cardsReady(ctx context.Context) bool {
	h.ready.mu.Lock()
	defer h.ready.mu.Unlock()
	if !h.ready.checked.IsZero() && time.Since(h.ready.checked) < cardsReadyTTL {
		return h.ready.ready
	}
	ready, err := h.lists().BookVariantsReady(ctx)
	if err != nil {
		logging.Warnf("book variants readiness: %v", err)
		return false
	}
	h.ready.ready, h.ready.checked = ready, time.Now()
	return ready
}

// scopedList says the list is one scope the site groups into cards: an
// author, a series, a genre, a collection, a curated collection, the caller's
// favorites or everyone's. The feed and a language alone stay per file.
//
//nolint:gocritic // reads one filter set; the list path passes it by value throughout
func scopedList(f models.BookFilters) bool {
	return f.Author != 0 || f.AuthorEntity != uuid.Nil || f.Series != 0 || f.Genre != 0 || f.Collection != 0 ||
		f.CuratedCollection != 0 || f.Fav || f.UsersFavorites
}

// groupCards says the ordinary list answers in cards: asked for with
// cards=1, readers' visibility only, a scoped list, and a walked read model.
func (h *SearchHandler) groupCards(ctx context.Context, q *bookListQuery) bool {
	return q.Cards && !q.IncludeHidden && !q.UnApproved && scopedList(q.BookFilters) && h.cardsReady(ctx)
}

// cardsList answers the ordinary list in cards: each card that holds more
// than one visible file carries variants, a single book nothing more.
func (h *SearchHandler) cardsList(c *gin.Context, userID int64, filters *models.BookFilters) {
	books, entries, total, err := h.lists().GetBookCards(c.Request.Context(), userID, *filters)
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}
	listed, err := h.listedBooks(c.Request.Context(), books)
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}
	for i := range listed {
		listed[i].Variants = entries[i].Variants
	}
	c.JSON(http.StatusOK, ExportAnswer{Books: listed, Length: pageCount(total, effectiveListLimit(filters.Limit))})
}

// VariantsAnswer is a card with all of its visible files.
type VariantsAnswer struct {
	Key       uuid.UUID       `json:"key"`
	Kind      string          `json:"kind"`
	Count     int             `json:"count"`
	DefaultID int64           `json:"default_id"`
	Variants  []ListedVariant `json:"variants"`
}

// ListedVariant is one file of a card: the listed book and its place in the
// card.
type ListedVariant struct {
	ListedBook
	Lineage    string `json:"lineage"`
	Latest     bool   `json:"latest"`
	Rank       int    `json:"rank"`
	Tag        string `json:"tag"`
	DocVersion string `json:"doc_version"`
	Default    bool   `json:"default"`
}

// Variants returns the visible files of the book's card
// Auth godoc
// @Summary Retrieve the variants of a book
// @Description The visible files of the book's card, by rank; the first is the card's default
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param  id path int true "Book ID"
// @Tags books
// @Produce  json
// @Success 200 {object} VariantsAnswer "The card and its files"
// @Failure 400 {object} httputil.HTTPError "Bad request"
// @Failure 404 {object} httputil.HTTPError "No such book for readers"
// @Failure 500 {object} httputil.HTTPError "Internal server error"
// @Router /api/books/variants/{id} [get]
func (h *SearchHandler) Variants(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
		return
	}
	books, variants, err := h.lists().BookVariantsOf(c.Request.Context(), id, c.GetInt64("user_id"))
	if errors.Is(err, pg.ErrNoRows) || (err == nil && len(books) == 0) {
		httputil.NewError(c, http.StatusNotFound, errors.New("book_not_found"))
		return
	}
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}
	listed, err := h.listedBooks(c.Request.Context(), books)
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}
	documents := map[string]bool{}
	answer := VariantsAnswer{Key: variants[0].GroupKey, Variants: make([]ListedVariant, len(listed))}
	for i := range listed {
		v := &variants[i]
		documents[v.Lineage] = true
		if v.Default {
			answer.DefaultID = v.BookID
		}
		answer.Variants[i] = ListedVariant{ListedBook: listed[i], Lineage: v.Lineage, Latest: v.Latest, Rank: v.Rank,
			Tag: v.Tag, DocVersion: v.DocVersion, Default: v.Default}
	}
	answer.Kind, answer.Count = models.VariantsKind(len(variants), len(documents))
	c.JSON(http.StatusOK, answer)
}
