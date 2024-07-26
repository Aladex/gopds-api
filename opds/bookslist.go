package opds

import (
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/models"
	"gopds-api/opdsutils"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func hasNextPage(limit, currentPage, totalCount int) bool {
	totalPages := totalCount / limit
	if currentPage < totalPages {
		return true
	}
	return false
}

func GetNewBooks(c *gin.Context) {
	filters := models.BookFilters{
		Limit:  10,
		Offset: 0,
		Title:  "",
		Author: 0,
		Series: 0,
		Lang:   "",
	}
	userID := c.GetInt64("user_id")

	hf, err := database.HaveFavs(userID)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	if c.FullPath() == "/opds/favorites/:page" {
		filters.Fav = true

	}

	pageNum, err := strconv.Atoi(c.Param("page"))
	if err != nil {
		logrus.Println(err)
		httputil.NewError(c, http.StatusBadRequest, errors.New("bad request"))
		return
	}
	authorID, err := strconv.Atoi(c.Param("author"))
	if err != nil {
		authorID = 0
	}
	if pageNum > 0 {
		filters.Offset = pageNum * 10
	}

	if authorID > 0 {
		filters.Author = authorID
	}

	books, tc, err := database.GetBooks(userID, filters)
	if err != nil {
		c.XML(500, err)
		return
	}
	var np string
	if filters.Fav {
		np = fmt.Sprintf("/opds/favorites/%d", pageNum+1)
	} else {
		np = fmt.Sprintf("/opds/new/%d/%d", pageNum+1, authorID)
	}
	rootLinks := []opdsutils.Link{
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

	if hasNextPage(filters.Limit, pageNum, tc) {
		rootLinks = append(rootLinks, opdsutils.Link{
			Href: np,
			Rel:  "next",
			Type: "application/atom+xml;profile=opds-catalog"})
	}

	feed := &opdsutils.Feed{
		Title:   "Лепробиблиотека",
		Links:   rootLinks,
		Updated: time.Now(),
	}
	feed.Items = []*opdsutils.Item{}

	if !filters.Fav && hf && pageNum == 0 && filters.Author == 0 {
		feed.Items = append(feed.Items, &opdsutils.Item{
			Title: "Избранное",
			Link: []opdsutils.Link{
				{
					Href: "/opds/favorites/0",
					Type: "application/atom+xml;profile=opds-catalog",
				},
			},
			Id:      "tag:search:favorites",
			Updated: time.Now(),
			Content: "Избранное",
		})
	}

	// Check if userAgent contains koreader
	isKoreader := false
	if strings.Contains(c.GetHeader("User-Agent"), "KOReader") {
		isKoreader = true
	}

	for _, book := range books {
		authors := []string{}
		bookItem := opdsutils.CreateItem(book, isKoreader)
		for _, author := range book.Authors {
			authors = append(authors, author.FullName)
		}
		feed.Items = append(feed.Items, &bookItem)
	}

	atom, err := feed.ToAtom()
	if err != nil {
		logrus.Println(err)
	}

	c.Data(200, "application/atom+xml;charset=utf-8", []byte(atom))
	return
}
