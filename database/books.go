package database

import (
	"context"
	"errors"
	"strings"

	"gopds-api/logging"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/go-pg/pg/v10/orm"
)

// AllLanguages is the books language of a reader who wants the whole library.
//
// It is a stored value rather than an empty one because empty already means
// something else: a reader who has never been asked which language to show. If
// wanting everything cleared the field, they would be asked again every visit.
// No book carries it as a language, so it cannot collide with a real code.
const AllLanguages = "all"

// ErrTextSearchUnsupported is returned when a list request carries search
// text. Text search lives in the search service, which ranks in PostgreSQL and
// reports an exact total; this path only lists a scope by date or position.
// Ignoring the needle instead would answer a search with the whole catalog —
// wrong, and silently so.
var ErrTextSearchUnsupported = errors.New("database: the book list performs no text search, use the search service")

// GetBooks lists books for a scope that carries no search text: the new-books
// feed, an author, a series, a genre, a language, the caller's favorites, a
// collection or the moderation queue.
func GetBooks(userID int64, filters models.BookFilters) ([]models.Book, int, error) {
	if strings.TrimSpace(filters.Title) != "" {
		return nil, 0, ErrTextSearchUnsupported
	}

	books := []models.Book{}
	var userFavs []int64

	err := db.Model(&models.UserToBook{}).Where("user_id = ?", userID).Select(&userFavs)
	if err != nil {
		logging.Error(err)
		return nil, 0, err
	}

	if filters.Limit > 100 || filters.Limit == 0 {
		filters.Limit = 100
	}

	query := db.Model(&books).
		Relation("Authors").
		Relation("Users").
		Relation("Series").
		Relation("Genres").
		ColumnExpr("book.*, (SELECT COUNT(*) FROM favorite_books WHERE book_id = book.id) AS favorite_count")

	query = applyListFilters(query, filters, userID)

	// Ordering follows the scope that was asked for.
	if filters.Fav {
		var booksIds []models.UserToBook
		err := db.Model(&booksIds).
			Column("book_id").
			Where("user_id = ?", userID).
			Order("id DESC").
			Select(&booksIds)
		if err == nil && len(booksIds) > 0 {
			var bIds []int64
			for _, bid := range booksIds {
				bIds = append(bIds, bid.BookID)
			}
			query = query.WhereIn("book.id IN (?)", bIds)
			query = query.OrderExpr(`
				(SELECT row_number 
				 FROM (SELECT book_id, ROW_NUMBER() OVER (ORDER BY id DESC) as row_number 
				       FROM favorite_books 
				       WHERE user_id = ?) favs 
				 WHERE favs.book_id = book.id) ASC`, userID)
		}
	} else if filters.UsersFavorites {
		query = query.Join("JOIN favorite_books fb ON fb.book_id = book.id").
			Group("book.id").
			OrderExpr("favorite_count DESC, book.id DESC")
	} else if filters.Collection != 0 {
		query = query.Join("JOIN book_collection_books bcb ON bcb.book_id = book.id").
			Where("bcb.book_collection_id = ?", filters.Collection).
			Order("bcb.position ASC")
	} else if filters.CuratedCollection != 0 {
		// Two-step approach: first pull the ordered list of book ids of the
		// (published, curated) collection, then constrain the main book query
		// with WhereIn + array_position for stable ordering. This avoids a
		// JOIN-on-relations conflict that returned an empty result on prod.
		ids, err := collectionBookIDsOrdered(filters.CuratedCollection)
		if err != nil {
			logging.Error(err)
			return nil, 0, err
		}
		if len(ids) == 0 {
			return []models.Book{}, 0, nil
		}
		query = query.
			WhereIn("book.id IN (?)", ids).
			OrderExpr("array_position(?, book.id)", pg.Array(ids))
	} else {
		query = query.Order("book.id DESC")
	}

	count, err := query.Limit(filters.Limit).Offset(filters.Offset).SelectAndCount()
	if err != nil {
		logging.Error(err)
		return nil, 0, err
	}

	for i, book := range books {
		books[i].Fav = isFav(userFavs, book)
	}

	populateSeriesNumbers(books)

	return books, count, nil
}

