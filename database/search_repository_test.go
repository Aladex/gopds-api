package database

import (
	"context"
	"testing"
	"time"

	"gopds-api/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPGSearchRepositoryExactBeatsFuzzy pins the lexicographic rank order on
// the shared fixture: exact normalized title first, prefix second, full
// query-word coverage third, interior substring fourth, typo/fuzzy last. The
// expectation is a literal list of fixture IDs, not a score recomputed in the
// test.
func TestPGSearchRepositoryExactBeatsFuzzy(t *testing.T) {
	withSearchFixture(t, func(f *searchFixture) {
		repo := NewPGSearchRepository(f.tx)
		page, err := repo.SearchBooks(context.Background(), models.BookSearchRequest{
			Query:    "Война и мир",
			UserID:   f.UserIDs["reader"],
			Language: "fx",
			Limit:    50,
		})
		require.NoError(t, err)

		want := []int64{
			// Exact tier: the favorited book wins the tie-break, the rest
			// follow in fixture (ascending ID) order.
			f.BookIDs["favBook"],
			f.BookIDs["exact"],
			f.BookIDs["dash"],
			f.BookIDs["seriesBook"],
			f.BookIDs["genreBook"],
			f.BookIDs["collectionBook"],
			f.BookIDs["curatedBook"],
			f.BookIDs["curatedIgnored"],
			// Then prefix, full word coverage, interior substring, typo.
			f.BookIDs["prefix"],
			f.BookIDs["allWords"],
			f.BookIDs["substring"],
			f.BookIDs["typo"],
		}
		got := make([]int64, len(page.Books))
		for i, b := range page.Books {
			got[i] = b.ID
		}
		assert.Equal(t, want, got)
		assert.Equal(t, len(want), page.Total)
	})
}

// TestPGSearchRepositoryCandidateLanes pins one behavior per subtest:
// normalization equivalence, the word-coverage lane, the typo lane and the
// fuzzy gate for one- and two-rune queries. Expectations are fixture IDs.
func TestPGSearchRepositoryCandidateLanes(t *testing.T) {
	search := func(f *searchFixture, query string) models.BookSearchPage {
		t.Helper()
		repo := NewPGSearchRepository(f.tx)
		page, err := repo.SearchBooks(context.Background(), models.BookSearchRequest{
			Query:    query,
			UserID:   f.UserIDs["reader"],
			Language: "fx",
			Limit:    50,
		})
		require.NoError(t, err)
		return page
	}
	ids := func(page models.BookSearchPage) []int64 {
		got := make([]int64, len(page.Books))
		for i, b := range page.Books {
			got[i] = b.ID
		}
		return got
	}

	t.Run("е and ё are the same letter", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			page := search(f, "Ежик в тумане")
			assert.Equal(t, []int64{f.BookIDs["yo"], f.BookIDs["spaces"]}, ids(page))
			assert.Equal(t, 2, page.Total)
		})
	})

	t.Run("unicode composed and decomposed forms meet", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			page := search(f, "Café")
			assert.Equal(t, []int64{f.BookIDs["decomposed"]}, ids(page))
			assert.Equal(t, 1, page.Total)
		})
	})

	t.Run("quotes, apostrophes and the numero sign vanish", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			page := search(f, "Мастер и Маргарита")
			assert.Equal(t, []int64{f.BookIDs["quotes"]}, ids(page))
			assert.Equal(t, 1, page.Total)

			page = search(f, "Книга № 2")
			assert.Equal(t, []int64{f.BookIDs["numero"]}, ids(page))
			assert.Equal(t, 1, page.Total)
		})
	})

	t.Run("dashes and repeated whitespace vanish", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			page := search(f, "  Война—и–мир  ")
			// The needle is "война и мир", so the ranking equals the main
			// ordering test: the favorited exact book leads, total is 12.
			require.NotEmpty(t, page.Books)
			assert.Equal(t, f.BookIDs["favBook"], page.Books[0].ID)
			assert.Equal(t, 12, page.Total)
		})
	})

	t.Run("swapped query words meet via the word-coverage lane", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			// The needle {тумане, в, ежик} is exactly the title's word set,
			// reordered — no exact, prefix or substring match exists.
			page := search(f, "тумане в ежик")
			assert.Equal(t, []int64{f.BookIDs["yo"], f.BookIDs["spaces"]}, ids(page))
			assert.Equal(t, 2, page.Total)
		})
	})

	t.Run("a one-rune typo is found through the fuzzy lanes", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			// Nothing matches exactly or by substring; only word/trigram
			// similarity above the floors can admit these two books.
			page := search(f, "ежик в туманю")
			assert.Equal(t, []int64{f.BookIDs["yo"], f.BookIDs["spaces"]}, ids(page))
			assert.Equal(t, 2, page.Total)
		})
	})

	t.Run("a one-rune query works without fuzzy", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			page := search(f, "Я")
			assert.Equal(t, []int64{f.BookIDs["oneChar"]}, ids(page))
			assert.Equal(t, 1, page.Total)
		})
	})

	t.Run("a two-rune query keeps prefix and substring, fuzzy stays off", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			page := search(f, "во")
			want := []int64{
				// Prefix tier: identical normalized titles tie on the
				// favorite count, then on ascending IDs; the longer prefix
				// book follows them through length_delta.
				f.BookIDs["favBook"],
				f.BookIDs["exact"],
				f.BookIDs["dash"],
				f.BookIDs["seriesBook"],
				f.BookIDs["genreBook"],
				f.BookIDs["collectionBook"],
				f.BookIDs["curatedBook"],
				f.BookIDs["curatedIgnored"],
				f.BookIDs["prefix"],
				// Substring tier: the shorter "мир и война" wins on
				// length_delta over the longer interior match.
				f.BookIDs["allWords"],
				f.BookIDs["substring"],
			}
			assert.Equal(t, want, ids(page))
			assert.Equal(t, len(want), page.Total)
			// The typo row "Вайна и мир" contains no "во" and fuzzy is
			// disabled below three runes, so it cannot appear.
			assert.NotContains(t, ids(page), f.BookIDs["typo"])
		})
	})

	t.Run("a two-rune near miss finds nothing when fuzzy is off", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			page := search(f, "ом")
			assert.Empty(t, page.Books)
			assert.Equal(t, 0, page.Total)
			assert.NotEmpty(t, page.QueryHash)
		})
	})

	t.Run("a single-word transposition stays below the book-search floor", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			// Transposing the last two letters of "Океан" scores 0.333 against
			// the title: the old 0.3 trigram floor rescued it, the book-search
			// floor of 0.5 must not. Nothing else in the fixture comes close.
			page := search(f, "окена")
			assert.NotContains(t, ids(page), f.BookIDs["transposition"])
			assert.Equal(t, 0, page.Total)
		})
	})
}

