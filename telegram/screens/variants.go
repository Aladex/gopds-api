package screens

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopds-api/models"
	"gopds-api/telegram/cbv2"
	"gopds-api/telegram/ru"
)

// A card's files as the site's selector shows them (VariantSelector.tsx,
// variantLabel.ts), in the bot's panel: the card's button, the list of its
// documents with each one's earlier revisions folded, and the list of one
// document's revisions. The words are the site's ru texts.

// VariantRow is one visible file of a card, in rank order: the file, its
// place in the card and the source fields its label is made of.
type VariantRow struct {
	Book       *models.Book
	Lineage    string
	Latest     bool
	Rank       int
	Tag        string
	DocVersion string
	// Default is the card's default file, the first by rank.
	Default         bool
	Publisher       string
	Translators     []string
	TranslatorsMore bool
}

// LabelField is a field a variant's label may name, in the label's order.
type LabelField int

// The label's fields, in the order a label lists them.
const (
	FieldTranslator LabelField = iota
	FieldPublisherYear
	FieldTag
	FieldRevision
	FieldAdded
)

var labelFields = []LabelField{FieldTranslator, FieldPublisherYear, FieldTag, FieldRevision, FieldAdded}

// The site's names of the fields, for «Отличаются: …».
var fieldNames = map[LabelField]string{
	FieldTranslator:    "перевод",
	FieldPublisherYear: "издательство и год",
	FieldTag:           "пометка",
	FieldRevision:      "номер редакции",
	FieldAdded:         "дата добавления",
}

// The site's words of the variant UI (ru translation.json).
const (
	copyVariantsTitle  = "Варианты"
	copyRevisionsTitle = "Редакции"
	copyVariantDefault = "основной"
	copyVariantsDiffer = "Отличаются: "
	// copyShown marks the file the card shows: the site's check mark.
	copyShown = "✓"
	// ButtonToVariants goes back from one document's revisions to the card's
	// whole list.
	ButtonToVariants = "← К вариантам"
	// variantButtonRunes bounds the card's variant button.
	variantButtonRunes = 32
	// variantRowRunes bounds a row's button in the variant list.
	variantRowRunes = 32
)

