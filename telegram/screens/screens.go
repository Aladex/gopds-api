package screens

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"gopds-api/models"
	"gopds-api/telegram/cbv2"
	"gopds-api/telegram/ru"
)

// The bot's words outside any one screen (plan-ui.md §1.11).
const (
	CopyHome           = "Напиши название книги или имя автора."
	CopyLinked         = "Бот подключён."
	CopyNoCommands     = "Такой команды нет. Напиши название книги или имя автора."
	CopyNotText        = "Для поиска нужно название книги или имя автора текстом."
	CopyEmptyQuery     = "Напиши название книги или имя автора."
	CopyTooLong        = "Запрос слишком длинный. Оставь до 200 символов — например, название книги или фамилию автора."
	CopyUnavailable    = "Эта книга больше недоступна."
	CopyExpired        = "Эта выдача больше не сохранена. Можно начать новый поиск."
	CopyScreenChanged  = "Этот экран уже изменился."
	CopyUnsupported    = "Эта кнопка больше не поддерживается."
	CopyButtonDenied   = "Эта кнопка недоступна."
	CopyLibraryDown    = "Библиотека временно недоступна. Попробуй ещё раз позже."
	CopySearchFailed   = "Не удалось выполнить поиск. Запрос сохранён."
	CopyFavAdded       = "Добавлено в избранное"
	CopyFavRemoved     = "Убрано из избранного"
	CopyFavFailed      = "Не удалось изменить избранное. Попробуй ещё раз."
	CopyListChanged    = "Список изменился. Открыта последняя страница."
	CopyOpenCard       = "Открыть карточку"
	CopyAnnotationFull = "Аннотация полностью"

	ButtonHome      = "Домой"
	ButtonPrev      = "← Назад"
	ButtonNext      = "Вперёд →"
	ButtonOnSite    = "На сайте"
	ButtonRetry     = "Повторить"
	ButtonRetryAll  = "Повторить поиск"
	ButtonToResults = "← К результатам"
	ButtonToBook    = "← К книге"

	ButtonFavorite    = "★ В избранном"
	ButtonNotFavorite = "☆ В избранное"
)

// The words of a results tab that has no list to show.
const (
	copyBooksDown   = "Книги временно недоступны."
	copyOnlyAuthors = "Книг по этому запросу нет. Найдены авторы — открой вкладку выше."
	copyAuthorsDown = "Авторы временно недоступны."
	copyNoAuthors   = "Авторов по этому запросу нет."
)

// MaxQuery is the longest search a reader may type, in characters.
const MaxQuery = 200

// PageSize is how many items a list page shows.
const PageSize = 5

// bookButtonRunes is how much of a title a list button shows.
const bookButtonRunes = 28

// listLineRunes bounds one line of a list: five items of two lines and the
// header stay inside a message's 4096 characters whatever the catalog holds.
const listLineRunes = 200

// listAuthors is how many authors a list item names.
const listAuthors = 2

// Formats are the formats a book is offered in, in the card's order after
// the default one.
var Formats = []string{FormatEPUB, formatFB2, formatMOBI, formatZIP}

// FormatEPUB is the default format.
const FormatEPUB = "epub"

const (
	formatFB2  = "fb2"
	formatMOBI = "mobi"
	formatZIP  = "zip"
)

// FormatArg numbers a format in a button, 1-based: 1 epub, 2 fb2, 3 mobi, 4 zip.
func FormatArg(format string) uint8 {
	for i, f := range Formats {
		if f == format {
			return uint8(i + 1) // #nosec G115 -- four formats
		}
	}
	return 0
}

// FormatOf is FormatArg reversed; "" for an unknown argument.
func FormatOf(arg uint8) string {
	if arg == 0 || int(arg) > len(Formats) {
		return ""
	}
	return Formats[arg-1]
}

// Pages is how many pages total items fill.
func Pages(total int) int {
	if total <= 0 {
		return 1
	}
	return (total + PageSize - 1) / PageSize
}

