package database

import (
	"gopds-api/models"
)

// GetAuthors возвращает найденных авторов и счетчик количества найденного
func GetAuthors(filters models.AuthorFilters) ([]models.Author, int, error) {
	authors := []models.Author{}
	count, err := db.Model(&authors).
		Where("full_name % ?", filters.Author).
		Limit(filters.Limit).
		Offset(filters.Offset).
		OrderExpr("full_name <-> ? ASC", filters.Author).
		SelectAndCount()
	if err != nil {
		return nil, 0, err
	}
	return authors, count, nil
}

// GetAuthor returns an object of author with full_name
func GetAuthor(filter models.AuthorRequest) (models.Author, error) {
	var author models.Author
	err := db.Model(&author).Where("id = ?", filter.ID).Select()
	if err != nil {
		return models.Author{}, err
	}
	return author, nil
}

// AddAuthor returns an id of author after select or after insert if not exists
func AddAuthor(author models.Author) (models.Author, error) {
	_, err := db.Model(&author).
		Where("full_name = ?full_name").
		SelectOrInsert()
	if err != nil {
		return models.Author{}, err
	}
	return author, nil
}
