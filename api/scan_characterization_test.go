package api

import (
	"bytes"
	"crypto/md5" // #nosec G501 -- the scan path's duplicate fingerprint, not a security use
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"text/template"
	"time"

	"gopds-api/database"
	"gopds-api/internal/scanfixture"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Part A of phase 9: exact REST JSON for books written by the real scan path.
// The bodies below are the current output byte for byte; only values that
// differ per run — row IDs and the register date — are filled in from the
// database. Once ProcessBook also writes the author metadata source layer,
// these must not change: same author order and names, same empty-list
// contract. The one deliberate change since is the book card's publisher and
// ISBN list, read from the current snapshot: the scan writes these books'
// snapshots without either, so each ends in "publisher":null,"isbn":[].

// scanVars are the per-run values the golden bodies refer to: row IDs, the
// register date as JSON, and the two values derived from the fixture date —
// the document year and the file MD5.
type scanVars struct {
	Book     map[string]int64
	Author   map[string]int64
	Series   map[string]int64
	Genre    map[string]int64
	Register map[string]string
	MD5      map[string]string
	Year     int
}

func loadScanVars(t *testing.T, db *pg.DB, books map[string]int64, now time.Time) scanVars {
	t.Helper()
	v := scanVars{
		Book:     books,
		Author:   map[string]int64{},
		Series:   map[string]int64{},
		Genre:    map[string]int64{},
		Register: map[string]string{},
		MD5:      map[string]string{},
		Year:     now.Year(),
	}
	for entry, content := range scanfixture.Books(now) {
		// #nosec G401 -- recomputing the scan path's duplicate fingerprint
		sum := md5.Sum(content)
		v.MD5[entry] = hex.EncodeToString(sum[:])
	}
	var named []struct {
		Kind string
		Name string
		ID   int64
	}
	_, err := db.Query(&named, `
		SELECT 'author' AS kind, full_name AS name, id FROM opds_catalog_author
		UNION ALL SELECT 'series', ser, id FROM opds_catalog_series
		UNION ALL SELECT 'genre', genre, id FROM opds_catalog_genre`)
	require.NoError(t, err)
	for _, n := range named {
		map[string]map[string]int64{"author": v.Author, "series": v.Series, "genre": v.Genre}[n.Kind][n.Name] = n.ID
	}
	for entry, id := range books {
		var registered time.Time
		_, err = db.QueryOne(pg.Scan(&registered), `SELECT registerdate FROM opds_catalog_book WHERE id = ?`, id)
		require.NoError(t, err)
		raw, marshalErr := json.Marshal(registered)
		require.NoError(t, marshalErr)
		v.Register[entry] = string(raw)
	}
	return v
}

func renderGolden(t *testing.T, golden string, v scanVars) string {
	t.Helper()
	tmpl, err := template.New("golden").Option("missingkey=error").Parse(golden)
	require.NoError(t, err)
	var out bytes.Buffer
	require.NoError(t, tmpl.Execute(&out, v))
	return out.String()
}

// The three characterization books as the REST book list serializes them.
// Nested empty lists are null today (series of a book without one, favorites,
// covers); only the top-level books list and the ISBN list are guaranteed to
// be [].
const (
	restMultiAuthor = `{"id":{{index .Book "multi-author.fb2"}},"path":"characterization.zip","format":"fb2",` +
		`"filename":"multi-author.fb2","registerdate":{{index .Register "multi-author.fb2"}},"docdate":"{{.Year}}",` +
		`"lang":"ru","title":"война и МИР","cover":false,"annotation":"Роман-эпопея о войне и мире.","fav":false,` +
		`"approved":true,"md5":"{{index .MD5 "multi-author.fb2"}}","duplicate_hidden":false,` +
		`"authors":[{"id":{{index .Author "Толстой лев"}},"full_name":"Толстой лев"},` +
		`{"id":{{index .Author "пушкин Анна"}},"full_name":"пушкин Анна"}],` +
		`"series":[{"id":{{index .Series "Эпопея"}},"ser_no":2,"ser":"Эпопея","lang_code":0}],` +
		`"genres":[{"id":{{index .Genre "prose_classic"}},"genre":"prose_classic"},` +
		`{"id":{{index .Genre "prose_history"}},"genre":"prose_history"}],` +
		`"favorites":null,"covers":null,"favorite_count":0,"position":0,"publisher":null,"isbn":[]}`
	restNoAuthor = `{"id":{{index .Book "no-author.fb2"}},"path":"characterization.zip","format":"fb2",` +
		`"filename":"no-author.fb2","registerdate":{{index .Register "no-author.fb2"}},"docdate":"{{.Year}}",` +
		`"lang":"ru","title":"Сборник без автора","cover":false,"annotation":"","fav":false,` +
		`"approved":true,"md5":"{{index .MD5 "no-author.fb2"}}","duplicate_hidden":false,` +
		`"authors":[{"id":{{index .Author "Автор неизвестен"}},"full_name":"Автор неизвестен"}],` +
		`"series":null,"genres":[{"id":{{index .Genre "antology"}},"genre":"antology"}],` +
		`"favorites":null,"covers":null,"favorite_count":0,"position":0,"publisher":null,"isbn":[]}`
	restLatin = `{"id":{{index .Book "latin-mismatch.fb2"}},"path":"characterization.zip","format":"fb2",` +
		`"filename":"latin-mismatch.fb2","registerdate":{{index .Register "latin-mismatch.fb2"}},"docdate":"{{.Year}}",` +
		`"lang":"en","title":"the GREAT book","cover":false,"annotation":"","fav":false,` +
		`"approved":true,"md5":"{{index .MD5 "latin-mismatch.fb2"}}","duplicate_hidden":false,` +
		`"authors":[{"id":{{index .Author "O'brien jOHN"}},"full_name":"O'brien jOHN"},` +
		`{"id":{{index .Author "толстой ЛЕВ"}},"full_name":"толстой ЛЕВ"}],` +
		`"series":[{"id":{{index .Series "Saga"}},"ser_no":0,"ser":"Saga","lang_code":0}],` +
		`"genres":[{"id":{{index .Genre "sf"}},"genre":"sf"}],` +
		`"favorites":null,"covers":null,"favorite_count":0,"position":0,"publisher":null,"isbn":[]}`
)

func bookPage(length int, books ...string) string {
	return `{"books":[` + strings.Join(books, ",") + `],"length":` + strconv.Itoa(length) + `}`
}

func TestScanCharacterizationRESTJSON(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	scanner := services.NewBookScanService(t.TempDir(), t.TempDir(),
		services.NewLanguageDetector(false, 5*time.Second), false, nil)
	now := time.Now()
	books := scanfixture.Ingest(t, t.TempDir(), now, scanner.ProcessBook)
	v := loadScanVars(t, db, books, now)

	// The production wiring of cmd/gopds: the shared search service over the
	// PostgreSQL repository and the source layer for the book card, behind the
	// stub identity middleware.
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", int64(0))
		c.Set("is_superuser", false)
		c.Next()
	})
	SetupBookRoutes(r.Group("/api/books"), &SearchHandler{
		Search:  services.NewSearchService(database.NewPGSearchRepository(db)),
		Sources: database.NewPGBookSourceRepository(db),
	})

	id := func(m map[string]int64, key string) string { return strconv.FormatInt(m[key], 10) }
	cases := []struct {
		name   string
		method string
		path   string
		body   any
		golden string
	}{
		{"search by book id: multi-author", http.MethodGet,
			"/api/books/list?book_id=" + id(books, scanfixture.EntryMultiAuthor), nil, bookPage(1, restMultiAuthor)},
		{"search by book id: no author", http.MethodGet,
			"/api/books/list?book_id=" + id(books, scanfixture.EntryNoAuthor), nil, bookPage(1, restNoAuthor)},
		{"search by book id: latin", http.MethodGet,
			"/api/books/list?book_id=" + id(books, scanfixture.EntryLatin), nil, bookPage(1, restLatin)},
		{"ordinary list by author", http.MethodGet,
			"/api/books/list?author=" + id(v.Author, "Толстой лев"), nil, bookPage(1, restMultiAuthor)},
		{"ordinary list by the fallback author", http.MethodGet,
			"/api/books/list?author=" + id(v.Author, "Автор неизвестен"), nil, bookPage(1, restNoAuthor)},
		// Newest first: the reverse of ingestion order.
		{"ordinary list of everything", http.MethodGet, "/api/books/list?limit=10", nil,
			bookPage(1, restLatin, restNoAuthor, restMultiAuthor)},
		{"title search hit", http.MethodGet, "/api/books/list?title=" + url.QueryEscape("Сборник"), nil,
			bookPage(1, restNoAuthor)},
		{"title search miss is an empty array", http.MethodGet,
			"/api/books/list?title=" + url.QueryEscape("Отсутствующее"), nil, bookPage(0)},
		{"author by id", http.MethodPost, "/api/books/author", map[string]int64{"author_id": v.Author["пушкин Анна"]},
			`{"id":{{index .Author "пушкин Анна"}},"full_name":"пушкин Анна"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := doJSON(t, r, c.method, c.path, c.body)
			require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
			assert.Equal(t, renderGolden(t, c.golden, v), rec.Body.String())
		})
	}
}
