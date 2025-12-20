package database

import (
	"gopds-api/models"
)

// GetAuthors returns an array of authors and total count of authors
func GetAuthors(filters models.AuthorFilters) ([]models.Author, int, error) {
	var authors []models.Author

	query := db.Model(&authors).
		Where("full_name % ?", filters.Author)

	// Filter by language if specified
	if filters.Lang != "" {
		query = query.
			Join("JOIN opds_catalog_bauthor AS ba ON ba.author_id = author.id").
			Join("JOIN opds_catalog_book AS b ON b.id = ba.book_id").
			Where("b.lang = ?", filters.Lang).
			Where("b.approved = true").
			Group("author.id")
	}

	count, err := query.
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
	fullName := author.FullName
	if fullName == "" {
		fullName = "Автор Неизвестен"
	}
	_, err := db.Model(&author).
		Where("full_name = ?", fullName).
		SelectOrInsert()
	if err != nil {
		return models.Author{}, err
	}
	return author, nil
}
