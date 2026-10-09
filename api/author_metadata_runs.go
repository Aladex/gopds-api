package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gopds-api/database"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
)

// Admin API of the author metadata runs (coordinator contract
// plans/briefs/authors-api-contract.md, "Runs"). The handlers only validate,
// call the run service and shape the JSON: no SQL, and no free text from a
// service, parser or database error ever reaches a response — every error is
// one closed code.

// maxRunBookIDs is the largest explicit book selector a run accepts.
const maxRunBookIDs = 10000

// Top-level keys of the runs responses.
const (
	jsonKeyError  = "error"
	jsonKeyRun    = "run"
	jsonKeyReport = "report"
)

// Run modes and retry stages of the contract.
const (
	runModeSmoke        = string(models.AuthorMetadataRunSmoke)
	runModePilotArchive = string(models.AuthorMetadataRunPilotArchive)
	runModeFull         = string(models.AuthorMetadataRunFull)

	retryStageExtraction = "extraction"
	retryStageLocal      = "local"
)

// Closed error codes of the runs API.
const (
	codeInvalidRequest        = "invalid_request"
	codeInvalidMode           = "invalid_mode"
	codeInvalidSelector       = "invalid_selector"
	codeTooManyBookIDs        = "too_many_book_ids"
	codeInvalidBookID         = "invalid_book_id"
	codeDuplicateBookID       = "duplicate_book_id"
	codeInvalidArchive        = "invalid_archive"
	codeInvalidID             = "invalid_id"
	codeInvalidStage          = "invalid_stage"
	codeInvalidErrorClass     = "invalid_error_class"
	codeRunNotFound           = "run_not_found"
	codeActiveRunExists       = "active_run_exists"
	codeFullRunNotApproved    = "full_run_not_approved"
	codeInvalidTransition     = "invalid_transition"
	codeNotACompletedPilot    = "not_a_completed_pilot"
	codeAlreadyApproved       = "already_approved"
	codeInternalError         = "internal_error"
	codeRunServiceUnavailable = "run_service_unavailable"
)

// Errors a run service returns; each maps to one closed code. A service may
// wrap them with any detail — only the identity is used.
var (
	ErrAuthorMetadataRunNotFound           = errors.New("api: author metadata run not found")
	ErrAuthorMetadataActiveRunExists       = errors.New("api: an author metadata run is already active")
	ErrAuthorMetadataFullRunNotApproved    = errors.New("api: no approved pilot matches this full run")
	ErrAuthorMetadataInvalidTransition     = errors.New("api: the run cannot make this transition")
	ErrAuthorMetadataNotACompletedPilot    = errors.New("api: the run is not a completed pilot")
	ErrAuthorMetadataAlreadyApproved       = errors.New("api: the pilot is already approved")
	ErrAuthorMetadataRunServiceUnavailable = errors.New("api: the author metadata run service is not wired")
)

// runErrorStatus maps a service error to its HTTP status and closed code.
// Anything unrecognized is an internal error; its text is never shown.
func runErrorStatus(err error) (status int, code string) {
	switch {
	case errors.Is(err, ErrAuthorMetadataRunNotFound):
		return http.StatusNotFound, codeRunNotFound
	case errors.Is(err, ErrAuthorMetadataActiveRunExists):
		return http.StatusConflict, codeActiveRunExists
	case errors.Is(err, ErrAuthorMetadataFullRunNotApproved):
		return http.StatusConflict, codeFullRunNotApproved
	case errors.Is(err, ErrAuthorMetadataInvalidTransition):
		return http.StatusConflict, codeInvalidTransition
	case errors.Is(err, ErrAuthorMetadataNotACompletedPilot):
		return http.StatusConflict, codeNotACompletedPilot
	case errors.Is(err, ErrAuthorMetadataAlreadyApproved):
		return http.StatusConflict, codeAlreadyApproved
	case errors.Is(err, ErrAuthorMetadataRunServiceUnavailable):
		return http.StatusInternalServerError, codeRunServiceUnavailable
	}
	return http.StatusInternalServerError, codeInternalError
}

