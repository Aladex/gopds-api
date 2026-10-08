// name the table; staticcheck sees a field nobody mentions.
//
//lint:file-ignore U1000 tableName is read by go-pg through reflection to
package models

import (
	"encoding/json"
	"time"
)

// The author metadata source layer (migration 23): extraction runs and their
// per-book items and attempts, the immutable metadata snapshots and contributor
// credits they produce, and the pilot approval that gates a full run.
//
// None of these types belongs to the reader model: they are never embedded in
// Book or Author and never reach the public JSON. Enum values mirror the CHECK
// constraints exactly; database/author_metadata_schema_test.go pins both.

// AuthorMetadataRunMode is how much of the catalog a run covers.
type AuthorMetadataRunMode string

const (
	AuthorMetadataRunSmoke        AuthorMetadataRunMode = "smoke"
	AuthorMetadataRunPilotArchive AuthorMetadataRunMode = "pilot_archive"
	AuthorMetadataRunFull         AuthorMetadataRunMode = "full"
)

// AuthorMetadataRunModes lists every mode the schema accepts.
func AuthorMetadataRunModes() []AuthorMetadataRunMode {
	return []AuthorMetadataRunMode{AuthorMetadataRunSmoke, AuthorMetadataRunPilotArchive, AuthorMetadataRunFull}
}

// AuthorMetadataRunStatus is the state of a run.
type AuthorMetadataRunStatus string

const (
	AuthorMetadataRunPending        AuthorMetadataRunStatus = "pending"
	AuthorMetadataRunRunning        AuthorMetadataRunStatus = "running"
	AuthorMetadataRunPaused         AuthorMetadataRunStatus = "paused"
	AuthorMetadataRunCompleted      AuthorMetadataRunStatus = "completed"
	AuthorMetadataRunFailedSystemic AuthorMetadataRunStatus = "failed_systemic"
)

// AuthorMetadataRunStatuses lists every status the schema accepts.
func AuthorMetadataRunStatuses() []AuthorMetadataRunStatus {
	return []AuthorMetadataRunStatus{
		AuthorMetadataRunPending, AuthorMetadataRunRunning, AuthorMetadataRunPaused,
		AuthorMetadataRunCompleted, AuthorMetadataRunFailedSystemic,
	}
}

// IsActive reports whether the status occupies the single active-run slot.
// paused is active: resume continues the same run.
func (s AuthorMetadataRunStatus) IsActive() bool {
	return s == AuthorMetadataRunPending || s == AuthorMetadataRunRunning || s == AuthorMetadataRunPaused
}

// AuthorMetadataRunItemStatus is the state of one book in one run: pending
// until it ends in exactly one terminal status.
type AuthorMetadataRunItemStatus string

const (
	AuthorMetadataRunItemPending             AuthorMetadataRunItemStatus = "pending"
	AuthorMetadataRunItemExtracted           AuthorMetadataRunItemStatus = "extracted"
	AuthorMetadataRunItemExtractedNoAuthor   AuthorMetadataRunItemStatus = "extracted_no_author"
	AuthorMetadataRunItemAlreadyCurrent      AuthorMetadataRunItemStatus = "already_current"
	AuthorMetadataRunItemEntryMissing        AuthorMetadataRunItemStatus = "entry_missing"
	AuthorMetadataRunItemInvalidFB2          AuthorMetadataRunItemStatus = "invalid_fb2"
	AuthorMetadataRunItemUnsupportedEncoding AuthorMetadataRunItemStatus = "unsupported_encoding"
	AuthorMetadataRunItemMetadataParseFailed AuthorMetadataRunItemStatus = "metadata_parse_failed"
)

// AuthorMetadataRunItemTerminalStatuses lists the statuses an item ends in.
func AuthorMetadataRunItemTerminalStatuses() []AuthorMetadataRunItemStatus {
	return []AuthorMetadataRunItemStatus{
		AuthorMetadataRunItemExtracted, AuthorMetadataRunItemExtractedNoAuthor,
		AuthorMetadataRunItemAlreadyCurrent, AuthorMetadataRunItemEntryMissing,
		AuthorMetadataRunItemInvalidFB2, AuthorMetadataRunItemUnsupportedEncoding,
		AuthorMetadataRunItemMetadataParseFailed,
	}
}

// AuthorMetadataRunItemStatuses lists every item status the schema accepts.
func AuthorMetadataRunItemStatuses() []AuthorMetadataRunItemStatus {
	return append([]AuthorMetadataRunItemStatus{AuthorMetadataRunItemPending}, AuthorMetadataRunItemTerminalStatuses()...)
}

