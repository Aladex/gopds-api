package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The pure part of the author summary: which titles, covers and genres a set
// of books gives, per language. The rows it is fed come from
// authorSummaryBooksSQL; its database tests are author_entity_summary_test.go.

var summaryNow = time.Now()

// summaryBook gives a plain book: one author, a cover, Russian, added an hour
// before summaryNow; id orders the rest.
func plainSummaryBook(id int64, title string) authorSummaryBook {
	return authorSummaryBook{ID: id, Title: title, Lang: "ru", Cover: true, Path: "p", Filename: title,
		RegisterDate: summaryNow.Add(-time.Hour), Authors: 1}
}

// allLanguages is the variant of every language of the books.
func allLanguages(t *testing.T, books []authorSummaryBook) authorSummaryVariant {
	t.Helper()
	got := composeAuthorSummaries(books)
	require.NotEmpty(t, got)
	require.Equal(t, authorSummaryLangAll, got[0].Lang)
	return got[0]
}

func TestBooksOfOneOrTwoAuthorsComeBeforeBooksOfMany(t *testing.T) {
	many := plainSummaryBook(1, "Сборник друзей")
	many.Authors, many.RegisterDate, many.Favorites = 3, summaryNow, 50
	two := plainSummaryBook(2, "Вдвоём")
	two.Authors = 2
	one := plainSummaryBook(3, "Один")
	one.RegisterDate = summaryNow.Add(-2 * time.Hour)
	got := allLanguages(t, []authorSummaryBook{many, two, one}).Works
	assert.Equal(t, []string{"Вдвоём", "Один", "Сборник друзей"}, got,
		"a third author outweighs both favorites and age")
}

func TestFavoritesComeBeforeNewerBooks(t *testing.T) {
	newer := plainSummaryBook(1, "Новая")
	newer.RegisterDate = summaryNow
	liked := plainSummaryBook(2, "Любимая")
	liked.RegisterDate, liked.Favorites = summaryNow.Add(-48*time.Hour), 2
	less := plainSummaryBook(3, "Чуть любимая")
	less.RegisterDate, less.Favorites = summaryNow.Add(-24*time.Hour), 1
	assert.Equal(t, []string{"Любимая", "Чуть любимая", "Новая"},
		allLanguages(t, []authorSummaryBook{newer, less, liked}).Works)
}

func TestNewerBooksComeFirstAndEqualDatesAreStable(t *testing.T) {
	old := plainSummaryBook(1, "Старая")
	old.RegisterDate = summaryNow.Add(-72 * time.Hour)
	fresh := plainSummaryBook(2, "Свежая")
	fresh.RegisterDate = summaryNow
	tiedLow, tiedHigh := plainSummaryBook(3, "Ничья три"), plainSummaryBook(4, "Ничья четыре")
	assert.Equal(t, []string{"Свежая", "Ничья четыре", "Ничья три", "Старая"},
		allLanguages(t, []authorSummaryBook{old, tiedLow, fresh, tiedHigh}).Works)
}

func TestMagazinesAndAnthologiesGoLastWhateverElseTheyHave(t *testing.T) {
	magazine := plainSummaryBook(1, "Просто название")
	magazine.Collected, magazine.Favorites, magazine.RegisterDate = true, 9, summaryNow
	byTitle := plainSummaryBook(2, "Если, 2009 № 12")
	byTitle.Favorites = 9
	book := plainSummaryBook(3, "Солярис")
	book.Authors, book.RegisterDate = 2, summaryNow.Add(-99*time.Hour)
	many := plainSummaryBook(4, "Рассказы разных лет")
	many.Authors = 7
	assert.Equal(t, []string{"Солярис", "Рассказы разных лет", "Просто название", "Если, 2009 № 12"},
		allLanguages(t, []authorSummaryBook{magazine, byTitle, many, book}).Works,
		"a collected book falls behind even a book of many authors")
}