// retryErrorClasses are the closed error classes a retry may reopen, per
// stage. The extraction list is built from the same constants the extraction
// path records (the per-book failure statuses and the lease classes); once
// the phase-8 run service publishes its own closed list, this must switch to
// it. The local list is the local worker's closed set.
var retryErrorClasses = map[string]map[string]bool{
	retryStageExtraction: setOf(
		string(models.AuthorMetadataRunItemEntryMissing),
		string(models.AuthorMetadataRunItemInvalidFB2),
		string(models.AuthorMetadataRunItemUnsupportedEncoding),
		string(models.AuthorMetadataRunItemMetadataParseFailed),
		database.LeaseErrorLeaseExpired,
		database.LeaseErrorMaxAttemptsExceeded,
		string(services.AuthorMetadataErrorTransientDatabase),
	),
	retryStageLocal: localRetryClasses(),
}

func localRetryClasses() map[string]bool {
	classes := make([]string, 0, len(services.AuthorMetadataErrorClasses()))
	for _, class := range services.AuthorMetadataErrorClasses() {
		classes = append(classes, string(class))
	}
	return setOf(classes...)
}

func setOf(values ...string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, v := range values {
		set[v] = true
	}
	return set
}

// AuthorMetadataRunStart is a validated start request: exactly one selector
// for smoke and pilot_archive, none for full.
type AuthorMetadataRunStart struct {
	Mode    string
	BookIDs []int64
	Archive *string
}

// AuthorMetadataExtractionByStatus counts extraction items by terminal
// status; every status is always present.
type AuthorMetadataExtractionByStatus struct {
	Extracted           int64 `json:"extracted"`
	ExtractedNoAuthor   int64 `json:"extracted_no_author"`
	AlreadyCurrent      int64 `json:"already_current"`
	EntryMissing        int64 `json:"entry_missing"`
	InvalidFB2          int64 `json:"invalid_fb2"`
	UnsupportedEncoding int64 `json:"unsupported_encoding"`
	MetadataParseFailed int64 `json:"metadata_parse_failed"`
}

// AuthorMetadataExtractionStage is the extraction stream of a run.
type AuthorMetadataExtractionStage struct {
	Total             int64                            `json:"total"`
	Done              int64                            `json:"done"`
	Pending           int64                            `json:"pending"`
	Leased            int64                            `json:"leased"`
	OldestPendingAgeS int64                            `json:"oldest_pending_age_s"`
	ByStatus          AuthorMetadataExtractionByStatus `json:"by_status"`
	CurrentArchive    *string                          `json:"current_archive"`
	ItemsPerMinute    float64                          `json:"items_per_minute"`
}

// AuthorMetadataLocalStage is the local normalization stream of a run.
type AuthorMetadataLocalStage struct {
	Total             int64 `json:"total"`
	Done              int64 `json:"done"`
	Pending           int64 `json:"pending"`
	Leased            int64 `json:"leased"`
	Failed            int64 `json:"failed"`
	OldestPendingAgeS int64 `json:"oldest_pending_age_s"`
}

// AuthorMetadataReviewStage is the manual review backlog of a run.
type AuthorMetadataReviewStage struct {
	Open   int64 `json:"open"`
	Closed int64 `json:"closed"`
}

// AuthorMetadataRunStages are the three streams of a run (scope amendment
// A6: there is no LLM stage).
type AuthorMetadataRunStages struct {
	Extraction AuthorMetadataExtractionStage `json:"extraction"`
	Local      AuthorMetadataLocalStage      `json:"local"`
	Review     AuthorMetadataReviewStage     `json:"review"`
}

// AuthorMetadataCreditCounts accounts for every current author credit;
// Unresolved is keyed by closed reason.
type AuthorMetadataCreditCounts struct {
	Selected   int64            `json:"selected"`
	Invalid    int64            `json:"invalid"`
	Review     int64            `json:"review"`
	Pending    int64            `json:"pending"`
	Unresolved map[string]int64 `json:"unresolved"`
}

// AuthorMetadataRunView is the contract's Run object.
type AuthorMetadataRunView struct {
	ID                    int64                      `json:"id"`
	Mode                  string                     `json:"mode"`
	Status                string                     `json:"status"`
	ExtractorVersion      string                     `json:"extractor_version"`
	NormalizerVersion     string                     `json:"normalizer_version"`
	CreatedAt             time.Time                  `json:"created_at"`
	StartedAt             *time.Time                 `json:"started_at"`
	ExtractionCompletedAt *time.Time                 `json:"extraction_completed_at"`
	CompletedAt           *time.Time                 `json:"completed_at"`
	LastErrorClass        *string                    `json:"last_error_class"`
	ApprovedForFull       bool                       `json:"approved_for_full"`
	Stages                AuthorMetadataRunStages    `json:"stages"`
	Credits               AuthorMetadataCreditCounts `json:"credits"`
}

