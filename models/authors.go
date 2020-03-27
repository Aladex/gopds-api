package models

// AuthorFilters фильтры для поиска авторов
type AuthorFilters struct {
	Limit  int    `form:"limit" json:"limit"`
	Offset int    `form:"offset" json:"offset"`
	Author string `form:"author" json:"author"`
}