// applyListFilters narrows the list to the requested scope, language and
// visibility.
//
//nolint:gocritic // reads one filter set; the list path passes it by value throughout
func applyListFilters(query *orm.Query, filters models.BookFilters, userID int64) *orm.Query {
	if filters.Fav {
		var booksIds []int64
		err := db.Model(&models.UserToBook{}).
			Column("book_id").
			Where("user_id = ?", userID).
			Order("id ASC").
			Select(&booksIds)
		if err != nil {
			logging.Warnf("Failed to load favorites for user %d: %v", userID, err)
			return query.Where("1 = 0")
		}
		if len(booksIds) > 0 {
			query = query.WhereIn("book.id IN (?)", booksIds)
		} else {
			return query.Where("1 = 0")
		}
	}

	// Both the web and the bot reach the catalog through here, so honoring
	// AllLanguages once covers both.
	if filters.Lang != "" && filters.Lang != AllLanguages {
		query = query.Where("book.lang = ?", filters.Lang)
	}

	if filters.UnApproved {
		query = query.Where("book.approved = false")
	} else {
		query = query.Where("book.approved = true")
	}

	if !filters.IncludeHidden {
		// Hide duplicate books from all results
		query = query.Where("book.duplicate_hidden = ?", false)
	}

	query = narrowByJunction(query, &models.OrderToAuthor{}, "author_id", int64(filters.Author), "")
	query = narrowByJunction(query, &models.OrderToSeries{}, "ser_id", int64(filters.Series), "")
	query = narrowByJunction(query, &models.BookCollectionBook{}, "book_collection_id", filters.Collection, "position ASC")
	query = narrowByJunction(query, &models.OrderToGenre{}, "genre_id", int64(filters.Genre), "")

	if filters.CuratedCollection != 0 {
		booksIds, err := collectionBookIDsOrdered(filters.CuratedCollection)
		if err == nil && len(booksIds) > 0 {
			query = query.WhereIn("book.id IN (?)", booksIds)
		} else {
			// Empty / non-public collection: no books should match.
			query = query.Where("FALSE")
		}
	}

	return query
}

// narrowByJunction restricts the list to the books a junction table links to
// one entity — an author, a series, a genre, a collection. A scope that is not
// asked for changes nothing; a scope that resolves to no book matches nothing,
// which is the only safe reading: widening back to the whole catalog would
// answer "this author's books" with everyone's.
func narrowByJunction(query *orm.Query, junction interface{}, column string, value int64, order string) *orm.Query {
	if value == 0 {
		return query
	}

	var bookIDs []int64
	q := db.Model(junction).Column("book_id").Where(column+" = ?", value)
	if order != "" {
		q = q.Order(order)
	}
	if err := q.Select(&bookIDs); err != nil {
		logging.Warnf("Failed to resolve %s = %d: %v", column, value, err)
		return query.Where("1 = 0")
	}
	if len(bookIDs) == 0 {
		return query.Where("1 = 0")
	}
	return query.WhereIn("book.id IN (?)", bookIDs)
}

// populateSeriesNumbers loads ser_no from the junction table into already-loaded Series.
// go-pg many-to-many doesn't carry over junction table columns, so we do it manually.
func populateSeriesNumbers(books []models.Book) {
	populateSeriesNumbersWithDB(db, books)
}

// populateSeriesNumbersWithDB is populateSeriesNumbers against an explicit
// handle: the package-global pool for legacy lists, the repository
// connection or rollback transaction for the new search.
func populateSeriesNumbersWithDB(dbh pg.DBI, books []models.Book) {
	if len(books) == 0 {
		return
	}

	bookIDs := make([]int64, 0, len(books))
	for _, b := range books {
		if len(b.Series) > 0 {
			bookIDs = append(bookIDs, b.ID)
		}
	}
	if len(bookIDs) == 0 {
		return
	}

	var junctions []models.OrderToSeries
	err := dbh.Model(&junctions).
		Where("book_id IN (?)", pg.In(bookIDs)).
		Select()
	if err != nil {
		logging.Warnf("Failed to load series numbers: %v", err)
		return
	}

	// Build lookup: bookID -> serID -> serNo
	type key struct{ bookID, serID int64 }
	lookup := make(map[key]int64, len(junctions))
	for _, j := range junctions {
		lookup[key{j.BookID, j.SeriesID}] = j.SerNo
	}

	// Indexed rather than ranged over a copy: `for _, book := range books` hands
	// out a whole Book per iteration only to write back through books[i]
	// anyway, and it leaves the index and the slice being written looking
	// unrelated.
	// The inner slice is taken once and ranged over directly. Writing through
	// books[i].Series[j] means neither a reader nor a checker can see in one
	// place that j belongs to the slice being indexed; a local slice header
	// shares the same backing array, so the assignment still lands on the book.
	for i := range books {
		bookID := books[i].ID
		series := books[i].Series
		for j := range series {
			if serNo, ok := lookup[key{bookID, series[j].ID}]; ok {
				series[j].SerNo = serNo
			}
		}
	}
}

