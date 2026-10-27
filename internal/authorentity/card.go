package authorentity

import (
	"crypto/sha256"
	"encoding/json"
	"slices"
	"unicode/utf8"
)

// The merge card (E4, §4.3): what the model sees of one side of a merge
// candidate. Everything on it is a function of the side's members' selected
// results and of the rows of its sample books, so a large side's card does
// not move when it gains a book outside the sample.

// MergeCardVersion is the version of the card's shape; it is part of every
// input, so a new shape lapses every decision asked with the old one.
const MergeCardVersion = "e4-card-v1"

// mergeCardSample is how many books a card shows: those with the lowest
// book id, the oldest in the catalog.
const mergeCardSample = 10

// mergeAnnotationRunes is where a card cuts a book's annotation.
const mergeAnnotationRunes = 300

// MergeCard is one side of a question.
type MergeCard struct {
	// Names are the display names of the side's members' selected results,
	// Variants their nicknames; both distinct and sorted.
	Names    []string `json:"names"`
	Variants []string `json:"variants,omitempty"`
	// Size is the side's size class in books, never a count.
	Size  string          `json:"size"`
	Books []MergeCardBook `json:"books"`
}

// MergeCardBook is one sample book of a card.
type MergeCardBook struct {
	Title string `json:"title"`
	// Samizdat is the «[СИ]» mark of the title.
	Samizdat bool `json:"samizdat,omitempty"`
	// Year is the current snapshot's source year, else the book's docdate.
	Year string `json:"year,omitempty"`
	Lang string `json:"lang,omitempty"`
	// Genres are the genre codes, never the titles (an LLM job rewrites
	// those); Series the series names; both sorted.
	Genres     []string `json:"genres,omitempty"`
	Series     []string `json:"series,omitempty"`
	Annotation string   `json:"annotation,omitempty"`
	// Coauthors are the display names of the selected results of the other
	// credits on the book, distinct and sorted: never another entity.
	Coauthors []string `json:"coauthors,omitempty"`
}

// MergeInput is the exact input of one question: two cards in the
// candidate's canonical side order.
type MergeInput struct {
	CardVersion string    `json:"card_version"`
	A           MergeCard `json:"a"`
	B           MergeCard `json:"b"`
}

// CardBookFields are the fields of a sample book the build loads after it
// has chosen the samples (the title it reads anyway).
type CardBookFields struct {
	Year       string
	Lang       string
	Genres     []string
	Series     []string
	Annotation string
}

// CardSource is a (table, columns) a card field is read from.
type CardSource struct {
	Table   string
	Columns []string
}

// The tables and columns the card reads.
const (
	srcSelection = "book_contributor_credit_selection"
	srcResult    = "contributor_normalization_result"
	srcSnapshot  = "book_metadata_snapshot"
	srcBook      = "opds_catalog_book"
	colID        = "id"
	colTitle     = "title"
	colBookID    = "book_id"
	colCurrent   = "is_current"
	colResultID  = "result_id"
	colState     = "state"
	colDisplay   = "display_name"
	sizeOver100  = "100+"
)

// MergeCardSources declare, per field of MergeCard and MergeCardBook
// ("Type.Field"), the rows it is read from. Every source must mark a build
// when it changes (§4.10): database TestMergeCardSourcesMarked checks the
// triggers, TestMergeCardFieldsDeclared that no field goes undeclared.
var MergeCardSources = map[string][]CardSource{
	"MergeCard.Names": {
		{Table: srcSelection, Columns: []string{colResultID, colState}},
		{Table: srcResult, Columns: []string{colDisplay}},
	},
	"MergeCard.Variants": {
		{Table: srcSelection, Columns: []string{colResultID, colState}},
		{Table: srcResult, Columns: []string{"nickname"}},
	},
	"MergeCard.Size": {
		{Table: srcSelection, Columns: []string{colResultID, colState}},
		{Table: srcSnapshot, Columns: []string{colCurrent}},
		{Table: srcBook, Columns: []string{colID}},
	},
	"MergeCard.Books": {
		{Table: srcSnapshot, Columns: []string{colCurrent}},
		{Table: srcBook, Columns: []string{colID}},
	},
	"MergeCardBook.Title":    {{Table: srcBook, Columns: []string{colTitle}}},
	"MergeCardBook.Samizdat": {{Table: srcBook, Columns: []string{colTitle}}},
	"MergeCardBook.Year": {
		{Table: srcSnapshot, Columns: []string{colCurrent, "source_year"}},
		{Table: srcBook, Columns: []string{"docdate"}},
	},
	"MergeCardBook.Lang": {{Table: srcBook, Columns: []string{"lang"}}},
	"MergeCardBook.Genres": {
		{Table: "opds_catalog_bgenre", Columns: []string{colBookID, "genre_id"}},
		{Table: "opds_catalog_genre", Columns: []string{"genre"}},
	},
	"MergeCardBook.Series": {
		{Table: "opds_catalog_bseries", Columns: []string{colBookID, "ser_id"}},
		{Table: "opds_catalog_series", Columns: []string{"ser"}},
	},
	"MergeCardBook.Annotation": {{Table: srcBook, Columns: []string{"annotation"}}},
	"MergeCardBook.Coauthors": {
		{Table: srcSelection, Columns: []string{colResultID, colState}},
		{Table: srcResult, Columns: []string{colDisplay}},
	},
}

// The size classes of a side (§4.3).
var mergeSizeClasses = []struct {
	label string
	max   int
}{{"1", 1}, {"2-3", 3}, {"4-10", 10}, {"11-30", 30}, {"31-100", 100}, {sizeOver100, 1 << 31}}

// MergeSizeClass is the size class of a side of n books.
func MergeSizeClass(n int) string {
	for _, c := range mergeSizeClasses {
		if n <= c.max {
			return c.label
		}
	}
	return mergeSizeClasses[len(mergeSizeClasses)-1].label
}

// MergeCardBookOf renders one sample book.
func MergeCardBookOf(title string, f *CardBookFields, coauthors []string) MergeCardBook {
	return MergeCardBook{Title: title, Samizdat: isSamizdat(title), Year: f.Year, Lang: f.Lang,
		Genres: sortedDistinct(f.Genres), Series: sortedDistinct(f.Series),
		Annotation: cutRunes(f.Annotation, mergeAnnotationRunes), Coauthors: sortedDistinct(coauthors)}
}

// MergeCardOf renders a card: the names and variants, the side's size in
// books, its sample books in book id order.
func MergeCardOf(names, variants []string, books int, sample []MergeCardBook) MergeCard {
	if sample == nil {
		sample = []MergeCardBook{}
	}
	return MergeCard{Names: sortedDistinct(names), Variants: sortedDistinct(variants), Size: MergeSizeClass(books),
		Books: sample}
}

// MergeInputJSON is the canonical bytes of a question: the one rendering
// the build hashes and the enqueue sends.
func MergeInputJSON(a, b *MergeCard) []byte {
	raw, err := json.Marshal(MergeInput{CardVersion: MergeCardVersion, A: *a, B: *b})
	if err != nil {
		// Strings and slices of strings always marshal.
		panic(err)
	}
	return raw
}

// MergeInputSHA256 is the input hash of a question.
func MergeInputSHA256(a, b *MergeCard) [32]byte { return sha256.Sum256(MergeInputJSON(a, b)) }

// sortedDistinct is the non-empty values, distinct and sorted; nil when none.
func sortedDistinct(values []string) []string {
	var out []string
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// cutRunes cuts s to at most n runes.
func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
