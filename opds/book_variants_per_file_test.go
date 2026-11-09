package opds

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"gopds-api/database"
	"gopds-api/internal/scanfixture"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// OPDS lists files, never cards: a book uploaded three times and grouped into
// one card for the site is three entries in an author's feed, under either
// author identity.
func TestOPDSAuthorFeedListsEveryFileOfAGroup(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	card := scanfixture.NewVariantCard(t, db)
	want := make([]string, len(card.Books))
	for i, id := range card.Books {
		want[i] = strconv.FormatInt(id, 10)
	}

	gin.SetMode(gin.TestMode)
	repo := database.NewPGBookSourceRepository(db)
	router := func(entities bool) *gin.Engine {
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("user_id", int64(0)); c.Next() })
		lines := &services.AuthorLines{Lookup: repo}
		var reader AuthorEntityReader
		if entities {
			lines.Entities = repo
			reader = repo
		}
		SetupOpdsRoutes(r.Group("/opds"), services.NewSearchService(database.NewPGSearchRepositoryFor(db, entities)),
			lines, reader, database.NewPGBookEntries(db))
		return r
	}
	entries := func(r *gin.Engine, path string) []string {
		t.Helper()
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody))
		require.Equal(t, http.StatusOK, rec.Code, "%s body=%s", path, rec.Body.String())
		var feed struct {
			Entries []struct {
				ID string `xml:"id"`
			} `xml:"entry"`
		}
		require.NoError(t, xml.Unmarshal(rec.Body.Bytes(), &feed))
		ids := make([]string, 0, len(feed.Entries))
		for _, e := range feed.Entries {
			ids = append(ids, e.ID)
		}
		return ids
	}

	assert.ElementsMatch(t, want, entries(router(true), "/opds/author/"+card.Entity.String()),
		"the entity's feed has one entry per file")
	assert.ElementsMatch(t, want, entries(router(false), "/opds/new/0/"+strconv.FormatInt(card.Author, 10)),
		"the legacy author's feed has one entry per file")
	assert.ElementsMatch(t, want, entries(router(true), "/opds/lang/ru/author/"+card.Entity.String()+"/0"),
		"so has the author's feed in a language")
}
