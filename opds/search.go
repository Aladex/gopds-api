package opds

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"gopds-api/opdsutils"
	"time"
)

type OpdsBookFilters struct {
	Search string `form:"searchTerms" json:"searchTerms" binding:"required"`
}

func Search(c *gin.Context) {
	var filters OpdsBookFilters
	if err := c.ShouldBindWith(&filters, binding.Query); err == nil {
		now := time.Now()
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
				Href: "/opds/search?searchTerm={searchTerms}",
				Rel:  "search",
				Type: "application/atom+xml",
			},
		}

		feed := &opdsutils.Feed{
			Title:       "Поиск книг",
			Links:       rootLinks,
			Id:          "tag:search:::",
			Description: "Books Feed",
			Created:     now,
		}
		feed.Items = []*opdsutils.Item{
			{
				Title: "Поиск авторов",
				Link: []opdsutils.Link{
					{
						Href:  "/opds/search?searchType=authors&searchTerm=" + filters.Search,
						Type:  "application/atom+xml;profile=opds-catalog",
						Title: "",
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
						Href:  "/opds/search?searchType=books&searchTerm=" + filters.Search,
						Type:  "application/atom+xml;profile=opds-catalog",
						Title: "",
					},
				},
				Id:      "tag:search:author",
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
	}

}
