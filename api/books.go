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

// ExportAnswer struct for books list response
type ExportAnswer struct {
	Books  []models.Book `json:"books"`
	Length int           `json:"length"`
}

// GetLangs method for get langs from db
// Auth godoc
// @Summary method for get langs from db
// @Description method for get langs from db
// @Param Authorization header string true "Just token without bearer"
// @Accept  json
// @Produce  json
// @Success 200 {object} ExportAnswer
// @Failure 401 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Router /books/langs [get]
func GetLangs(c *gin.Context) {
	langs := database.GetLanguages()
	if langs != nil {
		c.JSON(200, gin.H{"langs": langs})
		return
	}
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
}

// GetBooks method for get books from db and return them in json
// Auth godoc
// @Summary method for get books from db and return them in json
// @Description method for get books from db and return them in json
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
	userID := c.GetInt64("user_id")
	if err := c.ShouldBindWith(&filters, binding.Query); err == nil {
		books, count, err := database.GetBooks(userID, filters)
		if err != nil {
			c.JSON(500, err)
			return
		}
		lenght := count / 10
		if count-lenght*10 > 0 {
			lenght++
		}
		c.JSON(200, ExportAnswer{
			Books:  books,
			Length: lenght,
		})
		return
	}
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
}

// FavBook add or remove book from favorites for user
// Auth godoc
// @Summary add or remove book from favorites for user
// @Description add or remove book from favorites for user
// @Accept  json
// @Produce  json
// @Param  body body models.FavBook true "Book Data"
// @Success 200 {object} ExportAnswer
// @Failure 400 {object} httputil.HTTPError
// @Router /fav [post]
func FavBook(c *gin.Context) {
	dbId := c.GetInt64("user_id")
	var favBook models.FavBook
	if err := c.ShouldBindJSON(&favBook); err == nil {
		res, err := database.FavBook(dbId, favBook)
		if err != nil {
			httputil.NewError(c, http.StatusBadRequest, err)
			return
		}
		c.JSON(200, gin.H{"have_favs": res})
	}
}
