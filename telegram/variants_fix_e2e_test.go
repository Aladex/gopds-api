package telegram_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"gopds-api/models"
	"gopds-api/telegram/screens"
	"gopds-api/telegram/tgtest"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Follow-ups of the variant reviews: the rich variant list, a variant button
// before the read model is walked, paging inside one document, and saved
// files opened from the favorites and a collection.

// The variant list in a rich panel: its rows are links in the message, the
// shown and default files marked, a document's fold an action of its row;
// a link opens the card on that file.
func TestTheVariantListInRich(t *testing.T) {
	v := newV2(t)
	v.variantCard("Солярис", append([]fileSpec{{doc: "sol-bo", version: "0.9", translator: "Анна Бо",
		publisher: "АСТ", year: thisYear - 25}}, solaris...))
	v.say("солярис")
	results := v.lastSend()
	v.tap(&results, labelWith(t, &results, "Солярис"))
	card := v.latest(results.MessageID)
	require.Equal(t, []string{"3 варианта"}, firstRow(t, &card))
	v.tap(&card, "3 варианта")
	list := v.latest(card.MessageID)
	rich := content(&list)
	require.Contains(t, richBlockTypes(t, &list), "list", "a rich list: %s", rich)
	links := map[string]bool{}
	for _, b := range tgtest.RichButtons(t, &list) {
		links[b.Text] = true
	}
	for _, want := range []string{"пер. О. Ким", "пер. И. Ли", "пер. А. Бо", "ещё 1 ранняя редакция"} {
		assert.True(t, links[want], "%q is a link in the message: %v", want, links)
	}
	assert.Contains(t, rich, "основной")
	assert.Contains(t, rich, "✓", "the shown file is marked")

	v.tap(&list, "ещё 1 ранняя редакция")
	revs := v.latest(card.MessageID)
	assert.Contains(t, content(&revs), "2 редакции")
	v.tap(&revs, "← К вариантам")
	list = v.latest(card.MessageID)
	v.tap(&list, "пер. И. Ли")
	card = v.latest(card.MessageID)
	assert.Contains(t, content(&card), "Аннотация файла 2.", "Ли's file")
	assert.Contains(t, tgtest.Labels(t, &card), "← К результатам")
}

// A variant button pressed on a process that finds the read model not yet
// walked is not served: the bot lists files until it is.
func TestAVariantButtonBeforeTheWalk(t *testing.T) {
	v := newV2(t)
	v.plainCards()
	v.variantCard("Солярис", solaris)
	card := v.openFile("Солярис", 1)
	button := firstRow(t, &card)[0]
	require.True(t, strings.HasPrefix(button, "Вариант 2 из 3"), button)

	_, err := v.db.Exec(`UPDATE book_variant_walk SET finished_at = NULL`)
	require.NoError(t, err)
	fresh := *v
	fresh.stack = v.secondPod()
	edits := len(fresh.edits(card.MessageID))
	fresh.tap(&card, button)
	assert.Contains(t, fresh.answerTexts(), "Эта кнопка больше не поддерживается.")
	assert.Len(t, fresh.edits(card.MessageID), edits, "the panel is unchanged")
}

