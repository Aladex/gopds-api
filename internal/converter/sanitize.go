package converter

import (
	"bytes"
	"io"
	"strings"

	"github.com/saintfish/chardet"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
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

func sanitizeBrokenSelfClosingTags(content []byte) []byte {
	if !bytes.Contains(content, []byte("/</")) {
		if !bytes.Contains(content, []byte("/\n<")) && !bytes.Contains(content, []byte("/\r\n<")) && !bytes.Contains(content, []byte("/\t<")) && !bytes.Contains(content, []byte("/ <")) {
			return content
		}
	}
	out := bytes.ReplaceAll(content, []byte("/</"), []byte("/><"))
	out = bytes.ReplaceAll(out, []byte("/\r\n<"), []byte("/><"))
	out = bytes.ReplaceAll(out, []byte("/\n<"), []byte("/><"))
	out = bytes.ReplaceAll(out, []byte("/\t<"), []byte("/><"))
	out = bytes.ReplaceAll(out, []byte("/ <"), []byte("/><"))
	return out
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

// tryDecodeCharset detects encoding from XML declaration and converts to UTF-8.
// It also normalizes the XML declaration to encoding="utf-8" when conversion happens.
func tryDecodeCharset(content []byte) []byte {
	if isValidUTF8(content) {
		return content
	}

	declEnd := bytes.Index(content, []byte("?>"))
	if declEnd > 0 && declEnd < 200 {
		decl := string(content[:declEnd])
		encoding := extractEncoding(decl)
		if encoding != "" {
			decoded := convertEncoding(content, encoding)
			if decoded != nil {
				return normalizeEncodingDecl(decoded, "utf-8")
			}
		}
	}

	for _, enc := range []string{"iso-8859-5", "windows-1251", "cp1251", "iso-8859-1"} {
		decoded := convertEncoding(content, enc)
		if decoded != nil && isValidUTF8(decoded) {
			return normalizeEncodingDecl(decoded, "utf-8")
		}
	}

	if detected := detectCharset(content); detected != "" {
		decoded := convertEncoding(content, detected)
		if decoded != nil && isValidUTF8(decoded) {
			return normalizeEncodingDecl(decoded, "utf-8")
		}
	}

	return content
}

func isValidUTF8(data []byte) bool {
	return utf8BytesValid(data)
}

func utf8BytesValid(data []byte) bool {
	for i := 0; i < len(data); {
		if data[i] < 0x80 {
			i++
			continue
		}
		if data[i]&0xE0 == 0xC0 {
			if i+1 >= len(data) || data[i+1]&0xC0 != 0x80 {
				return false
			}
			i += 2
			continue
		}
		if data[i]&0xF0 == 0xE0 {
			if i+2 >= len(data) || data[i+1]&0xC0 != 0x80 || data[i+2]&0xC0 != 0x80 {
				return false
			}
			i += 3
			continue
		}
		if data[i]&0xF8 == 0xF0 {
			if i+3 >= len(data) || data[i+1]&0xC0 != 0x80 || data[i+2]&0xC0 != 0x80 || data[i+3]&0xC0 != 0x80 {
				return false
			}
			i += 4
			continue
		}
		return false
	}
	return true
}

func extractEncoding(decl string) string {
	start := strings.Index(decl, "encoding=")
	if start == -1 {
		return ""
	}
	start += 9
	if start >= len(decl) {
		return ""
	}

	quote := decl[start]
	if quote != '"' && quote != '\'' {
		return ""
	}

	start++
	end := strings.IndexByte(decl[start:], quote)
	if end == -1 {
		return ""
	}

	return strings.ToLower(decl[start : start+end])
}

func normalizeEncodingDecl(content []byte, encoding string) []byte {
	declEnd := bytes.Index(content, []byte("?>"))
	if declEnd == -1 || declEnd > 200 {
		return content
	}
	decl := string(content[:declEnd])
	start := strings.Index(decl, "encoding=")
	if start == -1 {
		return content
	}
	start += 9
	if start >= len(decl) {
		return content
	}

	quote := decl[start]
	if quote != '"' && quote != '\'' {
		return content
	}

	start++
	end := strings.IndexByte(decl[start:], quote)
	if end == -1 {
		return content
	}

	newDecl := decl[:start] + encoding + decl[start+end:]
	normalized := append([]byte(newDecl), content[declEnd:]...)
	return normalized
}

func convertEncoding(content []byte, encoding string) []byte {
	var dec transform.Transformer
	switch strings.ToLower(encoding) {
	case "iso-8859-1", "iso-8859-5", "latin1", "latin5":
		dec = charmap.ISO8859_5.NewDecoder()
	case "windows-1251", "cp1251":
		dec = charmap.Windows1251.NewDecoder()
	default:
		reader, err := charset.NewReaderLabel(strings.ToLower(encoding), bytes.NewReader(content))
		if err != nil {
			return nil
		}
		decoded, err := io.ReadAll(reader)
		if err != nil {
			return nil
		}
		return decoded
	}

	result, _, err := transform.Bytes(dec, content)
	if err != nil {
		return nil
	}
	return result
}

func makeCharsetReader(charsetLabel string, input io.Reader) (io.Reader, error) {
	charsetLabel = strings.ToLower(charsetLabel)
	switch charsetLabel {
	case "utf-8", "utf8":
		return input, nil
	case "windows-1251", "cp1251", "cp-1251":
		return transform.NewReader(input, charmap.Windows1251.NewDecoder()), nil
	case "iso-8859-1", "latin1", "iso_8859-1":
		return transform.NewReader(input, charmap.ISO8859_1.NewDecoder()), nil
	case "iso-8859-5", "latin5", "iso_8859-5":
		return transform.NewReader(input, charmap.ISO8859_5.NewDecoder()), nil
	case "koi8-r", "koi8r":
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

func detectCharset(content []byte) string {
	detector := chardet.NewTextDetector()
	result, err := detector.DetectBest(content)
	if err != nil || result == nil {
		return ""
	}
	return strings.ToLower(result.Charset)
}
