package database

import (
	"errors"
	"fmt"
	"github.com/go-pg/pg/v10/orm"
	"github.com/sirupsen/logrus"
	"gopds-api/models"
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
