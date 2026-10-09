package database

import (
	"context"
	"strings"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// PGBookSourceRepository reads what the public book card shows from the
// author metadata source layer: the publisher and ISBN list of each book's
// current metadata snapshot, and nothing else of it.
type PGBookSourceRepository struct {
	db pg.DBI
}

// NewPGBookSourceRepository wires the repository to a database handle.
func NewPGBookSourceRepository(db pg.DBI) *PGBookSourceRepository {
	return &PGBookSourceRepository{db: db}
}

// BookSourceDetails returns the publisher and ISBN list of every listed book
// that has a current snapshot, in one query for the whole page. A book without
// a current snapshot has no entry. Values are trimmed and otherwise returned
// as stored: a blank publisher is none, blank ISBN entries are dropped, and an
// empty list is empty rather than nil.
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
	return details, nil
}
