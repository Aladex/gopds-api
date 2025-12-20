package opds

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/logging"
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
		logging.Error(err)
		c.AbortWithStatus(http.StatusBadRequest)
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
		logging.Error(err)
		c.AbortWithStatus(http.StatusInternalServerError)
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

	feedId := fmt.Sprintf("tag:root:new:%d:%d", pageNum, authorID)
	if filters.Fav {
		feedId = fmt.Sprintf("tag:root:favorites:%d", pageNum)
	}

	feed := &opdsutils.Feed{
		Title:   "Лепробиблиотека",
		Id:      feedId,
		Links:   rootLinks,
		Updated: time.Now(),
	}
	feed.Items = []*opdsutils.Item{}

	// Show navigation items only on the root page (page 0, no author filter, not favorites)
	if !filters.Fav && pageNum == 0 && filters.Author == 0 {
		// Add favorites link if user has favorites
		if hf {
			feed.Items = append(feed.Items, &opdsutils.Item{
				Title: "Избранное",
				Link: []opdsutils.Link{
					{
						Href: "/opds/favorites/0",
						Type: "application/atom+xml;profile=opds-catalog",
					},
				},
				Id:      "tag:nav:favorites",
				Updated: time.Now(),
				Content: "Избранное",
			})
		}

		// Add languages navigation
		feed.Items = append(feed.Items, &opdsutils.Item{
			Title: "По языкам",
			Link: []opdsutils.Link{
				{
					Href: "/opds/languages",
					Type: "application/atom+xml;profile=opds-catalog",
				},
			},
			Id:      "tag:nav:languages",
			Updated: time.Now(),
			Content: "Книги по языкам",
		})
	}

	// Check if userAgent contains koreader
	isKoreader := false
	if strings.Contains(c.GetHeader("User-Agent"), "KOReader") {
		isKoreader = true
	}

	for _, book := range books {
		bookItem := opdsutils.CreateItem(book, isKoreader)
		feed.Items = append(feed.Items, &bookItem)
	}

	atom, err := feed.ToAtom()
	if err != nil {
		logging.Error(err)
	}

	c.Data(200, "application/atom+xml;charset=utf-8", []byte(atom))
	return
}
