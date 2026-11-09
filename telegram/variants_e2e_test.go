package telegram_test

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/telegram/tgtest"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A card's files in the bot: the card's button, its variant list on the same
// panel with each document's earlier revisions folded, and a pick that opens
// the card on that file.

// fileSpec is one file of a fixture card.
type fileSpec struct {
	// doc and version name the FB2 document and its revision: files of one
	// doc are revisions of one another.
	doc, version string
	translator   string
	publisher    string
	year         int
}

// variantCard writes a card of the files into the catalog: one title, one
// language and one author entity, each file its own annotation «Аннотация
// файла <i>» so a card says which it shows. It brings the card read model up
// to date and returns the files in the order given. The files are also the
// books of one legacy author, so a card offers «Книги автора».
func (v *v2Stack) variantCard(title string, files []fileSpec) []int64 {
	v.t.Helper()
	entity := uuid.New()
	_, err := v.db.Exec(`INSERT INTO author_entity (id, kind, display_name, sort_name, search_key)
		VALUES (?, 'person', 'Станислав Лем', 'Лем Станислав', public.search_normalize('Станислав Лем'))`, entity)
	require.NoError(v.t, err)
	var author int64
	_, err = v.db.QueryOne(pg.Scan(&author), `INSERT INTO opds_catalog_author (full_name) VALUES (?) RETURNING id`,
		"Лем Станислав "+entity.String()[:8])
	require.NoError(v.t, err)
	ids := make([]int64, len(files))
	added := time.Now().Add(-24 * time.Hour)
	for i, f := range files {
		_, err = v.db.QueryOne(pg.Scan(&ids[i]), `INSERT INTO opds_catalog_book
			(filename, path, format, title, annotation, lang, registerdate, docdate, approved, cover, md5, duplicate_hidden)
			VALUES (?, 'variants.zip', 'fb2', ?, ?, 'ru', ?, ?, true, false, md5(?), false) RETURNING id`,
			fmt.Sprintf("%s-%d.fb2", title, i), title, fmt.Sprintf("Аннотация файла %d.", i),
			added.Add(time.Duration(i)*time.Second), fmt.Sprint(f.year), fmt.Sprintf("%s-%d-%d", title, i, time.Now().UnixNano()))
		require.NoError(v.t, err)
		_, err = v.db.Exec(`INSERT INTO opds_catalog_bauthor (book_id, author_id) VALUES (?, ?)`, ids[i], author)
		require.NoError(v.t, err)
		_, err = v.db.Exec(`INSERT INTO book_author_display
			(book_id, position, display, sort_key, search_key, source, credit_id, entity_ids)
			VALUES (?, 0, 'Станислав Лем', 'лем станислав', 'станислав лем', 'layer', ?, ARRAY[?::uuid])`, ids[i], ids[i], entity)
		require.NoError(v.t, err)
		var snapshot int64
		_, err = v.db.QueryOne(pg.Scan(&snapshot), `INSERT INTO book_metadata_snapshot
				(book_id, book_md5, extractor_version, origin, outcome, archive_path, entry_name,
				 source_title, source_document_id, source_document_version, source_publisher, is_current)
			SELECT id, md5, 'extractor-v1', 'live', 'extracted', path, filename, title, ?, ?, nullif(?, ''), true
			FROM opds_catalog_book WHERE id = ? RETURNING id`, f.doc, f.version, f.publisher, ids[i])
		require.NoError(v.t, err)
		if f.translator != "" {
			_, err = v.db.Exec(`INSERT INTO book_contributor_credit
				(snapshot_id, role, position, source_display_name, source_fingerprint)
				VALUES (?, 'translator', 0, ?, sha256(convert_to(?, 'UTF8')))`, snapshot, f.translator, f.translator)
			require.NoError(v.t, err)
		}
	}
	v.settleVariants(ids...)
	return ids
}

// settleVariants brings the card read model up to date for the books: the
// first time by walking it to its end (the backfill, after which the bot
// lists cards), afterwards by draining their marks.
func (v *v2Stack) settleVariants(ids ...int64) {
	v.t.Helper()
	ctx := context.Background()
	ready, err := database.BookVariantsReady(ctx, v.db)
	require.NoError(v.t, err)
	if !ready {
		for step := 0; step < 200; step++ {
			res, err := database.BookVariantWalkStep(ctx, v.db, 50, time.Hour, 5*time.Second, time.Time{})
			require.NoError(v.t, err)
			if res.Finished {
				return
			}
		}
		v.t.Fatal("the variants walk did not finish")
	}
	require.NoError(v.t, database.MarkBookVariantsDirty(ctx, v.db, ids...))
	for {
		work, err := database.DrainBookVariantMarks(ctx, v.db, 50, 5*time.Second, time.Time{})
		require.NoError(v.t, err)
		if work.Books == 0 {
			return
		}
	}
}

