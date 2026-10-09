package parser

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	xunicode "golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// Typed errors returned by DecodeToUTF8. Callers use errors.Is to branch on
// the failure kind; the wrapped message carries the detail.
var (
	// ErrUnsupportedCharset marks input whose encoding is recognized but not
	// supported: UTF-32 (any BOM) and UTF-16 without a BOM.
	ErrUnsupportedCharset = errors.New("unsupported charset")
	// ErrUndeclaredCharset marks input that is not valid UTF-8, carries no
	// BOM, and has no usable XML encoding declaration. Such bytes are not
	// guessed: statistical single-byte detection was removed after a catalog
	// census (88k books) showed the guessing apparatus served an empty set.
	ErrUndeclaredCharset = errors.New("charset is not declared and content is not valid utf-8")
	// ErrDamagedContent marks input whose charset is known (BOM or
	// declaration) but whose bytes are damaged beyond local repair.
	// FB2Parser.Parse returns the same error when the parsed document
	// has no FictionBook root element.
	ErrDamagedContent = errors.New("content is damaged beyond local repair")
	// ErrUnsupportedDeclaredCharset marks input that carries an XML encoding
	// declaration outside the supported label set. The declaration exists
	// (unlike ErrUndeclaredCharset); the label is simply not one this build
	// decodes.
	ErrUnsupportedDeclaredCharset = errors.New("declared charset is not supported")
)

// Charset labels recognized in XML declarations, with their aliases.
// makeCharsetReader in fb2parser.go uses the same constants.
const (
	labelUTF8     = "utf-8"
	labelUTF8Bare = "utf8"
	labelUTF16    = "utf-16"
	labelUTF16LE  = "utf-16le"
	labelUTF16BE  = "utf-16be"

	labelCP1251          = "windows-1251"
	labelCP1251AliasFlat = "cp1251"
	labelCP1251AliasDash = "cp-1251"
	labelKOI8R           = "koi8-r"
	labelKOI8RAlias      = "koi8r"
	labelLatin5          = "iso-8859-5"
	labelLatin5Alias     = "latin5"
	labelLatin5AliasFlat = "iso_8859-5"
	labelLatin1          = "iso-8859-1"
	labelLatin1Alias     = "latin1"
	labelLatin1AliasFlat = "iso_8859-1"

	// The Western, Central European, Greek, Turkish, Baltic and Ukrainian
	// charsets FB2 in the wild declares beyond the Cyrillic set.
	labelCP1250          = "windows-1250"
	labelCP1250AliasFlat = "cp1250"
	labelCP1250AliasDash = "cp-1250"
	labelCP1252          = "windows-1252"
	labelCP1252AliasFlat = "cp1252"
	labelCP1252AliasDash = "cp-1252"
	labelCP1253          = "windows-1253"
	labelCP1253AliasFlat = "cp1253"
	labelCP1253AliasDash = "cp-1253"
	labelCP1254          = "windows-1254"
	labelCP1254AliasFlat = "cp1254"
	labelCP1254AliasDash = "cp-1254"
	labelCP1257          = "windows-1257"
	labelCP1257AliasFlat = "cp1257"
	labelCP1257AliasDash = "cp-1257"
	labelLatin2          = "iso-8859-2"
	labelLatin2Alias     = "latin2"
	labelLatin2AliasFlat = "iso_8859-2"
	labelLatin9          = "iso-8859-15"
	labelLatin9Alias     = "latin9"
	labelLatin9AliasFlat = "iso_8859-15"
	labelKOI8U           = "koi8-u"
	labelKOI8UAlias      = "koi8u"
)

// Byte order marks, longest first: UTF-32LE shares its first two bytes with
// UTF-16LE, so the longer signature must be checked before the shorter one.
var (
	bomUTF8    = []byte{0xEF, 0xBB, 0xBF}
	bomUTF32LE = []byte{0xFF, 0xFE, 0x00, 0x00}
	bomUTF32BE = []byte{0x00, 0x00, 0xFE, 0xFF}
	bomUTF16LE = []byte{0xFF, 0xFE}
	bomUTF16BE = []byte{0xFE, 0xFF}
)

// Prolog signatures of UTF-16 content without a BOM. This is a signature
// check, not a density heuristic: null-byte share cannot detect UTF-16,
// because Cyrillic UTF-16LE carries 0x04 high bytes, not 0x00. Content that
// does not start with the XML prolog is undetectable and documented as a
// blind spot.
var (
	xmlPrologUTF16LE = []byte{'<', 0, '?', 0, 'x', 0, 'm', 0, 'l', 0}
	xmlPrologUTF16BE = []byte{0, '<', 0, '?', 0, 'x', 0, 'm', 0, 'l'}
)

