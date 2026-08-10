package parser

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"golang.org/x/image/bmp"
)

func coverBook(t *testing.T, payload []byte, declared string) string {
	t.Helper()
	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns:l="http://www.w3.org/1999/xlink">
 <description><title-info>
  <book-title>Test</book-title>
  <coverpage><image l:href="#cover"/></coverpage>
 </title-info></description>
 <body><section><p>text</p></section></body>
 <binary id="cover" content-type=%q>%s</binary>
</FictionBook>`, declared, base64.StdEncoding.EncodeToString(payload))
}

func bmpPayload(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 6, 6))
	for y := 0; y < 6; y++ {
		for x := 0; x < 6; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 40), G: uint8(y * 40), B: 0x20, A: 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := bmp.Encode(&buf, img); err != nil {
		t.Fatalf("encode bmp: %v", err)
	}
	return buf.Bytes()
}

// The scanner's cover is written to disk verbatim and served to the browser
// by content sniffing, and the EPUB generator matches it against the body
// images by comparing bytes. All three want the same normalised picture, so
// the conversion belongs here rather than in each of them.
func TestParse_BMPCoverArrivesRenderable(t *testing.T) {
	book, err := NewFB2Parser(true).Parse(strings.NewReader(coverBook(t, bmpPayload(t), "image/bmp")))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(book.Cover) == 0 {
		t.Fatal("no cover was extracted")
	}
	if _, err := png.Decode(bytes.NewReader(book.Cover)); err != nil {
		t.Fatalf("the cover is not a picture a browser will draw: %v", err)
	}
}

// A cover that is no picture at all is worse than no cover: the catalog
// shows a broken image where the placeholder belongs.
func TestParse_UnplaceableCoverIsDropped(t *testing.T) {
	book, err := NewFB2Parser(true).Parse(strings.NewReader(
		coverBook(t, []byte("<html><body>not an image</body></html>"), "image/jpeg")))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(book.Cover) != 0 {
		t.Fatalf("kept %d bytes that are not a picture", len(book.Cover))
	}
}

// A JPEG cover must reach the disk exactly as the book stored it.
func TestParse_JPEGCoverIsNotReEncoded(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 6, 6))
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode: %v", err)
	}
	original := buf.Bytes()

	book, err := NewFB2Parser(true).Parse(strings.NewReader(coverBook(t, original, "image/jpeg")))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !bytes.Equal(book.Cover, original) {
		t.Fatalf("the cover was rewritten: %d bytes in, %d out", len(original), len(book.Cover))
	}
}
