// Package screens renders the bot's v2 screens: their exact words and their
// buttons, from data the router has already read. Nothing here talks to the
// database or Telegram.
//
// A screen's content is a Doc, a list of blocks, rendered one of two ways:
// as HTML text for a plain panel, or as rich blocks for a rich one. The words
// and the buttons are the same either way; a panel keeps the way it was sent
// in (plan-ui.md §2.4).
package screens

import (
	"bytes"
	"html"
	"io"
	"strings"

	"gopds-api/telegram/cbv2"

	"github.com/go-telegram/bot/models"
)

// Doc is a screen's content.
type Doc struct {
	Blocks []Block
}

// Block is one part of a Doc.
type Block interface {
	plain(b *strings.Builder)
	rich() []models.InputRichBlock
}

// Title is the screen's heading.
type Title struct{ Text string }

// Line is one line of text.
type Line struct {
	Text   string
	Italic bool
}

// Gap separates groups of lines in plain text; rich blocks are spaced by the
// client.
type Gap struct{}

// Table is a list of labeled values: «Серия: …».
type Table struct{ Rows [][2]string }

// Details is text the reader opens: a collapsed quote in plain text, a
// details block in rich.
type Details struct {
	Summary    string
	Paragraphs []string
}

// Photo is a book's cover. FileID is a file Telegram already has; Data the
// bytes, uploaded when there is no FileID or Telegram refuses it. Plain text
// shows no photo.
type Photo struct {
	BookID int64
	FileID string
	Data   []byte
}

// How rich blocks are laid out: the title a level-2 heading, table cells
// aligned left and to the top.
const (
	titleSize = 2
	alignLeft = "left"
	alignTop  = "top"
)

// CoverAttachment is the attach:// name an uploaded cover goes by.
const CoverAttachment = "cover"

func (t Title) plain(b *strings.Builder) {
	b.WriteString("<b>")
	b.WriteString(html.EscapeString(t.Text))
	b.WriteString("</b>\n")
}

func (t Title) rich() []models.InputRichBlock {
	return []models.InputRichBlock{{Type: models.RichBlockTypeSectionHeading,
		InputRichBlockSectionHeading: &models.InputRichBlockSectionHeading{Text: text(t.Text), Size: titleSize}}}
}

func (l Line) plain(b *strings.Builder) {
	if l.Italic {
		b.WriteString("<i>" + html.EscapeString(l.Text) + "</i>\n")
		return
	}
	b.WriteString(html.EscapeString(l.Text))
	b.WriteString("\n")
}

func (l Line) rich() []models.InputRichBlock {
	t := text(l.Text)
	if l.Italic {
		t = models.RichText{Type: models.RichTextTypeItalic, RichTextItalic: &models.RichTextItalic{Text: text(l.Text)}}
	}
	return []models.InputRichBlock{paragraph(&t)}
}

func (Gap) plain(b *strings.Builder)      { b.WriteString("\n") }
func (Gap) rich() []models.InputRichBlock { return nil }

// A photo is only shown rich.
func (Photo) plain(*strings.Builder) {}

func (t Table) plain(b *strings.Builder) {
	for _, row := range t.Rows {
		b.WriteString(html.EscapeString(row[0] + ": " + row[1]))
		b.WriteString("\n")
	}
}

func (t Table) rich() []models.InputRichBlock {
	if len(t.Rows) == 0 {
		return nil
	}
	cells := make([][]models.RichBlockTableCell, 0, len(t.Rows))
	for _, row := range t.Rows {
		label, value := text(row[0]), text(row[1])
		cells = append(cells, []models.RichBlockTableCell{
			{Text: &label, IsHeader: true, Align: alignLeft, Valign: alignTop},
			{Text: &value, Align: alignLeft, Valign: alignTop},
		})
	}
	return []models.InputRichBlock{{Type: models.RichBlockTypeTable,
		InputRichBlockTable: &models.InputRichBlockTable{Cells: cells, IsCompact: true}}}
}

func (d Details) plain(b *strings.Builder) {
	b.WriteString("<blockquote expandable>")
	b.WriteString(html.EscapeString(strings.Join(d.Paragraphs, "\n")))
	b.WriteString("</blockquote>\n")
}

func (d Details) rich() []models.InputRichBlock {
	inner := make([]models.InputRichBlock, 0, len(d.Paragraphs))
	for _, p := range d.Paragraphs {
		t := text(p)
		inner = append(inner, paragraph(&t))
	}
	return []models.InputRichBlock{{Type: models.RichBlockTypeDetails,
		InputRichBlockDetails: &models.InputRichBlockDetails{Summary: text(d.Summary), Blocks: inner}}}
}

func (p Photo) rich() []models.InputRichBlock {
	photo := models.InputMediaPhoto{Media: p.FileID}
	if p.FileID == "" {
		photo = models.InputMediaPhoto{Media: "attach://" + CoverAttachment, MediaAttachment: readerOf(p.Data)}
	}
	return []models.InputRichBlock{{Type: models.RichBlockTypePhoto,
		InputRichBlockPhoto: &models.InputRichBlockPhoto{Photo: photo}}}
}

