package models

// AuthorFilters filters for authors list
type AuthorFilters struct {
	Limit  int    `form:"limit" json:"limit"`
	Offset int    `form:"offset" json:"offset"`
	Author string `form:"author" json:"author"`
	Lang   string `form:"lang" json:"lang"`
}

// AuthorRequest request for an object of author for search bar
type AuthorRequest struct {
	ID int64 `json:"author_id" form:"author_id"`
}
