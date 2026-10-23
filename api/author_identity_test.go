package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Under authors.identity = entity (design E, §4.2) the REST surfaces name
// authors by their entity UUIDs: the list's author parameter, the author
// heading, the author search, the picker and the links of every author
// line. A legacy number is "not found" there, never another author's page;
// under the legacy identity an entity UUID is "not found" the same way.

// fakeEntities knows a fixed set of live entities.
type fakeEntities struct {
	live map[uuid.UUID]models.AuthorEntity
}

func (f *fakeEntities) EntityLines(_ context.Context, lines [][]models.AuthorDisplay) ([][]models.AuthorDisplay, error) {
	out := make([][]models.AuthorDisplay, len(lines))
	for i, line := range lines {
		out[i] = models.WithEntities(line, f.live)
	}
	return out, nil
}

func (f *fakeEntities) AuthorEntity(_ context.Context, id uuid.UUID) (models.AuthorEntity, error) {
	e, ok := f.live[id]
	if !ok {
		return models.AuthorEntity{}, database.ErrAuthorEntityNotFound
	}
	return e, nil
}

func newEntityTestRouter(search *fakeSearch, lines AuthorDisplayLookup, entities AuthorEntityReader) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", int64(77))
		c.Set("is_superuser", false)
		c.Next()
	})
	h := &SearchHandler{Search: search, AuthorLines: lines}
	if entities != nil {
		h.Entities = entities
	}
	SetupBookRoutes(r.Group("/api/books"), h)
	return r
}

var (
	kingID   = uuid.MustParse("01a168d0-a113-741a-b69d-53b91f3c7eb9")
	kingTwin = uuid.MustParse("01a168d0-a113-741b-b3d6-e5dbeb1c9f89")
	liveKing = &fakeEntities{live: map[uuid.UUID]models.AuthorEntity{
		kingID:   {ID: kingID, FullName: "Стивен Кинг", Label: "Ужасы"},
		kingTwin: {ID: kingTwin, FullName: "Стивен Кинг", Label: "Фантастика"},
	}}
)

func TestTheEntityIdentityScopesTheListByAnEntity(t *testing.T) {
	search := &fakeSearch{booksPage: models.BookSearchPage{Books: []models.Book{{ID: 1}}, Total: 1, Limit: 10}}
	r := newEntityTestRouter(search, nil, liveKing)

	rec := doJSON(t, r, http.MethodGet, "/api/books/list?title=x&author="+kingID.String(), nil)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.Len(t, search.booksReqs, 1)
	assert.Equal(t, kingID, search.booksReqs[0].AuthorEntityID)
	assert.Zero(t, search.booksReqs[0].AuthorID)

	rec = doJSON(t, r, http.MethodGet, "/api/books/autocomplete?query=оно&author="+kingID.String(), nil)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.Len(t, search.suggReqs, 1)
	assert.Equal(t, kingID, search.suggReqs[0].AuthorEntityID)
	assert.Zero(t, search.suggReqs[0].AuthorID)
}