// Home is the pinned home panel; linked greets a chat just bound to the
// library. Until the sections it leads to exist (S5) it carries no buttons.
func Home(linked bool) Screen {
	blocks := []Block{Line{Text: CopyHome}}
	if linked {
		blocks = []Block{Line{Text: CopyLinked}, Gap{}, Line{Text: CopyHome}}
	}
	return Screen{Doc: Doc{Blocks: blocks}}
}

// Notice is a one-line screen with a way home.
func Notice(text string, home *cbv2.Envelope) Screen {
	s := Screen{Doc: Doc{Blocks: []Block{Line{Text: text}}}}
	if home != nil {
		s.Keys = [][]Button{{{Text: ButtonHome, Env: home}}}
	}
	return s
}

// Results is what a search page shows.
type Results struct {
	Query string
	Lang  string
	// Tab is 0 for books, 1 for authors.
	Tab         uint8
	Page        int
	Books       []models.Book
	BookTotal   int
	Authors     []models.Author
	AuthorTotal int
	// BooksFailed and AuthorsFailed say a branch did not answer.
	BooksFailed, AuthorsFailed bool
	// AuthorName is the author the search is filtered by, "" for none.
	AuthorName string
	// Suggestions are offered on an empty page.
	Suggestions []models.AutocompleteSuggestion
}

// ResultsKeys are the envelopes the results buttons carry; the router fills
// them in, since they hold ids the screen does not know.
type ResultsKeys struct {
	Tab      func(tab uint8) *cbv2.Envelope
	RetryTab func(tab uint8) *cbv2.Envelope
	RetryAll *cbv2.Envelope
	Page     func(page int) *cbv2.Envelope
	Book     func(id int64) *cbv2.Envelope
	Home     *cbv2.Envelope
	// The filters (S5): ask for an author, drop it, pick a language, search
	// all languages; nil leaves a button out.
	Author, ClearAuthor, Language, AllLanguages *cbv2.Envelope
	// OpenAuthor opens an author of the authors tab; Suggest a suggestion.
	OpenAuthor func(id int64) *cbv2.Envelope
	Suggest    func(s *models.AutocompleteSuggestion) *cbv2.Envelope
	// Back returns to the list this one came from, nil for none.
	Back *Button
}

// ResultsScreen renders a search page (plan-ui.md §1.2–1.3).
func ResultsScreen(r *Results, k *ResultsKeys) Screen {
	if r.BooksFailed && r.AuthorsFailed {
		return Screen{
			Doc:  Doc{Blocks: []Block{Line{Text: CopySearchFailed}}},
			Keys: [][]Button{{{Text: ButtonRetryAll, Env: k.RetryAll}}, {{Text: ButtonHome, Env: k.Home}}},
		}
	}
	if !r.BooksFailed && !r.AuthorsFailed && r.BookTotal == 0 && r.AuthorTotal == 0 {
		return emptyResults(r, k)
	}
	blocks := []Block{Line{Text: "Поиск: «" + r.Query + "»"}}
	keys := [][]Button{tabsRow(r, k)}
	var list []Block
	var buttons [][]Button
	total := r.BookTotal
	if r.Tab == 0 {
		if line := filterLine(r); line != "" {
			blocks = append(blocks, Line{Text: line})
		}
		list, buttons = bookList(r, k)
	} else {
		total = r.AuthorTotal
		list, buttons = authorList(r, k)
	}
	blocks = append(blocks, list...)
	keys = append(keys, buttons...)
	pages := Pages(total)
	if total > PageSize {
		blocks = append(blocks, Gap{}, Line{Text: fmt.Sprintf("Страница %d из %d", r.Page+1, pages)})
	}
	if row := pagingRow(r.Page, pages, k.Page); len(row) > 0 {
		keys = append(keys, row)
	}
	if row := filterRow(r, k); len(row) > 0 {
		keys = append(keys, row)
	}
	return Screen{Doc: Doc{Blocks: blocks}, Keys: append(keys, lastRow(k.Back, k.Home)), Rich: richResults(r, k)}
}

