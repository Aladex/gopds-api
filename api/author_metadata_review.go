package api

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gopds-api/config"
	"gopds-api/database"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v10"
)

// Admin API of the author metadata review queue (coordinator contract
// plans/briefs/authors-api-contract.md, "Review"): thin handler DTOs over the
// review service, closed public errors, linked books only in the detail
// response and never in a list or a log. The handlers only validate, call the
// service and shape the JSON — the enrichment reads live in the adapter, the
// decision paths in the phase-11 service and repository.

// Top-level keys of the review responses.
const (
	jsonKeyItems = "items"
	jsonKeyItem  = "item"
)

// Closed error codes of the review API beyond the shared ones.
const (
	codeInvalidStatus   = "invalid_status"
	codeInvalidLimit    = "invalid_limit"
	codeInvalidCursor   = "invalid_cursor"
	codeInvalidScope    = "invalid_scope"
	codeInvalidKind     = "invalid_kind"
	codeInvalidResult   = "invalid_result"
	codeReviewNotFound  = "review_not_found"
	codeReviewConflict  = "review_conflict"
	codeScopeMismatch   = "scope_mismatch"
	codeRetryExhausted  = "retry_exhausted"
	codeLinkedBooksSeen = "linked_books_only_in_detail"
)

// Errors the review service returns; each maps to one closed code.
var (
	// ErrAuthorMetadataReviewNotFound marks an item ID with no row.
	ErrAuthorMetadataReviewNotFound = errors.New("api: author metadata review item not found")
	// ErrAuthorMetadataReviewConflict marks an item another reviewer already
	// decided.
	ErrAuthorMetadataReviewConflict = errors.New("api: author metadata review item already decided")
	// ErrAuthorMetadataReviewScopeMismatch marks an action whose confirmed
	// scope differs from the item's own.
	ErrAuthorMetadataReviewScopeMismatch = errors.New("api: the action's scope differs from the item's")
	// ErrAuthorMetadataReviewRetryExhausted marks a retry whose job has used
	// the worker's whole attempt budget.
	ErrAuthorMetadataReviewRetryExhausted = errors.New("api: the review retry's attempt budget is exhausted")
	// ErrAuthorMetadataReviewUnavailable marks the unwired service.
	ErrAuthorMetadataReviewUnavailable = errors.New("api: the author metadata review service is not wired")
)

// reviewErrorStatus maps a service error to its HTTP status and closed code.
func reviewErrorStatus(err error) (status int, code string) {
	switch {
	case errors.Is(err, ErrAuthorMetadataReviewNotFound):
		return http.StatusNotFound, codeReviewNotFound
	case errors.Is(err, ErrAuthorMetadataReviewConflict):
		return http.StatusConflict, codeReviewConflict
	case errors.Is(err, ErrAuthorMetadataReviewScopeMismatch):
		return http.StatusConflict, codeScopeMismatch
	case errors.Is(err, ErrAuthorMetadataReviewRetryExhausted):
		return http.StatusConflict, codeRetryExhausted
	case errors.Is(err, database.ErrInvalidManualCorrection):
		return http.StatusBadRequest, codeInvalidResult
	case errors.Is(err, database.ErrInvalidReviewAction):
		return http.StatusBadRequest, codeInvalidKind
	case errors.Is(err, ErrAuthorMetadataReviewUnavailable):
		return http.StatusInternalServerError, "review_service_unavailable"
	}
	return http.StatusInternalServerError, codeInternalError
}

// abortReviewError answers a validation or service error with its closed
// code; request validation reuses the runs decoder's error type.
func abortReviewError(c *gin.Context, err error) {
	var reqErr runRequestError
	if errors.As(err, &reqErr) {
		abortRun(c, http.StatusBadRequest, reqErr.code)
		return
	}
	status, code := reviewErrorStatus(err)
	abortRun(c, status, code)
}

// --- contract shapes ---

// AuthorMetadataReviewListItem is the contract's ReviewListItem.
type AuthorMetadataReviewListItem struct {
	ID                int64     `json:"id"`
	Scope             string    `json:"scope"`
	CreditID          *int64    `json:"credit_id"`
	SourceFingerprint string    `json:"source_fingerprint"`
	Reason            string    `json:"reason"`
	DecisionClass     string    `json:"decision_class"`
	DisplayName       string    `json:"display_name"`
	CreditsCount      int64     `json:"credits_count"`
	CreatedAt         time.Time `json:"created_at"`
}

// AuthorMetadataReviewSource is the canonical source of the item's
// fingerprint: the components of one credit that carries it.
type AuthorMetadataReviewSource struct {
	First    *string  `json:"first"`
	Middle   *string  `json:"middle"`
	Last     *string  `json:"last"`
	Nickname *string  `json:"nickname"`
	Display  string   `json:"display"`
	Flags    []string `json:"flags"`
}

