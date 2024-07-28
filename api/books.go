package api

import (
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/spf13/viper"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/models"
	"gopds-api/utils"
	"net/http"
	"time"
)

// ExportAnswer struct for books list response
type ExportAnswer struct {
	Books  []models.Book `json:"books"`
	Length int           `json:"length"`
}

// langsAnswer struct for languages list response
type langsAnswer struct {
	Langs models.Languages `json:"langs"`
}

type favAnswer struct {
	HaveFavs bool `json:"have_favs"`
}

// GetLangs method for retrieving languages from the database
// Auth godoc
// @Summary Retrieve languages from the database
// @Description Get the list of languages from the database
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Tags books
// @Accept  json
// @Produce  json
// @Success 200 {object} langsAnswer "List of languages"
// @Failure 401 {object} httputil.HTTPError "Unauthorized"
// @Failure 403 {object} httputil.HTTPError "Forbidden"
// @Router /api/books/langs [get]
func GetLangs(c *gin.Context) {
	langs := database.GetLanguages()
	if langs != nil {
		c.JSON(200, langsAnswer{Langs: langs})
		return
	}
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
}

// GetBooks method for retrieving books from the database and returning them in JSON format
// Auth godoc
// @Summary Retrieve books from the database
// @Description Get the list of books from the database and return them in JSON format
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param  limit query int true "Limit"
// @Param  offset query int true "Offset"
// @Param  title query string false "Title of the book"
// @Param  author query int false "Author ID"
// @Tags books
// @Accept  json
// @Produce  json
// @Success 200 {object} ExportAnswer "List of books and length"
// @Failure 500 {object} httputil.HTTPError "Internal server error"
// @Failure 403 {object} httputil.HTTPError "Forbidden"
// @Router /api/books/list [get]
func GetBooks(c *gin.Context) {
	var filters models.BookFilters
	userID := c.GetInt64("user_id")
	if err := c.ShouldBindWith(&filters, binding.Query); err == nil {
		books, count, err := database.GetBooks(userID, filters)
		if err != nil {
			c.JSON(500, err)
			return
		}
		lenght := count / filters.Limit
		if count-lenght*filters.Limit > 0 {
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

// FavBook method for adding or removing a book from a user's favorites
// Auth godoc
// @Summary Add or remove a book from favorites
// @Description Add or remove a book from a user's favorites
// @Tags books
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept  json
// @Produce  json
// @Param  body body models.FavBook true "Book Data"
// @Success 200 {object} favAnswer "Status of the favorite action"
// @Failure 400 {object} httputil.HTTPError "Bad request"
// @Router /api/books/fav [post]
func FavBook(c *gin.Context) {
	dbId := c.GetInt64("user_id")
	var favBook models.FavBook
	if err := c.ShouldBindJSON(&favBook); err == nil {
		res, err := database.FavBook(dbId, favBook)
		if err != nil {
			httputil.NewError(c, http.StatusBadRequest, err)
			return
		}
		c.JSON(200, favAnswer{HaveFavs: res})
	}
}

// GetSignedBookUrl method for retrieving a book file from the database
// Auth godoc
// @Summary Retrieve a signed URL for a book file
// @Description Get a signed URL for a book file
// @Tags books
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param  format path string true "Book format"
// @Param  id path string true "Book ID"
// @Accept  json
// @Produce  json
// @Success 200 {object} models.Result "URL of the book file"
// @Failure 400 {object} httputil.HTTPError "Bad request"
// @Router /api/books/get/{format}/{id} [get]
func GetSignedBookUrl(c *gin.Context) {
	bookURL := fmt.Sprintf("%s/files/books/get/%s/%s", viper.GetString("project_url"), c.Param("format"), c.Param("id"))
	expiry := time.Now().Add(time.Hour * 24).Unix()

	signaturedUrl := utils.GenerateSignedURL(viper.GetString("secret_key"), bookURL, time.Duration(expiry))
	c.JSON(200, models.Result{Result: signaturedUrl, Error: nil})
}
