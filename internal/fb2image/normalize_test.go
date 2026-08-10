package fb2image_test

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"runtime"
	"testing"

	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"

	"gopds-api/internal/fb2image"
)

// sample builds a small picture with enough color variation that a lossy
// re-encode still has something to preserve.
func sample() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 30), G: uint8(y * 30), B: 0x40, A: 0xFF})
		}
	}
	return img
}

func encodeBMP(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := bmp.Encode(&buf, sample()); err != nil {
		t.Fatalf("encode bmp: %v", err)
	}
	return buf.Bytes()
}

func encodeTIFF(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := tiff.Encode(&buf, sample(), nil); err != nil {
		t.Fatalf("encode tiff: %v", err)
	}
	return buf.Bytes()
}

func encodeJPEG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, sample(), nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

func encodePNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, sample()); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func encodeGIF(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := gif.Encode(&buf, sample(), nil); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	return buf.Bytes()
}

// A BMP cover reached the reader as cover.bin, which no EPUB reader draws.
// It has to arrive as a picture instead.
func TestNormalize_BMPBecomesPNG(t *testing.T) {
	in := encodeBMP(t)
	out, mime := fb2image.Normalize(in)
	if mime != "image/png" {
		t.Fatalf("mime = %q, want image/png", mime)
	}
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("output does not decode as png: %v", err)
	}
	if bytes.Equal(out, in) {
		t.Fatal("output is still the bmp payload")
	}
}

func TestNormalize_TIFFBecomesPNG(t *testing.T) {
	in := encodeTIFF(t)
	out, mime := fb2image.Normalize(in)
	if mime != "image/png" {
		t.Fatalf("mime = %q, want image/png", mime)
	}
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("output does not decode as png: %v", err)
	}
}

// The picture has to survive the trip, not merely become a valid PNG: an
// encoder handed a blank image also produces something png.Decode accepts.
func TestNormalize_BMPKeepsThePicture(t *testing.T) {
	out, _ := fb2image.Normalize(encodeBMP(t))
	got, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if b := got.Bounds(); b.Dx() != 8 || b.Dy() != 8 {
		t.Fatalf("bounds = %v, want 8x8", b)
	}
	// The source corners differ strongly; a lost or blanked image would not.
	tl, br := got.At(0, 0), got.At(7, 7)
	r1, g1, _, _ := tl.RGBA()
	r2, g2, _, _ := br.RGBA()
	if diff(r1, r2)+diff(g1, g2) < 0x4000 {
		t.Fatalf("corners are too close: %v vs %v", tl, br)
	}
}

func diff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}

// Formats every reader already draws must come back untouched. Re-encoding
// 5.5 million pictures to fix 263 would be a poor trade.
func TestNormalize_KnownFormatsPassThroughByteForByte(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		mime string
	}{
		{"jpeg", encodeJPEG(t), "image/jpeg"},
		{"png", encodePNG(t), "image/png"},
		{"gif", encodeGIF(t), "image/gif"},
		{"svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`), "image/svg+xml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, mime := fb2image.Normalize(tc.data)
			if mime != tc.mime {
				t.Fatalf("mime = %q, want %q", mime, tc.mime)
			}
			if !bytes.Equal(out, tc.data) {
				t.Fatalf("payload was rewritten: %d bytes in, %d out", len(tc.data), len(out))
			}
		})
	}
}

// A payload that is not a picture gets no type, so the caller drops it
// instead of shipping bytes nothing can display.
func TestNormalize_UnplaceablePayloadGetsNoType(t *testing.T) {
	cases := map[string][]byte{
		"empty":     {},
		"html":      []byte("<html><body>not an image</body></html>"),
		"text":      []byte("just some text that happens to be here"),
		"truncated": encodeBMP(t)[:10],
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			out, mime := fb2image.Normalize(data)
			if mime != "" {
				t.Fatalf("mime = %q, want empty", mime)
			}
			if out != nil {
				t.Fatalf("out = %d bytes, want nil", len(out))
			}
		})
	}
}

// The scanner and the EPUB generator both normalise, and the generator
// matches the cover against the body images by comparing bytes. That match
// only survives if the same input always yields the same output.
func TestNormalize_IsDeterministic(t *testing.T) {
	in := encodeBMP(t)
	first, _ := fb2image.Normalize(in)
	second, _ := fb2image.Normalize(in)
	if !bytes.Equal(first, second) {
		t.Fatal("two runs produced different bytes")
	}
}

func TestExtensionFor(t *testing.T) {
	cases := map[string]string{
		"image/jpeg":    ".jpg",
		"image/png":     ".png",
		"image/gif":     ".gif",
		"image/webp":    ".webp",
		"image/svg+xml": ".svg",
		"":              "",
		"image/bmp":     "",
	}
	for mime, want := range cases {
		if got := fb2image.ExtensionFor(mime); got != want {
			t.Fatalf("ExtensionFor(%q) = %q, want %q", mime, got, want)
		}
	}
}

