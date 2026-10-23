package screens

import (
	"testing"

	"gopds-api/models"

	"github.com/stretchr/testify/assert"
)

// A book's author names on a list line: its author line when the router read
// one; the catalog's authors otherwise — unless the line is the book's whole
// say (the entity identity), where an empty line names no one rather than
// handing back the catalog's names.
func TestAnEmptyLineOfRecordNamesNoOne(t *testing.T) {
	legacy := []models.Author{{ID: 177615, FullName: "Автор неизвестен"}}
	assert.Equal(t, "Автор неизвестен", AuthorsLine(&models.Book{Authors: legacy}), "no line read")
	assert.Equal(t, "Автор неизвестен", AuthorsLine(&models.Book{Authors: legacy, AuthorLine: []models.AuthorDisplay{}}),
		"an empty line under the legacy identity, as before")
	assert.Empty(t, AuthorsLine(&models.Book{Authors: legacy, AuthorLine: []models.AuthorDisplay{}, AuthorLineOnly: true}))
	assert.Equal(t, "Анна", AuthorsLine(&models.Book{Authors: legacy,
		AuthorLine: []models.AuthorDisplay{{Name: "Анна"}}, AuthorLineOnly: true}))
}