// maxRepairableCorruptBytes bounds the declared-UTF-8 repair branch. The
// catalog census found books declaring UTF-8 with single-digit counts of
// broken bytes (13 of the 24 damaged declared-UTF-8 books carry exactly
// one); 64 gives that an order of magnitude of headroom while any
// misdeclared single-byte text is thousands of corrupt bytes (Cyrillic
// prose in a single-byte charset breaks UTF-8 every few letters), so the
// budget separates "local damage" from "wrong charset" by a wide gap. The
// budget is the branch's only damage-based refusal: a corrupt byte inside
// markup is still repaired, because one bad byte must not cost a book.
const maxRepairableCorruptBytes = 64

// encodingAttrPattern finds the encoding attribute inside an XML prolog,
// tolerating whitespace around '=' and any letter case. RE2 has no
// backreferences, so the closing quote is not tied to the opening one; a
// mismatched-quote prolog is caught later by the root-element token scan.
var encodingAttrPattern = regexp.MustCompile(`(?i)\bencoding\s*=\s*["']([^"']*)["']`)

// DecodeToUTF8 resolves the charset of FB2 content and returns it as UTF-8,
// with the XML declaration normalized to encoding="utf-8". The decision
// order is fixed and contains no statistical guessing:
//
//  1. A BOM is authoritative (UTF-8, UTF-16LE/BE; UTF-32 is refused).
//  2. UTF-16 without a BOM is refused (prolog signature only).
//  3. A strict whole-file utf8.Valid check: valid UTF-8 wins over any
//     declaration, because files labeled windows-1251 but saved as UTF-8
//     exist. The whole file is checked, not the head — books with a broken
//     tail past 64 KiB exist.
//  4. An invalid-UTF-8 file follows its declaration: a supported
//     single-byte label is trusted and the decoded form's declaration is
//     normalized to UTF-8; a UTF-8 label goes to bounded local repair.
//     Whether the decoded text is a FictionBook document is not decided
//     here: the root check lives in the parse stage (FB2Parser), which
//     judges the sanitized text the XML decoder actually reads. A verdict
//     here would need its own reading of the prolog, and any reading that
//     differs from the sanitizers' loses real books or smuggles false
//     roots — the charset stage resolves bytes, not documents.
//  5. No BOM, invalid UTF-8, no usable declaration: a typed error. A
//     declaration with an unsupported label is a distinct typed error.
//     There is no default candidate and the original bytes are never
//     returned as-if-decoded.
func DecodeToUTF8(content []byte) ([]byte, error) {
	switch {
	case bytes.HasPrefix(content, bomUTF32LE), bytes.HasPrefix(content, bomUTF32BE):
		return nil, fmt.Errorf("%w: utf-32 byte order mark", ErrUnsupportedCharset)
	case bytes.HasPrefix(content, bomUTF16LE):
		return decodeUTF16(content[len(bomUTF16LE):], xunicode.LittleEndian)
	case bytes.HasPrefix(content, bomUTF16BE):
		return decodeUTF16(content[len(bomUTF16BE):], xunicode.BigEndian)
	case bytes.HasPrefix(content, bomUTF8):
		return finishUTF8Assumed(content[len(bomUTF8):])
	}

	if bytes.HasPrefix(content, xmlPrologUTF16LE) || bytes.HasPrefix(content, xmlPrologUTF16BE) {
		return nil, fmt.Errorf("%w: utf-16 without a byte order mark", ErrUnsupportedCharset)
	}

	if utf8.Valid(content) {
		return normalizeEncodingDecl(content), nil
	}

	decl := declaredEncoding(content)
	switch {
	case decl == "":
		return nil, ErrUndeclaredCharset
	case isUTF8Label(decl):
		return repairDeclaredUTF8(content)
	case isUTF16Label(decl):
		return nil, fmt.Errorf("%w: declared %s without a byte order mark", ErrUnsupportedCharset, decl)
	}

	decoded, known := decodeSingleByte(content, decl)
	if !known {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedDeclaredCharset, decl)
	}
	return normalizeEncodingDecl(decoded), nil
}

