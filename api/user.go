package api

import (
	"errors"
	"github.com/gin-gonic/gin"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/models"
	"net/http"
)

// GetUser метод для запроса объекта пользователя
// Auth godoc
// @Summary Returns an users object
// @Description user object
// @Tags admin
// @Accept  json
// @Produce  json
// @Param  body body models.User true "User filter"
// @Success 200 {object} models.User
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /admin/user [post]
func GetUser(c *gin.Context) {
	var user models.User
	if err := c.ShouldBindJSON(&user); err == nil {
		err := database.GetUser(&user)
		if err != nil {
			c.JSON(500, err)
			return
		}
		c.JSON(200, user)
		return
	}
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad request"))
}

// ChangeUser метод для изменения пользователя в бд
// Auth godoc
// @Summary Returns an user object
// @Description user object
// @Tags admin
// @Accept  json
// @Produce  json
// @Param  body body models.User true "User filter"
// @Success 200 {object} models.User
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /admin/change-user [post]
func ChangeUser(c *gin.Context) {
	var user models.User
	if err := c.ShouldBindJSON(&user); err == nil {
		changedUser, err := database.ChangeUser(user)
		if err != nil {
			c.JSON(500, err)
			return
		}
		c.JSON(200, changedUser)
		return
	}
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad request"))
}
