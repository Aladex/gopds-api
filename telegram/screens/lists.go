package screens

import (
	"fmt"
	"strconv"
	"strings"

	"gopds-api/models"
	"gopds-api/telegram/cbv2"
	"gopds-api/telegram/ru"
)

// The words of S5's screens (plan-ui.md §1.1, §1.4–1.7).
const (
	CopyHomeChoose      = "Напиши название книги или имя автора — или выбери:"
	CopyPromptDone      = "Готово."
	CopyPromptCancelled = "Вопрос закрыт. Новый текст будет обычным поиском."
	CopyPromptClosed    = "Этот вопрос уже закрыт. Открой фильтр ещё раз."
	CopyPromptAgain     = "Вопрос задан заново — ответь на сообщение ниже."
	CopyOldList         = "Этот список создан до обновления. Повтори поиск."
	CopyNoCollections   = "Подборок пока нет."
	CopyCollectionGone  = "Эта подборка больше недоступна."
	// CopyAuthorGone is an author a button or a list names that the library
	// no longer knows by that name: an old button after authors were moved
	// to their new pages.
	CopyAuthorGone = "Автор не найден."
	// CopyNoCollectionBooks is a collection none of whose books is in the
	// library (or in the chosen language).
	CopyNoCollectionBooks = "В этой подборке пока нет книг."

	ButtonFavorites     = "★ Избранное"
	ButtonCollections   = "Подборки"
	ButtonSettings      = "Настройки"
	ButtonAuthor        = "Автор"
	ButtonLanguage      = "Язык"
	ButtonClearAuthor   = "✕ Автор"
	ButtonAllLanguages  = "Искать на всех языках"
	ButtonOtherLanguage = "Другой язык"
	ButtonAnyLanguage   = "Все языки"
	ButtonCancel        = "Отмена"
	ButtonAnotherName   = "Ввести другое имя"
	ButtonSearchAuthor  = "Искать у автора"
	ButtonSearchFavs    = "Найти в избранном"
	ButtonAllFavorites  = "Всё избранное"
	ButtonSource        = "Источник подборки"
	ButtonToCollections = "← К подборкам"
	ButtonBack          = "← Назад"
	ButtonToAuthor      = "← К автору"
	ButtonToSeries      = "← К серии"
	ButtonToCollection  = "← К подборке"
	ButtonToFavorites   = "← К избранному"
	ButtonToAuthors     = "← К авторам"
	ButtonAuthorBooks   = "Книги автора"
	ButtonBookAuthors   = "Авторы книги"
	ButtonSeries        = "Серия"
)

// ButtonInlineSearch opens inline search from the home panel.
const ButtonInlineSearch = "🔍 Найти с подсказками"

// HomeKeys are the home panel's envelopes.
type HomeKeys struct {
	Favorites, Recent, Collections, Settings *cbv2.Envelope
}

// HomeScreen is the pinned home panel with its sections; linked greets a
// chat just bound to the library. inline offers inline search first, while
// the bot answers inline queries (plan-ui.md §3.6).
func HomeScreen(linked, inline bool, k *HomeKeys) Screen {
	blocks := []Block{Line{Text: CopyHomeChoose}}
	if linked {
		blocks = []Block{Line{Text: CopyLinked}, Gap{}, Line{Text: CopyHomeChoose}}
	}
	var keys [][]Button
	if inline {
		keys = append(keys, []Button{{Text: ButtonInlineSearch, SwitchInline: true}})
	}
	return Screen{Doc: Doc{Blocks: blocks}, Keys: append(keys,
		[]Button{{Text: ButtonFavorites, Env: k.Favorites}, {Text: ButtonRecent, Env: k.Recent}},
		[]Button{{Text: ButtonCollections, Env: k.Collections}, {Text: ButtonSettings, Env: k.Settings}},
	)}
}

