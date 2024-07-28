package database

import (
	"errors"
	"fmt"
	"github.com/go-pg/pg/v10/orm"
	"github.com/sirupsen/logrus"
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

// GetBooks returns a list of books
func GetBooks(userID int64, filters models.BookFilters) ([]models.Book, int, error) {
	books := []models.Book{}
	var userFavs []int64
	err := db.Model(&models.UserToBook{}).Where("user_id = ?", userID).Select(&userFavs)

	if filters.Limit > 100 || filters.Limit == 0 {
		filters.Limit = 100
	}

	count, err := db.Model(&books).
		Relation("Authors").
		Relation("Users").
		Relation("Series").
		ColumnExpr("book.*, (SELECT COUNT(*) FROM favorite_books WHERE book_id = book.id) AS favorite_count").
		WhereGroup(func(q *orm.Query) (*orm.Query, error) {
			if filters.Fav {
				var booksIds []models.UserToBook
				var exprArr []string
				err = db.Model(&booksIds).
					Column("book_id").
					Where("user_id = ?", userID).
					Order("id ASC").
					Select(&booksIds)
				if err == nil {
					if len(booksIds) > 0 {
						var bIds []int64

						for _, bid := range booksIds {
							bIds = append(bIds, bid.BookID)
							exprArr = append(exprArr, fmt.Sprintf("id=%d ASC", bid.BookID))
						}
						q = q.WhereIn("id IN (?)", bIds)

					}
					q = q.OrderExpr(strings.Join(exprArr, ","))
				}
			}
			if filters.Title != "" && filters.Author == 0 {
				q = q.Where("title % ?", filters.Title).
					OrderExpr("title <-> ? ASC", filters.Title)
			} else {

				if filters.UsersFavorites {
					// Get only books from UserToBook relation table favorite_books of all users and order by count of book_id
					q = q.Join("JOIN favorite_books fb ON fb.book_id = book.id")
					q = q.Group("book.id")
					// Order by count of favorites in descending order, then by book.id in descending order
					q = q.OrderExpr("favorite_count DESC, book.id DESC")
				} else {
					q = q.Order("id DESC")
				}
			}
			if filters.Lang != "" {
				q = q.Where("lang = ?", filters.Lang)
			}
			if filters.UnApproved {
				q = q.Where("approved = false")
			} else {
				q = q.Where("approved = true")
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
		logrus.Print(err)
		return nil, 0, err
	}

	for i, book := range books {
		books[i].Fav = isFav(userFavs, book)
	}

	return books, count, nil
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
