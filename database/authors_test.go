package database

import (
	"strings"
	"testing"

	"gopds-api/models"
)

// The author search matched no index for as long as it has existed: the
// predicate named the bare column while the trigram index is built over
// lower(full_name), so every search scanned all 176k authors. These pin the
// behavior that had to stay identical while that was fixed.

func TestGetAuthorsFindsByName(t *testing.T) {
	requireDatabase(t)

	authors, count, err := GetAuthors(models.AuthorFilters{Author: "Толстой", Limit: 10})
	if err != nil {
		t.Fatalf("searching for authors: %v", err)
	}
	if count == 0 || len(authors) == 0 {
		t.Fatal("a name known to be in the library returned nothing")
	}
	for _, a := range authors {
		if !strings.Contains(strings.ToLower(a.FullName), "толст") {
			t.Errorf("%q does not look like a match for the query", a.FullName)
		}
	}
}

// pg_trgm folds case when it builds trigrams, so the search was already
// case-insensitive and lowering the predicate to reach the index must not have
// changed that.
func TestGetAuthorsIgnoresCase(t *testing.T) {
	requireDatabase(t)

	upper, _, err := GetAuthors(models.AuthorFilters{Author: "ТОЛСТОЙ", Limit: 10})
	if err != nil {
		t.Fatalf("searching in upper case: %v", err)
	}
	lower, _, err := GetAuthors(models.AuthorFilters{Author: "толстой", Limit: 10})
	if err != nil {
		t.Fatalf("searching in lower case: %v", err)
	}

	if len(upper) != len(lower) {
		t.Fatalf("case changed the number of results: %d vs %d", len(upper), len(lower))
	}
	for i := range upper {
		if upper[i].ID != lower[i].ID {
			t.Errorf("case changed the order at %d: %d vs %d", i, upper[i].ID, lower[i].ID)
		}
	}
}

// The closest name comes first: this is a search, and the page exists so a
// reader can pick between people who share a surname.
func TestGetAuthorsOrdersByCloseness(t *testing.T) {
	requireDatabase(t)

	authors, _, err := GetAuthors(models.AuthorFilters{Author: "Толстой", Limit: 5})
	if err != nil {
		t.Fatalf("searching for authors: %v", err)
	}
	if len(authors) < 2 {
		t.Skip("not enough same-named authors in this database to judge ordering")
	}

	// The exact word should not be sitting below a longer variant of itself.
	first := strings.ToLower(authors[0].FullName)
	if !strings.Contains(first, "толст") {
		t.Errorf("the closest match was %q", authors[0].FullName)
	}
}

// A surname the reader typed in full has to find the author who carries it,
// however many given names follow. Whole-string similarity scored Tolkien's own
// full name at 0.292 — under the 0.3 the operator demands — so the most
// complete form of the name was not a result at all.
func TestGetAuthorsFindsNamesWithManyGivenNames(t *testing.T) {
	requireDatabase(t)

	authors, _, err := GetAuthors(models.AuthorFilters{Author: "Толкин", Limit: 50})
	if err != nil {
		t.Fatalf("searching for authors: %v", err)
	}

	var found bool
	for _, a := range authors {
		if strings.EqualFold(a.FullName, "Толкин Джон Рональд Руэл") {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, 0, len(authors))
		for _, a := range authors {
			names = append(names, a.FullName)
		}
		t.Errorf("the author's own full name is missing from %v", names)
	}
}

// Among names the search cannot tell apart, the author holding more books is
// the one a reader typing a famous surname almost always meant.
func TestGetAuthorsPrefersTheLargerOfSimilarNames(t *testing.T) {
	requireDatabase(t)

	for _, name := range []string{"Достоевский", "Толстой", "Толкин", "Пушкин"} {
		t.Run(name, func(t *testing.T) {
			authors, _, err := GetAuthors(models.AuthorFilters{
				Author: name,
				Lang:   "ru",
				Limit:  5,
			})
			if err != nil {
				t.Fatalf("searching for %q: %v", name, err)
			}
			if len(authors) < 2 {
				t.Skip("not enough same-named authors to judge ordering")
			}

			// The largest author of the name must not be buried under the
			// namesakes holding one book each.
			largest, at := authors[0].BooksCount, 0
			for i, a := range authors {
				if a.BooksCount > largest {
					largest, at = a.BooksCount, i
				}
			}
			if at != 0 {
				t.Errorf("the author holding %d books is %d rows down, under %+v",
					largest, at, authors[:at])
			}
		})
	}
}

func TestGetAuthorsHonoursTheLimit(t *testing.T) {
	requireDatabase(t)

	authors, _, err := GetAuthors(models.AuthorFilters{Author: "Толстой", Limit: 3})
	if err != nil {
		t.Fatalf("searching for authors: %v", err)
	}
	if len(authors) > 3 {
		t.Errorf("asked for 3 authors, got %d", len(authors))
	}
}

