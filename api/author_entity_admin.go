package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"gopds-api/database"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// The author entity step of the «Сканирование» card (design E, phase E1):
// its status — the newest pass with its progress, the newest report, the
// Wikidata reference and the autopilot — the start of a pass and the fetch
// of the Wikidata reference (E4w), both going on in the background, and the
// autopilot's switch. Admin only: the group
// is mounted behind AdminMiddleware. A second pass or fetch while one is
// active is a 409 active_run_exists.

// AuthorEntityAdminService is what the entity handlers need;
// services.AuthorEntityAdmin is one.
type AuthorEntityAdminService interface {
	Status(ctx context.Context) (*services.AuthorEntityStatus, error)
	Start(ctx context.Context, actor int64) (*database.AuthorEntityBuildRun, error)
	StartWikidataFetch(ctx context.Context, actor int64) (*database.WikidataExtract, error)
	SetAutopilot(ctx context.Context, enabled bool, actor int64) (*services.AuthorAutopilotStatus, error)
	MergeDecisions(ctx context.Context) (*database.MergeDecisionsView, error)
	RevokeMergeDecision(ctx context.Context, decisionID, actor int64) (int64, error)
	RetireMergeVeto(ctx context.Context, vetoID, actor int64) error
	ReanchorMergeVeto(ctx context.Context, vetoID int64, sideA, sideB uuid.UUID, actor int64) (int64, error)
	RegisterMergeKind(ctx context.Context, runID int64, kind string, actor int64) (int64, error)
	WithdrawMergeRegistration(ctx context.Context, id, actor int64) error
	BlockMergeScript(ctx context.Context, id int64, script string, actor int64) error
	ApplyMergeEval(ctx context.Context, registrationID, actor int64) (*services.AuthorEntityMergeApplyReport, error)
}

// codeEntityServiceUnavailable: the entity admin is not wired.
const codeEntityServiceUnavailable = "entity_service_unavailable"

var errAuthorEntityServiceUnavailable = errors.New("api: the author entity admin service is not wired")

// authorEntityAdminService is the wiring point the server sets once; until
// it does, every entity route answers entity_service_unavailable.
var authorEntityAdminService = func() AuthorEntityAdminService { return unavailableAuthorEntityAdmin{} }

// SetAuthorEntityAdminService installs the entity admin the routes mount.
func SetAuthorEntityAdminService(svc AuthorEntityAdminService) {
	authorEntityAdminService = func() AuthorEntityAdminService { return svc }
}

type unavailableAuthorEntityAdmin struct{}

func (unavailableAuthorEntityAdmin) Status(context.Context) (*services.AuthorEntityStatus, error) {
	return nil, errAuthorEntityServiceUnavailable
}

func (unavailableAuthorEntityAdmin) Start(context.Context, int64) (*database.AuthorEntityBuildRun, error) {
	return nil, errAuthorEntityServiceUnavailable
}

func (unavailableAuthorEntityAdmin) StartWikidataFetch(context.Context, int64) (*database.WikidataExtract, error) {
	return nil, errAuthorEntityServiceUnavailable
}

func (unavailableAuthorEntityAdmin) SetAutopilot(context.Context, bool, int64) (*services.AuthorAutopilotStatus, error) {
	return nil, errAuthorEntityServiceUnavailable
}

func (unavailableAuthorEntityAdmin) MergeDecisions(context.Context) (*database.MergeDecisionsView, error) {
	return nil, errAuthorEntityServiceUnavailable
}

func (unavailableAuthorEntityAdmin) RevokeMergeDecision(context.Context, int64, int64) (int64, error) {
	return 0, errAuthorEntityServiceUnavailable
}

func (unavailableAuthorEntityAdmin) RetireMergeVeto(context.Context, int64, int64) error {
	return errAuthorEntityServiceUnavailable
}

func (unavailableAuthorEntityAdmin) ReanchorMergeVeto(context.Context, int64, uuid.UUID, uuid.UUID, int64) (int64, error) {
	return 0, errAuthorEntityServiceUnavailable
}

func (unavailableAuthorEntityAdmin) RegisterMergeKind(context.Context, int64, string, int64) (int64, error) {
	return 0, errAuthorEntityServiceUnavailable
}

