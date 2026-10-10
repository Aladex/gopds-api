package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The read model holds a book's author line as rows, one per name in line
// order, each with the key the book sorts by. The line is the one the card
// shows; the sort key follows the design (§2.1): a selected name sorts by its
// normalized sort name, a name from the file by "last, first middle" when the
// file gives a last name and by the name itself otherwise, a legacy name by
// itself — all compared without case and with ё read as е.

func credit(id int64, display string, selectedSort *string, first, middle, last string) creditName {
	c := creditName{ID: id, Name: display, First: first, Middle: middle, Last: last}
	if selectedSort != nil {
		c.Selected, c.SortName = true, *selectedSort
	}
	return c
}

func sortName(s string) *string { return &s }

func layerRow(book int64, pos int, display, sortKey string, creditID int64, legacyID *int64) authorDisplayRow {
	return authorDisplayRow{BookID: book, Position: pos, Display: display, SortKey: sortKey,
		Source: "layer", CreditID: &creditID, LegacyAuthorID: legacyID}
}

func legacyRow(book int64, pos int, display, sortKey string, legacyID int64) authorDisplayRow {
	return authorDisplayRow{BookID: book, Position: pos, Display: display, SortKey: sortKey,
		Source: "legacy", LegacyAuthorID: &legacyID}
}

func id64(id int64) *int64 { return &id }

func TestModelRowsFollowTheLineAndSortByTheNormalizedName(t *testing.T) {
	rows := authorDisplayRows(7, &bookAuthorSources{
		legacy: []legacyAuthor{legacy(1, "Толстой Лев")},
		credits: []creditName{
			// The file spells the name in Latin; the selected result's sort
			// name, not the file's parts, decides where it sorts.
			credit(101, "Лев Николаевич Толстой", sortName("Толстой, Лев Николаевич"), "Leo", "", "TOLSTOY"),
			credit(102, "ПУШКИН", nil, "", "", "ПУШКИН"),
		},
	})
	tolstoy := layerRow(7, 0, "Лев Николаевич Толстой", "толстой, лев николаевич", 101, id64(1))
	tolstoy.ExtendsLegacy = true // the patronymic the catalog dropped
	assert.Equal(t, []authorDisplayRow{tolstoy, layerRow(7, 1, "Пушкин", "пушкин", 102, nil)}, rows)
}

func TestModelSortKeyFallsBackThroughTheFileNameParts(t *testing.T) {
	rows := authorDisplayRows(8, &bookAuthorSources{credits: []creditName{
		// Selected, but the result has no sort name: the file's parts.
		credit(201, "Пётр Ильич Петров", sortName(""), "Пётр", "Ильич", "Петров"),
		// The file names the last name only.
		credit(202, "Сидоров", nil, "", "", "Сидоров"),
		// No last name at all: a nickname sorts by itself.
		credit(203, "Аноним", nil, "", "", ""),
	}})
	assert.Equal(t, []string{"петров, петр ильич", "сидоров", "аноним"}, sortKeys(rows))
}

func TestModelRowsOfALegacyLineAreTheLegacyAuthors(t *testing.T) {
	rows := authorDisplayRows(9, &bookAuthorSources{
		legacy: []legacyAuthor{legacy(3, "Гоголь Николай"), legacy(4, "Ёлкин Пётр")},
	})
	assert.Equal(t, []authorDisplayRow{
		legacyRow(9, 0, "Гоголь Николай", "гоголь николай", 3),
		legacyRow(9, 1, "Ёлкин Пётр", "елкин петр", 4),
	}, rows)
}

// A linked name extends its legacy author when it carries a word the
// catalog's name lacks — a patronymic, a given name in full: only such names,
// and names linked to no one, can find a book or an author the legacy names
// do not, so only they are searched in the model.
func TestModelMarksTheNamesThatSayMoreThanTheCatalog(t *testing.T) {
	rows := authorDisplayRows(11, &bookAuthorSources{
		legacy: []legacyAuthor{legacy(1, "Петров Иван"), legacy(2, "Сидорова Анна")},
		credits: []creditName{
			credit(301, "Иван Ильич Петров", nil, "Иван", "Ильич", "Петров"),
			credit(302, "Анна Сидорова", nil, "Анна", "", "Сидорова"),
			credit(303, "Мастер", nil, "", "", ""),
		},
	})
	extends := make([]bool, len(rows))
	for i := range rows {
		extends[i] = rows[i].ExtendsLegacy
	}
	assert.Equal(t, []bool{true, false, false}, extends,
		"the patronymic is new; the reordered name is not; an unlinked name extends no one")

	legacyOnly := authorDisplayRows(12, &bookAuthorSources{legacy: []legacyAuthor{legacy(3, "Гоголь Николай")}})
	assert.False(t, legacyOnly[0].ExtendsLegacy)
}

func TestModelHasNoRowsForABookWithNoAuthorAtAll(t *testing.T) {
	assert.Empty(t, authorDisplayRows(10, &bookAuthorSources{}))
}

func sortKeys(rows []authorDisplayRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.SortKey
	}
	return out
}

// Every book in the catalog has a first row, so a list sorted by author is
// one walk of the model's index: a book whose line names no one has a row
// that names no one and sorts after every name. A book that is gone has none.
func TestEveryBookOfTheCatalogHasAFirstRow(t *testing.T) {
	assert.Equal(t, []authorDisplayRow{{BookID: 5, Position: 0, Source: "none"}},
		modelRowsOf(5, &bookAuthorSources{}, true))
	assert.Empty(t, modelRowsOf(5, &bookAuthorSources{}, false))
	assert.Empty(t, modelRowsOf(5, &bookAuthorSources{legacy: []legacyAuthor{legacy(3, "Гоголь Николай")}}, false),
		"a book that is gone keeps no row, whatever was linked to it")
	assert.Equal(t, authorDisplayRows(6, &bookAuthorSources{legacy: []legacyAuthor{legacy(3, "Гоголь Николай")}}),
		modelRowsOf(6, &bookAuthorSources{legacy: []legacyAuthor{legacy(3, "Гоголь Николай")}}, true))
}