// AuthorMetadataReviewProposal is the automatic result the item proposes.
type AuthorMetadataReviewProposal struct {
	GivenName      *string  `json:"given_name"`
	AdditionalName *string  `json:"additional_names"`
	FamilyName     *string  `json:"family_name"`
	Nickname       *string  `json:"nickname"`
	DisplayName    string   `json:"display_name"`
	SortName       *string  `json:"sort_name"`
	SearchKey      string   `json:"search_key"`
	Script         string   `json:"script"`
	Kind           string   `json:"kind"`
	Method         string   `json:"method"`
	QualityFlags   []string `json:"quality_flags"`
}

// AuthorMetadataReviewLinkedBook is one linked book of the detail; titles
// never leave this response.
type AuthorMetadataReviewLinkedBook struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// AuthorMetadataReviewDetail is the contract's ReviewDetail.
type AuthorMetadataReviewDetail struct {
	AuthorMetadataReviewListItem
	Source      AuthorMetadataReviewSource       `json:"source"`
	Proposal    *AuthorMetadataReviewProposal    `json:"proposal"`
	LinkedBooks []AuthorMetadataReviewLinkedBook `json:"linked_books"`
}

// AuthorMetadataReviewListQuery is one page request of the queue.
type AuthorMetadataReviewListQuery struct {
	Status string
	Limit  int
	After  *AuthorMetadataReviewCursor
}

// AuthorMetadataReviewCursor is the decoded continuation cursor.
type AuthorMetadataReviewCursor struct {
	CreatedAt time.Time
	ID        int64
}

// AuthorMetadataReviewListPage is one page and its continuation.
type AuthorMetadataReviewListPage struct {
	Items []AuthorMetadataReviewListItem
	Next  *AuthorMetadataReviewCursor
}

// AuthorMetadataReviewService is what the handlers need from the review
// side. Actions return only their error: the handler answers with the detail
// of the item they decided, through the same shaping as GET /review/:id.
type AuthorMetadataReviewService interface {
	List(ctx context.Context, q AuthorMetadataReviewListQuery) (AuthorMetadataReviewListPage, error)
	Detail(ctx context.Context, itemID int64) (AuthorMetadataReviewDetail, error)
	Accept(ctx context.Context, itemID, admin int64, scope string) error
	Edit(ctx context.Context, itemID, admin int64, scope string, result AuthorMetadataReviewEditResult) error
	Classify(ctx context.Context, itemID, admin int64, scope, kind string) error
	LeaveUnresolved(ctx context.Context, itemID, admin int64, scope string) error
	Retry(ctx context.Context, itemID, admin int64) error
}

// AuthorMetadataReviewEditResult is the edit body's result object.
type AuthorMetadataReviewEditResult struct {
	GivenName      *string
	AdditionalName *string
	FamilyName     *string
	Nickname       *string
	DisplayName    string
	SortName       *string
	Kind           string
}

// authorMetadataReviewService is the wiring point of the concrete adapter;
// until the server sets it, every route answers 500 review_service_unavailable.
var authorMetadataReviewService = func() AuthorMetadataReviewService {
	return unavailableAuthorMetadataReviews{}
}

type unavailableAuthorMetadataReviews struct{}

func (unavailableAuthorMetadataReviews) List(context.Context, AuthorMetadataReviewListQuery) (AuthorMetadataReviewListPage, error) {
	return AuthorMetadataReviewListPage{}, ErrAuthorMetadataReviewUnavailable
}

func (unavailableAuthorMetadataReviews) Detail(context.Context, int64) (AuthorMetadataReviewDetail, error) {
	return AuthorMetadataReviewDetail{}, ErrAuthorMetadataReviewUnavailable
}

func (unavailableAuthorMetadataReviews) Accept(context.Context, int64, int64, string) error {
	return ErrAuthorMetadataReviewUnavailable
}

func (unavailableAuthorMetadataReviews) Edit(context.Context, int64, int64, string, AuthorMetadataReviewEditResult) error {
	return ErrAuthorMetadataReviewUnavailable
}

func (unavailableAuthorMetadataReviews) Classify(context.Context, int64, int64, string, string) error {
	return ErrAuthorMetadataReviewUnavailable
}

func (unavailableAuthorMetadataReviews) LeaveUnresolved(context.Context, int64, int64, string) error {
	return ErrAuthorMetadataReviewUnavailable
}

func (unavailableAuthorMetadataReviews) Retry(context.Context, int64, int64) error {
	return ErrAuthorMetadataReviewUnavailable
}