// One document's revisions page like any list; a page past their end, after
// some went, opens the last one and says so; «← К вариантам» returns.
func TestPagingInsideADocument(t *testing.T) {
	v := newV2(t)
	v.plainCards()
	specs := []fileSpec{{doc: "other", version: "1.0", translator: "Ирина Ли", publisher: "Азбука", year: thisYear - 5}}
	for i := 0; i < 8; i++ {
		specs = append(specs, fileSpec{doc: "eight", version: fmt.Sprintf("1.%d", i), translator: "Анна Бо",
			publisher: "АСТ", year: thisYear - 20})
	}
	files := v.variantCard("Глас Господа", specs)
	card := v.openFile("Глас Господа", 0) // Ли's: the newest year, the default
	require.Equal(t, []string{"2 варианта"}, firstRow(t, &card))
	v.tap(&card, "2 варианта")
	list := v.latest(card.MessageID)
	v.tap(&list, labelWith(t, &list, "ещё 7 ранних редакций"))
	revs := v.latest(card.MessageID)
	text := content(&revs)
	assert.Contains(t, text, "8 редакций")
	assert.Contains(t, text, "1. ред. 1.7")
	assert.Contains(t, text, "Страница 1 из 2")

	v.tap(&revs, screens.ButtonNext)
	revs = v.latest(card.MessageID)
	text = content(&revs)
	assert.Contains(t, text, "6. ред. 1.2", "page 2 of the same document")
	assert.Contains(t, text, "Редакции")
	assert.NotContains(t, text, "Ли")
	v.tap(&revs, screens.ButtonPrev)
	revs = v.latest(card.MessageID)

	// Four earlier revisions go: one page left.
	_, err := v.db.Exec(`UPDATE opds_catalog_book SET duplicate_hidden = true WHERE id IN (?)`, pg.In(files[1:5]))
	require.NoError(t, err)
	v.tap(&revs, screens.ButtonNext)
	revs = v.latest(card.MessageID)
	assert.Contains(t, content(&revs), "4 редакции")
	assert.NotContains(t, content(&revs), "Страница")
	assert.Contains(t, v.answerTexts(), "Список изменился. Открыта последняя страница.")

	v.tap(&revs, "← К вариантам")
	list = v.latest(card.MessageID)
	assert.Contains(t, content(&list), "Отличаются:")
	assert.Contains(t, content(&list), "ещё 3 ранние редакции")
}

// savedFixture is a card of three files whose rank-3 file the reader starred
// and whose rank-2 file a public collection holds.
func (v *v2Stack) savedFixture() (ranked []int64) {
	v.t.Helper()
	files := v.variantCard("Солярис", solaris)
	_, err := v.db.Query(&ranked, `SELECT book_id FROM book_variant_member WHERE book_id IN (?) ORDER BY variant_rank`, pg.In(files))
	require.NoError(v.t, err)
	require.Len(v.t, ranked, 3)
	_, err = v.db.Exec(`INSERT INTO favorite_books (user_id, book_id) VALUES (?, ?)`, v.user, ranked[2])
	require.NoError(v.t, err)
	var collection int64
	_, err = v.db.QueryOne(pg.Scan(&collection), `INSERT INTO book_collections (name, is_public, is_curated)
		VALUES ('Лучшее Лема', true, true) RETURNING id`)
	require.NoError(v.t, err)
	_, err = v.db.Exec(`INSERT INTO book_collection_items (collection_id, book_id, external_title, match_status, position)
		VALUES (?, ?, 'Солярис', ?, 0)`, collection, ranked[1], models.MatchStatusAutoMatched)
	require.NoError(v.t, err)
	return ranked
}

// A favorites row and a collection row open the card on the saved file,
// plain and rich.
func TestSavedRowsOpenTheSavedFile(t *testing.T) {
	for _, plain := range []bool{true, false} {
		t.Run(fmt.Sprintf("plain=%t", plain), func(t *testing.T) {
			v := newV2(t)
			if plain {
				v.plainCards()
			}
			v.savedFixture()

			home := v.homeAgain()
			v.tap(&home, screens.ButtonFavorites)
			favs := v.latest(home.MessageID)
			assert.Contains(t, content(&favs), "3 варианта")
			v.tap(&favs, labelWith(t, &favs, "Солярис"))
			card := v.latest(home.MessageID)
			assert.Contains(t, content(&card), "Аннотация файла 0.", "the starred file, Бо's")
			assert.Equal(t, []string{"Вариант 3 из 3 · пер. А. Бо"}, firstRow(t, &card))

			home = v.homeAgain()
			v.tap(&home, screens.ButtonCollections)
			collections := v.latest(home.MessageID)
			v.tap(&collections, labelWith(t, &collections, "Лучшее Лема"))
			list := v.latest(home.MessageID)
			v.tap(&list, labelWith(t, &list, "Солярис"))
			card = v.latest(home.MessageID)
			assert.Contains(t, content(&card), "Аннотация файла 1.", "the collection's file, Ли's")
			assert.Equal(t, []string{"Вариант 2 из 3 · пер. И. Ли"}, firstRow(t, &card))
		})
	}
}

