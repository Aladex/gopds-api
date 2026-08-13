package converter

// preview_render.go renders one portion of a book into a self-contained HTML
// fragment that is safe to insert into the app DOM. The book file is
// untrusted input, so the renderer decides everything itself: the tag set is
// a whitelist, the attribute set is a per-tag whitelist, links survive only
// as fragments pointing inside the same portion, and images are references to
// resources the server serves, never payloads carried inline. CSP does not back this up —
// the app already allows unsafe-inline, so a miss here executes under the
// reader's session.

import (
	"fmt"
	"strings"
	"unicode"
)

// maxPreviewInlineDepth bounds the recursion of the inline renderer. The
// parser enforces its own limit, but the renderer is also reachable with
// hand-built trees (and the parser's limit could change); a renderer that
// trusts its caller is one refactor away from a stack overflow.
const maxPreviewInlineDepth = 32

// previewRender carries the per-chunk rendering context: which anchors exist
// in this portion, which notes travel with it, and what has already been
// emitted (duplicate source ids produce one anchor, first occurrence wins).
type previewRender struct {
	chunk  *PreviewChunk
	images PreviewImages
	policy PreviewPolicy

	noteIDs        map[string]string          // raw note id -> anchor id
	noteByID       map[string]*FB2BodySection // raw note id -> note
	anchors        map[string]string          // raw section/paragraph id -> anchor id
	emittedAnchors map[string]bool            // anchor ids already out (dedup, first occurrence wins)
	emittedNotes   map[string]bool            // raw note ids already inlined
	usedAnchors    map[string]bool            // anchor ids the draft bump must avoid

	// draft renders sizes, not output: every fragment link is assumed to
	// resolve, so the draft is never smaller than the final render of the
	// same block. Packing by draft sizes therefore never underfills a chunk.
	draft bool
}

// RenderChunkHTML renders one portion to a self-contained HTML fragment.
// The fragment's total size is bounded by the policy ceiling; exceeding it
// is an error, never a silent overflow.
func RenderChunkHTML(chunk *PreviewChunk, images PreviewImages, policy PreviewPolicy) (string, error) {
	if chunk == nil {
		return "", fmt.Errorf("fb2 preview: chunk is nil")
	}
	r := newPreviewRender(chunk, images, policy, false)

	anchors := chunk.anchorTable().byBlock
	var out strings.Builder
	for i, block := range chunk.blocks {
		out.WriteString(r.renderBlock(block, anchors[i]))
		out.WriteString(r.renderNotesAfter(block))
	}
	result := out.String()
	// One indivisible block is allowed to overflow; a portion holding several
	// is not. The distinction is the whole of the rule: without it the
	// ceiling stops meaning anything, and with a refusal on both a book with
	// one long paragraph cannot be read at all.
	if len(result) > policy.MaxChunkBytes && len(chunk.blocks) > 1 {
		return "", fmt.Errorf("%w: %d rendered bytes over the %d ceiling in a portion of %d blocks",
			ErrPreviewBlockTooLarge, len(result), policy.MaxChunkBytes, len(chunk.blocks))
	}
	return result, nil
}

func newPreviewRender(chunk *PreviewChunk, images PreviewImages, policy PreviewPolicy, draft bool) *previewRender {
	r := &previewRender{
		chunk:          chunk,
		images:         images,
		policy:         policy,
		noteIDs:        make(map[string]string),
		noteByID:       make(map[string]*FB2BodySection),
		emittedAnchors: make(map[string]bool),
		emittedNotes:   make(map[string]bool),
		usedAnchors:    make(map[string]bool),
		draft:          draft,
	}
	for _, note := range chunk.notes {
		if note == nil {
			continue
		}
		if key := anchorKey(note.ID); key != "" {
			if _, taken := r.noteIDs[key]; taken {
				continue // first occurrence wins, as for block anchors
			}
			r.noteIDs[key] = noteAnchorID(chunk.Index, key)
			r.noteByID[key] = note
			r.usedAnchors[r.noteIDs[key]] = true
		}
	}
	// The block anchor map comes from the chunk's single assignment pass
	// (aliased, never mutated here), so what a fragment link resolves to is
	// the very id the render emits for the target block.
	r.anchors = chunk.anchorTable().byID
	return r
}