// AuthorMetadataRunReport is the contract's Report object: the Run fields
// plus the readiness verdict.
type AuthorMetadataRunReport struct {
	AuthorMetadataRunView
	Ready           bool             `json:"ready"`
	NotReadyReasons []string         `json:"not_ready_reasons"`
	DurationS       int64            `json:"duration_s"`
	DBGrowthBytes   int64            `json:"db_growth_bytes"`
	ByClass         map[string]int64 `json:"by_class"`
	ByScript        map[string]int64 `json:"by_script"`
}

// AuthorMetadataRunService is what the handlers need from the run service.
// Errors are the ErrAuthorMetadata* identities above (possibly wrapped).
type AuthorMetadataRunService interface {
	Start(ctx context.Context, start AuthorMetadataRunStart, actorUserID int64) (AuthorMetadataRunView, error)
	Current(ctx context.Context) (*AuthorMetadataRunView, error)
	Get(ctx context.Context, id int64) (AuthorMetadataRunView, error)
	Report(ctx context.Context, id int64) (AuthorMetadataRunReport, error)
	Pause(ctx context.Context, id int64) (AuthorMetadataRunView, error)
	Resume(ctx context.Context, id int64) (AuthorMetadataRunView, error)
	ApproveFull(ctx context.Context, id, actorUserID int64) (AuthorMetadataRunView, error)
	Retry(ctx context.Context, id int64, stage, errorClass string) (int64, error)
}

// authorMetadataRunService is the single wiring point of the concrete run
// service, used by SetupAdminRoutes.
//
// PHASE-8 WIRING POINT: return an adapter over
// services/author_metadata_run_service.go here once it lands. Until then the
// routes exist and every call answers 500 run_service_unavailable.
var authorMetadataRunService = func() AuthorMetadataRunService {
	return unavailableAuthorMetadataRuns{}
}

// unavailableAuthorMetadataRuns stands in until the run service is wired.
type unavailableAuthorMetadataRuns struct{}

func (unavailableAuthorMetadataRuns) Start(context.Context, AuthorMetadataRunStart, int64) (AuthorMetadataRunView, error) {
	return AuthorMetadataRunView{}, ErrAuthorMetadataRunServiceUnavailable
}

func (unavailableAuthorMetadataRuns) Current(context.Context) (*AuthorMetadataRunView, error) {
	return nil, ErrAuthorMetadataRunServiceUnavailable
}

func (unavailableAuthorMetadataRuns) Get(context.Context, int64) (AuthorMetadataRunView, error) {
	return AuthorMetadataRunView{}, ErrAuthorMetadataRunServiceUnavailable
}

func (unavailableAuthorMetadataRuns) Report(context.Context, int64) (AuthorMetadataRunReport, error) {
	return AuthorMetadataRunReport{}, ErrAuthorMetadataRunServiceUnavailable
}

func (unavailableAuthorMetadataRuns) Pause(context.Context, int64) (AuthorMetadataRunView, error) {
	return AuthorMetadataRunView{}, ErrAuthorMetadataRunServiceUnavailable
}

func (unavailableAuthorMetadataRuns) Resume(context.Context, int64) (AuthorMetadataRunView, error) {
	return AuthorMetadataRunView{}, ErrAuthorMetadataRunServiceUnavailable
}

func (unavailableAuthorMetadataRuns) ApproveFull(context.Context, int64, int64) (AuthorMetadataRunView, error) {
	return AuthorMetadataRunView{}, ErrAuthorMetadataRunServiceUnavailable
}

func (unavailableAuthorMetadataRuns) Retry(context.Context, int64, string, string) (int64, error) {
	return 0, ErrAuthorMetadataRunServiceUnavailable
}

// authorMetadataRunsHandler serves the runs routes.
type authorMetadataRunsHandler struct {
	svc AuthorMetadataRunService
}

// SetupAuthorMetadataRunRoutes registers the runs routes on a group that is
// already behind the admin middleware.
func SetupAuthorMetadataRunRoutes(r *gin.RouterGroup, svc AuthorMetadataRunService) {
	h := &authorMetadataRunsHandler{svc: svc}
	runs := r.Group("/runs")
	runs.POST("", h.start)
	runs.GET("/current", h.current)
	runs.GET("/:id", h.get)
	runs.GET("/:id/report", h.report)
	runs.POST("/:id/pause", h.pause)
	runs.POST("/:id/resume", h.resume)
	runs.POST("/:id/approve-full", h.approveFull)
	runs.POST("/:id/retry", h.retry)
}

