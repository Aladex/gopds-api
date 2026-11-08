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

// feedFreshSQL is the visible books the read model has no row for yet, which
// the feed lists as cards of their own, by id. Every path that makes a book
// visible marks its card in the same transaction (the scan's insert,
// UpdateBook's approve and un-hide; a rebuild marks the books it moves), and
// the drain removes a mark only with the row it writes, so the queue holds
// every such book: no scan of the catalog, no guess from the highest id. A
// book a walk refused is not found here until the next walk takes it.
const feedFreshSQL = `
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
SELECT group_key, newest, mark, fresh, CASE WHEN mark <> '' OR fresh THEN newest ELSE 0 END AS shown_id
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

// freshMembershipSQL reads the cards the page's fresh books joined after the
// page was selected: a rebuild that committed in between.
const freshMembershipSQL = `
SELECT m.book_id AS shown_id, m.group_key::text AS group_key, g.newest_book_id AS newest, g.mark
FROM book_variant_member AS m
JOIN book_variant_group AS g ON g.group_key = m.group_key
WHERE m.book_id = ANY (?::bigint[])`

// foldFreshIntoCards keeps one identity per file on the page when a fresh
// book's rebuild committed after the page was selected. A fresh book now in
// a card that is also on the page gives way to that card, which takes the
// earlier of the two places; a fresh book now in a card not on the page
// becomes that card, in its place. Either way the card is read as it is now:
// its newest file and mark, the shown file following from them.
func foldFreshIntoCards(ctx context.Context, rows []cardRow) ([]cardRow, error) {
	var fresh []int64
	for i := range rows {
		if rows[i].Fresh {
			fresh = append(fresh, rows[i].ShownID)
		}
	}
	if len(fresh) == 0 {
		return rows, nil
	}
	var joined []cardRow
	if _, err := db.QueryContext(ctx, &joined, freshMembershipSQL, pg.Array(fresh)); err != nil {
		return nil, err
	}
	if len(joined) == 0 {
		return rows, nil
	}
	cardOf := make(map[int64]*cardRow, len(joined))
	for i := range joined {
		cardOf[joined[i].ShownID] = &joined[i]
	}
	now := func(card *cardRow) cardRow {
		row := cardRow{GroupKey: card.GroupKey, Newest: card.Newest, Mark: card.Mark}
		if row.Mark != "" {
			row.ShownID = row.Newest
		}
		return row
	}
	out := make([]cardRow, 0, len(rows))
	placed := map[string]int{}
	for i := range rows {
		row := rows[i]
		var card *cardRow
		if row.Fresh {
			card = cardOf[row.ShownID]
		}
		if card != nil {
			row = now(card)
		}
		if _, ok := placed[row.GroupKey]; ok {
			// The card is on the page already, at an earlier place, and keeps
			// it. A fresh book is newer than any card selected below it, so
			// the earlier place is the fresh book's, already read as it is
			// now; a card above a fresh book is newer still, and its newest
			// file and mark are not the fresh book's.
			continue
		}
		placed[row.GroupKey] = len(out)
		out = append(out, row)
	}
	return out, nil
}

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
	fresh := []any{lang, lang}
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
	rows, err := foldFreshIntoCards(ctx, rows)
	if err != nil {
		return nil, nil, 0, err
	}
	books, entries, err := cardEntries(ctx, rows, nextInFeed, false, userID)
	if err != nil {
		return nil, nil, 0, err
	}
	return books, entries, total, nil
}