// anchorTable is the assigned anchor of every block in a portion, plus the
// book-id lookup the fragment resolver uses. It is computed once per chunk.
type anchorTable struct {
	byBlock []string          // one entry per chunk block, "" where the block carries no anchor
	byID    map[string]string // normalized book id -> anchor of the first block carrying it
}

// anchorTable returns the portion's anchor assignment, computing it on first
// use. There is exactly one assignment pass per portion: the renderer emits
// these ids and Headings reports them, and neither re-derives anything.
func (c *PreviewChunk) anchorTable() *anchorTable {
	c.anchorOnce.Do(func() {
		c.anchorTab = collectBlockAnchors(c.blocks)
	})
	return c.anchorTab
}

// noteAnchorID is the chunk-local anchor of the note with the given
// normalized id. The format lives in one place so the renderer, the anchor
// assignment and the draft measurement cannot spell it differently.
func noteAnchorID(chunkIndex int, key string) string {
	return fmt.Sprintf("pv%d-note-%s", chunkIndex, key)
}

// collectBlockAnchors indexes the anchors the chunker already assigned. It
// distributes nothing: anchors are decided once, in flattenPreviewBlocks, and
// stored on the block. Anything here that invented an anchor would be a second
// authority, which is the defect this arrangement exists to prevent.
func collectBlockAnchors(blocks []chunkBlock) *anchorTable {
	byID := make(map[string]string)
	byBlock := make([]string, len(blocks))
	for i, block := range blocks {
		if block.anchor == "" {
			continue
		}
		byBlock[i] = block.anchor
		// Only the block that owns the book's id resolves references to it.
		// Deciding ownership from what happens to be in this portion would
		// make a repeated id the owner whenever chunking separated it from
		// the first occurrence, and a cross-reference would quietly point at
		// the wrong section instead of unwrapping as it does for any other
		// target in another portion.
		if !block.ownsID {
			continue
		}
		if key := anchorKey(blockRawID(block)); key != "" {
			byID[key] = block.anchor
		}
	}
	return &anchorTable{byBlock: byBlock, byID: byID}
}

// blockRawID returns the book's own id of a block, if any.
func blockRawID(block chunkBlock) string {
	if block.header != nil {
		return strings.TrimSpace(block.header.ID)
	}
	if block.para != nil {
		return strings.TrimSpace(block.para.ID)
	}
	return ""
}

// anchorKey is the one form a book-supplied id takes inside the renderer: the
// key of every map, and the text that goes into the emitted anchor.
//
// Keying on the raw id while emitting the sanitized one is what made `ab` and
// `a b` two distinct entries that both rendered as `pv0-ab` — two identical
// ids in one document, under an invariant that promised none. Normalising at
// the boundary, once, is what makes the promise true: after normalisation two
// such ids are the same anchor, and the first block carrying it wins.
func anchorKey(raw string) string {
	return sanitizeAnchorID(strings.TrimSpace(raw))
}

// sanitizeAnchorID strips whitespace and control characters from a
// book-supplied id. Everything else is kept (Cyrillic anchors are legitimate)
// and escaped on output.
func sanitizeAnchorID(raw string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return -1
		}
		return r
	}, raw)
}

// syntheticAnchorPrefix is the namespace for anchors invented for sections
// that carry no id. '!' is not allowed in a valid XML Name, so a well-formed
// FB2 id can never collide with this prefix. The collision resolution in
// the chunker still catches hostile or malformed input that somehow
// does.
const syntheticAnchorPrefix = "!auto"

