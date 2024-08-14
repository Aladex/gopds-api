package database

import (
	"github.com/go-pg/pg/v10/orm"
	"gopds-api/models"
)

func GetCollections(filters models.CollectionFilters, userID int64, isPublic bool) ([]models.BookCollection, error) {
	var collections []models.BookCollection
	query := db.Model(&collections)

	if isPublic {
		query = query.Where("is_public = ?", true)
	} else {
		query = query.Where("user_id = ?", userID)
	}

	if filters.Limit > 0 {
		query = query.Limit(filters.Limit)
	}
	if filters.Offset > 0 {
		query = query.Offset(filters.Offset)
	}

	err := query.Select()
	if err != nil {
		return nil, err
	}

	if filters.BookID != 0 && !isPublic {
		for i := range collections {
			count, err := db.Model(&models.BookCollectionBook{}).
				Where("book_collection_id = ? AND book_id = ?", collections[i].ID, filters.BookID).
				Count()
			if err != nil {
				return nil, err
			}
			collections[i].BookIsInCollection = count > 0
		}
	}

	return collections, nil
}

// CreateCollection creates a new collection
func CreateCollection(collection models.BookCollection) (models.BookCollection, error) {
	_, err := db.Model(&collection).Insert()
	return collection, err
}

func GetBookCollectionWithIDs(collectionID int64) (models.BookCollection, error) {
	var collection models.BookCollection
	err := db.Model(&collection).
		Column("book_collection.*").
		Relation("Books", func(q *orm.Query) (*orm.Query, error) {
			return q.Column("id"), nil
		}).
		Where("book_collection.id = ?", collectionID).
		Select()
	if err != nil {
		return collection, err
	}

	// Populate BookIDs field
	for _, book := range collection.Books {
		collection.BookIDs = append(collection.BookIDs, book.ID)
	}

	return collection, nil
}

// AddBookToCollection adds a book to a collection if the collection belongs to the user
func AddBookToCollection(userID, collectionID, bookID int64) error {
	// Check if the collection belongs to the user
	var collection models.BookCollection
	err := db.Model(&collection).
		Where("id = ? AND user_id = ?", collectionID, userID).
		Select()
	if err != nil {
		return err
	}

	// Add the book to the collection
	collectionBook := models.BookCollectionBook{
		BookCollectionID: collectionID,
		BookID:           bookID,
	}
	_, err = db.Model(&collectionBook).Insert()
	if err != nil {
		return err
	}

	return nil
}

func RemoveBookFromCollection(userID, collectionID, bookID int64) error {
	_, err := db.Model(&models.BookCollectionBook{}).
		Where("book_collection_id = ? AND book_id = ?", collectionID, bookID).
		Delete()
	return err
}

// GetCollectionsByBookID returns all user collections that contain the book
func GetCollectionsByBookID(userID, bookID int64) ([]models.BookCollection, error) {
	var collections []models.BookCollection
	err := db.Model(&collections).
		Column("book_collection.*").
		Join("JOIN book_collection_books bcb ON book_collection.id = bcb.book_collection_id").
		Where("bcb.book_id = ?", bookID).
		Where("book_collection.user_id = ?", userID).
		Select()
	if err != nil {
		return nil, err
	}
	return collections, nil
}
