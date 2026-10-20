package screens

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"gopds-api/models"
	"gopds-api/telegram/cbv2"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files of the rich lists")

// pastYear is a year the fixtures' books came out in, and how the golden
// files spell it: the files stay the same from one year to the next.
var pastYear = strconv.Itoa(time.Now().AddDate(-40, 0, 0).Year())

const goldenYear = "YEAR"

// richBook is a list's book: its id, title, authors and series.
func richBook(id int64, title string, authors []string, series string, no int64) models.Book {
	b := models.Book{ID: id, Title: title, Lang: "ru", DocDate: pastYear + "-01-01"}
	for i, a := range authors {
		b.Authors = append(b.Authors, models.Author{ID: id*10 + int64(i), FullName: a})
	}
	if series != "" {
		b.Series = []*models.Series{{ID: 7, Ser: series, SerNo: no}}
	}
	return b
}

func richBooks() []models.Book {
	long := strings.Repeat("Очень длинное название, которое не помещается в одну строку ", 5)
	return []models.Book{
		richBook(11, "Пикник на обочине", []string{"Аркадий Стругацкий", "Борис Стругацкий"}, "", 0),
		richBook(12, long, []string{"Станислав Лем"}, "Звёздные дневники", 2),
		richBook(13, "Основание", []string{"Айзек Азимов"}, "Основание", 1),
		richBook(14, "Без автора", nil, "", 0),
		richBook(15, "Трудно быть богом", []string{"А", "Б", "В"}, "Мир Полудня", 0),
	}
}

func resultsKeys() *ResultsKeys {
	at := func(op cbv2.Op, arg uint8, page int) *cbv2.Envelope {
		return &cbv2.Envelope{Op: op, Arg: arg, Page: uint32(page)} // #nosec G115 -- small test pages
	}
	return &ResultsKeys{
		Tab:          func(tab uint8) *cbv2.Envelope { return at(cbv2.OpTab, tab, 0) },
		RetryTab:     func(tab uint8) *cbv2.Envelope { return at(cbv2.OpRetryBranch, tab, 0) },
		RetryAll:     at(cbv2.OpRetrySearch, 0, 0),
		Page:         func(page int) *cbv2.Envelope { return at(cbv2.OpPage, 0, page) },
		Book:         func(id int64) *cbv2.Envelope { return &cbv2.Envelope{Op: cbv2.OpOpenBook, EntityID: uint64(id)} },
		OpenAuthor:   func(id int64) *cbv2.Envelope { return &cbv2.Envelope{Op: cbv2.OpOpenAuthor, EntityID: uint64(id)} },
		Author:       at(cbv2.OpPromptAuthor, 0, 0),
		ClearAuthor:  at(cbv2.OpClearAuthor, 0, 0),
		Language:     at(cbv2.OpLanguagePicker, 0, 0),
		AllLanguages: at(cbv2.OpSetLanguage, 0, 0),
		Back:         &Button{Text: ButtonBack, Env: at(cbv2.OpBack, 0, 0)},
		Home:         at(cbv2.OpHome, 0, 0),
	}
}

func listKeys() *ListKeys {
	return &ListKeys{
		Page:  func(p int) *cbv2.Envelope { return &cbv2.Envelope{Op: cbv2.OpPage, Page: uint32(p)} }, // #nosec G115 -- small test pages
		Book:  func(id int64) *cbv2.Envelope { return &cbv2.Envelope{Op: cbv2.OpOpenBook, EntityID: uint64(id)} },
		Extra: [][]Button{{{Text: ButtonSearchAuthor, Env: &cbv2.Envelope{Op: cbv2.OpPromptAuthorSearch}}}},
		Back:  &Button{Text: ButtonToBook, Env: &cbv2.Envelope{Op: cbv2.OpBack}},
		Home:  &cbv2.Envelope{Op: cbv2.OpHome},
	}
}