// renderBlock renders one block: its own anchor (if this is the first block
// in the chunk carrying it), then the block markup. The anchor is decided by
// the caller — the final render passes the assigned value from the chunk's
// anchor table, the draft passes the block's pre-assigned anchor.
func (r *previewRender) renderBlock(block chunkBlock, anchor string) string {
	var out strings.Builder
	out.WriteString(r.renderAnchor(anchor))
	if block.header != nil {
		r.renderSectionHeader(&out, block.header, block.depth)
	} else if block.para != nil {
		out.WriteString(r.renderParagraph(block.para))
	}
	return out.String()
}

// renderAnchor emits the assigned anchor, once per value: two blocks
// carrying the same book id produce one anchor, first occurrence wins. In
// draft mode it always emits, because the draft must not undercount.
func (r *previewRender) renderAnchor(anchor string) string {
	if anchor == "" {
		return ""
	}
	if r.emittedAnchors[anchor] && !r.draft {
		return ""
	}
	r.emittedAnchors[anchor] = true
	return `<a id="` + escapeAttr(anchor) + `"></a>` + "\n"
}

func (r *previewRender) renderSectionHeader(out *strings.Builder, section *FB2BodySection, depth int) {
	if section.Title == "" {
		return
	}
	heading := fmt.Sprintf("h%d", clampHeading(depth))
	out.WriteString("<")
	out.WriteString(heading)
	out.WriteString(">")
	out.WriteString(escapeText(section.Title))
	out.WriteString("</")
	out.WriteString(heading)
	out.WriteString(">\n")
}

// renderParagraph renders a paragraph with the wrapper its kind demands.
// Anchors are emitted by renderBlock for top-level blocks only: paragraphs
// inside a note are not registered, and links pointing at them unwrap.
func (r *previewRender) renderParagraph(p *FB2Paragraph) string {
	if p == nil {
		return ""
	}
	if p.Kind == ParagraphKindTable && p.Table != nil {
		return r.renderTable(p.Table)
	}
	// Separators are contentless kinds: checked before the emptiness return,
	// or they could never render anything.
	if p.Kind == ParagraphKindPoemBreak {
		return `<div class="stanza"></div>` + "\n"
	}
	if p.Kind == ParagraphKindEmptyLine {
		return `<div class="emptyline"></div>` + "\n"
	}
	var content strings.Builder
	if len(p.Content) > 0 {
		r.renderInlineElements(&content, p.Content, 0)
	} else if strings.TrimSpace(p.Text) != "" {
		content.WriteString(escapeText(p.Text))
	}
	if strings.TrimSpace(content.String()) == "" {
		return ""
	}
	switch p.Kind {
	case ParagraphKindCite:
		return `<p class="cite">` + content.String() + "</p>\n"
	case ParagraphKindEpigraph:
		return `<p class="epigraph">` + content.String() + "</p>\n"
	case ParagraphKindTextAuthor:
		return `<p class="text-author">` + content.String() + "</p>\n"
	case ParagraphKindSubtitle:
		return `<p class="subtitle">` + content.String() + "</p>\n"
	case ParagraphKindPoem, ParagraphKindPoemLine:
		return `<p class="poem-line">` + content.String() + "</p>\n"
	case ParagraphKindImage:
		return `<div class="image">` + content.String() + "</div>\n"
	default:
		return "<p>" + content.String() + "</p>\n"
	}
}