// isFav checks if a book is favorited by the user
func isFav(userFavs []int64, book models.Book) bool {
	for _, favID := range userFavs {
		if favID == book.ID {
			return true
		}
	}
	return false
}

// GetLanguages returns a list of languages
func GetLanguages() models.Languages {
	var langRes models.Languages
	err := db.Model(&models.Book{}).
		Column("lang").
		ColumnExpr("count(*) AS language_count").
		Where("duplicate_hidden = ?", false).
		Group("lang").
		OrderExpr("language_count DESC").
		Select(&langRes)

	if err != nil {
		logging.Error(err)
		return nil
	}
	return langRes
}

// IsValidLanguage checks if the provided language exists in the books database
func IsValidLanguage(lang string) bool {
	if lang == "" {
		return true // Empty language is valid (user can have no language preference)
	}

	// Asking for everything is a choice, and it has to be a stored one: an
	// empty column is how a reader who has never been asked is recognized, so
	// clearing the field back to empty would put them in front of that question
	// again on every visit.
	if lang == AllLanguages {
		return true
	}

	count, err := db.Model(&models.Book{}).
		Where("lang = ?", lang).
		Where("duplicate_hidden = ?", false).
		Count()

	if err != nil {
		logging.Error(err)
		return false
	}

	return count > 0
}

// GetBook returns a book by id from archive
func GetBook(bookID int64) (models.Book, error) {
	book := &models.Book{ID: bookID}
	err := db.Model(book).WherePK().Select()
	if err != nil {
		return *book, err
	}
	return *book, nil
}

// collectionBookIDsOrdered returns the resolved book ids of one published curated
// collection in curator-defined order. Returns empty (not error) for missing /
// unpublished / non-curated ids so callers don't leak drafts.
//
// Uses a raw query because go-pg's ORM aliases the model table differently
// from the bare table name and JOIN ... ON book_collection_items.* fails with
// "invalid reference to FROM-clause entry".
func collectionBookIDsOrdered(collectionID int64) ([]int64, error) {
	var ids []int64
	_, err := db.Query(&ids, `
		SELECT i.book_id
		FROM book_collection_items i
		JOIN book_collections bc ON bc.id = i.collection_id
		WHERE i.collection_id = ?
		  AND i.book_id IS NOT NULL
		  AND i.match_status IN (?)
		  AND bc.is_curated = TRUE
		  AND bc.is_public = TRUE
		ORDER BY i.position ASC
	`, collectionID, pg.In([]string{models.MatchStatusAutoMatched, models.MatchStatusManual}))
	if err != nil {
		return nil, err
	}
	return ids, nil
}

// PickMostRecentBookID returns the id of the book among the given list that was
// registered most recently (registerdate DESC, with id DESC as a tiebreaker).
// Used by curated-collection auto-resolve to pick a single winner when the
// matcher's top score is shared by several copies of the same book.
func PickMostRecentBookID(ctx context.Context, ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	if len(ids) == 1 {
		return ids[0], nil
	}
	book := &models.Book{}
	err := db.ModelContext(ctx, book).
		Column("id").
		Where("id IN (?)", pg.In(ids)).
		Order("registerdate DESC", "id DESC").
		Limit(1).
		Select()
	if err != nil {
		return 0, err
	}
	return book.ID, nil
}

// GetBooksByIDs returns books matching the given ids preloaded with their authors.
// Used by the curated-collection admin UI to render candidate chips with full title
// and author names instead of bare numeric ids.
func GetBooksByIDs(ids []int64) ([]models.Book, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var books []models.Book
	err := db.Model(&books).
		Where("book.id IN (?)", pg.In(ids)).
		Relation("Authors").
		Select()
	return books, err
}

