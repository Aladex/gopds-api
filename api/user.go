package api

import (
	"errors"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/models"
	"net/http"

	"github.com/gin-gonic/gin"
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
		if len(action.User.NewPassword) > 0 {
			action.User.Password = action.User.NewPassword
		}
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

// DeleteUser method for deleting a user
// Auth godoc
// @Summary Delete a user
// @Description Delete a user based on the provided user ID
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Tags admin
// @Accept  json
// @Produce  json
// @Param  id path string true "User ID"
// @Success 200 {object} models.Result "User deleted successfully"
// @Failure 400 {object} httputil.HTTPError "Bad request - invalid input parameters"
// @Failure 403 {object} httputil.HTTPError "Forbidden - access denied"
// @Failure 500 {object} httputil.HTTPError "Internal server error"
// @Router /api/admin/user/{id} [delete]
func DeleteUser(c *gin.Context) {
	userID := c.Param("id")
	if userID == "" {
		httputil.NewError(c, http.StatusBadRequest, errors.New("user ID is required"))
		return
	}

	err := database.DeleteUser(userID)
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(200, models.Result{
		Result: "user_deleted",
		Error:  nil,
	})
}
