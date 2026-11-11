package api

// The review queue's views: the important items (a name on two or more
// current author credits) and the singles behind their own filter. Handler
// tests pin the parameter, the cursor's binding to its view and the counts'
// shape; the database test pins the filter itself under both orders.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gopds-api/config"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReviewListViewValidation(t *testing.T) {
	f := &fakeReviewService{page: AuthorMetadataReviewListPage{Items: []AuthorMetadataReviewListItem{}}}
	r := reviewRouter(f)

	for _, query := range []string{
		"?status=open&view=",
		"?status=open&view=heavy",
		"?status=open&view=IMPORTANT",
		"?status=open&view=single",
	} {
		rec := reviewRequest(t, r, http.MethodGet, reviewBase+query, "")
		assert.Equal(t, http.StatusBadRequest, rec.Code, query)
		assert.Equal(t, codeInvalidView, reviewCode(t, rec), query)
	}
	assert.Empty(t, f.listQueries(), "no refused query reaches the service")

	for _, query := range []string{
		"?status=open",
		"?status=open&view=all",
		"?status=open&view=important",
		"?status=closed&view=singles&order=credits",
	} {
		rec := reviewRequest(t, r, http.MethodGet, reviewBase+query, "")
		assert.Equal(t, http.StatusOK, rec.Code, query)
	}
	got := f.listQueries()
	require.Len(t, got, 4)
	assert.Equal(t, reviewViewAll, got[0].View, "without a view the API lists every item, as before")
	assert.Equal(t, reviewViewAll, got[1].View)
	assert.Equal(t, reviewViewImportant, got[2].View)
	assert.Equal(t, reviewViewSingles, got[3].View)
	assert.Equal(t, reviewOrderCredits, got[3].Order)
}

func TestReviewCursorBelongsToItsView(t *testing.T) {
	f := &fakeReviewService{page: AuthorMetadataReviewListPage{Items: []AuthorMetadataReviewListItem{}}}
	r := reviewRouter(f)
	now := time.Now().UTC()
	cursor := func(order, view string) string {
		return encodeReviewCursor(AuthorMetadataReviewCursor{
			Order: order, View: view, CreatedAt: now.Add(-time.Hour), Credits: 3, ID: 7,
		})
	}

	for _, tc := range []struct{ name, query string }{
		{"important cursor, singles view", "?status=open&view=singles&cursor=" + cursor(reviewOrderCreated, reviewViewImportant)},
		{"important cursor, no view", "?status=open&cursor=" + cursor(reviewOrderCreated, reviewViewImportant)},
		{"plain cursor, important view", "?status=open&view=important&cursor=" + cursor(reviewOrderCreated, reviewViewAll)},
		{
			"singles cursor, important view, credits",
			"?status=open&order=credits&view=important&cursor=" + cursor(reviewOrderCredits, reviewViewSingles),
		},
		{"important credits cursor, created order", "?status=open&view=important&cursor=" + cursor(reviewOrderCredits, reviewViewImportant)},
		{"unknown view tag", "?status=open&view=important&cursor=" + base64.RawURLEncoding.EncodeToString([]byte("x:5.7"))},
		{"empty view tag", "?status=open&cursor=" + base64.RawURLEncoding.EncodeToString([]byte(":5.7"))},
		{"tag without body", "?status=open&view=important&cursor=" + base64.RawURLEncoding.EncodeToString([]byte("i:"))},
	} {
		rec := reviewRequest(t, r, http.MethodGet, reviewBase+tc.query, "")
		assert.Equal(t, http.StatusBadRequest, rec.Code, tc.name)
		assert.Equal(t, codeInvalidCursor, reviewCode(t, rec), tc.name)
	}
	assert.Empty(t, f.listQueries(), "no refused query reaches the service")

	// A cursor of the plain view keeps the format it always had, so an
	// address saved before the views still reads.
	assert.Equal(t,
		base64.RawURLEncoding.EncodeToString([]byte("k3.7")), cursor(reviewOrderCredits, reviewViewAll))
	assert.Equal(t,
		base64.RawURLEncoding.EncodeToString([]byte("k3.7")), cursor(reviewOrderCredits, ""))

	// Every view and order round-trips: the page's cursor is the next
	// request's, with its view.
	for _, view := range []string{reviewViewAll, reviewViewImportant, reviewViewSingles} {
		for _, order := range []string{reviewOrderCreated, reviewOrderCredits} {
			next := AuthorMetadataReviewCursor{Order: order, View: view, ID: 9}
			if order == reviewOrderCredits {
				next.Credits = 2
			} else {
				next.CreatedAt = now.Add(-time.Minute)
			}
			f.page = AuthorMetadataReviewListPage{Items: []AuthorMetadataReviewListItem{}, Next: &next}
			query := "?status=open&order=" + order + "&view=" + view
			rec := reviewRequest(t, r, http.MethodGet, reviewBase+query, "")
			require.Equal(t, http.StatusOK, rec.Code, query)
			var raw string
			require.NoError(t, json.Unmarshal(reviewBody(t, rec)["next_cursor"], &raw))
			rec = reviewRequest(t, r, http.MethodGet, reviewBase+query+"&cursor="+raw, "")
			require.Equal(t, http.StatusOK, rec.Code, query)
			last := f.listQueries()[len(f.listQueries())-1]
			require.NotNil(t, last.After, query)
			assert.Equal(t, view, last.View, query)
			assert.Equal(t, view, last.After.View, query)
			assert.Equal(t, order, last.After.Order, query)
			assert.Equal(t, int64(9), last.After.ID, query)
			assert.Equal(t, next.Credits, last.After.Credits, query)
			assert.True(t, next.CreatedAt.Equal(last.After.CreatedAt), query)
		}
	}
}

