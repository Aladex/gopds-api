package screens

import (
	"strconv"
	"strings"

	"gopds-api/models"
	"gopds-api/telegram/cbv2"
	"gopds-api/telegram/ru"
)

// The list screens as a rich message shows them: a heading and a small line
// of what is in force, the tabs as buttons in the message, the list with
// each title the link that opens it, and the pages in a row under it. The
// keyboard keeps what acts on the whole screen — the filters, the way back
// and «Домой» — in the same place as on every other screen.

// noSeriesNumber marks a book of a series that has no number in it.
const noSeriesNumber = "без номера"

// richResults is a results page laid out for a rich message.
func richResults(r *Results, k *ResultsKeys) *Screen {
	blocks := []Block{Title{Text: "Поиск: «" + r.Query + "»"}}
	total := r.BookTotal
	var list []Block
	if r.Tab == 0 {
		if line := filterLine(r); line != "" {
			blocks = append(blocks, Footer{Text: line})
		}
		list = richBookItems(r, k)
	} else {
		total = r.AuthorTotal
		blocks = append(blocks, Footer{Text: "Язык книг: " + languageOrAll(r.Lang)})
		list = richAuthorItems(r, k)
	}
	blocks = append(blocks, Buttons{Row: richTabs(r, k)})
	blocks = append(blocks, list...)
	blocks = append(blocks, pagerBlock(r.Page, Pages(total), k.Page)...)
	var keys [][]Button
	if row := filterRow(r, k); len(row) > 0 {
		keys = append(keys, row)
	}
	return &Screen{Doc: Doc{Blocks: blocks}, Keys: append(keys, lastRow(k.Back, k.Home))}
}

// languageOrAll names a language, or all of them.
func languageOrAll(lang string) string {
	if lang == "" {
		return allLanguages
	}
	return ru.Language(lang)
}

// richTabs are the results' tabs; the open one is styled, not ticked.
func richTabs(r *Results, k *ResultsKeys) []Button {
	row := tabsRow(r, k)
	for i := range row {
		row[i].Text = strings.TrimPrefix(row[i].Text, "✓ ")
	}
	open := int(r.Tab)
	failed := (r.Tab == 0 && r.BooksFailed) || (r.Tab == 1 && r.AuthorsFailed)
	if open < len(row) && !failed {
		row[open].Style = stylePrimary
	}
	return row
}

// richBookItems is the books tab's list.
func richBookItems(r *Results, k *ResultsKeys) []Block {
	switch {
	case r.BooksFailed:
		return []Block{Line{Text: copyBooksDown}}
	case r.BookTotal == 0:
		return []Block{Line{Text: copyOnlyAuthors}}
	}
	items := make([]Item, 0, len(r.Books))
	for i := range r.Books {
		b := &r.Books[i]
		items = append(items, Item{
			Value: r.Page*PageSize + i + 1,
			Link:  Button{Text: ru.Truncate(b.Title, listLineRunes), Env: k.Book(b.ID)},
			Sub:   ru.Truncate(withYear(bookSubline(b), b), listLineRunes),
		})
	}
	return []Block{List{Items: items}}
}

// richAuthorItems is the authors tab's list.
func richAuthorItems(r *Results, k *ResultsKeys) []Block {
	switch {
	case r.AuthorsFailed:
		return []Block{Line{Text: copyAuthorsDown}}
	case r.AuthorTotal == 0:
		return []Block{Line{Text: copyNoAuthors}}
	}
	items := make([]Item, 0, len(r.Authors))
	for i := range r.Authors {
		a := &r.Authors[i]
		it := Item{Value: r.Page*PageSize + i + 1, Link: Button{Text: ru.Truncate(a.FullName, listLineRunes)}}
		if k.OpenAuthor != nil {
			it.Link.Env = k.OpenAuthor(a.ID)
		}
		if a.BooksCount > 0 {
			it.Sub = ru.Books(a.BooksCount)
		}
		items = append(items, it)
	}
	return []Block{List{Items: items}}
}