func (unavailableAuthorEntityAdmin) WithdrawMergeRegistration(context.Context, int64, int64) error {
	return errAuthorEntityServiceUnavailable
}

func (unavailableAuthorEntityAdmin) BlockMergeScript(context.Context, int64, string, int64) error {
	return errAuthorEntityServiceUnavailable
}

func (unavailableAuthorEntityAdmin) ApplyMergeEval(
	context.Context, int64, int64,
) (*services.AuthorEntityMergeApplyReport, error) {
	return nil, errAuthorEntityServiceUnavailable
}

// authorEntityHandler serves the entity routes.
type authorEntityHandler struct {
	svc func() AuthorEntityAdminService
}

// SetupAuthorEntityRoutes registers the entity routes on a group already
// behind the admin middleware. The service is read at each request, so the
// server's wiring takes effect whenever it lands.
func SetupAuthorEntityRoutes(r *gin.RouterGroup, svc func() AuthorEntityAdminService) {
	h := &authorEntityHandler{svc: svc}
	r.GET("/status", h.status)
	r.POST("/build", h.start)
	r.POST("/wikidata", h.fetchWikidata)
	r.POST("/autopilot", h.setAutopilot)
	r.GET("/merges", h.merges)
	r.POST("/merges/decisions/:id/revoke", h.revoke)
	r.POST("/merges/vetoes/:id/retire", h.retire)
	r.POST("/merges/vetoes/:id/reanchor", h.reanchor)
	r.POST("/merges/registrations", h.register)
	r.POST("/merges/registrations/:id/withdraw", h.withdraw)
	r.POST("/merges/registrations/:id/block", h.blockScript)
	r.POST("/merges/registrations/:id/apply", h.apply)
}

// abortEntityError answers an error with its closed code; anything unknown
// is internal_error, its text never shown.
func abortEntityError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, database.ErrAuthorEntityBuildActive), errors.Is(err, database.ErrWikidataFetchActive):
		abortRun(c, http.StatusConflict, codeActiveRunExists)
	case errors.Is(err, errAuthorEntityServiceUnavailable):
		abortRun(c, http.StatusInternalServerError, codeEntityServiceUnavailable)
	case errors.Is(err, database.ErrMergeDecisionNotLive):
		abortRun(c, http.StatusConflict, codeDecisionNotLive)
	case errors.Is(err, database.ErrMergeVetoNotLive):
		abortRun(c, http.StatusConflict, codeVetoNotLive)
	case errors.Is(err, database.ErrMergeVetoSideUnrecorded):
		abortRun(c, http.StatusConflict, codeVetoSideUnrecorded)
	case errors.Is(err, database.ErrMergeVetoSides):
		abortRun(c, http.StatusUnprocessableEntity, codeVetoSidesInvalid)
	case errors.Is(err, services.ErrAuthorEntityMergeReportInvalid):
		abortRun(c, http.StatusUnprocessableEntity, codeMergeReportInvalid)
	case errors.Is(err, services.ErrAuthorEntityMergeGate):
		abortRun(c, http.StatusUnprocessableEntity, codeMergeGateInvalid)
	case errors.Is(err, services.ErrAuthorEntityMergeNotPassed):
		abortRun(c, http.StatusConflict, codeMergeKindUnproven)
	case errors.Is(err, database.ErrMergeRegistrationExists):
		abortRun(c, http.StatusConflict, codeMergeKindRegistered)
	case errors.Is(err, database.ErrMergeRegistrationNotLive), errors.Is(err, services.ErrAuthorEntityMergeNotRegistered):
		abortRun(c, http.StatusConflict, codeRegistrationNotLive)
	case errors.Is(err, services.ErrAuthorEntityMergeDriftHeld):
		abortRun(c, http.StatusConflict, codeMergeApplyDriftHeld)
	case errors.Is(err, database.ErrAuthorEntityVetoUnanchored):
		abortRun(c, http.StatusConflict, codeVetoUnanchored)
	case errors.Is(err, database.ErrAuthorEntityNoCompletedBuild):
		abortRun(c, http.StatusConflict, codeNoCompletedBuild)
	case errors.Is(err, database.ErrMergeRegistrationInvalid):
		abortRun(c, http.StatusBadRequest, codeInvalidRequest)
	default:
		abortRun(c, http.StatusInternalServerError, codeInternalError)
	}
}

