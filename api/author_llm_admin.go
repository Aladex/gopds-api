package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"gopds-api/database"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
)

// The LLM block of the «Сканирование» card: its status, the start of the
// chain's next run, pause and resume of a run, lifting a participant's or the
// endpoint's pause, and the download of an eval report. Admin only: the group
// is mounted behind AdminMiddleware. Every start is instant — the run's
// stages go on in the background — and an illegal transition is a 409 with a
// closed code.

// AuthorLLMAdminService is what the LLM handlers need; services.AuthorLLMAdmin
// is one.
type AuthorLLMAdminService interface {
	Status(ctx context.Context) (*services.AuthorLLMStatus, error)
	Start(ctx context.Context, kind, mode string, actor int64) (*services.AuthorLLMRunState, error)
	Pause(ctx context.Context, id int64) (*services.AuthorLLMRunState, error)
	Resume(ctx context.Context, id int64) (*services.AuthorLLMRunState, error)
	Close(ctx context.Context, id, actor int64) (*services.AuthorLLMRunState, error)
	ResumeParticipant(ctx context.Context, slot string) error
	ResumeEndpoint(ctx context.Context) error
	EvalReport(ctx context.Context, id int64) ([]byte, string, error)
}

// Closed error codes of the LLM routes (beside the runs API's shared ones).
const (
	codeLLMNotConfigured      = "llm_not_configured"
	codeLLMPrerequisite       = "prerequisite_incomplete"
	codeLLMRunKindUnavailable = "run_kind_unavailable"
	codeLLMRunConfigChanged   = "run_config_changed"
	codeLLMInvalidRun         = "invalid_run"
	codeLLMInvalidSlot        = "invalid_slot"
	codeLLMReportNotFound     = "report_not_found"
	codeLLMServiceUnavailable = "llm_service_unavailable"
)

// errAuthorLLMServiceUnavailable: the LLM admin is not wired.
var errAuthorLLMServiceUnavailable = errors.New("api: the LLM admin service is not wired")

// llmErrors maps the service's errors to statuses and codes.
var llmErrors = []struct {
	err    error
	status int
	code   string
}{
	{services.ErrAuthorLLMNotConfigured, http.StatusConflict, codeLLMNotConfigured},
	{services.ErrAuthorLLMPrerequisite, http.StatusConflict, codeLLMPrerequisite},
	{services.ErrAuthorLLMRunKindUnavailable, http.StatusConflict, codeLLMRunKindUnavailable},
	{database.ErrAuthorLLMActiveRun, http.StatusConflict, codeActiveRunExists},
	{database.ErrAuthorLLMRunState, http.StatusConflict, codeInvalidTransition},
	{services.ErrAuthorLLMRunConfigChanged, http.StatusConflict, codeLLMRunConfigChanged},
	{database.ErrAuthorLLMRunNotFound, http.StatusNotFound, codeRunNotFound},
	{services.ErrAuthorLLMReportNotFound, http.StatusNotFound, codeLLMReportNotFound},
	{services.ErrAuthorLLMInvalidRun, http.StatusBadRequest, codeLLMInvalidRun},
	{services.ErrAuthorLLMInvalidSlot, http.StatusBadRequest, codeLLMInvalidSlot},
	{errAuthorLLMServiceUnavailable, http.StatusInternalServerError, codeLLMServiceUnavailable},
}

// abortLLMError answers an error with its closed code; anything unknown is
// internal_error, its text never shown.
func abortLLMError(c *gin.Context, err error) {
	var reqErr runRequestError
	if errors.As(err, &reqErr) {
		abortRun(c, http.StatusBadRequest, reqErr.code)
		return
	}
	for _, m := range llmErrors {
		if errors.Is(err, m.err) {
			abortRun(c, m.status, m.code)
			return
		}
	}
	abortRun(c, http.StatusInternalServerError, codeInternalError)
}

