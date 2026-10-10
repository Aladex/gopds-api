package opds

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

// Part A of phase 9: exact OPDS Atom for books written by the real scan path.
// The golden feeds in testdata/scan_characterization are the current output
// byte for byte, with three kinds of per-run values filled in: book and author
// IDs (by fixture entry and author name), the document year, and the
// request-time <updated> stamps, which are blanked on both sides.

// updatedStamps matches the feed and entry <updated> stamps.
var updatedStamps = regexp.MustCompile(`<updated>[^<]*</updated>`)

const updatedPlaceholder = "<updated>UPDATED</updated>"

// characterization is the scanned fixture catalog: the scratch database, the
// book IDs by fixture entry, the catalog author IDs by name, the time of the
// scan and the template functions that fill both kinds of ID into a golden.
type characterization struct {
	db      *pg.DB
	books   map[string]int64
	authors map[string]int64
	now     time.Time
	funcs   template.FuncMap
}

func scanCharacterization(t *testing.T) *characterization {
	t.Helper()
	db := scanfixture.ScratchDB(t)
	scanner := services.NewBookScanService(t.TempDir(), t.TempDir(),
		services.NewLanguageDetector(false, 5*time.Second), false, nil)
	now := time.Now()
	books := scanfixture.Ingest(t, t.TempDir(), now, scanner.ProcessBook)

	authors := map[string]int64{}
	var rows []struct {
		ID       int64
		FullName string
	}
	_, err := db.Query(&rows, `SELECT id, full_name FROM opds_catalog_author`)
	require.NoError(t, err)
	for _, r := range rows {
		authors[r.FullName] = r.ID
	}
	funcs := template.FuncMap{
		"book": func(entry string) (int64, error) {
			if id, ok := books[entry]; ok {
				return id, nil
			}
			return 0, os.ErrNotExist
		},
		"author": func(name string) (int64, error) {
			if id, ok := authors[name]; ok {
				return id, nil
			}
			return 0, os.ErrNotExist
		},
	}
	return &characterization{db: db, books: books, authors: authors, now: now, funcs: funcs}
}

// router serves the production routes of cmd/gopds over the shared search
// service, with the given author lines; the anonymous user has no favorites.
func (ch *characterization) router(lines *services.AuthorLines) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user_id", int64(0)); c.Next() })
	SetupOpdsRoutes(r.Group("/opds"), services.NewSearchService(database.NewPGSearchRepository(ch.db)), lines)
	return r
}

// get serves path and returns the feed with its <updated> stamps blanked.
func (ch *characterization) get(t *testing.T, r *gin.Engine, path string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.Equal(t, "application/atom+xml;charset=utf-8", rec.Header().Get("Content-Type"))
	return updatedStamps.ReplaceAllString(rec.Body.String(), updatedPlaceholder)
}

// golden fills the IDs and the year into a golden feed.
func (ch *characterization) golden(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "scan_characterization", name))
	require.NoError(t, err)
	tmpl, err := template.New(name).Funcs(ch.funcs).Option("missingkey=error").Parse(string(raw))
	require.NoError(t, err)
	var want bytes.Buffer
	require.NoError(t, tmpl.Execute(&want, struct{ Year int }{ch.now.Year()}))
	return want.String()
}

// goldenFeeds are the feeds pinned byte for byte, by the golden of the
// catalog's author line; the author layer's golden adds ".layer".
var goldenFeeds = []struct {
	name, golden string
	path         func(ch *characterization) string
}{
	// Navigation entries, then the books newest first, each with its
	// authors in link order as related links and <author> elements.
	{"newest books", "newest", func(*characterization) string { return "/opds/new/0/0" }},
	{"books of one author", "author", func(ch *characterization) string {
		return "/opds/new/0/" + strconv.FormatInt(ch.authors["Толстой лев"], 10)
	}},
	{"book search", "search", func(*characterization) string { return "/opds/books?title=" + url.QueryEscape("Сборник") }},
}

func TestScanCharacterizationAtom(t *testing.T) {
	ch := scanCharacterization(t)
	r := ch.router(nil)
	for _, c := range goldenFeeds {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, ch.golden(t, c.golden+".atom.tmpl"), ch.get(t, r, c.path(ch)))
		})
	}
}

// The blanking must not hide anything but the stamps themselves.
func TestScanCharacterizationAtomBlanksOnlyUpdatedStamps(t *testing.T) {
	in := "<entry><updated>" + time.Now().UTC().Format(time.RFC3339) + "</updated><id>7</id><updated></updated></entry>"
	assert.Equal(t, "<entry>"+updatedPlaceholder+"<id>7</id>"+updatedPlaceholder+"</entry>",
		updatedStamps.ReplaceAllString(in, updatedPlaceholder))
}
