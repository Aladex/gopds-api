package opdsutils

import (
	"fmt"
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
	posterLink := "/books-posters/no-cover.png"
	if book.Cover {
		posterLink = fmt.Sprintf("/books-posters/%s/%s.jpg",
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

	links := []Link{
		{
			Href: "/opds/download/fb2/" + strconv.FormatInt(book.ID, 10),
			Rel:  "http://opds-spec.org/acquisition/open-access",
			Type: "application/fb2+zip",
		},
		{
			Href: "/opds/download/epub/" + strconv.FormatInt(book.ID, 10),
			Rel:  "http://opds-spec.org/acquisition/open-access",
			Type: "application/epub+zip",
		},
		{
			Href: "/opds/download/mobi/" + strconv.FormatInt(book.ID, 10),
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
		Created:     book.RegisterDate,
	}
}