// --- request validation and error mapping, shared by every handler ---

// runRequestError is a request the handler refuses with 400 and a code.
type runRequestError struct{ code string }

func (e runRequestError) Error() string { return e.code }

func abortRun(c *gin.Context, status int, code string) {
	c.AbortWithStatusJSON(status, gin.H{jsonKeyError: code})
}

// abortRunError answers a validation or service error with its closed code.
func abortRunError(c *gin.Context, err error) {
	var reqErr runRequestError
	if errors.As(err, &reqErr) {
		abortRun(c, http.StatusBadRequest, reqErr.code)
		return
	}
	status, code := runErrorStatus(err)
	abortRun(c, status, code)
}

// runID parses the :id path parameter: a positive decimal int64, nothing else.
func runID(c *gin.Context) (int64, error) {
	raw := c.Param("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != raw {
		return 0, runRequestError{codeInvalidID}
	}
	return id, nil
}

// actorID is the administrator AdminMiddleware authenticated.
func actorID(c *gin.Context) (int64, error) {
	id, ok := c.Get("user_id")
	if !ok {
		return 0, errors.New("no authenticated administrator")
	}
	actor, ok := id.(int64)
	if !ok || actor <= 0 {
		return 0, errors.New("no authenticated administrator")
	}
	return actor, nil
}

// decodeStrict decodes a body that is exactly one JSON object whose field
// names are the json tags of into's struct, each present at most once and
// never null, with
// nothing after the object but whitespace. encoding/json alone would match
// field names case-insensitively, let a repeated field overwrite the first,
// read a null as an absent value and stop before malformed trailing
// delimiters; each of those is an invalid_request here. Field types are then
// checked by decoding into the request struct.
func decodeStrict(c *gin.Context, into any) error {
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return runRequestError{codeInvalidRequest}
	}
	if structuralErr := checkRequestObject(raw, jsonFieldNames(into)); structuralErr != nil {
		return structuralErr
	}
	if err = json.Unmarshal(raw, into); err != nil {
		return runRequestError{codeInvalidRequest}
	}
	return nil
}

// jsonFieldNames lists the json tag names of the struct into points to: the
// request types declare their fields once.
func jsonFieldNames(into any) []string {
	t := reflect.TypeOf(into).Elem()
	names := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		if name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ","); name != "" && name != "-" {
			names = append(names, name)
		}
	}
	return names
}

// checkRequestObject walks the top-level object token by token.
func checkRequestObject(raw []byte, fields []string) error {
	invalid := runRequestError{codeInvalidRequest}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return invalid
	}
	allowed := setOf(fields...)
	seen := make(map[string]bool, len(fields))
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return invalid
		}
		key, ok := tok.(string)
		if !ok || !allowed[key] || seen[key] {
			return invalid
		}
		seen[key] = true
		var value json.RawMessage
		if err = dec.Decode(&value); err != nil || containsNull(value) {
			return invalid
		}
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return invalid
	}
	// Only whitespace may follow: a stray delimiter or a second value is a
	// token (or a syntax error), not io.EOF.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return invalid
	}
	return nil
}

// containsNull reports whether a JSON value is null or holds a null at any
// depth. No request field or element is nullable.
func containsNull(value json.RawMessage) bool {
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return true
	}
	var walk func(v any) bool
	walk = func(v any) bool {
		switch t := v.(type) {
		case nil:
			return true
		case []any:
			for _, e := range t {
				if walk(e) {
					return true
				}
			}
		case map[string]any:
			for _, e := range t {
				if walk(e) {
					return true
				}
			}
		}
		return false
	}
	return walk(decoded)
}

// startRunRequest is the POST /runs body.
type startRunRequest struct {
	Mode    string  `json:"mode"`
	BookIDs []int64 `json:"book_ids"`
	Archive *string `json:"archive"`
}

