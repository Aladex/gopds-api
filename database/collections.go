package database

import (
	"github.com/go-pg/pg/v10"
	"gopds-api/models"
)

// GetAllPublicCollections returns all public collections
func GetAllPublicCollections(db *pg.DB) ([]models.BookCollection, error) {
	var collections []models.BookCollection
	err := db.Model(&collections).
		Where("is_public = ?", true).
		Select()
	if err != nil {
		return nil, err
	}
	return collections, nil
}

// GetPrivateCollections returns all private collections by user ID
func GetPrivateCollections(db *pg.DB, userID int64) ([]models.BookCollection, error) {
	var collections []models.BookCollection
	err := db.Model(&collections).
		Where("user_id = ?", userID).
		Select()
	if err != nil {
		return nil, err
	}
	return collections, nil
}
