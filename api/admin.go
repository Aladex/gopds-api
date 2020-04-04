package api

import (
	"errors"
	"github.com/gin-gonic/gin"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/models"
	"net/http"
)

// ExportAnswer структура ответа найденных книг и полного списка языков для компонента Books.vue
type UsersAnswer struct {
	Users  []models.User `json:"users"`
	Length int           `json:"length"`
}

func GetUsers(c *gin.Context) {
	var filters models.UserFilters
	if err := c.ShouldBindJSON(&filters); err == nil {
		users, count, err := database.GetUserList(filters)
		if err != nil {
			c.JSON(500, err)
			return
		}
		lenght := count / 10
		if count-lenght*10 > 0 {
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