// BookList is one page of a list of books: an author's, a series', a
// collection's, the favorites, a search within one of them.
type BookList struct {
	// Header is the list's title lines.
	Header []Block
	Books  []models.Book
	Total  int
	Page   int
	// Number labels a book in the list; nil numbers the position.
	Number func(i int, b *models.Book) string
	// Sub is a book's second line; nil for none.
	Sub func(b *models.Book) string
	// Empty is shown when the list has no books.
	Empty []Block
}

// ListKeys are a list's envelopes and its extra rows.
type ListKeys struct {
	Page func(page int) *cbv2.Envelope
	Book func(id int64) *cbv2.Envelope
	// Extra rows come after the paging row.
	Extra [][]Button
	// Back is the way back, nil for none.
	Back *Button
	Home *cbv2.Envelope
}

// BookListScreen renders a page of a book list.
func BookListScreen(l *BookList, k *ListKeys) Screen {
	blocks := append([]Block{}, l.Header...)
	var keys [][]Button
	if l.Total == 0 {
		blocks = append(blocks, Gap{})
		blocks = append(blocks, l.Empty...)
	} else {
		blocks = append(blocks, Gap{})
		for i := range l.Books {
			b := &l.Books[i]
			n := strconv.Itoa(l.Page*PageSize + i + 1)
			if l.Number != nil {
				n = l.Number(i, b)
			}
			blocks = append(blocks, Line{Text: n + ". " + ru.Truncate(b.Title, listLineRunes)})
			if l.Sub != nil {
				if sub := l.Sub(b); sub != "" {
					blocks = append(blocks, Line{Text: "   " + ru.Truncate(sub, listLineRunes)})
				}
			}
			keys = append(keys, []Button{{Text: n + ". " + ru.Truncate(b.Title, bookButtonRunes), Env: k.Book(b.ID)}})
		}
		pages := Pages(l.Total)
		if l.Total > PageSize {
			blocks = append(blocks, Gap{}, Line{Text: fmt.Sprintf("Страница %d из %d", l.Page+1, pages)})
		}
		if row := pagingRow(l.Page, pages, k.Page); len(row) > 0 {
			keys = append(keys, row)
		}
	}
	keys = append(keys, k.Extra...)
	last := []Button{}
	if k.Back != nil {
		last = append(last, *k.Back)
	}
	last = append(last, Button{Text: ButtonHome, Env: k.Home})
	s := Screen{Doc: Doc{Blocks: blocks}, Keys: append(keys, last)}
	if l.Total > 0 {
		s.Rich = richBookList(l, k)
	}
	return s
}

// CountLine is «N книг · язык: …», the language only when one is set.
func CountLine(total int, lang string) string {
	line := ru.Books(total)
	if lang != "" {
		line += " · язык: " + ru.Language(lang)
	}
	return line
}

// SeriesNumber labels a book of a series by its number in it.
func SeriesNumber(seriesID int64) func(int, *models.Book) string {
	return func(_ int, b *models.Book) string {
		for _, s := range b.Series {
			if s != nil && s.ID == seriesID && s.SerNo > 0 {
				return strconv.FormatInt(s.SerNo, 10)
			}
		}
		return "без номера"
	}
}

// AuthorsLine is a list item's «Автор» second line.
func AuthorsLine(b *models.Book) string { return Authors(b, listAuthors) }

// SeriesSub is a list item's «Серия, кн. N» second line.
func SeriesSub(b *models.Book) string { return SeriesLine(b) }

// LanguageKeys are the language screen's envelopes.
type LanguageKeys struct {
	Set   func(code string) *cbv2.Envelope
	Other *cbv2.Envelope
	Back  Button
}

// The two languages offered first, and a language row's width.
const (
	langRussian     = "ru"
	langEnglish     = "en"
	nameEnglish     = "English"
	languagesPerRow = 2
	// nameButtonRunes is a person's name on a button: twice a title's.
	nameButtonRunes = bookButtonRunes * 2
	// allLanguages is «все языки» in a sentence.
	allLanguages = "все языки"
)

