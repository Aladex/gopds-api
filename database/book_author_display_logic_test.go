package database

import (
	"testing"

	"gopds-api/models"

	"github.com/stretchr/testify/assert"
)

// The author line of a book, decided from its legacy authors and the author
// credits of its current snapshot. These cases need no database: the query
// only gathers the two lists, and everything shown follows from them.

func linked(name string, id int64) models.AuthorDisplay {
	return models.AuthorDisplay{Name: name, LegacyAuthorID: &id}
}

func unlinked(name string) models.AuthorDisplay {
	return models.AuthorDisplay{Name: name}
}

func fromFile(name string) creditName           { return creditName{Name: name} }
func selected(name string) creditName           { return creditName{Name: name, Selected: true} }
func legacy(id int64, name string) legacyAuthor { return legacyAuthor{ID: id, Name: name} }

func layerLine(authors ...models.AuthorDisplay) models.BookAuthorDisplay {
	return models.BookAuthorDisplay{Source: models.AuthorDisplayLayer, Authors: authors}
}

func legacyLine(why models.AuthorDisplayFallback, authors ...models.AuthorDisplay) models.BookAuthorDisplay {
	if authors == nil {
		authors = []models.AuthorDisplay{}
	}
	return models.BookAuthorDisplay{Source: models.AuthorDisplayLegacy, Fallback: why, Authors: authors}
}

func TestAuthorLineFollowsTheFileOrderAndLinksByWords(t *testing.T) {
	got := resolveAuthorDisplay(&bookAuthorSources{
		legacy:  []legacyAuthor{legacy(1, "Петров Иван"), legacy(2, "Сидорова Анна")},
		credits: []creditName{selected("Анна Сидорова"), selected("Иван Петрович Петров")},
	})
	// The file lists Sidorova first; the patronymic the legacy name dropped
	// is an extra word of the credit, not a different person.
	assert.Equal(t, layerLine(linked("Анна Сидорова", 2), linked("Иван Петрович Петров", 1)), got)
}

func TestAuthorWordsIgnoreCaseYoAndPunctuation(t *testing.T) {
	got := resolveAuthorDisplay(&bookAuthorSources{
		legacy: []legacyAuthor{
			legacy(7, "Семёнов Пётр"), legacy(8, "Салтыков-Щедрин Михаил"), legacy(9, "Толкин Дж.Р.Р."),
		},
		credits: []creditName{
			selected("петр семенов"),
			selected("Михаил Евграфович Салтыков Щедрин"),
			selected("Дж. Р. Р. Толкин"),
		},
	})
	// Initials run together in one spelling and spaced in the other, a
	// hyphen in one and a space in the other: the same words.
	assert.Equal(t, layerLine(
		linked("петр семенов", 7),
		linked("Михаил Евграфович Салтыков Щедрин", 8),
		linked("Дж. Р. Р. Толкин", 9),
	), got)
}

func TestBookWithoutCreditsKeepsItsLegacyAuthors(t *testing.T) {
	got := resolveAuthorDisplay(&bookAuthorSources{
		legacy: []legacyAuthor{legacy(3, "Иванов Иван"), legacy(4, "Петров Пётр")},
	})
	assert.Equal(t, legacyLine(models.AuthorDisplayNoCredits, linked("Иванов Иван", 3), linked("Петров Пётр", 4)), got)

	assert.Equal(t, legacyLine(models.AuthorDisplayNoCredits), resolveAuthorDisplay(&bookAuthorSources{}),
		"a book with neither has an empty line, not a missing one")
}