// TestPGSearchRepositoryVisibilityAndScopes pins fail-closed visibility and
// every scope: each filter narrows the candidate set inside the same SQL
// request, and an unknown scope ID yields an empty page, never a global
// search. Expectations are fixture IDs on the shared catalogue.
func TestPGSearchRepositoryVisibilityAndScopes(t *testing.T) {
	search := func(f *searchFixture, req models.BookSearchRequest) models.BookSearchPage {
		t.Helper()
		repo := NewPGSearchRepository(f.tx)
		page, err := repo.SearchBooks(context.Background(), req)
		require.NoError(t, err)
		return page
	}
	ids := func(page models.BookSearchPage) []int64 {
		got := make([]int64, len(page.Books))
		for i, b := range page.Books {
			got[i] = b.ID
		}
		return got
	}
	base := func(f *searchFixture) models.BookSearchRequest {
		return models.BookSearchRequest{
			Query: "Война и мир", UserID: f.UserIDs["reader"], Language: "fx", Limit: 50,
		}
	}
	exactTier := func(f *searchFixture) []int64 {
		return []int64{
			f.BookIDs["favBook"],
			f.BookIDs["exact"],
			f.BookIDs["dash"],
			f.BookIDs["seriesBook"],
			f.BookIDs["genreBook"],
			f.BookIDs["collectionBook"],
			f.BookIDs["curatedBook"],
			f.BookIDs["curatedIgnored"],
		}
	}

	t.Run("language picks exactly one lane", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			req := base(f)
			req.Language = "fy"
			page := search(f, req)
			assert.Equal(t, []int64{f.BookIDs["english"]}, ids(page))
			assert.Equal(t, 1, page.Total)

			// "all" disables the filter, so production-dump rows join the
			// result. What can be pinned exactly is the fixture subset: it
			// keeps its relative order no matter what real rows surround it.
			req.Language = "all"
			req.Limit = 2000
			page = search(f, req)
			var fixtureIDs []int64
			for _, id := range ids(page) {
				if id >= searchFixtureIDBase {
					fixtureIDs = append(fixtureIDs, id)
				}
			}
			want := append([]int64{
				f.BookIDs["favBook"], f.BookIDs["exact"], f.BookIDs["dash"], f.BookIDs["english"],
				f.BookIDs["seriesBook"], f.BookIDs["genreBook"], f.BookIDs["collectionBook"],
				f.BookIDs["curatedBook"], f.BookIDs["curatedIgnored"],
			}, f.BookIDs["prefix"], f.BookIDs["allWords"], f.BookIDs["substring"], f.BookIDs["typo"])
			assert.Equal(t, want, fixtureIDs)
			assert.GreaterOrEqual(t, page.Total, 13)
		})
	})

	t.Run("unapproved browses only the unapproved lane", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			req := base(f)
			req.Unapproved = true
			page := search(f, req)
			assert.Equal(t, []int64{f.BookIDs["unapproved"]}, ids(page))
			assert.Equal(t, 1, page.Total)
		})
	})

	t.Run("hidden duplicates stay out unless asked for", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			page := search(f, base(f))
			assert.NotContains(t, ids(page), f.BookIDs["hidden"])

			req := base(f)
			req.IncludeHidden = true
			page = search(f, req)
			assert.Equal(t, 13, page.Total)
			want := append([]int64{
				f.BookIDs["favBook"], f.BookIDs["exact"], f.BookIDs["dash"], f.BookIDs["hidden"],
				f.BookIDs["seriesBook"], f.BookIDs["genreBook"], f.BookIDs["collectionBook"],
				f.BookIDs["curatedBook"], f.BookIDs["curatedIgnored"],
			}, f.BookIDs["prefix"], f.BookIDs["allWords"], f.BookIDs["substring"], f.BookIDs["typo"])
			assert.Equal(t, want, ids(page))
		})
	})

	t.Run("author scope keeps only that author's books", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			req := base(f)
			req.AuthorID = f.AuthorIDs["tolstoy"]
			page := search(f, req)
			assert.Equal(t, append(exactTier(f), f.BookIDs["prefix"]), ids(page))
			assert.Equal(t, 9, page.Total)

			req.AuthorID = f.AuthorIDs["other"]
			page = search(f, req)
			assert.Equal(t,
				[]int64{f.BookIDs["allWords"], f.BookIDs["substring"], f.BookIDs["typo"]},
				ids(page))
			assert.Equal(t, 3, page.Total)
		})
	})

	t.Run("series, genre and collection scopes", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			req := base(f)
			req.SeriesID = f.SeriesIDs["great"]
			page := search(f, req)
			assert.Equal(t, []int64{f.BookIDs["seriesBook"]}, ids(page))
			assert.Equal(t, 1, page.Total)

			req = base(f)
			req.GenreID = f.GenreIDs["roman"]
			page = search(f, req)
			assert.Equal(t, []int64{f.BookIDs["genreBook"]}, ids(page))
			assert.Equal(t, 1, page.Total)

			req = base(f)
			req.CollectionID = f.CollectionIDs["shelf"]
			page = search(f, req)
			assert.Equal(t, []int64{f.BookIDs["collectionBook"]}, ids(page))
			assert.Equal(t, 1, page.Total)
		})
	})

	t.Run("curated scope requires a matched item in a public curated collection", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			req := base(f)
			req.CuratedCollectionID = f.CollectionIDs["curated"]
			page := search(f, req)
			// curatedIgnored is in the same collection but with status
			// "ignored", which is not membership.
			assert.Equal(t, []int64{f.BookIDs["curatedBook"]}, ids(page))
			assert.Equal(t, 1, page.Total)
		})
	})

	t.Run("favorites keeps only the reader's favorites", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			req := base(f)
			req.Favorites = true
			page := search(f, req)
			assert.Equal(t, []int64{f.BookIDs["favBook"]}, ids(page))
			assert.Equal(t, 1, page.Total)
		})
	})

	t.Run("unknown scope IDs yield an empty page, not a global search", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			for _, apply := range map[string]func(*models.BookSearchRequest, int64){
				"AuthorID":            func(r *models.BookSearchRequest, id int64) { r.AuthorID = id },
				"SeriesID":            func(r *models.BookSearchRequest, id int64) { r.SeriesID = id },
				"GenreID":             func(r *models.BookSearchRequest, id int64) { r.GenreID = id },
				"CollectionID":        func(r *models.BookSearchRequest, id int64) { r.CollectionID = id },
				"CuratedCollectionID": func(r *models.BookSearchRequest, id int64) { r.CuratedCollectionID = id },
			} {
				req := base(f)
				// f.id() hands out an ID nothing seeds.
				apply(&req, f.id())
				page := search(f, req)
				assert.Empty(t, page.Books)
				assert.Equal(t, 0, page.Total)
			}
		})
	})

	t.Run("author query filters inside the same request", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			for _, authorQuery := range []string{"толстой", "то", "толстй лев"} {
				req := base(f)
				req.AuthorQuery = authorQuery
				page := search(f, req)
				// "толстой" and "то" hit the exact/prefix lanes; "толстй лев"
				// is neither prefix nor substring and passes only the
				// word-similarity floor (0.64 >= 0.60).
				assert.Equal(t, append(exactTier(f), f.BookIDs["prefix"]), ids(page), "AuthorQuery %q", authorQuery)
				assert.Equal(t, 9, page.Total, "AuthorQuery %q", authorQuery)
			}

			req := base(f)
			req.AuthorQuery = "козлов"
			page := search(f, req)
			assert.Empty(t, page.Books)
			assert.Equal(t, 0, page.Total)
		})
	})

	t.Run("an author query without a book query fails closed", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			req := base(f)
			req.Query = ""
			req.AuthorQuery = "толстой"
			page := search(f, req)
			assert.Empty(t, page.Books)
			assert.Equal(t, 0, page.Total)
			assert.NotEmpty(t, page.QueryHash)
		})
	})
}

