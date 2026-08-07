package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/models"
)

// AuthorAnswer struct for authors list on search page
type AuthorAnswer struct {
	Authors []models.Author `json:"authors"`
	Length  int             `json:"length"`
}

// pageCount turns a number of authors into the number of pages the search page
// draws its pager from.
//
// It rounds up, which the count/10+1 it replaced did not: a total that divided
// evenly offered a further page with nothing on it, and no results at all still
// offered page one.
func pageCount(total, limit int) int {
	if limit <= 0 {
		limit = defaultAuthorsPerPage
	}
	return (total + limit - 1) / limit
}

// defaultAuthorsPerPage is the page size assumed for a request that names none,
// matching what the search page asks for.
const defaultAuthorsPerPage = 10

// GetAuthor method for retrieving author information from the database
// Auth godoc
// @Summary Retrieve author information
// @Description Get information about a specific author from the database
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param  author query string false "Author ID"
// @Accept  json
// @Produce  json
// @Success 200 {object} models.AuthorRequest
// @Failure 500 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Router /api/books/author [post]
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
