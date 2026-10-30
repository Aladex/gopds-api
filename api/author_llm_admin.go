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
	EvalReport(ctx context.Context, id int64, version int) ([]byte, string, error)
	RecomputeEvalReport(ctx context.Context, id int64) (*services.AuthorLLMRunState, error)
	AdversarialItems(ctx context.Context, id int64) (*services.AuthorLLMAdversarialReview, error)
	ContestedItems(ctx context.Context, id int64) (*services.AuthorLLMAdversarialReview, error)
	AuditItems(ctx context.Context, id int64) (*services.AuthorLLMAdversarialReview, error)
	Adjudicate(ctx context.Context, itemID int64, label, note, by string) error
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
	codeLLMReportStale        = "report_stale"
	codeLLMInvalidVersion     = "invalid_version"
	codeLLMEvidenceChanged    = "evidence_changed"
	codeLLMNotAdjudicable     = "not_adjudicable"
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
	{database.ErrAuthorLLMReportStale, http.StatusConflict, codeLLMReportStale},
	{database.ErrAuthorLLMEvidenceChanged, http.StatusConflict, codeLLMEvidenceChanged},
	{services.ErrAuthorLLMInvalidRun, http.StatusBadRequest, codeLLMInvalidRun},
	{services.ErrAuthorLLMInvalidSlot, http.StatusBadRequest, codeLLMInvalidSlot},
	{services.ErrAuthorLLMInvalidAdjudication, http.StatusBadRequest, codeInvalidRequest},
	{database.ErrAuthorLLMNotAdjudicable, http.StatusConflict, codeLLMNotAdjudicable},
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

// authorLLMMergeAdminService is the merge configuration's admin (E4c),
// mounted on the same routes under /merge; until it is set they answer
// llm_service_unavailable.
var authorLLMMergeAdminService = func() AuthorLLMAdminService { return unavailableAuthorLLMAdmin{} }