// TestPGSearchRepositoryCancelledContext proves cancellation reaches the
// database and comes back as context.Canceled at the repository boundary. Each
// case gets its own fixture transaction: a mid-flight cancel leaves the
// connection in a bad state, and go-pg closes it, which ends the server-side
// session and rolls the fixture back — reusing that transaction afterwards
// would fail instantly with the stale connection error, not with the
// cancellation under test.
func TestPGSearchRepositoryCancelledContext(t *testing.T) {
	t.Run("an already-cancelled context stops the call", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			repo := NewPGSearchRepository(f.tx)
			req := models.BookSearchRequest{
				Query: "Война и мир", UserID: f.UserIDs["reader"], Language: "fx", Limit: 50,
			}

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := repo.SearchBooks(ctx, req)
			require.Error(t, err)
			assert.ErrorIs(t, err, context.Canceled)
		})
	})

	t.Run("cancelling a lock-waiting statement aborts it promptly", func(t *testing.T) {
		requireDatabase(t)

		// An EXCLUSIVE lock conflicts with the ACCESS SHARE the search takes on
		// every table in its range table, so the statement waits server-side.
		// This case runs on the pool, not the fixture transaction: the
		// fixture's own ROW EXCLUSIVE write locks would block LOCK TABLE
		// itself, and the cancel would destroy the fixture connection anyway.
		blocker, err := db.Begin()
		require.NoError(t, err)
		_, err = blocker.Exec(`LOCK TABLE opds_catalog_book IN EXCLUSIVE MODE`)
		require.NoError(t, err)
		defer func() { _ = blocker.Rollback() }()

		repo := NewPGSearchRepository(db)
		req := models.BookSearchRequest{Query: "война", Language: AllLanguages, Limit: 1}

		waitCtx, stopWaiting := context.WithCancel(context.Background())
		started := time.Now()
		go func() {
			time.Sleep(100 * time.Millisecond)
			stopWaiting()
		}()
		_, err = repo.SearchBooks(waitCtx, req)
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Less(t, time.Since(started), 900*time.Millisecond)
	})

	t.Run("the driver reports the abort as SQLSTATE 57014", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			repo := NewPGSearchRepository(f.tx)

			// The raw driver path reports the same abort as SQLSTATE 57014,
			// never as context.Canceled — the evidence preferContextError
			// exists for.
			sleepCtx, stopSleep := context.WithCancel(context.Background())
			errCh := make(chan error, 1)
			go func() {
				_, err := repo.db.ExecContext(sleepCtx, `SELECT pg_sleep(0.9)`)
				errCh <- err
			}()
			time.Sleep(50 * time.Millisecond)
			stopSleep()
			sleepErr := <-errCh
			require.Error(t, sleepErr)
			assert.Contains(t, sleepErr.Error(), "canceling statement due to user request")
		})
	})
}