// filterLine names the filters in force: «Язык: русский · Автор: Азимов».
func filterLine(r *Results) string {
	var parts []string
	if r.Lang != "" {
		parts = append(parts, "Язык: "+ru.Language(r.Lang))
	}
	if r.AuthorName != "" {
		parts = append(parts, "Автор: "+r.AuthorName)
	}
	return strings.Join(parts, " · ")
}

// filterRow is the author and language buttons, with their current values.
func filterRow(r *Results, k *ResultsKeys) []Button {
	var row []Button
	if k.Author != nil && r.Tab == 0 {
		if r.AuthorName == "" {
			row = append(row, Button{Text: ButtonAuthor, Env: k.Author})
		} else {
			row = append(row, Button{Text: ButtonAuthor + ": " + ru.Truncate(Surname(r.AuthorName), bookButtonRunes), Env: k.Author})
			if k.ClearAuthor != nil {
				row = append(row, Button{Text: ButtonClearAuthor, Env: k.ClearAuthor})
			}
		}
	}
	if k.Language != nil {
		label := ButtonLanguage
		if r.Lang != "" {
			label += ": " + ru.Language(r.Lang)
		}
		row = append(row, Button{Text: label, Env: k.Language})
	}
	return row
}

// lastRow is the way back when there is one, and «Домой».
func lastRow(back *Button, home *cbv2.Envelope) []Button {
	row := []Button{}
	if back != nil {
		row = append(row, *back)
	}
	return append(row, Button{Text: ButtonHome, Env: home})
}

// emptyResults is S-EMPTY: what is in force, the resets of what is, and up
// to three suggestions.
func emptyResults(r *Results, k *ResultsKeys) Screen {
	blocks := []Block{Line{Text: "По запросу «" + r.Query + "» ничего не найдено"}}
	if line := filterLine(r); line != "" {
		blocks = append(blocks, Line{Text: line})
	}
	blocks = append(blocks, Gap{}, Line{Text: "Попробуй часть названия или только фамилию автора."})
	var keys [][]Button
	if k.Suggest != nil {
		for i := range r.Suggestions {
			s := &r.Suggestions[i]
			label := "Книга: "
			if s.Type == "author" {
				label = "Автор: "
			}
			keys = append(keys, []Button{{Text: label + ru.Truncate(s.Value, bookButtonRunes), Env: k.Suggest(s)}})
		}
	}
	if r.Lang != "" && k.AllLanguages != nil {
		keys = append(keys, []Button{{Text: ButtonAllLanguages, Env: k.AllLanguages}})
	}
	if r.AuthorName != "" && k.ClearAuthor != nil {
		keys = append(keys, []Button{{Text: ButtonClearAuthor, Env: k.ClearAuthor}})
	}
	return Screen{Doc: Doc{Blocks: blocks}, Keys: append(keys, lastRow(k.Back, k.Home))}
}

// bookList is the books tab's list and its buttons.
func bookList(r *Results, k *ResultsKeys) ([]Block, [][]Button) {
	blocks := []Block{Gap{}}
	switch {
	case r.BooksFailed:
		return append(blocks, Line{Text: copyBooksDown}), nil
	case r.BookTotal == 0:
		return append(blocks, Line{Text: copyOnlyAuthors}), nil
	}
	var buttons [][]Button
	for i := range r.Books {
		b := &r.Books[i]
		n := r.Page*PageSize + i + 1
		blocks = append(blocks, Line{Text: strconv.Itoa(n) + ". " + ru.Truncate(b.Title, listLineRunes)})
		if second := bookSubline(b); second != "" {
			blocks = append(blocks, Line{Text: "   " + ru.Truncate(second, listLineRunes)})
		}
		buttons = append(buttons, []Button{{Text: strconv.Itoa(n) + ". " + ru.Truncate(b.Title, bookButtonRunes), Env: k.Book(b.ID)}})
	}
	return blocks, buttons
}

