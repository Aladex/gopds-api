package database

import (
	"errors"
	"fmt"
	"gopds-api/models"
	"sort"
	"strings"

	"github.com/go-pg/pg/v10/orm"
	"github.com/sirupsen/logrus"
)

func GetBooks(userID int64, filters models.BookFilters) ([]models.Book, int, error) {
	books := []models.Book{}
	var userFavs []int64

	err := db.Model(&models.UserToBook{}).Where("user_id = ?", userID).Select(&userFavs)
	if err != nil {
		logrus.Print(err)
		return nil, 0, err
	}

	if filters.Limit > 100 || filters.Limit == 0 {
		filters.Limit = 100
	}

	query := db.Model(&books).
		Relation("Authors").
		Relation("Users").
		Relation("Series").
		ColumnExpr("book.*, (SELECT COUNT(*) FROM favorite_books WHERE book_id = book.id) AS favorite_count")

	query = applyFilters(query, filters, userID)

	query = applySorting(query, filters, userID)

	count, err := query.Limit(filters.Limit).Offset(filters.Offset).SelectAndCount()
	if err != nil {
		logrus.Print(err)
		return nil, 0, err
	}

	for i, book := range books {
		books[i].Fav = isFav(userFavs, book)
	}

	return books, count, nil
}

func applySorting(query *orm.Query, filters models.BookFilters, userID int64) *orm.Query {
	// If title filter is set, use fuzzy sorting: exact_match DESC, strpos ASC, sim DESC, lev ASC, id DESC
	if filters.Title != "" {
		lowerTitle := strings.ToLower(filters.Title)
		query = query.OrderExpr("(book.title ILIKE ?) DESC", fmt.Sprintf("%%%s%%", filters.Title)).
			OrderExpr("strpos(lower(book.title), ?) ASC", lowerTitle).
			OrderExpr("similarity(book.title, ?) DESC", filters.Title).
			OrderExpr("levenshtein(lower(book.title), ?) ASC", lowerTitle).
			Order("book.id DESC")
		return query
	}

	// Default sorting for other filters
	if filters.Fav {
		var booksIds []models.UserToBook
		err := db.Model(&booksIds).
			Column("book_id").
			Where("user_id = ?", userID).
			Order("id ASC").
			Select(&booksIds)
		if err == nil && len(booksIds) > 0 {
			var bIds []int64
			for _, bid := range booksIds {
				bIds = append(bIds, bid.BookID)
			}
			query = query.WhereIn("book.id IN (?)", bIds)
			// Use CASE statement for safe ordering instead of dynamic SQL construction
			query = query.OrderExpr("CASE book.id "+
				"WHEN ? THEN 1 "+
				strings.Repeat("WHEN ? THEN ? ", len(bIds)-1)+
				"ELSE ? END ASC",
				append([]interface{}{bIds[0], 1},
					func() []interface{} {
						var args []interface{}
						for i := 1; i < len(bIds); i++ {
							args = append(args, bIds[i], i+1)
						}
						args = append(args, len(bIds)+1)
						return args
					}()...)...)
		}
	} else if filters.UsersFavorites {
		query = query.Join("JOIN favorite_books fb ON fb.book_id = book.id").
			Group("book.id").
			OrderExpr("favorite_count DESC, book.id DESC")
	} else if filters.Collection != 0 {
		query = query.Join("JOIN book_collection_books bcb ON bcb.book_id = book.id").
			Where("bcb.book_collection_id = ?", filters.Collection).
			Order("bcb.position ASC")
	} else {
		query = query.Order("book.id DESC")
	}

	return query
}