var (
	docYear     = regexp.MustCompile(`\b(\d{4})\b`)
	isoDay      = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})`)
	titleBreaks = regexp.MustCompile(`[^\p{L}\p{N}]+`)
)

// yearOfDoc is the year a document date names: its first four-digit run.
func yearOfDoc(docdate string) string {
	if m := docYear.FindStringSubmatch(docdate); m != nil {
		return m[1]
	}
	return ""
}

// calendarDay is the site's formatCalendarDay: an ISO date as dd.MM.yyyy,
// any other non-empty value as it is.
func calendarDay(value string) string {
	trimmed := strings.TrimSpace(value)
	if m := isoDay.FindStringSubmatch(trimmed); m != nil {
		return m[3] + "." + m[2] + "." + m[1]
	}
	return trimmed
}

// addedDay is the day the file was added, as the site shows it.
func addedDay(b *models.Book) string {
	if b.RegisterDate.IsZero() {
		return ""
	}
	return b.RegisterDate.Format("02.01.2006")
}

// ShortName is a name as a label shortens it: initials and the surname, the
// last word taken as the surname; one word, and initials already written as
// initials, stay as they are.
func ShortName(name string) string {
	words := strings.Fields(name)
	if len(words) <= 1 {
		return strings.Join(words, "")
	}
	out := make([]string, 0, len(words))
	for _, w := range words[:len(words)-1] {
		if strings.HasSuffix(w, ".") {
			out = append(out, w)
			continue
		}
		r, _ := utf8.DecodeRuneInString(w)
		out = append(out, string(r)+".")
	}
	return strings.Join(append(out, words[len(words)-1]), " ")
}

// fieldKey is what rows are compared by for a field.
func fieldKey(row *VariantRow, f LabelField) string {
	switch f {
	case FieldTranslator:
		more := "0"
		if row.TranslatorsMore {
			more = "1"
		}
		return strings.Join(row.Translators, "\x1f") + "|" + more
	case FieldPublisherYear:
		return strings.TrimSpace(row.Publisher) + "|" + yearOfDoc(row.Book.DocDate)
	case FieldTag:
		return row.Tag
	case FieldRevision:
		return row.DocVersion + "|" + calendarDay(row.Book.DocDate)
	case FieldAdded:
		return addedDay(row.Book)
	}
	return ""
}

// DifferingFields are the fields whose values differ among the rows, in the
// label's order. The revision tells files apart only inside one document.
func DifferingFields(rows []VariantRow) []LabelField {
	var out []LabelField
	for _, f := range labelFields {
		if f == FieldRevision {
			byLineage := map[string]map[string]bool{}
			differ := false
			for i := range rows {
				keys := byLineage[rows[i].Lineage]
				if keys == nil {
					keys = map[string]bool{}
					byLineage[rows[i].Lineage] = keys
				}
				keys[fieldKey(&rows[i], f)] = true
				if len(keys) > 1 {
					differ = true
				}
			}
			if differ {
				out = append(out, f)
			}
			continue
		}
		seen := map[string]bool{}
		for i := range rows {
			seen[fieldKey(&rows[i], f)] = true
		}
		if len(seen) > 1 {
			out = append(out, f)
		}
	}
	return out
}

// FieldText is one field of a row as its label says it; "" for nothing.
func FieldText(row *VariantRow, f LabelField) string {
	switch f {
	case FieldTranslator:
		var names []string
		for _, n := range row.Translators {
			if s := ShortName(n); s != "" {
				names = append(names, s)
			}
		}
		if len(names) == 0 {
			return ""
		}
		team := strings.Join(names, ", ")
		if row.TranslatorsMore {
			team += andOthers
		}
		return "пер. " + team
	case FieldPublisherYear:
		return joinNonEmpty(", ", strings.TrimSpace(row.Publisher), yearOfDoc(row.Book.DocDate))
	case FieldTag:
		if row.Tag == "" {
			return ""
		}
		return "[" + row.Tag + "]"
	case FieldRevision:
		var version, day string
		if row.DocVersion != "" {
			version = "ред. " + row.DocVersion
		}
		if d := calendarDay(row.Book.DocDate); d != "" {
			day = "файл " + d
		}
		return joinNonEmpty(" · ", version, day)
	case FieldAdded:
		if d := addedDay(row.Book); d != "" {
			return "добавлено " + d
		}
	}
	return ""
}

func joinNonEmpty(sep string, parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}

// titleKey is a title as the card compares it: case, ё, punctuation and
// spacing aside.
func titleKey(title string) string {
	t := strings.ReplaceAll(strings.ToLower(title), "ё", "е")
	return strings.TrimSpace(titleBreaks.ReplaceAllString(t, " "))
}

// VariantLabel is a row's label: the fields that differ in its card, or its
// added date when nothing does, and «как «…»» when the file carries a title
// of its own.
func VariantLabel(row *VariantRow, fields []LabelField, cardTitle string) string {
	var parts []string
	for _, f := range fields {
		if s := FieldText(row, f); s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		if added := FieldText(row, FieldAdded); added != "" {
			parts = append(parts, added)
		}
	}
	if titleKey(row.Book.Title) != titleKey(cardTitle) {
		parts = append(parts, "как «"+row.Book.Title+"»")
	}
	return strings.Join(parts, " · ")
}

// LabelLines is a label as two lines: what tells the file apart, and the
// rest of it.
func LabelLines(label string) (first, rest string) {
	if i := strings.Index(label, " · "); i >= 0 {
		return label[:i], label[i+len(" · "):]
	}
	return label, ""
}

// Lineage is one document of a card: its newest file and the earlier
// revisions under it.
type Lineage struct {
	Head  *VariantRow
	Older []*VariantRow
}

// CardVariants is a card of more than one visible file, shown on one of
// them.
type CardVariants struct {
	// Rows are the card's visible files in rank order.
	Rows    []VariantRow
	ShownID int64
}

// Kind is the site's kind of the card: revisions when every file is one
// document, variants otherwise.
func (v *CardVariants) Kind() string {
	kind, _ := models.VariantsKind(len(v.Rows), len(v.Lineages()))
	return kind
}

// Count is how many the card says it holds: documents, or files of one.
func (v *CardVariants) Count() int {
	_, count := models.VariantsKind(len(v.Rows), len(v.Lineages()))
	return count
}

func (v *CardVariants) revisions() bool { return v.Kind() == models.BookVariantsRevisions }

// Lineages are the card's documents in rank order of their newest files.
func (v *CardVariants) Lineages() []Lineage {
	var out []Lineage
	at := map[string]int{}
	for i := range v.Rows {
		row := &v.Rows[i]
		if j, ok := at[row.Lineage]; ok {
			out[j].Older = append(out[j].Older, row)
			continue
		}
		at[row.Lineage] = len(out)
		out = append(out, Lineage{Head: row})
	}
	return out
}

// Fields are what the card's labels say: a revision number for revisions,
// the differing fields but the revision for variants.
func (v *CardVariants) Fields() []LabelField {
	if v.revisions() {
		return []LabelField{FieldRevision}
	}
	var out []LabelField
	for _, f := range DifferingFields(v.Rows) {
		if f != FieldRevision {
			out = append(out, f)
		}
	}
	return out
}

// Title is the card's title, the default file's: what «как «…»» compares
// with.
func (v *CardVariants) Title() string {
	for i := range v.Rows {
		if v.Rows[i].Default {
			return v.Rows[i].Book.Title
		}
	}
	return v.Rows[0].Book.Title
}

// Label is a row's label under the card's fields.
func (v *CardVariants) Label(row *VariantRow) string {
	return VariantLabel(row, v.Fields(), v.Title())
}

func (v *CardVariants) shown() *VariantRow {
	for i := range v.Rows {
		if v.Rows[i].Book.ID == v.ShownID {
			return &v.Rows[i]
		}
	}
	return nil
}

// Position is where the shown file stands in its card, from 1: its place
// among the files for revisions, its document's place for variants.
func (v *CardVariants) Position() int {
	if v.revisions() {
		for i := range v.Rows {
			if v.Rows[i].Book.ID == v.ShownID {
				return i + 1
			}
		}
		return 1
	}
	for i, l := range v.Lineages() {
		if l.Head.Book.ID == v.ShownID {
			return i + 1
		}
		for _, o := range l.Older {
			if o.Book.ID == v.ShownID {
				return i + 1
			}
		}
	}
	return 1
}

// countText is «6 вариантов» or «22 редакции».
func (v *CardVariants) countText() string {
	if v.revisions() {
		return ru.Revisions(v.Count())
	}
	return ru.Variants(v.Count())
}

// ButtonText is the card's variant button, the site's phone chip: the
// count on the default file; «Вариант 2 из 6 · пер. Н. Прохорова» (or
// «Редакция 3 из 22 · ред. 2.0») on another, the label cut so that the
// place is never cut.
func (v *CardVariants) ButtonText() string {
	shown := v.shown()
	if shown == nil || shown.Default {
		return capitalize(v.countText())
	}
	word := "Вариант"
	if v.revisions() {
		word = "Редакция"
	}
	place := word + " " + strconv.Itoa(v.Position()) + " из " + strconv.Itoa(v.Count())
	first, _ := LabelLines(v.Label(shown))
	if first == "" {
		return place
	}
	room := variantButtonRunes - utf8.RuneCountInString(place) - utf8.RuneCountInString(" · ")
	if room <= 1 {
		return place
	}
	return place + " · " + ru.Truncate(first, room)
}

func capitalize(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

// VariantsList is one page of a card's variant list: the whole card, or one
// document's files when Lineage names it.
type VariantsList struct {
	Card *CardVariants
	// Lineage narrows the list to one document's files; "" is the card.
	Lineage string
	Page    int
}

// VariantsKeys are the variant list's envelopes.
type VariantsKeys struct {
	// Pick opens the card on a file.
	Pick func(bookID int64) *cbv2.Envelope
	// Fold opens one document's revisions, named by its newest file.
	Fold func(headID int64) *cbv2.Envelope
	Page func(page int) *cbv2.Envelope
	Back *Button
	Home *cbv2.Envelope
}

// variantItem is one row of a variant list: its place, its label, the
// file it opens and, for a document with earlier revisions, its fold.
type variantItem struct {
	n     int
	row   *VariantRow
	label string
	fold  *Lineage
}

// items are the list's rows: a document's newest file per row with its fold
// for the card, every file of the document for one document.
func (l *VariantsList) items() []variantItem {
	v := l.Card
	var out []variantItem
	for i, lin := range v.Lineages() {
		if l.Lineage == "" {
			it := variantItem{n: i + 1, row: lin.Head, label: v.Label(lin.Head)}
			if len(lin.Older) > 0 {
				lin := lin
				it.fold = &lin
			}
			out = append(out, it)
			continue
		}
		if lin.Head.Lineage != l.Lineage {
			continue
		}
		files := append([]*VariantRow{lin.Head}, lin.Older...)
		for j, row := range files {
			out = append(out, variantItem{n: j + 1, row: row,
				label: VariantLabel(row, []LabelField{FieldRevision}, v.Title())})
		}
	}
	return out
}

// VariantPages is how many pages the list fills.
func (l *VariantsList) VariantPages() int { return Pages(len(l.items())) }

// earlierRevisions is the site's fold: «ещё 2 ранние редакции».
func earlierRevisions(n int) string {
	return "ещё " + strconv.Itoa(n) + " " +
		ru.Plural(n, "ранняя редакция", "ранние редакции", "ранних редакций")
}

// VariantsScreen renders a page of a variant list.
func VariantsScreen(l *VariantsList, k *VariantsKeys) Screen {
	v := l.Card
	title := copyVariantsTitle
	if v.revisions() || l.Lineage != "" {
		title = copyRevisionsTitle
	}
	items := l.items()
	head := []Block{Title{Text: title}, Line{Text: ru.Truncate(v.Title(), titleRunes), Italic: true}}
	if l.Lineage == "" {
		head = append(head, Line{Text: v.countText()}, Line{Text: differLine(v.Fields())})
	} else {
		head = append(head, Line{Text: ru.Revisions(len(items))})
	}
	head = append(head, Gap{})
	pages := Pages(len(items))
	page := min(l.Page, pages-1)
	start := page * PageSize
	end := min(start+PageSize, len(items))
	plain, keys, listItems := l.rows(items[start:end], k)
	plain = append(append([]Block{}, head...), plain...)
	if pages > 1 {
		plain = append(plain, Gap{}, Line{Text: "Страница " + strconv.Itoa(page+1) + " из " + strconv.Itoa(pages)})
	}
	if row := pagingRow(page, pages, k.Page); len(row) > 0 {
		keys = append(keys, row)
	}
	last := lastRow(k.Back, k.Home)
	rich := append(append([]Block{}, head...), List{Items: listItems})
	rich = append(rich, pagerBlock(page, pages, k.Page)...)
	return Screen{Doc: Doc{Blocks: plain}, Keys: append(keys, last),
		Rich: &Screen{Doc: Doc{Blocks: rich}, Keys: [][]Button{last}}}
}

// rows renders a page's items: their lines, their buttons and their rich
// list items.
func (l *VariantsList) rows(items []variantItem, k *VariantsKeys) (plain []Block, keys [][]Button, list []Item) {
	v := l.Card
	list = make([]Item, 0, len(items))
	for i := range items {
		it := &items[i]
		first, sub := LabelLines(it.label)
		if it.row.Default && l.Lineage == "" {
			sub = joinNonEmpty(" · ", sub, copyVariantDefault)
		}
		line := strconv.Itoa(it.n) + ". " + joinNonEmpty(" · ", first, sub)
		prefix := strconv.Itoa(it.n) + ". "
		if it.row.Book.ID == v.ShownID {
			line += " " + copyShown
			prefix = copyShown + " " + prefix
			sub = joinNonEmpty(" · ", sub, copyShown)
		}
		// The number and the mark are inside the button's limit: the label
		// is what is cut.
		button := prefix + ru.Truncate(first, variantRowRunes-utf8.RuneCountInString(prefix))
		plain = append(plain, Line{Text: ru.Truncate(line, listLineRunes)})
		keys = append(keys, []Button{{Text: button, Env: k.Pick(it.row.Book.ID)}})
		item := Item{Value: it.n, Link: Button{Text: ru.Truncate(first, listLineRunes), Env: k.Pick(it.row.Book.ID)},
			Sub: ru.Truncate(sub, listLineRunes)}
		if it.fold != nil {
			fold := earlierRevisions(len(it.fold.Older))
			plain = append(plain, Line{Text: "   " + fold})
			keys = append(keys, []Button{{Text: strconv.Itoa(it.n) + ". " + fold, Env: k.Fold(it.fold.Head.Book.ID)}})
			item.Action = &Button{Text: fold, Env: k.Fold(it.fold.Head.Book.ID)}
		}
		list = append(list, item)
	}
	return plain, keys, list
}

// differLine is the site's «Отличаются: …», the added date when nothing else
// differs.
func differLine(fields []LabelField) string {
	if len(fields) == 0 {
		fields = []LabelField{FieldAdded}
	}
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = fieldNames[f]
	}
	return copyVariantsDiffer + strings.Join(names, ", ")
}