// renderInlineElements renders the inline tree through the whitelist: text is
// escaped, known formatting tags pass, links keep only resolving fragments,
// unknown node types are unwrapped to their children, and anything deeper
// than the depth guard is dropped.
func (r *previewRender) renderInlineElements(out *strings.Builder, elements []*FB2InlineElement, depth int) {
	if depth > maxPreviewInlineDepth {
		return
	}
	for _, el := range elements {
		if el == nil {
			continue
		}
		switch el.Type {
		case InlineTypeText:
			out.WriteString(escapeText(el.Content))
		case InlineTypeStrong, InlineTypeEmphasis, InlineTypeCode, InlineTypeSup, InlineTypeSub:
			tag := inlineTag(el.Type)
			out.WriteString("<")
			out.WriteString(tag)
			out.WriteString(">")
			r.renderInlineElements(out, el.Children, depth+1)
			out.WriteString("</")
			out.WriteString(tag)
			out.WriteString(">")
		case InlineTypeLink:
			href := ""
			if el.Attrs != nil {
				href = el.Attrs["href"]
			}
			resolved := r.resolveFragmentHref(href)
			if resolved == "" {
				// The link leads out of the portion or nowhere at all: keep
				// the text, drop the tag.
				r.renderInlineElements(out, el.Children, depth+1)
				break
			}
			out.WriteString(`<a href="`)
			out.WriteString(escapeAttr(resolved))
			out.WriteString(`">`)
			r.renderInlineElements(out, el.Children, depth+1)
			out.WriteString("</a>")
		case InlineTypeBreak:
			out.WriteString("<br/>")
		case InlineTypeImage:
			out.WriteString(r.renderImage(el))
		default:
			r.renderInlineElements(out, el.Children, depth+1)
		}
	}
}

// resolveFragmentHref maps a book-supplied href to the only form allowed in
// the output: a fragment pointing at an anchor inside this same portion.
// Everything else — every scheme, every host, every cross-portion or dangling
// fragment — resolves to nothing, and the caller unwraps the link.
func (r *previewRender) resolveFragmentHref(href string) string {
	href = strings.TrimFunc(href, func(c rune) bool {
		return unicode.IsSpace(c) || unicode.IsControl(c)
	})
	if !strings.HasPrefix(href, "#") {
		return ""
	}
	key := anchorKey(href[1:])
	if anchor, ok := r.noteIDs[key]; ok {
		return "#" + anchor
	}
	if anchor, ok := r.anchors[key]; ok {
		return "#" + anchor
	}
	if r.draft && key != "" {
		// Draft sizes must not undercount: assume the fragment resolves in
		// its final form. The final render only ever shrinks it (to nothing).
		// bookAnchorFor is the same spelling the chunker used when it
		// assigned the anchor, so the draft measures the string the final
		// render will emit rather than a look-alike.
		return "#" + bookAnchorFor(key)
	}
	return ""
}

// renderImage renders an inline image reference as a link to a resource the
// server serves, never as the picture itself.
//
// Every decision about whether this payload may be shown was made once, when
// the PreviewImages set was built, and the renderer only consults the answer.
// That is deliberate: the previous form inlined a base64 data URL here, which
// re-encoded the same binary on every reference and in every draft measurement,
// and spent the portion's whole byte budget on one median illustration.
func (r *previewRender) renderImage(el *FB2InlineElement) string {
	href := ""
	if el.Attrs != nil {
		href = strings.TrimSpace(el.Attrs["href"])
	}
	id := strings.ToLower(strings.TrimPrefix(href, "#"))
	if id == "" {
		return imagePlaceholderHTML
	}
	src := r.images.URL(id)
	if src == "" {
		// Either the book references a binary it does not carry, or that
		// binary did not pass image policy. Both leave the reader an explicit
		// marker rather than a picture frame that will never fill.
		return imagePlaceholderHTML
	}
	alt := ""
	if el.Attrs != nil {
		alt = el.Attrs["alt"]
	}
	// Lazy by default: a reader who closes the portion at the first paragraph
	// should not have paid for the illustrations further down it.
	return `<img src="` + escapeAttr(src) + `" loading="lazy" alt="` + escapeAttr(alt) + `"/>`
}

// Table cell tag names, named once for the renderer and its tests.
const (
	tableCellTag   = "td"
	tableHeaderTag = "th"
)

