package llmres

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopds-api/internal/authornorm"
	"gopds-api/internal/fb2sanitize"
	"gopds-api/internal/parser"
)

// ContextVersion versions the excerpt reader's byte contract: the same entry
// bytes under the same version always produce the same excerpt and SHA-256.
const ContextVersion = "authornorm-context-v1"

// Field caps of the excerpt contract (design doc section 2), in runes.
const (
	TitleMaxRunes       = 200
	AnnotationMaxRunes  = 1000
	PublishInfoMaxRunes = 400
	BodyStartMaxRunes   = 2000
	TotalMaxRunes       = 3600
)

// FB2 element local names the excerpt reader matches against.
const (
	elFictionBook = "FictionBook"
	elBody        = "body"
	elAuthor      = "author"
	elTranslator  = "translator"
	elBookName    = "book-name"
	elPublisher   = "publisher"
	elCity        = "city"
	elYear        = "year"
)

// Credit is one other credit of the same book, labeled with its role.
type Credit struct {
	Role string `json:"role"`
	Name string `json:"name"`
}

// BookContext is the bounded excerpt sent with a request as evidence: the
// book title, the other credits, the annotation, the publish info and the
// start of the first body, all as markup-free text with collapsed whitespace.
type BookContext struct {
	Title        string   `json:"title"`
	OtherCredits []Credit `json:"other_credits"`
	Annotation   string   `json:"annotation"`
	PublishInfo  string   `json:"publish_info"`
	BodyStart    string   `json:"body_start"`
}

// Payload is the canonical serialization of the excerpt: fixed field order,
// the context version inside, an empty credits array instead of null.
func (c *BookContext) Payload() []byte {
	credits := c.OtherCredits
	if credits == nil {
		credits = []Credit{}
	}
	payload := struct {
		Version      string   `json:"context_version"`
		Title        string   `json:"title"`
		OtherCredits []Credit `json:"other_credits"`
		Annotation   string   `json:"annotation"`
		PublishInfo  string   `json:"publish_info"`
		BodyStart    string   `json:"body_start"`
	}{ContextVersion, c.Title, credits, c.Annotation, c.PublishInfo, c.BodyStart}
	out, err := json.Marshal(payload)
	if err != nil {
		panic(err) // the payload shape is always marshalable
	}
	return out
}

// SHA256 is the excerpt's deterministic digest.
func (c *BookContext) SHA256() [32]byte { return sha256.Sum256(c.Payload()) }

// TotalLen is the excerpt's total size in runes, everything included.
func (c *BookContext) TotalLen() int {
	total := runeCount(c.Title) + runeCount(c.Annotation) + runeCount(c.PublishInfo) + runeCount(c.BodyStart)
	for _, credit := range c.OtherCredits {
		total += runeCount(credit.Name)
	}
	return total
}

func runeCount(s string) int { return utf8.RuneCountInString(s) }

// BuildContext reads the excerpt from the raw bytes of one FB2 entry. The
// charset resolver and the repair chain are exactly the parser's
// (DecodeToUTF8, then fb2sanitize.Apply), so the reader accepts the same
// books the parser accepts; a book that still does not parse yields an error
// and the job goes without an excerpt. The function is pure: the same bytes
// always give the same excerpt.
func BuildContext(fb2 []byte, excludeAuthorDisplay string) (BookContext, error) {
	decoded, err := parser.DecodeToUTF8(fb2)
	if err != nil {
		return BookContext{}, err
	}
	decoded = fb2sanitize.Apply(decoded)

	r := &contextReader{
		dec:     xml.NewDecoder(bytes.NewReader(decoded)),
		exclude: excludeAuthorDisplay,
	}
	r.dec.Strict = false
	r.dec.AutoClose = xml.HTMLAutoClose
	if err := r.run(); err != nil {
		return BookContext{}, err
	}
	r.finalize()
	ctx := r.ctx
	ctx.Title = truncateAtWord(ctx.Title, TitleMaxRunes)
	ctx.Annotation = truncateAtWord(ctx.Annotation, AnnotationMaxRunes)
	ctx.PublishInfo = truncateAtWord(ctx.PublishInfo, PublishInfoMaxRunes)
	ctx.BodyStart = truncateAtWord(ctx.BodyStart, BodyStartMaxRunes)
	// The whole excerpt is capped: body text is the elastic part, and a
	// pathological credits list is dropped from the tail. The body budget is
	// clamped at zero — an excess beyond the whole body empties the body
	// instead of reaching truncateAtWord as a negative slice bound.
	for ctx.TotalLen() > TotalMaxRunes && ctx.BodyStart != "" {
		excess := ctx.TotalLen() - TotalMaxRunes
		budget := max(runeCount(ctx.BodyStart)-excess, 0)
		ctx.BodyStart = truncateAtWord(ctx.BodyStart, budget)
	}
	for ctx.TotalLen() > TotalMaxRunes && len(ctx.OtherCredits) > 0 {
		ctx.OtherCredits = ctx.OtherCredits[:len(ctx.OtherCredits)-1]
	}
	return ctx, nil
}

