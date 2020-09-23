package opds

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/opdsutils"
	"net/http"
	"time"
)

var customLog = logging.SetLog()

// ExportAnswer структура ответа найденных книг и полного списка языков для компонента Books.vue
type ExportAnswer struct {
	Books     []models.Book    `json:"books"`
	Languages models.Languages `json:"langs"`
	Length    int              `json:"length"`
}

func GetNewBooks(c *gin.Context) {
	var filters models.BookFilters
	if err := c.ShouldBindWith(&filters, binding.Query); err == nil {
		books, _, _, err := database.GetBooks(filters)
		if err != nil {
			c.JSON(500, err)
			return
		}
		now := time.Now()
		feed := &opdsutils.Feed{
			Title: "Новые книги",
			Links: []opdsutils.Link{
				{Href: "/opds/new"},
			},
			Id:          "tag:search:new:book:0",
			Description: "Books Feed",
			Created:     now,
		}
		feed.Items = []*opdsutils.Item{}
		for _, book := range books {
			authors := []string{}
			bookItem := opdsutils.CreateItem(book)
			for _, author := range book.Authors {
				authors = append(authors, author.FullName)
			}
			feed.Items = append(feed.Items, &bookItem)
		}
		atom, err := feed.ToAtom()
		if err != nil {
			customLog.Fatal(err)
		}

		c.Data(200, "application/atom+xml;charset=utf-8", []byte(atom))
		return
	}
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
}