// authorLLMAdminService is the wiring point the server sets once; until it
// does, every LLM route answers llm_service_unavailable.
var authorLLMAdminService = func() AuthorLLMAdminService { return unavailableAuthorLLMAdmin{} }

// SetAuthorLLMAdminService installs the LLM admin the routes mount.
func SetAuthorLLMAdminService(svc AuthorLLMAdminService) {
	authorLLMAdminService = func() AuthorLLMAdminService { return svc }
}

type unavailableAuthorLLMAdmin struct{}

func (unavailableAuthorLLMAdmin) Status(context.Context) (*services.AuthorLLMStatus, error) {
	return nil, errAuthorLLMServiceUnavailable
}

func (unavailableAuthorLLMAdmin) Start(context.Context, string, string, int64) (*services.AuthorLLMRunState, error) {
	return nil, errAuthorLLMServiceUnavailable
}

func (unavailableAuthorLLMAdmin) Pause(context.Context, int64) (*services.AuthorLLMRunState, error) {
	return nil, errAuthorLLMServiceUnavailable
}

func (unavailableAuthorLLMAdmin) Resume(context.Context, int64) (*services.AuthorLLMRunState, error) {
	return nil, errAuthorLLMServiceUnavailable
}

func (unavailableAuthorLLMAdmin) Close(context.Context, int64, int64) (*services.AuthorLLMRunState, error) {
	return nil, errAuthorLLMServiceUnavailable
}

func (unavailableAuthorLLMAdmin) ResumeParticipant(context.Context, string) error {
	return errAuthorLLMServiceUnavailable
}

func (unavailableAuthorLLMAdmin) ResumeEndpoint(context.Context) error {
	return errAuthorLLMServiceUnavailable
}

func (unavailableAuthorLLMAdmin) EvalReport(context.Context, int64) (report []byte, sha string, err error) {
	return nil, "", errAuthorLLMServiceUnavailable
}

// authorLLMHandler serves the LLM routes.
type authorLLMHandler struct {
	svc AuthorLLMAdminService
}

// SetupAuthorLLMRoutes registers the LLM routes on a group already behind
// the admin middleware.
func SetupAuthorLLMRoutes(r *gin.RouterGroup, svc AuthorLLMAdminService) {
	h := &authorLLMHandler{svc: svc}
	r.GET("/status", h.status)
	r.POST("/runs", h.start)
	r.POST("/runs/:id/pause", h.pause)
	r.POST("/runs/:id/resume", h.resume)
	r.POST("/runs/:id/close", h.close)
	r.GET("/eval-report/:id", h.report)
	r.POST("/participants/:slot/resume", h.resumeParticipant)
	r.POST("/endpoint/resume", h.resumeEndpoint)
}

const jsonKeyLLMStatus = "status"

// status godoc
// @Summary The LLM block of the author metadata card
// @Description The chain's steps, the run to show, its progress, tokens and cost, and the pauses.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Produce json
// @Success 200 {object} map[string]services.AuthorLLMStatus
// @Router /api/admin/author-metadata/llm/status [get]
func (h *authorLLMHandler) status(c *gin.Context) {
	st, err := h.svc.Status(c.Request.Context())
	if err != nil {
		abortLLMError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{jsonKeyLLMStatus: st})
}

// startLLMRunRequest is the start's body: both fields are required.
type startLLMRunRequest struct {
	Kind string `json:"kind"`
	Mode string `json:"mode"`
}

