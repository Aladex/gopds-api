package converter

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"strings"
	"testing"
	"time"

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
	policy := testPreviewImagePolicy()
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

	tight := testPreviewImagePolicy()
	tight.MaxBytes = len(png) - 1
	_, _, err := PreparePreviewImage(png, tight)
	if !errors.Is(err, ErrPreviewImageTooLarge) {
		t.Errorf("byte-cap refusal: err = %v, want ErrPreviewImageTooLarge", err)
	}

	small := testPreviewImagePolicy()
	small.MaxPixels = 32*32 - 1
	_, _, err = PreparePreviewImage(png, small)
	if !errors.Is(err, ErrPreviewImageDimensions) {
		t.Errorf("pixel-cap refusal: err = %v, want ErrPreviewImageDimensions", err)
	}

	if _, _, err := PreparePreviewImage(png, testPreviewImagePolicy()); err != nil {
		t.Errorf("the same picture was refused under a policy that allows it: %v", err)
	}
}

// A forged header costs a header read, not an allocation. Seventy bytes
// claiming 20000x20000 once bought 1.5 GB on the EPUB path; both
// PreparePreviewImage and fb2image.Normalize read the dimensions before any
// pixel allocation, so this stays a header read here too.
//
// fb2image.Normalize rejects this on its own dimension cap, and that refusal
// surfaces here as ErrPreviewImageDimensions — a policy outcome, not
// corruption. Folding it into Corrupt (the previous behavior) buried a
// tunable signal under broken-bytes noise.
func TestPreparePreviewImage_RefusesAForgedHeader(t *testing.T) {
	_, _, err := PreparePreviewImage(forgeBMP(20000, 20000), testPreviewImagePolicy())
	if err == nil {
		t.Fatal("a header claiming 400 megapixels was accepted")
	}
	if !errors.Is(err, ErrPreviewImageDimensions) {
		t.Errorf("forged header: err = %v, want ErrPreviewImageDimensions", err)
	}
}

// The defect this function exists to close: a BMP header declaring 1048576x4
// sits under the preview policy pixel cap (4 MP below 32) but is refused by
// fb2image.Normalize on its own maxDimension. The previous gate let the bytes
// through and issued an address the handler could never satisfy;
// PreparePreviewImage must refuse too, because the same call decides and
// prepares. The refusal must arrive as Dimensions: it is a size outcome, not
// corruption, and the catalog counts the two separately.
func TestPreparePreviewImage_BMPWideRejectedByNormalize(t *testing.T) {
	_, _, err := PreparePreviewImage(forgeBMP(1048576, 4), testPreviewImagePolicy())
	if err == nil {
		t.Fatal("a header that fb2image.Normalize refuses was accepted — decision and preparation have diverged")
	}
	if !errors.Is(err, ErrPreviewImageDimensions) {
		t.Errorf("BMP 1048576x4: err = %v, want ErrPreviewImageDimensions", err)
	}
}

// TestPreparePreviewImage_NormalizeOversizeMapsToDimensions pins the mapping
// between fb2image's cap refusals and the preview's Dimensions reason across
// the shapes that hit Normalize's three sub-caps. Each must come out as
// Dimensions, never as Corrupt: they are size outcomes, and the catalog
// counter that drives policy must not be split across two sentinels.
func TestPreparePreviewImage_NormalizeOversizeMapsToDimensions(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		// Width past maxDimension — the original defect case. Pixel count
		// (4 MP) is below the preview cap, but the per-side cap refuses it.
		{"width past per-side cap", forgeBMP(1048576, 4)},
		// Height past maxDimension — symmetric to the above; the per-side
		// cap has to bite in both directions, not only width.
		{"height past per-side cap", forgeBMP(4, 1048576)},
		// Pixels past fb2image's maxPixels while each side stays under
		// maxDimension: 4000x4000 = 16 MP, maxPixels = 4 MP. Exercises the
		// separate pixel-count cap inside Normalize.
		{"pixels past Normalize cap", forgeBMP(4000, 4000)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := PreparePreviewImage(tc.data, testPreviewImagePolicy())
			if err == nil {
				t.Fatal("accepted an oversized forged header")
			}
			if !errors.Is(err, ErrPreviewImageDimensions) {
				t.Errorf("err = %v, want ErrPreviewImageDimensions", err)
			}
			// And explicitly not Corrupt: that is the regression this test
			// exists to prevent.
			if errors.Is(err, ErrPreviewImageCorrupt) {
				t.Errorf("err = %v; a size refusal must not land in the corrupt bucket", err)
			}
		})
	}
}

