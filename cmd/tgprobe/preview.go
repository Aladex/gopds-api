package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	gopds "gopds-api/models"
	"gopds-api/telegram/cbv2"
	"gopds-api/telegram/screens"
	"gopds-api/telegram/tgapi"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// The list preview (-preview lists): the results, an author's books and the
// favorites as the bot lays them out in rich messages, from fixture data,
// sent to the test chat. Their buttons work until the preview stops: a tab,
// a page or a title edits the message as the bot would, and a card's way
// back returns to the list.

// The preview's fixture: how many books and authors the search finds, how
// many books the author has, and how long it answers presses by default.
const (
	previewBooks        = 23
	previewAuthors      = 7
	previewAuthorBooks  = 12
	previewFavorites    = 4
	previewWait         = 10 * time.Minute
	previewNavKeyBytes  = 32
	previewYearsAgo     = 40
	previewYearsBetween = 3
	// previewAnnotationRepeats makes a card's annotation a few lines long.
	previewAnnotationRepeats = 6
)

// previewLang is the fixture's language.
const previewLang = "ru"

// previewNames are the authors of the fixture's books.
var previewNames = [...]string{"Аркадий Стругацкий", "Борис Стругацкий", "С. Ярославцев"}

// copyPreviewOnly answers a button the preview does not act on.
const copyPreviewOnly = "В превью эта кнопка ничего не делает"

// The lists of the preview.
const (
	listResults = iota + 1
	listAuthor
	listFavorites
)

// panel is one preview message: the list it shows, where in it, the book
// open on it, and the revision its buttons carry.
type panel struct {
	list, tab, page int
	book            int64
	rev             uint16
}

// previewer sends the preview and answers its presses.
type previewer struct {
	b      *bot.Bot
	chat   int64
	botID  int64
	key    []byte
	mu     sync.Mutex
	panels map[int]*panel
	books  []gopds.Book
	people []gopds.Author
}

func newPreviewer(chat, botID int64) (*previewer, error) {
	key := make([]byte, previewNavKeyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	books, people := previewFixture()
	return &previewer{chat: chat, botID: botID, key: key, panels: map[int]*panel{}, books: books, people: people}, nil
}

// runPreview sends the three lists and answers their presses until ctx ends.
// sendOnly sends them, and each list again as a press would leave it, and
// stops: it reads no updates and leaves the bot's webhook as it is, so it
// can go out through a bot that serves something else.
func runPreview(ctx context.Context, token string, chat int64, sendOnly bool) error {
	me, err := tgapi.GetMe(ctx, token, tgapi.Endpoint{})
	if err != nil {
		return fmt.Errorf("getMe: %w", tgapi.RedactURL(err))
	}
	p, err := newPreviewer(chat, me.ID)
	if err != nil {
		return err
	}
	if sendOnly {
		if p.b, err = tgapi.New(token, tgapi.Endpoint{}); err != nil {
			return fmt.Errorf("building the client: %w", tgapi.RedactURL(err))
		}
		if sendErr := p.sendStates(ctx); sendErr != nil {
			return sendErr
		}
		fmt.Println("tgprobe: the lists are in the chat; their buttons are not answered (-send-only)")
		return nil
	}
	client, err := tgapi.New(token, tgapi.Endpoint{}, bot.WithDefaultHandler(func(ctx context.Context, _ *bot.Bot, u *models.Update) {
		p.handle(ctx, u)
	}), bot.WithErrorsHandler(func(err error) {
		fmt.Fprintln(os.Stderr, "tgprobe: polling:", tgapi.RedactURL(err))
	}))
	if err != nil {
		return fmt.Errorf("building the client: %w", tgapi.RedactURL(err))
	}
	p.b = client
	// Updates come by polling, which a bot with a webhook does not get; the
	// preview leaves another bot's webhook alone.
	info, err := client.GetWebhookInfo(ctx)
	if err != nil {
		return fmt.Errorf("getWebhookInfo: %w", tgapi.RedactURL(err))
	}
	if info.URL != "" {
		return errors.New("the bot has a webhook: the preview needs a test bot without one")
	}
	if err := p.sendAll(ctx); err != nil {
		return err
	}
	fmt.Println("tgprobe: the lists are in the chat; their buttons work until the preview stops (Ctrl+C)")
	client.Start(ctx)
	return nil
}

// sendAll sends the results, the author's books and the favorites.
func (p *previewer) sendAll(ctx context.Context) error {
	for _, list := range []int{listResults, listAuthor, listFavorites} {
		if err := p.send(ctx, &panel{list: list, rev: 1}); err != nil {
			return err
		}
	}
	return nil
}

// sendStates sends the three lists, then — as messages of their own — the
// states a press leads to: the results' next page and authors tab, the
// author's next page.
func (p *previewer) sendStates(ctx context.Context) error {
	if err := p.sendAll(ctx); err != nil {
		return err
	}
	for _, at := range []panel{
		{list: listResults, page: 1, rev: 1},
		{list: listResults, tab: 1, rev: 1},
		{list: listAuthor, page: 1, rev: 1},
	} {
		if err := p.send(ctx, &at); err != nil {
			return err
		}
	}
	return nil
}

func (p *previewer) send(ctx context.Context, at *panel) error {
	screen := p.screen(at).In(true)
	content, markup, err := p.encode(screen)
	if err != nil {
		return err
	}
	msg, err := p.b.SendRichMessage(ctx, &bot.SendRichMessageParams{ChatID: p.chat, RichMessage: content, ReplyMarkup: markup})
	if err != nil {
		return fmt.Errorf("sending a list: %w", tgapi.RedactURL(err))
	}
	p.mu.Lock()
	p.panels[msg.ID] = at
	p.mu.Unlock()
	return nil
}

// handle answers a press of a preview button: a tab, a page, a title or the
// way back edits the message; the rest only says what the bot would do.
func (p *previewer) handle(ctx context.Context, u *models.Update) {
	q := u.CallbackQuery
	if q == nil || q.Message.Message == nil {
		return
	}
	msg := q.Message.Message
	toast := p.press(ctx, msg.Chat.ID, msg.ID, q.From.ID, q.Data)
	_, _ = p.b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: q.ID, Text: toast})
}

