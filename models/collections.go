package models

import (
	"encoding/json"
	"time"
)

// Match statuses for book_collection_items.
const (
	MatchStatusAutoMatched = "auto_matched"
	MatchStatusAmbiguous   = "ambiguous"
	MatchStatusManual      = "manual"
	MatchStatusNotFound    = "not_found"
	MatchStatusIgnored     = "ignored"
)

// Import statuses for curated book_collections.
const (
	ImportStatusImporting = "importing"
	ImportStatusCompleted = "completed"
	ImportStatusFailed    = "failed"
)

// MatchCandidate is one (book_id, score) pair returned by similarity search
// during curated collection import.
type MatchCandidate struct {
	BookID int64   `json:"book_id"`
	Score  float32 `json:"score"`
}

// CollectionImportStats are aggregated counters returned to the admin and stored
// in book_collections.import_stats. Matched counts both auto-matched results and
// manual-from-cache hits. Processed/Total drive the live progress bar shown by
// the admin UI while import_status is still 'importing'. AIProgress carries the
// streaming state of an in-flight LLM resolution.
type CollectionImportStats struct {
	Matched    int                  `json:"matched"`
	Ambiguous  int                  `json:"ambiguous"`
	NotFound   int                  `json:"not_found"`
	Processed  int                  `json:"processed"`
	Total      int                  `json:"total"`
	AIProgress *AIResolveProgress   `json:"ai_progress,omitempty"`
}

// AIResolveProgress is a snapshot of an in-flight LLM-driven loop. Updated after
// each LLM call so the admin UI can poll it via /status. Mode lets the UI label
// the progress correctly without two separate jsonb fields.
type AIResolveProgress struct {
	Running   bool         `json:"running"`
	Mode      string       `json:"mode,omitempty"` // "resolve_ambiguous" | "search_not_found"
	Processed int          `json:"processed"`
	Total     int          `json:"total"`
	Resolved  int          `json:"resolved"`
	Recent    []AIDecision `json:"recent"` // last decisions, most recent last
	StartedAt time.Time    `json:"started_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

// AIDecision is one LLM verdict on one ambiguous item.
type AIDecision struct {
	ItemID        int64  `json:"item_id"`
	ExternalTitle string `json:"external_title"`
	Action        string `json:"action"` // "resolved" | "skipped"
	BookID        *int64 `json:"book_id,omitempty"`
	BookTitle     string `json:"book_title,omitempty"`
}

// PersistedCollectionItem is the storage shape for one curated collection item:
// what the import service hands to the repository for INSERT.
type PersistedCollectionItem struct {
	Position       int
	ExternalTitle  string
	ExternalAuthor string
	ExternalExtra  json.RawMessage
	BookID         *int64
	MatchStatus    string
	MatchScore     float32
}

// CollectionVote struct for collection votes
type CollectionVote struct {
	tableName    struct{}  `pg:"collection_votes,discard_unknown_columns" json:"-"`
	ID           int64     `pg:"id,pk" json:"id"`
	UserID       int64     `pg:"user_id" json:"user_id"`
	CollectionID int64     `pg:"collection_id" json:"collection_id"`
	Vote         bool      `pg:"vote,use_zero" json:"vote"`
	CreatedAt    time.Time `pg:"created_at" json:"created_at"`
	UpdatedAt    time.Time `pg:"updated_at" json:"updated_at"`
}

// BookCollection struct for book_collections table.
// UserID is nullable: curated admin collections have no owner user.
type BookCollection struct {
	tableName          struct{}               `pg:"book_collections,discard_unknown_columns" json:"-"`
	ID                 int64                  `pg:"id,pk" json:"id"`
	UserID             *int64                 `pg:"user_id" json:"user_id,omitempty"`
	User               *User                  `pg:"rel:has-one,fk:user_id" json:"-"`
	Name               string                 `pg:"name" json:"name"`
	IsPublic           bool                   `pg:"is_public,use_zero" json:"is_public"`
	IsCurated          bool                   `pg:"is_curated,use_zero" json:"is_curated"`
	SourceURL          string                 `pg:"source_url,use_zero" json:"source_url,omitempty"`
	ImportStatus       string                 `pg:"import_status,use_zero" json:"import_status,omitempty"`
	ImportError        string                 `pg:"import_error,use_zero" json:"import_error,omitempty"`
	ImportedAt         *time.Time             `pg:"imported_at" json:"imported_at,omitempty"`
	ImportStats        *CollectionImportStats `pg:"import_stats,type:jsonb" json:"import_stats,omitempty"`
	CreatedAt          time.Time              `pg:"created_at" json:"created_at"`
	UpdatedAt          time.Time              `pg:"updated_at" json:"updated_at"`
	Books              []Book                 `pg:"many2many:book_collection_books,join_fk:book_id" json:"-"`
	BookIsInCollection bool                   `pg:"-" json:"book_is_in_collection"`
	BookIDs            []int64                `pg:"-" json:"book_ids"`
	VoteCount          int                    `pg:"-" json:"vote_count"`
}

// BookCollectionBook struct for many-to-many relation between books and book collections
type BookCollectionBook struct {
	tableName        struct{}  `pg:"book_collection_books,discard_unknown_columns" json:"-"`
	ID               int64     `pg:"id,pk" json:"id"`
	BookCollectionID int64     `pg:"book_collection_id" json:"book_collection_id"`
	BookID           int64     `pg:"book_id" json:"book_id"`
	Position         int       `pg:"position,default:0" json:"position"`
	CreatedAt        time.Time `pg:"created_at,default:now()" json:"created_at"`
	UpdatedAt        time.Time `pg:"updated_at,default:now()" json:"updated_at"`
}

// BookCollectionItem is one row of a curated collection import: either a matched
// local book (book_id set) or an external title we could not resolve (book_id NULL).
type BookCollectionItem struct {
	tableName      struct{}        `pg:"book_collection_items,discard_unknown_columns" json:"-"`
	ID             int64           `pg:"id,pk" json:"id"`
	CollectionID   int64           `pg:"collection_id" json:"collection_id"`
	BookID         *int64          `pg:"book_id" json:"book_id,omitempty"`
	ExternalTitle  string          `pg:"external_title" json:"external_title"`
	ExternalAuthor string          `pg:"external_author,use_zero" json:"external_author"`
	ExternalExtra  json.RawMessage `pg:"external_extra,type:jsonb" json:"external_extra,omitempty"`
	MatchStatus    string          `pg:"match_status" json:"match_status"`
	MatchScore     float32         `pg:"match_score,use_zero" json:"match_score"`
	Position       int             `pg:"position,use_zero" json:"position"`
	CreatedAt      time.Time       `pg:"created_at" json:"created_at"`
	UpdatedAt      time.Time       `pg:"updated_at" json:"updated_at"`
}

// BookMatchDecision caches a manual «(normalized author, normalized title) → book_id»
// resolution so that the same external pair encountered in another collection import
// is matched automatically without admin intervention.
type BookMatchDecision struct {
	tableName        struct{}  `pg:"book_match_decisions,discard_unknown_columns" json:"-"`
	ID               int64     `pg:"id,pk" json:"id"`
	AuthorNorm       string    `pg:"author_norm" json:"author_norm"`
	TitleNorm        string    `pg:"title_norm" json:"title_norm"`
	BookID           int64     `pg:"book_id" json:"book_id"`
	DecidedByUserID  *int64    `pg:"decided_by_user_id" json:"decided_by_user_id,omitempty"`
	CreatedAt        time.Time `pg:"created_at" json:"created_at"`
}