// IsTerminal reports whether the item has finished.
func (s AuthorMetadataRunItemStatus) IsTerminal() bool {
	return s != AuthorMetadataRunItemPending
}

// BookMetadataSnapshotOrigin says which path wrote a snapshot: a backfill run,
// or the scan transaction that added a new book.
type BookMetadataSnapshotOrigin string

const (
	BookMetadataSnapshotBackfill BookMetadataSnapshotOrigin = "backfill"
	BookMetadataSnapshotLive     BookMetadataSnapshotOrigin = "live"
)

// BookMetadataSnapshotOrigins lists every origin the schema accepts.
func BookMetadataSnapshotOrigins() []BookMetadataSnapshotOrigin {
	return []BookMetadataSnapshotOrigin{BookMetadataSnapshotBackfill, BookMetadataSnapshotLive}
}

// BookMetadataSnapshotOutcome is the extraction result a snapshot records.
type BookMetadataSnapshotOutcome string

const (
	BookMetadataSnapshotExtracted         BookMetadataSnapshotOutcome = "extracted"
	BookMetadataSnapshotExtractedNoAuthor BookMetadataSnapshotOutcome = "extracted_no_author"
)

// BookMetadataSnapshotOutcomes lists every outcome the schema accepts.
func BookMetadataSnapshotOutcomes() []BookMetadataSnapshotOutcome {
	return []BookMetadataSnapshotOutcome{BookMetadataSnapshotExtracted, BookMetadataSnapshotExtractedNoAuthor}
}

// ContributorRole is the title-info element a credit came from.
type ContributorRole string

const (
	ContributorRoleAuthor     ContributorRole = "author"
	ContributorRoleTranslator ContributorRole = "translator"
)

// ContributorRoles lists every role the schema accepts.
func ContributorRoles() []ContributorRole {
	return []ContributorRole{ContributorRoleAuthor, ContributorRoleTranslator}
}

// AuthorMetadataRun is one extraction run.
type AuthorMetadataRun struct {
	tableName             struct{}                `pg:"author_metadata_run"`
	ID                    int64                   `pg:"id,pk"`
	Mode                  AuthorMetadataRunMode   `pg:"mode"`
	Status                AuthorMetadataRunStatus `pg:"status,default:'pending'"`
	ExtractorVersion      string                  `pg:"extractor_version"`
	NormalizerVersion     string                  `pg:"normalizer_version"`
	SelectorBookIDs       []int64                 `pg:"selector_book_ids,array"`
	SelectorArchive       *string                 `pg:"selector_archive"`
	ItemsTotal            int                     `pg:"items_total,use_zero"`
	ItemsTerminal         int                     `pg:"items_terminal,use_zero"`
	LastErrorClass        *string                 `pg:"last_error_class"`
	CreatedByUserID       *int64                  `pg:"created_by_user_id"`
	CreatedAt             time.Time               `pg:"created_at,default:now()"`
	UpdatedAt             time.Time               `pg:"updated_at,default:now()"`
	StartedAt             *time.Time              `pg:"started_at"`
	ExtractionCompletedAt *time.Time              `pg:"extraction_completed_at"`
	FinishedAt            *time.Time              `pg:"finished_at"`
}

// AuthorMetadataRunItem is one book in one run: the extraction job and the
// history of its result. LeaseOwner and LeaseExpiresAt are set together.
type AuthorMetadataRunItem struct {
	tableName      struct{}                    `pg:"author_metadata_run_item"`
	ID             int64                       `pg:"id,pk"`
	RunID          int64                       `pg:"run_id"`
	BookID         int64                       `pg:"book_id"`
	Status         AuthorMetadataRunItemStatus `pg:"status,default:'pending'"`
	SnapshotID     *int64                      `pg:"snapshot_id"`
	LeaseOwner     *string                     `pg:"lease_owner,type:uuid"`
	LeaseExpiresAt *time.Time                  `pg:"lease_expires_at"`
	AttemptCount   int                         `pg:"attempt_count,use_zero"`
	NextAttemptAt  time.Time                   `pg:"next_attempt_at,default:now()"`
	CreatedAt      time.Time                   `pg:"created_at,default:now()"`
	UpdatedAt      time.Time                   `pg:"updated_at,default:now()"`
	FinishedAt     *time.Time                  `pg:"finished_at"`
}

