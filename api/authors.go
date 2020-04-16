package api

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/models"
	"net/http"
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

// GetAuthor метод для запроса информации по автору из БД opds
// Auth godoc
// @Summary возвращает JSON с автором
// @Description возвращает JSON с автором
// @Param Authorization header string true "Just token without bearer"
// @Param  author query string false "Author ID"
// @Accept  json
// @Produce  json
// @Success 200 {object} models.AuthorRequest
// @Failure 500 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Router /books/author [post]
func GetAuthor(c *gin.Context) {
	var filter models.AuthorRequest
	if err := c.ShouldBindJSON(&filter); err == nil {
		author, err := database.GetAuthor(filter)
		if err != nil {
			c.JSON(500, err)
			return
		}
		c.JSON(200, author)
		return
	}
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
}