// SetAuthorMetadataReviewService wires the concrete adapter over the phase-11
// review service, configured from the server's own configuration through
// services.AuthorMetadataReviewConfigFrom — the same retry budget the local
// workers run with. Called by the server's author-metadata wiring, before the
// routes are set up.
func SetAuthorMetadataReviewService(db *pg.DB, c *config.AuthorMetadataConfig) error {
	adapter, err := newAuthorMetadataReviewAdapter(db, c)
	if err != nil {
		return err
	}
	authorMetadataReviewService = func() AuthorMetadataReviewService { return adapter }
	return nil
}

// --- the adapter over the phase-11 service ---

// authorMetadataReviewAdapter serves the contract's list and detail shapes —
// which read more than the phase-11 DTOs carry (the closed queue, the
// display name, the credits count, the source components) — and forwards
// every decision to the proven review service inside one transaction per
// action.
type authorMetadataReviewAdapter struct {
	db  *pg.DB
	svc *services.AuthorMetadataReviewService
}

func newAuthorMetadataReviewAdapter(db *pg.DB, c *config.AuthorMetadataConfig) (*authorMetadataReviewAdapter, error) {
	svc, err := services.NewAuthorMetadataReviewService(db, services.AuthorMetadataReviewConfigFrom(c))
	if err != nil {
		return nil, err
	}
	return &authorMetadataReviewAdapter{db: db, svc: svc}, nil
}

// reviewListRow is one enriched queue row.
type reviewListRow struct {
	ID                int64
	ScopeCreditID     *int64
	SourceFingerprint []byte
	Reason            models.ReviewReason
	DecisionClass     *string
	DisplayName       string
	CreditsCount      int64
	CreatedAt         time.Time
}

const reviewListSQL = `SELECT i.id, i.scope_credit_id, i.source_fingerprint, i.reason, i.decision_class,
		coalesce(p.display_name, src.display_name, '') AS display_name,
		coalesce(cc.credits, 0) AS credits_count,
		i.created_at
	FROM contributor_review_item i
	LEFT JOIN contributor_normalization_result p ON p.id = i.proposal_result_id
	LEFT JOIN LATERAL (
		SELECT c.source_display_name AS display_name
		FROM book_contributor_credit c
		WHERE c.source_fingerprint = i.source_fingerprint
		ORDER BY c.id LIMIT 1
	) src ON true
	LEFT JOIN LATERAL (
		SELECT count(*) AS credits
		FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id
		WHERE c.source_fingerprint = i.source_fingerprint AND s.is_current AND c.role = 'author'
	) cc ON true
	WHERE i.status = ?0
		AND (?1::timestamptz IS NULL OR (i.created_at, i.id) > (?1, ?2))
	ORDER BY i.created_at, i.id
	LIMIT ?3`