func TestReviewCountsShape(t *testing.T) {
	f := &fakeReviewService{counts: AuthorMetadataReviewCounts{Important: 3650, Singles: 9409}}
	rec := reviewRequest(t, reviewRouter(f), http.MethodGet, reviewBase+"/counts", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"counts": {"important": 3650, "singles": 9409}}`, rec.Body.String())
	assert.Equal(t, []string{"counts"}, f.callList(), "the counts route is not an item id")

	rec = reviewRequest(t, reviewRouter(&fakeReviewService{err: ErrAuthorMetadataReviewUnavailable}),
		http.MethodGet, reviewBase+"/counts", "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "review_service_unavailable", reviewCode(t, rec))
}

func TestReviewViewsOnTheDatabase(t *testing.T) {
	db := reviewAPIDB(t)
	ctx := context.Background()
	previousWiring := authorMetadataReviewService
	t.Cleanup(func() { authorMetadataReviewService = previousWiring })
	serverConfig := config.AuthorMetadataConfig{}
	serverConfig.LocalNormalization.MaxAttempts = 5
	require.NoError(t, SetAuthorMetadataReviewService(db, &serverConfig))
	gin.SetMode(gin.TestMode)
	r := gin.New()
	admin := r.Group("/api/admin", func(c *gin.Context) {
		c.Set("username", "admin")
		c.Set("user_id", adminActorID)
		c.Next()
	})
	SetupAuthorMetadataReviewRoutes(admin.Group("/author-metadata"), authorMetadataReviewService())
	type page struct {
		Items []AuthorMetadataReviewListItem `json:"items"`
		Next  *string                        `json:"next_cursor"`
	}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, method, reviewBase+path, readerOrNull(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		return rec
	}
	get := func(query string) page {
		t.Helper()
		var p page
		require.NoError(t, json.Unmarshal(call(http.MethodGet, query, "").Body.Bytes(), &p))
		return p
	}
	walk := func(query string) []string {
		t.Helper()
		var names []string
		p := get(query)
		for {
			names = append(names, displayNames(p.Items)...)
			if p.Next == nil {
				return names
			}
			p = get(query + "&cursor=" + *p.Next)
		}
	}
	counts := func() AuthorMetadataReviewCounts {
		t.Helper()
		var body struct {
			Counts AuthorMetadataReviewCounts `json:"counts"`
		}
		require.NoError(t, json.Unmarshal(call(http.MethodGet, "/counts", "").Body.Bytes(), &body))
		return body.Counts
	}

	// Credits: light 1, heavy 3, middle 2, edge 2, lone 1. The created
	// order interleaves singles and important items, so a filter that leaks
	// or a cursor that skips shows at every position.
	light := nameValueOf(t, "А.", "Сидоров")
	heavy := nameValueOf(t, "Б.", "Кузнецов")
	middle := nameValueOf(t, "В.", "Орлов")
	lone := nameValueOf(t, "Г.", "Зайцев")
	edge := nameValueOf(t, "Д.", "Белов")
	seedOpenReviewItem(t, db, light, "Первая")
	seedOpenReviewItem(t, db, heavy, "Вторая")
	seedOpenReviewItem(t, db, middle, "Третья")
	seedOpenReviewItem(t, db, lone, "Четвёртая")
	seedOpenReviewItem(t, db, edge, "Пятая")
	addAuthorCredits(t, db, heavy, 2)
	addAuthorCredits(t, db, middle, 1)
	addAuthorCredits(t, db, edge, 1)

	all := []string{"А. Сидоров", "Б. Кузнецов", "В. Орлов", "Г. Зайцев", "Д. Белов"}
	important := []string{"Б. Кузнецов", "В. Орлов", "Д. Белов"}
	singles := []string{"А. Сидоров", "Г. Зайцев"}

	for _, limit := range []string{"1", "2", "10"} {
		assert.Equal(t, all, walk("?status=open&limit="+limit), "no view: every item, limit "+limit)
		assert.Equal(t, all, walk("?status=open&view=all&limit="+limit), limit)
		assert.Equal(t, important, walk("?status=open&view=important&limit="+limit), "created, limit "+limit)
		assert.Equal(t, singles, walk("?status=open&view=singles&limit="+limit), "created, limit "+limit)
		assert.Equal(t, []string{"Б. Кузнецов", "В. Орлов", "Д. Белов"},
			walk("?status=open&order=credits&view=important&limit="+limit), "credits, limit "+limit)
		assert.Equal(t, []string{"А. Сидоров", "Г. Зайцев"},
			walk("?status=open&order=credits&view=singles&limit="+limit), "credits, limit "+limit)
	}

	// The credits order keeps its ties by id inside a view.
	first := get("?status=open&order=credits&view=important&limit=2")
	assert.Equal(t, []int64{3, 2}, creditCounts(first.Items))
	require.NotNil(t, first.Next)
	second := get("?status=open&order=credits&view=important&limit=2&cursor=" + *first.Next)
	assert.Equal(t, []string{"Д. Белов"}, displayNames(second.Items))
	assert.Nil(t, second.Next, "a short page ends the view")

	assert.Equal(t, AuthorMetadataReviewCounts{Important: 3, Singles: 2}, counts())

	// A decided item leaves the open counts and the open views.
	call(http.MethodPost, "/1/accept", `{"scope":"fingerprint"}`)
	assert.Equal(t, AuthorMetadataReviewCounts{Important: 3, Singles: 1}, counts())
	assert.Equal(t, []string{"Г. Зайцев"}, walk("?status=open&view=singles&limit=10"))
	assert.Equal(t, []string{"А. Сидоров"}, walk("?status=closed&view=singles&limit=10"))
	assert.Empty(t, walk("?status=closed&view=important&limit=10"))
}
