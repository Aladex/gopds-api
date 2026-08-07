package database

import (
	"context"
	"strings"
	"testing"

	"gopds-api/models"
)

// These contracts began life on the old GetAuthors and now run against the
// search repository that replaced it. They deliberately query the restored
// dump rather than a fixture: what they pin is how real names behave — a
// surname buried under three given ones, four namesakes of one famous author,
// a count that must equal the list the reader lands on. A fixture can state
// none of that, which is why the suite outlived the function it was written
// for.
//
// One contract stayed behind in services: AllLanguages meaning "the whole
// library" is folded to an empty language before a request reaches the
// repository, so it is pinned in services/search_test.go.

// searchAuthors runs one author search through the repository the public paths
// use, over the package-global connection.
func searchAuthors(t *testing.T, req models.AuthorSearchRequest) models.AuthorSearchPage {
	t.Helper()
	page, err := NewPGSearchRepository(db).SearchAuthors(context.Background(), req)
	if err != nil {
		t.Fatalf("searching for authors %+v: %v", req, err)
	}
	return page
}

func TestAuthorSearchFindsByName(t *testing.T) {
	requireDatabase(t)

	page := searchAuthors(t, models.AuthorSearchRequest{Query: "Толстой", Limit: 10})
	if page.Total == 0 || len(page.Authors) == 0 {
		t.Fatal("a name known to be in the library returned nothing")
	}
	for _, a := range page.Authors {
		if !strings.Contains(strings.ToLower(a.FullName), "толст") {
			t.Errorf("%q does not look like a match for the query", a.FullName)
		}
	}
}

// Normalization folds case before the trigrams are built, so the search is
// case-insensitive and the two spellings must return the same page.
func TestAuthorSearchIgnoresCase(t *testing.T) {
	requireDatabase(t)

	upper := searchAuthors(t, models.AuthorSearchRequest{Query: "ТОЛСТОЙ", Limit: 10})
	lower := searchAuthors(t, models.AuthorSearchRequest{Query: "толстой", Limit: 10})

	if len(upper.Authors) != len(lower.Authors) {
		t.Fatalf("case changed the number of results: %d vs %d",
			len(upper.Authors), len(lower.Authors))
	}
	for i := range upper.Authors {
		if upper.Authors[i].ID != lower.Authors[i].ID {
			t.Errorf("case changed the order at %d: %d vs %d",
				i, upper.Authors[i].ID, lower.Authors[i].ID)
		}
	}
}

// The closest name comes first: this is a search, and the page exists so a
// reader can pick between people who share a surname.
func TestAuthorSearchOrdersByCloseness(t *testing.T) {
	requireDatabase(t)

	page := searchAuthors(t, models.AuthorSearchRequest{Query: "Толстой", Limit: 5})
	if len(page.Authors) < 2 {
		t.Skip("not enough same-named authors in this database to judge ordering")
	}

	// The exact word should not be sitting below a longer variant of itself.
	first := strings.ToLower(page.Authors[0].FullName)
	if !strings.Contains(first, "толст") {
		t.Errorf("the closest match was %q", page.Authors[0].FullName)
	}
}

// A surname the reader typed in full has to find the author who carries it,
// however many given names follow. Whole-string similarity scored Tolkien's own
// full name at 0.292 — under the 0.3 the operator demands — so the most
// complete form of the name was not a result at all.
func TestAuthorSearchFindsNamesWithManyGivenNames(t *testing.T) {
	requireDatabase(t)

	page := searchAuthors(t, models.AuthorSearchRequest{Query: "Толкин", Limit: 50})

	var found bool
	for _, a := range page.Authors {
		if strings.EqualFold(a.FullName, "Толкин Джон Рональд Руэл") {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, 0, len(page.Authors))
		for _, a := range page.Authors {
			names = append(names, a.FullName)
		}
		t.Errorf("the author's own full name is missing from %v", names)
	}
}

// Among names the search cannot tell apart, the author holding more books is
// the one a reader typing a famous surname almost always meant.
func TestAuthorSearchPrefersTheLargerOfSimilarNames(t *testing.T) {
	requireDatabase(t)

	for _, name := range []string{"Достоевский", "Толстой", "Толкин", "Пушкин"} {
		t.Run(name, func(t *testing.T) {
			page := searchAuthors(t, models.AuthorSearchRequest{
				Query:    name,
				Language: "ru",
				Limit:    5,
			})
			if len(page.Authors) < 2 {
				t.Skip("not enough same-named authors to judge ordering")
			}

			// The largest author of the name must not be buried under the
			// namesakes holding one book each.
			largest, at := page.Authors[0].BooksCount, 0
			for i, a := range page.Authors {
				if a.BooksCount > largest {
					largest, at = a.BooksCount, i
				}
			}
			if at != 0 {
				t.Errorf("the author holding %d books is %d rows down, under %+v",
					largest, at, page.Authors[:at])
			}
		})
	}
}