func applyFilters(query *orm.Query, filters models.BookFilters, userID int64) *orm.Query {
	if filters.Fav {
		var booksIds []int64
		err := db.Model(&models.UserToBook{}).
			Column("book_id").
			Where("user_id = ?", userID).
			Order("id ASC").
			Select(&booksIds)
		if err == nil && len(booksIds) > 0 {
			query = query.WhereIn("book.id IN (?)", booksIds)
		}
	}

	if filters.Title != "" {
		lowerTitle := strings.ToLower(filters.Title)
		// Fuzzy search: ILIKE for exact/partial OR pg_trgm for similar, with thresholds
		query = query.WhereGroup(func(q *orm.Query) (*orm.Query, error) {
			q = q.Where("book.title % ?", filters.Title).
				WhereOr("book.title ILIKE ?", fmt.Sprintf("%%%s%%", filters.Title))
			// Thresholds to improve relevance and performance
			q = q.Where("similarity(book.title, ?) > 0.5", filters.Title)
			q = q.Where("levenshtein(lower(book.title), ?) <= 5", lowerTitle)
			return q, nil
		})
	}

	if filters.Lang != "" {
		query = query.Where("book.lang = ?", filters.Lang)
	}

	if filters.UnApproved {
		query = query.Where("book.approved = false")
	} else {
		query = query.Where("book.approved = true")
	}

	if filters.Author != 0 {
		var booksIds []int64
		err := db.Model(&models.OrderToAuthor{}).
			Column("book_id").
			Where("author_id = ?", filters.Author).
			Select(&booksIds)
		if err == nil && len(booksIds) > 0 {
			query = query.WhereIn("book.id IN (?)", booksIds)
		}
	}

	if filters.Series != 0 {
		var booksIds []int64
		err := db.Model(&models.OrderToSeries{}).
			Column("book_id").
			Where("ser_id = ?", filters.Series).
			Select(&booksIds)
		if err == nil && len(booksIds) > 0 {
			query = query.WhereIn("book.id IN (?)", booksIds)
		}
	}

	if filters.Collection != 0 {
		var booksIds []int64
		err := db.Model(&models.BookCollectionBook{}).
			Column("book_id").
			Where("book_collection_id = ?", filters.Collection).
			Order("position ASC").
			Select(&booksIds)
		if err == nil && len(booksIds) > 0 {
			query = query.WhereIn("book.id IN (?)", booksIds)
		}
	}

	return query
}

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
		Group("lang").
		OrderExpr("language_count DESC").
		Select(&langRes)

	if err != nil {
		logrus.Print(err)
		return nil
	}
	return langRes
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

// UpdateBook updates a book
func UpdateBook(book models.Book) (models.Book, error) {
	var bookToChange models.Book
	err := db.Model(&bookToChange).Where("id = ?", book.ID).Select()
	if err != nil {
		return bookToChange, err
	}
	_, err = db.Model(&book).Set("approved = ?", book.Approved).Where("id = ?", book.ID).Update(&bookToChange)
	if err != nil {
		return bookToChange, err
	}
	return bookToChange, nil
}

// GetAutocompleteSuggestions returns suggestions for autocomplete
func GetAutocompleteSuggestions(query string, searchType string, authorID string, lang string) ([]models.AutocompleteSuggestion, error) {
	var suggestions []models.AutocompleteSuggestion

	if len(query) < 4 {
		return suggestions, nil
	}

	lowerQuery := strings.ToLower(strings.TrimSpace(query))

	// ----------- BOOKS ------------
	if searchType == "title" || searchType == "all" {
		var books []models.Book
		bookQuery := db.Model(&books).
			Column("book.id", "book.title").
			Where("book.approved = true").
			Where("book.title IS NOT NULL").
			Where("book.title != ''").
			Where("lower(book.title) LIKE ?", "%"+lowerQuery+"%")

		// If language is specified, filter books only for this language
		if lang != "" {
			bookQuery = bookQuery.Where("book.lang = ?", lang)
		}

		// If author ID is specified, filter books only for this author
		if authorID != "" {
			bookQuery = bookQuery.
				Join("JOIN opds_catalog_bauthor atb ON book.id = atb.book_id").
				Where("atb.author_id = ?", authorID)
		}

		err := bookQuery.
			OrderExpr("similarity(book.title, ?) DESC", query).
			OrderExpr("strpos(lower(book.title), ?) ASC", lowerQuery).
			Limit(500).
			Select()

		if err != nil {
		}

		if err == nil {
			seen := make(map[string]bool)
			for _, b := range books {
				n := strings.ToLower(strings.TrimSpace(b.Title))
				if n == "" || seen[n] {
					continue
				}
				seen[n] = true
				suggestions = append(suggestions, models.AutocompleteSuggestion{
					Value: b.Title,
					Type:  "book",
					ID:    b.ID,
				})
				if len(suggestions) >= 10 {
					break
				}
			}
		}
	}

	// ----------- AUTHORS ------------
	if searchType == "author" || searchType == "all" {
		queryWords := strings.Fields(strings.ToLower(strings.TrimSpace(query)))

		if len(queryWords) > 0 {
			var allAuthors []models.Author

			// Strategy 1: Quick search using LIKE on individual words (uses index)
			if len(queryWords) >= 2 {
				var multiWordAuthors []models.Author
				var whereConditions []string
				var whereArgs []interface{}

				for _, word := range queryWords {
					if len(word) > 1 {
						whereConditions = append(whereConditions, "lower(full_name) LIKE ?")
						whereArgs = append(whereArgs, "%"+word+"%")
					}
				}

				if len(whereConditions) > 0 {
					whereClause := strings.Join(whereConditions, " AND ")

					err := db.Model(&multiWordAuthors).
						Column("id", "full_name").
						Where("full_name IS NOT NULL").
						Where("full_name != ''").
						Where(whereClause, whereArgs...).
						Limit(50).
						Select()

					if err == nil {
						allAuthors = append(allAuthors, multiWordAuthors...)
					}
				}
			}

			// Strategy 2: Search by full query using LIKE (if few results)
			if len(allAuthors) < 20 {
				var exactAuthors []models.Author

				err := db.Model(&exactAuthors).
					Column("id", "full_name").
					Where("full_name IS NOT NULL").
					Where("full_name != ''").
					Where("lower(full_name) LIKE ?", "%"+lowerQuery+"%").
					Limit(30).
					Select()

				if err == nil {
					allAuthors = append(allAuthors, exactAuthors...)
				}
			}

			// Strategy 3: Search by individual words (if still few results)
			if len(allAuthors) < 30 {
				for _, word := range queryWords {
					if len(word) > 2 {
						var wordAuthors []models.Author

						err := db.Model(&wordAuthors).
							Column("id", "full_name").
							Where("full_name IS NOT NULL").
							Where("full_name != ''").
							Where("lower(full_name) LIKE ?", "%"+word+"%").
							Limit(20).
							Select()

						if err == nil {
							allAuthors = append(allAuthors, wordAuthors...)
						}
					}
				}
			}

			// Strategy 4: Trigram search only as a last resort
			if len(allAuthors) < 10 {
				var trigramAuthors []models.Author

				err := db.Model(&trigramAuthors).
					Column("id", "full_name").
					Where("full_name IS NOT NULL").
					Where("full_name != ''").
					Where("full_name % ?", query).
					OrderExpr("similarity(full_name, ?) DESC", query).
					Limit(20).
					Select()

				if err == nil {
					allAuthors = append(allAuthors, trigramAuthors...)
				}
			}

			if len(allAuthors) > 0 {
				type authorWithScore struct {
					author models.Author
					score  int
				}

				var scoredAuthors []authorWithScore
				seenAuthors := make(map[string]bool)

				for _, author := range allAuthors {
					if author.FullName != "" {
						normalizedName := strings.ToLower(strings.TrimSpace(author.FullName))

						if seenAuthors[normalizedName] {
							continue
						}
						seenAuthors[normalizedName] = true

						score := calculateAuthorScore(author.FullName, query, queryWords)

						if score > 0 {
							scoredAuthors = append(scoredAuthors, authorWithScore{
								author: author,
								score:  score,
							})
						}
					}
				}

				sort.Slice(scoredAuthors, func(i, j int) bool {
					return scoredAuthors[i].score > scoredAuthors[j].score
				})

				authorCount := 0
				maxAuthors := 10
				if searchType == "author" {
					maxAuthors = 15
				}

				for _, scoredAuthor := range scoredAuthors {
					if authorCount >= maxAuthors {
						break
					}

					suggestions = append(suggestions, models.AutocompleteSuggestion{
						Value: scoredAuthor.author.FullName,
						Type:  "author",
						ID:    scoredAuthor.author.ID,
					})
					authorCount++
				}
			}
		}
	}

	if len(suggestions) > 15 {
		suggestions = suggestions[:15]
	}

	return suggestions, nil
}

