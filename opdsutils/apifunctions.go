package opdsutils

import (
	"fmt"
	"strconv"
	"strings"

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

	// Atom requires an author on every entry: a book whose line names no
	// one — or only blank names, spaces of any kind — is shown as the
	// unknown author, linked to nothing.
	authors = nonBlankAuthors(authors)
	if len(authors) == 0 {
		authors = []models.AuthorDisplay{{Name: models.UnknownAuthorName}}
	}

	// Add links to author's books
	var itemAuthors []Author
	for _, author := range authors {
		itemAuthors = append(itemAuthors, Author{
			Name: author.Name,
			ID:   author.LegacyAuthorID,
		})
		switch {
		case len(author.Entities) == 1:
			// Under the entity identity a name links to its entity's books.
			itemAuthors[len(itemAuthors)-1].Entity = author.Entities[0].ID.String()
			links = append(links, allBooksLink("/opds/new/0/"+author.Entities[0].ID.String(), author.Name))
		case len(author.Entities) > 1:
			// A name of several people (design E, §6 item 5): an <author> has
			// one address, so the name has none, and each person's books get
			// a link of their own, under the person's name.
			for i := range author.Entities {
				links = append(links, allBooksLink("/opds/new/0/"+author.Entities[i].ID.String(),
					author.Entities[i].Title()))
			}
		case author.LegacyAuthorID != nil:
			// Link to browse all books by this author
			links = append(links, allBooksLink(fmt.Sprintf("/opds/new/0/%d", *author.LegacyAuthorID), author.Name))
		}
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

// nonBlankAuthors drops the names that are spaces only (Unicode-aware).
func nonBlankAuthors(authors []models.AuthorDisplay) []models.AuthorDisplay {
	out := make([]models.AuthorDisplay, 0, len(authors))
	for _, a := range authors {
		if strings.TrimSpace(a.Name) != "" {
			out = append(out, a)
		}
	}
	return out
}

// allBooksLink is an entry's link to all the books of one of its authors.
func allBooksLink(href, name string) Link {
	return Link{
		Href:  href,
		Rel:   "related",
		Type:  "application/atom+xml;profile=opds-catalog",
		Title: fmt.Sprintf("Все книги: %s", name),
	}
}
