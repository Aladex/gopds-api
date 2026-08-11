package converter

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"strings"
	"testing"

	"golang.org/x/image/bmp"
)

// forgeBMP writes a BMP header that declares the given canvas without
// allocating pixels for it. Both the policy gate and fb2image.Normalize read
// the dimensions from this header before any pixel allocation, so a forged
// canvas costs a header read, not the gigabytes a naive decoder would buy.
func forgeBMP(width, height int) []byte {
	var buf strings.Builder
	buf.WriteString("BM")
	var head []byte
	head = binary.LittleEndian.AppendUint32(head, 0)
	head = binary.LittleEndian.AppendUint32(head, 0)
	head = binary.LittleEndian.AppendUint32(head, 54)
	head = binary.LittleEndian.AppendUint32(head, 40)
	head = binary.LittleEndian.AppendUint32(head, uint32(width))
	head = binary.LittleEndian.AppendUint32(head, uint32(height))
	head = binary.LittleEndian.AppendUint16(head, 1)
	head = binary.LittleEndian.AppendUint16(head, 24)
	head = append(head, make([]byte, 40)...)
	buf.Write(head)
	return []byte(buf.String())
}

// realBMP encodes a solid RGBA rectangle as a real BMP, so a test can drive
// the transcode path of fb2image.Normalize against a payload that actually
// decodes. uniformImage in preview_test.go does not cover BMP, and adding it
// there would touch a file outside this task's scope.
func realBMP(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	var buf bytes.Buffer
	if err := bmp.Encode(&buf, img); err != nil {
		t.Fatalf("bmp encode: %v", err)
	}
	return buf.Bytes()
}

// PreparePreviewImage is the one call that decides whether a binary becomes a
// preview picture and produces the bytes the reader will receive. These cases
// were the AcceptPreviewImage table; each refusal now also pins its typed
// reason, because callers count by cause.
func TestPreparePreviewImage(t *testing.T) {
	png := uniformImage(t, "png", 8, 8)
	jpeg := uniformImage(t, "jpeg", 8, 8)
	gif := uniformImage(t, "gif", 8, 8)

	cases := []struct {
		name     string
		data     []byte
		wantErr  error
		wantMime string
	}{
		{"png", png, nil, "image/png"},
		{"jpeg", jpeg, nil, "image/jpeg"},
		{"gif", gif, nil, "image/gif"},
		{"empty", nil, ErrPreviewImageUnsupported, ""},
		{"prose", []byte("это не картинка, а просто текст"), ErrPreviewImageUnsupported, ""},
		// An XML document is not an image, even one whose prolog an
		// SVG-sniffer would have to walk past.
		{"html claiming to be an image", []byte(`<?xml version="1.0"?><html></html>`), ErrPreviewImageUnsupported, ""},
		// An XML document that can carry script must never be served under
		// the reader's origin, whatever the book declares it to be.
		{"svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), ErrPreviewImageUnsupported, ""},
		// Four bytes do not complete the PNG magic, so Classify sees no
		// format at all — the refusal reason is unsupported, not corrupt.
		{"truncated png magic", png[:4], ErrPreviewImageUnsupported, ""},
		// The PNG magic is whole but the header that follows is not, so
		// fb2image.Normalize passes the bytes through and DecodeConfig
		// fails on them: corrupt, a different reason from the no-magic
		// case above.
		{"png magic without header", png[:16], ErrPreviewImageCorrupt, ""},
	}
	policy := testPreviewPolicy()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, mime, err := PreparePreviewImage(tc.data, policy)
			if tc.wantErr != nil {
				if err == nil {
					t.Fatalf("PreparePreviewImage accepted %s (mime %q, %d bytes), want error wrapping %v",
						tc.name, mime, len(payload), tc.wantErr)
				}
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("PreparePreviewImage(%s) err = %v, want a wrapping of %v", tc.name, err, tc.wantErr)
				}
				if payload != nil {
					t.Errorf("on refusal, payload must be nil, got %d bytes", len(payload))
				}
				if mime != "" {
					t.Errorf("on refusal, mime must be empty, got %q", mime)
				}
				return
			}
			if err != nil {
				t.Fatalf("PreparePreviewImage(%s) refused a valid payload: %v", tc.name, err)
			}
			if mime != tc.wantMime {
				t.Errorf("mime = %q, want %q", mime, tc.wantMime)
			}
			if len(payload) == 0 {
				t.Errorf("payload empty for accepted %s", tc.name)
			}
		})
	}
}