// thisYear anchors the fixtures' publication years: counted back from now.
var thisYear = time.Now().Year()

// solaris is a card of three documents by translator. By rank: Ким's (the
// newest year, the default), Ли's, Бо's.
var solaris = []fileSpec{
	{doc: "sol-bo", version: "1.0", translator: "Анна Бо", publisher: "АСТ", year: thisYear - 24},
	{doc: "sol-li", version: "1.0", translator: "Ирина Ли", publisher: "Азбука", year: thisYear - 16},
	{doc: "sol-kim", version: "1.0", translator: "Олег Ким", publisher: "Эксмо", year: thisYear - 10},
}

// answerTexts are the toasts the presses were answered with.
func (v *v2Stack) answerTexts() []string {
	var out []string
	for _, c := range v.fake.AnsweredCalls("answerCallbackQuery") {
		out = append(out, c.Params["text"])
	}
	return out
}

// openFile searches the title and opens the card of the file whose
// annotation is the i-th: from the results when they list files, through the
// card's variant list when they list cards. It returns the results panel's
// message, now the card.
func (v *v2Stack) openFile(title string, i int) tgtest.Call {
	v.t.Helper()
	want := fmt.Sprintf("Аннотация файла %d.", i)
	v.say(strings.ToLower(title))
	results := v.lastSend()
	for _, label := range tgtest.Labels(v.t, &results) {
		if !strings.Contains(label, title) {
			continue
		}
		v.tap(&results, label)
		card := v.latest(results.MessageID)
		if strings.Contains(content(&card), want) {
			return card
		}
		if picked, ok := v.pickVariant(&card, want); ok {
			return picked
		}
		v.tap(&card, "← К результатам")
		results = v.latest(results.MessageID)
	}
	v.t.Fatalf("no result opens file %d of %q", i, title)
	return tgtest.Call{}
}

// pickVariant opens, from a card with variants, the file whose card shows
// want, trying the variant list's rows in turn.
func (v *v2Stack) pickVariant(card *tgtest.Call, want string) (tgtest.Call, bool) {
	v.t.Helper()
	button := firstRow(v.t, card)[0]
	if strings.HasPrefix(button, "EPUB") || strings.HasPrefix(button, "FB2") {
		return tgtest.Call{}, false
	}
	v.tap(card, button)
	list := v.latest(card.MessageID)
	var rows []string
	for _, label := range tgtest.Labels(v.t, &list) {
		if strings.Contains(label, ". ") && !strings.Contains(label, "ранн") {
			rows = append(rows, label)
		}
	}
	for _, row := range rows {
		v.tap(&list, row)
		picked := v.latest(card.MessageID)
		if strings.Contains(content(&picked), want) {
			return picked, true
		}
		v.tap(&picked, firstRow(v.t, &picked)[0])
		list = v.latest(card.MessageID)
	}
	return tgtest.Call{}, false
}

func firstRow(t *testing.T, c *tgtest.Call) []string {
	t.Helper()
	rows := tgtest.ButtonTexts(t, c)
	require.NotEmpty(t, rows)
	return rows[0]
}

