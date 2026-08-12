package converter

import (
	"archive/zip"
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"testing"

	"gopds-api/internal/parser"
)

// pngPixel returns a tiny PNG whose red channel varies, so several binaries
// differ in content while staying pictures a reader can draw.
func pngPixel(t *testing.T, red byte) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			img.Set(x, y, color.RGBA{R: red, G: 0x30, B: 0x40, A: 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// expectedImageOrder is the sequence the images must appear in, everywhere
// they appear.
func expectedImageOrder() []string {
	out := make([]string, 0, 12)
	for i := 1; i <= 12; i++ {
		out = append(out, fmt.Sprintf("image_%03d.png", i))
	}
	return out
}

// buildsToObserve is how many builds an order assertion inspects. One is not
// enough: Go iterates a small map by picking a random start and walking, so an
// unordered walk over a dozen images produces only a handful of distinct
// orders and lands on the right one often. Measured against a deliberately
// unordered manifest, a single-build check passed one run in five.
const buildsToObserve = 10

func manyImageDoc(t *testing.T) (*FB2Document, *parser.BookFile) {
	t.Helper()
	binaries := map[string]FB2Binary{}
	// Names deliberately out of insertion order: a map keyed by them iterates
	// unpredictably, which is exactly what the ordering has to survive.
	for _, id := range []string{
		"zeta", "alpha", "mid", "beta", "omega", "gamma",
		"delta", "kappa", "sigma", "tau", "iota", "rho",
	} {
		binaries[id] = FB2Binary{Data: pngPixel(t, byte(len(id)*20)), MIME: "image/png"}
	}
	body := &FB2BodySection{Content: []*FB2ContentItem{
		{Section: &FB2BodySection{
			Title: "ГЛАВА",
			Content: []*FB2ContentItem{
				{Paragraph: &FB2Paragraph{Text: "текст", Content: []*FB2InlineElement{
					{Type: InlineTypeText, Content: "текст"},
				}}},
			},
		}},
	}}
	return &FB2Document{Title: "КНИГА", Body: body, Binary: binaries},
		&parser.BookFile{Title: "КНИГА", Language: "ru"}
}

func generateEPUB(t *testing.T) []byte {
	t.Helper()
	doc, bookFile := manyImageDoc(t)
	rc, err := NewEPUBGenerator().GenerateEPUB(doc, bookFile)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return data
}

// Two builds of one book must be the same file. Go randomizes map iteration,
// and that randomness used to reach the output: the manifest listed the same
// pictures in a different order every run, so no two builds were identical
// and no build could be compared by hash. A run-count check ("several builds
// agreed") would only sample the randomness; equality of two builds plus the
// order assertions below are what actually hold it.
func TestGenerateEPUB_TwoBuildsAreIdentical(t *testing.T) {
	first := generateEPUB(t)
	second := generateEPUB(t)
	if !bytes.Equal(first, second) {
		t.Errorf("two builds of one book differ: %d vs %d bytes", len(first), len(second))
	}
}

// The image entries must be written in one fixed order. Reordering zip
// entries costs nothing on its own — each entry is compressed separately —
// but a reader of this code should not have to work out which of the walks
// over the images matters, so all of them are ordered and all are pinned.
func TestGenerateEPUB_ImageEntriesAreInFixedOrder(t *testing.T) {
	for build := 0; build < buildsToObserve; build++ {
		checkImageEntryOrder(t)
	}
}

func checkImageEntryOrder(t *testing.T) {
	t.Helper()
	data := generateEPUB(t)
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open epub: %v", err)
	}
	var got []string
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "OEBPS/images/") {
			got = append(got, strings.TrimPrefix(f.Name, "OEBPS/images/"))
		}
	}
	want := expectedImageOrder()
	if len(got) != len(want) {
		t.Fatalf("packed %d images, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("image entries in order %v, want %v", got, want)
		}
	}
}

// The manifest is one compressed stream, so the order of its <item> lines is
// what actually moved the output size from run to run. This is the assertion
// that fails if the manifest walk goes back to iterating the map.
func TestGenerateEPUB_ManifestListsImagesInFixedOrder(t *testing.T) {
	for build := 0; build < buildsToObserve; build++ {
		checkManifestOrder(t)
	}
}

func checkManifestOrder(t *testing.T) {
	t.Helper()
	data := generateEPUB(t)
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open epub: %v", err)
	}
	var opf string
	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, ".opf") {
			continue
		}
		rc, oerr := f.Open()
		if oerr != nil {
			t.Fatalf("open opf: %v", oerr)
		}
		body, rerr := io.ReadAll(rc)
		rc.Close()
		if rerr != nil {
			t.Fatalf("read opf: %v", rerr)
		}
		opf = string(body)
	}
	if opf == "" {
		t.Fatal("the EPUB carries no OPF")
	}
	// The order is compared as a sequence, not by "each appears after the
	// previous one". A positional check passed two runs out of five against a
	// deliberately unordered manifest: with a handful of images a random
	// permutation lands increasing often enough to let the mutant through.
	var got []string
	for _, line := range strings.Split(opf, "\n") {
		const marker = `href="images/`
		at := strings.Index(line, marker)
		if at == -1 {
			continue
		}
		rest := line[at+len(marker):]
		end := strings.Index(rest, `"`)
		if end == -1 {
			t.Fatalf("unterminated href in the manifest: %q", line)
		}
		got = append(got, rest[:end])
	}
	want := expectedImageOrder()
	if len(got) != len(want) {
		t.Fatalf("manifest lists %d images, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("manifest order %v, want %v — the walk over the images is unordered", got, want)
		}
	}
}