// The byte cap and the pixel cap are separate limits and each has to bite on
// its own: a small payload can declare an enormous canvas, and a large payload
// can declare a small one. Each refusal arrives with its own typed reason.
func TestPreparePreviewImage_CapsBiteSeparately(t *testing.T) {
	png := uniformImage(t, "png", 32, 32)

	tight := testPreviewPolicy()
	tight.MaxImageBytes = len(png) - 1
	_, _, err := PreparePreviewImage(png, tight)
	if !errors.Is(err, ErrPreviewImageTooLarge) {
		t.Errorf("byte-cap refusal: err = %v, want ErrPreviewImageTooLarge", err)
	}

	small := testPreviewPolicy()
	small.MaxImagePixels = 32*32 - 1
	_, _, err = PreparePreviewImage(png, small)
	if !errors.Is(err, ErrPreviewImageDimensions) {
		t.Errorf("pixel-cap refusal: err = %v, want ErrPreviewImageDimensions", err)
	}

	if _, _, err := PreparePreviewImage(png, testPreviewPolicy()); err != nil {
		t.Errorf("the same picture was refused under a policy that allows it: %v", err)
	}
}

// A forged header costs a header read, not an allocation. Seventy bytes
// claiming 20000x20000 once bought 1.5 GB on the EPUB path; both
// PreparePreviewImage and fb2image.Normalize read the dimensions before any
// pixel allocation, so this stays a header read here too.
func TestPreparePreviewImage_RefusesAForgedHeader(t *testing.T) {
	_, _, err := PreparePreviewImage(forgeBMP(20000, 20000), testPreviewPolicy())
	if err == nil {
		t.Fatal("a header claiming 400 megapixels was accepted")
	}
	// fb2image.Normalize rejects this on its own dimension cap; the
	// refusal reason is opaque to us, so the gate surfaces it as corrupt.
	if !errors.Is(err, ErrPreviewImageCorrupt) {
		t.Errorf("forged header: err = %v, want ErrPreviewImageCorrupt", err)
	}
}

// The defect this function exists to close: a BMP header declaring 1048576x4
// sits under the policy pixel cap (4 MP below 32) but is refused by
// fb2image.Normalize on its own maxDimension. The previous gate let the bytes
// through and issued an address the handler could never satisfy;
// PreparePreviewImage must refuse too, because the same call decides and
// prepares.
func TestPreparePreviewImage_BMPWideRejectedByNormalize(t *testing.T) {
	_, _, err := PreparePreviewImage(forgeBMP(1048576, 4), testPreviewPolicy())
	if err == nil {
		t.Fatal("a header that fb2image.Normalize refuses was accepted — decision and preparation have diverged")
	}
	if !errors.Is(err, ErrPreviewImageCorrupt) {
		t.Errorf("BMP 1048576x4: err = %v, want ErrPreviewImageCorrupt", err)
	}
}

// A transcoded payload can come out bigger than the source: a tiny BMP
// expands to a real PNG whose bytes may clear the result-side cap even when
// the input was well under it. The result gate is the only thing that
// catches that, so it has its own test and its own typed reason.
func TestPreparePreviewImage_RefusesOversizedResult(t *testing.T) {
	src := realBMP(t, 1, 1)
	// Render the transcoded PNG once under a permissive policy to learn the
	// result size, then set the byte cap above the source BMP but below the
	// transcoded PNG. That window is the only one where the result-side
	// gate alone can bite.
	preview, _, err := PreparePreviewImage(src, testPreviewPolicy())
	if err != nil {
		t.Fatalf("baseline prepare: %v", err)
	}
	policy := testPreviewPolicy()
	policy.MaxImageBytes = len(src)
	if policy.MaxImageBytes >= len(preview) {
		t.Skipf("transcoded PNG (%d bytes) is not larger than the source BMP (%d bytes); cannot isolate the result gate",
			len(preview), len(src))
	}
	_, _, err = PreparePreviewImage(src, policy)
	if !errors.Is(err, ErrPreviewImageTooLargeResult) {
		t.Errorf("oversized result: err = %v, want ErrPreviewImageTooLargeResult", err)
	}
}

// The ordinals are the contract between the renderer that emits an address and
// the handler that answers it, so they must not depend on map iteration order,
// and a refused binary must not consume one.
func TestBuildPreviewImages_StableOrdinalsSkipRefused(t *testing.T) {
	png := uniformImage(t, "png", 8, 8)
	bins := map[string]FB2Binary{
		"b_second": {Data: png},
		"a_first":  {Data: png},
		"c_bad":    {Data: []byte("не картинка")},
	}

	first := BuildPreviewImages(bins, "/preview/img", testPreviewPolicy())
	if first.Index["a_first"] != 1 || first.Index["b_second"] != 2 {
		t.Fatalf("ordinals follow sorted ids, got %v", first.Index)
	}
	if _, ok := first.Index["c_bad"]; ok {
		t.Fatal("a refused binary took an ordinal")
	}
	if got := first.URL("a_first"); got != "/preview/img/1" {
		t.Fatalf("URL = %q", got)
	}
	if got := first.URL("c_bad"); got != "" {
		t.Fatalf("a refused binary got the address %q", got)
	}

	// Twenty rebuilds: map iteration order differs between them, the answer
	// must not.
	for i := 0; i < 20; i++ {
		again := BuildPreviewImages(bins, "/preview/img", testPreviewPolicy())
		if fmt.Sprint(again.Index) != fmt.Sprint(first.Index) {
			t.Fatalf("run %d produced %v, first run produced %v", i, again.Index, first.Index)
		}
	}
}
