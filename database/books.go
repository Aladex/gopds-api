package database

import (
	"errors"
	"gopds-api/logging"
	"gopds-api/models"
	"sort"
	"strings"

	"github.com/go-pg/pg/v10/orm"
)

func GetBooks(userID int64, filters models.BookFilters) ([]models.Book, int, error) {
	// Use the enhanced search logic for all searches
	return GetBooksEnhanced(userID, filters)
}

// GetBooksEnhanced returns an enhanced list of books with improved search logic
func GetBooksEnhanced(userID int64, filters models.BookFilters) ([]models.Book, int, error) {
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

	// If we have a title search, use enhanced search logic
	if filters.Title != "" && len(strings.TrimSpace(filters.Title)) >= 3 {
		return getBooksByTitleEnhanced(userID, filters, userFavs)
	}

	// For non-title searches, use simpler logic with basic filters
	query := db.Model(&books).
		Relation("Authors").
		Relation("Users").
		Relation("Series").
		ColumnExpr("book.*, (SELECT COUNT(*) FROM favorite_books WHERE book_id = book.id) AS favorite_count")

	query = applyNonTitleFilters(query, filters, userID)

	// Simple sorting for non-title searches
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

	return books, count, nil
}

// getBooksByTitleEnhanced implements enhanced title search logic similar to autocomplete
func getBooksByTitleEnhanced(userID int64, filters models.BookFilters, userFavs []int64) ([]models.Book, int, error) {
	lowerQuery := strings.ToLower(strings.TrimSpace(filters.Title))

	// Strategy 1: Get books with exact and partial matches (like autocomplete)
	var candidateBooks []models.Book

	bookQuery := db.Model(&candidateBooks).
		Relation("Authors").
		Relation("Users").
		Relation("Series").
		ColumnExpr("book.*, (SELECT COUNT(*) FROM favorite_books WHERE book_id = book.id) AS favorite_count").
		Where("book.approved = true").
		Where("book.title IS NOT NULL").
		Where("book.title != ''").
		Where("lower(book.title) LIKE ?", "%"+lowerQuery+"%")

	// Apply other filters (lang, author, series, etc.)
	bookQuery = applyNonTitleFilters(bookQuery, filters, userID)

	// Get a larger set for better sorting (like autocomplete does with 500 limit)
	err := bookQuery.
		OrderExpr("similarity(book.title, ?) DESC", filters.Title).
		OrderExpr("strpos(lower(book.title), ?) ASC", lowerQuery).
		OrderExpr("book.id DESC").
		Limit(500).
		Select()

	if err != nil {
		logging.Error(err)
		return nil, 0, err
	}

	// Strategy 2: If we have few results, try trigram search
	if len(candidateBooks) < 50 {
		var trigramBooks []models.Book

		trigramQuery := db.Model(&trigramBooks).
			Relation("Authors").
			Relation("Users").
			Relation("Series").
			ColumnExpr("book.*, (SELECT COUNT(*) FROM favorite_books WHERE book_id = book.id) AS favorite_count").
			Where("book.approved = true").
			Where("book.title IS NOT NULL").
			Where("book.title != ''").
			Where("book.title % ?", filters.Title).
			Where("similarity(book.title, ?) > 0.3", filters.Title) // Lower threshold than original

		trigramQuery = applyNonTitleFilters(trigramQuery, filters, userID)

		err := trigramQuery.
			OrderExpr("similarity(book.title, ?) DESC", filters.Title).
			Limit(100).
			Select()

		if err == nil {
			// Merge results, avoiding duplicates
			seenBooks := make(map[int64]bool)
			for _, book := range candidateBooks {
				seenBooks[book.ID] = true
			}

			for _, book := range trigramBooks {
				if !seenBooks[book.ID] {
					candidateBooks = append(candidateBooks, book)
					seenBooks[book.ID] = true
				}
			}
		}
	}

	// Score and sort books by relevance (like autocomplete does for authors)
	type bookWithScore struct {
		book  models.Book
		score int
	}

	var scoredBooks []bookWithScore
	for _, book := range candidateBooks {
		score := calculateBookScore(book.Title, filters.Title)
		scoredBooks = append(scoredBooks, bookWithScore{
			book:  book,
			score: score,
		})
	}

	// Sort by score (best matches first)
	sort.Slice(scoredBooks, func(i, j int) bool {
		if scoredBooks[i].score != scoredBooks[j].score {
			return scoredBooks[i].score > scoredBooks[j].score
		}
		// If scores are equal, prefer newer books
		return scoredBooks[i].book.ID > scoredBooks[j].book.ID
	})

	// Apply pagination
	totalCount := len(scoredBooks)
	start := filters.Offset
	end := filters.Offset + filters.Limit

	if start >= totalCount {
		return []models.Book{}, totalCount, nil
	}
	if end > totalCount {
		end = totalCount
	}

	var finalBooks []models.Book
	for i := start; i < end; i++ {
		book := scoredBooks[i].book
		book.Fav = isFav(userFavs, book)
		finalBooks = append(finalBooks, book)
	}

	return finalBooks, totalCount, nil
}

// calculateBookScore calculates relevance score for book title (similar to author scoring in autocomplete)
func calculateBookScore(title, query string) int {
	lowerTitle := strings.ToLower(strings.TrimSpace(title))
	lowerQuery := strings.ToLower(strings.TrimSpace(query))

	score := 0

	// Exact match gets highest score
	if lowerTitle == lowerQuery {
		score += 1000
	}

	// Title starts with query gets high score
	if strings.HasPrefix(lowerTitle, lowerQuery) {
		score += 500
	}

	// Query at the beginning of title gets bonus
	if strings.Contains(lowerTitle, lowerQuery) {
		pos := strings.Index(lowerTitle, lowerQuery)
		score += 200 - pos // Earlier position = higher score
	}

	// Word boundary matches get bonus
	titleWords := strings.Fields(lowerTitle)
	queryWords := strings.Fields(lowerQuery)

	for _, qWord := range queryWords {
		for _, tWord := range titleWords {
			if strings.HasPrefix(tWord, qWord) {
				score += 100
			} else if strings.Contains(tWord, qWord) {
				score += 50
			}
		}
	}

	// Length similarity bonus (prefer similar length titles)
	lenDiff := abs(len(lowerTitle) - len(lowerQuery))
	if lenDiff < 5 {
		score += 25
	}

	return score
}

// applyNonTitleFilters applies all filters except title search
func applyNonTitleFilters(query *orm.Query, filters models.BookFilters, userID int64) *orm.Query {
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

// abs returns absolute value of integer
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
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

	count, err := db.Model(&models.Book{}).
		Where("lang = ?", lang).
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