// validate applies the contract: smoke and pilot_archive take exactly one
// selector, full none; book IDs are 1..10000 positive unique values; the
// archive is a bare file name.
func (r *startRunRequest) validate() (AuthorMetadataRunStart, error) {
	switch r.Mode {
	case runModeSmoke, runModePilotArchive, runModeFull:
	default:
		return AuthorMetadataRunStart{}, runRequestError{codeInvalidMode}
	}
	hasBookIDs := len(r.BookIDs) > 0
	hasArchive := r.Archive != nil
	if r.Mode == runModeFull {
		if r.BookIDs != nil || hasArchive {
			return AuthorMetadataRunStart{}, runRequestError{codeInvalidSelector}
		}
		return AuthorMetadataRunStart{Mode: r.Mode}, nil
	}
	if hasBookIDs == hasArchive {
		return AuthorMetadataRunStart{}, runRequestError{codeInvalidSelector}
	}
	if hasArchive {
		if !validArchiveName(*r.Archive) {
			return AuthorMetadataRunStart{}, runRequestError{codeInvalidArchive}
		}
		archive := *r.Archive
		return AuthorMetadataRunStart{Mode: r.Mode, Archive: &archive}, nil
	}
	if len(r.BookIDs) > maxRunBookIDs {
		return AuthorMetadataRunStart{}, runRequestError{codeTooManyBookIDs}
	}
	seen := make(map[int64]bool, len(r.BookIDs))
	for _, id := range r.BookIDs {
		if id <= 0 {
			return AuthorMetadataRunStart{}, runRequestError{codeInvalidBookID}
		}
		if seen[id] {
			return AuthorMetadataRunStart{}, runRequestError{codeDuplicateBookID}
		}
		seen[id] = true
	}
	return AuthorMetadataRunStart{Mode: r.Mode, BookIDs: append([]int64(nil), r.BookIDs...)}, nil
}

