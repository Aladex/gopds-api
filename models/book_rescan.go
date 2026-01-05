package models

import (
	"encoding/json"
	"time"
)

// BookRescanPending stores pending book rescan results awaiting admin approval
type BookRescanPending struct {
	tableName       struct{}  `pg:"book_rescan_pending"`
	ID              int64     `json:"id" pg:"id,pk"`
	BookID          int64     `json:"book_id" pg:"book_id,notnull"`
	Title           string    `json:"title" pg:"title"`
	Annotation      string    `json:"annotation" pg:"annotation"`
	Lang            string    `json:"lang" pg:"lang"`
	DocDate         string    `json:"docdate" pg:"docdate"`
	CoverData       []byte    `json:"-" pg:"cover_data"`
	CoverUpdated    bool      `json:"cover_updated" pg:"cover_updated"`
	AuthorsJSON     []byte    `json:"-" pg:"authors_json"`
	SeriesJSON      []byte    `json:"-" pg:"series_json"`
	TagsJSON        []byte    `json:"-" pg:"tags_json"`
	CreatedAt       time.Time `json:"created_at" pg:"created_at,notnull,default:now()"`
	UpdatedAt       time.Time `json:"updated_at" pg:"updated_at,notnull,default:now()"`
	CreatedByUserID int64     `json:"created_by_user_id" pg:"created_by_user_id"`
}

// RescanAuthor represents author info in rescan preview
type RescanAuthor struct {
	ID   int64  `json:"id,omitempty"`
	Name string `json:"name"`
}

// RescanSeries represents series info in rescan preview
type RescanSeries struct {
	ID    int64  `json:"id,omitempty"`
	Title string `json:"title"`
	Index string `json:"index"`
}

// BookRescanOldValues contains existing book values
type BookRescanOldValues struct {
	Title      string         `json:"title"`
	Annotation string         `json:"annotation"`
	Lang       string         `json:"lang"`
	DocDate    string         `json:"docdate"`
	Authors    []RescanAuthor `json:"authors"`
	Series     *RescanSeries  `json:"series"`
	Tags       []string       `json:"tags"`
	HasCover   bool           `json:"has_cover"`
}

// BookRescanNewValues contains parsed FB2 metadata
type BookRescanNewValues struct {
	Title      string         `json:"title"`
	Annotation string         `json:"annotation"`
	Lang       string         `json:"lang"`
	DocDate    string         `json:"docdate"`
	Authors    []RescanAuthor `json:"authors"`
	Series     *RescanSeries  `json:"series"`
	Tags       []string       `json:"tags"`
	HasCover   bool           `json:"has_cover"`
}

// RescanPreview is API response showing old vs new values
type RescanPreview struct {
	BookID          int64                `json:"book_id"`
	Old             *BookRescanOldValues `json:"old"`
	New             *BookRescanNewValues `json:"new"`
	Diff            []string             `json:"diff"`
	PendingRescanID int64                `json:"pending_rescan_id"`
}

// RescanApprovalRequest is the body for approve/reject
type RescanApprovalRequest struct {
	BookID int64  `json:"book_id" binding:"required"`
	Action string `json:"action" binding:"required,oneof=approve reject"`
}

// RescanApprovalResponse is returned after approve/reject
type RescanApprovalResponse struct {
	Success bool                 `json:"success"`
	Message string               `json:"message"`
	BookID  int64                `json:"book_id"`
	Action  string               `json:"action"`
	Updated *BookRescanNewValues `json:"updated,omitempty"` // Only if approved
}

// Helper to unmarshal JSON fields

func (p *BookRescanPending) GetAuthors() []RescanAuthor {
	if len(p.AuthorsJSON) == 0 {
		return []RescanAuthor{}
	}
	var authors []RescanAuthor
	_ = json.Unmarshal(p.AuthorsJSON, &authors)
	return authors
}

func (p *BookRescanPending) GetSeries() *RescanSeries {
	if len(p.SeriesJSON) == 0 {
		return nil
	}
	var series *RescanSeries
	_ = json.Unmarshal(p.SeriesJSON, &series)
	return series
}

func (p *BookRescanPending) GetTags() []string {
	if len(p.TagsJSON) == 0 {
		return []string{}
	}
	var tags []string
	_ = json.Unmarshal(p.TagsJSON, &tags)
	return tags
}

// SetAuthors marshals authors to JSON
func (p *BookRescanPending) SetAuthors(authors []RescanAuthor) error {
	data, err := json.Marshal(authors)
	if err != nil {
		return err
	}
	p.AuthorsJSON = data
	return nil
}

// SetSeries marshals series to JSON
func (p *BookRescanPending) SetSeries(series *RescanSeries) error {
	if series == nil {
		p.SeriesJSON = nil
		return nil
	}
	data, err := json.Marshal(series)
	if err != nil {
		return err
	}
	p.SeriesJSON = data
	return nil
}

// SetTags marshals tags to JSON
func (p *BookRescanPending) SetTags(tags []string) error {
	data, err := json.Marshal(tags)
	if err != nil {
		return err
	}
	p.TagsJSON = data
	return nil
}
