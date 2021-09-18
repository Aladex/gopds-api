package api

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"gopds-api/config"
	"gopds-api/database"
	"gopds-api/fb2scan"
	"gopds-api/httputil"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/static_assets"
	"io"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ExportAnswer структура ответа найденных книг и полного списка языков для компонента Books.vue
type ExportAnswer struct {
	Books  []models.Book `json:"books"`
	Length int           `json:"length"`
}

func generateCdnHash(s string) string {
	hash := md5.New()
	hash.Write([]byte(s))
	b := hash.Sum(nil)
	b64hash := base64.StdEncoding.EncodeToString(b)
	b64hash = strings.ReplaceAll(b64hash, "\n", "")
	b64hash = strings.ReplaceAll(b64hash, "+", "-")
	b64hash = strings.ReplaceAll(b64hash, "/", "_")
	b64hash = strings.ReplaceAll(b64hash, "=", "")
	return b64hash
}

func UploadBook(c *gin.Context) {
	file, err := c.FormFile("file")
	username := c.GetString("username")
	fmt.Println(c.GetString("username"))
	if err != nil {
		c.String(http.StatusBadRequest, fmt.Sprintf("get form err: %s", err.Error()))
		return
	}

	f, err := file.Open()

	fileReader := bufio.NewReader(f)

	fileBuffer := bytes.NewBuffer(nil)
	if _, err := io.Copy(fileBuffer, fileReader); err != nil {
		c.String(http.StatusBadRequest, fmt.Sprintf("upload file err: %s", err.Error()))
		return
	}
	fb2scan.SaveBook(fileBuffer.Bytes(), file.Filename, username)
}

// GetLangs метод для запроса списка языков из БД opds
// Auth godoc
// @Summary список языков
// @Description список языков
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

// GetBookPoster метод для запроса обложки
func GetBookPoster(c *gin.Context) {
	bookId, err := strconv.ParseInt(c.Param("book"), 10, 64)
	if err != nil {
		logging.CustomLog.Println(err)
		httputil.NewError(c, http.StatusBadRequest, errors.New("bad request"))
		return
	}
	var coverData []byte
	contentType := "image/png"
	cover, err := database.GetCover(bookId)
	if err != nil {
		coverData, err = static_assets.Asset("static_assets/posters/no-cover.png")
		if err != nil {
			c.JSON(500, err)
			return
		}
	} else {
		coverData, err = base64.StdEncoding.DecodeString(cover.Cover)
		if cover.ContentType != "" {
			contentType = cover.ContentType
		}
		if err != nil {
			c.JSON(500, err)
			return
		}
	}

	r := ioutil.NopCloser(bytes.NewReader(coverData)) // r type is io.ReadCloser

	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(r)
	if err != nil {
		c.JSON(500, err)
		return
	}
	err = r.Close()
	if err != nil {
		c.JSON(500, err)
		return
	}
	c.Header("Content-Type", contentType)
	_, err = io.Copy(c.Writer, buf)
	return
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

// CdnBookGenerate
func CdnBookGenerate(c *gin.Context) {
	bookID, err := strconv.ParseInt(c.Param("id"), 10, 0)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("bad_book_id"))
	}
	bookFormat := c.Param("format")
	expires := time.Now().Add(time.Minute * 10).Unix()
	path := fmt.Sprintf("/download/%s/%d", bookFormat, bookID)
	secret := config.AppConfig.GetString("app.cdn_key")
	token := generateCdnHash(fmt.Sprintf("%d%s %s", expires, path, secret))
	newLink := fmt.Sprintf("%s%s?md5=%s&expires=%d", config.AppConfig.GetString("app.file_book_cdn"), path, token, expires)
	c.Redirect(301, newLink)
}
