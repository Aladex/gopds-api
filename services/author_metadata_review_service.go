package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gopds-api/config"
	"gopds-api/database"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The admin-facing review service of the author metadata pipeline (phase
// 11): DTOs over the review repository, one transaction per decision, and
// the linked books an admin sees on the review screen. No HTTP here.
//
// The admin DTO is deliberately separate from the persisted rows the
// repository speaks: a review item reaches the admin as a closed summary,
// the raw selection audit only through DecisionHistory, and linked books
// never travel into any decision — they are for the person looking at the
// screen (privacy 3.11/3.14).

// AuthorMetadataReviewConfig wires the review service to the pipeline's
// current versions: retries re-queue under the normalizer version, and
// schema incompatibility is judged against the result schema version.
// RetryMaxAttempts is the local worker's attempt budget
// (AuthorMetadataLocalWorkerConfig.Retry.MaxAttempts): a retry the worker
// could not claim any more is refused rather than acknowledged, so it must
// be the budget the worker runs with.
type AuthorMetadataReviewConfig struct {
	NormalizerVersion   string
	ResultSchemaVersion int
	RetryMaxAttempts    int
}

// DefaultAuthorMetadataReviewConfig is the configuration of the shipped
// normalizer and the shipped local worker.
func DefaultAuthorMetadataReviewConfig() AuthorMetadataReviewConfig {
	return AuthorMetadataReviewConfig{
		NormalizerVersion:   authornormNormalizerVersion,
		ResultSchemaVersion: authornormResultSchemaVersion,
		RetryMaxAttempts:    DefaultAuthorMetadataLocalWorkerConfig().Retry.MaxAttempts,
	}
}

// AuthorMetadataReviewConfigFrom maps the server configuration onto the
// review service. The retry budget is the local stream's max_attempts,
// through the same mapping AuthorMetadataLocalWorkerConfigFrom gives the
// running workers, so a retry is refused exactly when they could not claim
// it.
func AuthorMetadataReviewConfigFrom(c *config.AuthorMetadataConfig) AuthorMetadataReviewConfig {
	cfg := DefaultAuthorMetadataReviewConfig()
	cfg.RetryMaxAttempts = AuthorMetadataLocalWorkerConfigFrom(c).Retry.MaxAttempts
	return cfg
}

const (
	// reviewFingerprintLen is the length of a raw SHA-256 source fingerprint.
	reviewFingerprintLen = 32
	// reviewLinkedBooksLimit caps the linked books one detail shows.
	reviewLinkedBooksLimit = 50
)

var (
	// ErrInvalidAuthorMetadataReviewConfig marks a configuration that would
	// queue retries or judge schemas against nothing.
	ErrInvalidAuthorMetadataReviewConfig = errors.New("services: invalid author metadata review config")
	// ErrInvalidReviewRequest marks an admin request with a non-positive
	// item or actor ID.
	ErrInvalidReviewRequest = errors.New("services: invalid review request")
)

func (c AuthorMetadataReviewConfig) Validate() error {
	if strings.TrimSpace(c.NormalizerVersion) == "" {
		return fmt.Errorf("%w: empty normalizer version", ErrInvalidAuthorMetadataReviewConfig)
	}
	if c.ResultSchemaVersion <= 0 {
		return fmt.Errorf("%w: result schema version must be positive", ErrInvalidAuthorMetadataReviewConfig)
	}
	if c.RetryMaxAttempts <= 0 {
		return fmt.Errorf("%w: the retry attempt budget must be positive", ErrInvalidAuthorMetadataReviewConfig)
	}
	return nil
}

// AuthorMetadataReviewService decides review items over the database.
type AuthorMetadataReviewService struct {
	db  *pg.DB
	cfg AuthorMetadataReviewConfig
}

// NewAuthorMetadataReviewService validates the configuration and returns the
// service.
func NewAuthorMetadataReviewService(db *pg.DB, cfg AuthorMetadataReviewConfig) (*AuthorMetadataReviewService, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: no database", ErrInvalidAuthorMetadataReviewConfig)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &AuthorMetadataReviewService{db: db, cfg: cfg}, nil
}

// ReviewItemView is one open review item as the admin sees it. The source
// fingerprint is hex; names are not part of the queue.
type ReviewItemView struct {
	ID          int64
	Reason      models.ReviewReason
	ScopeCredit *int64
	Fingerprint string
	Class       *string
	CreatedAt   time.Time
}

// ReviewCursor is the stable pagination cursor: (created_at, id).
type ReviewCursor struct {
	CreatedAt time.Time
	ID        int64
}

// ReviewPage is one page of the open queue and the cursor the next page
// continues after; Next is false when the page was not full.
type ReviewPage struct {
	Items []ReviewItemView
	Next  *ReviewCursor
}

// ReviewListOptions pages the queue.
type ReviewListOptions struct {
	Limit int
	After *ReviewCursor
	// Reason narrows the queue to one review reason.
	Reason models.ReviewReason
}

// List returns one page of the open review queue. The effective limit is
// normalized once, so a full default-sized page carries its continuation
// cursor like any other.
func (s *AuthorMetadataReviewService) List(ctx context.Context, opts ReviewListOptions) (ReviewPage, error) {
	limit := opts.Limit
	if limit == 0 {
		limit = database.DefaultReviewListLimit
	}
	filter := database.ReviewListFilter{Limit: limit, Reason: opts.Reason}
	if opts.After != nil {
		filter.AfterCreatedAt, filter.AfterID = opts.After.CreatedAt, opts.After.ID
	}
	items, err := database.ListOpenReviewItems(ctx, s.db, filter)
	if err != nil {
		return ReviewPage{}, err
	}
	page := ReviewPage{Items: make([]ReviewItemView, 0, len(items))}
	for i := range items {
		page.Items = append(page.Items, reviewItemView(&items[i]))
	}
	if len(items) == limit {
		last := &items[len(items)-1]
		page.Next = &ReviewCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

func reviewItemView(item *models.ContributorReviewItem) ReviewItemView {
	view := ReviewItemView{
		ID: item.ID, Reason: item.Reason, ScopeCredit: item.ScopeCreditID,
		Class: item.DecisionClass, CreatedAt: item.CreatedAt,
	}
	if len(item.ScopeFingerprint) > 0 {
		view.Fingerprint = fmt.Sprintf("%x", item.ScopeFingerprint)
	}
	return view
}

// ReviewProposalView is the automatic result a review item proposes, as the
// admin sees it.
type ReviewProposalView struct {
	ResultID      int64
	GivenName     string
	FamilyName    string
	DisplayName   string
	Kind          models.NormalizationKind
	Status        models.NormalizationStatus
	Class         string
	SchemaVersion string
}

// ReviewDetailView is everything the review screen shows for one item: the
// item, its proposal, the books linked to the source, and whether the
// decision rests on a manual result of an incompatible result schema.
type ReviewDetailView struct {
	Item               ReviewItemView
	Proposal           *ReviewProposalView
	LinkedBooks        []database.ReviewLinkedBook
	SchemaIncompatible bool
}

// Detail returns one open review item with its proposal and linked books.
func (s *AuthorMetadataReviewService) Detail(ctx context.Context, itemID int64) (ReviewDetailView, error) {
	if itemID <= 0 {
		return ReviewDetailView{}, ErrInvalidReviewRequest
	}
	var item models.ContributorReviewItem
	if err := s.db.ModelContext(ctx, &item).Where("id = ?", itemID).Select(); err != nil {
		return ReviewDetailView{}, fmt.Errorf("reading the review item: %w", err)
	}
	detail := ReviewDetailView{Item: reviewItemView(&item), LinkedBooks: []database.ReviewLinkedBook{}}
	detail.SchemaIncompatible = item.Reason == models.ReviewIncompatibleManualSchema

	if item.ProposalResultID != nil {
		var result models.ContributorNormalizationResult
		if err := s.db.ModelContext(ctx, &result).Where("id = ?", *item.ProposalResultID).Select(); err != nil {
			return ReviewDetailView{}, fmt.Errorf("reading the proposal: %w", err)
		}
		detail.Proposal = &ReviewProposalView{
			ResultID: result.ID, GivenName: strOrEmpty(result.GivenName),
			FamilyName: strOrEmpty(result.FamilyName), DisplayName: strOrEmpty(result.DisplayName),
			Kind: result.Kind, Status: result.Status,
			Class: strOrEmpty(result.DecisionClass), SchemaVersion: result.ResultSchemaVersion,
		}
	}
	if len(item.SourceFingerprint) == reviewFingerprintLen {
		books, err := database.LinkedBooksOfFingerprint(ctx, s.db, item.SourceFingerprint, reviewLinkedBooksLimit)
		if err != nil {
			return ReviewDetailView{}, err
		}
		detail.LinkedBooks = books
	}
	return detail, nil
}

// AdminCorrection is the admin's edited name: the DTO the service takes and
// the repository's persisted correction.
type AdminCorrection struct {
	GivenName      string
	AdditionalName string
	FamilyName     string
	Nickname       string
	Prefix         string
	Suffix         string
	DisplayName    string
	SortName       string
	SearchKey      string
	Script         string
	Kind           models.NormalizationKind
}

func (c *AdminCorrection) persisted() *database.ManualCorrection {
	return &database.ManualCorrection{
		GivenName: c.GivenName, AdditionalName: c.AdditionalName, FamilyName: c.FamilyName,
		Nickname: c.Nickname, Prefix: c.Prefix, Suffix: c.Suffix, DisplayName: c.DisplayName,
		SortName: c.SortName, SearchKey: c.SearchKey, Script: c.Script, Kind: c.Kind,
	}
}

func (s *AuthorMetadataReviewService) act(
	ctx context.Context, itemID, admin int64, decision database.ReviewDecision,
) (database.ReviewReport, error) {
	if itemID <= 0 || admin <= 0 {
		return database.ReviewReport{}, ErrInvalidReviewRequest
	}
	var report database.ReviewReport
	err := s.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		var applyErr error
		report, applyErr = database.ApplyReviewAction(ctx, tx, itemID, admin, decision)
		return applyErr
	})
	if err != nil {
		return database.ReviewReport{}, err
	}
	return report, nil
}

// Accept selects the item's proposal: an immutable manual copy of it, the
// scope's newest override, and the re-resolution of the item's credits.
func (s *AuthorMetadataReviewService) Accept(ctx context.Context, itemID, admin int64) (database.ReviewReport, error) {
	return s.act(ctx, itemID, admin, database.ReviewDecision{Action: database.ReviewAccept})
}

// Edit selects the admin's corrected name for the item's scope.
func (s *AuthorMetadataReviewService) Edit(
	ctx context.Context, itemID, admin int64, correction *AdminCorrection,
) (database.ReviewReport, error) {
	if correction == nil {
		return database.ReviewReport{}, ErrInvalidReviewRequest
	}
	persisted := correction.persisted()
	return s.act(ctx, itemID, admin, database.ReviewDecision{Action: database.ReviewEdit, Correction: persisted})
}

// Classify selects the proposal under the admin's kind.
func (s *AuthorMetadataReviewService) Classify(
	ctx context.Context, itemID, admin int64, kind models.NormalizationKind,
) (database.ReviewReport, error) {
	return s.act(ctx, itemID, admin, database.ReviewDecision{Action: database.ReviewClassify, Kind: kind})
}

// LeaveUnresolved closes the item with the credits unresolved behind the
// closed reason review_left_unresolved.
func (s *AuthorMetadataReviewService) LeaveUnresolved(ctx context.Context, itemID, admin int64) (database.ReviewReport, error) {
	return s.act(ctx, itemID, admin, database.ReviewDecision{Action: database.ReviewLeaveUnresolved})
}

// Retry hands the item's input back to the local normalizer under the
// configured normalizer version (scope amendment A4: never an LLM). A
// changed version is a new normalization key and therefore a new immutable
// result; an unchanged one reuses the stored result. A retry whose job has
// used the worker's whole attempt budget returns
// database.ErrReviewRetryExhausted and leaves the item open.
func (s *AuthorMetadataReviewService) Retry(ctx context.Context, itemID, admin int64) (database.ReviewReport, error) {
	return s.act(ctx, itemID, admin, database.ReviewDecision{
		Action: database.ReviewRetry, RetryNormalizerVersion: s.cfg.NormalizerVersion,
		RetryMaxAttempts: s.cfg.RetryMaxAttempts,
	})
}

// ApplyOverride writes a correction for a scope outside any open item — the
// admin's direct fix. It re-resolves the scope's credits through the one
// resolver in the same transaction.
func (s *AuthorMetadataReviewService) ApplyOverride(
	ctx context.Context,
	scope database.OverrideScope,
	admin int64,
	correction *AdminCorrection,
) (database.OverrideReport, error) {
	if admin <= 0 || correction == nil {
		return database.OverrideReport{}, ErrInvalidReviewRequest
	}
	persisted := correction.persisted()
	var report database.OverrideReport
	err := s.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		var applyErr error
		report, applyErr = database.ApplyOverride(ctx, tx, scope, admin, persisted)
		return applyErr
	})
	if err != nil {
		return database.OverrideReport{}, err
	}
	return report, nil
}