// decodeUTF16 transcodes BOM-marked UTF-16 content to UTF-8. A damaged body
// is ErrDamagedContent: the BOM already told us what the bytes claim to be.
// Locally repairable damage deeper in the document (control characters,
// unescaped markup, a broken prolog) is the downstream sanitizers' job, and
// judging it here would reject books they would have repaired.
func decodeUTF16(content []byte, endianness xunicode.Endianness) ([]byte, error) {
	dec := xunicode.UTF16(endianness, xunicode.IgnoreBOM).NewDecoder()
	out, _, err := transform.Bytes(dec, content)
	if err != nil {
		return nil, fmt.Errorf("%w: utf-16 body does not decode: %v", ErrDamagedContent, err)
	}
	// Normalize the declaration: one still saying utf-16 would send the
	// downstream decoder looking for a charset reader.
	return normalizeEncodingDecl(out), nil
}

// finishUTF8Assumed handles content whose UTF-8-ness is asserted by a BOM:
// valid content passes through, damaged content goes to bounded repair.
func finishUTF8Assumed(content []byte) ([]byte, error) {
	if utf8.Valid(content) {
		return normalizeEncodingDecl(content), nil
	}
	return repairDeclaredUTF8(content)
}

// repairDeclaredUTF8 handles content declared (or BOM-marked) as UTF-8 that
// fails the strict check. Replacement decoding is refused only for damage
// beyond the corrupt-byte budget; anything else is repaired, because one bad
// byte — even inside markup — must not cost the reader a whole book, and the
// downstream pipeline already repairs broken markup.
func repairDeclaredUTF8(content []byte) ([]byte, error) {
	corrupt := countCorruptUTF8Bytes(content)
	if corrupt > maxRepairableCorruptBytes {
		return nil, fmt.Errorf("%w: declared utf-8 with %d corrupt bytes (repair budget is %d)",
			ErrDamagedContent, corrupt, maxRepairableCorruptBytes)
	}
	repaired := bytes.ToValidUTF8(content, []byte("�"))
	return normalizeEncodingDecl(repaired), nil
}

// countCorruptUTF8Bytes counts positions where UTF-8 decoding fails (each
// RuneError of size 1 is one undecodable byte).
func countCorruptUTF8Bytes(data []byte) int {
	n := 0
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size == 1 {
			n++
			i++
			continue
		}
		i += size
	}
	return n
}

// declaredEncoding extracts the encoding label from the XML declaration. The
// scan is bounded by the declaration end ("?>"), not by an arbitrary byte
// count, and the attribute match tolerates whitespace and letter case.
func declaredEncoding(content []byte) string {
	end := xmlDeclEnd(content)
	if end == -1 {
		return ""
	}
	m := encodingAttrPattern.FindSubmatch(content[:end])
	if m == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(string(m[1])))
}

// xmlDeclEnd returns the index just past the closing "?>" of the XML
// declaration opening the content, or -1 when the content does not start
// with one. The "<?xml" prefix must be followed by a name boundary —
// whitespace or the closing "?>" itself — so an xml-stylesheet processing
// instruction (or any other target beginning with "xml") is not mistaken for
// the declaration and its pseudo-attributes are never read or rewritten as
// the document's encoding. The scan for the declaration end is unbounded:
// the whole file is scanned by utf8.Valid on this path anyway, so a byte cap
// would only add a correctness cliff (a declaration longer than the cap
// would be missed) without saving work. The prefix check runs first, so
// content without a declaration pays no "?>" scan at all.
func xmlDeclEnd(content []byte) int {
	trimmed := bytes.TrimLeft(content, " \t\r\n")
	if len(trimmed) < len(xmlDeclPrefix) || !strings.EqualFold(string(trimmed[:len(xmlDeclPrefix)]), xmlDeclPrefix) {
		return -1
	}
	if len(trimmed) > len(xmlDeclPrefix) {
		switch trimmed[len(xmlDeclPrefix)] {
		case ' ', '\t', '\r', '\n', '?':
		default:
			return -1
		}
	}
	end := bytes.Index(content, piClose)
	if end == -1 {
		return -1
	}
	return end + len(piClose)
}

