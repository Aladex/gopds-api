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
	// AuthorMetadataRunItemArchiveMissing: the book's archive is not on a
	// volume whose other archives open.
	AuthorMetadataRunItemArchiveMissing AuthorMetadataRunItemStatus = "archive_missing"
	// AuthorMetadataRunItemArchiveUnreadable: the book's archive is there but
	// does not open, while other archives of the volume do.
	AuthorMetadataRunItemArchiveUnreadable AuthorMetadataRunItemStatus = "archive_unreadable"
)

// AuthorMetadataRunItemTerminalStatuses lists the statuses an item ends in.
func AuthorMetadataRunItemTerminalStatuses() []AuthorMetadataRunItemStatus {
	return []AuthorMetadataRunItemStatus{
		AuthorMetadataRunItemExtracted, AuthorMetadataRunItemExtractedNoAuthor,
		AuthorMetadataRunItemAlreadyCurrent, AuthorMetadataRunItemEntryMissing,
		AuthorMetadataRunItemInvalidFB2, AuthorMetadataRunItemUnsupportedEncoding,
		AuthorMetadataRunItemMetadataParseFailed, AuthorMetadataRunItemArchiveMissing,
		AuthorMetadataRunItemArchiveUnreadable,
	}
}

// AuthorMetadataRunItemStatuses lists every item status the schema accepts.
func AuthorMetadataRunItemStatuses() []AuthorMetadataRunItemStatus {
	return append([]AuthorMetadataRunItemStatus{AuthorMetadataRunItemPending}, AuthorMetadataRunItemTerminalStatuses()...)
}

