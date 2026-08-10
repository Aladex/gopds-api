package converter

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"golang.org/x/image/bmp"

	"gopds-api/internal/parser"
)

func bmpBytes(t *testing.T) []byte {
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

// A BMP is a real picture that EPUB readers are not required to draw. Storing
// it verbatim produced image_001.bin, which every reader ignored, so the book
// simply had no illustration.
func TestBuildImages_BMPArrivesAsAPictureAReaderCanDraw(t *testing.T) {
	doc := &FB2Document{
		Body:   &FB2BodySection{},
		Binary: map[string]FB2Binary{"pic": {Data: bmpBytes(t), MIME: "image/bmp"}},
	}

	img, ok := buildImages(doc)["pic"]
	if !ok {
		t.Fatal("the binary vanished from the built images")
	}
	if img.MediaType != "image/png" || img.Filename != "image_001.png" {
		t.Fatalf("got %s / %s, want image/png / image_001.png", img.MediaType, img.Filename)
	}
	if _, err := png.Decode(bytes.NewReader(img.Data)); err != nil {
		t.Fatalf("the stored bytes are not a png: %v", err)
	}
}

// A payload that is no picture at all used to be written into the EPUB as
// image_001.bin and listed in the manifest. Nothing opens it, and the <img>
// pointing at it renders as a broken image; dropping it leaves the paragraph's
// [image] placeholder instead, which at least says what happened.
func TestBuildImages_UnplaceablePayloadIsDropped(t *testing.T) {
	cases := map[string][]byte{
		"html claiming to be svg": []byte(`<?xml version="1.0"?><html></html>`),
		"prose":                   []byte("just some prose, not an image"),
		"truncated bmp":           bmpBytes(t)[:8],
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			doc := &FB2Document{
				Body:   &FB2BodySection{},
				Binary: map[string]FB2Binary{"x.svg": {Data: data, MIME: "image/svg+xml"}},
			}
			if img, ok := buildImages(doc)["x.svg"]; ok {
				t.Fatalf("kept an unplaceable payload as %s / %s", img.MediaType, img.Filename)
			}
		})
	}
}

func TestBuildCover_BMPCoverArrivesAsAPicture(t *testing.T) {
	cover := buildCover(&parser.BookFile{Cover: bmpBytes(t)}, nil)
	if cover == nil {
		t.Fatal("no cover was built")
	}
	if cover.Image.MediaType != "image/png" || cover.Image.Filename != "cover.png" {
		t.Fatalf("got %s / %s, want image/png / cover.png",
			cover.Image.MediaType, cover.Image.Filename)
	}
	if _, err := png.Decode(bytes.NewReader(cover.Image.Data)); err != nil {
		t.Fatalf("the cover bytes are not a png: %v", err)
	}
}

// An unusable cover is the same thing as no cover. Writing cover.bin and
// declaring it in the OPF gave readers a cover page that renders empty.
func TestBuildCover_UnplaceableCoverIsNoCover(t *testing.T) {
	cover := buildCover(&parser.BookFile{Cover: []byte("<html></html>")}, nil)
	if cover != nil {
		t.Fatalf("built a cover from a non-picture: %s / %s",
			cover.Image.MediaType, cover.Image.Filename)
	}
}

// The generator recognizes that the cover is also one of the body images by
// comparing bytes, and both sides are normalised now. If the two sides
// normalised differently the same picture would be written into the EPUB
// twice, under two names.
func TestBuildCover_StillMatchesTheBodyImageAfterNormalising(t *testing.T) {
	raw := bmpBytes(t)
	doc := &FB2Document{
		Body:   &FB2BodySection{},
		Binary: map[string]FB2Binary{"pic": {Data: raw, MIME: "image/bmp"}},
	}
	images := buildImages(doc)

	cover := buildCover(&parser.BookFile{Cover: raw}, images)
	if cover == nil {
		t.Fatal("no cover was built")
	}
	if !cover.FromImages {
		t.Fatal("the cover was stored separately from the identical body image")
	}
}
