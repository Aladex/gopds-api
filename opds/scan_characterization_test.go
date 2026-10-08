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

func TestScanCharacterizationAtom(t *testing.T) {
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

	// The production routes of cmd/gopds over the shared search service; the
	// anonymous user has no favorites.
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user_id", int64(0)); c.Next() })
	SetupOpdsRoutes(r.Group("/opds"), services.NewSearchService(database.NewPGSearchRepository(db)))

	cases := []struct {
		name, path, golden string
	}{
		// Navigation entries, then the books newest first, each with its
		// authors in link order as related links and <author> elements.
		{"newest books", "/opds/new/0/0", "newest.atom.tmpl"},
		{"books of one author", "/opds/new/0/" + strconv.FormatInt(authors["Толстой лев"], 10), "author.atom.tmpl"},
		{"book search", "/opds/books?title=" + url.QueryEscape("Сборник"), "search.atom.tmpl"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, readErr := os.ReadFile(filepath.Join("testdata", "scan_characterization", c.golden))
			require.NoError(t, readErr)
			tmpl, parseErr := template.New(c.golden).Funcs(funcs).Option("missingkey=error").Parse(string(raw))
			require.NoError(t, parseErr)
			var want bytes.Buffer
			require.NoError(t, tmpl.Execute(&want, struct{ Year int }{now.Year()}))

			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, c.path, http.NoBody))
			require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
			assert.Equal(t, "application/atom+xml;charset=utf-8", rec.Header().Get("Content-Type"))
			assert.Equal(t, want.String(), updatedStamps.ReplaceAllString(rec.Body.String(), updatedPlaceholder))
		})
	}
}

// The blanking must not hide anything but the stamps themselves.
func TestScanCharacterizationAtomBlanksOnlyUpdatedStamps(t *testing.T) {
	in := "<entry><updated>" + time.Now().UTC().Format(time.RFC3339) + "</updated><id>7</id><updated></updated></entry>"
	assert.Equal(t, "<entry>"+updatedPlaceholder+"<id>7</id>"+updatedPlaceholder+"</entry>",
		updatedStamps.ReplaceAllString(in, updatedPlaceholder))
}
