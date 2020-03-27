package api

import (
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"gopds-api/database"
	"gopds-api/models"
)

// AuthorAnswer структура ответа для списка авторов при поиске
type AuthorAnswer struct {
	Authors []models.Author `json:"authors"`
	Length  int             `json:"length"`
}

// GetAuthors метод для запроса списка авторов из БД opds
// Auth godoc
// @Summary возвращает JSON с авторами
// @Description возвращает JSON с авторами
// @Param Authorization header string true "Just token without bearer"
// @Param  limit query int true "Limit"
// @Param  offset query int true "Offset"
// @Param  author query string false "Author ID"
// @Accept  json
// @Produce  json
// @Success 200 {object} ExportAnswer
// @Failure 500 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Router /books/authors [get]
func GetAuthors(c *gin.Context) {
	var filters models.AuthorFilters
	if err := c.ShouldBindWith(&filters, binding.Query); err == nil {
		authors, count, err := database.GetAuthors(filters)
		if err != nil {
			c.JSON(500, err)
			return
		}
		lenght := (count / 10) + 1
		c.JSON(200, AuthorAnswer{
			Authors: authors,
			Length:  lenght,
		})
	}
}
