package converter

// preview_test.go holds the shared builders and the output-invariant audit
// used by both the chunker and the render tests.

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"regexp"
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// testPreviewPolicy returns a generous chunk policy: only the HTML ceiling
// binds in most tests. Image caps live in testPreviewImagePolicy now — they
// are a different budget of a different resource and travel separately.
func testPreviewPolicy() PreviewPolicy {
	return PreviewPolicy{MaxChunkBytes: 64 * 1024}
}

// testPreviewImagePolicy returns generous image caps: only the HTML ceiling
// or specific image tests bind. The values are the historical defaults,
// carried across the type split unchanged so no test's outcome moves.
func testPreviewImagePolicy() PreviewImagePolicy {
	return PreviewImagePolicy{MaxBytes: 1 << 20, MaxPixels: 32 << 20}
}

// textPara builds a plain paragraph carrying the text both as the inline tree
// and as the plain fallback.
func textPara(text string) *FB2Paragraph {
	return &FB2Paragraph{
		Kind:    ParagraphKindNormal,
		Text:    text,
		Content: []*FB2InlineElement{{Type: InlineTypeText, Content: text}},
	}
}

// noteRefPara builds a paragraph whose only content is a footnote reference.
func noteRefPara(noteID, visible string) *FB2Paragraph {
	return &FB2Paragraph{
		Kind: ParagraphKindNormal,
		Text: visible,
		Content: []*FB2InlineElement{{
			Type:     InlineTypeLink,
			Attrs:    map[string]string{"href": "#" + noteID, "type": "note"},
			Children: []*FB2InlineElement{{Type: InlineTypeText, Content: visible}},
		}},
	}
}

// poemLinePara builds one poem-line paragraph.
func poemLinePara(text string) *FB2Paragraph {
	return &FB2Paragraph{
		Kind:    ParagraphKindPoemLine,
		Text:    text,
		Content: []*FB2InlineElement{{Type: InlineTypeText, Content: text}},
	}
}

// noteSection builds one footnote with the given id and body text.
func noteSection(id, body string) *FB2BodySection {
	return &FB2BodySection{
		ID:      id,
		Content: []*FB2ContentItem{{Paragraph: textPara(body)}},
	}
}

// paraChunk builds a chunk by hand from paragraphs, for render tests.
func paraChunk(index int, paras ...*FB2Paragraph) *PreviewChunk {
	chunk := &PreviewChunk{Index: index}
	for _, p := range paras {
		chunk.blocks = append(chunk.blocks, chunkBlock{para: p})
	}
	return chunk
}

// renderPreview renders or fails the test. Both policies are needed because
// rendering a chunk also resolves image references against a freshly built
// PreviewImages set: chunk policy bounds the HTML, image policy decides which
// binaries the index admits. Folding them back into one argument would
// pretend they are the same budget.
func renderPreview(
	t *testing.T,
	chunk *PreviewChunk,
	binaries map[string]FB2Binary,
	chunkPolicy PreviewPolicy,
	imagePolicy PreviewImagePolicy,
) string {
	t.Helper()
	out, err := RenderChunkHTML(chunk, BuildPreviewImages(binaries, "/preview/img", imagePolicy), chunkPolicy)
	if err != nil {
		t.Fatalf("RenderChunkHTML: %v", err)
	}
	return out
}

// uniformImage encodes a solid-color rectangle in the given format, so tests
// work on real decodable payloads rather than magic-byte forgeries that no
// decoder would accept.
func uniformImage(t *testing.T, format string, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 0x40, G: 0x50, B: 0x60, A: 0xFF})
		}
	}
	var buf bytes.Buffer
	var err error
	switch format {
	case "png":
		err = png.Encode(&buf, img)
	case "jpeg":
		err = jpeg.Encode(&buf, img, nil)
	case "gif":
		err = gif.Encode(&buf, img, nil)
	default:
		t.Fatalf("unknown format %q", format)
	}
	if err != nil {
		t.Fatalf("encode %s: %v", format, err)
	}
	return buf.Bytes()
}

// forgePNGDimensions rewrites the IHDR of a real PNG to declare other
// dimensions, fixing the CRC so the header stays well-formed. The file is
// tiny; only the claimed pixel count is huge — the decompression-bomb shape.
func forgePNGDimensions(t *testing.T, data []byte, width, height uint32) []byte {
	t.Helper()
	out := make([]byte, len(data))
	copy(out, data)
	// Layout: 8-byte signature, then length(4) "IHDR"(4) width(4) height(4).
	if len(out) < 33 || string(out[12:16]) != "IHDR" {
		t.Fatalf("not a PNG with IHDR where expected")
	}
	putU32 := func(off int, v uint32) {
		out[off] = byte(v >> 24)
		out[off+1] = byte(v >> 16)
		out[off+2] = byte(v >> 8)
		out[off+3] = byte(v)
	}
	putU32(16, width)
	putU32(20, height)
	// CRC covers "IHDR" + 13 data bytes, stored right after.
	crc := crc32IEEE(out[12:29])
	putU32(29, crc)
	return out
}

func crc32IEEE(data []byte) uint32 {
	// Local copy to keep the helper self-contained: polynomial reflected IEEE.
	var crc uint32 = 0xFFFFFFFF
	for _, b := range data {
		crc ^= uint32(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xEDB88320
			} else {
				crc >>= 1
			}
		}
	}
	return ^crc
}