// FlagIncompatibleManualSchemas opens review items for scopes whose active
// override rests on a manual result of another result schema version than
// the configured one, and returns how many items opened.
func (s *AuthorMetadataReviewService) FlagIncompatibleManualSchemas(ctx context.Context) (int, error) {
	var opened int
	err := s.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		var flagErr error
		opened, flagErr = database.FlagManualSchemaIncompatibility(ctx, tx, s.cfg.ResultSchemaVersion)
		return flagErr
	})
	if err != nil {
		return 0, err
	}
	return opened, nil
}

// DecisionHistoryEntry is one immutable audit record of a credit's
// resolution, as the admin sees it.
type DecisionHistoryEntry struct {
	CreditID         int64
	PreviousState    string
	PreviousResultID *int64
	State            string
	ResultID         *int64
	Basis            string
	OverrideID       *int64
	UnresolvedReason string
	DecidedByUserID  *int64
	RecordedAt       time.Time
}

// DecisionHistory returns a credit's append-only resolution audit in
// recording order.
func (s *AuthorMetadataReviewService) DecisionHistory(ctx context.Context, creditID int64) ([]DecisionHistoryEntry, error) {
	if creditID <= 0 {
		return nil, ErrInvalidReviewRequest
	}
	var rows []models.BookContributorCreditSelectionAudit
	err := s.db.ModelContext(ctx, &rows).Where("credit_id = ?", creditID).Order("id").Select()
	if err != nil {
		return nil, fmt.Errorf("reading the decision history: %w", err)
	}
	history := make([]DecisionHistoryEntry, 0, len(rows))
	for i := range rows {
		history = append(history, DecisionHistoryEntry{
			CreditID: rows[i].CreditID, PreviousState: strOrEmpty(rows[i].PreviousState),
			PreviousResultID: rows[i].PreviousResultID, State: string(rows[i].State),
			ResultID: rows[i].ResultID, Basis: strOrEmpty(rows[i].Basis),
			OverrideID: rows[i].OverrideID, UnresolvedReason: strOrEmpty(rows[i].UnresolvedReason),
			DecidedByUserID: rows[i].DecidedByUserID, RecordedAt: rows[i].RecordedAt,
		})
	}
	return history, nil
}

func strOrEmpty[T ~string](s *T) string {
	if s == nil {
		return ""
	}
	return string(*s)
}

// authornormNormalizerVersion and authornormResultSchemaVersion keep the
// service's default configuration in step with the shipped normalizer
// without importing its internals here.
const (
	authornormNormalizerVersion   = "authornorm-local-v1"
	authornormResultSchemaVersion = 1
)