// A legacy author none of the credits names is what an administrator's edit
// leaves behind: the book stays on its legacy authors.
func TestUnmatchedLegacyAuthorKeepsTheBookOnLegacy(t *testing.T) {
	got := resolveAuthorDisplay(&bookAuthorSources{
		legacy:  []legacyAuthor{legacy(5, "Иванов Иван"), legacy(6, "Новиков Олег")},
		credits: []creditName{selected("Иван Иванов")},
	})
	assert.Equal(t, legacyLine(models.AuthorDisplayLegacyUnmatched,
		linked("Иванов Иван", 5), linked("Новиков Олег", 6)), got)

	// The words of a legacy name must all be the credit's: a shared surname
	// is not the same person.
	got = resolveAuthorDisplay(&bookAuthorSources{
		legacy:  []legacyAuthor{legacy(5, "Иванов Пётр")},
		credits: []creditName{selected("Иван Иванов")},
	})
	assert.Equal(t, legacyLine(models.AuthorDisplayLegacyUnmatched, linked("Иванов Пётр", 5)), got)
}

// A credit no legacy author is — a nickname the legacy scan lost — is shown
// without a link, and the placeholder of a book the scan found no author for
// is no author at all.
func TestCreditWithoutLegacyCounterpartIsShownUnlinked(t *testing.T) {
	got := resolveAuthorDisplay(&bookAuthorSources{
		legacy:  []legacyAuthor{legacy(9, "Иванов Иван")},
		credits: []creditName{selected("Иван Иванов"), fromFile("Мастер")},
	})
	assert.Equal(t, layerLine(linked("Иван Иванов", 9), unlinked("Мастер")), got)

	got = resolveAuthorDisplay(&bookAuthorSources{
		credits: []creditName{fromFile("Козьма Прутков")},
	})
	assert.Equal(t, layerLine(unlinked("Козьма Прутков")), got)

	got = resolveAuthorDisplay(&bookAuthorSources{
		legacy:  []legacyAuthor{legacy(10, models.UnknownAuthorName)},
		credits: []creditName{fromFile("Аноним")},
	})
	assert.Equal(t, layerLine(unlinked("Аноним")), got)

	// A legacy name with no word in it says nothing either way.
	got = resolveAuthorDisplay(&bookAuthorSources{
		legacy:  []legacyAuthor{legacy(11, " — ")},
		credits: []creditName{fromFile("Аноним")},
	})
	assert.Equal(t, layerLine(unlinked("Аноним")), got)
}

// Every legacy author is matched when some pairing matches them all, even
// where the first credit could take the second credit's only partner.
func TestAuthorLinksPairEveryLegacyAuthorWhenTheyCan(t *testing.T) {
	got := resolveAuthorDisplay(&bookAuthorSources{
		legacy:  []legacyAuthor{legacy(1, "Петров Иван"), legacy(2, "Сидоров Иван")},
		credits: []creditName{selected("Иван Петров Сидоров"), selected("Иван Петров")},
	})
	assert.Equal(t, layerLine(linked("Иван Петров Сидоров", 2), linked("Иван Петров", 1)), got)
}

// One legacy author linked to the book twice is one author.
func TestDuplicateLegacyLinkIsOneAuthor(t *testing.T) {
	got := resolveAuthorDisplay(&bookAuthorSources{
		legacy: []legacyAuthor{legacy(12, "Иванов Иван"), legacy(12, "Иванов Иван")},
	})
	assert.Equal(t, legacyLine(models.AuthorDisplayNoCredits, linked("Иванов Иван", 12)), got)

	got = resolveAuthorDisplay(&bookAuthorSources{
		legacy:  []legacyAuthor{legacy(12, "Иванов Иван"), legacy(12, "Иванов Иван")},
		credits: []creditName{selected("Иван Иванов")},
	})
	assert.Equal(t, layerLine(linked("Иван Иванов", 12)), got)
}

