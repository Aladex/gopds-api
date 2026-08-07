package commands

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"testing"

	"gopds-api/api"
	"gopds-api/database"
	"gopds-api/internal/testdb"
	"gopds-api/models"
	"gopds-api/opds"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The parity tests next door drive the adapters over a stand-in that answers
// every request with the same three rows. That proves an adapter passes the
// needle down and renders what it is handed — worth pinning, and all it can
// prove: a mistake in the request DTO, in the visibility flags or in the SQL
// leaves it green, because the stand-in never looks at the request.
//
// These drive the same three adapters over the real service, repository and
// catalog. What they compare is the canonical answer — the ordered book IDs
// and the one exact total the repository computed — against what each client
// actually rendered, through its own query string, its own page size and its
// own format.
//
// Totals cannot be compared literally, and the plan asking for that was a
// mistake: REST serializes a page count, Telegram an exact row count, and OPDS
// no number at all, only whether a next page exists. Each is checked as a
// function of the same underlying total instead.

var parityDB *pg.DB

func TestMain(m *testing.M) {
	// Configured but unreachable fails the run: these are the cases that
	// prove the clients agree, and skipping them silently is how the claim
	// stayed unverified for eight phases.
	if cfg, ok := testdb.Configured(); ok {
		conn, err := testdb.Connect(cfg, database.DisableJIT)
		if err != nil {
			fmt.Fprintf(os.Stderr, "commands: %v\n", err)
			os.Exit(1)
		}
		parityDB = conn
		database.SetDB(conn)
	}

	code := m.Run()

	if parityDB != nil {
		_ = parityDB.Close()
	}
	os.Exit(code)
}

func requireParityDB(t *testing.T) services.PublicSearch {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if parityDB == nil {
		t.Skip(testdb.SkipReason)
	}
	return services.NewSearchService(database.NewPGSearchRepository(parityDB))
}

// parityCase is one reader intent, expressed the way each client expresses it.
type parityCase struct {
	name     string
	needle   string
	language string
}

var parityCases = []parityCase{
	{name: "exact title", needle: "война и мир"},
	{name: "partial title", needle: "гарри поттер"},
	{name: "typo", needle: "гари потер"},
	{name: "reordered words", needle: "поттер гарри"},
	{name: "language restricted", needle: "harry potter", language: "en"},
	{name: "zero result", needle: "qqqzzzxxxwwwvvv"},
}

const parityPage = 10

// canonical asks the service directly for the answer every client must show.
func canonical(t *testing.T, svc services.PublicSearch, c parityCase, limit int) models.BookSearchPage {
	t.Helper()
	page, err := svc.SearchBooks(context.Background(), models.BookSearchRequest{
		Query:    c.needle,
		Language: c.language,
		Limit:    limit,
	})
	require.NoError(t, err)
	return page
}

func idsOf(books []models.Book) []int64 {
	out := make([]int64, 0, len(books))
	for i := range books {
		out = append(out, books[i].ID)
	}
	return out
}

// restRouter wires the REST adapter to the given search, as a signed-in
// non-moderator.
func restRouter(svc services.PublicSearch) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", int64(0))
		c.Set("is_superuser", false)
		c.Next()
	})
	h := &api.SearchHandler{Search: svc}
	r.GET("/api/books/list", h.Books)
	return r
}

// opdsRouter wires both OPDS search feeds to the given search.
func opdsRouter(svc services.PublicSearch) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user_id", int64(0)); c.Next() })
	h := &opds.SearchHandler{Search: svc}
	r.GET("/opds/books", h.Books)
	r.GET("/opds/lang/:lang/search-books", h.BooksByLanguage)
	return r
}

// atomFeed is the part of an OPDS feed these tests read: the entry IDs, in
// order, and whether the feed offers a next page.
type atomFeed struct {
	Links []struct {
		Rel string `xml:"rel,attr"`
	} `xml:"link"`
	Entries []struct {
		ID string `xml:"id"`
	} `xml:"entry"`
}

func (f atomFeed) ids(t *testing.T) []int64 {
	t.Helper()
	out := make([]int64, 0, len(f.Entries))
	for _, e := range f.Entries {
		id, err := strconv.ParseInt(e.ID, 10, 64)
		require.NoError(t, err, "entry id %q is not a book id", e.ID)
		out = append(out, id)
	}
	return out
}

func (f atomFeed) hasNext() bool {
	for _, l := range f.Links {
		if l.Rel == "next" {
			return true
		}
	}
	return false
}

// serveOPDS runs one feed request and parses it, or reports that the feed was
// the "nothing found" document.
func serveOPDS(t *testing.T, r *gin.Engine, target string) (atomFeed, bool) {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	var feed atomFeed
	if err := xml.Unmarshal(rec.Body.Bytes(), &feed); err != nil {
		return atomFeed{}, false
	}
	return feed, len(feed.Entries) > 0
}