// golden compares a rich rendering — the message and its keyboard — with
// testdata/<name>.json; -update writes it.
func golden(t *testing.T, name string, s *Screen) {
	t.Helper()
	require.NotNil(t, s.Rich, "a list has a rich layout")
	msg, err := s.Rich.Doc.Rich(testEncoder)
	require.NoError(t, err)
	var keys [][]string
	for _, row := range s.Rich.Keys {
		var labels []string
		for _, b := range row {
			labels = append(labels, b.Text)
		}
		keys = append(keys, labels)
	}
	got, err := json.MarshalIndent(map[string]any{"rich_message": msg, "keyboard": keys}, "", "  ")
	require.NoError(t, err)
	out := strings.ReplaceAll(string(got), pastYear, goldenYear) + "\n"
	path := filepath.Join("testdata", name+".json")
	if *updateGolden {
		require.NoError(t, os.MkdirAll("testdata", 0o750))
		require.NoError(t, os.WriteFile(path, []byte(out), 0o600))
	}
	want, err := os.ReadFile(path) // #nosec G304 -- the test's own golden file
	require.NoError(t, err)
	assert.Equal(t, string(want), out)
}

func TestTheRichResultsBooksTab(t *testing.T) {
	r := &Results{Query: "пикник", Lang: "ru", AuthorName: "Аркадий Стругацкий", Page: 1,
		Books: richBooks(), BookTotal: 37, AuthorTotal: 2}
	golden(t, "results-books", new(ResultsScreen(r, resultsKeys())))
}

func TestTheRichResultsAuthorsTab(t *testing.T) {
	r := &Results{Query: "стругацкий", Tab: 1, BookTotal: 120, AuthorTotal: 3, Authors: []models.Author{
		{ID: 1, FullName: "Аркадий Стругацкий", BooksCount: 41},
		{ID: 2, FullName: "Борис Стругацкий", BooksCount: 1},
		{ID: 3, FullName: "С. Ярославцев"},
	}}
	golden(t, "results-authors", new(ResultsScreen(r, resultsKeys())))
}

func TestTheRichResultsWithAFailedTab(t *testing.T) {
	r := &Results{Query: "пикник", BooksFailed: true, AuthorTotal: 1,
		Authors: []models.Author{{ID: 1, FullName: "Аркадий Стругацкий", BooksCount: 41}}}
	golden(t, "results-books-failed", new(ResultsScreen(r, resultsKeys())))
}

func TestTheRichAuthorList(t *testing.T) {
	l := &BookList{Header: []Block{Title{Text: "Аркадий Стругацкий"}, Footer{Text: CountLine(12, "ru")}},
		Books: richBooks(), Total: 12, Sub: SeriesSub}
	golden(t, "author", new(BookListScreen(l, listKeys())))
}

func TestTheRichSeriesList(t *testing.T) {
	books := richBooks()[1:4]
	books[2].Series = []*models.Series{{ID: 7, Ser: "Звёздные дневники"}}
	books[0].Series[0].SerNo, books[1].Series = 1, []*models.Series{{ID: 7, Ser: "Звёздные дневники", SerNo: 2}}
	l := &BookList{Header: []Block{Title{Text: "Серия «Звёздные дневники»"}, Footer{Text: CountLine(3, "")}},
		Books: books, Total: 3, Number: SeriesNumber(7), Sub: AuthorsLine}
	golden(t, "series", new(BookListScreen(l, listKeys())))
}

func TestTheRichFavorites(t *testing.T) {
	k := listKeys()
	k.Back, k.Extra = nil, [][]Button{{{Text: ButtonSearchFavs, Env: &cbv2.Envelope{Op: cbv2.OpPromptFavSearch}}}}
	l := &BookList{Header: []Block{Title{Text: "Избранное"}, Footer{Text: "3 книги"}},
		Books: richBooks()[:3], Total: 3, Sub: AuthorsLine}
	golden(t, "favorites", new(BookListScreen(l, k)))
}

