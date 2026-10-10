package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"gopds-api/database"
	"gopds-api/httputil"

	"github.com/gin-gonic/gin"
)

// The comparison report is read a window of the catalog at a time: the
// whole catalog takes longer than the server may spend on one answer. The
// command-line report reads it whole.
const (
	authorDisplayReportDefaultBooks = 20_000
	authorDisplayReportMaxBooks     = 50_000
)

// compareAuthorDisplay reports on up to books books with IDs above afterID.
type compareAuthorDisplay func(ctx context.Context, afterID int64, books int) (*database.AuthorDisplayReport, error)

// AuthorDisplayReport compares the legacy authors with the author layer over
// one window of the catalog.
// @Summary Compare the legacy authors with the author layer
// @Description Counts and sample book IDs per category for the books above after_id; next_after_id starts the next window, null at the end.
// @Tags admin
// @Produce json
// @Param after_id query int false "Start after this book ID (default 0)"
// @Param books query int false "Window size (default 20000, at most 50000)"
// @Success 200 {object} database.AuthorDisplayReport
// @Failure 400 {object} httputil.HTTPError
// @Router /api/admin/authors/display-report [get]
func AuthorDisplayReport(c *gin.Context) {
	authorDisplayReport(func(ctx context.Context, afterID int64, books int) (*database.AuthorDisplayReport, error) {
		return database.CompareAuthorDisplay(ctx, database.GetDB(), afterID, books)
	})(c)
}

func authorDisplayReport(compare compareAuthorDisplay) gin.HandlerFunc {
	return func(c *gin.Context) {
		afterID, books := int64(0), authorDisplayReportDefaultBooks
		var err error
		if raw, ok := c.GetQuery("after_id"); ok {
			if afterID, err = strconv.ParseInt(raw, 10, 64); err != nil || afterID < 0 {
				httputil.NewError(c, http.StatusBadRequest, errors.New("bad_after_id"))
				return
			}
		}
		if raw, ok := c.GetQuery("books"); ok {
			if books, err = strconv.Atoi(raw); err != nil || books <= 0 {
				httputil.NewError(c, http.StatusBadRequest, errors.New("bad_books"))
				return
			}
			books = min(books, authorDisplayReportMaxBooks)
		}
		report, err := compare(c.Request.Context(), afterID, books)
		if err != nil {
			httputil.NewError(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, report)
	}
}