// TestPreparePreviewImage_TruncatedBMPMapsToCorrupt is the other half of the
// mapping: a payload whose format is recognized but whose bytes do not
// decode stays in Corrupt. Together with the dimensions test above it pins
// that the two reasons travel through PreparePreviewImage as two reasons,
// not one.
func TestPreparePreviewImage_TruncatedBMPMapsToCorrupt(t *testing.T) {
	// Real BMP, header only — magic is intact, so Classify passes it; the
	// decode then fails inside fb2image.Normalize as Undecodable.
	truncated := realBMP(t, 4, 4)[:54]
	_, _, err := PreparePreviewImage(truncated, testPreviewImagePolicy())
	if err == nil {
		t.Fatal("accepted a truncated BMP")
	}
	if !errors.Is(err, ErrPreviewImageCorrupt) {
		t.Errorf("err = %v, want ErrPreviewImageCorrupt", err)
	}
	if errors.Is(err, ErrPreviewImageDimensions) {
		t.Errorf("a corrupt payload must not be misreported as a size refusal")
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
	preview, _, err := PreparePreviewImage(src, testPreviewImagePolicy())
	if err != nil {
		t.Fatalf("baseline prepare: %v", err)
	}
	policy := testPreviewImagePolicy()
	policy.MaxBytes = len(src)
	if policy.MaxBytes >= len(preview) {
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

	first, err := BuildPreviewImages(context.Background(), bins, "/preview/img", testPreviewImagePolicy())
	if err != nil {
		t.Fatalf("BuildPreviewImages: %v", err)
	}
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
		again, err := BuildPreviewImages(context.Background(), bins, "/preview/img", testPreviewImagePolicy())
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if fmt.Sprint(again.Index) != fmt.Sprint(first.Index) {
			t.Fatalf("run %d produced %v, first run produced %v", i, again.Index, first.Index)
		}
	}
}

// A context already canceled before the call must report the cancellation and
// must not have run any of the per-binary work. The loop checks ctx at the
// top of each iteration, before PreparePreviewImage, so on a canceled ctx
// even the first iteration returns without having decoded or transcode one
// picture. The proof is observable: the Index stays empty even though every
// binary in the map is a valid PNG that would otherwise be admitted.
func TestBuildPreviewImages_CanceledBeforeStartDoesNoWork(t *testing.T) {
	png := uniformImage(t, "png", 8, 8)
	bins := map[string]FB2Binary{
		"a_first":  {Data: png},
		"b_second": {Data: png},
		"c_third":  {Data: png},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out, err := BuildPreviewImages(ctx, bins, "/preview/img", testPreviewImagePolicy())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want a wrapping of context.Canceled", err)
	}
	if len(out.Index) != 0 {
		t.Errorf("a canceled ctx must not produce any ordinals, got %d: %v",
			len(out.Index), out.Index)
	}
}

// A cancel that arrives while the loop is running must stop before the end of
// the map. There is no production hook to fire the cancel at an exact
// iteration, so this is a timing test: enough binaries that the loop cannot
// finish inside the cancel window, and a generous wait on the way back. The
// assertion is shape (error wraps context.Canceled and not every binary was
// admitted), not an exact count — the count depends on where the scheduler
// was when the cancel landed.
func TestBuildPreviewImages_CancelMidWorkStopsBeforeEnd(t *testing.T) {
	png := uniformImage(t, "png", 8, 8)
	const binCount = 2000
	bins := make(map[string]FB2Binary, binCount)
	for i := 0; i < binCount; i++ {
		bins[fmt.Sprintf("img_%04d", i)] = FB2Binary{Data: png}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type result struct {
		out PreviewImages
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := BuildPreviewImages(ctx, bins, "/preview/img", testPreviewImagePolicy())
		done <- result{out, err}
	}()

	// Let the loop process some binaries before the cancel reaches the next
	// iteration's ctx check. Two milliseconds is far below the time the full
	// run takes on any CI we run, and far above one iteration.
	time.Sleep(2 * time.Millisecond)
	cancel()

	select {
	case r := <-done:
		if !errors.Is(r.err, context.Canceled) {
			t.Fatalf("err = %v, want a wrapping of context.Canceled", r.err)
		}
		if len(r.out.Index) == binCount {
			t.Fatalf("the cancel did not stop the loop — every binary was processed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("BuildPreviewImages did not return within 5s of cancel")
	}
}

// A non-canceled context is the same as no context at all: the loop runs to
// the end and returns the same result the function did before ctx was added.
// This is the regression guard — if a future change makes the ctx check
// misfire on a live context, this test catches it.
func TestBuildPreviewImages_LiveContextMatchesNoContextBaseline(t *testing.T) {
	png := uniformImage(t, "png", 8, 8)
	bins := map[string]FB2Binary{
		"a_first":  {Data: png},
		"b_second": {Data: png},
		"c_bad":    {Data: []byte("не картинка")},
	}

	out, err := BuildPreviewImages(context.Background(), bins, "/preview/img", testPreviewImagePolicy())
	if err != nil {
		t.Fatalf("a live ctx must not produce an error: %v", err)
	}
	if out.Index["a_first"] != 1 || out.Index["b_second"] != 2 {
		t.Fatalf("ordinals follow sorted ids, got %v", out.Index)
	}
	if _, ok := out.Index["c_bad"]; ok {
		t.Fatal("a refused binary took an ordinal")
	}
	if got := out.URL("a_first"); got != "/preview/img/1" {
		t.Fatalf("URL = %q", got)
	}
}
