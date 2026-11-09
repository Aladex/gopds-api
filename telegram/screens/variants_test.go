package screens

import (
	"fmt"
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

// The site's label rule (variantLabel.test.ts), ported: a label says only
// what differs inside its card. Every date is made from now.

const hobbit = "Хоббит, или Туда и обратно"

func daysAgo(n int) time.Time { return time.Now().AddDate(0, 0, -n) }

// isoDaysAgo is a document date n days back, as the catalog stores one.
func isoDaysAgo(n int) string { return daysAgo(n).Format(time.DateOnly) }

// shownDay is a date as a label shows it.
func shownDay(t time.Time) string { return t.Format("02.01.2006") }

type rowOpt func(*VariantRow)

func vrow(id int64, opts ...rowOpt) VariantRow {
	r := VariantRow{Book: &models.Book{ID: id, Title: hobbit, DocDate: isoDaysAgo(400), RegisterDate: daysAgo(30)},
		Lineage: "doc-" + strconv.FormatInt(id, 10), Latest: true, Rank: int(id), DocVersion: "1.0"}
	for _, o := range opts {
		o(&r)
	}
	return r
}

func translators(names ...string) rowOpt { return func(r *VariantRow) { r.Translators = names } }
func publisher(p string) rowOpt          { return func(r *VariantRow) { r.Publisher = p } }
func docdate(d string) rowOpt            { return func(r *VariantRow) { r.Book.DocDate = d } }
func lineage(l string) rowOpt            { return func(r *VariantRow) { r.Lineage = l } }
func version(v string) rowOpt            { return func(r *VariantRow) { r.DocVersion = v } }
func older() rowOpt                      { return func(r *VariantRow) { r.Latest = false } }
func tag(t string) rowOpt                { return func(r *VariantRow) { r.Tag = t } }
func title(t string) rowOpt              { return func(r *VariantRow) { r.Book.Title = t } }
func added(t time.Time) rowOpt           { return func(r *VariantRow) { r.Book.RegisterDate = t } }

func TestVariantLabel(t *testing.T) {
	rows := []VariantRow{
		vrow(1, translators("Кирилл Королёв"), publisher("АСТ"), docdate("2025"), lineage("a")),
		vrow(2, translators("Наталья Прохорова"), publisher("Миликон-пресс"), docdate("2005"), lineage("b")),
	}
	assert.Equal(t, []LabelField{FieldTranslator, FieldPublisherYear}, DifferingFields(rows))
	assert.Equal(t, "пер. Н. Прохорова · Миликон-пресс, 2005",
		VariantLabel(&rows[1], []LabelField{FieldTranslator, FieldPublisherYear}, hobbit))

	day := isoDaysAgo(5)
	revs := []VariantRow{vrow(1, version("2.1"), docdate(day)), vrow(2, version("2.0"), docdate(day), older(), lineage("doc-1"))}
	assert.Equal(t, []LabelField{FieldRevision}, DifferingFields(revs))
	assert.Equal(t, "ред. 2.1 · файл "+shownDay(daysAgo(5)), VariantLabel(&revs[0], []LabelField{FieldRevision}, hobbit))

	same := []VariantRow{vrow(1, lineage("x")), vrow(2, lineage("y"))}
	same[1].Book.RegisterDate = same[0].Book.RegisterDate
	assert.Empty(t, DifferingFields(same))
	assert.Equal(t, "добавлено "+shownDay(same[0].Book.RegisterDate), VariantLabel(&same[0], nil, hobbit))

	team := vrow(1, translators("Евгений Михайлович Солонович", "Илья Голенищев-Кутузов"))
	team.TranslatorsMore = true
	assert.Equal(t, "пер. Е. М. Солонович, И. Голенищев-Кутузов и др.", VariantLabel(&team, []LabelField{FieldTranslator}, hobbit))

	initials := vrow(1, translators("К. Королёв"))
	assert.Equal(t, "пер. К. Королёв", VariantLabel(&initials, []LabelField{FieldTranslator}, hobbit))

	own := vrow(1, title("Хоббит"), translators("Нора Галь"))
	assert.Equal(t, "пер. Н. Галь · как «Хоббит»", VariantLabel(&own, []LabelField{FieldTranslator}, hobbit))
	punct := vrow(1, title("Хоббит или туда и обратно"), translators("Нора Галь"))
	assert.Equal(t, "пер. Н. Галь", VariantLabel(&punct, []LabelField{FieldTranslator}, "Хоббит, или Туда и Обратно"))
	yo := vrow(1, title("Ёжик в тумане"), translators("Нора Галь"))
	assert.Equal(t, "пер. Н. Галь", VariantLabel(&yo, []LabelField{FieldTranslator}, "Ежик в тумане"))

	tagged := vrow(1, tag("ЛП"))
	assert.Equal(t, "[ЛП]", VariantLabel(&tagged, []LabelField{FieldTag}, hobbit))
	nothing := vrow(1, publisher("АСТ"), docdate(""))
	assert.Equal(t, "АСТ", VariantLabel(&nothing, []LabelField{FieldTranslator, FieldPublisherYear}, hobbit))
}

func TestDifferingFieldsRevisionOnlyInsideALineage(t *testing.T) {
	rows := []VariantRow{vrow(1, lineage("a"), version("1.0"), docdate("2001")), vrow(2, lineage("b"), version("2.3"), docdate("2015"))}
	assert.NotContains(t, DifferingFields(rows), FieldRevision)
	rows = append(rows, vrow(3, lineage("b"), version("2.2"), older()))
	assert.Contains(t, DifferingFields(rows), FieldRevision)

	order := []VariantRow{
		vrow(1, translators("А Б"), publisher("X"), tag("ЛП"), version("1"), added(daysAgo(2)), lineage("same")),
		vrow(2, translators("В Г"), publisher("Y"), tag("СИ"), version("2"), added(daysAgo(3)), lineage("same")),
	}
	assert.Equal(t, []LabelField{FieldTranslator, FieldPublisherYear, FieldTag, FieldRevision, FieldAdded}, DifferingFields(order))
}

// sixHobbits is a card of six documents, the third with two earlier
// revisions, shown on the file given.
func sixHobbits(shown int64) *CardVariants {
	names := []string{"Наталья Рахманова", "Наталья Прохорова", "Валентина Маторина", "Зинаида Бобырь", "Кирилл Королёв", "Мария Каменкович"}
	pubs := []string{"Детская литература", "Миликон-пресс", "Северо-Запад", "Время", "АСТ", "Терра"}
	var rows []VariantRow
	id := int64(1)
	for i := range names {
		r := vrow(id, translators(names[i]), publisher(pubs[i]), docdate(strconv.Itoa(1976+5*i)), lineage(fmt.Sprintf("l%d", i)))
		r.Rank = len(rows) + 1
		rows = append(rows, r)
		id++
		if i == 2 {
			for _, v := range []string{"0.9", "0.8"} {
				o := vrow(id, translators(names[i]), publisher(pubs[i]), docdate(strconv.Itoa(1976+5*i)), lineage("l2"), version(v), older())
				o.Rank = len(rows) + 1
				rows = append(rows, o)
				id++
			}
		}
	}
	rows[0].Default = true
	return &CardVariants{Rows: rows, ShownID: shown}
}

func TestVariantButtonText(t *testing.T) {
	assert.Equal(t, "6 вариантов", sixHobbits(1).ButtonText(), "the default file: the count")
	// 34 runes: the label is cut at 32, the place never.
	assert.Equal(t, "Вариант 2 из 6 · пер. Н. Прохор…", sixHobbits(2).ButtonText())
	short := sixHobbits(2)
	short.Rows[1].Translators = []string{"Нора Галь"}
	assert.Equal(t, "Вариант 2 из 6 · пер. Н. Галь", short.ButtonText())
	assert.Equal(t, 3, sixHobbits(4).Position(), "an earlier revision stands at its document's place")

	var revs []VariantRow
	for i := 0; i < 22; i++ {
		r := vrow(int64(i+1), lineage("one"), version(fmt.Sprintf("2.%d", 21-i)), docdate(isoDaysAgo(i)))
		r.Rank, r.Latest = i+1, i == 0
		revs = append(revs, r)
	}
	revs[0].Default = true
	assert.Equal(t, "22 редакции", (&CardVariants{Rows: revs, ShownID: 1}).ButtonText())
	assert.Equal(t, "Редакция 3 из 22 · ред. 2.19", (&CardVariants{Rows: revs, ShownID: 3}).ButtonText())

	long := sixHobbits(2)
	long.Rows[1].Translators = []string{"Анна " + strings.Repeat("Длиннофамильная", 3)}
	text := long.ButtonText()
	assert.LessOrEqual(t, utf8.RuneCountInString(text), 32)
	assert.True(t, strings.HasPrefix(text, "Вариант 2 из 6 · пер. А."), text)
	assert.True(t, strings.HasSuffix(text, "…"), text)
}

// keysOf is a screen's keyboard as labels.
func keysOf(s *Screen) [][]string {
	var out [][]string
	for _, row := range s.Keys {
		var r []string
		for _, b := range row {
			r = append(r, b.Text)
		}
		out = append(out, r)
	}
	return out
}

var testVariantsKeys = &VariantsKeys{
	Pick: func(id int64) *cbv2.Envelope { return &cbv2.Envelope{Op: cbv2.OpOpenBook, EntityID: uint64(id)} }, // #nosec G115 -- test ids
	Fold: func(id int64) *cbv2.Envelope {
		return &cbv2.Envelope{Op: cbv2.OpVariants, EntityID: uint64(id), Arg: 1} // #nosec G115 -- test ids
	},
	Page: func(p int) *cbv2.Envelope { return &cbv2.Envelope{Op: cbv2.OpVariants, Page: uint32(p)} }, // #nosec G115 -- small
	Back: &Button{Text: ButtonToBook, Env: env()},
	Home: env(),
}

func TestVariantsScreen(t *testing.T) {
	s := VariantsScreen(&VariantsList{Card: sixHobbits(2)}, testVariantsKeys)
	text := s.Doc.Plain()
	for _, want := range []string{
		"Варианты", hobbit, "6 вариантов", "Отличаются: перевод, издательство и год",
		"1. пер. Н. Рахманова · Детская литература, 1976 · основной",
		"2. пер. Н. Прохорова · Миликон-пресс, 1981 ✓",
		"3. пер. В. Маторина · Северо-Запад, 1986\n   ещё 2 ранние редакции",
		"Страница 1 из 2",
	} {
		assert.Contains(t, text, want)
	}
	assert.NotContains(t, text, "0.9", "earlier revisions are folded")
	keys := keysOf(&s)
	assert.Equal(t, []string{"1. пер. Н. Рахманова"}, keys[0])
	assert.Equal(t, []string{"✓ 2. пер. Н. Прохорова"}, keys[1])
	assert.Equal(t, []string{"3. ещё 2 ранние редакции"}, keys[3])
	assert.Equal(t, uint64(3), s.Keys[3][0].Env.EntityID, "the fold names its document's newest file")
	assert.Equal(t, []string{ButtonNext}, keys[len(keys)-2])
	assert.Equal(t, []string{ButtonToBook, ButtonHome}, keys[len(keys)-1])
	require.NotNil(t, s.Rich)

	page2 := VariantsScreen(&VariantsList{Card: sixHobbits(2), Page: 1}, testVariantsKeys)
	assert.Contains(t, page2.Doc.Plain(), "6. пер. М. Каменкович")

	narrow := VariantsScreen(&VariantsList{Card: sixHobbits(4), Lineage: "l2"}, testVariantsKeys)
	text = narrow.Doc.Plain()
	assert.Contains(t, text, "Редакции")
	assert.Contains(t, text, "3 редакции")
	assert.Contains(t, text, "1. ред. 1.0")
	assert.Contains(t, text, "2. ред. 0.9")
	assert.Contains(t, text, "3. ред. 0.8")
	assert.NotContains(t, text, "Прохорова", "one document only")
	assert.Equal(t, []string{"✓ 2. ред. 0.9"}, keysOf(&narrow)[1])
}

// Revisions of one document: one row and its fold, as the site.
func TestRevisionsCardFoldsUnderItsNewest(t *testing.T) {
	var revs []VariantRow
	for i := 0; i < 22; i++ {
		r := vrow(int64(i+1), lineage("one"), version(fmt.Sprintf("2.%d", 21-i)), docdate(isoDaysAgo(i)))
		r.Rank, r.Latest = i+1, i == 0
		revs = append(revs, r)
	}
	revs[0].Default = true
	s := VariantsScreen(&VariantsList{Card: &CardVariants{Rows: revs, ShownID: 1}}, testVariantsKeys)
	text := s.Doc.Plain()
	assert.Contains(t, text, "Редакции")
	assert.Contains(t, text, "22 редакции")
	assert.Contains(t, text, "Отличаются: номер редакции")
	assert.Contains(t, text, "ещё 21 ранняя редакция")
	assert.NotContains(t, text, "Страница", "one row and its fold")
}

// A card of several files has its variant button first; a single book's
// keyboard is as it was.
func TestCardKeysWithVariants(t *testing.T) {
	book := &models.Book{ID: 2, Title: hobbit, Lang: "ru"}
	keys := *testCardKeys
	keys.Variants = &cbv2.Envelope{Op: cbv2.OpVariants, EntityID: 2}
	single, _ := CardScreen(&Card{Book: book, DefaultFormat: FormatEPUB}, &keys)
	withRow, _ := CardScreen(&Card{Book: book, DefaultFormat: FormatEPUB, Variants: sixHobbits(2)}, &keys)
	assert.Equal(t, []string{"EPUB", "FB2", "MOBI", "ZIP"}, keysOf(&single)[0])
	assert.Equal(t, []string{"Вариант 2 из 6 · пер. Н. Прохор…"}, keysOf(&withRow)[0])
	assert.Equal(t, keys.Variants, withRow.Keys[0][0].Env)
	assert.Equal(t, keysOf(&single), keysOf(&withRow)[1:], "the rest of the keyboard is the single book's")
}