// normalizeEncodingDecl rewrites the declaration's encoding value to utf-8,
// so a downstream XML decoder never re-decodes content that is already
// UTF-8. Only a real XML declaration is touched: another processing
// instruction (xml-stylesheet is the common one) keeps its own
// pseudo-attributes even when one of them is called encoding. Content
// without a declaration or without an encoding attribute is returned
// unchanged — as is content already declaring a UTF-8 label, which is the
// majority of the catalog and must not pay a whole-file copy for a rewrite
// that changes nothing.
func normalizeEncodingDecl(content []byte) []byte {
	end := xmlDeclEnd(content)
	if end == -1 {
		return content
	}
	decl := content[:end]
	loc := encodingAttrPattern.FindSubmatchIndex(decl)
	if loc == nil {
		return content
	}
	if isUTF8Label(strings.ToLower(strings.TrimSpace(string(decl[loc[2]:loc[3]])))) {
		return content
	}
	out := make([]byte, 0, len(content)+len(labelUTF8))
	out = append(out, decl[:loc[2]]...)
	out = append(out, labelUTF8...)
	out = append(out, decl[loc[3]:]...)
	return append(out, content[end:]...)
}

// isUTF8Label and isUTF16Label classify declaration labels.
func isUTF8Label(label string) bool {
	return label == labelUTF8 || label == labelUTF8Bare
}

func isUTF16Label(label string) bool {
	return label == labelUTF16 || label == labelUTF16LE || label == labelUTF16BE
}

// decodeSingleByte converts content from a declared single-byte charset to
// UTF-8. The label set is closed — see singleByteEncodingFor — because the
// declaration is trusted, not negotiated: known=false means the label is
// outside the supported set.
func decodeSingleByte(content []byte, label string) (decoded []byte, known bool) {
	enc, known := singleByteEncodingFor(label)
	if !known {
		return nil, false
	}
	return enc.decode(content)
}

// singleByteEncoding is one closed-table entry: either a charmap used exactly
// as the library decodes it, or an ISO 8859 page carried as a corrected
// 256-rune table (see correctedISORuneTable).
type singleByteEncoding struct {
	charmap *charmap.Charmap // set when the library table is used as-is
	runes   *[256]rune       // set when a corrected table replaces the library's
}

// decode converts declared single-byte content to UTF-8. Single-byte charmaps
// and corrected tables decode any byte; an error is defensive only.
func (e singleByteEncoding) decode(content []byte) ([]byte, bool) {
	if e.runes != nil {
		out := make([]byte, 0, len(content)*singleByteMaxUTF8)
		for _, b := range content {
			out = utf8.AppendRune(out, e.runes[b])
		}
		return out, true
	}
	out, _, err := transform.Bytes(e.charmap.NewDecoder(), content)
	if err != nil {
		return nil, false
	}
	return out, true
}

// newReader is the streaming form of decode, for the XML decoder's charset
// reader.
func (e singleByteEncoding) newReader(input io.Reader) io.Reader {
	if e.runes != nil {
		return transform.NewReader(input, runeTableDecoder{table: e.runes})
	}
	return transform.NewReader(input, e.charmap.NewDecoder())
}

// singleByteMaxUTF8 is the largest UTF-8 encoding of any rune in the
// supported single-byte pages (their highest code points sit in the
// box-drawing range, U+25xx): three bytes, the size a capacity hint needs.
const singleByteMaxUTF8 = 3

// runeTableDecoder turns a 256-rune table into a streaming Transformer: every
// input byte is exactly one rune, so the transform is a flat loop.
type runeTableDecoder struct{ table *[256]rune }

func (d runeTableDecoder) Transform(dst, src []byte, atEOF bool) (nDst, nSrc int, err error) {
	for nSrc < len(src) {
		var buf [utf8.UTFMax]byte
		n := utf8.EncodeRune(buf[:], d.table[src[nSrc]])
		if nDst+n > len(dst) {
			return nDst, nSrc, transform.ErrShortDst
		}
		nDst += copy(dst[nDst:], buf[:n])
		nSrc++
	}
	return nDst, nSrc, nil
}

func (d runeTableDecoder) Reset() {}

// correctedISORuneTable builds the 256-rune decode table of an ISO 8859 page
// from its charmap, restoring the C1 control characters the page defines at
// 0x80-0x9F. x/text's tables for several ISO pages (8859-2 and 8859-15 among
// them) map those bytes to U+FFFD, but the standards define U+0080-U+009F
// there: they are defined characters, not undecodable bytes, and replacing
// one with a different rune would silently alter the credited source text.
// The table is built from the library's own decode so the printable range
// stays exactly the library's; only the C1 slice is restored by definition.
// The previously supported ISO-8859-5 deliberately keeps its library behavior
// (byte-identical requirement for existing labels); see singleByteEncodingFor.
func correctedISORuneTable(enc *charmap.Charmap) *[256]rune {
	table := new([256]rune)
	for b := 0; b < 256; b++ {
		if b >= 0x80 && b <= 0x9F {
			table[b] = rune(b)
			continue
		}
		decoded, _, err := transform.Bytes(enc.NewDecoder(), []byte{byte(b)})
		r, size := utf8.DecodeRune(decoded)
		if err != nil || size == 0 {
			r = utf8.RuneError
		}
		table[b] = r
	}
	return table
}

