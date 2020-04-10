package api

import (
	"errors"
	"github.com/gin-gonic/gin"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/models"
	"net/http"
)

// ActionUser метод для запроса объекта пользователя или его изменения
// Auth godoc
// @Summary Returns an users object
// @Description user object
// @Tags admin
// @Accept  json
// @Produce  json
// @Param  body body models.AdminCommandToUser true "User фсешщт"
// @Success 200 {object} models.User
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /admin/user [post]
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
