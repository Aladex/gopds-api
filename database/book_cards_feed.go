package database

import (
	"context"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// The feed as cards: every card by its newest file, the newest first, read
// off book_variant_group's (lang, newest_book_id) indexes instead of a scope
// of every book. A card whose newest file came on a later day than all its
// others (Build's mark) opens on that file and says «новая редакция» or
// «новый вариант»; any other opens on its best visible file. Visibility is
// read live, through the same replacement chain as the scoped lists.

// isFeed says the list is the feed: no scope, no search, the default order.
//
//nolint:gocritic // reads one filter set; the list path passes it by value throughout
func isFeed(f models.BookFilters) bool {
	return f.Author == 0 && f.AuthorEntity == uuid.Nil && f.Series == 0 && f.Genre == 0 && f.Collection == 0 &&
		f.CuratedCollection == 0 && !f.Fav && !f.UsersFavorites && f.Sort == "" && f.Title == ""
}

// feedFreshSQL is the visible books the read model has no row for yet that
// the feed lists as cards of their own, by id. Fresh uploads are newer than
// every row's book (the first parameter, read beforehand, so the planner sees
// a constant and takes a short index range); a book whose row went (hidden,
// then shown again) is marked dirty. Looking only there keeps the feed off a
// scan of the catalog. A book a walk refused is not found here until the next
// walk takes it.
const feedFreshSQL = `
SELECT b.id
FROM opds_catalog_book AS b
WHERE b.id > ?::bigint AND b.approved AND NOT b.duplicate_hidden AND (?::text = '' OR b.lang = ?::text)
    AND NOT EXISTS (SELECT 1 FROM book_variant_member AS m WHERE m.book_id = b.id)
UNION
SELECT b.id
FROM book_variant_dirty AS d
JOIN opds_catalog_book AS b ON b.id = d.book_id
WHERE b.approved AND NOT b.duplicate_hidden AND (?::text = '' OR b.lang = ?::text)
    AND NOT EXISTS (SELECT 1 FROM book_variant_member AS m WHERE m.book_id = b.id)`

// feedPageSQL pages the feed: the cards and the fresh books, merged by their
// newest file. Each side is cut to the page's end first, so both are read off
// their indexes. A card without a mark leaves shown_id 0: its best visible
// file is found by the replacement chain, as for a file gone.
const feedPageSQL = `
SELECT group_key, newest, mark, CASE WHEN mark <> '' OR fresh THEN newest ELSE 0 END AS shown_id
FROM (
    (SELECT g.group_key::text AS group_key, g.newest_book_id AS newest, g.mark, false AS fresh
     FROM book_variant_group AS g
     WHERE (?::text = '' OR g.lang = ?::text)
     ORDER BY g.newest_book_id DESC
     LIMIT ?)
    UNION ALL
    (SELECT md5('book:' || f.id)::uuid::text, f.id, '', true
     FROM (` + feedFreshSQL + `) AS f
     ORDER BY f.id DESC
     LIMIT ?)
) AS page
ORDER BY newest DESC
LIMIT ? OFFSET ?`

// feedCountSQL counts the feed's cards: the read model's and the fresh books.
const feedCountSQL = `
SELECT (SELECT count(*) FROM book_variant_group AS g WHERE (?::text = '' OR g.lang = ?::text))
    + (SELECT count(*) FROM (` + feedFreshSQL + `) AS f)`

// nextInFeed is nextFiles for the feed: the card's best-ranked visible file.
func nextInFeed(ctx context.Context, keys []string, gone []int64) ([]cardRow, error) {
	var next []cardRow
	_, err := db.QueryContext(ctx, &next, `
		SELECT DISTINCT ON (m.group_key) m.book_id AS shown_id, m.group_key::text AS group_key,
			m.variant_rank AS shown_rank
		FROM book_variant_member AS m
		JOIN opds_catalog_book AS b ON b.id = m.book_id AND b.approved AND NOT b.duplicate_hidden
		WHERE m.group_key = ANY (?::uuid[]) AND m.book_id <> ALL (?::bigint[])
		ORDER BY m.group_key, m.variant_rank, m.book_id`, pg.Array(keys), pg.Array(gone))
	return next, err
}

// getFeedCards is GetBookCards for the feed.
//
//nolint:gocritic // reads one filter set; the list path passes it by value throughout
func getFeedCards(ctx context.Context, userID int64, filters models.BookFilters) ([]models.Book, []CardEntry, int, error) {
	lang := filters.Lang
	if lang == AllLanguages {
		lang = ""
	}
	end := filters.Limit + filters.Offset
	var newest int64
	if _, err := db.QueryOneContext(ctx, pg.Scan(&newest),
		`SELECT coalesce(max(book_id), 0) FROM book_variant_member`); err != nil {
		return nil, nil, 0, err
	}
	fresh := []any{newest, lang, lang, lang, lang}
	var rows []cardRow
	if _, err := db.QueryContext(ctx, &rows, feedPageSQL,
		append(append([]any{lang, lang, end}, fresh...), end, filters.Limit, filters.Offset)...); err != nil {
		return nil, nil, 0, err
	}
	var total int
	if _, err := db.QueryOneContext(ctx, pg.Scan(&total), feedCountSQL,
		append([]any{lang, lang}, fresh...)...); err != nil {
		return nil, nil, 0, err
	}
	if len(rows) == 0 {
		return []models.Book{}, []CardEntry{}, total, nil
	}
	if afterCardsSelected != nil {
		afterCardsSelected()
	}
	books, entries, err := cardEntries(ctx, rows, nextInFeed, false, userID)
	if err != nil {
		return nil, nil, 0, err
	}
	return books, entries, total, nil
}