// TestPGSearchRepositoryDedupeTotalAndStablePages proves the structural
// dedupe by book.id, the exact pre-pagination total over the 510-title
// regression block, and a stable page order across repeated calls.
func TestPGSearchRepositoryDedupeTotalAndStablePages(t *testing.T) {
	search := func(f *searchFixture, req models.BookSearchRequest) models.BookSearchPage {
		t.Helper()
		repo := NewPGSearchRepository(f.tx)
		page, err := repo.SearchBooks(context.Background(), req)
		require.NoError(t, err)
		return page
	}
	ids := func(page models.BookSearchPage) []int64 {
		got := make([]int64, len(page.Books))
		for i, b := range page.Books {
			got[i] = b.ID
		}
		return got
	}

	t.Run("a book with two matching relations occurs once", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			// Legacy data carries duplicate junction rows: the same
			// author-book pair linked twice. Dedupe by book.id must hold.
			f.exec(`INSERT INTO opds_catalog_bauthor (id, author_id, book_id) VALUES (?, ?, ?)`,
				f.id(), f.AuthorIDs["tolstoy"], f.BookIDs["exact"])

			page := search(f, models.BookSearchRequest{
				Query: "Война и мир", UserID: f.UserIDs["reader"], Language: "fx",
				AuthorID: f.AuthorIDs["tolstoy"], Limit: 50,
			})

			var occurrences int
			for _, b := range page.Books {
				if b.ID == f.BookIDs["exact"] {
					occurrences++
				}
			}
			assert.Equal(t, 1, occurrences)
			assert.Equal(t, 9, page.Total)
		})
	})

	t.Run("two visible books with the same title remain two books", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			page := search(f, models.BookSearchRequest{
				Query: "Сто лет одиночества", UserID: f.UserIDs["reader"], Language: "fx", Limit: 50,
			})
			assert.Equal(t, []int64{f.BookIDs["dupA"], f.BookIDs["dupB"]}, ids(page))
			assert.Equal(t, 2, page.Total)
		})
	})

	t.Run("five hundred ten equal titles page without overlap", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			req := models.BookSearchRequest{
				Query: "Рассказы", UserID: f.UserIDs["reader"], Language: "fx", Limit: 10,
			}
			page1 := search(f, req)
			assert.Equal(t, 510, page1.Total)
			assert.Len(t, page1.Books, 10)

			req.Offset = 10
			page2 := search(f, req)
			assert.Equal(t, 510, page2.Total)
			assert.Len(t, page2.Books, 10)
			assert.Equal(t, page1.QueryHash, page2.QueryHash)

			for _, id := range ids(page1) {
				assert.NotContains(t, ids(page2), id)
			}

			// 510 titles are exactly 51 pages of 10: the last one is full.
			req.Offset = 500
			page3 := search(f, req)
			assert.Len(t, page3.Books, 10)
		})
	})

	t.Run("repeating the same page query returns the same ID order", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			req := models.BookSearchRequest{
				Query: "Война и мир", UserID: f.UserIDs["reader"], Language: "fx", Limit: 50,
			}
			first := ids(search(f, req))
			require.NotEmpty(t, first)
			assert.Equal(t, first, ids(search(f, req)))
		})
	})
}

