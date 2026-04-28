package opds

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/opdsutils"

	"github.com/gin-gonic/gin"
)

const collectionsPageSize = 10

// GetCollections returns a navigation feed listing public curated collections.
func GetCollections(c *gin.Context) {
	pageNum, err := strconv.Atoi(c.Param("page"))
	if err != nil {
		pageNum = 0
	}

	ctx := context.Background()
	collections, total, err := database.ListPublicCuratedCollections(ctx, pageNum+1, collectionsPageSize)
	if err != nil {
		logging.Errorf("Failed to list public collections: %v", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
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

	if hasNextPage(collectionsPageSize, pageNum, total) {
		rootLinks = append(rootLinks, opdsutils.Link{
			Href: fmt.Sprintf("/opds/collections/%d", pageNum+1),
			Rel:  "next",
			Type: "application/atom+xml;profile=opds-catalog",
		})
	}

	feed := &opdsutils.Feed{
		Title:   "Подборки",
		Id:      fmt.Sprintf("tag:collections:%d", pageNum),
		Links:   rootLinks,
		Updated: time.Now(),
	}
	feed.Items = []*opdsutils.Item{}

	for _, col := range collections {
		feed.Items = append(feed.Items, &opdsutils.Item{
			Title: col.Name,
			Link: []opdsutils.Link{
				{
					Href: fmt.Sprintf("/opds/collections/%d/0", col.ID),
					Type: "application/atom+xml;profile=opds-catalog",
				},
			},
			Id:      fmt.Sprintf("tag:collection:%d", col.ID),
			Updated: col.CreatedAt,
			Content: col.Name,
		})
	}

	atom, err := feed.ToAtom()
	if err != nil {
		logging.Errorf("Error converting collections feed to Atom: %v", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.Data(http.StatusOK, "application/atom+xml;charset=utf-8", []byte(atom))
}

// GetCollectionBooks returns an acquisition feed with books from a specific collection.
func GetCollectionBooks(c *gin.Context) {
	collectionID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	pageNum, err := strconv.Atoi(c.Param("page"))
	if err != nil {
		pageNum = 0
	}

	userID := c.GetInt64("user_id")

	ctx := context.Background()
	col, err := database.GetPublicCuratedCollection(ctx, collectionID)
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	offset := pageNum * 10
	books, total, err := database.GetPublicCollectionBooksPage(ctx, collectionID, offset, 10)
	if err != nil {
		logging.Errorf("Failed to get collection books: %v", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	_ = userID // reserved for future fav support

	rootLinks := []opdsutils.Link{
		{
			Href: "/opds",
			Rel:  "start",
			Type: "application/atom+xml;profile=opds-catalog",
		},
		{
			Href: "/opds/collections/0",
			Rel:  "up",
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

	if hasNextPage(10, pageNum, total) {
		rootLinks = append(rootLinks, opdsutils.Link{
			Href: fmt.Sprintf("/opds/collections/%d/%d", collectionID, pageNum+1),
			Rel:  "next",
			Type: "application/atom+xml;profile=opds-catalog",
		})
	}

	feed := &opdsutils.Feed{
		Title:   col.Name,
		Id:      fmt.Sprintf("tag:collection:%d:books:%d", collectionID, pageNum),
		Links:   rootLinks,
		Updated: time.Now(),
	}
	feed.Items = []*opdsutils.Item{}

	for _, book := range books {
		bookItem := opdsutils.CreateItem(book, false)
		feed.Items = append(feed.Items, &bookItem)
	}

	atom, err := feed.ToAtom()
	if err != nil {
		logging.Errorf("Error converting collection books feed to Atom: %v", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.Data(http.StatusOK, "application/atom+xml;charset=utf-8", []byte(atom))
}
