// Package fb2sanitize holds the byte-level repair chain shared by the FB2
// metadata scanner (internal/parser) and the converter (internal/converter).
// Both pipelines run the same repairs on decoded text before XML parsing;
// keeping one implementation here is the point of the package — duplicated
// copies of this code produced two production defects that were fixed in one
// copy and missed in the other. The FB2 rescue policy (charset resolution,
// root verification, fallbacks, structural balancing) stays with the callers:
// this package repairs bytes, it does not judge documents.
package fb2sanitize

import (
	"bytes"
	"strings"
)

// Apply runs the shared repair chain on decoded UTF-8 content. The order is
// part of the contract: stray brackets are escaped before any tag-shape
// repair looks for brackets, and end-tag repair runs after the self-closing
// repair that may emit new well-formed closings. Every entry point must call
// this function rather than a hand-picked subset — a partial chain is how the
// two halves of the system started answering differently on the same book.
func Apply(content []byte) []byte {
	content = controlChars(content)
	content = invalidTagOpenings(content)
	content = invalidProcessingInstructions(content)
	content = invalidAmpersands(content)
	content = xmlVersion(content)
	content = brokenSelfClosingTags(content)
	content = brokenEndTags(content)
	content = brokenLangTag(content)
	content = missingXlinkPrefix(content)
	return content
}

// asciiPrintableStart is the first non-control ASCII byte; anything below it
// (except tab, newline, carriage return) is invalid in XML 1.0.
const asciiPrintableStart = 0x20

// controlChars replaces control bytes (except tab, newline, carriage return)
// with spaces: they are invalid in XML 1.0 and abort the decoder.
func controlChars(content []byte) []byte {
	changed := false
	out := make([]byte, 0, len(content))
	for i := 0; i < len(content); i++ {
		b := content[i]
		if b == '\t' || b == '\n' || b == '\r' {
			out = append(out, b)
			continue
		}
		if b < asciiPrintableStart {
			out = append(out, ' ')
			changed = true
			continue
		}
		out = append(out, b)
	}
	if !changed {
		return content
	}
	return out
}

// invalidTagOpenings escapes a "<" that cannot start markup, so a stray
// bracket in prose does not abort the decoder.
func invalidTagOpenings(content []byte) []byte {
	changed := false
	out := make([]byte, 0, len(content))
	for i := 0; i < len(content); i++ {
		b := content[i]
		if b != '<' {
			out = append(out, b)
			continue
		}
		if i+1 >= len(content) || !isLikelyXMLTagStart(content[i+1]) {
			out = append(out, '&', 'l', 't', ';')
			changed = true
			continue
		}
		out = append(out, b)
	}
	if !changed {
		return content
	}
	return out
}

func isLikelyXMLTagStart(b byte) bool {
	switch b {
	case '/', '?', '!', '_':
		return true
	default:
		return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
	}
}

// invalidProcessingInstructions escapes "<?" sequences that are not the XML
// declaration: stray PI openers abort the decoder.
func invalidProcessingInstructions(content []byte) []byte {
	changed := false
	out := make([]byte, 0, len(content))
	for i := 0; i < len(content); i++ {
		if content[i] == '<' && i+1 < len(content) && content[i+1] == '?' {
			if i == 0 && hasPrefixFold(content, []byte("<?xml")) {
				out = append(out, '<', '?')
				i++
				continue
			}
			out = append(out, '&', 'l', 't', ';', '?')
			i++
			changed = true
			continue
		}
		out = append(out, content[i])
	}
	if !changed {
		return content
	}
	return out
}

func hasPrefixFold(data, prefix []byte) bool {
	if len(data) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		a := data[i]
		b := prefix[i]
		if a >= 'A' && a <= 'Z' {
			a = a - 'A' + 'a'
		}
		if b >= 'A' && b <= 'Z' {
			b = b - 'A' + 'a'
		}
		if a != b {
			return false
		}
	}
	return true
}

// invalidAmpersands escapes a bare "&" that does not open a valid entity.
func invalidAmpersands(content []byte) []byte {
	changed := false
	out := make([]byte, 0, len(content))
	for i := 0; i < len(content); i++ {
		if content[i] != '&' {
			out = append(out, content[i])
			continue
		}

		semi := -1
		for j := i + 1; j < len(content) && j-i <= 32; j++ {
			if content[j] == ';' {
				semi = j
				break
			}
		}
		if semi == -1 {
			out = append(out, '&', 'a', 'm', 'p', ';')
			changed = true
			continue
		}

		entity := content[i+1 : semi]
		if isValidEntity(entity) {
			out = append(out, content[i:semi+1]...)
			i = semi
			continue
		}

		out = append(out, '&', 'a', 'm', 'p', ';')
		changed = true
	}
	if !changed {
		return content
	}
	return out
}

