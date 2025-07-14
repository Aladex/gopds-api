package database

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-pg/pg/v10/orm"
	"github.com/sirupsen/logrus"
	"gopds-api/models"
	"net/http"
	"os"
	"sort"
	"strings"
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

	query = applyFilters(query, &filters, userID)

	query = applySorting(query, filters, userID)

	// query = query.DistinctOn("book.id")

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
	if filters.Fav {
		var booksIds []models.UserToBook
		var exprArr []string
		err := db.Model(&booksIds).
			Column("book_id").
			Where("user_id = ?", userID).
			Order("id ASC").
			Select(&booksIds)
		if err == nil && len(booksIds) > 0 {
			var bIds []int64

			for _, bid := range booksIds {
				bIds = append(bIds, bid.BookID)
				exprArr = append(exprArr, fmt.Sprintf("book.id=%d ASC", bid.BookID))
			}
			query = query.WhereIn("book.id IN (?)", bIds)
			query = query.OrderExpr(strings.Join(exprArr, ","))
		}
	} else if filters.UsersFavorites {
		query = query.Join("JOIN favorite_books fb ON fb.book_id = book.id").
			Group("book.id").
			OrderExpr("favorite_count DESC, book.id DESC")
	} else if filters.Collection != 0 {
		query = query.Join("JOIN book_collection_books bcb ON bcb.book_id = book.id").
			Where("bcb.book_collection_id = ?", filters.Collection).
			Order("bcb.position ASC")
	} else if len(filters.OrderedByVector) > 0 {
		// Строим CASE для ORDER BY в порядке из Qdrant (descending score)
		var caseExpr strings.Builder
		caseExpr.WriteString("CASE book.id ")
		for i, id := range filters.OrderedByVector {
			caseExpr.WriteString(fmt.Sprintf("WHEN %d THEN %d ", id, i+1)) // Позиция от 1 до N
		}
		caseExpr.WriteString(fmt.Sprintf("ELSE %d END ASC", len(filters.OrderedByVector)+1)) // Не в списке - в конец
		query = query.OrderExpr(caseExpr.String())
	} else {
		query = query.Order("book.id DESC")
	}

	return query
}

type SearchHit struct {
	ID    int64
	Score float64
}

func getRelevantIDsFromQdrant(title string, lang string) ([]SearchHit, error) {
	embedURL := os.Getenv("EMBEDDING_URL")
	if embedURL == "" {
		embedURL = "http://localhost:8081/embed"
	}

	embedReqBody, _ := json.Marshal(map[string]interface{}{
		"inputs": []string{title},
	})

	resp, err := http.Post(embedURL, "application/json", bytes.NewBuffer(embedReqBody))
	if err != nil {
		return nil, fmt.Errorf("error building embedding request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding request failed with status: %d", resp.StatusCode)
	}

	var embVec [][]float32
	if err := json.NewDecoder(resp.Body).Decode(&embVec); err != nil {
		return nil, fmt.Errorf("error decoding embedding response: %w", err)
	}
	if len(embVec) == 0 {
		return nil, fmt.Errorf("embedding response is empty")
	}

	// Filter by language if provided
	var filter map[string]interface{}
	if lang != "" {
		filter = map[string]interface{}{
			"must": []map[string]interface{}{
				{
					"key":   "lang",
					"match": map[string]interface{}{"value": lang},
				},
			},
		}
	}

	// Search in Qdrant
	qdrantURL := os.Getenv("QDRANT_URL")
	if qdrantURL == "" {
		qdrantURL = "http://localhost:6333"
	}
	searchURL := qdrantURL + "/collections/books/points/search"

	searchReq := map[string]interface{}{
		"vector":       embVec[0],
		"top":          500,
		"with_payload": false,
	}
	if filter != nil {
		searchReq["filter"] = filter
	}

	reqBody, _ := json.Marshal(searchReq)

	searchResp, err := http.Post(searchURL, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("error sending search request to Qdrant: %w", err)
	}
	defer searchResp.Body.Close()

	if searchResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Qdrant search failed with status: %d", searchResp.StatusCode)
	}

	var searchResult struct {
		Result []struct {
			ID    interface{} `json:"id"`
			Score float64     `json:"score"`
		} `json:"result"`
	}
	if err := json.NewDecoder(searchResp.Body).Decode(&searchResult); err != nil {
		return nil, fmt.Errorf("error decoding Qdrant search response: %w", err)
	}

	var hits []SearchHit
	for _, res := range searchResult.Result {
		var id int64
		switch v := res.ID.(type) {
		case float64:
			id = int64(v)
		case int:
			id = int64(v)
		case int64:
			id = v
		default:
			return nil, fmt.Errorf("unexpected ID type: %T", v)
		}
		hits = append(hits, SearchHit{ID: id, Score: res.Score})
	}

	return hits, nil
}

