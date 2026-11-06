package api

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"gopds-api/database"
	"gopds-api/internal/scanfixture"

	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v10"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An uploaded cover changes the book's flag directly, past UpdateBook: the flag
// and the book's mark commit together, and the mark is what brings the author
// summary's cover pile along without waiting for the daily walk.
func TestAnUploadedCoverMarksTheBook(t *testing.T) {
	requireSearchTestDB(t)
	db := scanfixture.ScratchDB(t)
	gin.SetMode(gin.TestMode)
	viper.Set("app.posters_path", t.TempDir())
	t.Cleanup(func() { viper.Set("app.posters_path", "") })

	var book int64
	_, err := db.QueryOne(pg.Scan(&book), `INSERT INTO opds_catalog_book
		(filename, path, format, title, annotation, lang, registerdate, docdate, approved, cover, md5, duplicate_hidden)
		VALUES ('cover.fb2', 'cover-test.zip', 'fb2', 'Книга', '', 'ru', now(), '', true, false, md5('cover'), false)
		RETURNING id`)
	require.NoError(t, err)
	_, err = db.Exec(`DELETE FROM book_author_display_dirty`)
	require.NoError(t, err)

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("cover", "cover.jpg")
	require.NoError(t, err)
	_, err = part.Write(testCover(t))
	require.NoError(t, err)
	require.NoError(t, form.Close())

	route := gin.New()
	route.POST("/books/:id/cover", UploadBookCover)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/books/"+strconv.FormatInt(book, 10)+"/cover", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	route.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	stored, err := database.GetBook(book)
	require.NoError(t, err)
	assert.True(t, stored.Cover)
	var marks int
	_, err = db.QueryOne(pg.Scan(&marks), `SELECT count(*) FROM book_author_display_dirty WHERE book_id = ?`, book)
	require.NoError(t, err)
	assert.Equal(t, 1, marks)
}