// status godoc
// @Summary The author entity step of the author metadata card
// @Description The newest build pass with its progress and the report of the newest completed one.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Produce json
// @Success 200 {object} map[string]services.AuthorEntityStatus
// @Router /api/admin/author-metadata/entities/status [get]
func (h *authorEntityHandler) status(c *gin.Context) {
	st, err := h.svc().Status(c.Request.Context())
	if err != nil {
		abortEntityError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{jsonKeyLLMStatus: st})
}

// start godoc
// @Summary Start an author entity build pass
// @Description Instant; the pass goes on in the background. 409: a pass is already active.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Produce json
// @Success 201 {object} map[string]database.AuthorEntityBuildRun
// @Router /api/admin/author-metadata/entities/build [post]
func (h *authorEntityHandler) start(c *gin.Context) {
	actor, err := actorID(c)
	if err != nil {
		abortRun(c, http.StatusInternalServerError, codeInternalError)
		return
	}
	run, err := h.svc().Start(c.Request.Context(), actor)
	if err != nil {
		abortEntityError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{jsonKeyRun: run})
}

// fetchWikidata godoc
// @Summary Fetch the Wikidata reference of the author entity build
// @Description Instant; the fetch goes on in the background, from QLever; a failed one goes on from its chunks.
// @Description 409: a fetch is already active.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Produce json
// @Success 201 {object} map[string]database.WikidataExtract
// @Router /api/admin/author-metadata/entities/wikidata [post]
func (h *authorEntityHandler) fetchWikidata(c *gin.Context) {
	actor, err := actorID(c)
	if err != nil {
		abortRun(c, http.StatusInternalServerError, codeInternalError)
		return
	}
	ext, err := h.svc().StartWikidataFetch(c.Request.Context(), actor)
	if err != nil {
		abortEntityError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{jsonKeyExtract: ext})
}

// jsonKeyExtract wraps a Wikidata extract.
const jsonKeyExtract = "extract"

// autopilotRequest is the autopilot's switch.
type autopilotRequest struct {
	Enabled *bool `json:"enabled"`
}

// setAutopilot godoc
// @Summary Switch the author autopilot on or off
// @Description Once a scan has settled, the autopilot starts a resolve run over the registered strata and then an
// @Description entity build on its own. Switched off it starts nothing; a run it started goes on.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param body body autopilotRequest true "The switch"
// @Accept json
// @Produce json
// @Success 200 {object} map[string]services.AuthorAutopilotStatus
// @Router /api/admin/author-metadata/entities/autopilot [post]
func (h *authorEntityHandler) setAutopilot(c *gin.Context) {
	var req autopilotRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		abortRun(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	actor, err := actorID(c)
	if err != nil {
		abortRun(c, http.StatusInternalServerError, codeInternalError)
		return
	}
	st, err := h.svc().SetAutopilot(c.Request.Context(), *req.Enabled, actor)
	if err != nil {
		abortEntityError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{jsonKeyAutopilot: st})
}

// jsonKeyAutopilot wraps the autopilot's block.
const jsonKeyAutopilot = "autopilot"

// The model merges of the card (E4b-3b): the decisions and vetoes, the
// revoke of a decision (its veto keeps the pair apart), and a veto's retire
// and re-anchor. Closed codes: decision_not_live and veto_not_live (409) for
// a row that is no longer live, veto_side_unrecorded (409) for a decision
// whose books are not recorded, veto_sides_invalid (422) for a re-anchor
// whose entities are not the two sides.
const (
	codeDecisionNotLive    = "decision_not_live"
	codeVetoNotLive        = "veto_not_live"
	codeVetoSideUnrecorded = "veto_side_unrecorded"
	codeVetoSidesInvalid   = "veto_sides_invalid"
)

// JSON keys of the merge routes.
const (
	jsonKeyMerges = "merges"
	jsonKeyRevoke = "revoke_id"
	jsonKeyVeto   = "veto_id"
)

// pathID is the route's positive :id, false when it is not one.
func pathID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	return id, err == nil && id > 0
}