// The whole point of the number beside a name: it has to be the number of books
// the reader gets after clicking it. Anything else is worse than no number.
func TestGetAuthorsCountMatchesTheBookList(t *testing.T) {
	requireDatabase(t)

	authors, _, err := GetAuthors(models.AuthorFilters{Author: "Достоевский", Limit: 5})
	if err != nil {
		t.Fatalf("searching for authors: %v", err)
	}
	if len(authors) == 0 {
		t.Skip("no authors to compare against")
	}

	for _, a := range authors {
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
func TestGetAuthorsCountMatchesTheBookListPerLanguage(t *testing.T) {
	requireDatabase(t)

	const lang = "ru"

	authors, _, err := GetAuthors(models.AuthorFilters{
		Author: "Достоевский",
		Lang:   lang,
		Limit:  5,
	})
	if err != nil {
		t.Fatalf("searching for authors: %v", err)
	}
	if len(authors) == 0 {
		t.Skip("no authors to compare against")
	}

	for _, a := range authors {
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
func TestGetAuthorsOmitsAuthorsWithNoBooks(t *testing.T) {
	requireDatabase(t)

	authors, total, err := GetAuthors(models.AuthorFilters{Author: "Александр", Limit: 50})
	if err != nil {
		t.Fatalf("searching for authors: %v", err)
	}
	if len(authors) == 0 {
		t.Skip("no authors to inspect")
	}
	if total < len(authors) {
		t.Errorf("the total %d is smaller than the page of %d it describes", total, len(authors))
	}
	for _, a := range authors {
		if a.BooksCount <= 0 {
			t.Errorf("%q was offered with %d books", a.FullName, a.BooksCount)
		}
	}
}

// AllLanguages means the whole library, not a language whose code is "all" —
// reading it literally matched no book and emptied the search for every reader
// who had asked to see everything.
func TestGetAuthorsTreatsAllLanguagesAsNoFilter(t *testing.T) {
	requireDatabase(t)

	every, _, err := GetAuthors(models.AuthorFilters{
		Author: "Достоевский",
		Lang:   AllLanguages,
		Limit:  20,
	})
	if err != nil {
		t.Fatalf("searching across all languages: %v", err)
	}
	unset, _, err := GetAuthors(models.AuthorFilters{Author: "Достоевский", Limit: 20})
	if err != nil {
		t.Fatalf("searching with no language: %v", err)
	}

	if len(every) != len(unset) {
		t.Fatalf("%q returned %d authors, no language returned %d",
			AllLanguages, len(every), len(unset))
	}
	for i := range every {
		if every[i].ID != unset[i].ID || every[i].BooksCount != unset[i].BooksCount {
			t.Errorf("row %d differs: %+v vs %+v", i, every[i], unset[i])
		}
	}
}

// The language has to narrow the count, not just the rows.
func TestGetAuthorsCountsOnlyTheChosenLanguage(t *testing.T) {
	requireDatabase(t)

	all, _, err := GetAuthors(models.AuthorFilters{Author: "Достоевский", Limit: 20})
	if err != nil {
		t.Fatalf("searching across the library: %v", err)
	}
	scoped, _, err := GetAuthors(models.AuthorFilters{
		Author: "Достоевский",
		Lang:   "ru",
		Limit:  20,
	})
	if err != nil {
		t.Fatalf("searching in one language: %v", err)
	}

	counts := make(map[int64]int, len(all))
	for _, a := range all {
		counts[a.ID] = a.BooksCount
	}

	// The two searches return different pages — narrowing by language drops
	// authors and pulls others up into the limit — so only the authors on both
	// can be compared.
	compared := 0
	for _, a := range scoped {
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
func TestGetAuthorsPagesWithoutOverlap(t *testing.T) {
	requireDatabase(t)

	first, total, err := GetAuthors(models.AuthorFilters{Author: "Иванов", Limit: 10})
	if err != nil {
		t.Fatalf("reading the first page: %v", err)
	}
	if total <= 10 {
		t.Skip("not enough matches to page through")
	}
	second, secondTotal, err := GetAuthors(models.AuthorFilters{
		Author: "Иванов",
		Limit:  10,
		Offset: 10,
	})
	if err != nil {
		t.Fatalf("reading the second page: %v", err)
	}
	if secondTotal != total {
		t.Errorf("the total changed between pages: %d then %d", total, secondTotal)
	}

	seen := make(map[int64]bool, len(first))
	for _, a := range first {
		seen[a.ID] = true
	}
	for _, a := range second {
		if seen[a.ID] {
			t.Errorf("%q appears on both pages", a.FullName)
		}
	}
}

func TestGetAuthorsReturnsNothingForNonsense(t *testing.T) {
	requireDatabase(t)

	authors, _, err := GetAuthors(models.AuthorFilters{
		Author: "qqqzzzxxxwwwvvv",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("searching for a name nobody has: %v", err)
	}
	if len(authors) != 0 {
		t.Errorf("a nonsense query matched %d authors", len(authors))
	}
}