// press acts on one press and returns its toast.
func (p *previewer) press(ctx context.Context, chat int64, messageID int, from int64, data string) string {
	env, err := cbv2.Decode(data, p.key, cbv2.BindCtx{TGBotID: p.botID, OwnerTGID: from, ChatID: chat})
	if err != nil {
		return screens.CopyButtonDenied
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	at, ok := p.panels[messageID]
	switch {
	case !ok:
		return screens.CopyExpired
	case env.Revision != at.rev:
		return screens.CopyScreenChanged
	}
	next := *at
	//exhaustive:ignore // the preview moves through its lists; the rest is the bot's
	switch env.Op {
	case cbv2.OpTab:
		next.tab, next.page = int(env.Arg), 0
	case cbv2.OpPage:
		next.page = int(env.Page)
	case cbv2.OpOpenBook:
		next.book = int64(env.EntityID) // #nosec G115 -- the fixture's ids
	case cbv2.OpBack:
		if at.book == 0 {
			return copyPreviewOnly
		}
		next.book = 0
	default:
		return copyPreviewOnly
	}
	next.rev++
	screen := p.screen(&next).In(true)
	content, markup, err := p.encode(screen)
	if err == nil {
		_, err = p.b.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: chat, MessageID: messageID,
			RichMessage: &content, ReplyMarkup: markup})
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "tgprobe: editing a list:", tgapi.RedactURL(err))
		return "Не удалось: " + tgapi.RedactURL(err).Error()
	}
	*at = next
	return ""
}

// encode is a screen's rich content and keyboard, its buttons signed with
// the preview's key.
func (p *previewer) encode(s *screens.Screen) (models.InputRichMessage, *models.InlineKeyboardMarkup, error) {
	bind := cbv2.BindCtx{TGBotID: p.botID, OwnerTGID: p.chat, ChatID: p.chat}
	enc := func(e *cbv2.Envelope) (string, error) { return cbv2.Encode(e, p.key, bind) }
	content, err := s.Doc.Rich(enc)
	if err != nil {
		return content, nil, err
	}
	markup := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{}}
	for _, row := range s.Keys {
		line := []models.InlineKeyboardButton{}
		for _, b := range row {
			btn := models.InlineKeyboardButton{Text: b.Text, Style: b.Style, URL: b.URL}
			if b.Env != nil {
				if btn.CallbackData, err = enc(b.Env); err != nil {
					return content, nil, err
				}
			}
			line = append(line, btn)
		}
		markup.InlineKeyboard = append(markup.InlineKeyboard, line)
	}
	return content, markup, nil
}