// merges godoc
// @Summary The model merges of the author entity card
// @Description The live merge decisions with what the newest pass did with them, the live vetoes with their state
// @Description and the entities holding their sides, and whether a live veto is unanchored.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Produce json
// @Success 200 {object} map[string]database.MergeDecisionsView
// @Router /api/admin/author-metadata/entities/merges [get]
func (h *authorEntityHandler) merges(c *gin.Context) {
	v, err := h.svc().MergeDecisions(c.Request.Context())
	if err != nil {
		abortEntityError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{jsonKeyMerges: v})
}

// revoke godoc
// @Summary Revoke a model merge decision
// @Description The next pass splits the pair; the revoke's veto keeps it apart. 409 decision_not_live,
// @Description veto_side_unrecorded.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Decision ID"
// @Produce json
// @Success 200 {object} map[string]int64
// @Router /api/admin/author-metadata/entities/merges/decisions/{id}/revoke [post]
func (h *authorEntityHandler) revoke(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		abortRun(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	actor, err := actorID(c)
	if err != nil {
		abortRun(c, http.StatusInternalServerError, codeInternalError)
		return
	}
	revokeID, err := h.svc().RevokeMergeDecision(c.Request.Context(), id, actor)
	if err != nil {
		abortEntityError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{jsonKeyRevoke: revokeID})
}

// retire godoc
// @Summary Retire a merge veto
// @Description The administrator's decision to let the veto's sides be joined again. 409 veto_not_live.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Veto ID"
// @Produce json
// @Success 200 {object} map[string]int64
// @Router /api/admin/author-metadata/entities/merges/vetoes/{id}/retire [post]
func (h *authorEntityHandler) retire(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		abortRun(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	actor, err := actorID(c)
	if err != nil {
		abortRun(c, http.StatusInternalServerError, codeInternalError)
		return
	}
	if err := h.svc().RetireMergeVeto(c.Request.Context(), id, actor); err != nil {
		abortEntityError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{jsonKeyVeto: id})
}

// reanchorRequest names the two current entities that are a veto's sides.
type reanchorRequest struct {
	SideA *uuid.UUID `json:"side_a"`
	SideB *uuid.UUID `json:"side_b"`
}

// reanchor godoc
// @Summary Re-anchor a merge veto on two current entities
// @Description Retires the veto and records a new one from the two entities' books. 409 veto_not_live; 422
// @Description veto_sides_invalid: one entity, or one holding no fingerprint of its side.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Veto ID"
// @Param body body reanchorRequest true "The two sides"
// @Accept json
// @Produce json
// @Success 200 {object} map[string]int64
// @Router /api/admin/author-metadata/entities/merges/vetoes/{id}/reanchor [post]
func (h *authorEntityHandler) reanchor(c *gin.Context) {
	id, ok := pathID(c)
	var req reanchorRequest
	if !ok || decodeStrict(c, &req) != nil || req.SideA == nil || req.SideB == nil {
		abortRun(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	actor, err := actorID(c)
	if err != nil {
		abortRun(c, http.StatusInternalServerError, codeInternalError)
		return
	}
	newID, err := h.svc().ReanchorMergeVeto(c.Request.Context(), id, *req.SideA, *req.SideB, actor)
	if err != nil {
		abortEntityError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{jsonKeyVeto: newID})
}

// Registration of a merge stratum (E4d): register a kind from a completed
// merge eval whose latest report passed it, withdraw it or block one script
// of it (the next build splits their merges), and apply its eval's accepted
// items as decisions. Closed codes: merge_report_invalid and
// merge_gate_invalid (422), merge_kind_not_passed, merge_kind_registered,
// registration_not_live, merge_apply_drift_held, veto_unanchored,
// no_completed_build and active_run_exists (409).
const (
	codeMergeReportInvalid  = "merge_report_invalid"
	codeMergeGateInvalid    = "merge_gate_invalid"
	codeMergeKindUnproven   = "merge_kind_not_passed"
	codeMergeKindRegistered = "merge_kind_registered"
	codeRegistrationNotLive = "registration_not_live"
	codeMergeApplyDriftHeld = "merge_apply_drift_held"
	codeVetoUnanchored      = "veto_unanchored"
	codeNoCompletedBuild    = "no_completed_build"
)

// JSON keys of the registration routes.
const (
	jsonKeyRegistration = "registration_id"
	jsonKeyApply        = "apply"
)

// registerRequest names the merge eval run and the kind it passed.
type registerRequest struct {
	RunID *int64  `json:"run_id"`
	Kind  *string `json:"kind"`
}

// register godoc
// @Summary Register a merge kind from a merge eval report
// @Description The run's latest report must be a merge report gated against judge_consensus that passed the kind.
// @Description 422 merge_report_invalid, merge_gate_invalid; 409 merge_kind_not_passed, merge_kind_registered.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param body body registerRequest true "The run and the kind"
// @Accept json
// @Produce json
// @Success 201 {object} map[string]int64
// @Router /api/admin/author-metadata/entities/merges/registrations [post]
func (h *authorEntityHandler) register(c *gin.Context) {
	var req registerRequest
	if decodeStrict(c, &req) != nil || req.RunID == nil || *req.RunID <= 0 || req.Kind == nil ||
		!database.ValidMergeKind(*req.Kind) {
		abortRun(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	actor, err := actorID(c)
	if err != nil {
		abortRun(c, http.StatusInternalServerError, codeInternalError)
		return
	}
	id, err := h.svc().RegisterMergeKind(c.Request.Context(), *req.RunID, *req.Kind, actor)
	if err != nil {
		abortEntityError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{jsonKeyRegistration: id})
}

// withdraw godoc
// @Summary Withdraw a merge registration
// @Description The kill switch of a kind: from the next build on its merges split, the absorbed IDs revived.
// @Description 409 registration_not_live.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Registration ID"
// @Produce json
// @Success 200 {object} map[string]int64
// @Router /api/admin/author-metadata/entities/merges/registrations/{id}/withdraw [post]
func (h *authorEntityHandler) withdraw(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		abortRun(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	actor, err := actorID(c)
	if err != nil {
		abortRun(c, http.StatusInternalServerError, codeInternalError)
		return
	}
	if err := h.svc().WithdrawMergeRegistration(c.Request.Context(), id, actor); err != nil {
		abortEntityError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{jsonKeyRegistration: id})
}

// blockRequest names the script to block.
type blockRequest struct {
	Script *string `json:"script"`
}

// blockScript godoc
// @Summary Block one script of a merge registration
// @Description From the next build on the kind's merges of that script split. 409 registration_not_live.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Registration ID"
// @Param body body blockRequest true "The script: Cyrl, Latn or Cyrl-Latn"
// @Accept json
// @Produce json
// @Success 200 {object} map[string]int64
// @Router /api/admin/author-metadata/entities/merges/registrations/{id}/block [post]
func (h *authorEntityHandler) blockScript(c *gin.Context) {
	id, ok := pathID(c)
	var req blockRequest
	if !ok || decodeStrict(c, &req) != nil || req.Script == nil || !database.ValidMergeScript(*req.Script) {
		abortRun(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	actor, err := actorID(c)
	if err != nil {
		abortRun(c, http.StatusInternalServerError, codeInternalError)
		return
	}
	if err := h.svc().BlockMergeScript(c.Request.Context(), id, *req.Script, actor); err != nil {
		abortEntityError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{jsonKeyRegistration: id})
}

// apply godoc
// @Summary Apply a registered merge eval
// @Description Inserts a decision for every census or holdout item of the kind the pair and both judges answered
// @Description same, compared with the stored candidates; the next build merges them. 409 registration_not_live,
// @Description merge_apply_drift_held, veto_unanchored, no_completed_build, active_run_exists.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Registration ID"
// @Produce json
// @Success 200 {object} map[string]services.AuthorEntityMergeApplyReport
// @Router /api/admin/author-metadata/entities/merges/registrations/{id}/apply [post]
func (h *authorEntityHandler) apply(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		abortRun(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	actor, err := actorID(c)
	if err != nil {
		abortRun(c, http.StatusInternalServerError, codeInternalError)
		return
	}
	rep, err := h.svc().ApplyMergeEval(c.Request.Context(), id, actor)
	if err != nil {
		abortEntityError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{jsonKeyApply: rep})
}