// renderTable renders a simple table; cells go through the same inline
// whitelist as everything else.
func (r *previewRender) renderTable(table *FB2Table) string {
	if table == nil || len(table.Rows) == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteString(`<table class="table">` + "\n")
	for _, row := range table.Rows {
		if len(row) == 0 {
			continue
		}
		out.WriteString("  <tr>")
		for _, cell := range row {
			if cell == nil {
				continue
			}
			tag := tableCellTag
			if cell.Header {
				tag = tableHeaderTag
			}
			out.WriteString("<")
			out.WriteString(tag)
			out.WriteString(">")
			if len(cell.Content) > 0 {
				r.renderInlineElements(&out, cell.Content, 0)
			} else if strings.TrimSpace(cell.Text) != "" {
				out.WriteString(escapeText(cell.Text))
			}
			out.WriteString("</")
			out.WriteString(tag)
			out.WriteString(">")
		}
		out.WriteString("</tr>\n")
	}
	out.WriteString("</table>\n")
	return out.String()
}

// renderNotesAfter renders the footnotes first referenced by this block,
// right after it: notes expand in place, under the paragraph that cites them.
// A note cited again later in the portion links back to the same anchor.
func (r *previewRender) renderNotesAfter(block chunkBlock) string {
	if block.para == nil || len(r.noteByID) == 0 {
		return ""
	}
	var out strings.Builder
	for _, raw := range noteRefsInParagraph(block.para, r.noteByID) {
		if r.emittedNotes[raw] && !r.draft {
			continue
		}
		note := r.noteByID[raw]
		if note == nil {
			continue
		}
		r.emittedNotes[raw] = true
		out.WriteString(r.renderNote(note))
	}
	return out.String()
}

// renderNote renders one footnote as a marked div carrying its chunk-local
// anchor, its title when present, and its paragraphs.
func (r *previewRender) renderNote(note *FB2BodySection) string {
	var out strings.Builder
	out.WriteString(`<div class="preview-note" id="`)
	out.WriteString(escapeAttr(r.noteIDs[anchorKey(note.ID)]))
	out.WriteString(`">` + "\n")
	if title := strings.TrimSpace(note.Title); title != "" {
		out.WriteString(`<p class="preview-note-title">`)
		out.WriteString(escapeText(title))
		out.WriteString("</p>\n")
	}
	for _, p := range noteParagraphs(note) {
		out.WriteString(r.renderParagraph(p))
	}
	out.WriteString("</div>\n")
	return out.String()
}

// noteParagraphs collects a note's paragraphs, descending into nested
// sections: a note's text belongs to the note wherever the source put it.
func noteParagraphs(section *FB2BodySection) []*FB2Paragraph {
	var out []*FB2Paragraph
	var walk func(content []*FB2ContentItem)
	walk = func(content []*FB2ContentItem) {
		for _, item := range content {
			if item == nil {
				continue
			}
			if item.Paragraph != nil {
				out = append(out, item.Paragraph)
			}
			if item.Section != nil {
				walk(item.Section.Content)
			}
		}
	}
	if section != nil {
		walk(section.Content)
	}
	return out
}

// noteRefsInParagraph returns the raw note ids a paragraph references, in
// order of first appearance. References live in links whose fragment names a
// note from the chunk's set.
func noteRefsInParagraph(p *FB2Paragraph, notes map[string]*FB2BodySection) []string {
	var refs []string
	seen := make(map[string]bool)
	var walk func(elements []*FB2InlineElement)
	walk = func(elements []*FB2InlineElement) {
		for _, el := range elements {
			if el == nil {
				continue
			}
			if el.Type == InlineTypeLink && el.Attrs != nil {
				href := strings.TrimSpace(el.Attrs["href"])
				if raw := strings.TrimPrefix(href, "#"); raw != href && raw != "" {
					key := anchorKey(raw)
					if _, ok := notes[key]; ok && !seen[key] {
						seen[key] = true
						refs = append(refs, key)
					}
				}
			}
			walk(el.Children)
		}
	}
	if p == nil {
		return nil
	}
	walk(p.Content)
	if p.Table != nil {
		for _, row := range p.Table.Rows {
			for _, cell := range row {
				if cell != nil {
					walk(cell.Content)
				}
			}
		}
	}
	return refs
}
