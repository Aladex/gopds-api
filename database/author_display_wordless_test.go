package database

import (
	"testing"

	"gopds-api/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A name with no letter or digit — a lone no-break space, a run of other
// Unicode spaces, punctuation, replacement characters — is not a name. The
// line is decided as if that credit, or that legacy author, were not there:
// the next name of the file, else the legacy authors, else a line that names
// no one. Production met it as book 389625, whose one credit is U+00A0.

func creditRowOf(chosen *string, source string) *authorRow {
	return &authorRow{CreditID: 9, ChosenName: chosen, SourceName: source}
}

func str(s string) *string { return &s }

func TestANameWithoutAWordIsNoName(t *testing.T) {
	for _, shape := range []string{
		"\u00a0",       // production: credit 568275
		"\u00a0\u00a0", // more of them
		" \u2003 ",     // an em space between ASCII ones
		"\u3000",       // ideographic space
		"\t",           // a tab
		"-",            // punctuation only
		"- -",          // legacy shape in the catalog dump
		"...",          // ditto
		"\ufffd\ufffd", // replacement characters (a broken encoding)
	} {
		_, ok := creditFromRow(creditRowOf(nil, shape))
		assert.False(t, ok, "credit %q", shape)
		_, ok = legacyFromRow(&authorRow{LegacyID: id64(7), SourceName: shape})
		assert.False(t, ok, "legacy author %q", shape)
	}

	c, ok := creditFromRow(creditRowOf(nil, "Иван Петров"))
	require.True(t, ok)
	assert.Equal(t, "Иван Петров", c.Name)
	assert.False(t, c.Selected)
}

// A selected result whose display name has no word gives way to the file's
// spelling, and the file's wordless spelling to the selected name.
func TestASelectedNameWithoutAWordGivesWayToTheFile(t *testing.T) {
	c, ok := creditFromRow(&authorRow{CreditID: 9, ChosenName: str("\u00a0"), ChosenSortName: "\u00a0", SourceName: "Иван Петров"})
	require.True(t, ok)
	assert.Equal(t, creditName{ID: 9, Name: "Иван Петров"}, c)

	c, ok = creditFromRow(&authorRow{CreditID: 9, ChosenName: str("Иван Петров"), ChosenSortName: "Петров, Иван", SourceName: "\u00a0"})
	require.True(t, ok)
	assert.Equal(t, creditName{ID: 9, Name: "Иван Петров", Selected: true, SortName: "Петров, Иван"}, c)

	_, ok = creditFromRow(&authorRow{CreditID: 9, ChosenName: str("\u00a0"), SourceName: "\u00a0"})
	assert.False(t, ok)
}

// The line of a book whose only credit is wordless is its legacy line, as
// for a book without credits; a wordless legacy author is left out of it.
func TestAWordlessCreditLeavesTheBookOnItsLegacyLine(t *testing.T) {
	s := sourcesFromRows([]authorRow{
		{BookID: 1, LegacyID: id64(10), SourceName: "Петров Иван"},
		{BookID: 1, LegacyID: id64(11), SourceName: "."},
		{BookID: 1, CreditID: 20, SourceName: "\u00a0"},
	})[1]
	assert.Equal(t, legacyLine(models.AuthorDisplayNoCredits, linked("Петров Иван", 10)), resolveAuthorDisplay(s))

	s = sourcesFromRows([]authorRow{
		{BookID: 2, CreditID: 21, SourceName: "\u00a0"},
		{BookID: 2, CreditID: 22, SourceName: "Анна Сидорова"},
	})[2]
	assert.Equal(t, layerLine(unlinked("Анна Сидорова")), resolveAuthorDisplay(s), "the next name of the file")

	s = sourcesFromRows([]authorRow{
		{BookID: 3, LegacyID: id64(12), SourceName: "\ufffd\ufffd"},
		{BookID: 3, CreditID: 23, SourceName: "-"},
	})[3]
	assert.Equal(t, []authorDisplayRow{{BookID: 3, Source: "none"}}, modelRowsOf(3, s, true), "a line that names no one")
}

// No model row can carry an empty sort key any more: every name left has a
// word, and so has whatever it sorts by.
func TestEveryNameSortsByAKeyWithAWord(t *testing.T) {
	rows := authorDisplayRows(4, sourcesFromRows([]authorRow{
		{BookID: 4, CreditID: 30, ChosenName: str("Иван Петров"), ChosenSortName: "\u00a0", SourceName: "x",
			LastName: "\u00a0"},
		{BookID: 4, CreditID: 31, SourceName: "Анна", LastName: "-"},
	})[4])
	require.Len(t, rows, 2)
	assert.Equal(t, "иван петров", rows[0].SortKey)
	assert.Equal(t, "анна", rows[1].SortKey)
}
