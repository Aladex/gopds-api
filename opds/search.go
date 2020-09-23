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
	"strconv"
	"time"
)

type OpdsBookFilters struct {
	Search string `form:"searchTerms" json:"searchTerms" binding:"required"`
}

type OpdsAuthorRequest struct {
	Search string `form:"searchTerms" json:"searchTerms" binding:"required"`
}

type OpdsBooksSearch struct {
	Title string `form:"title" json:"title" binding:"required"`
	Page  int    `form:"page" json:"page"`
}

type OpdsAuthorSearch struct {
	Name string `form:"name" json:"name" binding:"required"`
	Page int    `form:"page" json:"page"`
}

func Search(c *gin.Context) {
	var filters OpdsBookFilters
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
			Description: "Books Feed",
			Created:     now,
		}
		feed.Items = []*opdsutils.Item{
			{
				Title: "Поиск авторов",
				Link: []opdsutils.Link{
					{
						Href: "/opds/search-author?name=" + filters.Search,
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
						Href:  "/opds/books?title=" + filters.Search,
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
	} else {
		fmt.Println(err)
		httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
	}

}

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

		now := time.Now()
		rootLinks := []opdsutils.Link{
			{
				Href: fmt.Sprintf("/opds/books?title=%s&page=%d", filters.Title, filters.Page+1),
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
			customLog.Println(err)
		}

		c.Data(200, "application/atom+xml;charset=utf-8", []byte(atom))
		return
	}
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
}

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
		if err != nil {
			c.XML(500, err)
			return
		}
		fmt.Println(authors)
		now := time.Now()
		rootLinks := []opdsutils.Link{
			{
				Href: fmt.Sprintf("/opds/search-author?name=%s&page=%d", filters.Name, filters.Page+1),
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
						Href:  fmt.Sprintf("/opds/author-profile?id=%d", a.ID),
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
