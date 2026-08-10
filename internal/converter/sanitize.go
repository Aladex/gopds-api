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
