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
	"strconv"

	// "strconv"
	"strings"
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
			Link: []opdsutils.Link{
				{Href: "/opds/new"},
			},
			Id:          "tag:search:new:book:0",
			Description: "Books Feed",
			Created:     now,
		}
		feed.Items = []*opdsutils.Item{}
		for _, book := range books {
			authors := []string{}
			for _, author := range book.Authors {
				authors = append(authors, author.FullName)
			}
			feed.Items = append(feed.Items, &opdsutils.Item{
				Id:          strconv.FormatInt(book.ID, 10),
				Title:       book.Title,
				Updated:     book.RegisterDate,
				Description: book.Annotation,
				Link: []opdsutils.Link{
					{Href: "/opds/author/232398",
						Rel:    "related",
						Type:   "application/atom+xml",
						Length: ""},
				},
			})
		}
		atom, err := feed.ToAtom()
		atom = strings.ReplaceAll(atom, `<feed xmlns="http://www.w3.org/2005/Atom">`, `<feed xmlns="http://www.w3.org/2005/Atom" xmlns:dc="http://purl.org/dc/terms/" xmlns:os="http://a9.com/-/spec/opensearch/1.1/" xmlns:opds="http://opds-spec.org/2010/catalog">`)
		if err != nil {
			customLog.Fatal(err)
		}

		c.Data(200, "application/atom+xml;charset=utf-8", []byte(atom))
		return
	}
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
}

func TestXML(c *gin.Context) {
	atom := `<?xml version="1.0" encoding="utf-8"?>
 <feed xmlns="http://www.w3.org/2005/Atom" xmlns:dc="http://purl.org/dc/terms/" xmlns:os="http://a9.com/-/spec/opensearch/1.1/" xmlns:opds="http://opds-spec.org/2010/catalog"> <id>tag:search:new:book:0</id>
 <title>Новинки с 16.09.2020 по 23.09.2020</title>
 <updated>2020-09-23T12:07:56+02:00</updated>
 <icon>/favicon.ico</icon>
 <link href="/opds-opensearch.xml" rel="search" type="application/opensearchdescription+xml" />
 <link href="/opds/search?searchTerm={searchTerms}" rel="search" type="application/atom+xml" />
 <link href="/opds" rel="start" type="application/atom+xml;profile=opds-catalog" />
 <link href="/opds/new/1/new/" rel="next" type="application/atom+xml;profile=opds-catalog" />
 <entry> <updated>2020-09-23T12:07:56+02:00</updated>
 <title>Колдовская птица Набрид-Кюнт</title>
 <link href="/opds/author/232398" rel="related" type="application/atom+xml" title="Все книги автора Смит Фара Роуз" />
 <id>tag:book:d8e8b8a20b9b6a1e4a0b24f4fb5d0c7e</id>
</entry>
</feed>
`
	c.Data(200, "application/atom+xml;charset=utf-8", []byte(atom))
}
