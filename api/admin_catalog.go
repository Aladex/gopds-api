package api

import (
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/models"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

type AdminAuthorsSearchResponse struct {
	Authors []models.Author `json:"authors"`
}

type AdminSeriesSearchResponse struct {
	Series []models.Series `json:"series"`
}

// SearchAuthors returns author suggestions for admin edit dialogs.
// @Summary Search authors
// @Description Search authors by name for admin edit dialogs
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param q query string true "Search query"
// @Param limit query int false "Limit"
// @Produce json
// @Success 200 {object} AdminAuthorsSearchResponse
// @Failure 500 {object} httputil.HTTPError
// @Router /api/admin/authors/search [get]
func SearchAuthors(c *gin.Context) {
	query := strings.TrimSpace(c.Query("q"))
	limit := parseSearchLimit(c.Query("limit"))

	authors, err := database.SearchAuthors(query, limit)
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, AdminAuthorsSearchResponse{
		Authors: authors,
	})
}

// SearchSeries returns series suggestions for admin edit dialogs.
// @Summary Search series
// @Description Search series by name for admin edit dialogs
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param q query string true "Search query"
// @Param limit query int false "Limit"
// @Produce json
// @Success 200 {object} AdminSeriesSearchResponse
// @Failure 500 {object} httputil.HTTPError
// @Router /api/admin/series/search [get]
func SearchSeries(c *gin.Context) {
	query := strings.TrimSpace(c.Query("q"))
	limit := parseSearchLimit(c.Query("limit"))

	series, err := database.SearchSeries(query, limit)
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, AdminSeriesSearchResponse{
		Series: series,
	})
}

func parseSearchLimit(raw string) int {
	if raw == "" {
		return 20
	}
	limit, err := strconv.Atoi(raw)
	if err != nil {
		return 20
	}
	return limit
}