// languageButton is a language's name on a button.
func languageButton(code, current string) string {
	name := ru.Language(code)
	switch code {
	case langRussian:
		name = "Русский"
	case langEnglish:
		name = nameEnglish
	default:
		r := []rune(name)
		if len(r) > 0 {
			name = strings.ToUpper(string(r[0])) + string(r[1:])
		}
	}
	if code == current {
		return "✓ " + name
	}
	return name
}

// LanguageScreen is S-LANGUAGE (search: the search's language; settings: the
// language of new searches).
func LanguageScreen(current string, settings bool, k *LanguageKeys) Screen {
	now := allLanguages
	if current != "" {
		now = ru.Language(current)
	}
	blocks := []Block{Title{Text: "Язык книг в этом поиске"}, Gap{}, Line{Text: "Сейчас: " + now + ". Язык других поисков не изменится."}}
	if settings {
		blocks = []Block{Title{Text: "Язык поиска"}, Gap{}, Line{Text: "Сейчас: " + now + ". Уже открытые списки не изменятся."}}
	}
	all := ButtonAnyLanguage
	if current == "" {
		all = "✓ " + all
	}
	return Screen{Doc: Doc{Blocks: blocks}, Keys: [][]Button{
		{
			{Text: languageButton(langRussian, current), Env: k.Set(langRussian)},
			{Text: languageButton(langEnglish, current), Env: k.Set(langEnglish)},
		},
		{{Text: all, Env: k.Set("")}},
		{{Text: ButtonOtherLanguage, Env: k.Other}},
		{k.Back},
	}}
}

// languagesPerPage is the «Другой язык» pager's page.
const languagesPerPage = 10

// LanguagePagerScreen lists the languages that exist, a page at a time.
func LanguagePagerScreen(codes []string, page int, current string, set func(string) *cbv2.Envelope,
	pageEnv func(int) *cbv2.Envelope, back Button) Screen {
	pages := (len(codes) + languagesPerPage - 1) / languagesPerPage
	if pages == 0 {
		pages = 1
	}
	if page >= pages {
		page = pages - 1
	}
	blocks := []Block{Title{Text: "Другой язык"}}
	if pages > 1 {
		blocks = append(blocks, Gap{}, Line{Text: fmt.Sprintf("Страница %d из %d", page+1, pages)})
	}
	var keys [][]Button
	var row []Button
	end := min((page+1)*languagesPerPage, len(codes))
	for _, code := range codes[page*languagesPerPage : end] {
		row = append(row, Button{Text: languageButton(code, current), Env: set(code)})
		if len(row) == languagesPerRow {
			keys = append(keys, row)
			row = nil
		}
	}
	if len(row) > 0 {
		keys = append(keys, row)
	}
	if paging := pagingRow(page, pages, pageEnv); len(paging) > 0 {
		keys = append(keys, paging)
	}
	return Screen{Doc: Doc{Blocks: blocks}, Keys: append(keys, []Button{back})}
}

// PromptAuthorFilter asks for the author of a search.
func PromptAuthorFilter(query string, cancel *cbv2.Envelope) Screen {
	return prompt([]Block{Line{Text: "Автор для запроса «" + query + "»"}, Gap{},
		Line{Text: "Ответь на это сообщение именем автора."}}, cancel)
}

// PromptAuthorSearch asks what to find among an author's books.
func PromptAuthorSearch(name string, cancel *cbv2.Envelope) Screen {
	return prompt([]Block{Line{Text: "Что найти у автора «" + name + "»?"}, Gap{},
		Line{Text: "Ответь на это сообщение названием или его частью."}}, cancel)
}

// PromptFavoritesSearch asks what to find among the favorites.
func PromptFavoritesSearch(cancel *cbv2.Envelope) Screen {
	return prompt([]Block{Line{Text: "Что найти в избранном?"}, Gap{},
		Line{Text: "Ответь на это сообщение названием или автором."}}, cancel)
}