func applyFilters(query *orm.Query, filters *models.BookFilters, userID int64) *orm.Query {
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

	if filters.Title != "" && filters.Author == 0 && filters.Series == 0 && filters.Collection == 0 {
		// Hybrid search: PG trgm + Qdrant vector
		// Step 1: PG trgm search
		type PgHit struct {
			ID    int64   `pg:"id"`
			Score float64 `pg:"score"`
		}
		var pgHits []PgHit
		trgmThreshold := 0.7 // Adjust as needed
		err := db.Model((*models.Book)(nil)).
			ColumnExpr("id, similarity(title, ?) AS score", filters.Title).
			Where("similarity(title, ?) > ?", filters.Title, trgmThreshold).
			OrderExpr("score DESC").
			Limit(500). // Match Qdrant top
			Select(&pgHits)
		if err != nil {
			logrus.Printf("PG trgm error: %v", err)
			pgHits = []PgHit{}
		}

		// Step 2: Qdrant vector search
		vectorHits, err := getRelevantIDsFromQdrant(filters.Title, filters.Lang)
		if err != nil {
			logrus.Printf("Qdrant error: %v", err)
			vectorHits = []SearchHit{}
		}

		// Step 3: Create rank maps (rank starts from 1)
		pgMap := make(map[int64]int)
		for rank, hit := range pgHits {
			pgMap[hit.ID] = rank + 1
		}

		vectorMap := make(map[int64]int)
		for rank, hit := range vectorHits {
			vectorMap[hit.ID] = rank + 1
		}

		// Выделяем точные совпадения
		var exactMatches []int64
		err = db.Model((*models.Book)(nil)).
			Column("id").
			Where("LOWER(title) = LOWER(?)", filters.Title).
			Select(&exactMatches)
		if err != nil {
			logrus.Printf("Exact match error: %v", err)
		}

		// Step 4: RRF to combine
		finalIDs := rrfRank(pgMap, vectorMap)
		// Удаляем точные из finalIDs (если там уже есть)
		idSet := make(map[int64]struct{})
		for _, id := range exactMatches {
			idSet[id] = struct{}{}
		}
		deduped := make([]int64, 0, len(finalIDs))
		for _, id := range finalIDs {
			if _, exists := idSet[id]; !exists {
				deduped = append(deduped, id)
			}
		}

		// Склеиваем: сначала точные, потом остальное
		finalIDs = append(exactMatches, deduped...)

		if len(finalIDs) > 0 {
			query = query.WhereIn("book.id IN (?)", finalIDs)
			filters.OrderedByVector = finalIDs
		} else {
			query = query.Where("1=0")
		}
	} else if filters.Title != "" {
		query = query.Where("book.title ILIKE ?", fmt.Sprintf("%%%s%%", filters.Title))
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

// rrfRank combines ranks from PG and Vector using Reciprocal Rank Fusion
func rrfRank(pgMap, vectorMap map[int64]int) []int64 {
	combinedScores := make(map[int64]float64)
	allIDs := make(map[int64]struct{})
	for id := range pgMap {
		allIDs[id] = struct{}{}
	}
	for id := range vectorMap {
		allIDs[id] = struct{}{}
	}

	k := 60.0 // RRF constant
	for id := range allIDs {
		score := 0.0
		if rank, ok := pgMap[id]; ok {
			score += 1.0 / (float64(rank) + k)
		}
		if rank, ok := vectorMap[id]; ok {
			score += 1.0 / (float64(rank) + k)
		}
		combinedScores[id] = score
	}

	var sorted []int64
	for id := range combinedScores {
		sorted = append(sorted, id)
	}
	sort.Slice(sorted, func(i, j int) bool {
		return combinedScores[sorted[i]] > combinedScores[sorted[j]]
	})

	return sorted
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