// TestNormalize_SVGNeedsItsRoot pins the difference between "this is XML" and
// "this is an SVG". Every XML document opens with a prolog, so accepting one
// as confirmation would let any XML — an HTML page, another FB2 — be declared
// an image and written into the EPUB as one.
func TestNormalize_SVGNeedsItsRoot(t *testing.T) {
	cases := []struct {
		name string
		data string
		want bool
	}{
		{"bare svg root", `<svg xmlns="http://www.w3.org/2000/svg"/>`, true},
		{"prolog then svg", `<?xml version="1.0"?><svg/>`, true},
		{"comment then svg", `<!-- note --><svg/>`, true},
		{"doctype then svg", `<!DOCTYPE svg><svg/>`, true},
		{"prolog then html", `<?xml version="1.0"?><html></html>`, false},
		{"svg-like name is not svg", `<svgx/>`, false},
		{"hyphenated name is not svg", `<svg-script/>`, false},
		{"cdata opener before svg", `<![CDATA[><svg/>`, false},
		{"closed cdata before svg", `<![CDATA[x]]><svg/>`, false},
		{"junk text before svg", `junk<svg/>`, false},
		{"uppercase root is not svg", `<SVG/>`, false},
		{"foreign namespace is not svg", `<x:svg xmlns:x="urn:not-svg"/>`, false},
		{"declared svg namespace", `<svg xmlns="http://www.w3.org/2000/svg"><g/></svg>`, true},
		{"doctype with internal subset", `<!DOCTYPE svg [<!ENTITY a "b">]><svg/>`, true},
		{"utf-8 bom then svg", "\ufeff<svg/>", true},
		{"prolog then fictionbook", `<?xml version="1.0"?><FictionBook/>`, false},
		{"prolog alone", `<?xml version="1.0"?>`, false},
		{"unterminated prolog", `<?xml version="1.0"`, false},
		{"plain text", `not markup at all`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, mime := fb2image.Normalize([]byte(tc.data))
			if got := mime == "image/svg+xml"; got != tc.want {
				t.Errorf("Normalize(%q) gave %q; treated as svg = %v, want %v",
					tc.data, mime, got, tc.want)
			}
		})
	}
}

// bombHeader forges a BMP whose header claims an enormous picture while the
// file itself is a few dozen bytes. Decoding it allocates from the declared
// size, so the payload has to be refused before a decoder ever sees it.
func bombHeader(w, h int32) []byte {
	var buf bytes.Buffer
	buf.WriteString("BM")
	_ = binary.Write(&buf, binary.LittleEndian, []int32{0, 0, 54})
	_ = binary.Write(&buf, binary.LittleEndian, int32(40))
	_ = binary.Write(&buf, binary.LittleEndian, w)
	_ = binary.Write(&buf, binary.LittleEndian, h)
	_ = binary.Write(&buf, binary.LittleEndian, []int16{1, 24})
	_ = binary.Write(&buf, binary.LittleEndian, []int32{0, 0, 2835, 2835, 0, 0})
	buf.Write(make([]byte, 16))
	return buf.Bytes()
}

// The picture comes out of a book, so its header is attacker-controlled. A
// claim of billions of pixels must cost a header read, not an allocation.
func TestNormalize_RefusesAnOversizedPicture(t *testing.T) {
	cases := map[string][]byte{
		"enormous square": bombHeader(60000, 60000),
		"enormous width":  bombHeader(1<<20, 4),
		"enormous height": bombHeader(4, 1<<20),
		"negative width":  bombHeader(-8, 8),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			out, mime := fb2image.Normalize(data)
			if mime != "" || out != nil {
				t.Fatalf("accepted an oversized picture: %q, %d bytes", mime, len(out))
			}
		})
	}
}

// A transparent picture handed straight to a JPEG encoder loses its alpha and
// the clear areas come out black. The fixture is a TIFF on purpose: x/image's
// BMP encoder cannot write alpha at all, so a BMP fixture would pass whatever
// the code did. Real 32-bit BMPs in the wild do carry it, and they decode.
func TestNormalize_KeepsTransparency(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			src.Set(x, y, color.NRGBA{R: 0xFF, A: 0x00})
		}
	}
	var raw bytes.Buffer
	if err := tiff.Encode(&raw, src, nil); err != nil {
		t.Fatalf("encode tiff: %v", err)
	}

	out, mime := fb2image.Normalize(raw.Bytes())
	if mime != "image/png" {
		t.Fatalf("mime = %q, want image/png", mime)
	}
	got, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, _, _, a := got.At(1, 1).RGBA(); a != 0 {
		t.Fatalf("alpha = %d, want 0: transparency was flattened", a)
	}
}

// TestNormalize_RefusesTheBombBeforeAllocating is the test the outcome-only
// case above cannot be: a forged header is already refused today, but only
// because the decoder runs out of bytes — after it has allocated from the
// size the header claimed. Seventy bytes of book bought 1.5 GB. The refusal
// has to happen before that, so this measures the cost rather than the answer.
func TestNormalize_RefusesTheBombBeforeAllocating(t *testing.T) {
	data := bombHeader(20000, 20000)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	out, mime := fb2image.Normalize(data)
	runtime.ReadMemStats(&after)

	if mime != "" || out != nil {
		t.Fatalf("accepted a forged header: %q", mime)
	}
	const budget = 32 << 20
	if grew := after.TotalAlloc - before.TotalAlloc; grew > budget {
		t.Fatalf("allocated %.1f MB for a %d-byte payload; the header was believed before it was checked",
			float64(grew)/(1<<20), len(data))
	}
}

// The ceiling protects memory, but set too low it silently drops real covers.
// The largest BMP or TIFF in the catalog is 1155x790, and the widest side
// anywhere is 1328; a picture of that size has to still come through.
func TestNormalize_AcceptsTheLargestPictureTheLibraryHolds(t *testing.T) {
	for _, size := range []image.Point{{X: 1155, Y: 790}, {X: 1328, Y: 562}} {
		var raw bytes.Buffer
		if err := bmp.Encode(&raw, image.NewRGBA(image.Rectangle{Max: size})); err != nil {
			t.Fatalf("encode: %v", err)
		}
		out, mime := fb2image.Normalize(raw.Bytes())
		if mime != "image/png" {
			t.Fatalf("%dx%d was refused: mime = %q", size.X, size.Y, mime)
		}
		got, err := png.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if b := got.Bounds(); b.Dx() != size.X || b.Dy() != size.Y {
			t.Fatalf("bounds = %v, want %v", b, size)
		}
	}
}
