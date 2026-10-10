package opdsutils

import (
	"fmt"
	"strconv"

	"github.com/spf13/viper"

	"gopds-api/internal/posters"
	"gopds-api/models"
)

func rels() []string {
	return []string{
		"http://opds-spec.org/image",
		"x-stanza-cover-image",
		"http://opds-spec.org/thumbnail",
		"x-stanza-cover-image-thumbnail",
	}
}

func createPostersLink(book *models.Book) []Link {
	var links []Link
	posterLink := viper.GetString("app.cdn") + "/books-posters/no-cover.png"
	if book.Cover {
		posterLink = fmt.Sprintf("%s/books-posters/%s",
			viper.GetString("app.cdn"),
			posters.RelativePath(book.Path, book.FileName))
	}
	for _, r := range rels() {
		links = append(links, Link{
			Href: posterLink,
			Rel:  r,
			Type: "image/jpeg",
		})
	}
	return links
}

// CreateItem creates an BookItem for xml generate. authors is the book's
// author line: each name becomes an <author>, and a name linked to a catalog
// author also gets a link to that author's books; a name without one is
// shown with no link to guess at.
func CreateItem(book *models.Book, authors []models.AuthorDisplay, isKoreader bool) Item {
	posterLinks := createPostersLink(book)
	linkPath := "/opds/get/"

	links := []Link{
		{
			Href: linkPath + "fb2/" + strconv.FormatInt(book.ID, 10),
			Rel:  "http://opds-spec.org/acquisition/open-access",
			Type: "application/fb2+zip",
		},
		{
			Href: linkPath + "epub/" + strconv.FormatInt(book.ID, 10),
			Rel:  "http://opds-spec.org/acquisition/open-access",
			Type: "application/epub+zip",
		},
		{
			Href: linkPath + "mobi/" + strconv.FormatInt(book.ID, 10),
			Rel:  "http://opds-spec.org/acquisition/open-access",
			Type: "application/x-mobipocket-ebook",
		},
	}
	links = append(links, posterLinks...)

	// Add links to author's books
	var itemAuthors []Author
	for _, author := range authors {
		itemAuthors = append(itemAuthors, Author{
			Name: author.Name,
			ID:   author.LegacyAuthorID,
		})
		if author.LegacyAuthorID == nil {
			continue
		}
		// Link to browse all books by this author
		links = append(links, Link{
			Href:  fmt.Sprintf("/opds/new/0/%d", *author.LegacyAuthorID),
			Rel:   "related",
			Type:  "application/atom+xml;profile=opds-catalog",
			Title: fmt.Sprintf("Все книги: %s", author.Name),
		})
	}

	annotation := book.Annotation
	if isKoreader && len(annotation) > 20 {
		annotation = annotation[:20]
	}

	return Item{
		Title:       book.Title,
		Link:        links,
		Authors:     itemAuthors,
		Description: annotation,
		Id:          strconv.FormatInt(book.ID, 10),
		Updated:     book.RegisterDate,
		Language:    book.Lang,
		Issued:      book.DocDate,
	}
}
