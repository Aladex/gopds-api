package models

import (
	"encoding/json"
	"time"
)

// BookRescanPending stores pending book rescan results awaiting admin approval
type BookRescanPending struct {
	tableName       struct{}        `pg:"book_rescan_pending"`
	ID              int64           `json:"id" pg:"id,pk"`
	BookID          int64           `json:"book_id" pg:"book_id,notnull"`
	Title           string          `json:"title" pg:"title"`
	Annotation      string          `json:"annotation" pg:"annotation"`
	Lang            string          `json:"lang" pg:"lang"`
	DocDate         string          `json:"docdate" pg:"docdate"`
	CoverData       []byte          `json:"-" pg:"cover_data"`
	CoverUpdated    bool            `json:"cover_updated" pg:"cover_updated"`
	AuthorsJSON     json.RawMessage `json:"-" pg:"authors_json,type:jsonb"`
	SeriesJSON      json.RawMessage `json:"-" pg:"series_json,type:jsonb"`
	TagsJSON        json.RawMessage `json:"-" pg:"tags_json,type:jsonb"`
	CreatedAt       time.Time       `json:"created_at" pg:"created_at,notnull,default:now()"`
	UpdatedAt       time.Time       `json:"updated_at" pg:"updated_at,notnull,default:now()"`
	CreatedByUserID int64           `json:"created_by_user_id" pg:"created_by_user_id"`
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
	Action string `json:"action" binding:"required,oneof=approve reject"`
	// Fields to update (nil = update by default, true/false = explicit choice)
	UpdateTitle      *bool `json:"update_title,omitempty"`
	UpdateAnnotation *bool `json:"update_annotation,omitempty"`
	UpdateLang       *bool `json:"update_lang,omitempty"`
	UpdateDocDate    *bool `json:"update_docdate,omitempty"`
	UpdateAuthors    *bool `json:"update_authors,omitempty"`
	UpdateSeries     *bool `json:"update_series,omitempty"`
	UpdateCover      *bool `json:"update_cover,omitempty"`
	UpdateTags       *bool `json:"update_tags,omitempty"`
}

// SetDefaults sets all unspecified fields to true (update all by default)
func (r *RescanApprovalRequest) SetDefaults() {
	t := true
	if r.UpdateTitle == nil {
		r.UpdateTitle = &t
	}
	if r.UpdateAnnotation == nil {
		r.UpdateAnnotation = &t
	}
	if r.UpdateLang == nil {
		r.UpdateLang = &t
	}
	if r.UpdateDocDate == nil {
		r.UpdateDocDate = &t
	}
	if r.UpdateAuthors == nil {
		r.UpdateAuthors = &t
	}
	if r.UpdateSeries == nil {
		r.UpdateSeries = &t
	}
	if r.UpdateCover == nil {
		r.UpdateCover = &t
	}
	if r.UpdateTags == nil {
		r.UpdateTags = &t
	}
}

// GetUpdatedFields returns the list of field names that will be updated
func (r *RescanApprovalRequest) GetUpdatedFields() []string {
	var fields []string
	if ShouldUpdate(r.UpdateTitle) {
		fields = append(fields, "title")
	}
	if ShouldUpdate(r.UpdateAnnotation) {
		fields = append(fields, "annotation")
	}
	if ShouldUpdate(r.UpdateLang) {
		fields = append(fields, "lang")
	}
	if ShouldUpdate(r.UpdateDocDate) {
		fields = append(fields, "docdate")
	}
	if ShouldUpdate(r.UpdateAuthors) {
		fields = append(fields, "authors")
	}
	if ShouldUpdate(r.UpdateSeries) {
		fields = append(fields, "series")
	}
	if ShouldUpdate(r.UpdateCover) {
		fields = append(fields, "cover")
	}
	if ShouldUpdate(r.UpdateTags) {
		fields = append(fields, "tags")
	}
	return fields
}

// ShouldUpdate returns true if a field should be updated (nil or true)
func ShouldUpdate(flag *bool) bool {
	if flag == nil {
		return true // Default: update if not specified
	}
	return *flag
}

// RescanApprovalResponse is returned after approve/reject
type RescanApprovalResponse struct {
	Success       bool                 `json:"success"`
	Message       string               `json:"message"`
	BookID        int64                `json:"book_id"`
	Action        string               `json:"action"`
	Updated       *BookRescanNewValues `json:"updated,omitempty"`        // Only if approved
	UpdatedFields []string             `json:"updated_fields,omitempty"` // Which fields were applied
	SkippedFields []string             `json:"skipped_fields,omitempty"` // Which fields were not applied
}

// Helper to unmarshal JSON fields

func (p *BookRescanPending) GetAuthors() []RescanAuthor {
	if len(p.AuthorsJSON) == 0 {
		return []RescanAuthor{}
	}
	var authors []RescanAuthor
	_ = json.Unmarshal([]byte(p.AuthorsJSON), &authors)
	return authors
}

func (p *BookRescanPending) GetSeries() *RescanSeries {
	if len(p.SeriesJSON) == 0 {
		return nil
	}
	var series *RescanSeries
	_ = json.Unmarshal([]byte(p.SeriesJSON), &series)
	return series
}

func (p *BookRescanPending) GetTags() []string {
	if len(p.TagsJSON) == 0 {
		return []string{}
	}
	var tags []string
	_ = json.Unmarshal([]byte(p.TagsJSON), &tags)
	return tags
}

// SetAuthors marshals authors to JSON
func (p *BookRescanPending) SetAuthors(authors []RescanAuthor) error {
	if authors == nil {
		authors = []RescanAuthor{}
	}
	data, err := json.Marshal(authors)
	if err != nil {
		return err
	}
	p.AuthorsJSON = json.RawMessage(data)
	return nil
}

// SetSeries marshals series to JSON
func (p *BookRescanPending) SetSeries(series *RescanSeries) error {
	if series == nil {
		p.SeriesJSON = json.RawMessage("null")
		return nil
	}
	data, err := json.Marshal(series)
	if err != nil {
		return err
	}
	p.SeriesJSON = json.RawMessage(data)
	return nil
}

// SetTags marshals tags to JSON
func (p *BookRescanPending) SetTags(tags []string) error {
	if tags == nil {
		tags = []string{}
	}
	data, err := json.Marshal(tags)
	if err != nil {
		return err
	}
	p.TagsJSON = json.RawMessage(data)
	return nil
}