func TestTitlesThatReadAsCollectedVolumes(t *testing.T) {
	for _, title := range []string{"Журнал «Если» 2009 № 12", "Искатель, 1962 №2", "НФ: Альманах научной фантастики",
		"Детская библиотека. Том 66", "Том 1. 1955-1959", "Собрание сочинений в 10 т. Т. 3. Трудно быть богом",
		"Антология", "Психосексология: хрестоматия", "Цикл Романов \"Сан-Антонио\". Компиляция. Книги 1-30",
		"Выпуск 32", "2014_5_fb2", "The Best Anthology", "Vol. 3"} {
		assert.True(t, looksCollected(title), title)
	}
	for _, title := range []string{"Улитка на склоне [журнальный вариант]", "Солярис", "Томми", "Атом 5",
		"Жук в муравейнике (сборник)", "Хромая судьба"} {
		assert.False(t, looksCollected(title), title)
	}
}

func TestAPileHoldsDifferentTitles(t *testing.T) {
	var books []authorSummaryBook
	for i, title := range []string{"Хищные вещи века", "Хищные  вещи века [вариант 1965 г.]", "Хищные вещи века (вариант 1965 г.)",
		"ХИЩНЫЕ ВЕЩИ ВЕКА", "Трудно быть богом", "Град обреченный", "Град обречённый", "Волны гасят ветер", "Стажёры"} {
		b := plainSummaryBook(int64(100-i), title)
		books = append(books, b)
	}
	v := allLanguages(t, books)
	assert.Equal(t, []string{"Хищные вещи века", "Трудно быть богом", "Град обреченный", "Волны гасят ветер"}, v.Works)
	titles := []string{}
	for _, c := range v.Covers {
		titles = append(titles, c.Title)
	}
	assert.Equal(t, []string{"Хищные вещи века", "Трудно быть богом", "Град обреченный"}, titles)
}

func TestTitleTagsAreNotShown(t *testing.T) {
	for raw, want := range map[string]string{
		"Сказка о Тройке [сборник, тег]": "Сказка о Тройке",
		"Рыцарь (СИ)":              "Рыцарь",
		"Война (ЛП) [тег]":         "Война",
		"Мир Полудня (сборник)":    "Мир Полудня",
		"Парень из   преисподней ": "Парень из преисподней",
		"[тег]":                   "[тег]",
		"Марсианин (The Martian)": "Марсианин (The Martian)",
	} {
		assert.Equal(t, want, shownTitle(raw), raw)
	}
}

func TestABookWithNoWordsInItsTitleIsNoWorkAndKeepsItsCover(t *testing.T) {
	blank := plainSummaryBook(2, "  ...")
	blank.RegisterDate = summaryNow
	alsoBlank := plainSummaryBook(1, " ")
	v := allLanguages(t, []authorSummaryBook{alsoBlank, blank})
	assert.Empty(t, v.Works)
	assert.Len(t, v.Covers, 2, "two books with no title are not one title")
}

func TestOnlyBooksWithACoverReachThePile(t *testing.T) {
	bare := plainSummaryBook(3, "Без обложки")
	bare.Cover, bare.RegisterDate = false, summaryNow
	covered := plainSummaryBook(2, "С обложкой")
	v := allLanguages(t, []authorSummaryBook{bare, covered})
	assert.Equal(t, []string{"Без обложки", "С обложкой"}, v.Works, "a work needs no cover")
	require.Len(t, v.Covers, 1)
	assert.Equal(t, "С обложкой", v.Covers[0].Title)
}

func TestPilesAndListsAreBounded(t *testing.T) {
	var books []authorSummaryBook
	for i := 0; i < 9; i++ {
		b := plainSummaryBook(int64(i+1), "Книга "+string(rune('А'+i)))
		b.GenreIDs, b.GenreTitles = []int64{int64(i + 1)}, []string{"Жанр " + string(rune('А'+i))}
		books = append(books, b)
	}
	v := allLanguages(t, books)
	assert.Len(t, v.Works, maxSummaryWorks)
	assert.Len(t, v.Covers, maxSummaryCovers)
	assert.Len(t, v.Genres, maxSummaryGenres)
	assert.Equal(t, 4, maxSummaryWorks)
	assert.Equal(t, 3, maxSummaryCovers)
	assert.Equal(t, 3, maxSummaryGenres)
}