// The inline picker pages cards: 21 cards of two files are a page of 20 and
// a page of 1, each result saying what its card holds.
func TestGroupedInlinePagination(t *testing.T) {
	v := newV2(t)
	for i := 1; i <= 21; i++ {
		v.variantCard(fmt.Sprintf("Рассказ %d", i), []fileSpec{
			{doc: fmt.Sprintf("story-%d-a", i), version: "1.0", translator: "Анна Бо", year: thisYear - 3},
			{doc: fmt.Sprintf("story-%d-b", i), version: "1.0", translator: "Ирина Ли", year: thisYear - 2},
		})
	}
	first := v.ask(ownerTG, sender, "рассказ", "")
	require.Len(t, first.Results, 20)
	assert.Equal(t, "20", first.call.Params["next_offset"])
	for _, r := range first.Results {
		assert.Contains(t, r.Description, "2 варианта", r.Title)
	}
	second := v.ask(ownerTG, sender, "рассказ", "20")
	require.Len(t, second.Results, 1, "21 cards, not 42 files")
	assert.Empty(t, second.call.Params["next_offset"])
	seen := map[string]bool{}
	for _, r := range append(first.Results, second.Results...) {
		assert.False(t, seen[r.Title], "%s listed once", r.Title)
		seen[r.Title] = true
	}
}

// A grouped answer names the card's default; a pick of another file of the
// card (an older answer's) opens the card on that file.
func TestAGroupedInlinePickOfAnotherFile(t *testing.T) {
	v := newV2(t)
	files := v.variantCard("Солярис", solaris)
	answer := v.ask(ownerTG, sender, "солярис", "")
	require.Len(t, answer.Results, 1, "one card")
	assert.Equal(t, "b"+strconv.FormatInt(files[2], 10), answer.Results[0].ID, "Ким's, the default")
	assert.Contains(t, answer.Results[0].Description, "3 варианта")

	v.pick(v.botID(), stackSiteURL+"/books/page/1?book_id="+strconv.FormatInt(files[0], 10))
	card := v.lastSend()
	assert.Contains(t, content(&card), "Аннотация файла 0.", "Бо's file")
	assert.Equal(t, []string{"Вариант 3 из 3 · пер. А. Бо"}, firstRow(t, &card))
}

// A search's page and tab buttons sent before the release run the search
// again, now grouped: a page past the new end opens the last one and says
// so, and the books tab counts cards.
func TestOldSearchButtonsAfterGrouping(t *testing.T) {
	v := newV2(t)
	v.plainCards()
	var specs []fileSpec
	for i := 0; i < 12; i++ {
		specs = append(specs, fileSpec{doc: fmt.Sprintf("twelve-%d", i), version: "1.0",
			translator: fmt.Sprintf("Переводчик %d", i), year: thisYear - 30 + i})
	}
	v.variantCard("Солярис", specs)
	off := *v
	off.stack = func() *stack { s := *v.stack; s.cardsOff = true; return s.start() }()
	off.say("солярис")
	results := off.lastSend()
	require.Contains(t, tgtest.Labels(t, &results), "✓ Книги · 12")
	off.tap(&results, screens.ButtonNext)
	results = off.latest(results.MessageID)
	off.tap(&results, screens.ButtonNext)
	results = off.latest(results.MessageID)
	require.Contains(t, content(&results), "Страница 3 из 3")

	v.tap(&results, screens.ButtonPrev)
	results = v.latest(results.MessageID)
	assert.Contains(t, tgtest.Labels(t, &results), "✓ Книги · 1")
	assert.Contains(t, content(&results), "12 вариантов")
	assert.NotContains(t, content(&results), "Страница")
	assert.Contains(t, v.answerTexts(), "Список изменился. Открыта последняя страница.")

	// A tab of a per-file results panel, pressed now: the books tab counts
	// cards.
	off.say("солярис")
	fresh := off.lastSend()
	require.Contains(t, tgtest.Labels(t, &fresh), "✓ Книги · 12")
	v.tap(&fresh, labelWith(t, &fresh, "Авторы · "))
	authors := v.latest(fresh.MessageID)
	assert.Contains(t, tgtest.Labels(t, &authors), "Книги · 1")
}
