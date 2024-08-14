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

func AddBookToCollection(userID, collectionID, bookID int64) error {
	// Check if the collection belongs to the user
	var collection models.BookCollection
	err := db.Model(&collection).
		Where("id = ? AND user_id = ?", collectionID, userID).
		Select()
	if err != nil {
		return err
	}

	// Fetch the current maximum position in the collection
	var maxPosition int
	err = db.Model((*models.BookCollectionBook)(nil)).
		Where("book_collection_id = ?", collectionID).
		ColumnExpr("COALESCE(MAX(position), 0)").
		Select(&maxPosition)
	if err != nil {
		return err
	}

	// Add the book to the collection with the next position
	collectionBook := models.BookCollectionBook{
		BookCollectionID: collectionID,
		BookID:           bookID,
		Position:         maxPosition + 1,
	}
	_, err = db.Model(&collectionBook).Insert()
	if err != nil {
		return err
	}

	return nil
}

func RemoveBookFromCollection(userID, collectionID, bookID int64) error {
	// Start a transaction
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Fetch all books in the collection ordered by their current position
	var books []models.BookCollectionBook
	err = tx.Model(&books).
		Where("book_collection_id = ?", collectionID).
		Order("position ASC").
		Select()
	if err != nil {
		return err
	}

	// Delete the specified book from the collection
	_, err = tx.Model(&models.BookCollectionBook{}).
		Where("book_collection_id = ? AND book_id = ?", collectionID, bookID).
		Delete()
	if err != nil {
		return err
	}

	// Update the positions of the remaining books
	position := 1
	for _, book := range books {
		if book.BookID != bookID {
			_, err = tx.Model(&book).
				Set("position = ?", position).
				Where("id = ?", book.ID).
				Update()
			if err != nil {
				return err
			}
			position++
		}
	}

	// Commit the transaction
	err = tx.Commit()
	if err != nil {
		return err
	}

	return nil
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

// UpdateBookPositionInCollection updates the position of a book in a collection
func UpdateBookPositionInCollection(userID, collectionID, bookID int64, newPosition int) error {
	// Start a transaction
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Fetch the current position of the book and ensure the collection belongs to the user
	var currentBook models.BookCollectionBook
	err = tx.Model(&currentBook).
		Join("JOIN book_collections bc ON bc.id = book_collection_books.book_collection_id").
		Where("book_collection_books.book_collection_id = ? AND book_collection_books.book_id = ? AND bc.user_id = ?", collectionID, bookID, userID).
		Select()
	if err != nil {
		return err
	}

	// Determine the direction of the move
	if newPosition < currentBook.Position {
		// Moving up
		_, err = tx.Model((*models.BookCollectionBook)(nil)).
			Set("position = position + 1").
			Where("book_collection_id = ? AND position >= ? AND position < ?", collectionID, newPosition, currentBook.Position).
			Update()
	} else if newPosition > currentBook.Position {
		// Moving down
		_, err = tx.Model((*models.BookCollectionBook)(nil)).
			Set("position = position - 1").
			Where("book_collection_id = ? AND position <= ? AND position > ?", collectionID, newPosition, currentBook.Position).
			Update()
	}
	if err != nil {
		return err
	}

	// Set the new position for the moved book
	_, err = tx.Model(&currentBook).
		Set("position = ?", newPosition).
		Where("id = ?", currentBook.ID).
		Update()
	if err != nil {
		return err
	}

	// Commit the transaction
	err = tx.Commit()
	if err != nil {
		return err
	}

	return nil
}