// List serves one page of the open or closed queue.
func (a *authorMetadataReviewAdapter) List(
	ctx context.Context, q AuthorMetadataReviewListQuery,
) (AuthorMetadataReviewListPage, error) {
	var after *time.Time
	if q.After != nil {
		u := q.After.CreatedAt.UTC()
		after = &u
	}
	var rows []reviewListRow
	if _, err := a.db.QueryContext(ctx, &rows, reviewListSQL,
		q.Status, after, cursorID(q.After), q.Limit); err != nil {
		return AuthorMetadataReviewListPage{}, fmt.Errorf("listing review items: %w", err)
	}
	page := AuthorMetadataReviewListPage{Items: make([]AuthorMetadataReviewListItem, 0, len(rows))}
	for i := range rows {
		page.Items = append(page.Items, reviewListItem(&rows[i]))
	}
	if len(rows) == q.Limit {
		last := &rows[len(rows)-1]
		page.Next = &AuthorMetadataReviewCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

func cursorID(after *AuthorMetadataReviewCursor) int64 {
	if after == nil {
		return 0
	}
	return after.ID
}

func reviewListItem(row *reviewListRow) AuthorMetadataReviewListItem {
	scope := reviewScopeFingerprint
	if row.ScopeCreditID != nil {
		scope = reviewScopeCredit
	}
	return AuthorMetadataReviewListItem{
		ID: row.ID, Scope: scope, CreditID: row.ScopeCreditID,
		SourceFingerprint: fmt.Sprintf("%x", row.SourceFingerprint),
		Reason:            string(row.Reason), DecisionClass: strOrEmpty2(row.DecisionClass),
		DisplayName: row.DisplayName, CreditsCount: row.CreditsCount,
		CreatedAt: row.CreatedAt.UTC(),
	}
}

// reviewSummaryRow reads one item's enriched summary row.
func (a *authorMetadataReviewAdapter) reviewSummaryRow(ctx context.Context, itemID int64) (reviewListRow, error) {
	var row reviewListRow
	_, err := a.db.QueryOneContext(ctx, &row, `SELECT i.id, i.scope_credit_id, i.source_fingerprint, i.reason,
			i.decision_class, coalesce(p.display_name, src.display_name, '') AS display_name,
			coalesce(cc.credits, 0) AS credits_count, i.created_at
		FROM contributor_review_item i
		LEFT JOIN contributor_normalization_result p ON p.id = i.proposal_result_id
		LEFT JOIN LATERAL (
			SELECT c.source_display_name AS display_name FROM book_contributor_credit c
			WHERE c.source_fingerprint = i.source_fingerprint ORDER BY c.id LIMIT 1
		) src ON true
		LEFT JOIN LATERAL (
			SELECT count(*) AS credits FROM book_contributor_credit c
			JOIN book_metadata_snapshot s ON s.id = c.snapshot_id
			WHERE c.source_fingerprint = i.source_fingerprint AND s.is_current AND c.role = 'author'
		) cc ON true
		WHERE i.id = ?`, itemID)
	if err != nil {
		return reviewListRow{}, fmt.Errorf("reading the review item's summary: %w", err)
	}
	return row, nil
}

func strOrEmpty2(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Detail serves one item of either status with its source, proposal and the
// bounded linked books — the only response titles appear in.
func (a *authorMetadataReviewAdapter) Detail(ctx context.Context, itemID int64) (AuthorMetadataReviewDetail, error) {
	if itemID <= 0 {
		return AuthorMetadataReviewDetail{}, ErrAuthorMetadataReviewNotFound
	}
	var item models.ContributorReviewItem
	if err := a.db.ModelContext(ctx, &item).Where("id = ?", itemID).Select(); err != nil {
		if errors.Is(err, pg.ErrNoRows) {
			return AuthorMetadataReviewDetail{}, ErrAuthorMetadataReviewNotFound
		}
		return AuthorMetadataReviewDetail{}, fmt.Errorf("reading the review item: %w", err)
	}

	row, err := a.reviewSummaryRow(ctx, itemID)
	if err != nil {
		return AuthorMetadataReviewDetail{}, err
	}

	source, err := a.reviewSource(ctx, item.SourceFingerprint)
	if err != nil {
		return AuthorMetadataReviewDetail{}, err
	}
	detail := AuthorMetadataReviewDetail{
		AuthorMetadataReviewListItem: reviewListItem(&row),
		Source:                       source,
		LinkedBooks:                  []AuthorMetadataReviewLinkedBook{},
	}
	if item.ProposalResultID != nil {
		var result models.ContributorNormalizationResult
		if err = a.db.ModelContext(ctx, &result).Where("id = ?", *item.ProposalResultID).Select(); err != nil {
			return AuthorMetadataReviewDetail{}, fmt.Errorf("reading the proposal: %w", err)
		}
		var additional *string
		if len(result.AdditionalNames) > 0 {
			additional = &result.AdditionalNames[0]
		}
		detail.Proposal = &AuthorMetadataReviewProposal{
			GivenName: result.GivenName, AdditionalName: additional, FamilyName: result.FamilyName,
			Nickname: result.Nickname, DisplayName: strOrEmpty2(result.DisplayName),
			SortName: result.SortName, SearchKey: strOrEmpty2(result.SearchKey),
			Script: strOrEmpty2(result.Script), Kind: string(result.Kind), Method: string(result.Method),
			QualityFlags: nonEmptyFlags(result.QualityFlags),
		}
	}
	linked, err := database.LinkedBooksOfFingerprint(ctx, a.db, item.SourceFingerprint, reviewLinkedBooksCap)
	if err != nil {
		return AuthorMetadataReviewDetail{}, err
	}
	for _, book := range linked {
		detail.LinkedBooks = append(detail.LinkedBooks,
			AuthorMetadataReviewLinkedBook{ID: book.BookID, Title: book.Title})
	}
	return detail, nil
}

// reviewLinkedBooksCap is the contract's "at most 10" linked books.
const reviewLinkedBooksCap = 10

func nonEmptyFlags(flags []string) []string {
	if flags == nil {
		return []string{}
	}
	return flags
}

// reviewSource reads the canonical source of one fingerprint from the credit
// that carries it: every credit of a fingerprint holds byte-identical source
// fields, so the lowest-ID credit is the representative.
func (a *authorMetadataReviewAdapter) reviewSource(
	ctx context.Context, fingerprint []byte,
) (AuthorMetadataReviewSource, error) {
	var source struct {
		First    *string  `pg:"first"`
		Middle   *string  `pg:"middle"`
		Last     *string  `pg:"last"`
		Nickname *string  `pg:"nickname"`
		Display  string   `pg:"display"`
		Flags    []string `pg:"flags,array"`
	}
	_, err := a.db.QueryOneContext(ctx, &source, `SELECT source_first_name AS first,
			source_middle_name AS middle, source_last_name AS last, source_nickname AS nickname,
			source_display_name AS display, quality_flags AS flags
		FROM book_contributor_credit
		WHERE source_fingerprint = ?
		ORDER BY id LIMIT 1`, fingerprint)
	if errors.Is(err, pg.ErrNoRows) {
		// A fingerprint-only item whose credits are all superseded shows its
		// fingerprint and nothing else.
		return AuthorMetadataReviewSource{Flags: []string{}}, nil
	}
	if err != nil {
		return AuthorMetadataReviewSource{}, fmt.Errorf("reading the item's source: %w", err)
	}
	return AuthorMetadataReviewSource{
		First: source.First, Middle: source.Middle, Last: source.Last, Nickname: source.Nickname,
		Display: source.Display, Flags: nonEmptyFlags(source.Flags),
	}, nil
}

// act runs one decision through the phase-11 service after confirming the
// scope the admin's client confirmed matches the item's own.
func (a *authorMetadataReviewAdapter) act(
	ctx context.Context, itemID int64, scope string,
	decision func() (database.ReviewReport, error),
) error {
	if err := a.confirmScope(ctx, itemID, scope); err != nil {
		return err
	}
	if _, err := decision(); err != nil {
		return translateReviewError(err)
	}
	return nil
}

// confirmScope checks the request's scope against the item's own.
func (a *authorMetadataReviewAdapter) confirmScope(ctx context.Context, itemID int64, scope string) error {
	var item struct {
		ScopeCreditID *int64
	}
	_, err := a.db.QueryOneContext(ctx, &item,
		`SELECT scope_credit_id FROM contributor_review_item WHERE id = ?`, itemID)
	if errors.Is(err, pg.ErrNoRows) {
		return ErrAuthorMetadataReviewNotFound
	}
	if err != nil {
		return fmt.Errorf("reading the review item's scope: %w", err)
	}
	actual := reviewScopeFingerprint
	if item.ScopeCreditID != nil {
		actual = reviewScopeCredit
	}
	if actual != scope {
		return ErrAuthorMetadataReviewScopeMismatch
	}
	return nil
}

// translateReviewError maps the service's identities onto the API's.
func translateReviewError(err error) error {
	switch {
	case errors.Is(err, database.ErrReviewNotFound):
		return ErrAuthorMetadataReviewNotFound
	case errors.Is(err, database.ErrReviewConflict):
		return ErrAuthorMetadataReviewConflict
	case errors.Is(err, database.ErrReviewRetryExhausted):
		return ErrAuthorMetadataReviewRetryExhausted
	}
	return err
}

func (a *authorMetadataReviewAdapter) Accept(ctx context.Context, itemID, admin int64, scope string) error {
	return a.act(ctx, itemID, scope, func() (database.ReviewReport, error) {
		return a.svc.Accept(ctx, itemID, admin)
	})
}

func (a *authorMetadataReviewAdapter) Edit(
	ctx context.Context, itemID, admin int64, scope string, result AuthorMetadataReviewEditResult,
) error {
	return a.act(ctx, itemID, scope, func() (database.ReviewReport, error) {
		return a.svc.Edit(ctx, itemID, admin, &services.AdminCorrection{
			GivenName: strOrEmpty2(result.GivenName), AdditionalName: strOrEmpty2(result.AdditionalName),
			FamilyName: strOrEmpty2(result.FamilyName), Nickname: strOrEmpty2(result.Nickname),
			DisplayName: result.DisplayName, SortName: strOrEmpty2(result.SortName),
			Kind: models.NormalizationKind(result.Kind),
		})
	})
}

func (a *authorMetadataReviewAdapter) Classify(ctx context.Context, itemID, admin int64, scope, kind string) error {
	return a.act(ctx, itemID, scope, func() (database.ReviewReport, error) {
		return a.svc.Classify(ctx, itemID, admin, models.NormalizationKind(kind))
	})
}

func (a *authorMetadataReviewAdapter) LeaveUnresolved(ctx context.Context, itemID, admin int64, scope string) error {
	return a.act(ctx, itemID, scope, func() (database.ReviewReport, error) {
		return a.svc.LeaveUnresolved(ctx, itemID, admin)
	})
}

// Retry re-runs the local normalizer; the contract sends no body, and the
// item's scope needs no confirmation.
func (a *authorMetadataReviewAdapter) Retry(ctx context.Context, itemID, admin int64) error {
	if _, err := a.svc.Retry(ctx, itemID, admin); err != nil {
		return translateReviewError(err)
	}
	return nil
}

// --- request decoding and validation ---

// reviewScopeValue is the closed scope vocabulary of the action bodies.
const (
	reviewScopeCredit      = "credit"
	reviewScopeFingerprint = "fingerprint"
)

// reviewListLimitDefault and reviewListLimitMax bound the queue pages.
const (
	reviewListLimitDefault = 50
	reviewListLimitMax     = 100
)

type reviewActionRequest struct {
	Scope string `json:"scope"`
}

type reviewEditResultRequest struct {
	GivenName      *string `json:"given_name"`
	AdditionalName *string `json:"additional_names"`
	FamilyName     *string `json:"family_name"`
	Nickname       *string `json:"nickname"`
	DisplayName    string  `json:"display_name"`
	SortName       *string `json:"sort_name"`
	Kind           string  `json:"kind"`
}

type reviewEditRequest struct {
	Scope  string                  `json:"scope"`
	Result reviewEditResultRequest `json:"result"`
}

type reviewClassifyRequest struct {
	Scope string `json:"scope"`
	Kind  string `json:"kind"`
}

// reviewNullableEditPaths are the edit result's optional name fields, sent as
// null when absent: the one allowance the strict decoder makes for this API.
var reviewNullableEditPaths = []string{
	"result.given_name", "result.additional_names", "result.family_name",
	"result.nickname", "result.sort_name",
}

func decodeReviewAction(c *gin.Context, into any) error {
	return decodeStrict(c, into)
}

func decodeReviewEdit(c *gin.Context, req *reviewEditRequest) error {
	return decodeStrictNullable(c, req, reviewNullableEditPaths...)
}

func (r *reviewActionRequest) validate() error {
	return validateReviewScope(r.Scope)
}

func validateReviewScope(scope string) error {
	if scope != reviewScopeCredit && scope != reviewScopeFingerprint {
		return runRequestError{codeInvalidScope}
	}
	return nil
}

func (r *reviewClassifyRequest) validate() error {
	if err := validateReviewScope(r.Scope); err != nil {
		return err
	}
	switch models.NormalizationKind(r.Kind) {
	case models.NormalizationCollective, models.NormalizationUnknown, models.NormalizationMalformed:
		return nil
	case models.NormalizationPerson:
		return runRequestError{codeInvalidKind}
	}
	return runRequestError{codeInvalidKind}
}

func (r *reviewEditRequest) validate() error {
	if err := validateReviewScope(r.Scope); err != nil {
		return err
	}
	if r.Result == (reviewEditResultRequest{}) {
		return runRequestError{codeInvalidResult}
	}
	switch models.NormalizationKind(r.Result.Kind) {
	case models.NormalizationPerson, models.NormalizationCollective, models.NormalizationUnknown,
		models.NormalizationMalformed:
	default:
		return runRequestError{codeInvalidKind}
	}
	if strings.TrimSpace(r.Result.DisplayName) == "" {
		return runRequestError{codeInvalidResult}
	}
	return nil
}

// encodeReviewCursor renders the opaque continuation cursor.
func encodeReviewCursor(c AuthorMetadataReviewCursor) string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(strconv.FormatInt(c.CreatedAt.UnixNano(), 10) + "." + strconv.FormatInt(c.ID, 10)))
}

// decodeReviewCursor parses a cursor this API issued; anything else is an
// invalid_request-shaped 400 with its own code.
func decodeReviewCursor(raw string) (*AuthorMetadataReviewCursor, error) {
	if raw == "" {
		return nil, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, runRequestError{codeInvalidCursor}
	}
	stamp, id, found := strings.Cut(string(payload), ".")
	if !found {
		return nil, runRequestError{codeInvalidCursor}
	}
	nanos, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil || nanos <= 0 {
		return nil, runRequestError{codeInvalidCursor}
	}
	parsedID, err := strconv.ParseInt(id, 10, 64)
	if err != nil || parsedID <= 0 {
		return nil, runRequestError{codeInvalidCursor}
	}
	createdAt := time.Unix(0, nanos).UTC()
	if createdAt.UnixNano() != nanos {
		return nil, runRequestError{codeInvalidCursor}
	}
	return &AuthorMetadataReviewCursor{CreatedAt: createdAt, ID: parsedID}, nil
}

// reviewListQuery validates the list's query parameters.
func reviewListQuery(c *gin.Context) (AuthorMetadataReviewListQuery, error) {
	status := c.Query("status")
	if status != "open" && status != "closed" {
		return AuthorMetadataReviewListQuery{}, runRequestError{codeInvalidStatus}
	}
	limit := reviewListLimitDefault
	if raw := c.Query("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > reviewListLimitMax || strconv.Itoa(parsed) != raw {
			return AuthorMetadataReviewListQuery{}, runRequestError{codeInvalidLimit}
		}
		limit = parsed
	}
	after, err := decodeReviewCursor(c.Query("cursor"))
	if err != nil {
		return AuthorMetadataReviewListQuery{}, err
	}
	return AuthorMetadataReviewListQuery{Status: status, Limit: limit, After: after}, nil
}

