package database

import (
	"context"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// PGBookEntries reads what a page of book entries shows beyond the rows the
// lists already have, over one database handle: each book's credited
// translators and the visible files of its card.
type PGBookEntries struct {
	db pg.DBI
}

// NewPGBookEntries wires the reader to a database handle.
func NewPGBookEntries(db pg.DBI) *PGBookEntries {
	return &PGBookEntries{db: db}
}

// bookEntryCountsSQL counts, for every book asked by a page, the visible
// files of its card over the live catalog: the card's rows and the book
// itself, which may have no row yet — the same files cardFilesSQL reads a
// card's counts from, one aggregate per asked book.
const bookEntryCountsSQL = `
WITH page AS (SELECT unnest(?::bigint[]) AS id),
keys AS (
    SELECT p.id, coalesce(m.group_key, md5('book:' || p.id)::uuid) AS g
    FROM page AS p LEFT JOIN book_variant_member AS m ON m.book_id = p.id),
files AS (
    SELECT k.id, m.book_id FROM keys AS k JOIN book_variant_member AS m ON m.group_key = k.g
    UNION
    SELECT k.id, k.id FROM keys AS k)
SELECT f.id AS book_id, count(DISTINCT f.book_id) AS files
FROM files AS f
JOIN opds_catalog_book AS b ON b.id = f.book_id AND b.approved AND NOT b.duplicate_hidden
GROUP BY f.id`

// BookEntryDetails returns each listed book's credited translators and the
// visible files of its card, in two queries for the whole page. Translators
// follow translatorNamesOf; a book without a current snapshot credits no
// one. The count is of the card's visible files, whoever of it was asked: a
// book no read-model row covers yet is a card of one, itself, and a file
// hidden since the page was read counts nowhere.
func (r *PGBookEntries) BookEntryDetails(ctx context.Context, bookIDs []int64) (map[int64]models.BookEntryDetail, error) {
	details := make(map[int64]models.BookEntryDetail, len(bookIDs))
	if len(bookIDs) == 0 {
		return details, nil
	}
	credits, err := translatorRowsOf(ctx, r.db, bookIDs)
	if err != nil {
		return nil, err
	}
	for start := 0; start < len(credits); {
		end := start
		for end < len(credits) && credits[end].BookID == credits[start].BookID {
			end++
		}
		detail := details[credits[start].BookID]
		detail.Translators, detail.TranslatorsMore = translatorNamesOf(credits[start:end])
		details[credits[start].BookID] = detail
		start = end
	}
	var counts []struct {
		BookID int64 `pg:"book_id"`
		Files  int   `pg:"files"`
	}
	if _, err = r.db.QueryContext(ctx, &counts, bookEntryCountsSQL, pg.Array(bookIDs)); err != nil {
		return nil, err
	}
	for _, row := range counts {
		detail := details[row.BookID]
		detail.VariantCount = row.Files
		details[row.BookID] = detail
	}
	return details, nil
}