func TestTheRichCollections(t *testing.T) {
	collections := []models.BookCollection{{ID: 5, Name: "Лучшая фантастика века"}, {ID: 6, Name: "Летнее чтение"}}
	s := CollectionsScreen(collections, 7, 1,
		func(id int64) *cbv2.Envelope { return &cbv2.Envelope{Op: cbv2.OpOpenCollection, EntityID: uint64(id)} },
		func(p int) *cbv2.Envelope { return &cbv2.Envelope{Op: cbv2.OpCollections, Page: uint32(p)} }, // #nosec G115 -- small test pages
		&cbv2.Envelope{Op: cbv2.OpHome})
	golden(t, "collections", &s)
}

func TestTheRichRecent(t *testing.T) {
	books := richBooks()
	sent := time.Now().Add(-26 * time.Hour)
	items := []RecentItem{
		{Book: &books[0], BookID: 11, Format: "epub", SentAt: sent},
		{BookID: 99, Format: "fb2", SentAt: sent},
		{Book: &books[2], BookID: 13, Format: "mobi", SentAt: sent},
	}
	s := RecentScreen(items, 3, 0, true, &RecentKeys{
		Page: func(p int) *cbv2.Envelope { return &cbv2.Envelope{Op: cbv2.OpRecent, Page: uint32(p)} }, // #nosec G115 -- small test pages
		Resend: func(id int64, format string) *cbv2.Envelope {
			return &cbv2.Envelope{Op: cbv2.OpResend, EntityID: uint64(id), Arg: FormatArg(format)}
		},
		Open: func(id int64) *cbv2.Envelope { return &cbv2.Envelope{Op: cbv2.OpOpenBook, EntityID: uint64(id)} },
		Home: &cbv2.Envelope{Op: cbv2.OpHome},
	})
	msg, err := s.Rich.Doc.Rich(testEncoder)
	require.NoError(t, err)
	raw, err := json.Marshal(msg)
	require.NoError(t, err)
	// The time of a send is the clock's; the golden file has it as SENT.
	out := strings.ReplaceAll(string(raw), sent.UTC().Format(recentTimeLayout), "SENT")
	assert.Contains(t, out, `"text":"Отправить EPUB","style":"link","callback_data":"`+mustEncode(t, cbv2.OpResend, 11, 0, 1)+`"`)
	assert.Contains(t, out, `"text":"Пикник на обочине","style":"link","callback_data":"`+mustEncode(t, cbv2.OpOpenBook, 11, 0, 0)+`"`)
	assert.Contains(t, out, `Аркадий Стругацкий, Борис Стругацкий · EPUB · SENT`)
	assert.Contains(t, out, `{"blocks":[{"type":"paragraph","text":"Книга больше недоступна"}],"value":2,"type":"1"}`,
		"a book gone from the catalog is text: nothing to open or send")
	assert.Equal(t, [][]Button{{{Text: ButtonHome, Env: &cbv2.Envelope{Op: cbv2.OpHome}}}}, s.Rich.Keys)
}

func mustEncode(t *testing.T, op cbv2.Op, entity uint64, page uint32, arg uint8) string {
	t.Helper()
	data, err := testEncoder(&cbv2.Envelope{Op: op, EntityID: entity, Page: page, Arg: arg})
	require.NoError(t, err)
	return data
}

// (p) The plain rendering of a list is the one of before: a header's footer
// and heading are lines, and no button of the message gets into the text.
func TestAListsPlainRenderingIsUnchanged(t *testing.T) {
	l := &BookList{Header: []Block{Heading{Text: "Поиск в избранном: «<x>»"}, Footer{Text: "3 книги"}},
		Books: richBooks()[:1], Total: 1, Sub: AuthorsLine}
	s := BookListScreen(l, listKeys())
	assert.Equal(t, "Поиск в избранном: «&lt;x&gt;»\n3 книги\n\n1. Пикник на обочине\n   Аркадий Стругацкий, Борис Стругацкий",
		s.Doc.Plain())
	assert.Equal(t, "1. Пикник на обочине", s.Keys[0][0].Text, "the plain keyboard keeps a row per book")
	assert.NotContains(t, s.Rich.Doc.Plain(), ButtonNext)
}