func prompt(blocks []Block, cancel *cbv2.Envelope) Screen {
	return Screen{Doc: Doc{Blocks: blocks}, Keys: [][]Button{{{Text: ButtonCancel, Env: cancel}}}}
}

// AuthorPickScreen lists the authors a reply matched; none, and it says so.
func AuthorPickScreen(name string, authors []models.Author, pick func(int64) *cbv2.Envelope, again, cancel *cbv2.Envelope) Screen {
	tail := [][]Button{{{Text: ButtonAnotherName, Env: again}}, {{Text: ButtonCancel, Env: cancel}}}
	if len(authors) == 0 {
		return Screen{Doc: Doc{Blocks: []Block{
			Line{Text: "Автор «" + name + "» не найден. Проверь написание или напиши только фамилию."},
		}}, Keys: tail}
	}
	var keys [][]Button
	for i := range authors {
		keys = append(keys, []Button{{Text: ru.Truncate(authors[i].FullName, nameButtonRunes), Env: pick(authors[i].ID)}})
	}
	return Screen{Doc: Doc{Blocks: []Block{Line{Text: "Выбери автора"}}}, Keys: append(keys, tail...)}
}

// Surname is the last word of a name: the filter's short label.
func Surname(name string) string {
	fields := strings.Fields(name)
	if len(fields) == 0 {
		return name
	}
	return fields[len(fields)-1]
}

// CollectionsScreen lists the published collections.
func CollectionsScreen(collections []models.BookCollection, total, page int,
	open func(int64) *cbv2.Envelope, pageEnv func(int) *cbv2.Envelope, home *cbv2.Envelope) Screen {
	if total == 0 {
		return Screen{Doc: Doc{Blocks: []Block{Line{Text: CopyNoCollections}}}, Keys: [][]Button{{{Text: ButtonHome, Env: home}}}}
	}
	blocks := []Block{Title{Text: ButtonCollections}, Gap{}}
	var keys [][]Button
	for i := range collections {
		c := &collections[i]
		blocks = append(blocks, Line{Text: strconv.Itoa(page*PageSize+i+1) + ". " + ru.Truncate(c.Name, listLineRunes)})
		keys = append(keys, []Button{{Text: ru.Truncate(c.Name, bookButtonRunes), Env: open(c.ID)}})
	}
	pages := Pages(total)
	if total > PageSize {
		blocks = append(blocks, Gap{}, Line{Text: fmt.Sprintf("Страница %d из %d", page+1, pages)})
	}
	if row := pagingRow(page, pages, pageEnv); len(row) > 0 {
		keys = append(keys, row)
	}
	return Screen{Doc: Doc{Blocks: blocks}, Keys: append(keys, []Button{{Text: ButtonHome, Env: home}}),
		Rich: richCollections(collections, total, page, open, pageEnv, home)}
}

// BookAuthorsScreen lists the authors of a book.
func BookAuthorsScreen(b *models.Book, open func(int64) *cbv2.Envelope, back *cbv2.Envelope) Screen {
	var keys [][]Button
	for i := range b.Authors {
		if name := strings.TrimSpace(b.Authors[i].FullName); name != "" {
			keys = append(keys, []Button{{Text: ru.Truncate(name, nameButtonRunes), Env: open(b.Authors[i].ID)}})
		}
	}
	return Screen{Doc: Doc{Blocks: []Block{Line{Text: "Авторы книги «" + ru.Truncate(b.Title, listLineRunes) + "»"}}},
		Keys: append(keys, []Button{{Text: ButtonToBook, Env: back}})}
}

// WithBack puts a way back before the last row's buttons; nil changes
// nothing.
func (s Screen) WithBack(back *Button) Screen {
	if back == nil || len(s.Keys) == 0 {
		return s
	}
	keys := append([][]Button{}, s.Keys...)
	last := len(keys) - 1
	keys[last] = append([]Button{*back}, keys[last]...)
	s.Keys = keys
	if s.Rich != nil {
		rich := s.Rich.WithBack(back)
		s.Rich = &rich
	}
	return s
}
