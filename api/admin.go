package api

import (
	"errors"
	"github.com/gin-gonic/gin"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/logging"
	"gopds-api/models"
	"net/http"
)

var customLog = logging.SetLog()

// UsersAnswer структура ответа найденных пользователей для компонента Admin.vue
type UsersAnswer struct {
	Users  []models.User `json:"users"`
	Length int           `json:"length"`
}

// GetUsers метод для запроса списка книг из БД opds
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

// GetInvites возвращает лист из инвайтов
// Auth godoc
// @Summary возвращает лист из инвайтов
// @Description возвращает лист из инвайтов
// @Tags admin
// @Param Authorization header string true "Just token without bearer"
// @Accept  json
// @Produce  json
// @Success 200 {object} []models.Invite
// @Failure 500 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Router /admin/invites [get]
func GetInvites(c *gin.Context) {
	var invites []models.Invite
	err := database.GetInvites(&invites)
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(200, invites)
}

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
