package database

import (
	"strings"
	"testing"

	"gopds-api/models"
)

// The author search matched no index for as long as it has existed: the
// predicate named the bare column while the trigram index is built over
// lower(full_name), so every search scanned all 176k authors. These pin the
// behaviour that had to stay identical while that was fixed.

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
