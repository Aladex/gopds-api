package api

import (
	"errors"
	"github.com/gin-gonic/gin"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/models"
	"net/http"
)

// SetupAdminRoutes sets up the admin routes
func SetupAdminRoutes(r *gin.RouterGroup) {
	r.POST("/users", GetUsers)
	r.GET("/invites", GetInvites)
	r.POST("/invite", ChangeInvite)
	r.POST("/user", ActionUser)
	r.POST("/update-book", UpdateBook)
}

// UsersAnswer struct for users list in admin space
type UsersAnswer struct {
	Users  []models.User `json:"users"`
	Length int           `json:"length"`
}

// GetUsers method for fetching the list of users in the admin space
// Auth godoc
// @Summary Retrieve the list of users
// @Description Get a list of users for the admin space
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept  json
// @Produce  json
// @Param  body body models.UserFilters true "User filters"
// @Success 200 {object} UsersAnswer
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /api/admin/users [post]
func GetUsers(c *gin.Context) {
	var filters models.UserFilters
	if err := c.ShouldBindJSON(&filters); err == nil {
		users, count, err := database.GetUserList(filters)
		if err != nil {
			c.JSON(500, err)
			return
		}
		lenght := count / 50
		if count-lenght*50 > 0 {
			lenght++
		}
		c.JSON(200, UsersAnswer{
			Users:  users,
			Length: lenght,
		})
		return
	}
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad request"))
}

// GetInvites method for retrieving the list of invites
// Auth godoc
// @Summary Retrieve the list of invites
// @Description Get a list of invites
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept  json
// @Produce  json
// @Success 200 {object} models.Result
// @Failure 500 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Router /api/admin/invites [get]
func GetInvites(c *gin.Context) {
	invites := []models.Invite{}
	err := database.GetInvites(&invites)
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(200, models.Result{
		Result: invites,
		Error:  nil,
	})
}

// ChangeInvite method for changing or creating an invite
// Auth godoc
// @Summary Change or create an invite
// @Description Change or create an invite
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept  json
// @Produce  json
// @Param  body body models.InviteRequest true "Invite parameters"
// @Success 200 {object} models.Result
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /api/admin/invite [post]
func ChangeInvite(c *gin.Context) {
	var inviteRequest models.InviteRequest
	if err := c.ShouldBindJSON(&inviteRequest); err == nil {
		err := database.ChangeInvite(inviteRequest)
		if err != nil {
			httputil.NewError(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(200, models.Result{
			Result: "result_ok",
			Error:  nil,
		})
		return
	}
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
}

// UpdateBook updates the information of a specific book.
// @Summary Update book information
// @Description Update the information of a specific book based on the provided JSON payload.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept json
// @Produce json
// @Param body body models.Book true "Book update information"
// @Success 200 {object} models.Result "Book updated successfully"
// @Failure 400 {object} httputil.HTTPError "Bad request - invalid input parameters"
// @Failure 500 {object} httputil.HTTPError "Internal server error"
// @Router /api/admin/update-book [post]
func UpdateBook(c *gin.Context) {
	var bookToUpdate models.Book
	if err := c.ShouldBindJSON(&bookToUpdate); err == nil {
		res, err := database.UpdateBook(bookToUpdate)
		if err != nil {
			httputil.NewError(c, http.StatusBadRequest, err)
			return
		}
		// Return the result of the operation
		c.JSON(200, models.Result{
			Result: res,
			Error:  nil,
		})
	}
}
