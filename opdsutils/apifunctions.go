package opdsutils

import (
	"fmt"
	"github.com/spf13/viper"
	"gopds-api/models"
	"strconv"
	"strings"
)

func rels() []string {
	return []string{
		"http://opds-spec.org/image",
		"x-stanza-cover-image",
		"http://opds-spec.org/thumbnail",
		"x-stanza-cover-image-thumbnail",
	}
}

func createPostersLink(book models.Book) []Link {
	var links []Link
	posterLink := viper.GetString("app.cdn") + "/books-posters/no-cover.png"
	if book.Cover {
		posterLink = fmt.Sprintf("%s/books-posters/%s/%s.jpg",
			viper.GetString("app.cdn"),
			strings.ReplaceAll(book.Path, ".", "-"),
			strings.ReplaceAll(book.FileName, ".", "-"))
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

// CreateItem creates an BookItem for xml generate
func CreateItem(book models.Book) Item {
	posterLinks := createPostersLink(book)
	linkPath := "/opds/get/"
	// linkPath := "/opds/download/"

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
	itemAuthors := []Author{}
	for _, author := range book.Authors {
		itemAuthors = append(itemAuthors, Author{
			Name: author.FullName,
			ID:   author.ID,
		})
	}

	return Item{
		Title:       book.Title,
		Link:        links,
		Authors:     itemAuthors,
		Description: book.Annotation,
		Id:          strconv.FormatInt(book.ID, 10),
		Updated:     book.RegisterDate,
		Language:    book.Lang,
		Issued:      book.DocDate,
	}
}