func TestTheCardOffersItsVariants(t *testing.T) {
	v := newV2(t)
	v.plainCards()
	files := v.variantCard("Солярис", solaris)
	sends := len(v.sends())

	card := v.openFile("Солярис", 1) // Ли's, rank 2
	assert.Equal(t, []string{"Вариант 2 из 3 · пер. И. Ли"}, firstRow(t, &card), "the button comes first")
	assert.Equal(t, []string{"EPUB", "FB2", "MOBI", "ZIP"}, tgtest.ButtonTexts(t, &card)[1])

	v.tap(&card, "Вариант 2 из 3 · пер. И. Ли")
	list := v.latest(card.MessageID)
	assert.Equal(t, "editMessageText", list.Method, "the same panel")
	text := content(&list)
	for _, want := range []string{"Варианты", "Солярис", "3 варианта", "Отличаются: перевод, издательство и год",
		fmt.Sprintf("1. пер. О. Ким · Эксмо, %d · основной", thisYear-10),
		fmt.Sprintf("2. пер. И. Ли · Азбука, %d ✓", thisYear-16),
		fmt.Sprintf("3. пер. А. Бо · АСТ, %d", thisYear-24)} {
		assert.Contains(t, text, want)
	}
	assert.Equal(t, []string{"← К книге", "Домой"}, tgtest.ButtonTexts(t, &list)[3])

	v.tap(&list, "1. пер. О. Ким")
	card = v.latest(card.MessageID)
	assert.Contains(t, content(&card), "Аннотация файла 2.", "the card on Ким's file")
	assert.Equal(t, []string{"3 варианта"}, firstRow(t, &card), "the default file: the count")

	v.tap(&card, "3 варианта")
	list = v.latest(card.MessageID)
	v.tap(&list, "3. пер. А. Бо")
	card = v.latest(card.MessageID)
	assert.Contains(t, content(&card), "Аннотация файла 0.")
	assert.Equal(t, []string{"Вариант 3 из 3 · пер. А. Бо"}, firstRow(t, &card))

	// The picked file's own download.
	v.tap(&card, "EPUB")
	var job int64
	_, err := v.db.QueryOne(pg.Scan(&job), `SELECT book_id FROM telegram_delivery ORDER BY id DESC LIMIT 1`)
	require.NoError(t, err)
	assert.Equal(t, files[0], job)

	// «Назад» from a picked file is the list the card came from.
	card = v.latest(card.MessageID)
	v.tap(&card, "← К результатам")
	back := v.latest(card.MessageID)
	assert.Contains(t, content(&back), "Поиск: «солярис»")

	// «← К книге» returns to the card on the file it showed.
	card = v.openFile("Солярис", 1)
	v.tap(&card, "Вариант 2 из 3 · пер. И. Ли")
	list = v.latest(card.MessageID)
	v.tap(&list, "← К книге")
	card = v.latest(card.MessageID)
	assert.Contains(t, content(&card), "Аннотация файла 1.")

	for _, s := range v.sends()[sends:] {
		assert.NotContains(t, content(&s), "Варианты", "the variant list is never a new message")
	}
}

// Earlier revisions fold under their document, as on the site; the fold
// opens that document's revisions, and «← К вариантам» returns.
func TestEarlierRevisionsFoldAsOnTheSite(t *testing.T) {
	v := newV2(t)
	v.plainCards()
	files := v.variantCard("Непобедимый", []fileSpec{
		{doc: "inv-a", version: "1.0", translator: "Анна Бо", publisher: "АСТ", year: thisYear - 25},
		{doc: "inv-a", version: "1.1", translator: "Анна Бо", publisher: "АСТ", year: thisYear - 24},
		{doc: "inv-a", version: "2.0", translator: "Анна Бо", publisher: "АСТ", year: thisYear - 23},
		{doc: "inv-b", version: "1.0", translator: "Ирина Ли", publisher: "Азбука", year: thisYear - 16},
	})
	_ = files
	card := v.openFile("Непобедимый", 3) // Ли's, the newest year: the default
	require.Equal(t, []string{"2 варианта"}, firstRow(t, &card))
	v.tap(&card, "2 варианта")
	list := v.latest(card.MessageID)
	text := content(&list)
	assert.Contains(t, text, "ещё 2 ранние редакции")
	assert.NotContains(t, text, "ред. 1.1", "folded")
	fold := labelWith(t, &list, "ещё 2 ранние редакции")

	v.tap(&list, fold)
	revs := v.latest(card.MessageID)
	text = content(&revs)
	for _, want := range []string{"Редакции", "3 редакции", "1. ред. 2.0", "2. ред. 1.1", "3. ред. 1.0"} {
		assert.Contains(t, text, want)
	}
	assert.NotContains(t, text, "Ли", "one document only")
	assert.Contains(t, tgtest.Labels(t, &revs), "← К вариантам")

	v.tap(&revs, "← К вариантам")
	list = v.latest(card.MessageID)
	assert.Contains(t, content(&list), "Отличаются:")

	v.tap(&list, fold)
	revs = v.latest(card.MessageID)
	v.tap(&revs, "2. ред. 1.1")
	card = v.latest(card.MessageID)
	assert.Contains(t, content(&card), "Аннотация файла 1.")
	assert.Equal(t, []string{"Вариант 2 из 2 · пер. А. Бо"}, firstRow(t, &card), "an earlier revision stands at its document's place")
	assert.Contains(t, tgtest.Labels(t, &card), "← К результатам", "the pick keeps the card's way back")
}