// TestSearchParityOverTheRealCatalog is the Definition of Done's cross-client
// claim, checked against the catalog rather than asserted.
func TestSearchParityOverTheRealCatalog(t *testing.T) {
	svc := requireParityDB(t)

	for _, c := range parityCases {
		t.Run(c.name, func(t *testing.T) {
			want := canonical(t, svc, c, parityPage)
			wantIDs := idsOf(want.Books)

			t.Run("rest", func(t *testing.T) {
				target := "/api/books/list?limit=10&title=" + urlQuery(c.needle)
				if c.language != "" {
					target += "&lang=" + c.language
				}
				rec := httptest.NewRecorder()
				restRouter(svc).ServeHTTP(rec,
					httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, http.NoBody))
				require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

				var got api.ExportAnswer
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
				assert.Equal(t, wantIDs, idsOf(got.Books), "REST rendered a different page")
				// REST reports the total as a page count over the same limit.
				assert.Equal(t, expectedPages(want.Total, parityPage), got.Length,
					"REST paged %d rows differently", want.Total)
			})

			t.Run("opds", func(t *testing.T) {
				target := "/opds/books?title=" + urlQuery(c.needle)
				if c.language != "" {
					target = "/opds/lang/" + c.language + "/search-books?title=" + urlQuery(c.needle)
				}
				feed, hasEntries := serveOPDS(t, opdsRouter(svc), target)

				if len(wantIDs) == 0 {
					assert.False(t, hasEntries, "OPDS showed entries for a query that finds nothing")
					return
				}
				require.True(t, hasEntries, "OPDS showed nothing for a query that finds rows")
				// OPDS pages by ten, the same window asked for above.
				assert.Equal(t, wantIDs, feed.ids(t), "OPDS rendered a different page")
				// It publishes no total; what it must get right is whether one
				// more page exists, which is a function of the same total.
				assert.Equal(t, want.Total > parityPage, feed.hasNext(),
					"OPDS promised the wrong next page for %d rows", want.Total)
			})

			t.Run("telegram", func(t *testing.T) {
				bot := newCommandProcessorWithDeps(svc, func(int64) (models.User, error) {
					return models.User{ID: 0, BooksLang: c.language}, nil
				})
				result, err := bot.ExecuteFindBookWithPagination(context.Background(), c.needle, 1, 0, parityPage)
				require.NoError(t, err)

				if len(wantIDs) == 0 {
					assert.Empty(t, result.Books, "Telegram showed rows for a query that finds nothing")
					return
				}
				assert.Equal(t, wantIDs, idsOf(result.Books), "Telegram rendered a different page")
				require.NotNil(t, result.SearchParams)
				// Telegram carries the exact total, unrounded.
				assert.Equal(t, want.Total, result.SearchParams.TotalCount,
					"Telegram carried a different total")
			})
		})
	}
}

// TestSearchParityAcrossPageSizes is the other half of the claim. The clients
// page differently — OPDS by ten, the bot by five — so equality of pages is
// the wrong contract; what must hold is that they are windows onto one ordered
// sequence.
func TestSearchParityAcrossPageSizes(t *testing.T) {
	svc := requireParityDB(t)

	for _, c := range parityCases {
		if c.name == "zero result" {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			want := canonical(t, svc, c, parityPage)
			if len(want.Books) < parityPage {
				t.Skipf("%q returns %d rows, too few to page", c.needle, len(want.Books))
			}
			wantIDs := idsOf(want.Books)

			bot := newCommandProcessorWithDeps(svc, func(int64) (models.User, error) {
				return models.User{ID: 0, BooksLang: c.language}, nil
			})

			// The bot's own page size takes the first five of the same ten.
			first, err := bot.ExecuteFindBookWithPagination(context.Background(), c.needle, 1, 0, 5)
			require.NoError(t, err)
			assert.Equal(t, wantIDs[:5], idsOf(first.Books), "the bot's first page left the sequence")

			// And its second page continues it, without repeating or skipping.
			second, err := bot.ExecuteFindBookWithPagination(context.Background(), c.needle, 1, 5, 5)
			require.NoError(t, err)
			assert.Equal(t, wantIDs[5:10], idsOf(second.Books), "the bot's second page left the sequence")

			// Both pages describe the same total, and it is the canonical one.
			require.NotNil(t, first.SearchParams)
			require.NotNil(t, second.SearchParams)
			assert.Equal(t, want.Total, first.SearchParams.TotalCount)
			assert.Equal(t, want.Total, second.SearchParams.TotalCount)
		})
	}
}

// expectedPages mirrors how REST turns a total into a page count.
func expectedPages(total, limit int) int {
	if limit <= 0 || total <= 0 {
		return 0
	}
	pages := total / limit
	if total%limit != 0 {
		pages++
	}
	return pages
}

// urlQuery escapes a needle for a query string.
func urlQuery(s string) string {
	return (&url.URL{Path: s}).EscapedPath()
}