// screen paints a panel: its open book's card, or its list.
func (p *previewer) screen(at *panel) *screens.Screen {
	env := func(e cbv2.Envelope) *cbv2.Envelope {
		e.Revision = at.rev
		return &e
	}
	home := env(cbv2.Envelope{Op: cbv2.OpHome})
	if at.book != 0 {
		return p.card(at, env, home)
	}
	page := func(pg int) *cbv2.Envelope {
		return env(cbv2.Envelope{Op: cbv2.OpPage, Arg: uint8(at.tab), Page: uint32(pg)}) // #nosec G115 -- small fixture pages
	}
	open := func(id int64) *cbv2.Envelope {
		return env(cbv2.Envelope{Op: cbv2.OpOpenBook, EntityID: uint64(id)}) // #nosec G115 -- fixture ids
	}
	switch at.list {
	case listResults:
		s := screens.ResultsScreen(p.results(at), &screens.ResultsKeys{
			Tab:      func(tab uint8) *cbv2.Envelope { return env(cbv2.Envelope{Op: cbv2.OpTab, Arg: tab}) },
			RetryTab: func(tab uint8) *cbv2.Envelope { return env(cbv2.Envelope{Op: cbv2.OpRetryBranch, Arg: tab}) },
			RetryAll: env(cbv2.Envelope{Op: cbv2.OpRetrySearch}),
			Page:     page,
			Book:     open,
			OpenAuthor: func(id int64) *cbv2.Envelope {
				return env(cbv2.Envelope{Op: cbv2.OpOpenAuthor, EntityID: uint64(id)}) // #nosec G115 -- fixture ids
			},
			Author:   env(cbv2.Envelope{Op: cbv2.OpPromptAuthor}),
			Language: env(cbv2.Envelope{Op: cbv2.OpLanguagePicker}),
			Home:     home,
		})
		return &s
	case listAuthor:
		books := p.books[:previewAuthorBooks]
		s := screens.BookListScreen(&screens.BookList{
			Header: []screens.Block{screens.Title{Text: "Аркадий и Борис Стругацкие"},
				screens.Footer{Text: screens.CountLine(len(books), previewLang)}},
			Books: pageOf(books, at.page), Total: len(books), Page: at.page, Sub: screens.SeriesSub,
		}, &screens.ListKeys{Page: page, Book: open, Home: home,
			Extra: [][]screens.Button{{
				{Text: screens.ButtonSearchAuthor, Env: env(cbv2.Envelope{Op: cbv2.OpPromptAuthorSearch})},
				{Text: screens.ButtonLanguage + ": русский", Env: env(cbv2.Envelope{Op: cbv2.OpLanguagePicker})},
			}},
			Back: &screens.Button{Text: screens.ButtonToResults, Env: env(cbv2.Envelope{Op: cbv2.OpBack})}})
		return &s
	default:
		books := p.books[previewBooks-previewFavorites:]
		s := screens.BookListScreen(&screens.BookList{
			Header: []screens.Block{screens.Title{Text: "Избранное"}, screens.Footer{Text: screens.CountLine(len(books), "")}},
			Books:  books, Total: len(books), Sub: screens.AuthorsLine,
		}, &screens.ListKeys{Page: page, Book: open, Home: home,
			Extra: [][]screens.Button{{{Text: screens.ButtonSearchFavs, Env: env(cbv2.Envelope{Op: cbv2.OpPromptFavSearch})}}}})
		return &s
	}
}

