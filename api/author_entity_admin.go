package api

import (
	"context"
	"errors"
	"net/http"

	"gopds-api/database"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
)

// The author entity step of the «Сканирование» card (design E, phase E1):
// its status — the newest pass with its progress and the newest report — and
// the start of a pass, which goes on in the background. Admin only: the
// group is mounted behind AdminMiddleware. A second pass while one is active
// is a 409 active_run_exists.

// AuthorEntityAdminService is what the entity handlers need;
// services.AuthorEntityAdmin is one.
type AuthorEntityAdminService interface {
	Status(ctx context.Context) (*services.AuthorEntityStatus, error)
	Start(ctx context.Context, actor int64) (*database.AuthorEntityBuildRun, error)
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
}

// abortEntityError answers an error with its closed code; anything unknown
// is internal_error, its text never shown.
func abortEntityError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, database.ErrAuthorEntityBuildActive):
		abortRun(c, http.StatusConflict, codeActiveRunExists)
	case errors.Is(err, errAuthorEntityServiceUnavailable):
		abortRun(c, http.StatusInternalServerError, codeEntityServiceUnavailable)
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
