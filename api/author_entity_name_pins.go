package api

import (
	"errors"
	"net/http"

	"gopds-api/database"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// The name pin (design/author-display, name pins): read an entity's names,
// pin one of them, hand the name back to the build. Closed codes:
// name_pin_batch (422): no entity, more than 200, or one twice;
// name_pin_entity (422): an entity that is unknown or merged;
// name_pin_name (422): a name none of the entity's credits is written with;
// name_pin_not_pinned (409): an unpin of a name that is not pinned. An active
// build answers 409 active_run_exists, as every hand write does.
const (
	codeNamePinBatch     = "name_pin_batch"
	codeNamePinEntity    = "name_pin_entity"
	codeNamePinName      = "name_pin_name"
	codeNamePinNotPinned = "name_pin_not_pinned"
	jsonKeyEntities      = "entities"
)

// pinNamesRequest is the names to pin, one per entity.
type pinNamesRequest struct {
	Pins []database.NamePin `json:"pins"`
}

// unpinNamesRequest is the entities whose names go back to the build.
type unpinNamesRequest struct {
	Entities []uuid.UUID `json:"entities"`
}

// abortNamePinError answers a name pin's refusals, and every other error as
// the entity block does.
func abortNamePinError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, database.ErrNamePinBatch):
		abortRun(c, http.StatusUnprocessableEntity, codeNamePinBatch)
	case errors.Is(err, database.ErrNamePinEntity):
		abortRun(c, http.StatusUnprocessableEntity, codeNamePinEntity)
	case errors.Is(err, database.ErrNamePinName):
		abortRun(c, http.StatusUnprocessableEntity, codeNamePinName)
	case errors.Is(err, database.ErrNamePinNotPinned):
		abortRun(c, http.StatusConflict, codeNamePinNotPinned)
	default:
		abortEntityError(c, err)
	}
}

// entityNames godoc
// @Summary An author entity's names
// @Description The entity's shown name, whether it is pinned (by whom, when) and the names its credits are written
// @Description with, the most used first: what a pin chooses from. 422 name_pin_entity.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path string true "Entity UUID"
// @Produce json
// @Success 200 {object} database.EntityNames
// @Router /api/admin/author-metadata/entities/names/{id} [get]
func (h *authorEntityHandler) entityNames(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		abortRun(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	names, err := h.svc().EntityNames(c.Request.Context(), id)
	if err != nil {
		abortNamePinError(c, err)
		return
	}
	c.JSON(http.StatusOK, names)
}

// pinNames godoc
// @Summary Pin the names of author entities
// @Description Each entity is shown under the chosen name, one its credits are written with, and every build keeps
// @Description it; no merge input changes. All or none. 422 name_pin_batch, name_pin_entity, name_pin_name;
// @Description 409 active_run_exists.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param body body pinNamesRequest true "Entity and name, up to 200"
// @Accept json
// @Produce json
// @Success 200 {object} map[string]int
// @Router /api/admin/author-metadata/entities/names/pins [post]
func (h *authorEntityHandler) pinNames(c *gin.Context) {
	var req pinNamesRequest
	if decodeStrict(c, &req) != nil {
		abortRun(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	actor, err := actorID(c)
	if err != nil {
		abortRun(c, http.StatusInternalServerError, codeInternalError)
		return
	}
	if err := h.svc().PinEntityNames(c.Request.Context(), req.Pins, actor); err != nil {
		abortNamePinError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{jsonKeyEntities: len(req.Pins)})
}

// unpinNames godoc
// @Summary Hand the names of author entities back to the build
// @Description The next build shows the names it computes. All or none. 422 name_pin_batch, name_pin_entity;
// @Description 409 name_pin_not_pinned, active_run_exists.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param body body unpinNamesRequest true "The entities, up to 200"
// @Accept json
// @Produce json
// @Success 200 {object} map[string]int
// @Router /api/admin/author-metadata/entities/names/pins/revoke [post]
func (h *authorEntityHandler) unpinNames(c *gin.Context) {
	var req unpinNamesRequest
	if decodeStrict(c, &req) != nil {
		abortRun(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	actor, err := actorID(c)
	if err != nil {
		abortRun(c, http.StatusInternalServerError, codeInternalError)
		return
	}
	if err := h.svc().UnpinEntityNames(c.Request.Context(), req.Entities, actor); err != nil {
		abortNamePinError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{jsonKeyEntities: len(req.Entities)})
}