func HaveFavs(userID int64) (bool, error) {
	count, err := db.Model(&models.UserToBook{}).Where("user_id = ?", userID).Count()
	if count == 0 || err != nil {
		return false, err
	}
	return true, nil
}

// FavBook adds a book to user favs
func FavBook(userID int64, fav models.FavBook) (bool, error) {
	book := &models.Book{ID: fav.BookID}
	err := db.Model(book).WherePK().Select()
	if err != nil {
		return false, err
	}
	if fav.Fav {
		favBookObj := models.UserToBook{
			UserID: userID,
			BookID: fav.BookID,
		}
		_, err = db.Model(&favBookObj).Insert()
		if err != nil {
			return false, errors.New("duplicated_favorites")
		}
	} else {
		_, err := db.Model(&models.UserToBook{}).
			Where("book_id = ?", fav.BookID).
			Where("user_id = ?", userID).
			Delete()
		if err != nil {
			return false, errors.New("cannot_unfav")
		}
	}

	hf, err := HaveFavs(userID)
	return hf, err
}

// UpdateBook updates a book with provided fields
func UpdateBook(updateReq models.BookUpdateRequest) (models.Book, error) {
	var bookToUpdate models.Book

	// First, retrieve the existing book
	err := db.Model(&bookToUpdate).
		Where("id = ?", updateReq.ID).
		Relation("Authors").
		Relation("Series").
		Relation("Genres").
		Select()
	if err != nil {
		return bookToUpdate, err
	}

	tx, err := db.Begin()
	if err != nil {
		return bookToUpdate, err
	}
	commit := false
	defer func() {
		if !commit {
			_ = tx.Rollback()
		}
	}()

	// Build the update query dynamically based on provided fields
	query := tx.Model(&bookToUpdate).Where("id = ?", updateReq.ID)

	// Only update fields that are provided (not nil)
	fields := 0
	if updateReq.Title != nil {
		query = query.Set("title = ?", *updateReq.Title)
		fields++
	}
	if updateReq.Annotation != nil {
		query = query.Set("annotation = ?", *updateReq.Annotation)
		fields++
	}
	if updateReq.Lang != nil {
		query = query.Set("lang = ?", *updateReq.Lang)
		fields++
	}
	if updateReq.DocDate != nil {
		query = query.Set("docdate = ?", *updateReq.DocDate)
		fields++
	}
	if updateReq.Approved != nil {
		query = query.Set("approved = ?", *updateReq.Approved)
		fields++
	}
	if updateReq.DuplicateHidden != nil {
		query = query.Set("duplicate_hidden = ?", *updateReq.DuplicateHidden)
		fields++
	}
	if updateReq.DuplicateOfID != nil {
		query = query.Set("duplicate_of_id = ?", *updateReq.DuplicateOfID)
		fields++
	}

	// Nothing named means nothing to write. Left to itself go-pg reads an
	// absent Set as "update every column", so a request that changed no field
	// rewrote the whole row from the struct in memory — which is how a book
	// with nothing to update ended up failing a NOT NULL constraint.
	if fields > 0 {
		if _, err = query.Update(); err != nil {
			return bookToUpdate, err
		}
	}

	if updateReq.Authors != nil {
		if err := updateBookAuthorsFromUpdateRequest(tx, updateReq.ID, updateReq.Authors); err != nil {
			return bookToUpdate, err
		}
	}

	if updateReq.Series != nil {
		if err := updateBookSeriesFromUpdateRequest(tx, updateReq.ID, updateReq.Series); err != nil {
			return bookToUpdate, err
		}
	}

	if err := tx.Commit(); err != nil {
		return bookToUpdate, err
	}
	commit = true

	// Retrieve the updated book with all relations
	err = db.Model(&bookToUpdate).
		Where("id = ?", updateReq.ID).
		Relation("Authors").
		Relation("Series").
		Relation("Genres").
		Select()
	if err != nil {
		return bookToUpdate, err
	}

	books := []models.Book{bookToUpdate}
	populateSeriesNumbers(books)
	bookToUpdate = books[0]

	return bookToUpdate, nil
}