// ErrNotFictionBook marks bytes that decode as text but carry no FictionBook
// root element — the excerpt reader does not judge repairs beyond the
// parser's own rescue policy, it simply refuses non-FB2 content.
var ErrNotFictionBook = errors.New("llmres: no FictionBook root element")

// contextReader is the streaming state of one excerpt read.
type contextReader struct {
	dec     *xml.Decoder
	exclude string

	ctx BookContext

	stack     []string // local names of the open elements
	rootSeen  bool     // a FictionBook root opened
	bodySeen  bool     // only the first body is read
	skipDepth int      // inside an image or binary element when > 0

	titleChunks      []string
	annotationChunks []string
	bodyChunks       []string

	creditRole       string // role of the contributor being read
	creditComponents []authornorm.SourceComponent
	publishChunks    map[string][]string // publish-info field name -> text chunks
}

// run streams the document and stops as soon as every field is full.
func (r *contextReader) run() error {
	for {
		tok, err := r.dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if len(r.stack) == 0 {
				r.rootSeen = true
				if t.Name.Local != elFictionBook {
					return ErrNotFictionBook
				}
			}
			r.start(t.Name.Local)
		case xml.EndElement:
			r.end()
		case xml.CharData:
			r.text(string(t))
		}
		if r.complete() {
			break
		}
	}
	if !r.rootSeen {
		return ErrNotFictionBook
	}
	return nil
}

// complete reports whether every capped field has collected enough: reading
// on would not change the excerpt, so the rest of the book is never decoded.
func (r *contextReader) complete() bool {
	return r.bodySeen && r.overCap(r.bodyChunks, BodyStartMaxRunes)
}

func (r *contextReader) overCap(chunks []string, limit int) bool {
	return runeCount(collapse(chunks)) > limit
}

// pathIs reports whether the open path equals the given path exactly; FB2
// paths are short and unambiguous.
func (r *contextReader) pathIs(names ...string) bool {
	if len(r.stack) != len(names) {
		return false
	}
	for i, n := range names {
		if r.stack[i] != n {
			return false
		}
	}
	return true
}

func (r *contextReader) inSubtree(names ...string) bool {
	if len(r.stack) < len(names) {
		return false
	}
	for i, n := range names {
		if r.stack[i] != n {
			return false
		}
	}
	return true
}

func (r *contextReader) start(local string) {
	parent := ""
	if len(r.stack) > 0 {
		parent = r.stack[len(r.stack)-1]
	}
	r.stack = append(r.stack, local)

	switch {
	case r.skipDepth > 0:
		r.skipDepth++
	case local == elBody && parent == elFictionBook && !r.bodySeen:
		r.bodySeen = true
	case local == elBody && parent == elFictionBook:
		r.skipDepth = 1 // a second body is not part of the excerpt
	case local == "image" || local == "binary":
		r.skipDepth = 1 // image and binary text is skipped wherever it appears
	case r.pathIs(elFictionBook, "description", "title-info", elAuthor),
		r.pathIs(elFictionBook, "description", "title-info", elTranslator):
		r.creditRole = local
		r.creditComponents = nil
	}
}

func (r *contextReader) end() {
	if len(r.stack) == 0 {
		return
	}
	if r.skipDepth > 0 {
		r.skipDepth--
		r.stack = r.stack[:len(r.stack)-1]
		return
	}
	switch {
	case r.pathIs(elFictionBook, "description", "title-info", elAuthor),
		r.pathIs(elFictionBook, "description", "title-info", elTranslator):
		r.finishCredit()
	}
	r.stack = r.stack[:len(r.stack)-1]
}

func (r *contextReader) text(data string) {
	if r.skipDepth > 0 || len(r.stack) == 0 {
		return
	}
	switch {
	case r.pathIs(elFictionBook, "description", "title-info", "book-title"):
		r.titleChunks = appendChunk(r.titleChunks, data, TitleMaxRunes)
	case r.inSubtree(elFictionBook, "description", "title-info", "annotation"):
		r.annotationChunks = appendChunk(r.annotationChunks, data, AnnotationMaxRunes)
	case r.inSubtree(elFictionBook, "description", "title-info", elAuthor),
		r.inSubtree(elFictionBook, "description", "title-info", elTranslator):
		r.creditText(data)
	case r.inSubtree(elFictionBook, "description", "publish-info"):
		r.publishText(data)
	case r.bodySeen && r.inBody():
		r.bodyChunks = appendChunk(r.bodyChunks, data, BodyStartMaxRunes)
	}
}

// inBody reports whether the current path sits inside the first body element.
func (r *contextReader) inBody() bool {
	for i, name := range r.stack {
		if name == elBody && i > 0 && r.stack[i-1] == elFictionBook {
			return true
		}
	}
	return false
}

