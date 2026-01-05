package api

import (
	"errors"
	"github.com/gin-gonic/gin"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/models"
	"net/http"
	"strconv"
)

// SetupAdminRoutes sets up the admin routes
func SetupAdminRoutes(r *gin.RouterGroup) {
	r.POST("/users", GetUsers)
	r.GET("/invites", GetInvites)
	r.POST("/invite", ChangeInvite)
	r.POST("/user", ActionUser)
	r.DELETE("/user/:id", DeleteUser)
	r.POST("/update-book", UpdateBook)
	r.PUT("/books/:id", UpdateBookByID)

	// Setup duplicate management routes
	SetupDuplicatesRoutes(r)
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
// @Description Update the information of a specific book based on the provided JSON payload. Only provided fields will be updated.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept json
// @Produce json
// @Param body body models.BookUpdateRequest true "Book update information"
// @Success 200 {object} models.Result "Book updated successfully"
// @Failure 400 {object} httputil.HTTPError "Bad request - invalid input parameters"
// @Failure 404 {object} httputil.HTTPError "Book not found"
// @Failure 500 {object} httputil.HTTPError "Internal server error"
// @Router /api/admin/update-book [post]
func UpdateBook(c *gin.Context) {
	var updateReq models.BookUpdateRequest
	if err := c.ShouldBindJSON(&updateReq); err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("invalid_request_body"))
		return
	}

	// Update the book in database
	updatedBook, err := database.UpdateBook(updateReq)
	if err != nil {
		// Check if book was not found
		if err.Error() == "pg: no rows in result set" {
			httputil.NewError(c, http.StatusNotFound, errors.New("book_not_found"))
			return
		}
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	// Return the updated book
	c.JSON(http.StatusOK, models.Result{
		Result: updatedBook,
		Error:  nil,
	})
}

// UpdateBookByID updates the information of a specific book using URL parameter.
// @Summary Update book information by ID
// @Description Update the information of a specific book by ID based on the provided JSON payload. Only provided fields will be updated.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Book ID"
// @Accept json
// @Produce json
// @Param body body models.BookUpdateRequest true "Book update information"
// @Success 200 {object} models.Result "Book updated successfully"
// @Failure 400 {object} httputil.HTTPError "Bad request - invalid input parameters"
// @Failure 404 {object} httputil.HTTPError "Book not found"
// @Failure 500 {object} httputil.HTTPError "Internal server error"
// @Router /api/admin/books/{id} [put]
func UpdateBookByID(c *gin.Context) {
	bookIDStr := c.Param("id")
	bookID, err := strconv.ParseInt(bookIDStr, 10, 64)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("invalid_book_id"))
		return
	}

	var updateReq models.BookUpdateRequest
	if err := c.ShouldBindJSON(&updateReq); err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("invalid_request_body"))
		return
	}

	// Set the ID from URL parameter
	updateReq.ID = bookID

	// Update the book in database
	updatedBook, err := database.UpdateBook(updateReq)
	if err != nil {
		// Check if book was not found
		if err.Error() == "pg: no rows in result set" {
			httputil.NewError(c, http.StatusNotFound, errors.New("book_not_found"))
			return
		}
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	// Return the updated book
	c.JSON(http.StatusOK, models.Result{
		Result: updatedBook,
		Error:  nil,
	})
}
