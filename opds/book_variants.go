package opds

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/opdsutils"

	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v10"
)

// bookVariantsOf is the database call behind GetBookVariants, replaced in
// tests.
var bookVariantsOf = database.BookVariantsOf

// GetBookVariants serves the card of one book file as an acquisition feed:
// the card's visible files in the card's order, each a full entry with its
// own formats, author line and translators. A book no reader can see is
// answered like a path nothing serves.
func (f *Feeds) GetBookVariants(c *gin.Context) {
	bookID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || bookID <= 0 {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	books, _, err := bookVariantsOf(c.Request.Context(), bookID, c.GetInt64("user_id"))
	if errors.Is(err, pg.ErrNoRows) || (err == nil && len(books) == 0) {
		feedNotFound(c)
		return
	}
	if err != nil {
		logging.Error(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	feed := &opdsutils.Feed{
		Title:   fmt.Sprintf("%s: %s", opdsutils.TitleVariantsFeed, askedTitle(books, bookID)),
		Id:      fmt.Sprintf("tag:book:%d:variants", bookID),
		Links:   variantsLinks(),
		Updated: time.Now(),
		Items:   bookItems(c, f.Entries, f.AuthorLines, books, isKoreader(c)),
	}
	atom, err := feed.ToAtom()
	if err != nil {
		logging.Error(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Data(http.StatusOK, atomContentType, []byte(atom))
}

// askedTitle is the title of the book the feed was asked for, as the feed
// names its card.
func askedTitle(books []models.Book, bookID int64) string {
	for i := range books {
		if books[i].ID == bookID {
			return books[i].Title
		}
	}
	return ""
}

// variantsLinks are the feed links of a book's variants: start, up to the
// newest books and the searches, like the other acquisition feeds.
func variantsLinks() []opdsutils.Link {
	return []opdsutils.Link{
		{
			Href: hrefRoot,
			Rel:  relStart,
			Type: typeOpdsCatalog,
		},
		{
			Href: hrefNewBooks,
			Rel:  relUp,
			Type: typeOpdsCatalog,
		},
		{
			Href: hrefOpenSearch,
			Rel:  relSearch,
			Type: typeOpenSearchDsc,
		},
		{
			Href: hrefSearchTerms,
			Rel:  relSearch,
			Type: typeAtom,
		},
	}
}