// The star is the card's: a star on another file fills it, unstarring takes
// every file out, starring saves the shown file.
func TestTheStarIsCardLevel(t *testing.T) {
	v := newV2(t)
	v.plainCards()
	files := v.variantCard("Солярис", solaris)
	_, err := v.db.Exec(`INSERT INTO favorite_books (user_id, book_id) VALUES (?, ?), (?, ?)`,
		v.user, files[2], v.user, files[0])
	require.NoError(t, err)

	card := v.openFile("Солярис", 1)
	assert.Contains(t, tgtest.Labels(t, &card), "★ В избранном", "Ким's and Бо's stars are the card's")
	v.tap(&card, "★ В избранном")
	assert.Equal(t, 0, v.count(`SELECT count(*) FROM favorite_books WHERE user_id = ?`, v.user))
	assert.Contains(t, v.answerTexts(), "Убрано из избранного")

	card = v.latest(card.MessageID)
	v.tap(&card, "☆ В избранное")
	var starred []int64
	_, err = v.db.Query(&starred, `SELECT book_id FROM favorite_books WHERE user_id = ?`, v.user)
	require.NoError(t, err)
	assert.Equal(t, []int64{files[1]}, starred, "the shown file is saved")
}

// With telegram.cards off the card is as it was: no variant row, the star
// per file, and a variant button already sent is not served.
func TestCardsOffKeepTheCardPerFile(t *testing.T) {
	v := newV2(t)
	v.plainCards()
	files := v.variantCard("Солярис", solaris)
	_, err := v.db.Exec(`INSERT INTO favorite_books (user_id, book_id) VALUES (?, ?)`, v.user, files[2])
	require.NoError(t, err)
	card := v.openFile("Солярис", 1)
	require.Equal(t, []string{"Вариант 2 из 3 · пер. И. Ли"}, firstRow(t, &card))

	off := *v
	off.stack = func() *stack { s := *v.stack; s.cardsOff = true; return s.start() }()
	off.tap(&card, "Вариант 2 из 3 · пер. И. Ли")
	assert.Contains(t, off.answerTexts(), "Эта кнопка больше не поддерживается.")

	card = off.openFile("Солярис", 1)
	assert.Equal(t, []string{"EPUB", "FB2", "MOBI", "ZIP"}, firstRow(t, &card), "no variant row")
	assert.Contains(t, tgtest.Labels(t, &card), "☆ В избранное", "the star is the file's")
}

// A list's button names a file, and opens the card on it: a button of a
// per-file list, sent before the release, opens what it named, now with the
// variant row.
func TestAnOldOpenBookButtonOpensItsFile(t *testing.T) {
	v := newV2(t)
	v.plainCards()
	v.variantCard("Солярис", solaris)
	off := *v
	off.stack = func() *stack { s := *v.stack; s.cardsOff = true; return s.start() }()
	off.say("солярис")
	results := off.lastSend()
	require.Contains(t, tgtest.Labels(t, &results), "✓ Книги · 3", "the release before listed the files")
	for _, label := range tgtest.Labels(t, &results) {
		if !strings.Contains(label, "Солярис") {
			continue
		}
		v.tap(&results, label)
		card := v.latest(results.MessageID)
		if strings.Contains(content(&card), "Аннотация файла 0.") {
			assert.Equal(t, []string{"Вариант 3 из 3 · пер. А. Бо"}, firstRow(t, &card))
			return
		}
		v.tap(&card, "← К результатам")
		results = v.latest(results.MessageID)
	}
	t.Fatal("no old button opened Бо's file")
}

// The shown file gone: its variant list is not there.
func TestVariantsOfAGoneFile(t *testing.T) {
	v := newV2(t)
	v.plainCards()
	files := v.variantCard("Солярис", solaris)
	card := v.openFile("Солярис", 1)
	_, err := v.db.Exec(`UPDATE opds_catalog_book SET duplicate_hidden = true WHERE id = ?`, files[1])
	require.NoError(t, err)
	v.tap(&card, "Вариант 2 из 3 · пер. И. Ли")
	assert.Contains(t, v.answerTexts(), "Эта книга больше недоступна.")
}

// A card that shrank under an open list: a page past its end shows the last
// one and says so.
func TestVariantsPageAfterTheCardShrank(t *testing.T) {
	v := newV2(t)
	v.plainCards()
	var specs []fileSpec
	for i := 0; i < 7; i++ {
		specs = append(specs, fileSpec{doc: fmt.Sprintf("seven-%d", i), version: "1.0",
			translator: fmt.Sprintf("Переводчик %c", 'А'+rune(i)), year: thisYear - 30 + i})
	}
	files := v.variantCard("Эдем", specs)
	v.say("эдем")
	results := v.lastSend()
	v.tap(&results, labelWith(t, &results, "1. Эдем"))
	card := v.latest(results.MessageID)
	v.tap(&card, firstRow(t, &card)[0])
	list := v.latest(card.MessageID)
	assert.Contains(t, content(&list), "Страница 1 из 2")

	// Three files the card does not show go.
	var gone []int64
	for _, id := range files {
		if len(gone) < 3 && !strings.Contains(content(&card), fmt.Sprintf("Аннотация файла %d.", indexOf(files, id))) {
			gone = append(gone, id)
		}
	}
	_, err := v.db.Exec(`UPDATE opds_catalog_book SET duplicate_hidden = true WHERE id IN (?)`, pg.In(gone))
	require.NoError(t, err)
	v.tap(&list, "Вперёд →")
	list = v.latest(card.MessageID)
	assert.NotContains(t, content(&list), "Страница", "one page left")
	assert.Contains(t, content(&list), "4 варианта")
	assert.Contains(t, v.answerTexts(), "Список изменился. Открыта последняя страница.")
}