// card is the open book's card, rich, with the way back to the list.
func (p *previewer) card(at *panel, env func(cbv2.Envelope) *cbv2.Envelope, home *cbv2.Envelope) *screens.Screen {
	var book *gopds.Book
	for i := range p.books {
		if p.books[i].ID == at.book {
			book = &p.books[i]
		}
	}
	if book == nil {
		book = &p.books[0]
	}
	other := env(cbv2.Envelope{Op: cbv2.OpDownload})
	s, _ := screens.CardScreen(&screens.Card{Book: book, Rich: true, DefaultFormat: screens.FormatEPUB, BackText: screens.ButtonToResults},
		&screens.CardKeys{
			Download:   func(string) *cbv2.Envelope { return other },
			Favorite:   func(bool) *cbv2.Envelope { return env(cbv2.Envelope{Op: cbv2.OpSetFavorite}) },
			Annotation: env(cbv2.Envelope{Op: cbv2.OpAnnotation}),
			Back:       env(cbv2.Envelope{Op: cbv2.OpBack}),
			Home:       home,
		})
	return &s
}

// results is the search page a panel is on.
func (p *previewer) results(at *panel) *screens.Results {
	r := &screens.Results{Query: "мир", Lang: previewLang, Tab: uint8(at.tab), Page: at.page, // #nosec G115 -- two tabs
		BookTotal: previewBooks, AuthorTotal: previewAuthors}
	if at.tab == 0 {
		r.Books = pageOf(p.books, at.page)
		return r
	}
	start := min(at.page*screens.PageSize, len(p.people))
	r.Authors = p.people[start:min(start+screens.PageSize, len(p.people))]
	return r
}

func pageOf(books []gopds.Book, page int) []gopds.Book {
	start := min(page*screens.PageSize, len(books))
	return books[start:min(start+screens.PageSize, len(books))]
}

// previewFixture is the preview's catalog: long titles and short, series
// with numbers and without, one author and several, years.
func previewFixture() ([]gopds.Book, []gopds.Author) {
	titles := []string{
		"Трудно быть богом",
		"Понедельник начинается в субботу. Сказка для научных сотрудников младшего возраста",
		"Пикник на обочине",
		"Обитаемый остров",
		"Жук в муравейнике",
		"Волны гасят ветер",
		"Хромая судьба",
		"Град обреченный",
		"Отягощённые злом, или Сорок лет спустя",
		"За миллиард лет до конца света. Рукопись, обнаруженная при странных обстоятельствах",
		"Улитка на склоне",
		"Малыш",
		"Мир Полудня: Полдень, XXII век",
		"Стажёры",
		"Страна багровых туч",
		"Путь на Амальтею",
		"Далёкая Радуга",
		"Попытка к бегству",
		"Парень из преисподней",
		"Сказка о Тройке",
		"Мир, которого не было: очень длинное название книги, чтобы посмотреть, как переносится ссылка в списке",
		"Гадкие лебеди",
		"Дьявол среди людей",
	}
	names := make([]gopds.Author, 0, len(previewNames))
	for i, name := range previewNames {
		names = append(names, gopds.Author{ID: int64(i + 1), FullName: name})
	}
	// Both brothers, the elder alone, the three of them.
	credits := [][]gopds.Author{names[:2], names[:1], names}
	year := time.Now().Year() - previewYearsAgo
	books := make([]gopds.Book, 0, len(titles))
	for i, title := range titles {
		b := gopds.Book{ID: int64(i + 1), Title: title, Lang: previewLang, Authors: credits[i%len(credits)],
			DocDate:    fmt.Sprintf("%d-01-01", year+i%previewYearsBetween),
			Annotation: strings.Repeat("Аннотация книги из превью. ", previewAnnotationRepeats)}
		if i%2 == 0 {
			b.Series = []*gopds.Series{{ID: 1, Ser: "Мир Полудня", SerNo: int64(i/2 + 1)}}
		}
		books = append(books, b)
	}
	// The authors the search finds, and how many books each has.
	found := []struct {
		name  string
		books int
	}{
		{previewNames[0], 41}, {previewNames[1], 38}, {previewNames[2], 3}, {"С. Витицкий", 2}, {"Мир Миров", 1},
		{"Мирон Владимирович Очень-Длинная-Фамилия-Для-Проверки-Переноса", 0}, {"Мирра Лохвицкая", 12},
	}
	people := make([]gopds.Author, 0, len(found))
	for i, a := range found {
		people = append(people, gopds.Author{ID: int64(i + 1), FullName: a.name, BooksCount: a.books})
	}
	return books, people
}