// SetAuthorLLMMergeAdminService installs the merge configuration's admin.
func SetAuthorLLMMergeAdminService(svc AuthorLLMAdminService) {
	authorLLMMergeAdminService = func() AuthorLLMAdminService { return svc }
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

func (unavailableAuthorLLMAdmin) EvalReport(context.Context, int64, int) (report []byte, sha string, err error) {
	return nil, "", errAuthorLLMServiceUnavailable
}

func (unavailableAuthorLLMAdmin) RecomputeEvalReport(context.Context, int64) (*services.AuthorLLMRunState, error) {
	return nil, errAuthorLLMServiceUnavailable
}

func (unavailableAuthorLLMAdmin) AdversarialItems(context.Context, int64) (*services.AuthorLLMAdversarialReview, error) {
	return nil, errAuthorLLMServiceUnavailable
}

func (unavailableAuthorLLMAdmin) ContestedItems(context.Context, int64) (*services.AuthorLLMAdversarialReview, error) {
	return nil, errAuthorLLMServiceUnavailable
}

func (unavailableAuthorLLMAdmin) AuditItems(context.Context, int64) (*services.AuthorLLMAdversarialReview, error) {
	return nil, errAuthorLLMServiceUnavailable
}

func (unavailableAuthorLLMAdmin) Adjudicate(context.Context, int64, string, string, string) error {
	return errAuthorLLMServiceUnavailable
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
	r.POST("/eval-report/:id/recompute", h.recompute)
	r.GET("/eval-report/:id/adversarial", h.adversarial)
	r.GET("/eval-report/:id/contested", h.contested)
	r.GET("/eval-report/:id/audit", h.audit)
	r.PUT("/adjudications/:id", h.adjudicate)
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
// @Description Instant; the stages go on in the background. 409: layer off, a run active, previous step incomplete, kind unavailable.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param body body startLLMRunRequest true "kind (eval|resolve|swap|merge), mode (dev|pilot|full; eval also swap, stage0..3, merge0..4)"
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
// @Description An eval run's report has versions (a recomputation adds one); without version it is the latest.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Run id"
// @Param version query int false "Report version, 1 and up"
// @Produce json
// @Success 200 {file} file
// @Router /api/admin/author-metadata/llm/eval-report/{id} [get]
func (h *authorLLMHandler) report(c *gin.Context) {
	id, err := runID(c)
	if err != nil {
		abortLLMError(c, err)
		return
	}
	version := 0
	if raw := c.Query("version"); raw != "" {
		if version, err = strconv.Atoi(raw); err != nil || version < 1 {
			abortRun(c, http.StatusBadRequest, codeLLMInvalidVersion)
			return
		}
	}
	body, sha, err := h.svc.EvalReport(c.Request.Context(), id, version)
	if err != nil {
		abortLLMError(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="author-llm-eval-`+strconv.FormatInt(id, 10)+`.json"`)
	c.Header("X-Report-SHA256", sha)
	c.Data(http.StatusOK, "application/json", body)
}

// recompute godoc
// @Summary Recompute a completed eval run's report
// @Description Computes the report again from the run's stored answers with the current gate code, without calling
// @Description any model, and stores it as the next version unless it says what the latest says. Only a completed eval
// @Description run of the current configuration.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Run id"
// @Produce json
// @Success 200 {object} map[string]services.AuthorLLMRunState
// @Router /api/admin/author-metadata/llm/eval-report/{id}/recompute [post]
func (h *authorLLMHandler) recompute(c *gin.Context) {
	id, err := runID(c)
	if err != nil {
		abortLLMError(c, err)
		return
	}
	run, err := h.svc.RecomputeEvalReport(c.Request.Context(), id)
	if err != nil {
		abortLLMError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{jsonKeyRun: run})
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

// adversarial godoc
// @Summary The accepted adversarial suite items of a merge eval run
// @Description Each item's two sides (names, books), the four answers and its current adjudication (merge routes only).
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Run id"
// @Produce json
// @Success 200 {object} map[string]services.AuthorLLMAdversarialReview
// @Router /api/admin/author-metadata/llm/merge/eval-report/{id}/adversarial [get]
func (h *authorLLMHandler) adversarial(c *gin.Context) {
	id, err := runID(c)
	if err != nil {
		abortLLMError(c, err)
		return
	}
	review, err := h.svc.AdversarialItems(c.Request.Context(), id)
	if err != nil {
		abortLLMError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"review": review})
}

// contested godoc
// @Summary The contested accepts of a merge eval run
// @Description The census or holdout items the pair agreed same and the judges did not both confirm, as the latest
// @Description report lists them: each item's two sides, the four answers and its current adjudication (merge routes
// @Description only). A label same or unclear clears the error the gate counts; none is ever applied.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Run id"
// @Produce json
// @Success 200 {object} map[string]services.AuthorLLMAdversarialReview
// @Router /api/admin/author-metadata/llm/merge/eval-report/{id}/contested [get]
func (h *authorLLMHandler) contested(c *gin.Context) {
	id, err := runID(c)
	if err != nil {
		abortLLMError(c, err)
		return
	}
	review, err := h.svc.ContestedItems(c.Request.Context(), id)
	if err != nil {
		abortLLMError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"review": review})
}

// audit godoc
// @Summary The audit sample of a merge eval run's census
// @Description The census items all four participants answered same that the latest report samples for the audit
// @Description (up to 300 per kind): each item's two sides, the four answers and its current adjudication (merge
// @Description routes only). A sampled item pending or labeled different fails its kind.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Run id"
// @Produce json
// @Success 200 {object} map[string]services.AuthorLLMAdversarialReview
// @Router /api/admin/author-metadata/llm/merge/eval-report/{id}/audit [get]
func (h *authorLLMHandler) audit(c *gin.Context) {
	id, err := runID(c)
	if err != nil {
		abortLLMError(c, err)
		return
	}
	review, err := h.svc.AuditItems(c.Request.Context(), id)
	if err != nil {
		abortLLMError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"review": review})
}

// adjudicateRequest is an adjudication's body: label is required, note may
// be empty.
type adjudicateRequest struct {
	Label string `json:"label"`
	Note  string `json:"note"`
}

// adjudicate godoc
// @Summary Label an accepted adversarial suite item, a contested accept or an audited item
// @Description label same, different or unclear. 409 not_adjudicable: neither an adversarial suite item, a
// @Description contested accept nor an item of a census's audit sample of a merge eval.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Eval item id"
// @Param body body adjudicateRequest true "label (same|different|unclear), note"
// @Success 204
// @Router /api/admin/author-metadata/llm/merge/adjudications/{id} [put]
func (h *authorLLMHandler) adjudicate(c *gin.Context) {
	id, err := runID(c)
	if err != nil {
		abortLLMError(c, err)
		return
	}
	var req adjudicateRequest
	if err := decodeStrict(c, &req); err != nil || req.Label == "" {
		abortRun(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	by := c.GetString("username")
	if by == "" {
		actor, actorErr := actorID(c)
		if actorErr != nil {
			abortRun(c, http.StatusInternalServerError, codeInternalError)
			return
		}
		by = "user:" + strconv.FormatInt(actor, 10)
	}
	if err := h.svc.Adjudicate(c.Request.Context(), id, req.Label, req.Note, by); err != nil {
		abortLLMError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