// authorList is the authors tab's list and its buttons.
func authorList(r *Results, k *ResultsKeys) ([]Block, [][]Button) {
	lang := "все"
	if r.Lang != "" {
		lang = ru.Language(r.Lang)
	}
	blocks := []Block{Line{Text: "Авторы по запросу. Язык книг: " + lang + "."}, Gap{}}
	switch {
	case r.AuthorsFailed:
		return append(blocks, Line{Text: copyAuthorsDown}), nil
	case r.AuthorTotal == 0:
		return append(blocks, Line{Text: copyNoAuthors}), nil
	}
	var buttons [][]Button
	for i := range r.Authors {
		a := &r.Authors[i]
		line := strconv.Itoa(r.Page*PageSize+i+1) + ". " + ru.Truncate(a.FullName, listLineRunes)
		if a.BooksCount > 0 {
			line += " — " + ru.Books(a.BooksCount)
		}
		blocks = append(blocks, Line{Text: line})
		if k.OpenAuthor != nil {
			buttons = append(buttons, []Button{{Text: ru.Truncate(a.FullName, nameButtonRunes), Env: k.OpenAuthor(a.ID)}})
		}
	}
	return blocks, buttons
}

func tabsRow(r *Results, k *ResultsKeys) []Button {
	books := Button{Text: "Книги · " + strconv.Itoa(r.BookTotal), Env: k.Tab(0)}
	authors := Button{Text: "Авторы · " + strconv.Itoa(r.AuthorTotal), Env: k.Tab(1)}
	if r.BooksFailed {
		books = Button{Text: "Книги: повторить", Env: k.RetryTab(0)}
	}
	if r.AuthorsFailed {
		authors = Button{Text: "Авторы: повторить", Env: k.RetryTab(1)}
	}
	if r.Tab == 0 && !r.BooksFailed {
		books.Text = "✓ " + books.Text
	}
	if r.Tab == 1 && !r.AuthorsFailed {
		authors.Text = "✓ " + authors.Text
	}
	return []Button{books, authors}
}

func pagingRow(page, pages int, env func(int) *cbv2.Envelope) []Button {
	var row []Button
	if page > 0 {
		row = append(row, Button{Text: ButtonPrev, Env: env(page - 1)})
	}
	if page+1 < pages {
		row = append(row, Button{Text: ButtonNext, Env: env(page + 1)})
	}
	return row
}

// bookSubline is «Автор · Серия, кн. N».
func bookSubline(b *models.Book) string {
	var parts []string
	if a := Authors(b, listAuthors); a != "" {
		parts = append(parts, a)
	}
	if s := SeriesLine(b); s != "" {
		parts = append(parts, s)
	}
	return strings.Join(parts, " · ")
}

// Authors names a book's authors, at most limit of them, then «и др.».
func Authors(b *models.Book, limit int) string {
	names := authorNames(b)
	if len(names) > limit {
		return strings.Join(names[:limit], ", ") + " и др."
	}
	return strings.Join(names, ", ")
}

// authorNames are the names a book is credited to: its author line when the
// router read one, its catalog authors otherwise.
func authorNames(b *models.Book) []string {
	var names []string
	if len(b.AuthorLine) > 0 {
		for _, a := range b.AuthorLine {
			if n := strings.TrimSpace(a.Name); n != "" {
				names = append(names, n)
			}
		}
		return names
	}
	for i := range b.Authors {
		if n := strings.TrimSpace(b.Authors[i].FullName); n != "" {
			names = append(names, n)
		}
	}
	return names
}

// SeriesLine is «Серия, кн. N», or the series alone without a number.
func SeriesLine(b *models.Book) string {
	for _, s := range b.Series {
		if s == nil || strings.TrimSpace(s.Ser) == "" {
			continue
		}
		if s.SerNo > 0 {
			return fmt.Sprintf("%s, кн. %d", s.Ser, s.SerNo)
		}
		return s.Ser
	}
	return ""
}

var yearOf = regexp.MustCompile(`\b(1\d{3}|20\d{2})\b`)

// Year is the book's year when its document date carries one.
func Year(b *models.Book) string {
	return yearOf.FindString(b.DocDate)
}

// CopyOpeningCard is the card while it is being made: its cover is on its way
// to Telegram.
const CopyOpeningCard = "⏳ Открываю карточку…"