// --- routes and handlers ---

// SetupAuthorMetadataReviewRoutes registers the review routes on a group
// that is already behind the admin middleware.
func SetupAuthorMetadataReviewRoutes(r *gin.RouterGroup, svc AuthorMetadataReviewService) {
	h := &authorMetadataReviewsHandler{svc: svc}
	review := r.Group("/review")
	review.GET("", h.list)
	review.GET("/:id", h.detail)
	review.POST("/:id/accept", h.accept)
	review.POST("/:id/edit", h.edit)
	review.POST("/:id/classify", h.classify)
	review.POST("/:id/unresolved", h.unresolved)
	review.POST("/:id/retry", h.retry)
}

type authorMetadataReviewsHandler struct {
	svc AuthorMetadataReviewService
}

// list godoc
// @Summary Author review queue
// @Description One page of open or closed review items with the (created_at, id) continuation cursor.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Produce json
// @Param status query string true "open or closed"
// @Param cursor query string false "Continuation cursor from a previous page"
// @Param limit query int false "1..100, default 50"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/admin/author-metadata/review [get]
func (h *authorMetadataReviewsHandler) list(c *gin.Context) {
	q, err := reviewListQuery(c)
	if err != nil {
		abortReviewError(c, err)
		return
	}
	page, err := h.svc.List(c.Request.Context(), q)
	if err != nil {
		abortReviewError(c, err)
		return
	}
	presented := make([]AuthorMetadataReviewListItem, 0, len(page.Items))
	for i := range page.Items {
		presented = append(presented, presentReviewListItem(&page.Items[i]))
	}
	response := gin.H{jsonKeyItems: presented, "next_cursor": nil}
	if page.Next != nil {
		response["next_cursor"] = encodeReviewCursor(*page.Next)
	}
	c.JSON(http.StatusOK, response)
}