func isValidEntity(entity []byte) bool {
	if len(entity) == 0 {
		return false
	}
	switch string(entity) {
	case "amp", "lt", "gt", "quot", "apos":
		return true
	}
	if entity[0] != '#' {
		return false
	}
	ref := entity[1:]
	if len(ref) > 0 && (ref[0] == 'x' || ref[0] == 'X') {
		return allHexDigits(ref[1:])
	}
	return allDecDigits(ref)
}

func allHexDigits(s []byte) bool {
	if len(s) == 0 {
		return false
	}
	for _, b := range s {
		switch {
		case b >= '0' && b <= '9', b >= 'a' && b <= 'f', b >= 'A' && b <= 'F':
		default:
			return false
		}
	}
	return true
}

// allDecDigits treats an empty slice as valid: the historical scanner
// accepted a bare "&#;" reference, and this refactor preserves behavior
// byte for byte.
func allDecDigits(s []byte) bool {
	for _, b := range s {
		if b < '0' || b > '9' {
			return false
		}
	}
	return true
}

// xmlVersion rewrites a non-1.0 version in the XML declaration: the decoder
// refuses anything else, and books declaring "1.1" exist.
func xmlVersion(content []byte) []byte {
	if len(content) == 0 {
		return content
	}

	declEnd := bytes.Index(content, []byte("?>"))
	if declEnd == -1 || declEnd > 200 {
		return content
	}
	decl := string(content[:declEnd])
	versionIdx := strings.Index(decl, "version=")
	if versionIdx == -1 {
		return content
	}

	versionIdx += len("version=")
	if versionIdx >= len(decl) {
		return content
	}

	quote := decl[versionIdx]
	if quote != '"' && quote != '\'' {
		return content
	}

	versionIdx++
	end := strings.IndexByte(decl[versionIdx:], quote)
	if end == -1 {
		return content
	}

	version := strings.TrimSpace(decl[versionIdx : versionIdx+end])
	if version == "1.0" {
		return content
	}

	newDecl := decl[:versionIdx] + "1.0" + decl[versionIdx+end:]
	return append([]byte(newDecl), content[declEnd:]...)
}

// brokenSelfClosingTags repairs a self-closing tag that lost its closing
// bracket. It looks back to confirm the slash really belongs to a tag: a
// slash in prose before markup -- "100 руб. / <emphasis>" -- is text, and
// rewriting it blindly put a literal "/>" into annotations and turned closing
// tags into opening ones.
// Handles cases like:
//   - <image .../</section> -> <image /></section>
//   - <image .../< -> <image /><
//   - <empty-line / <p> -> <empty-line /><p>
func brokenSelfClosingTags(content []byte) []byte {
	changed := false
	out := make([]byte, 0, len(content))

	for i := 0; i < len(content); i++ {
		if content[i] != '/' {
			out = append(out, content[i])
			continue
		}

		// Check if this is potentially a broken self-closing tag: "/" followed by whitespace or "<"
		if i+1 >= len(content) {
			out = append(out, content[i])
			continue
		}

		next := content[i+1]

		// Patterns: "/<" (immediately) or "/ <" (across whitespace) ->
		// convert to "/>". Both shapes share one path: for "/<" the
		// whitespace skip simply takes no steps.
		if next == '<' || isXMLSpace(next) {
			// Look back to find the opening < of the current tag
			if !isPartOfSelfClosingTag(content, i) {
				out = append(out, content[i])
				continue
			}

			// Skip whitespace between / and <
			j := i + 1
			for j < len(content) && isXMLSpace(content[j]) {
				j++
			}

			if j < len(content) && content[j] == '<' {
				out = append(out, '/', '>')
				changed = true

				// The closing tag that follows is kept. Dropping it used to
				// look like part of the damage, but it is usually a real
				// boundary: swallowing </section> turned two sibling sections
				// into a nested pair, which rewrites the book's structure
				// instead of repairing a missing bracket.
				i = j - 1
				continue
			}
		}

		out = append(out, content[i])
	}

	if !changed {
		return content
	}
	return out
}

// isPartOfSelfClosingTag checks if the "/" at position i is part of a self-closing tag
func isPartOfSelfClosingTag(content []byte, slashPos int) bool {
	// Look back to find the opening <
	openPos := -1
	for i := slashPos - 1; i >= 0 && i > slashPos-200; i-- {
		if content[i] == '<' {
			openPos = i
			break
		}
		if content[i] == '>' {
			// Found closing > before opening <, so this / is not part of a tag
			return false
		}
	}

	if openPos == -1 {
		return false
	}

	// Extract tag name
	nameStart := openPos + 1
	for nameStart < slashPos && isXMLSpace(content[nameStart]) {
		nameStart++
	}

	if nameStart >= slashPos {
		return false
	}

	// Check if this looks like an opening tag (not </ or <! or <?)
	if content[nameStart] == '/' || content[nameStart] == '!' || content[nameStart] == '?' {
		return false
	}

	// Tags that are typically self-closing in FB2
	tagName := extractTagNameAt(content, nameStart)
	switch strings.ToLower(tagName) {
	case "image", "empty-line", "br", "img":
		return true
	}

	// A tag carrying attributes is likely a malformed self-closing one.
	return hasAttributeBefore(content, nameStart, slashPos)
}

