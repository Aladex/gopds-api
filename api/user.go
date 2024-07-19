package api

import (
	"errors"
	"github.com/gin-gonic/gin"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/models"
	"net/http"
)

// ActionUser method for changing user information
// Auth godoc
// @Summary Change user information
// @Description Perform an action on a user based on the provided data
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Tags admin
// @Accept  json
// @Produce  json
// @Param  body body models.AdminCommandToUser true "User action data"
// @Success 200 {object} models.User "User object after the action"
// @Failure 400 {object} httputil.HTTPError "Bad request - invalid input parameters"
// @Failure 403 {object} httputil.HTTPError "Forbidden - access denied"
// @Failure 500 {object} httputil.HTTPError "Internal server error"
// @Router /api/admin/user [post]
func ActionUser(c *gin.Context) {
	var action models.AdminCommandToUser
	if err := c.ShouldBindJSON(&action); err == nil {
		user, err := database.ActionUser(action)
		if err != nil {
			c.JSON(500, err)
			return
		}
		c.JSON(200, user)
		return
	}
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad request"))
}