// withYear adds the book's year to its second line.
func withYear(sub string, b *models.Book) string {
	y := Year(b)
	switch {
	case y == "":
		return sub
	case sub == "":
		return y
	}
	return sub + " · " + y
}

// richBookList is a page of a book list laid out for a rich message.
func richBookList(l *BookList, k *ListKeys) *Screen {
	blocks := append([]Block{}, l.Header...)
	items := make([]Item, 0, len(l.Books))
	for i := range l.Books {
		b := &l.Books[i]
		it := Item{Value: l.Page*PageSize + i + 1, Link: Button{Text: ru.Truncate(b.Title, listLineRunes), Env: k.Book(b.ID)}}
		var sub string
		if l.Sub != nil {
			sub = l.Sub(b)
		}
		if l.Number != nil {
			// A series numbers its books by their place in it; a book
			// without one is listed unnumbered, and says so.
			it.Value = 0
			if n, err := strconv.Atoi(l.Number(i, b)); err == nil && n > 0 {
				it.Value = n
			} else {
				sub = joinDot(noSeriesNumber, sub)
			}
		}
		it.Sub = ru.Truncate(withYear(sub, b), listLineRunes)
		items = append(items, it)
	}
	blocks = append(blocks, List{Items: items})
	blocks = append(blocks, pagerBlock(l.Page, Pages(l.Total), k.Page)...)
	keys := append([][]Button{}, k.Extra...)
	return &Screen{Doc: Doc{Blocks: blocks}, Keys: append(keys, lastRow(k.Back, k.Home))}
}

// joinDot joins the parts that are there with « · ».
func joinDot(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

// richCollections is a page of the collections laid out for a rich message.
func richCollections(collections []models.BookCollection, total, page int,
	open func(int64) *cbv2.Envelope, pageEnv func(int) *cbv2.Envelope, home *cbv2.Envelope) *Screen {
	items := make([]Item, 0, len(collections))
	for i := range collections {
		c := &collections[i]
		items = append(items, Item{Value: page*PageSize + i + 1, Link: Button{Text: ru.Truncate(c.Name, listLineRunes), Env: open(c.ID)}})
	}
	blocks := []Block{Title{Text: ButtonCollections}, List{Items: items}}
	blocks = append(blocks, pagerBlock(page, Pages(total), pageEnv)...)
	return &Screen{Doc: Doc{Blocks: blocks}, Keys: [][]Button{{{Text: ButtonHome, Env: home}}}}
}

// richRecent is a page of S-RECENT laid out for a rich message: a title
// opens the card, «Отправить» after its line sends it again.
func richRecent(items []RecentItem, total, page int, k *RecentKeys) *Screen {
	list := make([]Item, 0, len(items))
	for i := range items {
		it := &items[i]
		n := page*PageSize + i + 1
		if it.Book == nil {
			list = append(list, Item{Value: n, Link: Button{Text: copyRecentGoneBook}})
			continue
		}
		format := strings.ToUpper(it.Format)
		list = append(list, Item{
			Value:  n,
			Link:   Button{Text: ru.Truncate(it.Book.Title, listLineRunes), Env: k.Open(it.BookID)},
			Sub:    ru.Truncate(joinDot(Authors(it.Book, listAuthors), format, it.SentAt.UTC().Format(recentTimeLayout)), listLineRunes),
			Action: &Button{Text: "Отправить " + format, Env: k.Resend(it.BookID, it.Format)},
		})
	}
	blocks := []Block{Title{Text: ButtonRecent}, Footer{Text: copyRecentLimits}, List{Items: list}}
	blocks = append(blocks, pagerBlock(page, Pages(total), k.Page)...)
	return &Screen{Doc: Doc{Blocks: blocks}, Keys: [][]Button{{{Text: ButtonHome, Env: k.Home}}}}
}