// IsTerminal reports whether the item has finished. Values the schema would
// refuse, the zero value among them, are not terminal.
func (s AuthorMetadataRunItemStatus) IsTerminal() bool {
	for _, terminal := range AuthorMetadataRunItemTerminalStatuses() {
		if s == terminal {
			return true
		}
	}
	return false
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
	// SeedCursor is set while a full run is being seeded: the highest book ID
	// whose item exists. Nil once seeding is over, and for every run seeded
	// at its start.
	SeedCursor *int64 `pg:"seed_cursor"`
	// SeedTarget is how many books the catalog held when a full run
	// started: the end of its seeding progress. Nil for a run seeded at once.
	SeedTarget *int `pg:"seed_target"`
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

// The normalization pipeline (migration 24): immutable normalization results,
// the per-credit resolution with its append-only audit, immutable manual
// overrides, the local normalization queue, the manual review queue and the
// acceptance-policy classes. There is no LLM stream; the method enum keeps
// "llm" so a later LLM project needs no enum migration.

// NormalizationMethod is how a result was produced.
type NormalizationMethod string

const (
	NormalizationStructured NormalizationMethod = "structured"
	NormalizationRules      NormalizationMethod = "rules"
	NormalizationLLM        NormalizationMethod = "llm"
	NormalizationManual     NormalizationMethod = "manual"
)

// NormalizationMethods lists every method the schema accepts.
func NormalizationMethods() []NormalizationMethod {
	return []NormalizationMethod{NormalizationStructured, NormalizationRules, NormalizationLLM, NormalizationManual}
}

// NormalizationKind is what a result says the contributor is.
type NormalizationKind string

const (
	NormalizationPerson     NormalizationKind = "person"
	NormalizationCollective NormalizationKind = "collective"
	NormalizationUnknown    NormalizationKind = "unknown"
	NormalizationMalformed  NormalizationKind = "malformed"
)

// NormalizationKinds lists every kind the schema accepts.
func NormalizationKinds() []NormalizationKind {
	return []NormalizationKind{NormalizationPerson, NormalizationCollective, NormalizationUnknown, NormalizationMalformed}
}

// NormalizationStatus is the outcome a result records. Malformed results are
// always invalid.
type NormalizationStatus string

const (
	NormalizationNormalized NormalizationStatus = "normalized"
	NormalizationUnresolved NormalizationStatus = "unresolved"
	NormalizationInvalid    NormalizationStatus = "invalid"
)

// NormalizationStatuses lists every result status the schema accepts.
func NormalizationStatuses() []NormalizationStatus {
	return []NormalizationStatus{NormalizationNormalized, NormalizationUnresolved, NormalizationInvalid}
}

// CreditSelectionState is where an author credit stands.
type CreditSelectionState string

const (
	CreditSelectionSelected   CreditSelectionState = "selected"
	CreditSelectionInvalid    CreditSelectionState = "invalid"
	CreditSelectionUnresolved CreditSelectionState = "unresolved"
	CreditSelectionReview     CreditSelectionState = "review"
)

// CreditSelectionStates lists every resolution state the schema accepts.
func CreditSelectionStates() []CreditSelectionState {
	return []CreditSelectionState{
		CreditSelectionSelected, CreditSelectionInvalid, CreditSelectionUnresolved, CreditSelectionReview,
	}
}

// CreditSelectionBasis is why a selected result was selected.
type CreditSelectionBasis string

const (
	CreditSelectionAutomatic           CreditSelectionBasis = "automatic"
	CreditSelectionFingerprintOverride CreditSelectionBasis = "fingerprint_override"
	CreditSelectionCreditOverride      CreditSelectionBasis = "credit_override"
)

// CreditSelectionBases lists every selection basis the schema accepts.
func CreditSelectionBases() []CreditSelectionBasis {
	return []CreditSelectionBasis{
		CreditSelectionAutomatic, CreditSelectionFingerprintOverride, CreditSelectionCreditOverride,
	}
}

// UnresolvedReason is the closed reason an author credit stays unresolved.
type UnresolvedReason string

const (
	UnresolvedPolicyNotRegistered  UnresolvedReason = "policy_not_registered"
	UnresolvedNormalizerFailed     UnresolvedReason = "normalizer_failed"
	UnresolvedReviewLeftUnresolved UnresolvedReason = "review_left_unresolved"
)

// UnresolvedReasons lists every unresolved reason the schema accepts.
func UnresolvedReasons() []UnresolvedReason {
	return []UnresolvedReason{
		UnresolvedPolicyNotRegistered, UnresolvedNormalizerFailed, UnresolvedReviewLeftUnresolved,
	}
}

// NormalizationJobStatus is the state of a local normalization job.
type NormalizationJobStatus string

const (
	NormalizationJobPending   NormalizationJobStatus = "pending"
	NormalizationJobCompleted NormalizationJobStatus = "completed"
	NormalizationJobFailed    NormalizationJobStatus = "failed"
)

// NormalizationJobStatuses lists every job status the schema accepts.
func NormalizationJobStatuses() []NormalizationJobStatus {
	return []NormalizationJobStatus{NormalizationJobPending, NormalizationJobCompleted, NormalizationJobFailed}
}

// IsTerminal reports whether the job has finished. Values the schema would
// refuse are not terminal.
func (s NormalizationJobStatus) IsTerminal() bool {
	return s == NormalizationJobCompleted || s == NormalizationJobFailed
}

// ReviewReason is why a review item was opened.
type ReviewReason string

const (
	ReviewAmbiguousDecision        ReviewReason = "ambiguous_decision"
	ReviewIncompatibleManualSchema ReviewReason = "incompatible_manual_schema"
)

// ReviewReasons lists every review reason the schema accepts.
func ReviewReasons() []ReviewReason {
	return []ReviewReason{ReviewAmbiguousDecision, ReviewIncompatibleManualSchema}
}

// ReviewStatus is whether a review item still waits for a decision.
type ReviewStatus string

const (
	ReviewOpen   ReviewStatus = "open"
	ReviewClosed ReviewStatus = "closed"
)

// ReviewStatuses lists every review status the schema accepts.
func ReviewStatuses() []ReviewStatus {
	return []ReviewStatus{ReviewOpen, ReviewClosed}
}

// ReviewResolution is the action that closed a review item.
type ReviewResolution string

const (
	ReviewAccepted       ReviewResolution = "accepted"
	ReviewEdited         ReviewResolution = "edited"
	ReviewClassified     ReviewResolution = "classified"
	ReviewLeftUnresolved ReviewResolution = "left_unresolved"
	ReviewRetried        ReviewResolution = "retried"
)

// ReviewResolutions lists every resolution the schema accepts.
func ReviewResolutions() []ReviewResolution {
	return []ReviewResolution{ReviewAccepted, ReviewEdited, ReviewClassified, ReviewLeftUnresolved, ReviewRetried}
}

// AuthorAcceptanceClass registers one (decision class, script) for automatic
// selection from its policy version on, for one normalizer configuration,
// with the hash of the frozen evidence report it rests on and that report's
// repository path. A shipped registration has no actor (Source "shipped");
// an administrator's would name one. Immutable; registrations ship with the
// code.
type AuthorAcceptanceClass struct {
	tableName            struct{}  `pg:"author_acceptance_class"`
	ID                   int64     `pg:"id,pk"`
	PolicyVersion        string    `pg:"policy_version"`
	DecisionClass        string    `pg:"decision_class"`
	Script               string    `pg:"script"`
	ConfigVersion        string    `pg:"config_version"`
	EvidenceReportSHA256 []byte    `pg:"evidence_report_sha256"`
	EvidenceRef          string    `pg:"evidence_ref"`
	Source               string    `pg:"source"`
	RegisteredByUserID   *int64    `pg:"registered_by_user_id"`
	RegisteredAt         time.Time `pg:"registered_at,default:now()"`
}

// ContributorNormalizationResult is the immutable lexical result for one
// normalization input: not a person and not a canonical author. Automatic
// results carry their key and versions; a manual result carries its author
// instead.
type ContributorNormalizationResult struct {
	tableName           struct{}            `pg:"contributor_normalization_result"`
	ID                  int64               `pg:"id,pk"`
	SourceFingerprint   []byte              `pg:"source_fingerprint"`
	NormalizationKey    []byte              `pg:"normalization_key"`
	ExtractorVersion    *string             `pg:"extractor_version"`
	NormalizerVersion   *string             `pg:"normalizer_version"`
	ResultSchemaVersion string              `pg:"result_schema_version"`
	Method              NormalizationMethod `pg:"method"`
	Kind                NormalizationKind   `pg:"kind"`
	Status              NormalizationStatus `pg:"status"`
	DecisionClass       *string             `pg:"decision_class"`
	GivenName           *string             `pg:"given_name"`
	AdditionalNames     []string            `pg:"additional_names,array,default:'{}'"`
	FamilyName          *string             `pg:"family_name"`
	Nickname            *string             `pg:"nickname"`
	Prefix              *string             `pg:"prefix"`
	Suffix              *string             `pg:"suffix"`
	DisplayName         *string             `pg:"display_name"`
	SortName            *string             `pg:"sort_name"`
	SearchKey           *string             `pg:"search_key"`
	Script              *string             `pg:"script"`
	QualityFlags        []string            `pg:"quality_flags,array,default:'{}'"`
	CreatedByUserID     *int64              `pg:"created_by_user_id"`
	CreatedAt           time.Time           `pg:"created_at,default:now()"`
}

// ContributorManualOverride is an immutable manual decision for exactly one
// scope: ScopeCreditID or ScopeFingerprint. SourceFingerprint is the source
// it is about; CreditRole and ResultMethod are fixed by the schema.
type ContributorManualOverride struct {
	tableName         struct{}            `pg:"contributor_manual_override"`
	ID                int64               `pg:"id,pk"`
	ScopeCreditID     *int64              `pg:"scope_credit_id"`
	ScopeFingerprint  []byte              `pg:"scope_fingerprint"`
	SourceFingerprint []byte              `pg:"source_fingerprint"`
	CreditRole        ContributorRole     `pg:"credit_role,default:'author'"`
	ResultID          int64               `pg:"result_id"`
	ResultMethod      NormalizationMethod `pg:"result_method,default:'manual'"`
	CreatedByUserID   int64               `pg:"created_by_user_id"`
	CreatedAt         time.Time           `pg:"created_at,default:now()"`
}

// BookContributorCreditSelection is where one author credit stands: selected,
// invalid, unresolved with a closed reason, or in review. Every change is
// appended to the audit by the database.
type BookContributorCreditSelection struct {
	tableName         struct{}              `pg:"book_contributor_credit_selection"`
	CreditID          int64                 `pg:"credit_id,pk"`
	SourceFingerprint []byte                `pg:"source_fingerprint"`
	CreditRole        ContributorRole       `pg:"credit_role,default:'author'"`
	State             CreditSelectionState  `pg:"state"`
	ResultID          *int64                `pg:"result_id"`
	Basis             *CreditSelectionBasis `pg:"basis"`
	OverrideID        *int64                `pg:"override_id"`
	PolicyVersion     *string               `pg:"policy_version"`
	UnresolvedReason  *UnresolvedReason     `pg:"unresolved_reason"`
	DecidedByUserID   *int64                `pg:"decided_by_user_id"`
	DecidedAt         time.Time             `pg:"decided_at,default:now()"`
}

// BookContributorCreditSelectionAudit is one immutable record of a resolution
// change.
type BookContributorCreditSelectionAudit struct {
	tableName        struct{}              `pg:"book_contributor_credit_selection_audit"`
	ID               int64                 `pg:"id,pk"`
	CreditID         int64                 `pg:"credit_id"`
	PreviousState    *CreditSelectionState `pg:"previous_state"`
	PreviousResultID *int64                `pg:"previous_result_id"`
	State            CreditSelectionState  `pg:"state"`
	ResultID         *int64                `pg:"result_id"`
	Basis            *CreditSelectionBasis `pg:"basis"`
	OverrideID       *int64                `pg:"override_id"`
	PolicyVersion    *string               `pg:"policy_version"`
	UnresolvedReason *UnresolvedReason     `pg:"unresolved_reason"`
	DecidedByUserID  *int64                `pg:"decided_by_user_id"`
	RecordedAt       time.Time             `pg:"recorded_at,default:now()"`
}

// ContributorNormalizationJob is one local normalization job per
// normalization key. Lease columns follow AuthorMetadataRunItem.
type ContributorNormalizationJob struct {
	tableName         struct{}               `pg:"contributor_normalization_job"`
	ID                int64                  `pg:"id,pk"`
	NormalizationKey  []byte                 `pg:"normalization_key"`
	SourceFingerprint []byte                 `pg:"source_fingerprint"`
	ExtractorVersion  string                 `pg:"extractor_version"`
	NormalizerVersion string                 `pg:"normalizer_version"`
	Status            NormalizationJobStatus `pg:"status,default:'pending'"`
	ResultID          *int64                 `pg:"result_id"`
	LastErrorClass    *string                `pg:"last_error_class"`
	LeaseOwner        *string                `pg:"lease_owner,type:uuid"`
	LeaseExpiresAt    *time.Time             `pg:"lease_expires_at"`
	AttemptCount      int                    `pg:"attempt_count,use_zero"`
	NextAttemptAt     time.Time              `pg:"next_attempt_at,default:now()"`
	CreatedAt         time.Time              `pg:"created_at,default:now()"`
	UpdatedAt         time.Time              `pg:"updated_at,default:now()"`
	FinishedAt        *time.Time             `pg:"finished_at"`
}

// ContributorNormalizationJobAttempt is one append-only attempt of a local
// job, finished once with an outcome, a closed error class, or both.
type ContributorNormalizationJobAttempt struct {
	tableName  struct{}                `pg:"contributor_normalization_job_attempt"`
	ID         int64                   `pg:"id,pk"`
	JobID      int64                   `pg:"job_id"`
	AttemptNo  int                     `pg:"attempt_no"`
	LeaseOwner string                  `pg:"lease_owner,type:uuid"`
	StartedAt  time.Time               `pg:"started_at,default:now()"`
	FinishedAt *time.Time              `pg:"finished_at"`
	Outcome    *NormalizationJobStatus `pg:"outcome"`
	ErrorClass *string                 `pg:"error_class"`
}

// ContributorReviewItem is one manual review item for exactly one scope,
// open until a resolution closes it for good.
type ContributorReviewItem struct {
	tableName          struct{}          `pg:"contributor_review_item"`
	ID                 int64             `pg:"id,pk"`
	ScopeCreditID      *int64            `pg:"scope_credit_id"`
	ScopeFingerprint   []byte            `pg:"scope_fingerprint"`
	SourceFingerprint  []byte            `pg:"source_fingerprint"`
	CreditRole         ContributorRole   `pg:"credit_role,default:'author'"`
	Reason             ReviewReason      `pg:"reason"`
	DecisionClass      *string           `pg:"decision_class"`
	ProposalResultID   *int64            `pg:"proposal_result_id"`
	Status             ReviewStatus      `pg:"status,default:'open'"`
	Resolution         *ReviewResolution `pg:"resolution"`
	ResolutionResultID *int64            `pg:"resolution_result_id"`
	ResolvedByUserID   *int64            `pg:"resolved_by_user_id"`
	CreatedAt          time.Time         `pg:"created_at,default:now()"`
	FinishedAt         *time.Time        `pg:"finished_at"`
}
