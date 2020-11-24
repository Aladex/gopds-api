package opds

import (
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/models"
	"gopds-api/opdsutils"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const notFound = `<?xml version="1.0" encoding="utf-8"?>
 <feed xmlns="http://www.w3.org/2005/Atom" xmlns:dc="http://purl.org/dc/terms/" xmlns:os="http://a9.com/-/spec/opensearch/1.1/" xmlns:opds="http://opds-spec.org/2010/catalog"> 
 <id>tag:search:books:notfound:</id>
 <title>Результат поиска</title>
 <link href="/opds-opensearch.xml" rel="search" type="application/opensearchdescription+xml" />
 <link href="/opds/search?searchTerm={searchTerms}" rel="search" type="application/atom+xml" />
 <link href="/opds" rel="start" type="application/atom+xml;profile=opds-catalog" />
</feed>`

type searchTerms struct {
	Search string `form:"searchTerms" json:"searchTerms" binding:"required"`
}

// OpdsBooksSearch struct for book search
type OpdsBooksSearch struct {
	Title string `form:"title" json:"title" binding:"required"`
	Page  int    `form:"page" json:"page"`
}

// OpdsAuthorSearch struct for author search
type OpdsAuthorSearch struct {
	Name string `form:"name" json:"name" binding:"required"`
	Page int    `form:"page" json:"page"`
}

// Search basic search XML view for books and author search
func Search(c *gin.Context) {
	var filters searchTerms
	if err := c.ShouldBindWith(&filters, binding.Query); err == nil {
		now := time.Now()
		searchRootLinks := []opdsutils.Link{
			{
				Href: "/opds",
				Rel:  "start",
				Type: "application/atom+xml;profile=opds-catalog",
			},
			{
				Href: "/opds-opensearch.xml",
				Rel:  "search",
				Type: "application/opensearchdescription+xml",
			},
			{
				Href: "/opds/search?searchTerms={searchTerms}",
				Rel:  "search",
				Type: "application/atom+xml",
			},
		}

		feed := &opdsutils.Feed{
			Title:       "Поиск книг",
			Links:       searchRootLinks,
			Id:          "tag:search:::",
			Description: "Поиск книг",
			Created:     now,
		}
		feed.Items = []*opdsutils.Item{
			{
				Title: "Поиск авторов",
				Link: []opdsutils.Link{
					{
						Href: "/opds/search-author?name=" + url.QueryEscape(filters.Search),
						Type: "application/atom+xml;profile=opds-catalog",
					},
				},
				Id:      "tag:search:author",
				Updated: time.Now(),
				Created: time.Now(),
				Content: "Поиск авторов по фамилии",
			},
			{
				Title: "Поиск книг",
				Link: []opdsutils.Link{
					{
						Href:  "/opds/books?title=" + url.QueryEscape(filters.Search),
						Type:  "application/atom+xml;profile=opds-catalog",
						Title: "",
					},
				},
				Id:      "tag:search:book",
				Updated: time.Now(),
				Created: time.Now(),
				Content: "Поиск книг по названию",
			},
		}

		atom, err := feed.ToAtom()
		if err != nil {
			customLog.Println(err)
		}

		c.Data(200, "application/atom+xml;charset=utf-8", []byte(atom))
		return
	}
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))

}

// GetBooks returns an list of books
func GetBooks(c *gin.Context) {
	var filters OpdsBooksSearch
	if err := c.ShouldBindWith(&filters, binding.Query); err == nil {
		dbFilters := models.BookFilters{
			Limit:  10,
			Offset: 0,
			Title:  filters.Title,
		}

		if filters.Page > 0 {
			dbFilters.Offset = filters.Page * 10
		}

		books, _, _, err := database.GetBooks(dbFilters)
		if err != nil {
			c.XML(500, err)
			return
		}
		if len(books) == 0 {
			c.Data(200, "application/atom+xml;charset=utf-8", []byte(notFound))
			return
		}

		now := time.Now()
		rootLinks := []opdsutils.Link{
			{
				Href: fmt.Sprintf("/opds/books?title=%s&page=%d", url.QueryEscape(filters.Title), filters.Page+1),
				Rel:  "next",
				Type: "application/atom+xml;profile=opds-catalog",
			},
			{
				Href: "/opds",
				Rel:  "start",
				Type: "application/atom+xml;profile=opds-catalog",
			},
			{
				Href: "/opds-opensearch.xml",
				Rel:  "search",
				Type: "application/opensearchdescription+xml",
			},
			{
				Href: "/opds/search?searchTerm={searchTerms}",
				Rel:  "search",
				Type: "application/atom+xml",
			},
		}

		feed := &opdsutils.Feed{
			Title:       "Новые книги",
			Links:       rootLinks,
			Id:          fmt.Sprintf("tag:search:new:book:%d", filters.Page),
			Description: "Поиск книги",
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
			customLog.Println(err)
		}

		c.Data(200, "application/atom+xml;charset=utf-8", []byte(atom))
		return
	}
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
}

// GetAuthor returns authors from search request
func GetAuthor(c *gin.Context) {
	var filters OpdsAuthorSearch
	if err := c.ShouldBindWith(&filters, binding.Query); err == nil {
		dbFilters := models.AuthorFilters{
			Limit:  10,
			Offset: 0,
			Author: filters.Name,
		}

		if filters.Page > 0 {
			dbFilters.Offset = filters.Page * 10
		}

		authors, _, err := database.GetAuthors(dbFilters)

		if len(authors) == 0 {
			c.Data(200, "application/atom+xml;charset=utf-8", []byte(notFound))
			return
		}

		if err != nil {
			c.XML(500, err)
			return
		}
		now := time.Now()
		rootLinks := []opdsutils.Link{
			{
				Href: fmt.Sprintf("/opds/search-author?name=%s&page=%d", url.QueryEscape(filters.Name), filters.Page+1),
				Rel:  "next",
				Type: "application/atom+xml;profile=opds-catalog",
			},
			{
				Href: "/opds",
				Rel:  "start",
				Type: "application/atom+xml;profile=opds-catalog",
			},
			{
				Href: "/opds-opensearch.xml",
				Rel:  "search",
				Type: "application/opensearchdescription+xml",
			},
			{
				Href: "/opds/search?searchTerm={searchTerms}",
				Rel:  "search",
				Type: "application/atom+xml",
			},
		}

		feed := &opdsutils.Feed{
			Title:       "Поиск автора",
			Links:       rootLinks,
			Id:          fmt.Sprintf("tag:search:new:author:%d", filters.Page),
			Description: "Author Feed",
			Created:     now,
		}
		feed.Items = []*opdsutils.Item{}
		for _, a := range authors {
			feed.Items = append(feed.Items, &opdsutils.Item{
				Title: a.FullName,
				Link: []opdsutils.Link{
					{
						Href:  fmt.Sprintf("/opds/new/0/%d", a.ID),
						Rel:   "search",
						Type:  "application/atom+xml;profile=opds-catalog",
						Title: a.FullName,
					},
				},
				Id:      strconv.FormatInt(a.ID, 10),
				Updated: time.Now(),
				Created: time.Now(),
				Content: a.FullName,
			})
		}
		atom, err := feed.ToAtom()
		if err != nil {
			customLog.Println(err)
		}
		c.Data(200, "application/atom+xml;charset=utf-8", []byte(atom))
		return
	}
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
}