// detail godoc
// @Summary Author review item
// @Description The item with its source components, its proposal and its linked books.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Review item ID"
// @Produce json
// @Success 200 {object} map[string]AuthorMetadataReviewDetail
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/admin/author-metadata/review/{id} [get]
func (h *authorMetadataReviewsHandler) detail(c *gin.Context) {
	id, err := runID(c)
	if err != nil {
		abortReviewError(c, err)
		return
	}
	detail, err := h.svc.Detail(c.Request.Context(), id)
	if err != nil {
		abortReviewError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{jsonKeyItem: presentReviewDetail(&detail)})
}

// accept godoc
// @Summary Accept an author review item's proposal
// @Description Selects the proposal for the item's scope; answers the decided item.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Review item ID"
// @Accept json
// @Produce json
// @Param body body reviewActionRequest true "The scope being confirmed"
// @Success 200 {object} map[string]AuthorMetadataReviewDetail
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/admin/author-metadata/review/{id}/accept [post]
func (h *authorMetadataReviewsHandler) accept(c *gin.Context) {
	h.scopeAction(c, func(ctx context.Context, id, admin int64, scope string) error {
		return h.svc.Accept(ctx, id, admin, scope)
	})
}

// edit godoc
// @Summary Correct an author review item's name
// @Description Selects the admin's correction for the item's scope; answers the decided item.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Review item ID"
// @Accept json
// @Produce json
// @Param body body reviewEditRequest true "The scope and the corrected result"
// @Success 200 {object} map[string]AuthorMetadataReviewDetail
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/admin/author-metadata/review/{id}/edit [post]
func (h *authorMetadataReviewsHandler) edit(c *gin.Context) {
	id, err := runID(c)
	if err != nil {
		abortReviewError(c, err)
		return
	}
	var req reviewEditRequest
	if err = decodeReviewEdit(c, &req); err != nil {
		abortReviewError(c, err)
		return
	}
	if err = req.validate(); err != nil {
		abortReviewError(c, err)
		return
	}
	actor, err := actorID(c)
	if err != nil {
		abortRun(c, http.StatusInternalServerError, codeInternalError)
		return
	}
	if err = h.svc.Edit(c.Request.Context(), id, actor, req.Scope, AuthorMetadataReviewEditResult{
		GivenName: req.Result.GivenName, AdditionalName: req.Result.AdditionalName,
		FamilyName: req.Result.FamilyName, Nickname: req.Result.Nickname,
		DisplayName: req.Result.DisplayName, SortName: req.Result.SortName, Kind: req.Result.Kind,
	}); err != nil {
		abortReviewError(c, err)
		return
	}
	h.respondItem(c, id)
}

