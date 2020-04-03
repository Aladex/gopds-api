package api

import (
	"errors"
	"github.com/gin-gonic/gin"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/models"
	"net/http"
)

// Registration creates a new user
// Auth godoc
// @Summary creates a new user
// @Description creates a new user
// @Tags login
// @Accept  json
// @Produce  json
// @Param  body body models.RegisterRequest true "User Data"
// @Success 201 {object} string
// @Failure 409 {object} httputil.HTTPError
// @Router /register [post]
func Registration(c *gin.Context) {
	var newUser models.RegisterRequest
	if err := c.ShouldBindJSON(&newUser); err == nil {
		if !newUser.CheckValues() {
			httputil.NewError(c, http.StatusConflict, errors.New("bad form"))
			return
		}

		_, err := database.CheckInvite(newUser.Invite)
		if err != nil {
			httputil.NewError(c, http.StatusConflict, errors.New("bad invite"))
			return
		}

		err = database.CreateUser(newUser)
		if err != nil {
			httputil.NewError(c, http.StatusConflict, errors.New("user is already exists"))
			return
		}
		c.JSON(201, "user is successfully created")
		return
	}
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad request"))
}