// Another identity's ID, or an entity that is not live, is a 404 on every
// path that takes an author — before anything is listed — and text that is
// no ID at all stays a 400.
func TestAnAuthorOfTheOtherIdentityIsNotFound(t *testing.T) {
	for _, tc := range []struct {
		name     string
		entities AuthorEntityReader
		author   string
		want     int
	}{
		{"a legacy number under entities", liveKing, "5", http.StatusNotFound},
		{"a gone entity", liveKing, uuid.NewString(), http.StatusNotFound},
		{"the nil UUID under entities", liveKing, uuid.Nil.String(), http.StatusNotFound},
		{"the compact nil UUID under entities", liveKing, "00000000000000000000000000000000", http.StatusNotFound},
		{"the braced nil UUID under entities", liveKing, "{" + uuid.Nil.String() + "}", http.StatusNotFound},
		{"the compact nil UUID under the legacy identity", nil, "00000000000000000000000000000000", http.StatusNotFound},
		{"a zero-padded legacy number under entities", liveKing, "00000000000000000000000000008671", http.StatusNotFound},
		{"the nil UUID under the legacy identity", nil, uuid.Nil.String(), http.StatusNotFound},
		{"an entity under the legacy identity", nil, kingID.String(), http.StatusNotFound},
		{"not an ID under entities", liveKing, "Кинг", http.StatusBadRequest},
		{"not an ID under the legacy identity", nil, "Кинг", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			search := &fakeSearch{}
			r := newEntityTestRouter(search, nil, tc.entities)
			for _, path := range []string{
				"/api/books/list?title=x&author=" + url.QueryEscape(tc.author),
				"/api/books/list?author=" + url.QueryEscape(tc.author),
				"/api/books/autocomplete?query=x&author=" + url.QueryEscape(tc.author),
			} {
				rec := doJSON(t, r, http.MethodGet, path, nil)
				assert.Equal(t, tc.want, rec.Code, "%s: body=%s", path, rec.Body.String())
			}
			rec := doJSON(t, r, http.MethodPost, "/api/books/author", map[string]any{"author_id": tc.author})
			assert.Equal(t, tc.want, rec.Code, "heading: body=%s", rec.Body.String())
			assert.Empty(t, search.booksReqs, "nothing is searched for an author that is not there")
			assert.Empty(t, search.suggReqs)
		})
	}

	// The legacy heading takes the number the web client sends.
	rec := doJSON(t, newEntityTestRouter(&fakeSearch{}, nil, liveKing), http.MethodPost, "/api/books/author",
		map[string]any{"author_id": 5})
	assert.Equal(t, http.StatusNotFound, rec.Code, "a legacy number under entities: body=%s", rec.Body.String())
}

// The heading names an entity by its display name and, with a namesake, its
// label: what the list's reload shows is what the card's link said.
func TestTheEntityHeadingIsTheEntitysName(t *testing.T) {
	r := newEntityTestRouter(&fakeSearch{}, nil, liveKing)
	rec := doJSON(t, r, http.MethodPost, "/api/books/author", map[string]any{"author_id": kingTwin.String()})
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.JSONEq(t, `{"id":"`+kingTwin.String()+`","full_name":"Стивен Кинг","label":"Фантастика"}`, rec.Body.String())
}

