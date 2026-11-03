package authorentity_test

import (
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/stretchr/testify/assert"
)

// Middle names in the given-name guard (E4n): a middle name one spells two
// ways, or its initials, is one middle name; a given name of several words
// is the given name and its middle names. Patronymics stay strict.

// The Rowling shape: «Джоан Кэтлин» written whole in the given name, beside
// «Джоан Кэтлинг Роулинг» through «Д. К. Роулинг», is one person.
func TestAGivenNameOfTwoWordsIsTheGivenAndTheMiddleName(t *testing.T) {
	w := newWorld(t, threeNames(t, person("Роулинг", "Джоан", "Кэтлинг"), person("Роулинг", "Д.", "К."),
		person("Роулинг", "Джоан Кэтлин")))
	w.pass()
	first := w.decide(authorentity.CandidateInitials, 11, 1)
	second := w.decide(authorentity.CandidateInitials, 11, 21)
	p := w.pass()
	assert.Equal(t, applied(first), w.state(first))
	assert.Equal(t, applied(second), w.state(second))
	same(t, p, 1, 21)
}

func TestTheMiddleNameComparison(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		same bool
	}{
		{"Хэдли", "Хэдлис", true},
		{"Хэдли", "Хедли", true},
		{"Гилл", "Гил", true},
		{"Кэтлинг", "Кетлин", true},
		{"Белкнап", "Белнап", true},
		{"Джон", "Дж", true},
		{"Роналд", "Роналд Руэл", true},
		{"Эндрю", "Эдвард", false},
		// Two fathers, however close the spelling (one or two edits apart,
		// the level-0 typo merge joins them first).
		{"Трофимович", "Трифонович", false},
	} {
		t.Run(tc.a+"/"+tc.b, func(t *testing.T) {
			w := newWorld(t, threeNames(t, person("Савченко", "Иван", tc.a), person("Савченко", "И."),
				person("Савченко", "Иван", tc.b)))
			w.pass()
			kept := w.decide(authorentity.CandidateInitials, 11, 1)
			second := w.decide(authorentity.CandidateInitials, 11, 21)
			p := w.pass()
			assert.Equal(t, applied(kept), w.state(kept))
			if tc.same {
				assert.Equal(t, applied(second), w.state(second))
				same(t, p, 1, 21)
				return
			}
			assert.Equal(t, conflict(second, authorentity.ConflictGivenName), w.state(second))
			apart(t, p, 1, 21)
		})
	}
}