// calculateAuthorScore calculates the relevance of an author's name
func calculateAuthorScore(fullName, originalQuery string, queryWords []string) int {
	lowerFullName := strings.ToLower(fullName)
	lowerQuery := strings.ToLower(originalQuery)
	nameWords := strings.Fields(lowerFullName)
	score := 0
	matched := 0

	// exact match (case insensitive)
	if lowerFullName == lowerQuery {
		return 2000
	}

	// substring match
	if strings.Contains(lowerFullName, lowerQuery) {
		score += 500
	}

	// check words
	nameSet := make(map[string]bool, len(nameWords))
	for _, nw := range nameWords {
		nameSet[strings.ToLower(nw)] = true
	}

	for _, qw := range queryWords {
		lowerQw := strings.ToLower(qw)
		if nameSet[lowerQw] {
			matched++
			score += 300
		} else {
			// soft search: prefix/substring
			for _, nw := range nameWords {
				lowerNw := strings.ToLower(nw)
				if strings.HasPrefix(lowerNw, lowerQw) && len(lowerQw) >= 3 {
					matched++
					score += 150
					break
				}
				if strings.Contains(lowerNw, lowerQw) && len(lowerQw) >= 3 {
					matched++
					score += 75
					break
				}
			}
		}
	}

	// if all words are found regardless of order
	if matched == len(queryWords) {
		score += 600
		// if word count matches - additional bonus
		if len(nameWords) == len(queryWords) {
			score += 200
		}
	}

	// permutations of two words
	if len(queryWords) == 2 && len(nameWords) >= 2 {
		lowerQuery1 := strings.ToLower(queryWords[0])
		lowerQuery2 := strings.ToLower(queryWords[1])
		if nameSet[lowerQuery1] && nameSet[lowerQuery2] {
			score += 300
		}
	}

	// penalty for too long names
	if len(nameWords) > 5 {
		score -= (len(nameWords) - 5) * 20
	}

	if matched > 0 && score < 50 {
		score = 50
	}
	return score
}