// CardOpeningScreen is a book's card before it is ready: its title and
// authors, and a way out (plan.md: the card opens at once, the cover after).
// back is the card's way back, nil for none.
func CardOpeningScreen(b *models.Book, back *Button, home *cbv2.Envelope) Screen {
	blocks := []Block{Title{Text: ru.Truncate(b.Title, titleRunes)}}
	if a := Authors(b, cardAuthors); a != "" {
		blocks = append(blocks, Line{Text: a, Italic: true})
	}
	blocks = append(blocks, Gap{}, Line{Text: CopyOpeningCard})
	return Screen{Doc: Doc{Blocks: blocks}, Keys: [][]Button{lastRow(back, home)}}
}

// Card is what a book card needs besides the book.
type Card struct {
	Book *models.Book
	// Cover is the cover to show, nil without one.
	Cover *Photo
	// Rich says the card is rendered rich; the annotation's room depends on it.
	Rich          bool
	Favorite      bool
	DefaultFormat string
	// Preparing are the formats whose files are on their way.
	Preparing map[string]bool
	// Sending are those of them the bot sends from its own copy: nothing is
	// prepared, and the button does not say so.
	Sending map[string]bool
	SiteURL string
	// BackText labels the way back, "" for none.
	BackText string
	// Kindle offers «Kindle»: the account has a Kindle address.
	Kindle bool
}

// CardKeys are the card's envelopes.
type CardKeys struct {
	Download   func(format string) *cbv2.Envelope
	Favorite   func(want bool) *cbv2.Envelope
	Annotation *cbv2.Envelope
	Back       *cbv2.Envelope
	Home       *cbv2.Envelope
	// Author opens the book's one author, BookAuthors lists several, Series
	// opens its series; nil leaves the button out.
	Author      func(id int64) *cbv2.Envelope
	BookAuthors *cbv2.Envelope
	Series      func(id int64) *cbv2.Envelope
	// Kindle asks to send the book to the reader's Kindle.
	Kindle *cbv2.Envelope
}

// Limits of the card (plan-ui.md §2.2, §2.6).
const (
	richAnnotationRunes      = 6000
	richAnnotationParagraphs = 40
	plainLimit               = 4096
	plainMargin              = 64
	titleRunes               = 256
	cardAuthors              = 10
	cardGenres               = 3
)

// CardScreen renders a book card (plan-ui.md §1.8, §2). It also reports
// whether a plain annotation had to be cut.
func CardScreen(c *Card, k *CardKeys) (Screen, bool) {
	b := c.Book
	var blocks []Block
	if c.Rich && c.Cover != nil {
		blocks = append(blocks, *c.Cover)
	}
	blocks = append(blocks, Title{Text: ru.Truncate(b.Title, titleRunes)})
	if a := Authors(b, cardAuthors); a != "" {
		blocks = append(blocks, Line{Text: a, Italic: true})
	}
	if rows := cardTable(b); len(rows) > 0 {
		if !c.Rich {
			blocks = append(blocks, Gap{})
		}
		blocks = append(blocks, Table{Rows: rows})
	}
	cut := false
	annotation := strings.TrimSpace(b.Annotation)
	if annotation != "" {
		if !c.Rich {
			blocks = append(blocks, Gap{})
		}
		head := Doc{Blocks: blocks}
		var paragraphs []string
		if c.Rich {
			paragraphs = richParagraphs(annotation, c.SiteURL != "")
		} else {
			room := plainLimit - plainMargin - head.PlainLength()
			if utf8.RuneCountInString(annotation) > room {
				annotation = ru.TruncateSentence(annotation, room)
				cut = true
			}
			paragraphs = []string{annotation}
		}
		blocks = append(blocks, Details{Summary: "Аннотация", Paragraphs: paragraphs})
	}
	return Screen{Doc: Doc{Blocks: blocks}, Keys: cardKeys(c, k, cut)}, cut
}