// --- Output invariant audit -------------------------------------------------
//
// The audit parses the rendered fragment with a real HTML parser and checks
// the whole document: only whitelisted tags, only whitelisted attributes per
// tag, none of the explicitly dangerous attributes, and every href and src
// passing its own policy again on the rendered side. String searches cannot
// prove any of this — they do not see the document the browser builds.

var previewAllowedTags = map[string]bool{
	"p": true, "a": true, "img": true, "br": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	// tbody never appears in the rendered string, but the HTML parser (and
	// the browser) synthesizes it around table rows, so the DOM this audit
	// walks contains it. It carries no attributes.
	"table": true, "tbody": true, "tr": true, "td": true, "th": true,
	"em": true, "strong": true, "code": true, "sup": true, "sub": true,
	"div": true, "span": true,
}

var previewAllowedAttrs = map[string]map[string]bool{
	"a":     {"href": true, "id": true, "class": true},
	"img":   {"src": true, "alt": true, "loading": true},
	"p":     {"class": true},
	"div":   {"class": true, "id": true},
	"span":  {"class": true},
	"table": {"class": true},
}

var previewForbiddenAttrs = []string{"style", "srcset", "formaction", "target", "download"}

// previewSrcPattern is the only shape a src may take: the base the test built
// the picture set with, then a slash and an ordinal we assigned. It is a
// stricter rule than the data: whitelist it replaces — a URL of this shape can
// carry no payload at all, so there is nothing in it for a book to influence.
var previewSrcPattern = regexp.MustCompile(`^/preview/img/[1-9]\d*$`)

// auditPreviewHTML parses the fragment and asserts the output invariant. It
// returns the collected id set for tests that check anchors further.
func auditPreviewHTML(t *testing.T, fragment string) map[string]bool {
	t.Helper()
	ctx := &xhtml.Node{Type: xhtml.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := xhtml.ParseFragment(strings.NewReader(fragment), ctx)
	if err != nil {
		t.Fatalf("output does not parse as HTML: %v", err)
	}

	ids := make(map[string]bool)
	var walk func(n *xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode {
			tag := strings.ToLower(n.Data)
			if !previewAllowedTags[tag] {
				t.Errorf("tag <%s> is not on the whitelist", tag)
			}
			for _, attr := range n.Attr {
				key := strings.ToLower(attr.Key)
				if strings.HasPrefix(key, "on") {
					t.Errorf("event handler attribute %q on <%s>", attr.Key, tag)
				}
				for _, forbidden := range previewForbiddenAttrs {
					if key == forbidden {
						t.Errorf("forbidden attribute %q on <%s>", attr.Key, tag)
					}
				}
				if strings.HasPrefix(key, "data-") {
					t.Errorf("data-* attribute %q on <%s>", attr.Key, tag)
				}
				if allowed, ok := previewAllowedAttrs[tag]; !ok || !allowed[key] {
					t.Errorf("attribute %q is not allowed on <%s>", attr.Key, tag)
				}
				switch key {
				case "id":
					if ids[attr.Val] {
						t.Errorf("duplicate id %q in one fragment", attr.Val)
					}
					ids[attr.Val] = true
				case "href":
					if !strings.HasPrefix(attr.Val, "#") || len(attr.Val) < 2 {
						t.Errorf("href %q is not a fragment reference", attr.Val)
					}
				case "src":
					if !previewSrcPattern.MatchString(attr.Val) {
						t.Errorf("src %q is not an address this renderer may emit", shorten(attr.Val))
					}
				case "loading":
					if attr.Val != "lazy" {
						t.Errorf("loading=%q: the only value the renderer emits is lazy", attr.Val)
					}
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	for _, n := range nodes {
		walk(n)
	}

	// Second pass: every href fragment must resolve to an id in this same
	// fragment — no dangling links and no escapes out of the portion.
	var resolveCheck func(n *xhtml.Node)
	resolveCheck = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode {
			for _, attr := range n.Attr {
				if strings.EqualFold(attr.Key, "href") {
					target := strings.TrimPrefix(attr.Val, "#")
					if !ids[target] {
						t.Errorf("href %q does not resolve to an anchor in the fragment", attr.Val)
					}
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			resolveCheck(child)
		}
	}
	for _, n := range nodes {
		resolveCheck(n)
	}
	return ids
}

// shorten keeps long attribute values readable in failure messages.
func shorten(s string) string {
	const maxLen = 60
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

// countOccurrences counts non-overlapping occurrences of needle.
func countOccurrences(haystack, needle string) int {
	return strings.Count(haystack, needle)
}

// markerOrder checks the markers appear in the haystack in the given order.
func markerOrder(haystack string, markers []string) error {
	pos := 0
	for i, m := range markers {
		idx := strings.Index(haystack[pos:], m)
		if idx < 0 {
			return fmt.Errorf("marker %d (%q) missing after position %d", i, m, pos)
		}
		pos += idx + len(m)
	}
	return nil
}

// previewImagesFor prepares the picture set from the document's own binaries,
// so a test exercises the same path production will: the chunker sizes what
// the renderer emits, from one map, under one policy. The earlier tests kept
// two maps and the chunker never saw the images at all.
func previewImagesFor(doc *FB2Document, policy PreviewImagePolicy) PreviewImages {
	var bins map[string]FB2Binary
	if doc != nil {
		bins = doc.Binary
	}
	return BuildPreviewImages(bins, "/preview/img", policy)
}