// TestPGSearchRepositoryExactBookID proves exact-ID navigation: the pinned
// book passes through the same visibility and scope filters, an empty textual
// query is allowed, and invisible or out-of-scope IDs fail closed. The pin
// bypasses the textual candidate gate entirely — it is a navigation request,
// not a text filter.
func TestPGSearchRepositoryExactBookID(t *testing.T) {
	search := func(f *searchFixture, req models.BookSearchRequest) models.BookSearchPage {
		t.Helper()
		repo := NewPGSearchRepository(f.tx)
		page, err := repo.SearchBooks(context.Background(), req)
		require.NoError(t, err)
		return page
	}

	t.Run("a visible exact ID returns only that book", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			page := search(f, models.BookSearchRequest{
				ExactBookID: f.BookIDs["exact"],
				UserID:      f.UserIDs["reader"], Language: "fx", Limit: 50,
			})
			require.Len(t, page.Books, 1)
			assert.Equal(t, f.BookIDs["exact"], page.Books[0].ID)
			assert.Equal(t, 1, page.Total)
		})
	})

	t.Run("a non-matching query does not override the pin", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			page := search(f, models.BookSearchRequest{
				Query: "Рассказы", ExactBookID: f.BookIDs["exact"],
				UserID: f.UserIDs["reader"], Language: "fx", Limit: 50,
			})
			require.Len(t, page.Books, 1)
			assert.Equal(t, f.BookIDs["exact"], page.Books[0].ID)
			assert.Equal(t, 1, page.Total)
		})
	})

	t.Run("invisible or out-of-scope exact IDs return zero", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			cases := map[string]models.BookSearchRequest{
				"hidden duplicate": {
					ExactBookID: f.BookIDs["hidden"],
					UserID:      f.UserIDs["reader"], Language: "fx", Limit: 50,
				},
				"unapproved": {
					ExactBookID: f.BookIDs["unapproved"],
					UserID:      f.UserIDs["reader"], Language: "fx", Limit: 50,
				},
				"wrong language": {
					ExactBookID: f.BookIDs["english"],
					UserID:      f.UserIDs["reader"], Language: "fx", Limit: 50,
				},
				"outside the author scope": {
					ExactBookID: f.BookIDs["exact"], AuthorID: f.AuthorIDs["bulgakov"],
					UserID: f.UserIDs["reader"], Language: "fx", Limit: 50,
				},
				"outside the favorites scope": {
					ExactBookID: f.BookIDs["exact"], Favorites: true,
					UserID: f.UserIDs["reader"], Language: "fx", Limit: 50,
				},
			}
			for name, req := range cases {
				page := search(f, req)
				assert.Empty(t, page.Books, name)
				assert.Equal(t, 0, page.Total, name)
			}
		})
	})
}