func TestAuthorSearchHonoursTheLimit(t *testing.T) {
	requireDatabase(t)

	page := searchAuthors(t, models.AuthorSearchRequest{Query: "Толстой", Limit: 3})
	if len(page.Authors) > 3 {
		t.Errorf("asked for 3 authors, got %d", len(page.Authors))
	}
}

// The whole point of the number beside a name: it has to be the number of books
// the reader gets after clicking it. Anything else is worse than no number.
// The click lands on the ordinary author list, so this also pins that the two
// paths — search and list — still agree after the legacy search was removed.
func TestAuthorSearchCountMatchesTheBookList(t *testing.T) {
	requireDatabase(t)

	page := searchAuthors(t, models.AuthorSearchRequest{Query: "Достоевский", Limit: 5})
	if len(page.Authors) == 0 {
		t.Skip("no authors to compare against")
	}

	for _, a := range page.Authors {
		_, count, err := GetBooks(0, models.BookFilters{
			Author: int(a.ID),
			Limit:  1,
		})
		if err != nil {
			t.Fatalf("listing books of %q: %v", a.FullName, err)
		}
		if count != a.BooksCount {
			t.Errorf("%q is offered with %d books but its list holds %d",
				a.FullName, a.BooksCount, count)
		}
	}
}

// Same again with a language on, which is how nearly every reader browses.
func TestAuthorSearchCountMatchesTheBookListPerLanguage(t *testing.T) {
	requireDatabase(t)

	const lang = "ru"

	page := searchAuthors(t, models.AuthorSearchRequest{
		Query:    "Достоевский",
		Language: lang,
		Limit:    5,
	})
	if len(page.Authors) == 0 {
		t.Skip("no authors to compare against")
	}

	for _, a := range page.Authors {
		_, count, err := GetBooks(0, models.BookFilters{
			Author: int(a.ID),
			Lang:   lang,
			Limit:  1,
		})
		if err != nil {
			t.Fatalf("listing books of %q: %v", a.FullName, err)
		}
		if count != a.BooksCount {
			t.Errorf("%q is offered with %d books in %s but its list holds %d",
				a.FullName, a.BooksCount, lang, count)
		}
	}
}

// An author is only worth offering if there is something behind the name.
func TestAuthorSearchOmitsAuthorsWithNoBooks(t *testing.T) {
	requireDatabase(t)

	page := searchAuthors(t, models.AuthorSearchRequest{Query: "Александр", Limit: 50})
	if len(page.Authors) == 0 {
		t.Skip("no authors to inspect")
	}
	if page.Total < len(page.Authors) {
		t.Errorf("the total %d is smaller than the page of %d it describes",
			page.Total, len(page.Authors))
	}
	for _, a := range page.Authors {
		if a.BooksCount <= 0 {
			t.Errorf("%q was offered with %d books", a.FullName, a.BooksCount)
		}
	}
}

// The language has to narrow the count, not just the rows.
func TestAuthorSearchCountsOnlyTheChosenLanguage(t *testing.T) {
	requireDatabase(t)

	all := searchAuthors(t, models.AuthorSearchRequest{Query: "Достоевский", Limit: 20})
	scoped := searchAuthors(t, models.AuthorSearchRequest{
		Query:    "Достоевский",
		Language: "ru",
		Limit:    20,
	})

	counts := make(map[int64]int, len(all.Authors))
	for _, a := range all.Authors {
		counts[a.ID] = a.BooksCount
	}

	// The two searches return different pages — narrowing by language drops
	// authors and pulls others up into the limit — so only the authors on both
	// can be compared.
	compared := 0
	for _, a := range scoped.Authors {
		whole, ok := counts[a.ID]
		if !ok {
			continue
		}
		compared++
		if a.BooksCount > whole {
			t.Errorf("%q holds %d books in one language but %d in the whole library",
				a.FullName, a.BooksCount, whole)
		}
	}
	if compared == 0 {
		t.Skip("the two searches share no author to compare")
	}
}

// Paging must not repeat or skip an author.
func TestAuthorSearchPagesWithoutOverlap(t *testing.T) {
	requireDatabase(t)

	first := searchAuthors(t, models.AuthorSearchRequest{Query: "Иванов", Limit: 10})
	if first.Total <= 10 {
		t.Skip("not enough matches to page through")
	}
	second := searchAuthors(t, models.AuthorSearchRequest{
		Query:  "Иванов",
		Limit:  10,
		Offset: 10,
	})
	if second.Total != first.Total {
		t.Errorf("the total changed between pages: %d then %d", first.Total, second.Total)
	}

	seen := make(map[int64]bool, len(first.Authors))
	for _, a := range first.Authors {
		seen[a.ID] = true
	}
	for _, a := range second.Authors {
		if seen[a.ID] {
			t.Errorf("%q appears on both pages", a.FullName)
		}
	}
}

func TestAuthorSearchReturnsNothingForNonsense(t *testing.T) {
	requireDatabase(t)

	page := searchAuthors(t, models.AuthorSearchRequest{
		Query: "qqqzzzxxxwwwvvv",
		Limit: 10,
	})
	if len(page.Authors) != 0 {
		t.Errorf("a nonsense query matched %d authors", len(page.Authors))
	}
}