func cardTable(b *models.Book) [][2]string {
	var rows [][2]string
	if s := SeriesLine(b); s != "" {
		rows = append(rows, [2]string{"Серия", s})
	}
	var genres []string
	for i := range b.Genres {
		if len(genres) == cardGenres {
			break
		}
		if g := strings.TrimSpace(b.Genres[i].DisplayName()); g != "" {
			genres = append(genres, g)
		}
	}
	if len(genres) > 0 {
		rows = append(rows, [2]string{"Жанр", strings.Join(genres, ", ")})
	}
	if b.Lang != "" {
		rows = append(rows, [2]string{"Язык", ru.Language(b.Lang)})
	}
	if y := Year(b); y != "" {
		rows = append(rows, [2]string{"Год", y})
	}
	return rows
}

// richParagraphs splits the annotation into at most richAnnotationParagraphs
// paragraphs within richAnnotationRunes, pointing to the site when cut.
func richParagraphs(annotation string, site bool) []string {
	cut := false
	if utf8.RuneCountInString(annotation) > richAnnotationRunes {
		annotation = ru.TruncateSentence(annotation, richAnnotationRunes)
		cut = true
	}
	var paragraphs []string
	for _, p := range strings.Split(annotation, "\n") {
		if p = strings.TrimSpace(p); p != "" {
			paragraphs = append(paragraphs, p)
		}
	}
	if len(paragraphs) > richAnnotationParagraphs {
		tail := strings.Join(paragraphs[richAnnotationParagraphs-1:], " ")
		paragraphs = append(paragraphs[:richAnnotationParagraphs-1], tail)
	}
	if cut && site {
		paragraphs = append(paragraphs, "Полностью — на сайте.")
	}
	return paragraphs
}

func cardKeys(c *Card, k *CardKeys, cut bool) [][]Button {
	order := []string{c.DefaultFormat}
	for _, f := range Formats {
		if f != c.DefaultFormat {
			order = append(order, f)
		}
	}
	var formats []Button
	for i, f := range order {
		label := strings.ToUpper(f)
		btn := Button{Text: label, Env: k.Download(f)}
		if i == 0 {
			btn.Style = "primary"
		}
		// A file on its way is shown, not pressed again.
		switch {
		case c.Sending[f]:
			btn = Button{Text: "⏳ Отправляю " + label + "…", Disabled: true}
		case c.Preparing[f]:
			btn = Button{Text: "⏳ Готовлю " + label + "…", Disabled: true}
		}
		formats = append(formats, btn)
	}
	keys := [][]Button{formats}
	favorite := Button{Text: ButtonNotFavorite, Env: k.Favorite(true)}
	if c.Favorite {
		favorite = Button{Text: ButtonFavorite, Env: k.Favorite(false)}
	}
	row := []Button{favorite}
	if c.Kindle && k.Kindle != nil {
		row = append(row, Button{Text: ButtonKindle, Env: k.Kindle})
	}
	keys = append(keys, row)
	if row := relationsRow(c.Book, k); len(row) > 0 {
		keys = append(keys, row)
	}
	if cut {
		keys = append(keys, []Button{{Text: CopyAnnotationFull, Env: k.Annotation}})
	}
	if c.SiteURL != "" {
		keys = append(keys, []Button{{Text: ButtonOnSite, URL: BookURL(c.SiteURL, c.Book.ID)}})
	}
	last := []Button{}
	if c.BackText != "" && k.Back != nil {
		last = append(last, Button{Text: c.BackText, Env: k.Back})
	}
	last = append(last, Button{Text: ButtonHome, Env: k.Home})
	return append(keys, last)
}

// relationsRow is «Книги автора» (or «Авторы книги») and «Серия».
func relationsRow(b *models.Book, k *CardKeys) []Button {
	var row []Button
	var authors []int64
	for i := range b.Authors {
		if strings.TrimSpace(b.Authors[i].FullName) != "" {
			authors = append(authors, b.Authors[i].ID)
		}
	}
	switch {
	case len(authors) == 1 && k.Author != nil:
		row = append(row, Button{Text: ButtonAuthorBooks, Env: k.Author(authors[0])})
	case len(authors) > 1 && k.BookAuthors != nil:
		row = append(row, Button{Text: ButtonBookAuthors, Env: k.BookAuthors})
	}
	if k.Series != nil {
		for _, s := range b.Series {
			if s != nil && strings.TrimSpace(s.Ser) != "" {
				row = append(row, Button{Text: ButtonSeries, Env: k.Series(s.ID)})
				break
			}
		}
	}
	return row
}

