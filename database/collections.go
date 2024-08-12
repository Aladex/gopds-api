package database

import (
	"gopds-api/models"
)

// GetAllPublicCollections returns all public collections
func GetAllPublicCollections(filters models.CollectionFilters) ([]models.BookCollection, error) {
	var collections []models.BookCollection
	query := db.Model(&collections).Where("is_public = ?", true)

	if filters.Limit > 0 {
		query = query.Limit(filters.Limit)
	}
	if filters.Offset > 0 {
		query = query.Offset(filters.Offset)
	}

	err := query.Select()
	return collections, err
}

// GetPrivateCollections returns all private collections by user ID
func GetPrivateCollections(userID int64) ([]models.BookCollection, error) {
	var collections []models.BookCollection
	err := db.Model(&collections).
		Where("user_id = ?", userID).
		Select()
	if err != nil {
		return nil, err
	}
	return collections, nil
}

// CreateCollection creates a new collection
func CreateCollection(collection models.BookCollection) (models.BookCollection, error) {
	_, err := db.Model(&collection).Insert()
	return collection, err
}
