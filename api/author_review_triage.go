package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"gopds-api/database"
	"gopds-api/middlewares"

	"github.com/gin-gonic/gin"
)

// The review triage of the author review queue (design/review-triage): a dry
// run that plans a decision for every open item, the plan's table, and the
// audit sample to label. Admin only: the group is mounted behind
// AdminMiddleware; the start of a dry run checks the double-submit CSRF
// token. The dry run goes on in the background; a second one while a batch
// works is a 409 active_run_exists.

// AuthorReviewTriageService is what the triage handlers need;
// services.AuthorReviewTriageAdmin is one.
type AuthorReviewTriageService interface {
	StartDryRun(ctx context.Context, actor int64) (*database.TriageBatch, error)
	Batches(ctx context.Context) ([]database.TriageBatch, error)
	Batch(ctx context.Context, id int64) (*database.TriageBatchView, error)
	AuditSample(ctx context.Context, id int64) ([]database.TriageAuditRow, error)
}

// Error codes of the triage routes.
const (
	codeTriageServiceUnavailable = "triage_service_unavailable"
	codeTriageBatchNotFound      = "batch_not_found"
	codeTriageBatchState         = "batch_state"
)

var errAuthorReviewTriageUnavailable = errors.New("api: the review triage service is not wired")

// authorReviewTriageService is the wiring point the server sets once; until
// it does, every triage route answers triage_service_unavailable.
var authorReviewTriageService = func() AuthorReviewTriageService { return unavailableReviewTriage{} }

// SetAuthorReviewTriageService installs the triage admin the routes mount.
func SetAuthorReviewTriageService(svc AuthorReviewTriageService) {
	authorReviewTriageService = func() AuthorReviewTriageService { return svc }
}

type unavailableReviewTriage struct{}

func (unavailableReviewTriage) StartDryRun(context.Context, int64) (*database.TriageBatch, error) {
	return nil, errAuthorReviewTriageUnavailable
}

func (unavailableReviewTriage) Batches(context.Context) ([]database.TriageBatch, error) {
	return nil, errAuthorReviewTriageUnavailable
}

func (unavailableReviewTriage) Batch(context.Context, int64) (*database.TriageBatchView, error) {
	return nil, errAuthorReviewTriageUnavailable
}

func (unavailableReviewTriage) AuditSample(context.Context, int64) ([]database.TriageAuditRow, error) {
	return nil, errAuthorReviewTriageUnavailable
}

// reviewTriageHandler serves the triage routes.
type reviewTriageHandler struct {
	svc func() AuthorReviewTriageService
}

// SetupAuthorReviewTriageRoutes registers the triage routes on a group
// already behind the admin middleware. The service is read at each request,
// so the server's wiring takes effect whenever it lands.
func SetupAuthorReviewTriageRoutes(r *gin.RouterGroup, svc func() AuthorReviewTriageService) {
	h := &reviewTriageHandler{svc: svc}
	r.GET("/batches", h.batches)
	r.POST("/batches", middlewares.CSRFMiddleware(), h.start)
	r.GET("/batches/:id", h.batch)
	r.GET("/batches/:id/audit-sample", h.auditSample)
}

// abortTriageError answers an error with its closed code; anything unknown
// is internal_error, its text never shown.
func abortTriageError(c *gin.Context, err error) {
	var reqErr runRequestError
	switch {
	case errors.As(err, &reqErr):
		abortRun(c, http.StatusBadRequest, reqErr.code)
	case errors.Is(err, errAuthorReviewTriageUnavailable):
		abortRun(c, http.StatusServiceUnavailable, codeTriageServiceUnavailable)
	case errors.Is(err, database.ErrTriageActiveBatch):
		abortRun(c, http.StatusConflict, codeActiveRunExists)
	case errors.Is(err, database.ErrTriageBatchNotFound):
		abortRun(c, http.StatusNotFound, codeTriageBatchNotFound)
	case errors.Is(err, database.ErrTriageBatchState):
		abortRun(c, http.StatusConflict, codeTriageBatchState)
	default:
		abortRun(c, http.StatusInternalServerError, codeInternalError)
	}
}

