package database

import (
	"gopds-api/models"
)

// The trigram index on authors is built over lower(full_name):
//
//	opds_catalog_author_full_name_trgm_idx gin (lower(full_name) gin_trgm_ops)
//
// A predicate has to name the same expression to reach it. Written against the
// bare column, these searches matched no index and fell back to scanning all
// 176k authors on every keystroke — 238ms where the index answers in 12.
//
// Lowering changes nothing else: pg_trgm folds case when it builds trigrams, so
// `%` was already case-insensitive. The results are identical, verified across
// several names.
const authorNameExpr = "lower(full_name)"

// GetAuthors returns an array of authors and total count of authors
func GetAuthors(filters models.AuthorFilters) ([]models.Author, int, error) {
	var authors []models.Author

	query := db.Model(&authors).
		Where(authorNameExpr+" % lower(?)", filters.Author)

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
		OrderExpr(authorNameExpr+" <-> lower(?) ASC", filters.Author).
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
