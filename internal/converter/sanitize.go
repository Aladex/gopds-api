package converter

import (
	"bytes"
	"io"
	"strings"

	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
)

// Charset labels used by makeCharsetReader, with their common aliases.
const (
	charsetUTF8        = "utf-8"
	charsetUTF8Bare    = "utf8"
	charsetLatin1      = "iso-8859-1"
	charsetLatin1Alias = "latin1"
	charsetLatin5      = "iso-8859-5"
	charsetLatin5Alias = "latin5"
	charsetCP1251      = "windows-1251"
	charsetCP1251Alias = "cp1251"
	charsetKOI8R       = "koi8-r"
)

func sanitizeInvalidTagOpenings(content []byte) []byte {
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

func sanitizeInvalidProcessingInstructions(content []byte) []byte {
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

func hasPrefixFold(data []byte, prefix []byte) bool {
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

func sanitizeInvalidAmpersands(content []byte) []byte {
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
	if len(entity) >= 2 && (entity[1] == 'x' || entity[1] == 'X') {
		if len(entity) == 2 {
			return false
		}
		for i := 2; i < len(entity); i++ {
			b := entity[i]
			if !((b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')) {
				return false
			}
		}
		return true
	}
	for i := 1; i < len(entity); i++ {
		b := entity[i]
		if b < '0' || b > '9' {
			return false
		}
	}
	return true
}

func sanitizeControlChars(content []byte) []byte {
	changed := false
	out := make([]byte, 0, len(content))
	for i := 0; i < len(content); i++ {
		b := content[i]
		if b == '\t' || b == '\n' || b == '\r' {
			out = append(out, b)
			continue
		}
		if b < 0x20 {
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

// sanitizeBrokenSelfClosingTags fixes malformed self-closing tags.
// Handles cases like:
//   - <image .../</section> -> <image />
//   - <image .../< -> <image /><
//   - <empty-line / <p> -> <empty-line /><p>
func sanitizeBrokenSelfClosingTags(content []byte) []byte {
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

		// Pattern: /</tag> or /</ or /< -> convert to />
		if next == '<' {
			// Look back to find the opening < of the current tag
			if !isPartOfSelfClosingTag(content, i) {
				out = append(out, content[i])
				continue
			}

			// Skip whitespace between / and <
			j := i + 1
			for j < len(content) && (content[j] == ' ' || content[j] == '\t' || content[j] == '\n' || content[j] == '\r') {
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

		// Pattern: / followed by whitespace before < (e.g., "/ <" or "/ \n<")
		if next == ' ' || next == '\t' || next == '\n' || next == '\r' {
			if !isPartOfSelfClosingTag(content, i) {
				out = append(out, content[i])
				continue
			}

			// Look ahead for <
			j := i + 1
			for j < len(content) && (content[j] == ' ' || content[j] == '\t' || content[j] == '\n' || content[j] == '\r') {
				j++
			}

			if j < len(content) && content[j] == '<' {
				out = append(out, '/', '>')
				changed = true
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
	for nameStart < slashPos && (content[nameStart] == ' ' || content[nameStart] == '\t' || content[nameStart] == '\n' || content[nameStart] == '\r') {
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

	// Also accept any tag that has attributes (likely malformed self-closing)
	hasAttributes := false
	for i := nameStart; i < slashPos; i++ {
		if content[i] == ' ' || content[i] == '\t' || content[i] == '\n' || content[i] == '\r' {
			// Check if there's a = sign after whitespace (indicates attribute)
			for j := i; j < slashPos; j++ {
				if content[j] == '=' {
					hasAttributes = true
					break
				}
				if content[j] != ' ' && content[j] != '\t' && content[j] != '\n' && content[j] != '\r' {
					break
				}
			}
			if hasAttributes {
				break
			}
		}
	}

	return hasAttributes
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

func sanitizeMissingXlinkPrefix(content []byte) []byte {
	if !bytes.Contains(content, []byte("xmlns:xlink")) {
		return content
	}
	if bytes.Contains(content, []byte("xmlns:l")) {
		return content
	}
	out := bytes.ReplaceAll(content, []byte(" l:href=\""), []byte(" xlink:href=\""))
	return out
}

func sanitizeBrokenEndTags(content []byte) []byte {
	changed := false
	out := make([]byte, 0, len(content))
	for i := 0; i < len(content); i++ {
		if content[i] != '<' || i+2 >= len(content) || content[i+1] != '/' {
			out = append(out, content[i])
			continue
		}

		j := i + 2
		for j < len(content) && isNameChar(content[j]) {
			j++
		}
		if j == i+2 {
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

func sanitizeBrokenLangTag(content []byte) []byte {
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

func sanitizeXMLVersion(content []byte) []byte {
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

func makeCharsetReader(charsetLabel string, input io.Reader) (io.Reader, error) {
	charsetLabel = strings.ToLower(charsetLabel)
	switch charsetLabel {
	case charsetUTF8, charsetUTF8Bare:
		return input, nil
	case charsetCP1251, charsetCP1251Alias, "cp-1251":
		return transform.NewReader(input, charmap.Windows1251.NewDecoder()), nil
	case charsetLatin1, charsetLatin1Alias, "iso_8859-1":
		return transform.NewReader(input, charmap.ISO8859_1.NewDecoder()), nil
	case charsetLatin5, charsetLatin5Alias, "iso_8859-5":
		return transform.NewReader(input, charmap.ISO8859_5.NewDecoder()), nil
	case charsetKOI8R, "koi8r":
		return transform.NewReader(input, charmap.KOI8R.NewDecoder()), nil
	case "koi8-u", "koi8u":
		return transform.NewReader(input, charmap.KOI8U.NewDecoder()), nil
	default:
		reader, err := charset.NewReaderLabel(charsetLabel, input)
		if err != nil {
			return input, nil
		}
		return reader, nil
	}
}

// balanceSectionTags ensures all <section> tags are properly balanced.
// This is critical for FB2 files where sections define the book structure.
// It automatically closes unclosed sections and removes orphaned closing tags.
func balanceSectionTags(content []byte) []byte {
	if !bytes.Contains(content, []byte("<section")) && !bytes.Contains(content, []byte("</section>")) {
		return content
	}

	out := make([]byte, 0, len(content))
	sectionStack := make([]int, 0, 32) // Track nesting depth positions

	i := 0
	for i < len(content) {
		if content[i] != '<' {
			out = append(out, content[i])
			i++
			continue
		}

		// Find the end of the tag
		gt := bytes.IndexByte(content[i:], '>')
		if gt == -1 {
			out = append(out, content[i:]...)
			break
		}
		gt += i

		tagContent := content[i+1 : gt]
		tagStr := strings.TrimSpace(string(tagContent))

		// Check if this is a <section> opening tag
		if strings.HasPrefix(tagStr, "section") || strings.HasPrefix(tagStr, "section ") {
			// Not a closing tag and not self-closing
			if !strings.HasSuffix(tagStr, "/") {
				sectionStack = append(sectionStack, len(out))
			}
			out = append(out, content[i:gt+1]...)
			i = gt + 1
			continue
		}

		// Check if this is a </section> closing tag
		if strings.HasPrefix(tagStr, "/section") {
			if len(sectionStack) > 0 {
				// Valid closing tag, pop from stack
				sectionStack = sectionStack[:len(sectionStack)-1]
				out = append(out, content[i:gt+1]...)
			} else {
				// Orphaned closing tag, skip it
				// (don't add to output)
			}
			i = gt + 1
			continue
		}

		// Regular tag, just copy
		out = append(out, content[i:gt+1]...)
		i = gt + 1
	}

	// Close any remaining unclosed sections
	for range sectionStack {
		out = append(out, []byte("</section>")...)
	}

	return out
}

// balanceCommonTags ensures common FB2 tags are properly balanced.
// Handles tags like <p>, <title>, <cite>, <epigraph>, <poem>, <stanza>, etc.
func balanceCommonTags(content []byte) []byte {
	// Tags to balance (order matters for nesting)
	tags := []string{"title", "epigraph", "cite", "poem", "stanza", "p", "v", "subtitle", "text-author"}

	out := content
	for _, tag := range tags {
		out = balanceSpecificTag(out, tag)
	}
	return out
}

// balanceSpecificTag balances a specific tag type
func balanceSpecificTag(content []byte, tag string) []byte {
	openTag := "<" + tag
	closeTag := "</" + tag + ">"
	closeTagBytes := []byte(closeTag)

	if !bytes.Contains(content, []byte(openTag)) {
		return content
	}

	out := make([]byte, 0, len(content))
	stack := make([]int, 0, 16)

	// Tags that should auto-close when encountering the same opening tag
	// (e.g., <p> should close previous unclosed <p>)
	autoCloseTags := map[string]bool{
		"p": true, "v": true, "subtitle": true, "text-author": true,
	}
	shouldAutoClose := autoCloseTags[tag]

	i := 0
	for i < len(content) {
		if content[i] != '<' {
			out = append(out, content[i])
			i++
			continue
		}

		gt := bytes.IndexByte(content[i:], '>')
		if gt == -1 {
			out = append(out, content[i:]...)
			break
		}
		gt += i

		tagContent := content[i+1 : gt]
		tagStr := strings.TrimSpace(string(tagContent))
		lowerTag := strings.ToLower(tagStr)

		// Check if this is our opening tag
		if lowerTag == tag || strings.HasPrefix(lowerTag, tag+" ") {
			if !strings.HasSuffix(tagStr, "/") {
				// Auto-close previous tag of the same type if needed
				if shouldAutoClose && len(stack) > 0 {
					out = append(out, closeTagBytes...)
					stack = stack[:len(stack)-1]
				}
				stack = append(stack, len(out))
			}
			out = append(out, content[i:gt+1]...)
			i = gt + 1
			continue
		}

		// Check if this is our closing tag
		if lowerTag == "/"+tag {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
				out = append(out, content[i:gt+1]...)
			}
			// Orphaned closing tags are skipped
			i = gt + 1
			continue
		}

		// Regular tag
		out = append(out, content[i:gt+1]...)
		i = gt + 1
	}

	// Close any remaining unclosed tags
	for range stack {
		out = append(out, closeTagBytes...)
	}

	return out
}