func TestEachLanguageHasItsOwnVariantAndAllHoldsEveryBook(t *testing.T) {
	ru, en, none := plainSummaryBook(3, "Солярис"), plainSummaryBook(2, "Solaris"), plainSummaryBook(1, "Без языка")
	en.Lang, none.Lang = "en", ""
	got := composeAuthorSummaries([]authorSummaryBook{ru, en, none})
	byLang := map[string][]string{}
	for _, v := range got {
		byLang[v.Lang] = v.Works
	}
	assert.Equal(t, map[string][]string{
		"":   {"Солярис", "Solaris", "Без языка"},
		"ru": {"Солярис"},
		"en": {"Solaris"},
	}, byLang, "a book with no language is in the all-languages variant only: no filter can ask for it")
	assert.Equal(t, "", got[0].Lang, "the all-languages variant first")
}

func TestNoBooksNoVariants(t *testing.T) {
	assert.Empty(t, composeAuthorSummaries(nil))
}

func TestGenresRankByBooksAndCollectedBooksDoNotCountWhileOthersExist(t *testing.T) {
	sf := func(id int64) authorSummaryBook {
		b := plainSummaryBook(id, "Роман "+string(rune('А'+id)))
		b.GenreIDs, b.GenreTitles = []int64{20}, []string{"Фантастика"}
		return b
	}
	horror := plainSummaryBook(10, "Ужас")
	horror.GenreIDs, horror.GenreTitles = []int64{10, 20}, []string{"Ужасы", "Фантастика"}
	var mags []authorSummaryBook
	for i := int64(11); i < 18; i++ {
		m := plainSummaryBook(i, "Журнал "+string(rune('А'+i)))
		m.Collected, m.GenreIDs, m.GenreTitles = true, []int64{5}, []string{"Журнал"}
		mags = append(mags, m)
	}
	assert.Equal(t, []string{"Фантастика", "Ужасы"},
		allLanguages(t, append(mags, sf(1), sf(2), horror)).Genres)
	assert.Equal(t, []string{"Журнал"}, allLanguages(t, mags).Genres, "a magazine's only genre is still its genre")
}

func TestGenreTiesGoToTheLowerID(t *testing.T) {
	tieHigh := plainSummaryBook(4, "Четвёртая")
	tieHigh.GenreIDs, tieHigh.GenreTitles = []int64{8}, []string{"Я"}
	tieLow := plainSummaryBook(5, "Пятая")
	tieLow.GenreIDs, tieLow.GenreTitles = []int64{2}, []string{"Ю"}
	assert.Equal(t, []string{"Ю", "Я"}, allLanguages(t, []authorSummaryBook{tieHigh, tieLow}).Genres)
}

// The catalog has two genres called «Журнал»: a book carrying both is one book
// of that genre. Counted twice it would tie the detective and win on its ID.
func TestAGenreTitleCountsOncePerBookAndABlankOneNotAtAll(t *testing.T) {
	a := plainSummaryBook(1, "Первая")
	a.GenreIDs, a.GenreTitles = []int64{7, 9}, []string{"Журнал", "Журнал"}
	b := plainSummaryBook(2, "Вторая")
	b.GenreIDs, b.GenreTitles = []int64{8}, []string{"Детектив"}
	c := plainSummaryBook(3, "Третья")
	c.GenreIDs, c.GenreTitles = []int64{8, 1}, []string{"Детектив", ""}
	assert.Equal(t, []string{"Детектив", "Журнал"}, allLanguages(t, []authorSummaryBook{a, b, c}).Genres)
}