// start godoc
// @Summary Start the chain's next LLM run
// @Description Instant; the stages go on in the background. 409: layer off, a run active, previous step incomplete, kind not built.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param body body startLLMRunRequest true "kind (eval|resolve|swap) and mode (dev|pilot|full)"
// @Produce json
// @Success 201 {object} map[string]services.AuthorLLMRunState
// @Router /api/admin/author-metadata/llm/runs [post]
func (h *authorLLMHandler) start(c *gin.Context) {
	var req startLLMRunRequest
	if err := decodeStrict(c, &req); err != nil || req.Kind == "" || req.Mode == "" {
		abortRun(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	actor, err := actorID(c)
	if err != nil {
		abortRun(c, http.StatusInternalServerError, codeInternalError)
		return
	}
	run, err := h.svc.Start(c.Request.Context(), req.Kind, req.Mode, actor)
	if err != nil {
		abortLLMError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{jsonKeyRun: run})
}

// pause godoc
// @Summary Pause an LLM run
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Run id"
// @Produce json
// @Success 200 {object} map[string]services.AuthorLLMRunState
// @Router /api/admin/author-metadata/llm/runs/{id}/pause [post]
func (h *authorLLMHandler) pause(c *gin.Context) { h.runAction(c, h.svc.Pause) }

// resume godoc
// @Summary Resume an LLM run
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Run id"
// @Produce json
// @Success 200 {object} map[string]services.AuthorLLMRunState
// @Router /api/admin/author-metadata/llm/runs/{id}/resume [post]
func (h *authorLLMHandler) resume(c *gin.Context) { h.runAction(c, h.svc.Resume) }

func (h *authorLLMHandler) runAction(c *gin.Context,
	action func(context.Context, int64) (*services.AuthorLLMRunState, error)) {
	id, err := runID(c)
	if err != nil {
		abortLLMError(c, err)
		return
	}
	run, err := action(c.Request.Context(), id)
	if err != nil {
		abortLLMError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{jsonKeyRun: run})
}

// report godoc
// @Summary Download an LLM eval report
// @Description The stored report, byte for byte; X-Report-SHA256 is the digest a registration cites.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Run id"
// @Produce json
// @Success 200 {file} file
// @Router /api/admin/author-metadata/llm/eval-report/{id} [get]
func (h *authorLLMHandler) report(c *gin.Context) {
	id, err := runID(c)
	if err != nil {
		abortLLMError(c, err)
		return
	}
	body, sha, err := h.svc.EvalReport(c.Request.Context(), id)
	if err != nil {
		abortLLMError(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="author-llm-eval-`+strconv.FormatInt(id, 10)+`.json"`)
	c.Header("X-Report-SHA256", sha)
	c.Data(http.StatusOK, "application/json", body)
}

// resumeParticipant godoc
// @Summary Lift an LLM participant's pause
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param slot path string true "production_a|production_b|judge_a|judge_b"
// @Success 204
// @Router /api/admin/author-metadata/llm/participants/{slot}/resume [post]
func (h *authorLLMHandler) resumeParticipant(c *gin.Context) {
	if err := h.svc.ResumeParticipant(c.Request.Context(), c.Param("slot")); err != nil {
		abortLLMError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// resumeEndpoint godoc
// @Summary Lift the LLM endpoint's pause
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Success 204
// @Router /api/admin/author-metadata/llm/endpoint/resume [post]
func (h *authorLLMHandler) resumeEndpoint(c *gin.Context) {
	if err := h.svc.ResumeEndpoint(c.Request.Context()); err != nil {
		abortLLMError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// close godoc
// @Summary Close an LLM run
// @Description Ends a run of an older configuration, or a paused one: open calls fenced, charged tokens kept, slot freed.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Run id"
// @Produce json
// @Success 200 {object} map[string]services.AuthorLLMRunState
// @Router /api/admin/author-metadata/llm/runs/{id}/close [post]
func (h *authorLLMHandler) close(c *gin.Context) {
	id, err := runID(c)
	if err != nil {
		abortLLMError(c, err)
		return
	}
	actor, err := actorID(c)
	if err != nil {
		abortRun(c, http.StatusInternalServerError, codeInternalError)
		return
	}
	run, err := h.svc.Close(c.Request.Context(), id, actor)
	if err != nil {
		abortLLMError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{jsonKeyRun: run})
}