// BookURL is the site's page of one book.
func BookURL(siteURL string, bookID int64) string {
	return strings.TrimRight(siteURL, "/") + "/books/page/1?book_id=" + strconv.FormatInt(bookID, 10)
}

// annotationPageRunes is one plain annotation page.
const annotationPageRunes = 3800

// AnnotationPages splits an annotation into plain pages, at paragraph or
// word boundaries.
func AnnotationPages(annotation string) []string {
	annotation = strings.TrimSpace(annotation)
	var pages []string
	for utf8.RuneCountInString(annotation) > annotationPageRunes {
		runes := []rune(annotation)
		head := string(runes[:annotationPageRunes])
		cut := strings.LastIndex(head, "\n")
		if cut < len(head)/2 {
			cut = strings.LastIndex(head, " ")
		}
		if cut <= 0 {
			cut = len(head)
		}
		pages = append(pages, strings.TrimSpace(head[:cut]))
		annotation = strings.TrimSpace(annotation[cut:])
	}
	return append(pages, annotation)
}

// AnnotationScreen is one page of a long annotation (plain cards only).
func AnnotationScreen(b *models.Book, page int, prev, next, back, home *cbv2.Envelope) Screen {
	pages := AnnotationPages(b.Annotation)
	if page >= len(pages) {
		page = len(pages) - 1
	}
	blocks := []Block{Title{Text: ru.Truncate(b.Title, titleRunes) + " — аннотация"}, Gap{}, Line{Text: pages[page]}}
	if len(pages) > 1 {
		blocks = append(blocks, Gap{}, Line{Text: fmt.Sprintf("Страница %d из %d", page+1, len(pages))})
	}
	var keys [][]Button
	var row []Button
	if page > 0 {
		row = append(row, Button{Text: ButtonPrev, Env: prev})
	}
	if page+1 < len(pages) {
		row = append(row, Button{Text: ButtonNext, Env: next})
	}
	if len(row) > 0 {
		keys = append(keys, row)
	}
	keys = append(keys, []Button{{Text: ButtonToBook, Env: back}, {Text: ButtonHome, Env: home}})
	return Screen{Doc: Doc{Blocks: blocks}, Keys: keys}
}

// Bounds of a document's caption (1024 characters in all) and name.
const (
	captionAuthors     = 3
	captionLineRunes   = 900
	captionSeriesRunes = 60
	fileNameRunes      = 120
)

// FileCaption is the document's caption (plan-ui.md §1.10).
func FileCaption(b *models.Book, format, size string) string {
	line := b.Title
	if a := Authors(b, captionAuthors); a != "" {
		line += " — " + a
	}
	lines := []string{ru.Truncate(line, captionLineRunes)}
	if s := SeriesLine(b); s != "" {
		lines = append(lines, ru.Truncate(s, captionSeriesRunes))
	}
	lines = append(lines, strings.ToUpper(format)+" · "+size)
	return strings.Join(lines, "\n")
}

var unsafeName = regexp.MustCompile(`[/\\:*?"<>|\x00-\x1f]+`)

// FileName is «Автор - Название.ext» (plan-ui.md §1.10).
func FileName(b *models.Book, format string) string {
	name := b.Title
	authors := authorNames(b)
	switch {
	case len(authors) == 1:
		name = authors[0] + " - " + name
	case len(authors) > 1:
		name = authors[0] + " и др. - " + name
	}
	name = strings.TrimSpace(unsafeName.ReplaceAllString(name, "_"))
	if name == "" {
		name = "book"
	}
	return ru.Truncate(name, fileNameRunes) + "." + format
}