// TestPGSearchRepositoryHydratesOnlyThePage proves the returned page carries
// the same shape the legacy lists serve: authors, series with their junction
// ser_no, genres, the global favorite count and the caller's own favorite
// flag. Books outside the requested page are never loaded.
func TestPGSearchRepositoryHydratesOnlyThePage(t *testing.T) {
	search := func(f *searchFixture, req models.BookSearchRequest) models.BookSearchPage {
		t.Helper()
		repo := NewPGSearchRepository(f.tx)
		page, err := repo.SearchBooks(context.Background(), req)
		require.NoError(t, err)
		return page
	}
	byID := func(page models.BookSearchPage, id int64) *models.Book {
		t.Helper()
		for i := range page.Books {
			if page.Books[i].ID == id {
				return &page.Books[i]
			}
		}
		return nil
	}

	t.Run("page books carry relations and favorite state", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			page := search(f, models.BookSearchRequest{
				Query: "Война и мир", UserID: f.UserIDs["reader"], Language: "fx", Limit: 50,
			})
			require.Len(t, page.Books, 12)

			exact := byID(page, f.BookIDs["exact"])
			require.NotNil(t, exact)
			require.Len(t, exact.Authors, 1)
			assert.Equal(t, f.AuthorIDs["tolstoy"], exact.Authors[0].ID)
			assert.Zero(t, exact.FavoriteCount)
			assert.False(t, exact.Fav)

			seriesBook := byID(page, f.BookIDs["seriesBook"])
			require.NotNil(t, seriesBook)
			require.Len(t, seriesBook.Series, 1)
			assert.Equal(t, f.SeriesIDs["great"], seriesBook.Series[0].ID)
			assert.Equal(t, int64(1), seriesBook.Series[0].SerNo)

			genreBook := byID(page, f.BookIDs["genreBook"])
			require.NotNil(t, genreBook)
			require.Len(t, genreBook.Genres, 1)
			assert.Equal(t, f.GenreIDs["roman"], genreBook.Genres[0].ID)

			favBook := byID(page, f.BookIDs["favBook"])
			require.NotNil(t, favBook)
			assert.Equal(t, 1, favBook.FavoriteCount)
			assert.True(t, favBook.Fav)
		})
	})

	t.Run("Fav follows the caller, not the book", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			page := search(f, models.BookSearchRequest{
				Query: "Война и мир", Language: "fx", Limit: 50,
			})
			favBook := byID(page, f.BookIDs["favBook"])
			require.NotNil(t, favBook)
			assert.False(t, favBook.Fav)
			assert.Equal(t, 1, favBook.FavoriteCount)
		})
	})

	t.Run("books outside the page are never loaded", func(t *testing.T) {
		withSearchFixture(t, func(f *searchFixture) {
			page := search(f, models.BookSearchRequest{
				Query: "Война и мир", UserID: f.UserIDs["reader"], Language: "fx", Limit: 2,
			})
			require.Len(t, page.Books, 2)
			assert.Equal(t, 12, page.Total)
			for _, b := range page.Books {
				assert.Contains(t,
					[]int64{f.BookIDs["favBook"], f.BookIDs["exact"]}, b.ID)
			}
		})
	})
}

