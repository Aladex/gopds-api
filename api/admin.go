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

// GetUsers method for get users list in admin space
// Auth godoc
// @Summary Returns users list
// @Description users list for admin space
// @Tags admin
// @Accept  json
// @Produce  json
// @Param  body body models.UserFilters true "User filters"
// @Success 200 {object} UsersAnswer
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /admin/users [post]
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

// GetInvites return invites list
// Auth godoc
// @Summary return invites list
// @Description return invites list
// @Tags admin
// @Param Authorization header string true "Just token without bearer"
// @Accept  json
// @Produce  json
// @Success 200 {object} models.Result
// @Failure 500 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Router /admin/invites [get]
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

// ChangeInvite method for change or create invite
// Auth godoc
// @Summary method for change or create invite
// @Description method for change or create invite
// @Tags admin
// @Accept  json
// @Produce  json
// @Param  body body models.InviteRequest true "Invite params"
// @Success 200 {object} models.Result
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /admin/invite [post]
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
//
// This method attempts to bind the incoming JSON payload to a Book model. If the binding is successful,
// it proceeds to update the book's information in the database using the UpdateBook function. If the database
// operation is successful, it responds with a JSON payload indicating whether the book has favorites.
// In case of an error during JSON binding or the database operation, it responds with an appropriate HTTP error status and message.
//
// @Summary Update book information
// @Description Updates the information for a specific book based on the provided JSON payload.
// @Tags books
// @Accept json
// @Produce json
// @Param body body models.Book true "Book update information"
// @Success 200 {object} gin.H "have_favs indicates whether the book has favorites"
// @Failure 400 {object} httputil.HTTPError "Bad request - invalid input parameters"
// @Failure 500 {object} httputil.HTTPError "Internal server error"
func UpdateBook(c *gin.Context) {
	var bookToUpdate models.Book
	if err := c.ShouldBindJSON(&bookToUpdate); err == nil {
		res, err := database.UpdateBook(bookToUpdate)
		if err != nil {
			httputil.NewError(c, http.StatusBadRequest, err)
			return
		}
		c.JSON(200, gin.H{"have_favs": res})
	}
}
