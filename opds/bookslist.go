package opds

import (
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/opdsutils"
	"net/http"
	"strconv"
	"time"
)

var customLog = logging.SetLog()

func GetNewBooks(c *gin.Context) {
	filters := models.BookFilters{
		Limit:  10,
		Offset: 0,
		Title:  "",
		Author: 0,
		Series: 0,
		Lang:   "",
	}

	pageNum, err := strconv.Atoi(c.Param("page"))
	if err != nil {
		customLog.Println(err)
		httputil.NewError(c, http.StatusBadRequest, errors.New("bad request"))
		return
	}
	authorID, err := strconv.Atoi(c.Param("author"))
	if err != nil {
		customLog.Println(err)
		httputil.NewError(c, http.StatusBadRequest, errors.New("bad request"))
		return
	}
	if pageNum > 0 {
		filters.Offset = pageNum * 10
	}

	if authorID > 0 {
		filters.Author = authorID
	}

	books, _, _, err := database.GetBooks(filters)
	if err != nil {
		c.XML(500, err)
		return
	}
	now := time.Now()
	rootLinks := []opdsutils.Link{
		{
			Href: fmt.Sprintf("/opds/new/%d/%d", pageNum+1, authorID),
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
			Href: "/opds/search?searchTerms={searchTerms}",
			Rel:  "search",
			Type: "application/atom+xml",
		},
	}

	feed := &opdsutils.Feed{
		Title:       "Новые книги",
		Links:       rootLinks,
		Id:          fmt.Sprintf("tag:search:new:book:%d", pageNum),
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