/*
 * A page is two queries: one ranks the ids, one loads the rows. Between them
 * the catalogue scanner can delete a book, and then the second query returns
 * fewer rows than the first asked for.
 *
 * The ordering step used to size its result by the number of rows loaded while
 * indexing it by the position of the id requested, so one vanished book made
 * the last position fall outside the slice and panicked the request. These are
 * ordinary slice arithmetic, so they need no database.
 */
func TestOrderByIDsSurvivesRowsThatVanished(t *testing.T) {
	books := []models.Book{{ID: 10}, {ID: 30}}

	t.Run("a book deleted between the two queries is skipped, not fatal", func(t *testing.T) {
		got := orderByIDs([]int64{10, 20, 30}, books)

		require.Len(t, got, 2)
		assert.Equal(t, []int64{10, 30}, []int64{got[0].ID, got[1].ID})
	})

	t.Run("rank order is the id order, not the order rows came back in", func(t *testing.T) {
		got := orderByIDs([]int64{30, 10}, books)

		require.Len(t, got, 2)
		assert.Equal(t, []int64{30, 10}, []int64{got[0].ID, got[1].ID})
	})

	// Nothing survives, and the caller gets an empty page rather than a slice
	// of zero-valued books that would render as untitled rows.
	t.Run("every book gone leaves nothing behind", func(t *testing.T) {
		assert.Empty(t, orderByIDs([]int64{10, 20}, nil))
	})

	t.Run("a row the page never asked for is ignored", func(t *testing.T) {
		got := orderByIDs([]int64{10}, []models.Book{{ID: 10}, {ID: 99}})

		require.Len(t, got, 1)
		assert.Equal(t, int64(10), got[0].ID)
	})
}
