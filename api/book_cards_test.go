package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"gopds-api/database"
	"gopds-api/internal/scanfixture"
	"gopds-api/models"

	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The ordinary list groups into cards only when the site asks, for readers'
// visibility, on a scoped list and with a walked read model; every other
// request is the per-file list it always was.

// fakeLists records which list the handler asked and answers from a table.
type fakeLists struct {
	ready      bool
	readyCalls int
	perFile    []models.BookFilters
	cards      []models.BookFilters
	cardBooks  []models.Book
	entries    []database.CardEntry
	cardTotal  int
	variants   map[int64][]models.BookVariant
	variantErr error
}

//nolint:gocritic // implements BookLists
func (f *fakeLists) GetBooks(_ int64, filters models.BookFilters) ([]models.Book, int, error) {
	f.perFile = append(f.perFile, filters)
	return []models.Book{{ID: 1}, {ID: 2}}, 2, nil
}

//nolint:gocritic // implements BookLists
func (f *fakeLists) GetBookCards(_ context.Context, _ int64, filters models.BookFilters) ([]models.Book, []database.CardEntry, int, error) {
	f.cards = append(f.cards, filters)
	return f.cardBooks, f.entries, f.cardTotal, nil
}

func (f *fakeLists) BookVariantsReady(context.Context) (bool, error) {
	f.readyCalls++
	return f.ready, nil
}

func (f *fakeLists) BookVariantsOf(_ context.Context, bookID, _ int64) ([]models.Book, []models.BookVariant, error) {
	if f.variantErr != nil {
		return nil, nil, f.variantErr
	}
	vs, ok := f.variants[bookID]
	if !ok {
		return nil, nil, pg.ErrNoRows
	}
	books := make([]models.Book, len(vs))
	for i := range vs {
		books[i] = models.Book{ID: vs[i].BookID, Title: "Солярис"}
	}
	return books, vs, nil
}

func newListsRouter(lists BookLists, superuser bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", int64(7))
		c.Set("is_superuser", superuser)
		c.Next()
	})
	SetupBookRoutes(r.Group("/api/books"), &SearchHandler{Search: &fakeSearch{}, Lists: lists})
	return r
}

func TestBooksGroupsOnlyWhenAsked(t *testing.T) {
	cases := []struct {
		name      string
		superuser bool
		path      string
		cards     bool
	}{
		{"without cards", false, "/api/books/list?author=12&limit=10", false},
		{"cards on an author", false, "/api/books/list?author=12&limit=10&cards=1", true},
		{"cards on a series", false, "/api/books/list?series=3&cards=1", true},
		{"cards on a genre", false, "/api/books/list?genre=3&cards=1", true},
		{"cards on a collection", false, "/api/books/list?collection=3&cards=1", true},
		{"cards on a curated collection", false, "/api/books/list?curated_collection=3&cards=1", true},
		{"cards on favorites", false, "/api/books/list?fav=true&cards=1", true},
		{"cards on everyone's favorites", false, "/api/books/list?users_favorites=true&cards=1", true},
		{"cards with include_hidden, moderator", true, "/api/books/list?author=12&cards=1&include_hidden=true", false},
		{"cards with unapproved, moderator", true, "/api/books/list?author=12&cards=1&unapproved=true", false},
		// A reader cannot widen visibility: the flag is dropped and the list
		// is the readers' one, so it groups.
		{"cards with include_hidden, reader", false, "/api/books/list?author=12&cards=1&include_hidden=true", true},
		{"cards with no scope (the feed)", false, "/api/books/list?cards=1", false},
		{"cards with a language only", false, "/api/books/list?lang=ru&cards=1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lists := &fakeLists{ready: true}
			rec := doJSON(t, newListsRouter(lists, tc.superuser), http.MethodGet, tc.path, nil)
			require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
			if tc.cards {
				assert.Len(t, lists.cards, 1)
				assert.Empty(t, lists.perFile)
			} else {
				assert.Empty(t, lists.cards)
				assert.Len(t, lists.perFile, 1)
			}
		})
	}
}