// The corrected tables for the two newly admitted ISO pages, built once.
var (
	iso8859_2RuneTable  = correctedISORuneTable(charmap.ISO8859_2)
	iso8859_15RuneTable = correctedISORuneTable(charmap.ISO8859_15)
)

// singleByteEncodingFor is the one closed table of declared single-byte
// charsets this build decodes, shared by the byte decoder and the XML
// decoder's charset reader so the two can never disagree (koi8-u was once
// known to one and refused by the other). Each label is admitted on measured
// need, with aliases following the WHATWG label conventions and the existing
// Cyrillic set's style:
//
//   - the Cyrillic set FB2 was built around: windows-1251, KOI8-R, ISO-8859-5,
//     ISO-8859-1, and koi8-u (Ukrainian — the second language of the
//     catalog; the reader table already knew it);
//   - windows-1252: the first prod pilot refused 2% of an archive (143 of
//     7141 books) on exactly this label — Western European books;
//   - windows-1250 and -1257 (Central European and Baltic), -1253 (Greek) and
//     -1254 (Turkish): the Western books a Russian-library catalog carries;
//   - ISO-8859-2 and ISO-8859-15: the ISO counterparts European tools declare
//     instead of the windows pages, decoded through corrected tables because
//     the library replaces their defined C1 characters with U+FFFD.
//
// An open label lookup (x/net/html/charset) is deliberately not used: it
// would silently accept a far wider label set — including labels whose
// mappings differ from these charmaps — turning every future unknown
// declaration into accepted-by-default instead of a reviewed, measured
// decision, and the refusal contract (ErrUnsupportedDeclaredCharset) would
// no longer name a set anyone could test.
func singleByteEncodingFor(label string) (singleByteEncoding, bool) {
	switch label {
	case labelCP1251, labelCP1251AliasFlat, labelCP1251AliasDash:
		return singleByteEncoding{charmap: charmap.Windows1251}, true
	case labelKOI8R, labelKOI8RAlias:
		return singleByteEncoding{charmap: charmap.KOI8R}, true
	case labelKOI8U, labelKOI8UAlias:
		return singleByteEncoding{charmap: charmap.KOI8U}, true
	case labelLatin5, labelLatin5Alias, labelLatin5AliasFlat:
		return singleByteEncoding{charmap: charmap.ISO8859_5}, true
	case labelLatin1, labelLatin1Alias, labelLatin1AliasFlat:
		return singleByteEncoding{charmap: charmap.ISO8859_1}, true
	case labelLatin2, labelLatin2Alias, labelLatin2AliasFlat:
		return singleByteEncoding{runes: iso8859_2RuneTable}, true
	case labelLatin9, labelLatin9Alias, labelLatin9AliasFlat:
		return singleByteEncoding{runes: iso8859_15RuneTable}, true
	case labelCP1250, labelCP1250AliasFlat, labelCP1250AliasDash:
		return singleByteEncoding{charmap: charmap.Windows1250}, true
	case labelCP1252, labelCP1252AliasFlat, labelCP1252AliasDash:
		return singleByteEncoding{charmap: charmap.Windows1252}, true
	case labelCP1253, labelCP1253AliasFlat, labelCP1253AliasDash:
		return singleByteEncoding{charmap: charmap.Windows1253}, true
	case labelCP1254, labelCP1254AliasFlat, labelCP1254AliasDash:
		return singleByteEncoding{charmap: charmap.Windows1254}, true
	case labelCP1257, labelCP1257AliasFlat, labelCP1257AliasDash:
		return singleByteEncoding{charmap: charmap.Windows1257}, true
	default:
		return singleByteEncoding{}, false
	}
}

// xmlDeclPrefix is the target opening the XML declaration processing
// instruction.
const xmlDeclPrefix = "<?xml"

// piClose ends the XML declaration (and any processing instruction).
var piClose = []byte("?>")