func (c Code) plain(b *strings.Builder) {
	b.WriteString(html.EscapeString(c.Prefix))
	b.WriteString("<code>" + html.EscapeString(c.Code) + "</code>")
	b.WriteString(html.EscapeString(c.Suffix))
	b.WriteString("\n")
}

func (c Code) rich() []models.InputRichBlock {
	parts := []models.RichText{}
	if c.Prefix != "" {
		parts = append(parts, text(c.Prefix))
	}
	parts = append(parts, models.RichText{Type: models.RichTextTypeCode, RichTextCode: &models.RichTextCode{Text: text(c.Code)}})
	if c.Suffix != "" {
		parts = append(parts, text(c.Suffix))
	}
	t := models.RichText{Array: parts}
	return []models.InputRichBlock{paragraph(&t)}
}

func text(s string) models.RichText { return models.RichText{PlainText: s} }

func paragraph(t *models.RichText) models.InputRichBlock {
	return models.InputRichBlock{Type: models.RichBlockTypeParagraph,
		InputRichBlockParagraph: &models.InputRichBlockParagraph{Text: *t}}
}

// Plain is the Doc as HTML text.
func (d Doc) Plain() string {
	var b strings.Builder
	for _, block := range d.Blocks {
		block.plain(&b)
	}
	return strings.TrimRight(b.String(), "\n")
}

// Encoder turns a button's envelope into its callback_data.
type Encoder func(*cbv2.Envelope) (string, error)

// pressable is a block holding buttons: rendered rich, it needs their
// callback_data.
type pressable interface {
	richWith(enc *encoding) []models.InputRichBlock
}

// encoding encodes the buttons of one rich rendering and keeps the first
// error.
type encoding struct {
	enc Encoder
	err error
}

// button is b as a rich message's button.
func (e *encoding) button(b *Button) models.RichMessageButton {
	if b.Disabled {
		// A disabled button carries no action and no style.
		return models.RichMessageButton{Text: text(b.Text), Disabled: &models.DisabledButton{}}
	}
	out := models.RichMessageButton{Text: text(b.Text), Style: b.Style, URL: b.URL}
	if b.Env != nil {
		data, err := e.enc(b.Env)
		if err != nil && e.err == nil {
			e.err = err
		}
		out.CallbackData = data
	}
	return out
}

// Rich is the Doc as a rich message; enc encodes the buttons in it.
func (d Doc) Rich(enc Encoder) (models.InputRichMessage, error) {
	e := &encoding{enc: enc}
	var blocks []models.InputRichBlock
	for _, block := range d.Blocks {
		if p, ok := block.(pressable); ok {
			blocks = append(blocks, p.richWith(e)...)
			continue
		}
		blocks = append(blocks, block.rich()...)
	}
	return models.InputRichMessage{Blocks: blocks}, e.err
}

// PlainLength is how long the plain text is after entity parsing — what
// Telegram's 4096 limit counts.
func (d Doc) PlainLength() int {
	var n int
	for _, block := range d.Blocks {
		var b strings.Builder
		block.plain(&b)
		n += len([]rune(html.UnescapeString(stripTags(b.String()))))
	}
	return n
}

func stripTags(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Button is one inline button: a v2 callback, a URL, or text to copy.
type Button struct {
	Text     string
	Env      *cbv2.Envelope
	URL      string
	Style    string
	CopyText string
	// SwitchInline puts «@bot » in the chat's input field: inline search.
	SwitchInline bool
	// Disabled is a button that does nothing when pressed: a state shown in
	// the keyboard, like a file on its way (plan.md P1-6). It carries no
	// action and no style.
	Disabled bool
}

// Screen is a rendered screen: content and keyboard.
type Screen struct {
	Doc  Doc
	Keys [][]Button
	// Rich is how a rich message shows the screen when that differs: a list
	// whose items, tabs and pages are buttons in the message, and a keyboard
	// left with the rest. nil shows Doc and Keys either way.
	Rich *Screen
}

// In is the screen as a panel of the given kind shows it.
func (s *Screen) In(rich bool) *Screen {
	if rich && s.Rich != nil {
		return s.Rich
	}
	return s
}

// Cover is the screen's cover, if it has one.
func (s *Screen) Cover() (Photo, bool) {
	for _, b := range s.Doc.Blocks {
		if p, ok := b.(Photo); ok {
			return p, true
		}
	}
	return Photo{}, false
}

// WithCover is the screen with its cover replaced.
func (s *Screen) WithCover(p Photo) Screen {
	out := Screen{Keys: s.Keys}
	for _, b := range s.Doc.Blocks {
		if _, ok := b.(Photo); ok {
			b = p
		}
		out.Doc.Blocks = append(out.Doc.Blocks, b)
	}
	return out
}

// WithoutPhoto is the screen with its cover left out.
func (s *Screen) WithoutPhoto() Screen {
	out := Screen{Keys: s.Keys}
	for _, b := range s.Doc.Blocks {
		if _, ok := b.(Photo); !ok {
			out.Doc.Blocks = append(out.Doc.Blocks, b)
		}
	}
	return out
}

func readerOf(data []byte) io.Reader { return bytes.NewReader(data) }
