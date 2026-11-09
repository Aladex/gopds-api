package database

import (
	"context"
	"strings"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// PGBookSourceRepository reads what the public book card shows from the
// author metadata source layer: the publisher, ISBN list and translators of
// each book's current metadata snapshot, and nothing else of it.
type PGBookSourceRepository struct {
	db pg.DBI
}

// NewPGBookSourceRepository wires the repository to a database handle.
func NewPGBookSourceRepository(db pg.DBI) *PGBookSourceRepository {
	return &PGBookSourceRepository{db: db}
}

// BookSourceDetails returns the publisher, ISBN list and translators of every
// listed book that has a current snapshot, in two queries for the whole page.
// A book without a current snapshot has no entry. Values are trimmed and
// otherwise returned as stored: a blank publisher is none, blank ISBN entries
// are dropped, and an empty ISBN list is empty rather than nil. Translators
// follow translatorNamesOf; a book with none has a nil list.
func (r *PGBookSourceRepository) BookSourceDetails(ctx context.Context, bookIDs []int64) (map[int64]models.BookSourceDetail, error) {
	details := make(map[int64]models.BookSourceDetail, len(bookIDs))
	if len(bookIDs) == 0 {
		return details, nil
	}
	var rows []struct {
		BookID    int64    `pg:"book_id"`
		Publisher *string  `pg:"source_publisher"`
		ISBNs     []string `pg:"source_isbns,array"`
	}
	if _, err := r.db.QueryContext(ctx, &rows, `
		SELECT book_id, source_publisher, source_isbns
		FROM book_metadata_snapshot
		WHERE is_current AND book_id IN (?)`, pg.In(bookIDs)); err != nil {
		return nil, err
	}
	for _, row := range rows {
		detail := models.BookSourceDetail{ISBN: make([]string, 0, len(row.ISBNs))}
		if row.Publisher != nil {
			if publisher := strings.TrimSpace(*row.Publisher); publisher != "" {
				detail.Publisher = &publisher
			}
		}
		for _, isbn := range row.ISBNs {
			if isbn = strings.TrimSpace(isbn); isbn != "" {
				detail.ISBN = append(detail.ISBN, isbn)
			}
		}
		details[row.BookID] = detail
	}
	if err := r.addTranslators(ctx, bookIDs, details); err != nil {
		return nil, err
	}
	return details, nil
}

// maxCardTranslators is how many translator names the card names before it
// says «и др.».
const maxCardTranslators = 2

// translatorRow is one translator credit of a current snapshot.
type translatorRow struct {
	BookID  int64   `pg:"book_id"`
	Display string  `pg:"source_display_name"`
	First   *string `pg:"source_first_name"`
	Middle  *string `pg:"source_middle_name"`
	Last    *string `pg:"source_last_name"`
}

// addTranslators fills in the translators of the books that have an entry,
// that is a current snapshot.
func (r *PGBookSourceRepository) addTranslators(ctx context.Context, bookIDs []int64, details map[int64]models.BookSourceDetail) error {
	rows, err := translatorRowsOf(ctx, r.db, bookIDs)
	if err != nil {
		return err
	}
	for start := 0; start < len(rows); {
		end := start
		for end < len(rows) && rows[end].BookID == rows[start].BookID {
			end++
		}
		if detail, ok := details[rows[start].BookID]; ok {
			detail.Translators, detail.TranslatorsMore = translatorNamesOf(rows[start:end])
			details[rows[start].BookID] = detail
		}
		start = end
	}
	return nil
}

// translatorRowsOf reads the translator credits of the listed books' current
// snapshots in credit order, in one query.
func translatorRowsOf(ctx context.Context, db pg.DBI, bookIDs []int64) ([]translatorRow, error) {
	var rows []translatorRow
	_, err := db.QueryContext(ctx, &rows, `
		SELECT s.book_id, c.source_display_name, c.source_first_name, c.source_middle_name, c.source_last_name
		FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current AND s.book_id IN (?)
		WHERE c.role = 'translator'
		ORDER BY s.book_id, c.position`, pg.In(bookIDs))
	return rows, err
}

// translatorNamesOf names a book's translators from its credits in credit
// order: the display name, or else the first, middle and last names joined.
// Empty names are dropped, and so is a name already given (compared with
// whitespace collapsed and case folded). It returns at most
// maxCardTranslators names, and more when another name remains; no name at
// all is a nil list.
func translatorNamesOf(credits []translatorRow) (names []string, more bool) {
	seen := make(map[string]bool, len(credits))
	for i := range credits {
		name := strings.Join(strings.Fields(credits[i].Display), " ")
		if name == "" {
			name = strings.Join(strings.Fields(strings.Join([]string{
				deref(credits[i].First), deref(credits[i].Middle), deref(credits[i].Last)}, " ")), " ")
		}
		key := strings.ToLower(name)
		if name == "" || seen[key] {
			continue
		}
		seen[key] = true
		if len(names) == maxCardTranslators {
			return names, true
		}
		names = append(names, name)
	}
	return names, false
}
