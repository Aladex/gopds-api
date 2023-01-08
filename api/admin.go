package api

import (
	"errors"
	"github.com/gin-gonic/gin"
	"gopds-api/database"
	"gopds-api/fb2scan"
	"gopds-api/httputil"
	"gopds-api/models"
	"net/http"
)

// UsersAnswer struct for users list in admin space
type UsersAnswer struct {
	Users  []models.User `json:"users"`
	Length int           `json:"length"`
}

// StartScan func for start scan books
// Auth godoc
// @Summary start scan books
// @Description start scan books
// @Tags admin
// @Param Authorization header string true "Just token without bearer"
// @Accept  json
// @Produce  json
// @Success 200 {object} models.Result
// @Failure 500 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Router /admin/scan [get]
func StartScan(c *gin.Context) {
	go fb2scan.GetArchivesList()
	c.JSON(200, models.Result{
		Result: "ok",
		Error:  nil,
	})
}

// UpdateCovers start update covers
// Auth godoc
// @Summary start update covers
// @Description start update covers
// @Tags admin
// @Param Authorization header string true "Just token without bearer"
// @Accept  json
// @Produce  json
// @Success 200 {object} models.Result
// @Failure 500 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Router /admin/covers [get]
func UpdateCovers(c *gin.Context) {
	go fb2scan.UpdateCovers()
	c.JSON(200, models.Result{
		Result: "ok",
		Error:  nil,
	})
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

// UpdateBook method for update book
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