// classify godoc
// @Summary Classify an author review item's source
// @Description collective, unknown or malformed; malformed is the terminal invalid path. Answers the decided item.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Review item ID"
// @Accept json
// @Produce json
// @Param body body reviewClassifyRequest true "The scope and the kind"
// @Success 200 {object} map[string]AuthorMetadataReviewDetail
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/admin/author-metadata/review/{id}/classify [post]
func (h *authorMetadataReviewsHandler) classify(c *gin.Context) {
	id, err := runID(c)
	if err != nil {
		abortReviewError(c, err)
		return
	}
	var req reviewClassifyRequest
	if err = decodeReviewAction(c, &req); err != nil {
		abortReviewError(c, err)
		return
	}
	if err = req.validate(); err != nil {
		abortReviewError(c, err)
		return
	}
	actor, err := actorID(c)
	if err != nil {
		abortRun(c, http.StatusInternalServerError, codeInternalError)
		return
	}
	if err = h.svc.Classify(c.Request.Context(), id, actor, req.Scope, req.Kind); err != nil {
		abortReviewError(c, err)
		return
	}
	h.respondItem(c, id)
}

// unresolved godoc
// @Summary Leave an author review item unresolved
// @Description Closes the item with its credits unresolved; answers the decided item.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Review item ID"
// @Accept json
// @Produce json
// @Param body body reviewActionRequest true "The scope being confirmed"
// @Success 200 {object} map[string]AuthorMetadataReviewDetail
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/admin/author-metadata/review/{id}/unresolved [post]
func (h *authorMetadataReviewsHandler) unresolved(c *gin.Context) {
	h.scopeAction(c, func(ctx context.Context, id, admin int64, scope string) error {
		return h.svc.LeaveUnresolved(ctx, id, admin, scope)
	})
}

