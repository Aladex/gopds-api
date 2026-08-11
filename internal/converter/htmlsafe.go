package converter

// htmlsafe.go is the single place that answers "how is a string written into
// HTML" for both renderers this package ships (EPUB and preview). An escaping
// fix belongs here exactly once: the EPUB a reader downloads and the preview
// a reader sees in the browser are built from the same untrusted book.

import "html"

// escapeText escapes character data. Both renderers use it for every piece of
// book text that lands between tags.
//
// Its body is identical to escapeAttr's, and deliberately so rather than by
// oversight: html.EscapeString already escapes quotes, which is what an
// attribute needs on top of what text needs. The two names are kept apart
// because they record which context a call site is in, and an audit reads
// that. They would diverge the day either context needs more — an unquoted
// attribute, say, or a context where quotes must survive as themselves.
func escapeText(s string) string {
	return html.EscapeString(s)
}

// escapeAttr escapes an attribute value. Both renderers use it for every
// book-derived string that lands inside quotes: anchors, hrefs, alts.
func escapeAttr(s string) string {
	return html.EscapeString(s)
}

// htmlTagEm is the only tag name inlineTag rewrites; the rest pass through.
const htmlTagEm = "em"

// inlineTag maps an FB2 inline type to the HTML tag both renderers emit for
// it.
//
// It is a mapping, not a gate: what it is handed, it names. The whitelist
// lives in the switch at each call site, which reaches this function only for
// types it has already accepted. Said the other way round, because it once
// read as a guarantee here: passing an arbitrary string to this function
// returns that string, so a caller that stops filtering stops being safe.
func inlineTag(tag string) string {
	switch tag {
	case InlineTypeEmphasis:
		return htmlTagEm
	default:
		return tag
	}
}

// imagePlaceholderHTML marks a dropped image. An image can be missing,
// undecodable, over budget or forbidden (SVG) — in every case the reader gets
// an explicit marker, not a silent hole in the text.
const imagePlaceholderHTML = `<span class="fb2-image">[image]</span>`