func TestBooksCardsAnswer(t *testing.T) {
	key := uuid.New()
	lists := &fakeLists{ready: true,
		cardBooks: []models.Book{{ID: 10}, {ID: 20}},
		entries: []database.CardEntry{
			{BookID: 10, Variants: &models.BookVariantsInfo{Key: key, Count: 3, Kind: models.BookVariantsVariants,
				Reason: models.BookVariantReasonSaved}},
			{BookID: 20},
		},
		cardTotal: 21}
	rec := doJSON(t, newListsRouter(lists, false), http.MethodGet, "/api/books/list?author=12&limit=10&cards=1", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	var page struct {
		Books  []map[string]json.RawMessage `json:"books"`
		Length int                          `json:"length"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	assert.Equal(t, 3, page.Length, "21 cards are three pages of ten")
	require.Len(t, page.Books, 2)
	assert.JSONEq(t, fmt.Sprintf(`{"key":%q,"count":3,"kind":"variants","reason":"saved"}`, key),
		string(page.Books[0]["variants"]))
	assert.NotContains(t, page.Books[1], "variants", "a single book's JSON is the per-file one")
}

func TestBooksCardsBeforeReady(t *testing.T) {
	lists := &fakeLists{ready: false}
	r := newListsRouter(lists, false)
	for range 3 {
		rec := doJSON(t, r, http.MethodGet, "/api/books/list?author=12&cards=1", nil)
		require.Equal(t, http.StatusOK, rec.Code)
	}
	assert.Empty(t, lists.cards, "before the first walk the site keeps per-file lists")
	assert.Len(t, lists.perFile, 3)
	assert.Equal(t, 1, lists.readyCalls, "the answer is cached")
}

func TestVariantsEndpoint(t *testing.T) {
	key := uuid.New()
	lists := &fakeLists{variants: map[int64][]models.BookVariant{
		20: {
			{BookID: 10, GroupKey: key, Lineage: "doc-a", Latest: true, Rank: 1, Tag: "СИ", DocVersion: "2.1", Default: true},
			{BookID: 20, GroupKey: key, Lineage: "doc-b", Latest: true, Rank: 2},
			{BookID: 30, GroupKey: key, Lineage: "doc-b", Latest: false, Rank: 3},
		},
	}}
	rec := doJSON(t, newListsRouter(lists, false), http.MethodGet, "/api/books/variants/20", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	var answer struct {
		Key       uuid.UUID                    `json:"key"`
		Kind      string                       `json:"kind"`
		Count     int                          `json:"count"`
		DefaultID int64                        `json:"default_id"`
		Variants  []map[string]json.RawMessage `json:"variants"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &answer))
	assert.Equal(t, key, answer.Key)
	assert.Equal(t, models.BookVariantsVariants, answer.Kind)
	assert.Equal(t, 2, answer.Count, "two documents")
	assert.Equal(t, int64(10), answer.DefaultID)
	require.Len(t, answer.Variants, 3)
	first := answer.Variants[0]
	assert.JSONEq(t, `10`, string(first["id"]))
	assert.JSONEq(t, `"Солярис"`, string(first["title"]), "each variant is a listed book")
	assert.JSONEq(t, `[]`, string(first["isbn"]))
	assert.JSONEq(t, `"doc-a"`, string(first["lineage"]))
	assert.JSONEq(t, `true`, string(first["latest"]))
	assert.JSONEq(t, `1`, string(first["rank"]))
	assert.JSONEq(t, `"СИ"`, string(first["tag"]))
	assert.JSONEq(t, `"2.1"`, string(first["doc_version"]))
	assert.JSONEq(t, `true`, string(first["default"]))
	assert.JSONEq(t, `false`, string(answer.Variants[2]["latest"]))
	assert.JSONEq(t, `false`, string(answer.Variants[2]["default"]))
}

func TestVariantsEndpoint404ForHidden(t *testing.T) {
	lists := &fakeLists{variants: map[int64][]models.BookVariant{}}
	r := newListsRouter(lists, false)
	rec := doJSON(t, r, http.MethodGet, "/api/books/variants/99", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = doJSON(t, r, http.MethodGet, "/api/books/variants/abc", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// The card's star: unstarring with whole_group takes every file of the card
// off the reader's favorites; without it only the book, as before.
func TestFavWholeGroup(t *testing.T) {
	scratch := scanfixture.ScratchDB(t)
	previous := database.GetDB()
	database.SetDB(scratch)
	t.Cleanup(func() { database.SetDB(previous) })

	scalar := func(query string, args ...any) int64 {
		t.Helper()
		var id int64
		_, err := scratch.QueryOne(pg.Scan(&id), query, args...)
		require.NoError(t, err, query)
		return id
	}
	user := scalar(`INSERT INTO auth_user (password, is_superuser, username, email, date_joined)
		VALUES ('', false, 'reader', 'reader@fixture.local', now()) RETURNING id`)
	book := func(n int) int64 {
		return scalar(`INSERT INTO opds_catalog_book
			(filename, path, format, title, annotation, lang, registerdate, docdate, approved, cover, md5, duplicate_hidden)
			VALUES (?, 'fav.zip', 'fb2', 'Солярис', '', 'ru', now(), '', true, false, md5(?), false) RETURNING id`,
			fmt.Sprintf("fav-%d.fb2", n), fmt.Sprintf("fav-%d", n))
	}
	a, b, c, unrelated := book(1), book(2), book(3), book(4)
	key := uuid.New()
	entity := uuid.New()
	for rank, id := range []int64{a, b, c} {
		scalar(`INSERT INTO book_variant_member
			(book_id, group_key, base_key, part_entity, part_lang, lineage, latest, variant_rank)
			VALUES (?, ?, 'base', ?, 'ru', ?, true, ?) RETURNING book_id`, id, key, entity, fmt.Sprint(id), rank+1)
	}
	for _, id := range []int64{a, c, unrelated} {
		scalar(`INSERT INTO favorite_books (user_id, book_id) VALUES (?, ?) RETURNING id`, user, id)
	}
	stars := func() []int64 {
		var ids []int64
		_, err := scratch.Query(&ids, `SELECT book_id FROM favorite_books WHERE user_id = ? ORDER BY book_id`, user)
		require.NoError(t, err)
		return ids
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(ctx *gin.Context) { ctx.Set("user_id", user); ctx.Next() })
	r.POST("/fav", FavBook)

	// Without whole_group, only the book itself.
	rec := doJSON(t, r, http.MethodPost, "/fav", map[string]any{"book_id": a, "fav": false})
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.Equal(t, []int64{c, unrelated}, stars())

	// whole_group on starring changes nothing: it stars the shown file.
	rec = doJSON(t, r, http.MethodPost, "/fav", map[string]any{"book_id": a, "fav": true, "whole_group": true})
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.Equal(t, []int64{a, c, unrelated}, stars())

	// Unstarring the card from a file that was never starred.
	rec = doJSON(t, r, http.MethodPost, "/fav", map[string]any{"book_id": b, "fav": false, "whole_group": true})
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.JSONEq(t, `{"have_favs":true}`, rec.Body.String())
	assert.Equal(t, []int64{unrelated}, stars())
}