// jsonKeyBatches and jsonKeyBatch wrap the triage's batches.
const (
	jsonKeyBatches = "batches"
	jsonKeyBatch   = "batch"
)

// batches godoc
// @Summary The review triage's batches
// @Description The newest dry runs first, with their status and progress.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Produce json
// @Success 200 {object} map[string][]database.TriageBatch
// @Router /api/admin/author-metadata/triage/batches [get]
func (h *reviewTriageHandler) batches(c *gin.Context) {
	batches, err := h.svc().Batches(c.Request.Context())
	if err != nil {
		abortTriageError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{jsonKeyBatches: batches})
}

// start godoc
// @Summary Start a dry run of the review triage
// @Description Instant; the plan is built in the background and writes only the triage tables.
// @Description 409 active_run_exists: a batch is planning, applying or undoing.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Produce json
// @Success 202 {object} map[string]database.TriageBatch
// @Router /api/admin/author-metadata/triage/batches [post]
func (h *reviewTriageHandler) start(c *gin.Context) {
	actor, err := actorID(c)
	if err != nil {
		abortRun(c, http.StatusInternalServerError, codeInternalError)
		return
	}
	batch, err := h.svc().StartDryRun(c.Request.Context(), actor)
	if err != nil {
		abortTriageError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{jsonKeyBatch: batch})
}

// batch godoc
// @Summary One batch of the review triage
// @Description The plan per step and class with ten examples a cell, what it left and excluded, and the outcomes.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Batch ID"
// @Produce json
// @Success 200 {object} database.TriageBatchView
// @Router /api/admin/author-metadata/triage/batches/{id} [get]
func (h *reviewTriageHandler) batch(c *gin.Context) {
	id, err := runID(c)
	if err != nil {
		abortTriageError(c, err)
		return
	}
	view, err := h.svc().Batch(c.Request.Context(), id)
	if err != nil {
		abortTriageError(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

// triageAuditHeader is the audit sample's header: the auditors fill verdict
// (same, different or unclear) and note.
var triageAuditHeader = []string{"item_id", "pool", "step", "rule", "class", "credits", "src_first", "src_middle",
	"src_last", "src_nick", "src_display", "before", "after", "action", "evidence", "verdict", "note"}

// auditSample godoc
// @Summary The audit sample of a dry run
// @Description A stratified sample per step and its reserve, as tab-separated values to label.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Batch ID"
// @Produce text/tab-separated-values
// @Success 200 {string} string
// @Router /api/admin/author-metadata/triage/batches/{id}/audit-sample [get]
func (h *reviewTriageHandler) auditSample(c *gin.Context) {
	id, err := runID(c)
	if err != nil {
		abortTriageError(c, err)
		return
	}
	rows, err := h.svc().AuditSample(c.Request.Context(), id)
	if err != nil {
		abortTriageError(c, err)
		return
	}
	var b strings.Builder
	b.WriteString(strings.Join(triageAuditHeader, "\t"))
	b.WriteByte('\n')
	for i := range rows {
		b.WriteString(strings.Join(triageAuditLine(&rows[i]), "\t"))
		b.WriteByte('\n')
	}
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="triage-%d-audit.tsv"`, id))
	c.Data(http.StatusOK, "text/tab-separated-values; charset=utf-8", []byte(b.String()))
}

// triageSourceFields are the source's five fields: first, middle, last,
// nickname and display.
const triageSourceFields = 5

// triageAuditLine is one row's cells, each cleaned of tabs and line breaks.
func triageAuditLine(r *database.TriageAuditRow) []string {
	source := make([]string, triageSourceFields)
	copy(source, r.Source)
	cells := []string{strconv.FormatInt(r.ReviewItemID, 10), r.Pool, strconv.Itoa(int(r.Step)), r.Rule, r.Class,
		strconv.Itoa(r.Credits)}
	cells = append(cells, source...)
	cells = append(cells, strings.Join(r.Before, "; "), strings.Join(r.After, "; "), r.Action, string(r.Evidence),
		"", "")
	for i := range cells {
		cells[i] = tsvCell(cells[i])
	}
	return cells
}

// tsvCell turns every run of tabs and line breaks into one space.
func tsvCell(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '\t' || r == '\n' || r == '\r' }), " ")
}
