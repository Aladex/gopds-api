package opds

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"gopds-api/database"
	"gopds-api/internal/scanfixture"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The same scanned books under authors.identity = entity (design E, §4.2):
// the author line read from the layer, every name linked to the author
// entities its credit names. The feeds are pinned byte for byte with the
// entities' UUIDs filled in by name ({{entity "…"}}); the author feeds take
// entity UUIDs, /opds/author/<uuid> is a feed, and a legacy number is not
// found — nor is a UUID under the legacy identity.

// withEntities links the characterization's credits to the fixture's
// entities and lets the goldens name them.
func (ch *characterization) withEntities(t *testing.T) map[string]uuid.UUID {
	t.Helper()
	ids := scanfixture.LinkEntities(t, ch.db)
	ch.funcs["entity"] = func(name string) (string, error) {
		if id, ok := ids[name]; ok {
			return id.String(), nil
		}
		return "", os.ErrNotExist
	}
	return ids
}

// entityRouter serves the production routes under the entity identity.
func (ch *characterization) entityRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user_id", int64(0)); c.Next() })
	repo := database.NewPGBookSourceRepository(ch.db)
	SetupOpdsRoutes(r.Group("/opds"), services.NewSearchService(database.NewPGSearchRepositoryFor(ch.db, true)),
		&services.AuthorLines{Lookup: repo, Entities: repo}, repo)
	return r
}

// status serves path and returns the status code.
func status(r *gin.Engine, path string) int {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody))
	return rec.Code
}

func TestScanCharacterizationAtomUnderTheEntityIdentity(t *testing.T) {
	ch := scanCharacterization(t)
	ids := ch.withEntities(t)
	r := ch.entityRouter()
	tolstoy := ids["Толстой"].String()

	feeds := slices.Clone(goldenFeeds)
	feeds[1].path = func(*characterization) string { return "/opds/new/0/" + tolstoy }
	feeds = append(feeds,
		struct {
			name, golden string
			path         func(ch *characterization) string
		}{"the author's own feed", "author", func(*characterization) string { return "/opds/author/" + tolstoy }},
		struct {
			name, golden string
			path         func(ch *characterization) string
		}{"author search", "search-author", func(*characterization) string {
			return "/opds/search-author?name=" + url.QueryEscape("Толстой")
		}},
		struct {
			name, golden string
			path         func(ch *characterization) string
		}{"books of one author in a language", "lang-author", func(*characterization) string {
			return "/opds/lang/ru/author/" + tolstoy + "/0"
		}},
	)
	for _, c := range feeds {
		t.Run(c.name, func(t *testing.T) {
			got := ch.get(t, r, c.path(ch))
			name := c.golden + ".entity.atom.tmpl"
			if os.Getenv("OPDS_WRITE_ENTITY_GOLDEN") != "" {
				require.NoError(t, os.WriteFile(filepath.Join("testdata", "scan_characterization", name+".out"),
					[]byte(got), 0o600))
			}
			assert.Equal(t, ch.golden(t, name), got)
		})
	}

	// A legacy number names no author here, on every author feed.
	legacy := strconv.FormatInt(ch.authors["Толстой лев"], 10)
	for _, path := range []string{
		"/opds/new/0/" + legacy, "/opds/author/" + legacy, "/opds/lang/ru/author/" + legacy + "/0",
		"/opds/new/0/" + uuid.NewString(), "/opds/author/" + uuid.NewString(),
		// The nil UUID is no entity, not "every author".
		"/opds/new/0/" + uuid.Nil.String(), "/opds/author/" + uuid.Nil.String(),
		"/opds/lang/ru/author/" + uuid.Nil.String() + "/0",
		"/opds/new/0/00000000000000000000000000000000", "/opds/author/00000000000000000000000000000000",
		"/opds/lang/ru/author/00000000000000000000000000000000/0",
	} {
		assert.Equal(t, http.StatusNotFound, status(r, path), path)
	}
	assert.Equal(t, http.StatusBadRequest, status(r, "/opds/lang/ru/author/Толстой/0"))

	// Nor does an entity UUID under the legacy identity.
	legacyRouter := ch.router(&services.AuthorLines{Lookup: database.NewPGBookSourceRepository(ch.db)})
	for _, path := range []string{
		"/opds/new/0/" + tolstoy, "/opds/author/" + tolstoy, "/opds/lang/ru/author/" + tolstoy + "/0",
		"/opds/new/0/" + uuid.Nil.String(), "/opds/lang/ru/author/" + uuid.Nil.String() + "/0",
	} {
		assert.Equal(t, http.StatusNotFound, status(legacyRouter, path), path)
	}
	assert.Equal(t, http.StatusOK, status(legacyRouter, "/opds/new/0/"+legacy))
	// The author's own feed is not served under the legacy identity: its
	// <uri> there stays the address nothing answers, as it always was.
	assert.Equal(t, http.StatusNotFound, status(legacyRouter, "/opds/author/"+legacy))
}
