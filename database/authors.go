package database

import (
	"gopds-api/models"
)

// The trigram index on authors is built over lower(full_name):
//
//	opds_catalog_author_full_name_trgm_idx gin (lower(full_name) gin_trgm_ops)
//
// A predicate has to name the same expression to reach it. Written against the
// bare column, these searches matched no index and fell back to scanning all
// 176k authors on every keystroke — 238ms where the index answers in 12.
//
// Lowering changes nothing else: pg_trgm folds case when it builds trigrams, so
// `%` was already case-insensitive. The results are identical, verified across
// several names.
const authorNameExpr = "lower(full_name)"

// maxAuthorsPerPage bounds a page of search results. The catalog holds 176k
// authors and a common first name matches several thousand of them, so a
// request that names no limit must not be answered with all of them.
const maxAuthorsPerPage = 100

// authorSearchSQL finds authors by name and counts the books each one would
// actually open.
//
// The count has to agree with the list behind it or it misleads: every filter
// here is one the book list applies too — approved, not a hidden duplicate, and
// the reader's books language. An author whose books are all filtered away
// counts zero, and the inner join then drops them from the results, because a
// row that leads to an empty list is a dead end offered as a choice. Roughly
// one author in ten is such a row.
//
// The language predicate is written as a disjunction rather than assembled
// conditionally: go-pg substitutes parameters into the SQL before sending it,
// so an absent language leaves Postgres comparing one empty literal to another,
// which it folds to true at plan time. The clause costs nothing when unused.
//
// The total comes back as a window over the filtered set, in the same round
// trip, because pagination has to count what the page is drawn from — counting
// matched names instead would page over rows this query does not return.
//
// Candidates come from two operators because neither finds the other's names.
//
// `%` compares whole strings, so a name is punished for everything the reader
// did not type: "Tolkien John Ronald Reuel" scores 0.292 against "Tolkien",
// under the 0.3 the operator demands, and the author's own full name was
// therefore not a search result at all. `%>` asks the other question — how well
// the query matches some run of words within the name — and finds it at 1.0,
// while missing the loose fuzzy matches `%` is good at. Both reach the same GIN
// index, so the union is a BitmapOr of two index scans and costs 4ms.
//
// Ordering runs word distance, then size, then id.
//
// Whole-string distance ranks by length as much as by likeness: every "Tolkien
// <given names>" sorts by how much followed the surname, which put a Tolkien
// holding one book above the one holding 119, and buried Tolstoy and his 712
// under four namesakes holding two apiece. Word distance ignores the words the
// reader did not type, so all of them tie at zero and the question becomes
// which of these people the reader meant — answered by how many books each
// one holds. Names that merely resemble the query still sort behind, by how
// far off they are.
//
// Ties break on id last. Rows tied under an ORDER BY may come back in any
// order, and whole runs of names do tie, so the second page was repeating
// authors from the first and skipping others entirely.
const authorSearchSQL = `
WITH matched AS (
	SELECT a.id, a.full_name
	FROM opds_catalog_author AS a
	WHERE lower(a.full_name) % lower(?0)
		OR lower(a.full_name) %> lower(?0)
), counted AS (
	SELECT m.id, m.full_name, count(b.id) AS books_count
	FROM matched AS m
	JOIN opds_catalog_bauthor AS ba ON ba.author_id = m.id
	JOIN opds_catalog_book AS b ON b.id = ba.book_id
		AND b.approved
		AND NOT b.duplicate_hidden
		AND (?3 = '' OR b.lang = ?3)
	GROUP BY m.id, m.full_name
)
SELECT id, full_name, books_count, count(*) OVER () AS total
FROM counted
ORDER BY lower(?0) <<-> lower(full_name) ASC,
	books_count DESC,
	id ASC
LIMIT ?1 OFFSET ?2`

// authorRow is one result of authorSearchSQL. Total repeats on every row, being
// a window over the whole filtered set rather than a property of the author.
type authorRow struct {
	ID         int64
	FullName   string
	BooksCount int
	Total      int
}

// GetAuthors returns the authors matching a name, each with the number of books
// it holds, and the total number of matches for paging.
func GetAuthors(filters models.AuthorFilters) ([]models.Author, int, error) {
	limit := filters.Limit
	if limit <= 0 || limit > maxAuthorsPerPage {
		limit = maxAuthorsPerPage
	}

	// AllLanguages means the whole library, which is no filter at all — the
	// same reading the book list gives it.
	lang := filters.Lang
	if lang == AllLanguages {
		lang = ""
	}

	var rows []authorRow
	if _, err := db.Query(&rows, authorSearchSQL, filters.Author, limit, filters.Offset, lang); err != nil {
		return nil, 0, err
	}

	authors := make([]models.Author, 0, len(rows))
	for _, row := range rows {
		authors = append(authors, models.Author{
			ID:         row.ID,
			FullName:   row.FullName,
			BooksCount: row.BooksCount,
		})
	}

	total := 0
	if len(rows) > 0 {
		total = rows[0].Total
	}
	return authors, total, nil
}

// GetAuthor returns an object of author with full_name
func GetAuthor(filter models.AuthorRequest) (models.Author, error) {
	var author models.Author
	err := db.Model(&author).Where("id = ?", filter.ID).Select()
	if err != nil {
		return models.Author{}, err
	}
	return author, nil
}
