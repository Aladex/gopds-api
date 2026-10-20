package screens

import (
	"fmt"
	"html"
	"strconv"
	"strings"

	"gopds-api/telegram/cbv2"

	"github.com/go-telegram/bot/models"
)

// The blocks only a rich message has: a list whose titles are the buttons
// that open them, rows of buttons inside the message, and a small line of
// secondary text. A list screen lays itself out with them in its Rich
// variant; its plain rendering keeps the text and keyboard of before.

// Footer is a small secondary line: a list's count, the filters in force.
// Plain text shows it as any other line.
type Footer struct{ Text string }

// Heading is a heading in a rich message and a line of plain text, where the
// screen has always had a line.
type Heading struct{ Text string }

// Buttons is a row of buttons inside the message: a list's tabs, its pages.
type Buttons struct{ Row []Button }

// List is a list of items, numbered by Value; a run of items without one
// is a bulleted list.
type List struct{ Items []Item }

// Item is one entry of a List.
type Item struct {
	// Value is the item's number, 0 for none.
	Value int
	// Link is the item's title, pressed to open it; without an envelope it
	// is only text.
	Link Button
	// Sub is the line under the title, "" for none.
	Sub string
	// Action follows Sub: what else the item does, nil for nothing.
	Action *Button
}

// The styles of buttons in a rich message.
const (
	stylePrimary = "primary"
	// styleLink shows a callback button as a link in the text.
	styleLink = "link"
	// listNumbers labels an ordered list's items with decimal numbers.
	listNumbers = "1"
	// The ends of a list's pager in the message.
	pagerPrev = "←"
	pagerNext = "→"
)

func (f Footer) plain(b *strings.Builder) {
	b.WriteString(html.EscapeString(f.Text))
	b.WriteString("\n")
}

func (f Footer) rich() []models.InputRichBlock {
	return []models.InputRichBlock{{Type: models.RichBlockTypeFooter,
		InputRichBlockFooter: &models.InputRichBlockFooter{Text: text(f.Text)}}}
}

func (h Heading) plain(b *strings.Builder) { Line{Text: h.Text}.plain(b) }

func (h Heading) rich() []models.InputRichBlock { return Title(h).rich() }

// A row of buttons in the message is the keyboard's business in plain text.
func (Buttons) plain(*strings.Builder)        {}
func (Buttons) rich() []models.InputRichBlock { return nil }

func (r Buttons) richWith(e *encoding) []models.InputRichBlock {
	if len(r.Row) == 0 {
		return nil
	}
	row := make([]models.RichMessageButton, 0, len(r.Row))
	for i := range r.Row {
		row = append(row, e.button(&r.Row[i]))
	}
	return []models.InputRichBlock{{Type: models.RichBlockTypeButtons,
		InputRichBlockButtons: &models.InputRichBlockButtons{Buttons: row}}}
}

func (l List) plain(b *strings.Builder) {
	for i := range l.Items {
		it := &l.Items[i]
		if it.Value > 0 {
			b.WriteString(strconv.Itoa(it.Value) + ". ")
		}
		b.WriteString(html.EscapeString(it.Link.Text))
		b.WriteString("\n")
		if it.Sub != "" {
			b.WriteString("   " + html.EscapeString(it.Sub) + "\n")
		}
	}
}

func (List) rich() []models.InputRichBlock { return nil }

// richWith is the items as lists: each run of numbered items an ordered
// list, each run of the others a bulleted one.
func (l List) richWith(e *encoding) []models.InputRichBlock {
	var out []models.InputRichBlock
	var run []models.InputRichBlockListItem
	numbered := false
	flush := func() {
		if len(run) > 0 {
			out = append(out, models.InputRichBlock{Type: models.RichBlockTypeList,
				InputRichBlockList: &models.InputRichBlockList{Items: run}})
		}
		run = nil
	}
	for i := range l.Items {
		it := &l.Items[i]
		if len(run) > 0 && numbered != (it.Value > 0) {
			flush()
		}
		numbered = it.Value > 0
		run = append(run, it.rich(e))
	}
	flush()
	return out
}

// rich is the item: its title a link, then its second line in italics with
// the action after it.
func (it *Item) rich(e *encoding) models.InputRichBlockListItem {
	title := text(it.Link.Text)
	if it.Link.Env != nil {
		link := it.Link
		link.Style = styleLink
		title = models.RichText{Type: models.RichTextTypeButton, RichTextButton: &models.RichTextButton{Button: e.button(&link)}}
	}
	blocks := []models.InputRichBlock{paragraph(&title)}
	var second []models.RichText
	if it.Sub != "" {
		second = append(second, models.RichText{Type: models.RichTextTypeItalic,
			RichTextItalic: &models.RichTextItalic{Text: text(it.Sub)}})
	}
	if it.Action != nil {
		if len(second) > 0 {
			second = append(second, text(" · "))
		}
		action := *it.Action
		action.Style = styleLink
		second = append(second, models.RichText{Type: models.RichTextTypeButton,
			RichTextButton: &models.RichTextButton{Button: e.button(&action)}})
	}
	if len(second) > 0 {
		t := models.RichText{Array: second}
		blocks = append(blocks, paragraph(&t))
	}
	item := models.InputRichBlockListItem{Blocks: blocks}
	if it.Value > 0 {
		item.Value, item.Type = it.Value, listNumbers
	}
	return item
}

// pagerBlock is a list's pages as a row in the message: «←», where it is,
// «→». The arrows alone keep the row short and apart from the keyboard's
// «← Назад». The ends stay in place, disabled, so the row does not move
// under the reader's finger; one page has no row.
func pagerBlock(page, pages int, env func(int) *cbv2.Envelope) []Block {
	if pages <= 1 {
		return nil
	}
	prev := Button{Text: pagerPrev, Disabled: true}
	if page > 0 {
		prev = Button{Text: pagerPrev, Env: env(page - 1)}
	}
	next := Button{Text: pagerNext, Disabled: true}
	if page+1 < pages {
		next = Button{Text: pagerNext, Env: env(page + 1)}
	}
	where := Button{Text: fmt.Sprintf("Страница %d из %d", page+1, pages), Disabled: true}
	return []Block{Buttons{Row: []Button{prev, where, next}}}
}