func indexOf(ids []int64, id int64) int {
	for i := range ids {
		if ids[i] == id {
			return i
		}
	}
	return -1
}

// leaseStatements counts the statements run on behalf of updates (under an
// inbox lease) while fn runs.
func (v *v2Stack) leaseStatements(fn func()) int {
	v.t.Helper()
	var n atomic.Int64
	var on atomic.Bool
	v.db.AddQueryHook(queryHook(func(ctx context.Context, _ string) error {
		if _, ok := database.InboxLeaseFrom(ctx); ok && on.Load() {
			n.Add(1)
		}
		return nil
	}))
	on.Store(true)
	fn()
	on.Store(false)
	return int(n.Load())
}

// A card reads the same statements however many files it holds: the files
// are read in batches, never one by one.
func TestCardReadsDoNotGrowWithVariants(t *testing.T) {
	v := newV2(t)
	v.plainCards()
	two := make([]fileSpec, 2)
	many := make([]fileSpec, 22)
	for i := range two {
		two[i] = fileSpec{doc: fmt.Sprintf("two-%d", i), version: "1.0", translator: fmt.Sprintf("Ли %d", i), year: thisYear - 30 + i}
	}
	for i := range many {
		many[i] = fileSpec{doc: fmt.Sprintf("many-%d", i%11), version: fmt.Sprintf("1.%d", i/11),
			translator: fmt.Sprintf("Ким %d", i%11), year: thisYear - 30 + i%11}
	}
	v.variantCard("Кибериада", two)
	v.variantCard("Насморк", many)
	open := func(results *tgtest.Call, title string) tgtest.Call {
		v.tap(results, labelWith(t, results, "1. "+title))
		return v.latest(results.MessageID)
	}
	// One open first, so what is read once a process (the read model's
	// readiness) is not counted.
	v.say("кибериада")
	warm := v.lastSend()
	open(&warm, "Кибериада")

	v.say("кибериада")
	smallList := v.lastSend()
	v.say("насморк")
	bigList := v.lastSend()
	var small, big tgtest.Call
	smallCard := v.leaseStatements(func() { small = open(&smallList, "Кибериада") })
	bigCard := v.leaseStatements(func() { big = open(&bigList, "Насморк") })
	t.Logf("statements: open a card of 2 files %d, of 22 files %d", smallCard, bigCard)
	assert.Equal(t, smallCard, bigCard, "opening a card of 2 and of 22 files")

	smallVariants := v.leaseStatements(func() { v.tap(&small, firstRow(t, &small)[0]) })
	bigVariants := v.leaseStatements(func() { v.tap(&big, firstRow(t, &big)[0]) })
	t.Logf("statements: list the files of 2 %d, of 22 %d", smallVariants, bigVariants)
	assert.Equal(t, smallVariants, bigVariants, "listing the files of 2 and of 22")
}

// A single book's card reads what it read before cards: the card's star in
// the one statement the file's star took.
func TestASingleBookCardReadsAsBefore(t *testing.T) {
	v := newV2(t)
	v.plainCards()
	v.variantCard("Солярис", solaris) // the read model walked: cards are on
	off := *v
	off.stack = func() *stack { s := *v.stack; s.cardsOff = true; return s.start() }()
	// One open on each pod first, so what is read once a process is not
	// counted.
	v.openFixtureCard()
	off.openFixtureCard()

	// The card alone: the search itself reads more with cards (the cards of
	// its page), which Task 4 measures on its own.
	openOnly := func(p *v2Stack) int {
		p.say("мир")
		results := p.lastSend()
		return p.leaseStatements(func() { p.openFixtureCardFrom(&results) })
	}
	on, without := openOnly(v), openOnly(&off)
	t.Logf("statements: a single book's card with cards on %d, off %d", on, without)
	assert.Equal(t, without, on)
}
