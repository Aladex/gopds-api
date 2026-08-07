package opds

import (
	"errors"
	"net/url"
	"time"

	"net/http"

	"gopds-api/httputil"
	"gopds-api/logging"
	"gopds-api/opdsutils"

	"github.com/gin-gonic/gin"
)

const notFound = `<?xml version="1.0" encoding="utf-8"?>
 <feed xmlns="http://www.w3.org/2005/Atom" xmlns:dc="http://purl.org/dc/terms/" xmlns:os="http://a9.com/-/spec/opensearch/1.1/" xmlns:opds="http://opds-spec.org/2010/catalog">
 <id>tag:search:books:notfound</id>
 <title>Результат поиска</title>
 <link href="/opds-opensearch.xml" rel="search" type="application/opensearchdescription+xml" />
 <link href="/opds/search?searchTerms={searchTerms}" rel="search" type="application/atom+xml" />
 <link href="/opds" rel="start" type="application/atom+xml;profile=opds-catalog" />
</feed>`

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
	searchTerms := c.Query("searchTerms")
	if searchTerms == "" {
		httputil.NewError(c, http.StatusBadRequest, errors.New("searchTerms parameter is required"))
		return
	}

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
		Title:   "Поиск книг",
		Id:      "tag:root:search",
		Links:   searchRootLinks,
		Updated: time.Now(),
	}
	feed.Items = []*opdsutils.Item{
		{
			Title: "Поиск авторов",
			Link: []opdsutils.Link{
				{
					Href: "/opds/search-author?name=" + url.QueryEscape(searchTerms),
					Type: "application/atom+xml;profile=opds-catalog",
				},
			},
			Id:      "tag:search:author",
			Updated: time.Now(),
			Content: "Поиск авторов по фамилии",
		},
		{
			Title: "Поиск книг",
			Link: []opdsutils.Link{
				{
					Href:  "/opds/books?title=" + url.QueryEscape(searchTerms),
					Type:  "application/atom+xml;profile=opds-catalog",
					Title: "",
				},
			},
			Id:      "tag:search:book",
			Updated: time.Now(),
			Content: "Поиск книг по названию",
		},
	}

	atom, err := feed.ToAtom()
	if err != nil {
		logging.Errorf("Error converting feed to Atom: %v", err)
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	c.Data(200, "application/atom+xml;charset=utf-8", []byte(atom))
}