// hasAttributeBefore reports whether an attribute assignment appears between
// the tag name and the slash, which marks a tag that was probably self-closing
// before its bracket went missing. An attribute is whitespace, then a name,
// then '='; requiring that shape keeps "<foo=bar" from counting, and looking
// past the name is what the previous scan failed to do, so ordinary
// name="value" never matched and the repair covered only a four-tag whitelist.
func hasAttributeBefore(content []byte, nameStart, slashPos int) bool {
	for i := nameStart; i < slashPos; i++ {
		if !isXMLSpace(content[i]) {
			continue
		}
		j := i
		for j < slashPos && isXMLSpace(content[j]) {
			j++
		}
		nameLen := 0
		for j < slashPos && isXMLNameByte(content[j]) {
			j++
			nameLen++
		}
		for j < slashPos && isXMLSpace(content[j]) {
			j++
		}
		if nameLen > 0 && j < slashPos && content[j] == '=' {
			return true
		}
	}
	return false
}

// endTagOpenLen is the length of the "</" end-tag opener.
const endTagOpenLen = len("</")

// brokenEndTags closes an end tag that lost its ">" before the next bracket.
func brokenEndTags(content []byte) []byte {
	changed := false
	out := make([]byte, 0, len(content))
	for i := 0; i < len(content); i++ {
		if content[i] != '<' || i+endTagOpenLen >= len(content) || content[i+1] != '/' {
			out = append(out, content[i])
			continue
		}

		j := i + endTagOpenLen
		for j < len(content) && isNameChar(content[j]) {
			j++
		}
		if j == i+endTagOpenLen {
			out = append(out, content[i])
			continue
		}

		if j < len(content) && content[j] != '>' {
			out = append(out, content[i:j]...)
			out = append(out, '>')
			changed = true
			i = j - 1
			continue
		}

		out = append(out, content[i])
	}
	if !changed {
		return content
	}
	return out
}

// brokenLangTag repairs "<lang ru</lang>" — an opening lang tag that lost
// its ">" and swallowed its text into the tag.
func brokenLangTag(content []byte) []byte {
	changed := false
	out := make([]byte, 0, len(content))
	for i := 0; i < len(content); i++ {
		if i+5 >= len(content) || content[i] != '<' {
			out = append(out, content[i])
			continue
		}
		if !bytes.HasPrefix(content[i:], []byte("<lang")) {
			out = append(out, content[i])
			continue
		}

		nextTagOffset := bytes.IndexByte(content[i+1:], '<')
		if nextTagOffset == -1 {
			out = append(out, content[i])
			continue
		}
		nextTag := i + 1 + nextTagOffset

		gt := bytes.IndexByte(content[i:nextTag], '>')
		if gt != -1 {
			out = append(out, content[i])
			continue
		}

		if !bytes.HasPrefix(content[nextTag:], []byte("</lang>")) {
			out = append(out, content[i])
			continue
		}

		out = append(out, []byte("<lang>")...)
		out = append(out, content[i+5:nextTag]...)
		changed = true
		i = nextTag - 1
		continue
	}
	if !changed {
		return content
	}
	return out
}

// missingXlinkPrefix rewrites l:href attributes to xlink:href when the
// document declares the xlink namespace but not the l one.
func missingXlinkPrefix(content []byte) []byte {
	if !bytes.Contains(content, []byte("xmlns:xlink")) {
		return content
	}
	if bytes.Contains(content, []byte("xmlns:l")) {
		return content
	}
	return bytes.ReplaceAll(content, []byte(" l:href=\""), []byte(" xlink:href=\""))
}

// extractTagNameAt extracts the tag name starting at the given position
func extractTagNameAt(content []byte, start int) string {
	end := start
	for end < len(content) && isNameChar(content[end]) {
		end++
	}
	if end == start {
		return ""
	}
	return string(content[start:end])
}

// isXMLSpace reports whether the byte is XML whitespace.
func isXMLSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// isXMLNameByte reports whether the byte may appear in an attribute name.
func isXMLNameByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b == ':' || b == '-' || b == '_' || b == '.':
		return true
	default:
		return false
	}
}

// isNameChar reports whether the byte may appear in an XML tag name.
func isNameChar(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z':
		return true
	case b >= 'A' && b <= 'Z':
		return true
	case b >= '0' && b <= '9':
		return true
	case b == '-', b == '_', b == ':', b == '.':
		return true
	default:
		return false
	}
}