// AuthorMetadataRunItemAttempt is one append-only attempt of a run item. It
// is finished once, with a terminal outcome, a closed error class, or both.
type AuthorMetadataRunItemAttempt struct {
	tableName  struct{}                     `pg:"author_metadata_run_item_attempt"`
	ID         int64                        `pg:"id,pk"`
	RunItemID  int64                        `pg:"run_item_id"`
	AttemptNo  int                          `pg:"attempt_no"`
	LeaseOwner string                       `pg:"lease_owner,type:uuid"`
	StartedAt  time.Time                    `pg:"started_at,default:now()"`
	FinishedAt *time.Time                   `pg:"finished_at"`
	Outcome    *AuthorMetadataRunItemStatus `pg:"outcome"`
	ErrorClass *string                      `pg:"error_class"`
}

// BookMetadataSnapshot is the immutable result of one extractor version over
// one file version of one book. Only IsCurrent may change after insert.
//
// Optional source fields are pointers: nil is an absent element, a pointer to
// "" a present empty one.
type BookMetadataSnapshot struct {
	tableName             struct{}                    `pg:"book_metadata_snapshot"`
	ID                    int64                       `pg:"id,pk"`
	BookID                int64                       `pg:"book_id"`
	BookMD5               string                      `pg:"book_md5"`
	ExtractorVersion      string                      `pg:"extractor_version"`
	Origin                BookMetadataSnapshotOrigin  `pg:"origin"`
	RunID                 *int64                      `pg:"run_id"`
	Outcome               BookMetadataSnapshotOutcome `pg:"outcome"`
	ArchivePath           string                      `pg:"archive_path"`
	EntryName             string                      `pg:"entry_name"`
	XMLProvenance         json.RawMessage             `pg:"xml_provenance,type:jsonb,default:'{}'"`
	SourceTitle           *string                     `pg:"source_title"`
	SourceLang            *string                     `pg:"source_lang"`
	SourceSrcLang         *string                     `pg:"source_src_lang"`
	SourceISBNs           []string                    `pg:"source_isbns,array,default:'{}'"`
	SourcePublisher       *string                     `pg:"source_publisher"`
	SourceCity            *string                     `pg:"source_city"`
	SourceYear            *string                     `pg:"source_year"`
	SourceDocumentID      *string                     `pg:"source_document_id"`
	SourceDocumentVersion *string                     `pg:"source_document_version"`
	SourceSequences       json.RawMessage             `pg:"source_sequences,type:jsonb,default:'[]'"`
	QualityFlags          []string                    `pg:"quality_flags,array,default:'{}'"`
	IsCurrent             bool                        `pg:"is_current,use_zero"`
	CreatedAt             time.Time                   `pg:"created_at,default:now()"`
}

// BookContributorCredit is one immutable title-info/author or
// title-info/translator element of one snapshot. Position is zero-based
// within its role; SourceFingerprint is the raw 32-byte SHA-256.
type BookContributorCredit struct {
	tableName         struct{}        `pg:"book_contributor_credit"`
	ID                int64           `pg:"id,pk"`
	SnapshotID        int64           `pg:"snapshot_id"`
	Role              ContributorRole `pg:"role"`
	Position          int             `pg:"position,use_zero"`
	SourceFirstName   *string         `pg:"source_first_name"`
	SourceMiddleName  *string         `pg:"source_middle_name"`
	SourceLastName    *string         `pg:"source_last_name"`
	SourceNickname    *string         `pg:"source_nickname"`
	SourceID          *string         `pg:"source_id"`
	SourceDisplayName string          `pg:"source_display_name"`
	SourceFingerprint []byte          `pg:"source_fingerprint"`
	QualityFlags      []string        `pg:"quality_flags,array,default:'{}'"`
	XMLProvenance     json.RawMessage `pg:"xml_provenance,type:jsonb,default:'{}'"`
	CreatedAt         time.Time       `pg:"created_at,default:now()"`
}

// AuthorMetadataPilotApproval is an administrator's immutable approval of a
// completed pilot_archive run. RunMode and RunStatus are fixed by the schema;
// together with the versions they must match the approved run.
type AuthorMetadataPilotApproval struct {
	tableName         struct{}                `pg:"author_metadata_pilot_approval"`
	ID                int64                   `pg:"id,pk"`
	RunID             int64                   `pg:"run_id"`
	RunMode           AuthorMetadataRunMode   `pg:"run_mode,default:'pilot_archive'"`
	RunStatus         AuthorMetadataRunStatus `pg:"run_status,default:'completed'"`
	ExtractorVersion  string                  `pg:"extractor_version"`
	NormalizerVersion string                  `pg:"normalizer_version"`
	ApprovedByUserID  int64                   `pg:"approved_by_user_id"`
	ApprovedAt        time.Time               `pg:"approved_at,default:now()"`
}