// creditText routes contributor child text into the component being read.
func (r *contextReader) creditText(data string) {
	if len(r.stack) == 0 {
		return
	}
	local := r.stack[len(r.stack)-1]
	var kind authornorm.ComponentKind
	switch local {
	case "first-name":
		kind = authornorm.ComponentFirst
	case "middle-name":
		kind = authornorm.ComponentMiddle
	case "last-name":
		kind = authornorm.ComponentLast
	case "nickname":
		kind = authornorm.ComponentNickname
	default:
		return
	}
	// A child element's text may arrive in several chunks; they join raw, the
	// component is canonicalized when the credit closes.
	if n := len(r.creditComponents); n > 0 && r.creditComponents[n-1].Kind == kind {
		r.creditComponents[n-1].Value += data
		return
	}
	r.creditComponents = append(r.creditComponents, authornorm.SourceComponent{Kind: kind, Value: data})
}

// finishCredit closes one contributor element: the credit joins the excerpt
// unless it is the author the request is about.
func (r *contextReader) finishCredit() {
	role := r.creditRole
	components := r.creditComponents
	r.creditRole = ""
	r.creditComponents = nil
	v, err := authornorm.NewSourceValue(components)
	if err != nil {
		return // an all-empty contributor carries no name
	}
	if role == elAuthor && v.DisplayName() == r.exclude {
		return
	}
	r.ctx.OtherCredits = append(r.ctx.OtherCredits, Credit{Role: role, Name: v.DisplayName()})
}

// publishText collects the publish-info fields; the fixed field order is
// applied when the excerpt is assembled.
func (r *contextReader) publishText(data string) {
	if len(r.stack) == 0 {
		return
	}
	local := r.stack[len(r.stack)-1]
	switch local {
	case elBookName, elPublisher, elCity, elYear:
	default:
		return
	}
	if r.publishChunks == nil {
		r.publishChunks = map[string][]string{}
	}
	r.publishChunks[local] = append(r.publishChunks[local], data)
}

// assemblePublishInfo renders publish-info as one string in the fixed
// book-name, publisher, city, year order; chunks of one field collapse to
// single-spaced text.
func assemblePublishInfo(chunks map[string][]string) string {
	order := []string{elBookName, elPublisher, elCity, elYear}
	var out []string
	for _, name := range order {
		if text := collapse(chunks[name]); text != "" {
			out = append(out, text)
		}
	}
	return strings.Join(out, ", ")
}

// appendChunk appends a text chunk to a field's chunk list, stopping once the
// collapsed text already exceeds the cap — more text cannot change the
// truncated result.
func appendChunk(chunks []string, data string, limit int) []string {
	if runeCount(collapse(chunks)) > limit {
		return chunks
	}
	return append(chunks, data)
}

// collapse joins text chunks with single spaces and collapses every run of
// white space to one U+0020, trimmed at the edges.
func collapse(chunks []string) string {
	return strings.Join(strings.Fields(strings.Join(chunks, " ")), " ")
}

// truncateAtWord cuts a collapsed string to at most limit runes at the last
// word boundary inside the cap; when no boundary fits — a single word longer
// than the cap — it returns nothing rather than a partial word.
func truncateAtWord(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	head := r[:limit]
	lastSpace := -1
	for i, c := range head {
		if unicode.IsSpace(c) {
			lastSpace = i
		}
	}
	if lastSpace <= 0 {
		return ""
	}
	return string(head[:lastSpace])
}

// finalize assembles the collected chunks into the excerpt fields; it runs at
// the end of run via the caller.
func (r *contextReader) finalize() {
	r.ctx.Title = collapse(r.titleChunks)
	r.ctx.Annotation = collapse(r.annotationChunks)
	r.ctx.BodyStart = collapse(r.bodyChunks)
	r.ctx.PublishInfo = assemblePublishInfo(r.publishChunks)
}

// ContextRelevant is the relevance check of design doc section 2: the excerpt
// is sent only when at least one non-initial source token appears in it as a
// whole word, after case and ё folding.
func ContextRelevant(ctx *BookContext, tokens []Token) bool {
	var b strings.Builder
	b.WriteString(ctx.Title)
	b.WriteByte(' ')
	b.WriteString(ctx.Annotation)
	b.WriteByte(' ')
	b.WriteString(ctx.PublishInfo)
	b.WriteByte(' ')
	b.WriteString(ctx.BodyStart)
	for _, credit := range ctx.OtherCredits {
		b.WriteByte(' ')
		b.WriteString(credit.Name)
	}
	words := strings.Fields(authornorm.SearchKey(b.String()))
	for _, t := range tokens {
		if strings.IndexFunc(t.Text, unicode.IsLetter) < 0 {
			continue
		}
		if authornorm.IsInitialToken(t.Text) {
			continue
		}
		needle := strings.Fields(authornorm.SearchKey(t.Text))
		if len(needle) == 0 {
			continue
		}
		if containsPhrase(words, needle) {
			return true
		}
	}
	return false
}

// containsPhrase reports whether the folded needle words appear as a
// consecutive run in the excerpt's words.
func containsPhrase(words, needle []string) bool {
	for start := 0; start+len(needle) <= len(words); start++ {
		match := true
		for j := range needle {
			if words[start+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
