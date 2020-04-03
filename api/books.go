package api

import (
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"gopds-api/database"
	"gopds-api/models"
)

// ExportAnswer структура ответа найденных книг и полного списка языков для компонента Books.vue
type ExportAnswer struct {
	Books     []models.Book    `json:"books"`
	Languages models.Languages `json:"langs"`
	Length    int              `json:"length"`
}

// GetBooks метод для запроса списка книг из БД opds
// Auth godoc
// @Summary возвращает JSON с книгами
// @Description возвращает JSON с книгами
// @Param Authorization header string true "Just token without bearer"
// @Param  limit query int true "Limit"
// @Param  offset query int true "Offset"
// @Param  title query string false "Title of book"
// @Param  author query int false "Author ID"
// @Accept  json
// @Produce  json
// @Success 200 {object} ExportAnswer
// @Failure 500 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Router /books/list [get]
func GetBooks(c *gin.Context) {
	var filters models.BookFilters
	if err := c.ShouldBindWith(&filters, binding.Query); err == nil {
		books, langs, count, err := database.GetBooks(filters)
		if err != nil {
			c.JSON(500, err)
			return
		}
		lenght := count / 10
		if count-lenght*10 > 0 {
			lenght++
		}
		c.JSON(200, ExportAnswer{
			Books:     books,
			Languages: langs,
			Length:    lenght,
		})
	}
}