// retry godoc
// @Summary Re-run the local normalizer for an author review item
// @Description Re-queues the item's input; no body. Answers the item.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Review item ID"
// @Produce json
// @Success 200 {object} map[string]AuthorMetadataReviewDetail
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/admin/author-metadata/review/{id}/retry [post]
func (h *authorMetadataReviewsHandler) retry(c *gin.Context) {
	id, err := runID(c)
	if err != nil {
		abortReviewError(c, err)
		return
	}
	actor, err := actorID(c)
	if err != nil {
		abortRun(c, http.StatusInternalServerError, codeInternalError)
		return
	}
	if err = h.svc.Retry(c.Request.Context(), id, actor); err != nil {
		abortReviewError(c, err)
		return
	}
	h.respondItem(c, id)
}

// scopeAction serves the actions that confirm only a scope.
func (h *authorMetadataReviewsHandler) scopeAction(
	c *gin.Context, action func(context.Context, int64, int64, string) error,
) {
	id, err := runID(c)
	if err != nil {
		abortReviewError(c, err)
		return
	}
	var req reviewActionRequest
	if err = decodeReviewAction(c, &req); err != nil {
		abortReviewError(c, err)
		return
	}
	if err = req.validate(); err != nil {
		abortReviewError(c, err)
		return
	}
	actor, err := actorID(c)
	if err != nil {
		abortRun(c, http.StatusInternalServerError, codeInternalError)
		return
	}
	if err = action(c.Request.Context(), id, actor, req.Scope); err != nil {
		abortReviewError(c, err)
		return
	}
	h.respondItem(c, id)
}

// respondItem answers with the decided item's detail.
func (h *authorMetadataReviewsHandler) respondItem(c *gin.Context, id int64) {
	detail, err := h.svc.Detail(c.Request.Context(), id)
	if err != nil {
		abortReviewError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{jsonKeyItem: presentReviewDetail(&detail)})
}

// presentReviewDetail normalizes a detail for the contract: UTC timestamps
// and non-empty lists.
func presentReviewDetail(detail *AuthorMetadataReviewDetail) AuthorMetadataReviewDetail {
	in := *detail
	in.AuthorMetadataReviewListItem = presentReviewListItem(&in.AuthorMetadataReviewListItem)
	if in.Source.Flags == nil {
		in.Source.Flags = []string{}
	}
	if in.Proposal != nil && in.Proposal.QualityFlags == nil {
		in.Proposal.QualityFlags = []string{}
	}
	if in.LinkedBooks == nil {
		in.LinkedBooks = []AuthorMetadataReviewLinkedBook{}
	}
	return in
}

// presentReviewListItem normalizes one list item: a UTC timestamp.
func presentReviewListItem(in *AuthorMetadataReviewListItem) AuthorMetadataReviewListItem {
	item := *in
	item.CreatedAt = item.CreatedAt.UTC()
	return item
}