func TestTheEntityAuthorSearchAnswersEntities(t *testing.T) {
	search := &fakeSearch{authorsPage: models.AuthorSearchPage{
		Entities: []models.AuthorEntity{{ID: kingID, FullName: "Стивен Кинг", Label: "Ужасы", BooksCount: 682}},
		Total:    1, Limit: 10,
	}}
	rec := doJSON(t, newEntityTestRouter(search, nil, liveKing), http.MethodGet, "/api/books/authors?author=кинг&limit=10", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.JSONEq(t, `{"authors":[{"id":"`+kingID.String()+`","full_name":"Стивен Кинг","label":"Ужасы","books_count":682}],
		"length":1}`, rec.Body.String())

	empty := &fakeSearch{authorsPage: models.AuthorSearchPage{Total: 0, Limit: 10}}
	rec = doJSON(t, newEntityTestRouter(empty, nil, liveKing), http.MethodGet, "/api/books/authors?author=zz&limit=10", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"authors":[],"length":0}`, rec.Body.String())
}

// Every name of a listed line links to its live entities and to no legacy
// author; the catalog's own author list stays for the administrator's book
// editor, which starts from it.
func TestTheEntityIdentityLinksTheListedLinesToEntities(t *testing.T) {
	search := &fakeSearch{booksPage: models.BookSearchPage{
		Books: []models.Book{
			{ID: 10, Authors: []models.Author{{ID: 1, FullName: "Кинг Стивен"}}},
			{ID: 20, Authors: []models.Author{{ID: 2, FullName: "Автор неизвестен"}}},
		},
		Total: 2, Limit: 10,
	}}
	lines := &fakeAuthorLines{lines: map[int64]models.BookAuthorDisplay{
		10: {Source: models.AuthorDisplayLayer, Authors: []models.AuthorDisplay{
			{Name: "Стивен Кинг", LegacyAuthorID: int64Ptr(1), EntityIDs: []string{kingID.String()}},
			{Name: "Питер Страуб"},
		}},
		20: {Source: models.AuthorDisplayLayer},
	}}
	rec := doJSON(t, newEntityTestRouter(search, lines, liveKing), http.MethodGet, "/api/books/list?title=x", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	books := listedSourceFields(t, rec.Body.Bytes())
	require.Len(t, books, 2)
	assert.JSONEq(t, `[{"name":"Стивен Кинг","entities":[{"id":"`+kingID.String()+
		`","full_name":"Стивен Кинг","label":"Ужасы"}]},{"name":"Питер Страуб"}]`, string(books[0]["authors_display"]))
	assert.JSONEq(t, `[{"id":1,"full_name":"Кинг Стивен"}]`, string(books[0]["authors"]))
	assert.JSONEq(t, `[]`, string(books[1]["authors_display"]), "an empty line is sent empty, not left out")

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	assert.NotContains(t, string(raw["books"]), "legacy_author_id")
}

// entityEmptyLineFixture is the listed book the web card renders in
// BookCard.authors.test.tsx: the API's answer for a book whose author line
// names no one, under the entity identity — shaped after the prod copy's
// book 104540, whose only catalog author is ".". One file on both sides of
// the boundary: this test pins what the handler sends, the card's test what
// a card makes of it. The book's registration date is not in the file (no
// date literal in a fixture): the test gives the book one and checks it
// apart.
//
//nolint:misspell // the frontend's directory is named so
const entityEmptyLineFixture = "../booksdump-frontend/src/features/catalogue/__tests__/fixtures/entity-empty-line.json"

// Under the entity identity a line that names no one is sent as an empty
// line, not left out: a card left without one falls back to the catalog's
// authors and their legacy links, which name no author here. Under the
// legacy identity it is left out, as it always was.
func TestAnEmptyEntityLineIsSentEmpty(t *testing.T) {
	registered := time.Now().UTC()
	search := &fakeSearch{booksPage: models.BookSearchPage{
		Books: []models.Book{{ID: 104540, Title: "Сборник", Lang: "ru", Approved: true, RegisterDate: registered,
			Authors: []models.Author{{ID: 177615, FullName: "."}}}},
		Total: 1, Limit: 10,
	}}
	lines := &fakeAuthorLines{lines: map[int64]models.BookAuthorDisplay{104540: {Source: models.AuthorDisplayLayer}}}

	rec := doJSON(t, newEntityTestRouter(search, lines, liveKing), http.MethodGet, "/api/books/list?title=x", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var page struct {
		Books []map[string]json.RawMessage `json:"books"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	require.Len(t, page.Books, 1)
	var sentDate time.Time
	require.NoError(t, json.Unmarshal(page.Books[0]["registerdate"], &sentDate))
	assert.True(t, registered.Equal(sentDate), "the book's own date")
	delete(page.Books[0], "registerdate")
	got, err := json.Marshal(page.Books[0])
	require.NoError(t, err)
	want, err := os.ReadFile(entityEmptyLineFixture)
	require.NoError(t, err)
	assert.JSONEq(t, string(want), string(got))

	rec = doJSON(t, newEntityTestRouter(search, lines, nil), http.MethodGet, "/api/books/list?title=x", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	books := listedSourceFields(t, rec.Body.Bytes())
	require.Len(t, books, 1)
	_, sent := books[0]["authors_display"]
	assert.False(t, sent, "the legacy identity leaves an empty line out, as before")
}

// Under the legacy identity a legacy ID zero-padded to 32 digits names that
// author, as it always did — it is not read as a UUID.
func TestALegacyZeroPaddedIDIsThatAuthor(t *testing.T) {
	search := &fakeSearch{booksPage: models.BookSearchPage{Books: []models.Book{{ID: 1}}, Total: 1, Limit: 10}}
	rec := doJSON(t, newEntityTestRouter(search, nil, nil), http.MethodGet,
		"/api/books/list?title=x&author=00000000000000000000000000008671", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.Len(t, search.booksReqs, 1)
	assert.Equal(t, int64(8671), search.booksReqs[0].AuthorID)
}
