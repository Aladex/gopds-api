package database

import (
	"fmt"
	"github.com/go-pg/pg/v10"
	"github.com/go-pg/pg/v10/orm"
	"github.com/sirupsen/logrus"
	"gopds-api/models"
	"time"
)

func GetCollections(filters models.CollectionFilters, userID int64, isPublic bool) ([]models.BookCollection, error) {
	var collections []models.BookCollection
	query := db.Model(&collections).
		Column("book_collection.*").
		ColumnExpr("COALESCE(SUM(CASE WHEN cv.vote THEN 1 ELSE -1 END), 0) AS vote_count").
		Join("LEFT JOIN collection_votes AS cv ON cv.collection_id = book_collection.id").
		Group("book_collection.id")

	// Явно указываем, к какой таблице относится user_id
	if isPublic {
		query = query.Where("book_collection.is_public = ?", true)
	} else {
		query = query.Where("book_collection.user_id = ?", userID)
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

	// Убедитесь, что счетчик голосов равен 0, если голоса отсутствуют
	for i := range collections {
		if collections[i].VoteCount == -1 {
			collections[i].VoteCount = 0
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
		logrus.Errorf("Failed to begin transaction: %v", err)
		return err
	}
	defer tx.Rollback()

	// Проверяем, что коллекция принадлежит пользователю
	var collectionOwnerID int64
	err = tx.Model((*models.BookCollection)(nil)).
		Column("user_id").
		Where("id = ?", collectionID).
		Select(&collectionOwnerID)
	if err != nil {
		logrus.Errorf("Failed to fetch collection owner: %v", err)
		return err
	}

	if collectionOwnerID != userID {
		logrus.Errorf("User (ID: %d) does not own the collection (ID: %d)", userID, collectionID)
		return fmt.Errorf("user does not own the collection")
	}

	// Fetch the current position of the book within the collection
	var currentBook models.BookCollectionBook
	err = tx.Model(&currentBook).
		Where("book_collection_id = ? AND book_id = ?", collectionID, bookID).
		Select()
	if err != nil {
		logrus.Errorf("Failed to fetch current book position: %v", err)
		return err
	}

	// Determine the direction of the move and update positions accordingly
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
		logrus.Errorf("Failed to update positions of other books: %v", err)
		return err
	}

	// Set the new position for the moved book
	_, err = tx.Model(&currentBook).
		Set("position = ?", newPosition).
		Where("id = ?", currentBook.ID).
		Update()
	if err != nil {
		logrus.Errorf("Failed to set new position for book: %v", err)
		return err
	}

	// Commit the transaction
	err = tx.Commit()
	if err != nil {
		logrus.Errorf("Failed to commit transaction: %v", err)
		return err
	}

	return nil
}

// UpdateCollection updates the name and public status of a collection
func UpdateCollection(userID, collectionID int64, name string, isPublic bool) (models.BookCollection, error) {
	var collection models.BookCollection
	err := db.Model(&collection).
		Where("id = ? AND user_id = ?", collectionID, userID).
		Select()
	if err != nil {
		return collection, err
	}

	collection.Name = name
	collection.IsPublic = isPublic
	collection.UpdatedAt = time.Now()

	_, err = db.Model(&collection).
		WherePK().
		Update()
	return collection, err
}

// GetCollection retrieves the details of a specified collection
func GetCollection(userID, collectionID int64) (models.BookCollection, error) {
	var collection models.BookCollection
	err := db.Model(&collection).
		Where("id = ? AND user_id = ?", collectionID, userID).
		Select()
	return collection, err
}

// VoteCollection adds or updates a user's vote for a collection
func VoteCollection(userID, collectionID int64, vote bool) error {
	var existingVote models.CollectionVote
	err := db.Model(&existingVote).
		Where("user_id = ? AND collection_id = ?", userID, collectionID).
		Select()
	if err != nil && err != pg.ErrNoRows {
		return err
	}

	if existingVote.ID == 0 {
		// Insert new vote
		newVote := models.CollectionVote{
			UserID:       userID,
			CollectionID: collectionID,
			Vote:         vote,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}
		_, err = db.Model(&newVote).Insert()
	} else {
		// Update existing vote
		existingVote.Vote = vote
		existingVote.UpdatedAt = time.Now()
		_, err = db.Model(&existingVote).WherePK().Update()
	}
	return err
}