// validArchiveName accepts a bare archive file name as the catalog stores it
// in opds_catalog_book.path: no directory part, no padding, not . or ..
func validArchiveName(name string) bool {
	return name != "" && name == strings.TrimSpace(name) && name != "." && name != ".." &&
		!strings.ContainsAny(name, `/\`)
}

// retryRunRequest is the POST /runs/:id/retry body.
type retryRunRequest struct {
	Stage      string `json:"stage"`
	ErrorClass string `json:"error_class"`
}

func (r *retryRunRequest) validate() error {
	classes, ok := retryErrorClasses[r.Stage]
	if !ok {
		return runRequestError{codeInvalidStage}
	}
	if !classes[r.ErrorClass] {
		return runRequestError{codeInvalidErrorClass}
	}
	return nil
}

// --- response shaping ---

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func nonNilCounts(m map[string]int64) map[string]int64 {
	if m == nil {
		return map[string]int64{}
	}
	return m
}

// presentRun normalizes a run for the contract: UTC timestamps, {} for an
// empty breakdown.
func presentRun(in *AuthorMetadataRunView) AuthorMetadataRunView {
	run := *in
	run.CreatedAt = run.CreatedAt.UTC()
	run.StartedAt = utcPtr(run.StartedAt)
	run.ExtractionCompletedAt = utcPtr(run.ExtractionCompletedAt)
	run.CompletedAt = utcPtr(run.CompletedAt)
	run.Credits.Unresolved = nonNilCounts(run.Credits.Unresolved)
	return run
}

// presentReport normalizes a report: the run part as presentRun, [] and {}
// for empty lists and breakdowns.
func presentReport(in *AuthorMetadataRunReport) AuthorMetadataRunReport {
	report := *in
	report.AuthorMetadataRunView = presentRun(&in.AuthorMetadataRunView)
	if report.NotReadyReasons == nil {
		report.NotReadyReasons = []string{}
	}
	report.ByClass = nonNilCounts(report.ByClass)
	report.ByScript = nonNilCounts(report.ByScript)
	return report
}

// --- handlers ---

// start godoc
// @Summary Start an author metadata run
// @Description smoke and pilot_archive take exactly one selector (book_ids or archive), full none.
// @Description 409 active_run_exists, 409 full_run_not_approved.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept json
// @Produce json
// @Param body body startRunRequest true "Run mode and selector"
// @Success 201 {object} map[string]AuthorMetadataRunView
// @Failure 400 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/admin/author-metadata/runs [post]
func (h *authorMetadataRunsHandler) start(c *gin.Context) {
	var req startRunRequest
	if err := decodeStrict(c, &req); err != nil {
		abortRunError(c, err)
		return
	}
	start, err := req.validate()
	if err != nil {
		abortRunError(c, err)
		return
	}
	actor, err := actorID(c)
	if err != nil {
		abortRun(c, http.StatusInternalServerError, codeInternalError)
		return
	}
	run, err := h.svc.Start(c.Request.Context(), start, actor)
	if err != nil {
		abortRunError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{jsonKeyRun: presentRun(&run)})
}

// current godoc
// @Summary Current author metadata run
// @Description The active run, or null when there is none.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Produce json
// @Success 200 {object} map[string]AuthorMetadataRunView
// @Failure 500 {object} map[string]string
// @Router /api/admin/author-metadata/runs/current [get]
func (h *authorMetadataRunsHandler) current(c *gin.Context) {
	run, err := h.svc.Current(c.Request.Context())
	if err != nil {
		abortRunError(c, err)
		return
	}
	if run == nil {
		c.JSON(http.StatusOK, gin.H{jsonKeyRun: nil})
		return
	}
	c.JSON(http.StatusOK, gin.H{jsonKeyRun: presentRun(run)})
}

// get godoc
// @Summary Author metadata run status
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Run ID"
// @Produce json
// @Success 200 {object} map[string]AuthorMetadataRunView
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/admin/author-metadata/runs/{id} [get]
func (h *authorMetadataRunsHandler) get(c *gin.Context) {
	h.runAction(c, h.svc.Get)
}

// report godoc
// @Summary Author metadata run report
// @Description The run plus the readiness verdict, the review backlog and the pending credits.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Run ID"
// @Produce json
// @Success 200 {object} map[string]AuthorMetadataRunReport
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/admin/author-metadata/runs/{id}/report [get]
func (h *authorMetadataRunsHandler) report(c *gin.Context) {
	id, err := runID(c)
	if err != nil {
		abortRunError(c, err)
		return
	}
	report, err := h.svc.Report(c.Request.Context(), id)
	if err != nil {
		abortRunError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{jsonKeyReport: presentReport(&report)})
}

// pause godoc
// @Summary Pause an author metadata run
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Run ID"
// @Produce json
// @Success 200 {object} map[string]AuthorMetadataRunView
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/admin/author-metadata/runs/{id}/pause [post]
func (h *authorMetadataRunsHandler) pause(c *gin.Context) {
	h.runAction(c, h.svc.Pause)
}

// resume godoc
// @Summary Resume an author metadata run
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Run ID"
// @Produce json
// @Success 200 {object} map[string]AuthorMetadataRunView
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/admin/author-metadata/runs/{id}/resume [post]
func (h *authorMetadataRunsHandler) resume(c *gin.Context) {
	h.runAction(c, h.svc.Resume)
}

// approveFull godoc
// @Summary Approve a completed pilot for a full run
// @Description Records the acting administrator and the pilot's exact versions. 409 not_a_completed_pilot, 409 already_approved.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Run ID"
// @Produce json
// @Success 200 {object} map[string]AuthorMetadataRunView
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/admin/author-metadata/runs/{id}/approve-full [post]
func (h *authorMetadataRunsHandler) approveFull(c *gin.Context) {
	id, err := runID(c)
	if err != nil {
		abortRunError(c, err)
		return
	}
	actor, err := actorID(c)
	if err != nil {
		abortRun(c, http.StatusInternalServerError, codeInternalError)
		return
	}
	run, err := h.svc.ApproveFull(c.Request.Context(), id, actor)
	if err != nil {
		abortRunError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{jsonKeyRun: presentRun(&run)})
}

// retry godoc
// @Summary Reopen failed items of one stage and closed error class
// @Description stage is extraction or local; error_class is a closed class of that stage.
// @Description Answers the exact number of reopened items.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Run ID"
// @Accept json
// @Produce json
// @Param body body retryRunRequest true "Stage and closed error class"
// @Success 202 {object} map[string]int64
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/admin/author-metadata/runs/{id}/retry [post]
func (h *authorMetadataRunsHandler) retry(c *gin.Context) {
	id, err := runID(c)
	if err != nil {
		abortRunError(c, err)
		return
	}
	var req retryRunRequest
	if err = decodeStrict(c, &req); err != nil {
		abortRunError(c, err)
		return
	}
	if err = req.validate(); err != nil {
		abortRunError(c, err)
		return
	}
	reopened, err := h.svc.Retry(c.Request.Context(), id, req.Stage, req.ErrorClass)
	if err != nil {
		abortRunError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"reopened": reopened})
}

// runAction serves the routes that take a run ID and answer with the run.
func (h *authorMetadataRunsHandler) runAction(c *gin.Context,
	action func(context.Context, int64) (AuthorMetadataRunView, error)) {
	id, err := runID(c)
	if err != nil {
		abortRunError(c, err)
		return
	}
	run, err := action(c.Request.Context(), id)
	if err != nil {
		abortRunError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{jsonKeyRun: presentRun(&run)})
}