// (p) A rich list keeps no row per book in its keyboard: the titles are the
// buttons.
func TestARichListsKeyboardHasNoBookRows(t *testing.T) {
	r := &Results{Query: "q", Books: richBooks(), BookTotal: 5}
	s := ResultsScreen(&Results{Query: r.Query, Books: r.Books, BookTotal: r.BookTotal}, resultsKeys())
	for _, row := range s.Rich.Keys {
		for _, b := range row {
			assert.NotEqual(t, cbv2.OpOpenBook, b.Env.Op, "keyboard button %q opens a book", b.Text)
		}
	}
	assert.Equal(t, [][]string{{"Автор", "Язык"}, {ButtonBack, ButtonHome}}, labelsOf(s.Rich.Keys))
}

func labelsOf(rows [][]Button) [][]string {
	var out [][]string
	for _, row := range rows {
		var labels []string
		for _, b := range row {
			labels = append(labels, b.Text)
		}
		out = append(out, labels)
	}
	return out
}

// (p) The pager keeps its ends in place: disabled where there is no page.
func TestThePagersEndsAreDisabledAtTheEnds(t *testing.T) {
	env := func(p int) *cbv2.Envelope { return &cbv2.Envelope{Op: cbv2.OpPage, Page: uint32(p)} } // #nosec G115 -- small test pages
	assert.Nil(t, pagerBlock(0, 1, env), "one page has no pager")
	first := pagerBlock(0, 3, env)[0].(Buttons).Row
	assert.True(t, first[0].Disabled)
	assert.Nil(t, first[0].Env)
	assert.Equal(t, uint32(1), first[2].Env.Page)
	assert.Equal(t, "Страница 1 из 3", first[1].Text)
	assert.True(t, first[1].Disabled)
	last := pagerBlock(2, 3, env)[0].(Buttons).Row
	assert.Equal(t, uint32(1), last[0].Env.Page)
	assert.True(t, last[2].Disabled)
	assert.Nil(t, last[2].Env)
}

// (p) A button that cannot be encoded fails the rendering, not a press.
func TestARichListThatCannotBeEncodedIsAnError(t *testing.T) {
	s := ResultsScreen(&Results{Query: "q", Books: richBooks(), BookTotal: 5}, resultsKeys())
	failure := errors.New("no key")
	_, err := s.Rich.Doc.Rich(func(*cbv2.Envelope) (string, error) { return "", failure })
	assert.ErrorIs(t, err, failure)
}

// (p) The longest page of the longest titles stays inside a rich message's
// limits.
func TestTheWorstRichListFitsTheLimits(t *testing.T) {
	var books []models.Book
	for i := 0; i < PageSize; i++ {
		books = append(books, *worstBook())
	}
	s := ResultsScreen(&Results{Query: strings.Repeat("я", MaxQuery), Lang: "ru", AuthorName: strings.Repeat("Имя ", 50),
		Books: books, BookTotal: 999999, AuthorTotal: 999999, Page: 3}, resultsKeys())
	msg, err := s.Rich.Doc.Rich(testEncoder)
	require.NoError(t, err)
	raw, err := json.Marshal(msg)
	require.NoError(t, err)
	assert.Less(t, utf8.RuneCount(raw), 32768)
	assert.Less(t, strings.Count(string(raw), `"type"`), 500, "blocks, items and runs of text together")
	for _, b := range s.Rich.Doc.Blocks {
		if row, ok := b.(Buttons); ok {
			assert.LessOrEqual(t, len(row.Row), 8, "a row in the message holds 1-8 buttons")
		}
	}
}