func updateBookAuthorsFromUpdateRequest(tx *pg.Tx, bookID int64, authors []models.Author) error {
	_, err := tx.Model(&models.OrderToAuthor{}).
		Where("book_id = ?", bookID).
		Delete()
	if err != nil && err != pg.ErrNoRows {
		return err
	}

	seenIDs := make(map[int64]struct{})
	seenNames := make(map[string]struct{})

	for _, author := range authors {
		fullName := strings.TrimSpace(author.FullName)
		if author.ID == 0 && fullName == "" {
			continue
		}

		var authorID int64
		if author.ID > 0 {
			authorID = author.ID
			if _, ok := seenIDs[authorID]; ok {
				continue
			}
			exists := &models.Author{}
			err := tx.Model(exists).
				Where("id = ?", authorID).
				Select(exists)
			if err != nil && err != pg.ErrNoRows {
				return err
			}
			if err == pg.ErrNoRows {
				if fullName == "" {
					continue
				}
				createdID, err := getOrCreateAuthorByName(tx, fullName)
				if err != nil {
					return err
				}
				authorID = createdID
			}
		} else {
			normalized := strings.ToLower(fullName)
			if _, ok := seenNames[normalized]; ok {
				continue
			}
			createdID, err := getOrCreateAuthorByName(tx, fullName)
			if err != nil {
				return err
			}
			authorID = createdID
			seenNames[normalized] = struct{}{}
		}

		if _, ok := seenIDs[authorID]; ok {
			continue
		}
		_, err := tx.Model(&models.OrderToAuthor{
			AuthorID: authorID,
			BookID:   bookID,
		}).Insert()
		if err != nil {
			return err
		}
		seenIDs[authorID] = struct{}{}
	}

	return nil
}

func updateBookSeriesFromUpdateRequest(tx *pg.Tx, bookID int64, series []models.Series) error {
	_, err := tx.Model(&models.OrderToSeries{}).
		Where("book_id = ?", bookID).
		Delete()
	if err != nil && err != pg.ErrNoRows {
		return err
	}

	seenIDs := make(map[int64]struct{})
	seenNames := make(map[string]struct{})

	for _, entry := range series {
		seriesName := strings.TrimSpace(entry.Ser)
		if entry.ID == 0 && seriesName == "" {
			continue
		}

		var seriesID int64
		if entry.ID > 0 {
			seriesID = entry.ID
			if _, ok := seenIDs[seriesID]; ok {
				continue
			}
			exists := &models.Series{}
			err := tx.Model(exists).
				Where("id = ?", seriesID).
				Select(exists)
			if err != nil && err != pg.ErrNoRows {
				return err
			}
			if err == pg.ErrNoRows {
				if seriesName == "" {
					continue
				}
				createdID, err := getOrCreateSeriesByName(tx, seriesName)
				if err != nil {
					return err
				}
				seriesID = createdID
			}
		} else {
			normalized := strings.ToLower(seriesName)
			if _, ok := seenNames[normalized]; ok {
				continue
			}
			createdID, err := getOrCreateSeriesByName(tx, seriesName)
			if err != nil {
				return err
			}
			seriesID = createdID
			seenNames[normalized] = struct{}{}
		}

		if _, ok := seenIDs[seriesID]; ok {
			continue
		}
		_, err := tx.Model(&models.OrderToSeries{
			SeriesID: seriesID,
			BookID:   bookID,
			SerNo:    entry.SerNo,
		}).Insert()
		if err != nil {
			return err
		}
		seenIDs[seriesID] = struct{}{}
	}

	return nil
}

func getOrCreateAuthorByName(tx *pg.Tx, name string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, errors.New("author name is empty")
	}

	existing := &models.Author{}
	err := tx.Model(existing).
		Where("full_name = ?", name).
		Select(existing)
	if err == nil {
		return existing.ID, nil
	}
	if err != nil && err != pg.ErrNoRows {
		return 0, err
	}

	author := &models.Author{FullName: name}
	_, err = tx.Model(author).Insert()
	if err != nil {
		return 0, err
	}
	return author.ID, nil
}

func getOrCreateSeriesByName(tx *pg.Tx, name string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, errors.New("series name is empty")
	}

	existing := &models.Series{}
	err := tx.Model(existing).
		Where("ser = ?", name).
		Select(existing)
	if err == nil {
		return existing.ID, nil
	}
	if err != nil && err != pg.ErrNoRows {
		return 0, err
	}

	series := &models.Series{Ser: name}
	_, err = tx.Model(series).Insert()
	if err != nil {
		return 0, err
	}
	return series.ID, nil
}
