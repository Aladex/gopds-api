package database

import (
	"fmt"
	"github.com/go-pg/pg/v9/orm"
	"gopds-api/models"
	"strings"
)

// GetAuthors возвращает найденных авторов и счетчик количества найденного
func GetAuthors(filters models.AuthorFilters) ([]models.Author, int, error) {
	authors := []models.Author{}
	authorSlice := strings.Split(filters.Author, " ")
	count, err := db.Model(&authors).
		WhereGroup(func(q *orm.Query) (*orm.Query, error) {
			for _, t := range authorSlice {
				likeAuthor := fmt.Sprintf("%%%s%%", t)
				q = q.Where("full_name ILIKE ?", likeAuthor)
			}
			return q, nil
		}).
		Limit(filters.Limit).
		Offset(filters.Offset).
		Order("full_name ASC").
		SelectAndCount()
	if err != nil {
		return nil, 0, err
	}
	return authors, count, nil
}
