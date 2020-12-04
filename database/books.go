package database

import (
	"errors"
	"fmt"
	"github.com/go-pg/pg/v9/orm"
	"gopds-api/models"
	"strings"
)

func isFav(ids []int64, book models.Book) bool {
	for _, id := range ids {
		if book.ID == id {
			return true
		}
	}
	return false
}

// GetBooks Возвращает список книг и общее количество при селекте
func GetBooks(userID int64, filters models.BookFilters) ([]models.Book, models.Languages, int, error) {
	books := []models.Book{}
	var userFavs []int64
	err := db.Model(&models.UserToBook{}).Where("user_id = ?", userID).Select(&userFavs)

	var langRes models.Languages

	if filters.Limit > 100 || filters.Limit == 0 {
		filters.Limit = 100
	}
	err = db.Model(&models.Book{}).
		Column("lang").
		ColumnExpr("count(*) AS language_count").
		Group("lang").
		OrderExpr("language_count DESC").
		Select(&langRes)

	if err != nil {
		customLog.Print(err)
		return nil, langRes, 0, err
	}

	count, err := db.Model(&books).
		Relation("Authors").
		Relation("Users").
		Relation("Series").
		WhereGroup(func(q *orm.Query) (*orm.Query, error) {
			if filters.Title != "" && filters.Author == 0 {
				q = q.Where("title % ?", filters.Title).
					OrderExpr("title <-> ? ASC", filters.Title)
			} else {
				q = q.Order("id DESC")
			}
			if filters.Lang != "" {
				q = q.Where("lang = ?", filters.Lang)
			}
			if filters.Author != 0 {
				var booksIds []int64
				err := db.Model(&models.OrderToAuthor{}).
					Column("book_id").
					Where("author_id = ?", filters.Author).
					Select(&booksIds)
				if err == nil {
					for _, title := range strings.Split(filters.Title, " ") {
						q = q.Where("title ILIKE ?", fmt.Sprintf("%%%s%%", title))
					}
					q = q.WhereIn("id IN (?)", booksIds)
				}
			}
			if filters.Fav {
				var booksIds []int64
				err = db.Model(&models.UserToBook{}).
					Column("book_id").
					Where("user_id = ?", userID).
					Select(&booksIds)
				if err == nil {
					if len(booksIds) > 0 {
						q = q.WhereIn("id IN (?)", booksIds)
					}
				}
			}
			if filters.Series != 0 {
				var booksIds []int64
				err := db.Model(&models.OrderToSeries{}).
					Column("book_id").
					Where("ser_id = ?", filters.Series).
					Select(&booksIds)
				if err == nil {
					q = q.WhereIn("id IN (?)", booksIds)
				}
			}
			return q, nil
		}).
		Limit(filters.Limit).
		Offset(filters.Offset).
		SelectAndCount()
	if err != nil {
		customLog.Print(err)
		return nil, langRes, 0, err
	}

	for i, book := range books {
		books[i].Fav = isFav(userFavs, book)
	}

	return books, langRes, count, nil
}

// GetBook возвращает информацию по книге для того, чтобы вытащить ее из архива
func GetBook(bookID int64) (models.Book, error) {
	book := &models.Book{ID: bookID}
	err := db.Select(book)
	if err != nil {
		return *book, err
	}
	return *book, nil
}

// FavBook добавляет книгу в избранное
func FavBook(userID int64, fav models.FavBook) (models.Book, error) {
	book := &models.Book{ID: fav.BookID}
	err := db.Select(book)
	if err != nil {
		return *book, err
	}
	if fav.Fav {
		favBookObj := models.UserToBook{
			UserID: userID,
			BookID: fav.BookID,
		}
		_, err = db.Model(&favBookObj).Insert()
		if err != nil {
			return *book, errors.New("duplicated_favorites")
		}
	} else {
		_, err := db.Model(&models.UserToBook{}).
			Where("book_id = ?", fav.BookID).
			Where("user_id = ?", userID).
			Delete()
		if err != nil {
			return *book, errors.New("cannot_unfav")
		}
	}

	return *book, nil
}
