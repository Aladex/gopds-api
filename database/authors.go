package database

import (
	"gopds-api/models"
)

// GetAuthor returns an object of author with full_name
func GetAuthor(id int64) (models.Author, error) {
	var author models.Author
	err := db.Model(&author).Where("id = ?", id).Select()
	if err != nil {
		return models.Author{}, err
	}
	return author, nil
}
