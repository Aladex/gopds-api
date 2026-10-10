package database

import (
	"context"
	"slices"
	"strings"
	"unicode"

	"gopds-api/internal/parser"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// legacyAuthor is a catalog author linked to a book.
type legacyAuthor struct {
	ID   int64
	Name string
}

// creditName is one author credit of a book's current snapshot as it would
// be shown: the selected normalized name, or else the name as the file
// spells it. Selected tells the two apart.
type creditName struct {
	Name     string
	Selected bool
	// The read model's sort key comes from these: the credit, the selected
	// result's sort name, and the name parts as the file gives them.
	ID                  int64
	SortName            string
	First, Middle, Last string
}

// bookAuthorSources is everything a book's author line is decided from: its
// legacy authors in link order and its author credits in file order.
type bookAuthorSources struct {
	legacy  []legacyAuthor
	credits []creditName
}

// bookAuthorRowsSQL reads both lists for a page of books at once: the legacy
// links (one row per distinct author, in the order they were linked) and the
// author credits of each book's current snapshot in file order, each with the
// name it shows. A selection whose result carries no display name falls back
// to the file's spelling like any unselected credit. A credit row also carries
// what the read model sorts it by: its ID, the selected result's sort name
// and the file's name parts.
const bookAuthorRowsSQL = `
	SELECT book_id, legacy_id, name, selected, credit_id, sort_name, first_name, middle_name, last_name FROM (
	SELECT ba.book_id, a.id AS legacy_id, a.full_name AS name, false AS selected, min(ba.id) AS ord,
	       NULL::bigint AS credit_id, NULL::text AS sort_name,
	       NULL::text AS first_name, NULL::text AS middle_name, NULL::text AS last_name
	FROM opds_catalog_bauthor ba
	JOIN opds_catalog_author a ON a.id = ba.author_id
	WHERE ba.book_id IN (?0)
	GROUP BY ba.book_id, a.id, a.full_name
	UNION ALL
	SELECT s.book_id, NULL, CASE WHEN chosen.display_name IS NOT NULL THEN chosen.display_name
	                             ELSE c.source_display_name END,
	       chosen.display_name IS NOT NULL, c.position,
	       c.id, CASE WHEN chosen.display_name IS NOT NULL THEN chosen.sort_name END,
	       c.source_first_name, c.source_middle_name, c.source_last_name
	FROM book_metadata_snapshot s
	JOIN book_contributor_credit c ON c.snapshot_id = s.id AND c.role = 'author'
	LEFT JOIN LATERAL (
		SELECT nullif(btrim(r.display_name), '') AS display_name, r.sort_name
		FROM book_contributor_credit_selection sel
		JOIN contributor_normalization_result r ON r.id = sel.result_id
		WHERE sel.credit_id = c.id AND sel.state = 'selected') chosen ON true
	WHERE s.is_current AND s.book_id IN (?0)) author_rows
	ORDER BY book_id, legacy_id IS NULL, ord`

// loadBookAuthorSources gathers the legacy authors and author credits of the
// books in one query. Every requested book has an entry, empty when it has
// neither.
func loadBookAuthorSources(ctx context.Context, db pg.DBI, bookIDs []int64) (map[int64]*bookAuthorSources, error) {
	sources := make(map[int64]*bookAuthorSources, len(bookIDs))
	for _, id := range bookIDs {
		sources[id] = &bookAuthorSources{}
	}
	if len(bookIDs) == 0 {
		return sources, nil
	}
	var rows []struct {
		BookID     int64  `pg:"book_id"`
		LegacyID   *int64 `pg:"legacy_id"`
		Name       string `pg:"name"`
		Selected   bool   `pg:"selected"`
		CreditID   int64  `pg:"credit_id"`
		SortName   string `pg:"sort_name"`
		FirstName  string `pg:"first_name"`
		MiddleName string `pg:"middle_name"`
		LastName   string `pg:"last_name"`
	}
	if _, err := db.QueryContext(ctx, &rows, bookAuthorRowsSQL, pg.In(bookIDs)); err != nil {
		return nil, err
	}
	for _, row := range rows {
		s := sources[row.BookID]
		if row.LegacyID != nil {
			s.legacy = append(s.legacy, legacyAuthor{ID: *row.LegacyID, Name: row.Name})
		} else {
			s.credits = append(s.credits, creditName{
				Name: row.Name, Selected: row.Selected, ID: row.CreditID, SortName: row.SortName,
				First: row.FirstName, Middle: row.MiddleName, Last: row.LastName,
			})
		}
	}
	return sources, nil
}

// BookAuthorDisplay returns the author line of every listed book, in one
// query for the whole page; every requested book has an entry.
//
// A book with author credits in its current snapshot shows them in file
// order: the selected normalized name, or else the name as the file spells
// it, capitals tamed by the scan's own rule. Each name links to the legacy
// author of the book it names — every word of the legacy name is a word of
// the credit — and to nothing when none does. A book keeps its legacy authors
// when it has no credits, and when its legacy authors carry a word none of the
// credits has: the catalog was edited by hand, and an edit must not vanish
// from the card. Legacy names made only of the file's words are the legacy
// scan's own misreading (it pairs first and last names by index), and give
// way to the file.
func (r *PGBookSourceRepository) BookAuthorDisplay(ctx context.Context, bookIDs []int64) (map[int64]models.BookAuthorDisplay, error) {
	sources, err := loadBookAuthorSources(ctx, r.db, bookIDs)
	if err != nil {
		return nil, err
	}
	lines := make(map[int64]models.BookAuthorDisplay, len(sources))
	for id, s := range sources {
		lines[id] = resolveAuthorDisplay(s)
	}
	return lines, nil
}

// resolveAuthorDisplay decides a book's author line from its two lists.
func resolveAuthorDisplay(s *bookAuthorSources) models.BookAuthorDisplay {
	legacy := distinctLegacyAuthors(s.legacy)
	if len(s.credits) == 0 {
		return legacyDisplay(legacy, models.AuthorDisplayNoCredits)
	}
	if !legacyWordsInCredits(legacy, s.credits) {
		return legacyDisplay(legacy, models.AuthorDisplayLegacyUnmatched)
	}
	links := matchLegacyAuthors(legacy, s.credits)
	if creditNamesSeveral(legacy, s.credits, links) {
		return legacyDisplay(legacy, models.AuthorDisplayGluedCredit)
	}
	authors := make([]models.AuthorDisplay, len(s.credits))
	for i, c := range s.credits {
		name := c.Name
		if !c.Selected {
			name = displayCase(name)
		}
		authors[i] = models.AuthorDisplay{Name: name, LegacyAuthorID: links[i]}
	}
	return models.BookAuthorDisplay{Source: models.AuthorDisplayLayer, Authors: authors}
}

func legacyDisplay(legacy []legacyAuthor, why models.AuthorDisplayFallback) models.BookAuthorDisplay {
	authors := make([]models.AuthorDisplay, len(legacy))
	for i, a := range legacy {
		id := a.ID
		authors[i] = models.AuthorDisplay{Name: a.Name, LegacyAuthorID: &id}
	}
	return models.BookAuthorDisplay{Source: models.AuthorDisplayLegacy, Fallback: why, Authors: authors}
}

// distinctLegacyAuthors drops a second link of the same author to the book.
func distinctLegacyAuthors(legacy []legacyAuthor) []legacyAuthor {
	out := make([]legacyAuthor, 0, len(legacy))
	for _, a := range legacy {
		if !slices.ContainsFunc(out, func(b legacyAuthor) bool { return b.ID == a.ID }) {
			out = append(out, a)
		}
	}
	return out
}

// namesAnAuthor tells a legacy author from the placeholder a scan links to a
// book it found no author for, and from a name without a word: neither is an
// author the file could name.
func namesAnAuthor(a legacyAuthor) bool {
	return a.Name != models.UnknownAuthorName && len(nameWords(a.Name)) > 0
}

// legacyWordsInCredits reports whether every word of the book's legacy
// authors is a word of one of its credits.
func legacyWordsInCredits(legacy []legacyAuthor, credits []creditName) bool {
	var legacyNames, creditNames []string
	for _, a := range legacy {
		if namesAnAuthor(a) {
			legacyNames = append(legacyNames, a.Name)
		}
	}
	for _, c := range credits {
		creditNames = append(creditNames, c.Name)
	}
	return wordsWithin(nameWords(strings.Join(legacyNames, " ")), nameWords(strings.Join(creditNames, " ")))
}

// creditNamesSeveral reports whether a legacy author the pairing left out
// is named inside a credit paired with someone else — two people, neither's
// words all the other's. A file that writes a whole cast into one author
// field reads so; a longer and a shorter catalog name of one person does not.
func creditNamesSeveral(legacy []legacyAuthor, credits []creditName, links []*int64) bool {
	paired := make(map[int64]bool, len(links))
	for _, id := range links {
		if id != nil {
			paired[*id] = true
		}
	}
	for _, left := range legacy {
		if paired[left.ID] || !namesAnAuthor(left) {
			continue
		}
		leftWords := nameWords(left.Name)
		for c, id := range links {
			if id == nil || !wordsWithin(leftWords, nameWords(credits[c].Name)) {
				continue
			}
			ownerWords := nameWords(legacyName(legacy, *id))
			if !wordsWithin(leftWords, ownerWords) && !wordsWithin(ownerWords, leftWords) {
				return true
			}
		}
	}
	return false
}

// legacyName is the name of the legacy author with the given ID.
func legacyName(legacy []legacyAuthor, id int64) string {
	for _, a := range legacy {
		if a.ID == id {
			return a.Name
		}
	}
	return ""
}

// matchLegacyAuthors pairs credits with the legacy authors they name, one to
// one, matching as many legacy authors as any pairing can. links[i] is the
// legacy author of credit i, nil when it has none.
func matchLegacyAuthors(legacy []legacyAuthor, credits []creditName) []*int64 {
	creditWords := make([][]string, len(credits))
	for i, c := range credits {
		creditWords[i] = nameWords(c.Name)
	}
	// candidates[l] are the credits legacy author l may be, by index.
	candidates := make([][]int, len(legacy))
	for l, a := range legacy {
		if !namesAnAuthor(a) {
			continue
		}
		words := nameWords(a.Name)
		for c := range credits {
			if wordsWithin(words, creditWords[c]) {
				candidates[l] = append(candidates[l], c)
			}
		}
	}

	// Augmenting paths over a handful of names: owner[c] is the legacy
	// author credit c is paired with, -1 when none.
	owner := make([]int, len(credits))
	for c := range owner {
		owner[c] = -1
	}
	var pair func(l int, seen []bool) bool
	pair = func(l int, seen []bool) bool {
		for _, c := range candidates[l] {
			if seen[c] {
				continue
			}
			seen[c] = true
			if owner[c] < 0 || pair(owner[c], seen) {
				owner[c] = l
				return true
			}
		}
		return false
	}
	for l := range legacy {
		pair(l, make([]bool, len(credits)))
	}

	links := make([]*int64, len(credits))
	for c, l := range owner {
		if l >= 0 {
			id := legacy[l].ID
			links[c] = &id
		}
	}
	return links
}

// nameWords splits a name into its distinct words, compared without case,
// with ё read as е, and with anything but letters, digits and their marks as
// a separator; sorted.
func nameWords(name string) []string {
	name = strings.ReplaceAll(strings.ToLower(name), "ё", "е")
	words := strings.FieldsFunc(name, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r)
	})
	slices.Sort(words)
	return slices.Compact(words)
}

// wordsWithin reports whether every word of sub is a word of set; both are
// sorted and distinct.
func wordsWithin(sub, set []string) bool {
	for _, w := range sub {
		if _, found := slices.BinarySearch(set, w); !found {
			return false
		}
	}
	return true
}

// displayCase applies the scan's case rule to each word of a name taken as
// the file spells it, as the scan applies it to each name part.
func displayCase(name string) string {
	words := strings.Fields(name)
	for i, w := range words {
		words[i] = parser.NormalizeNameCase(w)
	}
	return strings.Join(words, " ")
}