// A name shown as the file spells it loses its capitals by the legacy scan's
// own rule, one word at a time; a selected name is shown as normalized.
func TestShoutedFileNamesAreTitleCasedForDisplay(t *testing.T) {
	got := resolveAuthorDisplay(&bookAuthorSources{
		legacy: []legacyAuthor{legacy(13, "Верн Жюль"), legacy(14, "Иванов Иван")},
		credits: []creditName{
			fromFile("ЖЮЛЬ ВЕРН"),
			fromFile("ИВАНОВ Иван"),
			selected("ДЖОРДЖ ОРУЭЛЛ"),
			fromFile("Иван Тургенев"),
		},
	})
	assert.Equal(t, layerLine(
		linked("Жюль Верн", 13),
		linked("Иванов Иван", 14),
		unlinked("ДЖОРДЖ ОРУЭЛЛ"),
		unlinked("Иван Тургенев"),
	), got)
}

// The legacy scan pairs first and last names by index, so one author without
// a first name shifts every name after it: the catalog's names are wrong,
// but every word in them is the file's. Such a book is shown from the file,
// and a glued name links nowhere — it is no one.
func TestGluedLegacyNamesGiveWayToTheFile(t *testing.T) {
	got := resolveAuthorDisplay(&bookAuthorSources{
		legacy: []legacyAuthor{legacy(1, "Толстой Лев"), legacy(2, "Пушкин Анна")},
		credits: []creditName{
			selected("Лев Николаевич Толстой"), selected("Александр Пушкин"), selected("Анна Ахматова"),
		},
	})
	assert.Equal(t, layerLine(
		linked("Лев Николаевич Толстой", 1),
		unlinked("Александр Пушкин"),
		unlinked("Анна Ахматова"),
	), got)
}

// Accepted until phase 4: an edit that only takes words away from the file's
// authors, or moves the file's words between authors, leaves no word the file
// lacks, and the line cannot tell it from the legacy scan's own misreadings —
// so the file's line is shown. Only an edit that brings in a word the file
// does not have keeps the book on its legacy authors. These cases pin that
// choice; phase 4 writes edits to the layer and ends the ambiguity.
func TestSubtractiveAndRecombiningEditsGiveWayToTheFile(t *testing.T) {
	t.Run("a word removed from an author", func(t *testing.T) {
		got := resolveAuthorDisplay(&bookAuthorSources{
			legacy:  []legacyAuthor{legacy(1, "Петров")},
			credits: []creditName{selected("Иван Петрович Петров")},
		})
		assert.Equal(t, layerLine(linked("Иван Петрович Петров", 1)), got)
	})

	t.Run("a patronymic removed", func(t *testing.T) {
		// The legacy name never had one; the edit and the scan read alike.
		got := resolveAuthorDisplay(&bookAuthorSources{
			legacy:  []legacyAuthor{legacy(2, "Петров Иван")},
			credits: []creditName{selected("Иван Петрович Петров")},
		})
		assert.Equal(t, layerLine(linked("Иван Петрович Петров", 2)), got)
	})

	t.Run("an author removed", func(t *testing.T) {
		got := resolveAuthorDisplay(&bookAuthorSources{
			legacy:  []legacyAuthor{legacy(3, "Петров Иван")},
			credits: []creditName{selected("Иван Петров"), selected("Анна Сидорова")},
		})
		assert.Equal(t, layerLine(linked("Иван Петров", 3), unlinked("Анна Сидорова")), got)
	})

	t.Run("words moved between authors", func(t *testing.T) {
		got := resolveAuthorDisplay(&bookAuthorSources{
			legacy:  []legacyAuthor{legacy(4, "Sidorov Ivan"), legacy(5, "Petrov Anna")},
			credits: []creditName{selected("Ivan Petrov"), selected("Anna Sidorov")},
		})
		assert.Equal(t, layerLine(unlinked("Ivan Petrov"), unlinked("Anna Sidorov")), got)
	})

	t.Run("a word the file lacks keeps the edit", func(t *testing.T) {
		got := resolveAuthorDisplay(&bookAuthorSources{
			legacy:  []legacyAuthor{legacy(6, "Петров-Водкин Иван")},
			credits: []creditName{selected("Иван Петров")},
		})
		assert.Equal(t, legacyLine(models.AuthorDisplayLegacyUnmatched, linked("Петров-Водкин Иван", 6)), got)
	})
}
