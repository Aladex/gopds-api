package database

import (
	"errors"
	"fmt"
	"github.com/go-pg/pg/v10/orm"
	"gopds-api/logging"
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

func GetCatalogs(scanned bool) ([]string, error) {
	var catalogs []string
	err := db.Model(&models.Catalog{}).Column("cat_name").Where("is_scanned = ?", scanned).Select(&catalogs)
	if err != nil {
		return nil, err
	}
	return catalogs, nil
}

func AddCatalog(catalog models.Catalog) error {
	_, err := db.Model(&catalog).Insert()
	if err != nil {
		return err
	}
	return nil
}

// GetLanguages возвращает список языков из БД
func GetLanguages() models.Languages {
	var langRes models.Languages
	err := db.Model(&models.Book{}).
		Column("lang").
		ColumnExpr("count(*) AS language_count").
		Group("lang").
		OrderExpr("language_count DESC").
		Select(&langRes)

	if err != nil {
		logging.CustomLog.Print(err)
		return nil
	}
	return langRes
}

// AddSeries returns an id of series after select or after insert if not exists
func AddCover(cover models.Cover) error {
	_, err := db.Model(&cover).Insert()
	if err != nil {
		return err
	}
	return nil
}

func AddAuthorBook(book models.Book) {
	for _, a := range book.Authors {
		_, err := db.Model(&models.OrderToAuthor{
			AuthorID: a.ID,
			BookID:   book.ID,
		}).Insert()
		if err != nil {
			logging.CustomLog.Println(err)
		}
	}
}

// AddSeries returns an id of series after select or after insert if not exists
func AddSeries(series models.Series) (models.Series, error) {
	_, err := db.Model(&series).
		Where("ser = ?ser").
		SelectOrInsert()
	if err != nil {
		return models.Series{}, err
	}
	return series, nil
}

func GetCover(book int64) (models.Cover, error) {
	var cover models.Cover
	err := db.Model(&cover).Where("book_id = ?", book).First()
	if err != nil {
		logging.CustomLog.Println(errors.New(fmt.Sprintf("no cover for book_id: %d", book)))
		return cover, err
	}
	return cover, nil
}

func UpdateBookCover(book models.Book) error {
	var bookId int64
	err := db.Model(&models.Book{}).
		Column("id").
		Where("filename = ?", book.FileName).
		Where("path = ?", book.Path).
		Order("id ASC").
		Select(&bookId)
	if err != nil {
		logging.CustomLog.Print(err)
		return err
	}
	for _, c := range book.Covers {
		c.BookID = bookId
		err = AddCover(*c)
		if err != nil {
			logging.CustomLog.Print(err)
		}
	}
	return nil
}

// AddBook
func AddBook(book models.Book) error {
	for ai, author := range book.Authors {
		a, err := AddAuthor(author)
		if err != nil {
			logging.CustomLog.Print(err)
			return nil
		}
		book.Authors[ai] = a
	}

	_, err := db.Model(&book).Returning("id").Insert()
	if err != nil {
		fmt.Println(book)
		return err
	}

	for _, series := range book.Series {
		s, err := AddSeries(*series)
		if err != nil {
			logging.CustomLog.Print(err)
			continue
		}
		serieBook := models.OrderToSeries{
			SerNo:    s.SerNo,
			SeriesID: s.ID,
			BookID:   book.ID,
		}
		_, err = db.Model(&serieBook).Insert()
		if err != nil {
			logging.CustomLog.Print(err)
			continue
		}
	}

	for _, c := range book.Covers {
		c.BookID = book.ID
		err = AddCover(*c)
		if err != nil {
			logging.CustomLog.Print(err)
		}
	}
	AddAuthorBook(book)
	return nil
}

// GetBooks Возвращает список книг и общее количество при селекте
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
		WhereGroup(func(q *orm.Query) (*orm.Query, error) {
			if filters.Fav {
				var booksIds []int64
				err = db.Model(&models.UserToBook{}).
					Column("book_id").
					Where("user_id = ?", userID).
					Select(&booksIds)
				if err == nil {
					if len(booksIds) > 0 {
						q = q.WhereIn("id IN (?)", booksIds)
						exprArr := []string{}
						for _, bid := range booksIds {
							exprArr = append(exprArr, fmt.Sprintf("id=%d ASC", bid))
						}
						q.OrderExpr(strings.Join(exprArr, ", "))
					}
				}
			}
			if filters.Title != "" && filters.Author == 0 {
				q = q.Where("title % ?", filters.Title).
					OrderExpr("title <-> ? ASC", filters.Title)
			} else {
				q = q.Order("id DESC")
			}
			if filters.Lang != "" {
				q = q.Where("lang = ?", filters.Lang)
			}
			if filters.UnApproved {
				q = q.Where("approved = false")
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
		logging.CustomLog.Print(err)
		return nil, 0, err
	}

	for i, book := range books {
		books[i].Fav = isFav(userFavs, book)
	}

	return books, count, nil
}

// GetBook возвращает информацию по книге для того, чтобы вытащить ее из архива
func GetBook(bookID int64) (models.Book, error) {
	book := &models.Book{ID: bookID}
	err := db.Model(book).Select()
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

// FavBook добавляет книгу в избранное
func FavBook(userID int64, fav models.FavBook) (bool, error) {
	book := &models.Book{ID: fav.BookID}
	err := db.Model(book).Select()
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

// UpdateBook
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
