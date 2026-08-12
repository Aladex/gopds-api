package converter

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/image/bmp"

	"gopds-api/internal/fb2image"
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

	first, err := BuildPreviewImages(context.Background(), bins, testPreviewImageBase(), testPreviewImagePolicy(), 0)
	if err != nil {
		t.Fatalf("BuildPreviewImages: %v", err)
	}
	if ord, ok := first.Projection().Ordinal("a_first"); !ok || ord != 1 {
		t.Fatalf("a_first ordinal = (%d, %v), want (1, true)", ord, ok)
	}
	if ord, ok := first.Projection().Ordinal("b_second"); !ok || ord != 2 {
		t.Fatalf("b_second ordinal = (%d, %v), want (2, true)", ord, ok)
	}
	if _, ok := first.Projection().Ordinal("c_bad"); ok {
		t.Fatal("a refused binary took an ordinal")
	}
	if got := first.Projection().URL("a_first"); got != testPreviewImageBase().URLFor(1) {
		t.Fatalf("URL = %q", got)
	}
	if got := first.Projection().URL("c_bad"); got != "" {
		t.Fatalf("a refused binary got the address %q", got)
	}

	// Twenty rebuilds: map iteration order differs between them, the answer
	// must not. The set has no public iterator, so compare through the
	// read-only API the renderer uses — URL per id — across every id the
	// fixture put in.
	for i := 0; i < 20; i++ {
		again, err := BuildPreviewImages(context.Background(), bins, testPreviewImageBase(), testPreviewImagePolicy(), 0)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		for id := range bins {
			if again.Projection().URL(id) != first.Projection().URL(id) {
				t.Fatalf("run %d: %q -> %q, first run -> %q", i, id, again.Projection().URL(id), first.Projection().URL(id))
			}
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

	out, err := BuildPreviewImages(ctx, bins, testPreviewImageBase(), testPreviewImagePolicy(), 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want a wrapping of context.Canceled", err)
	}
	if out.Len() != 0 {
		t.Errorf("a canceled ctx must not produce any ordinals, got %d", out.Len())
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
		out PreviewImageSet
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := BuildPreviewImages(ctx, bins, testPreviewImageBase(), testPreviewImagePolicy(), 0)
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
		if r.out.Len() == binCount {
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

	out, err := BuildPreviewImages(context.Background(), bins, testPreviewImageBase(), testPreviewImagePolicy(), 0)
	if err != nil {
		t.Fatalf("a live ctx must not produce an error: %v", err)
	}
	if ord, ok := out.Projection().Ordinal("a_first"); !ok || ord != 1 {
		t.Fatalf("a_first ordinal = (%d, %v), want (1, true)", ord, ok)
	}
	if ord, ok := out.Projection().Ordinal("b_second"); !ok || ord != 2 {
		t.Fatalf("b_second ordinal = (%d, %v), want (2, true)", ord, ok)
	}
	if _, ok := out.Projection().Ordinal("c_bad"); ok {
		t.Fatal("a refused binary took an ordinal")
	}
	if got := out.Projection().URL("a_first"); got != testPreviewImageBase().URLFor(1) {
		t.Fatalf("URL = %q", got)
	}
}

// The address the renderer puts in src must be assembled from parts the code
// itself produced, not from a string the test pinned. NewPreviewImageBase
// refuses inputs that would yield an address the renderer is not allowed to
// emit, and the table below pins each refusal: an empty revision, one with a
// space, a slash, or characters that would let a caller smuggle a scheme, a
// host, or a path traversal past the constructor.
func TestNewPreviewImageBase_RejectsInvalidInputs(t *testing.T) {
	cases := []struct {
		name     string
		bookID   int64
		revision string
	}{
		{"empty revision", 1, ""},
		{"revision with space", 1, "rev 1"},
		{"revision with slash", 1, "rev/1"},
		{"revision with dot-dot", 1, "rev..1"},
		{"revision with colon (scheme attempt)", 1, "rev://host"},
		{"revision with query separator", 1, "rev?x"},
		{"revision with fragment separator", 1, "rev#x"},
		{"revision with non-ascii", 1, "рев1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewPreviewImageBase(tc.bookID, tc.revision)
			if !errors.Is(err, ErrPreviewImageBaseInvalid) {
				t.Fatalf("err = %v, want a wrapping of ErrPreviewImageBaseInvalid", err)
			}
		})
	}
}

// The happy path produces a base that contains both the book id and the
// revision, in that order, under /preview/. The exact form is the code's to
// choose; the test pins the shape by asking the code, not by hard-coding a
// string the renderer is then forced to match.
func TestNewPreviewImageBase_AcceptsNormalCase(t *testing.T) {
	const bookID int64 = 42
	const revision = "rev1"
	base, err := NewPreviewImageBase(bookID, revision)
	if err != nil {
		t.Fatalf("a normal input must produce no error: %v", err)
	}
	s := base.String()
	if !strings.HasPrefix(s, "/preview/") {
		t.Errorf("base %q does not start with /preview/", s)
	}
	if !strings.Contains(s, "42") {
		t.Errorf("base %q does not carry the book id", s)
	}
	if !strings.Contains(s, revision) {
		t.Errorf("base %q does not carry the revision", s)
	}
}

// The address a renderer emits in src must equal the address the base
// produces through its own URLFor. This is the regression that an
// in-test-harcoded "wantSrc" cannot catch: a change in format that the test
// would have rubber-stamped. Asking the code closes that gap.
func TestNewPreviewImageBase_AddressMatchesRenderOutput(t *testing.T) {
	const bookID int64 = 7
	const revision = "v2"
	base, err := NewPreviewImageBase(bookID, revision)
	if err != nil {
		t.Fatalf("NewPreviewImageBase: %v", err)
	}
	pngData := uniformImage(t, "png", 4, 4)
	bins := map[string]FB2Binary{"cover": {Data: pngData}}
	images, err := BuildPreviewImages(context.Background(), bins, base, testPreviewImagePolicy(), 0)
	if err != nil {
		t.Fatalf("BuildPreviewImages: %v", err)
	}
	out, err := RenderChunkHTML(paraChunk(0, imagePara("cover")), images.Projection(), testPreviewPolicy())
	if err != nil {
		t.Fatalf("RenderChunkHTML: %v", err)
	}
	wantSrc := base.URLFor(1)
	if got := images.Projection().URL("cover"); got != wantSrc {
		t.Errorf("images.URL = %q, want the code-produced %q", got, wantSrc)
	}
	if !strings.Contains(out, `src="`+wantSrc+`"`) {
		t.Errorf("rendered output does not carry the code-produced address %q: %s", wantSrc, shorten(out))
	}
}

// MaxSide caps the longer side of the canvas for every format, not only the
// transcode path. Without it, a PNG declaring 1048576x4 is admitted (4 MP is
// under the pixel cap, and Normalize's own per-side cap never touches PNG)
// while a BMP of the same shape is refused by Normalize on transcode.
// The per-side answer is consistent across formats; the pixel answer is not
// (see PreviewImagePolicy.MaxSide for the two-layer contract).
func TestPreparePreviewImage_MaxSideAppliesToAllFormats(t *testing.T) {
	// A PNG carrying 1048576x4 in its IHDR. forgePNGDimensions rewrites the
	// header of a real 4x4 PNG, fixing CRC so DecodeConfig reads it.
	pngWide := forgePNGDimensions(t, uniformImage(t, "png", 4, 4), 1048576, 4)
	bmpWide := forgeBMP(1048576, 4)
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"png 1048576x4", pngWide},
		{"bmp 1048576x4", bmpWide},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := PreparePreviewImage(tc.data, testPreviewImagePolicy())
			if !errors.Is(err, ErrPreviewImageDimensions) {
				t.Errorf("err = %v, want ErrPreviewImageDimensions", err)
			}
		})
	}
}

// The fb2image refusal has to survive the wrapping in mapNormalizeError. The
// comment there promises errors.Is finds it, but `%v` breaks the chain. The
// outer reason (Dimensions) and the inner reason (fb2image.ErrTooLarge) must
// both be visible to errors.Is on the same error value.
func TestPreparePreviewImage_NormalizeErrorChainsThroughWrap(t *testing.T) {
	_, _, err := PreparePreviewImage(forgeBMP(1048576, 4), testPreviewImagePolicy())
	if err == nil {
		t.Fatal("expected an error for a forged BMP")
	}
	if !errors.Is(err, ErrPreviewImageDimensions) {
		t.Errorf("outer: err = %v, want ErrPreviewImageDimensions in the chain", err)
	}
	if !errors.Is(err, fb2image.ErrTooLarge) {
		t.Errorf("inner: err = %v, want fb2image.ErrTooLarge in the chain", err)
	}
}

// A non-positive book id has no business producing a base. Zero slips past
// today (it formats as "0", which is a parseable path segment), and a
// negative id is meaningless for a catalog row. Refuse both.
func TestNewPreviewImageBase_RejectsNonPositiveBookID(t *testing.T) {
	for _, bookID := range []int64{0, -1} {
		if _, err := NewPreviewImageBase(bookID, "rev1"); !errors.Is(err, ErrPreviewImageBaseInvalid) {
			t.Errorf("bookID=%d: err = %v, want ErrPreviewImageBaseInvalid", bookID, err)
		}
	}
}

// The zero value of PreviewImageBase must be unusable. A caller who forgot to
// build a base through the constructor would otherwise get addresses of the
// form "/N" out of URLFor — they look real, route somewhere, and lead
// nowhere. BuildPreviewImages is the boundary where the empty base meets the
// real pipeline, so it is where the refusal sits.
func TestBuildPreviewImages_RejectsZeroBase(t *testing.T) {
	var zero PreviewImageBase
	if zero.String() != "" {
		t.Fatalf("precondition: a zero base must have an empty path, got %q", zero.String())
	}
	bins := map[string]FB2Binary{"a": {Data: uniformImage(t, "png", 4, 4)}}
	out, err := BuildPreviewImages(context.Background(), bins, zero, testPreviewImagePolicy(), 0)
	if err == nil {
		t.Fatalf("a zero base must be rejected, got out with %d ordinals", out.Len())
	}
	if out.Len() != 0 {
		t.Errorf("on rejection, no ordinals must be assigned, got %d", out.Len())
	}
}

// BuildPreviewImages must reject an invalid policy even when the binary map
// is empty. The validate() call inside the loop only fires on the first
// iteration; with no binaries the loop never runs, and without an explicit
// pre-loop check the function would return (empty set, nil) — looking like
// "this book has no pictures", when in fact the policy was never configured.
func TestBuildPreviewImages_RejectsInvalidPolicyWithEmptyBins(t *testing.T) {
	var zeroPolicy PreviewImagePolicy
	_, err := BuildPreviewImages(context.Background(), nil, testPreviewImageBase(), zeroPolicy, 0)
	if !errors.Is(err, ErrPreviewImagePolicyInvalid) {
		t.Fatalf("err = %v, want ErrPreviewImagePolicyInvalid", err)
	}
}

// A policy with any non-positive limit is a caller bug, not a property of
// any one picture. The previous behavior — silently rejecting every image
// when a field was left at zero — was indistinguishable from a book whose
// every binary fails policy, and a misconfigured policy is very different
// from a hostile book. Both functions must surface it as a typed policy
// error, distinct from the per-image refusals.
func TestPreviewImagePolicy_ZeroOrNegativeFieldIsRejected(t *testing.T) {
	good := testPreviewImagePolicy()
	cases := []struct {
		name   string
		policy PreviewImagePolicy
	}{
		{"MaxSide zero", PreviewImagePolicy{MaxBytes: good.MaxBytes, MaxPixels: good.MaxPixels, MaxSide: 0}},
		{"MaxBytes zero", PreviewImagePolicy{MaxBytes: 0, MaxPixels: good.MaxPixels, MaxSide: good.MaxSide}},
		{"MaxPixels zero", PreviewImagePolicy{MaxBytes: good.MaxBytes, MaxPixels: 0, MaxSide: good.MaxSide}},
		{"all zero (the zero value)", PreviewImagePolicy{}},
		{"MaxSide negative", PreviewImagePolicy{MaxBytes: good.MaxBytes, MaxPixels: good.MaxPixels, MaxSide: -1}},
		{"MaxBytes negative", PreviewImagePolicy{MaxBytes: -1, MaxPixels: good.MaxPixels, MaxSide: good.MaxSide}},
		{"MaxPixels negative", PreviewImagePolicy{MaxBytes: good.MaxBytes, MaxPixels: -1, MaxSide: good.MaxSide}},
	}
	png := uniformImage(t, "png", 4, 4)
	for _, tc := range cases {
		t.Run(tc.name+"/PreparePreviewImage", func(t *testing.T) {
			_, _, err := PreparePreviewImage(png, tc.policy)
			if !errors.Is(err, ErrPreviewImagePolicyInvalid) {
				t.Fatalf("PreparePreviewImage: err = %v, want a wrapping of ErrPreviewImagePolicyInvalid", err)
			}
			// The error has to be distinguishable from any per-image
			// refusal: even the most common one (Unsupported) must not
			// match, otherwise a caller counting by cause lumps a
			// misconfigured policy in with a hostile book.
			if errors.Is(err, ErrPreviewImageUnsupported) ||
				errors.Is(err, ErrPreviewImageCorrupt) ||
				errors.Is(err, ErrPreviewImageDimensions) ||
				errors.Is(err, ErrPreviewImageTooLarge) ||
				errors.Is(err, ErrPreviewImageTooLargeResult) {
				t.Errorf("policy error matched a per-image sentinel: %v", err)
			}
		})
		t.Run(tc.name+"/BuildPreviewImages", func(t *testing.T) {
			bins := map[string]FB2Binary{"a": {Data: png}}
			_, err := BuildPreviewImages(context.Background(), bins, testPreviewImageBase(), tc.policy, 0)
			if !errors.Is(err, ErrPreviewImagePolicyInvalid) {
				t.Fatalf("BuildPreviewImages: err = %v, want a wrapping of ErrPreviewImagePolicyInvalid", err)
			}
		})
	}
}

// A properly configured policy still accepts a normal picture. This is the
// regression guard for the guard: if a future change flips the validation
// predicate and rejects every policy, the suite loses its image coverage
// silently. The error bucket has to stay empty here.
func TestPreviewImagePolicy_FullPolicyAcceptsAsBefore(t *testing.T) {
	png := uniformImage(t, "png", 8, 8)
	if _, _, err := PreparePreviewImage(png, testPreviewImagePolicy()); err != nil {
		t.Fatalf("a full policy must accept a normal picture, got %v", err)
	}
	bins := map[string]FB2Binary{"a": {Data: png}}
	set, err := BuildPreviewImages(context.Background(), bins, testPreviewImageBase(), testPreviewImagePolicy(), 0)
	if err != nil {
		t.Fatalf("BuildPreviewImages: %v", err)
	}
	if set.RefusalReason("a") != nil {
		t.Errorf("a full policy admitted 'a' but also recorded a refusal: %v", set.RefusalReason("a"))
	}
}

// Every prepared byte must survive — the previous Build discarded them,
// forcing the handler to redo the decode and transcode on demand. The set
// now returns the exact payload PreparePreviewImage produced, so the
// handler and the policy gate see the same bytes.
func TestBuildPreviewImages_KeepsPreparedBytes(t *testing.T) {
	policy := testPreviewImagePolicy()
	// A real PNG and a real BMP: BMP exercises the transcode path inside
	// PreparePreviewImage, PNG the pass-through.
	pngData := uniformImage(t, "png", 8, 8)
	bmpData := realBMP(t, 8, 8)
	bins := map[string]FB2Binary{
		"png_bin": {Data: pngData},
		"bmp_bin": {Data: bmpData},
	}
	wantPNG, wantPNGMIME, err := PreparePreviewImage(pngData, policy)
	if err != nil {
		t.Fatalf("PreparePreviewImage(png): %v", err)
	}
	wantBMP, wantBMPMIME, err := PreparePreviewImage(bmpData, policy)
	if err != nil {
		t.Fatalf("PreparePreviewImage(bmp): %v", err)
	}

	set, err := BuildPreviewImages(context.Background(), bins, testPreviewImageBase(), policy, 0)
	if err != nil {
		t.Fatalf("BuildPreviewImages: %v", err)
	}
	got := map[string]PreparedPreviewImage{}
	for _, img := range set.Images() {
		got[img.ID] = img
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 prepared images, got %d: %+v", len(got), got)
	}
	// Compare bytes through the public API and through PreparePreviewImage
	// directly — same value, no copy or re-encode in between.
	if !bytes.Equal(got["png_bin"].Payload, wantPNG) {
		t.Errorf("png payload drifted from PreparePreviewImage: got %d bytes, want %d",
			len(got["png_bin"].Payload), len(wantPNG))
	}
	if got["png_bin"].MIME != wantPNGMIME {
		t.Errorf("png MIME = %q, want %q", got["png_bin"].MIME, wantPNGMIME)
	}
	if !bytes.Equal(got["bmp_bin"].Payload, wantBMP) {
		t.Errorf("bmp payload drifted from PreparePreviewImage: got %d bytes, want %d",
			len(got["bmp_bin"].Payload), len(wantBMP))
	}
	if got["bmp_bin"].MIME != wantBMPMIME {
		t.Errorf("bmp MIME = %q, want %q", got["bmp_bin"].MIME, wantBMPMIME)
	}
}

// A refused binary must carry its typed reason into Refused, not vanish. The
// previous Build threw the error away and the only signal downstream got was
// "no ordinal" — indistinguishable from "binary was not in the input at
// all". Keeping the error lets callers count refusals by cause.
func TestBuildPreviewImages_RefusedKeepsTypedReason(t *testing.T) {
	policy := testPreviewImagePolicy()
	bins := map[string]FB2Binary{
		"ok":       {Data: uniformImage(t, "png", 4, 4)},
		"empty":    {Data: nil},                                                 // -> ErrPreviewImageUnsupported
		"html":     {Data: []byte("<html></html>")},                             // -> ErrPreviewImageUnsupported
		"svg":      {Data: []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")}, // -> ErrPreviewImageUnsupported
		"oversize": {Data: forgeBMP(1048576, 4)},                                // -> ErrPreviewImageDimensions (via Normalize)
	}
	set, err := BuildPreviewImages(context.Background(), bins, testPreviewImageBase(), policy, 0)
	if err != nil {
		t.Fatalf("BuildPreviewImages: %v", err)
	}
	if _, ok := set.Refusals()["ok"]; ok {
		t.Errorf("ok was admitted but also recorded as refused")
	}
	for _, id := range []string{"empty", "html", "svg"} {
		reason, ok := set.Refusals()[id]
		if !ok {
			t.Errorf("%q: refusal reason missing from Refused", id)
			continue
		}
		if !errors.Is(reason, ErrPreviewImageUnsupported) {
			t.Errorf("%q: reason = %v, want a wrapping of ErrPreviewImageUnsupported", id, reason)
		}
	}
	reason, ok := set.Refusals()["oversize"]
	if !ok {
		t.Fatalf("oversize: refusal reason missing from Refused")
	}
	if !errors.Is(reason, ErrPreviewImageDimensions) {
		t.Errorf("oversize: reason = %v, want a wrapping of ErrPreviewImageDimensions", reason)
	}
}

// The prepare call must run exactly once per binary — never twice, never
// zero. The previous Build prepared once but threw the bytes away, forcing
// a second prepare on the handler side; a future change that re-runs the
// loop or caches naively could slip to two, and only a counter catches it.
// We swap the package-level preparePreviewImage hook for one that counts,
// and restore it on the way out.
func TestBuildPreviewImages_PrepareCalledOncePerBinary(t *testing.T) {
	policy := testPreviewImagePolicy()
	bins := map[string]FB2Binary{
		"png_a": {Data: uniformImage(t, "png", 4, 4)},
		"png_b": {Data: uniformImage(t, "png", 4, 4)},
		"png_c": {Data: uniformImage(t, "png", 4, 4)},
		"bad":   {Data: []byte("not an image")},
	}

	calls := make(map[string]int)
	prev := preparePreviewImage
	preparePreviewImage = func(data []byte, p PreviewImagePolicy) ([]byte, string, error) {
		// Identify which binary this is by matching the data pointer, since
		// the hook does not see the id. The fixture uses unique payloads
		// only by reference — same image bytes for the three PNGs, so we
		// count total calls and reason about the total.
		calls["*"]++
		return PreparePreviewImage(data, p)
	}
	defer func() { preparePreviewImage = prev }()

	if _, err := BuildPreviewImages(context.Background(), bins, testPreviewImageBase(), policy, 0); err != nil {
		t.Fatalf("BuildPreviewImages: %v", err)
	}
	if got, want := calls["*"], len(bins); got != want {
		t.Errorf("prepare called %d times, want exactly %d (one per binary, including refusals)", got, want)
	}
}

// Ordinals and Images ordering must be stable across rebuilds, independent
// of map iteration. The previous test pinned this through Projection().URL;
// this one pins it through the new public surface — the Images slice in
// ordinal order — so a future change that builds Images in iteration order
// by accident still fails here.
func TestBuildPreviewImages_ImagesAreInOrdinalOrderAcrossRebuilds(t *testing.T) {
	policy := testPreviewImagePolicy()
	bins := map[string]FB2Binary{
		"e": {Data: uniformImage(t, "png", 4, 4)},
		"d": {Data: uniformImage(t, "png", 4, 4)},
		"c": {Data: uniformImage(t, "png", 4, 4)},
		"b": {Data: uniformImage(t, "png", 4, 4)},
		"a": {Data: uniformImage(t, "png", 4, 4)},
	}
	first, err := BuildPreviewImages(context.Background(), bins, testPreviewImageBase(), policy, 0)
	if err != nil {
		t.Fatalf("BuildPreviewImages: %v", err)
	}
	// First sanity: ids appear in sorted order, ordinals in 1..N order.
	for i, img := range first.Images() {
		if img.Ordinal != i+1 {
			t.Errorf("Images[%d].Ordinal = %d, want %d", i, img.Ordinal, i+1)
		}
	}
	wantIDs := []string{"a", "b", "c", "d", "e"}
	for i, want := range wantIDs {
		if i >= first.Len() || first.Images()[i].ID != want {
			t.Fatalf("Images[%d].ID = %q, want %q (sorted-id order)", i, first.Images()[i].ID, want)
		}
	}
	// Twenty rebuilds: map iteration order differs, the slice must not.
	for run := 0; run < 20; run++ {
		again, err := BuildPreviewImages(context.Background(), bins, testPreviewImageBase(), policy, 0)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if again.Len() != first.Len() {
			t.Fatalf("run %d: %d images, want %d", run, again.Len(), first.Len())
		}
		for i := range again.Images() {
			if again.Images()[i].ID != first.Images()[i].ID || again.Images()[i].Ordinal != first.Images()[i].Ordinal {
				t.Fatalf("run %d: Images[%d] drifted: got %+v, want %+v",
					run, i, again.Images()[i], first.Images()[i])
			}
		}
	}
}

// animatedGIF encodes a real multi-frame GIF through gif.EncodeAll, so the
// fixture exercises the same code path production serves — not a forged
// header that bypasses the decoder.
func animatedGIF(t *testing.T, frames int) []byte {
	t.Helper()
	var buf bytes.Buffer
	palette := color.Palette{color.Black, color.White}
	imgs := make([]*image.Paletted, frames)
	for i := range imgs {
		imgs[i] = image.NewPaletted(image.Rect(0, 0, 4, 4), palette)
	}
	delays := make([]int, frames)
	for i := range delays {
		delays[i] = 10
	}
	if err := gif.EncodeAll(&buf, &gif.GIF{Image: imgs, Delay: delays, LoopCount: 0}); err != nil {
		t.Fatalf("encode animated gif: %v", err)
	}
	return buf.Bytes()
}

// staticGIF encodes a single-frame GIF through gif.Encode — the same path,
// one frame.
func staticGIF(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := gif.Encode(&buf, image.NewPaletted(image.Rect(0, 0, 4, 4), color.Palette{color.Black, color.White}), nil); err != nil {
		t.Fatalf("encode static gif: %v", err)
	}
	return buf.Bytes()
}

// Animated GIF, WebP and APNG are shown in the preview on the same terms as
// their static counterparts. This is a deliberate decision (the reader is
// better served by seeing the animation than by a placeholder), not a
// side-effect of Normalize passing these formats through unchanged. The
// price: the per-frame pixel cap bounds one canvas, not the sum of frames,
// so a small-per-frame animation can still expand at the reader — that
// trade-off is paid knowingly, and these tests pin the choice so a future
// change cannot refuse animation by accident.
//
// The assertion is exact byte equality: PreparePreviewImage must hand back
// the same bytes it received for pass-through formats, not a truncated or
// re-encoded subset. "Leave only the first frame" is the mutation this
// catches — the output would be a valid picture, but not what the book
// carried.
//
// Fixtures in testdata/ are real, not forged:
//
//	animated.png  — a real APNG (acTL + 5 fcTL + 4 fdAT), built with:
//	  ffmpeg -f lavfi -i testsrc=duration=0.5:size=16x16:rate=10 \
//	    -plays 0 -f apng animated.png
//
//	animated.webp — a real animated WebP (VP8X + ANIM + 5 ANMF), built with:
//	  ffmpeg -f lavfi -i testsrc=duration=0.5:size=16x16:rate=10 \
//	    -loop 0 -c:v libwebp_anim animated.webp
//
// GIF is generated in-process through gif.EncodeAll (Go's standard encoder),
// so no external file is needed.
func TestPreparePreviewImage_AnimatedPayloadsAccepted(t *testing.T) {
	policy := testPreviewImagePolicy()
	apng, err := os.ReadFile("testdata/animated.png")
	if err != nil {
		t.Fatalf("read testdata/animated.png: %v", err)
	}
	webp, err := os.ReadFile("testdata/animated.webp")
	if err != nil {
		t.Fatalf("read testdata/animated.webp: %v", err)
	}
	cases := []struct {
		name     string
		data     []byte
		wantMime string
	}{
		{"animated gif (3 frames)", animatedGIF(t, 3), "image/gif"},
		{"static gif (control)", staticGIF(t), "image/gif"},
		{"animated png (apng)", apng, "image/png"},
		{"static png (control)", uniformImage(t, "png", 4, 4), "image/png"},
		{"animated webp", webp, "image/webp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, mime, err := PreparePreviewImage(tc.data, policy)
			if err != nil {
				t.Fatalf("PreparePreviewImage refused an animated payload: %v\n"+
					"animation is served on the same terms as static pictures.", err)
			}
			if mime != tc.wantMime {
				t.Errorf("mime = %q, want %q", mime, tc.wantMime)
			}
			// Exact byte equality: the pipeline must not re-encode, truncate,
			// or drop frames from a pass-through payload. A mutation that
			// "leaves only the first frame" would produce a valid picture
			// with fewer bytes — only an exact comparison catches that.
			if !bytes.Equal(payload, tc.data) {
				t.Errorf("payload (%d bytes) differs from input (%d bytes); "+
					"animation must pass through byte-for-byte",
					len(payload), len(tc.data))
			}
		})
	}
}

// --- Snapshot immutability tests -------------------------------------------
//
// PreviewImageSet is a snapshot: once built, nothing the caller does to the
// source map, to the slice returned by Images(), or to the map returned by
// Refusals() can reach the stored data. Each test below pins one surface
// and is killed by its own mutation — removing exactly one of the three
// copy sites in the implementation.

// Mutating the source FB2Binary.Data after BuildPreviewImages must not
// change what Images() returns. Killed by removing bytes.Clone at the
// build site (PreviewImageSet construction).
func TestPreviewImageSet_SourceDataMutationDoesNotLeak(t *testing.T) {
	png := uniformImage(t, "png", 4, 4)
	bins := map[string]FB2Binary{"a": {Data: png}}
	set, err := BuildPreviewImages(context.Background(), bins, testPreviewImageBase(), testPreviewImagePolicy(), 0)
	if err != nil {
		t.Fatalf("BuildPreviewImages: %v", err)
	}
	want := set.Images()[0].Payload
	// Corrupt the source — the set already holds its own copy.
	bins["a"].Data[0] ^= 0xFF
	got := set.Images()[0].Payload
	if !bytes.Equal(got, want) {
		t.Errorf("Images()[0].Payload changed after source mutation: got %x, want %x",
			got[:min(len(got), 8)], want[:min(len(want), 8)])
	}
}

// Mutating the Payload in a slice returned by Images() must not reach the
// stored snapshot. Killed by removing bytes.Clone inside Images().
func TestPreviewImageSet_PayloadMutationDoesNotLeak(t *testing.T) {
	png := uniformImage(t, "png", 4, 4)
	bins := map[string]FB2Binary{"a": {Data: png}}
	set, err := BuildPreviewImages(context.Background(), bins, testPreviewImageBase(), testPreviewImagePolicy(), 0)
	if err != nil {
		t.Fatalf("BuildPreviewImages: %v", err)
	}
	// Capture want as an independent copy before any mutation. If Images()
	// returns a clone, the stored slice is separate; if it returns a
	// reference, want itself would be an alias and the mutation would be
	// invisible.
	want := append([]byte(nil), set.Images()[0].Payload...)
	copy1 := set.Images()
	copy1[0].Payload[0] ^= 0xFF
	got := set.Images()[0].Payload
	if !bytes.Equal(got, want) {
		t.Errorf("Images()[0].Payload changed after mutating a returned copy: got %x, want %x",
			got[:min(len(got), 8)], want[:min(len(want), 8)])
	}
}

// Mutating Ordinal, ID or MIME in a slice returned by Images() must not
// reach the stored snapshot. These are value types copied by the struct
// assignment in Images() — no bytes.Clone is needed, but the test pins the
// invariant so a future change that returns pointers instead of values is
// caught.
func TestPreviewImageSet_FieldMutationDoesNotLeak(t *testing.T) {
	png := uniformImage(t, "png", 4, 4)
	bins := map[string]FB2Binary{"a": {Data: png}}
	set, err := BuildPreviewImages(context.Background(), bins, testPreviewImageBase(), testPreviewImagePolicy(), 0)
	if err != nil {
		t.Fatalf("BuildPreviewImages: %v", err)
	}
	copy1 := set.Images()
	copy1[0].Ordinal = 999
	copy1[0].ID = "tampered"
	copy1[0].MIME = "text/plain"
	again := set.Images()
	if again[0].Ordinal != 1 || again[0].ID != "a" || again[0].MIME != "image/png" {
		t.Errorf("Fields changed after mutating a returned copy: got Ordinal=%d ID=%q MIME=%q",
			again[0].Ordinal, again[0].ID, again[0].MIME)
	}
}

// Mutating the map returned by Refusals() must not reach the stored
// snapshot. Killed by returning the internal map directly instead of
// copying it.
func TestPreviewImageSet_RefusalsMutationDoesNotLeak(t *testing.T) {
	bins := map[string]FB2Binary{
		"good": {Data: uniformImage(t, "png", 4, 4)},
		"bad":  {Data: []byte("not an image")},
	}
	set, err := BuildPreviewImages(context.Background(), bins, testPreviewImageBase(), testPreviewImagePolicy(), 0)
	if err != nil {
		t.Fatalf("BuildPreviewImages: %v", err)
	}
	original := set.Refusals()
	if _, ok := original["bad"]; !ok {
		t.Fatalf("precondition: 'bad' must be in Refusals")
	}
	// Tamper with the returned map.
	original["injected"] = errors.New("fake")
	delete(original, "bad")
	again := set.Refusals()
	if _, ok := again["bad"]; !ok {
		t.Errorf("Refusals() lost 'bad' after caller mutated a returned map")
	}
	if _, ok := again["injected"]; ok {
		t.Errorf("Refusals() gained 'injected' from caller mutation")
	}
}

// --- Total budget: a bound on the work, not on the answer -----------------

// countingPrepare swaps the package-level preparePreviewImage hook for one
// that counts calls and hands every binary the same fixed payload, so a test
// controls the prepared size exactly and reads the work done from the
// counter. The real PreparePreviewImage is bypassed on purpose: these tests
// pin WHEN preparation stops, and that must not depend on what a codec does
// to a particular fixture.
func countingPrepare(t *testing.T, payloadSize int) *int {
	t.Helper()
	calls := new(int)
	prev := preparePreviewImage
	preparePreviewImage = func(data []byte, p PreviewImagePolicy) ([]byte, string, error) {
		*calls++
		return make([]byte, payloadSize), "image/png", nil
	}
	t.Cleanup(func() { preparePreviewImage = prev })
	return calls
}

// The budget must stop the preparation, not refuse a finished set. Four
// binaries of 600 prepared bytes under a 1000-byte budget: the first fits,
// the second crosses (1200 > 1000), and the third and fourth must never be
// prepared. A mutation that checks the sum after the loop returns the same
// error — only the counter tells it apart (4 calls instead of 2).
func TestBuildPreviewImages_BudgetStopsPreparation(t *testing.T) {
	bins := map[string]FB2Binary{
		"a": {Data: []byte("a")},
		"b": {Data: []byte("b")},
		"c": {Data: []byte("c")},
		"d": {Data: []byte("d")},
	}
	calls := countingPrepare(t, 600)

	// Ids sort a,b,c,d, so the crossing happens deterministically at b.
	_, err := BuildPreviewImages(context.Background(), bins, testPreviewImageBase(), testPreviewImagePolicy(), 1000)
	if !errors.Is(err, ErrPreviewImagesTotalTooLarge) {
		t.Fatalf("err = %v, want ErrPreviewImagesTotalTooLarge", err)
	}
	if *calls != 2 {
		t.Errorf("prepare called %d times, want 2 — the budget must stop the work, not the answer", *calls)
	}
}

// The budget counts the prepared payload, not the source binary: transcoding
// can change the size either way, and the prepared bytes are what memory,
// the cache and the reader carry. Here the source (2000 bytes) is bigger
// than the payload (600): counting sources would stop after ONE prepare
// (2000 > 1000), counting payloads admits one and stops at two.
func TestBuildPreviewImages_BudgetCountsPreparedBytesNotSource(t *testing.T) {
	bins := map[string]FB2Binary{
		"a": {Data: make([]byte, 2000)},
		"b": {Data: make([]byte, 2000)},
	}
	calls := countingPrepare(t, 600)

	_, err := BuildPreviewImages(context.Background(), bins, testPreviewImageBase(), testPreviewImagePolicy(), 1000)
	if !errors.Is(err, ErrPreviewImagesTotalTooLarge) {
		t.Fatalf("err = %v, want ErrPreviewImagesTotalTooLarge", err)
	}
	if *calls != 2 {
		t.Errorf("prepare called %d times, want 2 — the budget is on prepared bytes, not on sources", *calls)
	}
}

// Boundary: a prepared total of exactly the budget passes. Pins the
// comparison as strict (> not >=), same contract the service-level gate
// test pins end to end.
func TestBuildPreviewImages_BudgetAtExactTotalPasses(t *testing.T) {
	bins := map[string]FB2Binary{
		"a": {Data: []byte("a")},
		"b": {Data: []byte("b")},
	}
	calls := countingPrepare(t, 600)

	set, err := BuildPreviewImages(context.Background(), bins, testPreviewImageBase(), testPreviewImagePolicy(), 1200)
	if err != nil {
		t.Fatalf("a prepared total of exactly the budget must pass: %v", err)
	}
	if *calls != 2 || set.Len() != 2 {
		t.Errorf("calls = %d, prepared = %d, want 2 and 2", *calls, set.Len())
	}
}

// --- Used binaries: only what the markup references reaches preparation ---

// A repeated reference is still one binary: the used-set collapses the
// duplicates, and the prepare counter — not the set size — proves the work
// happened once.
func TestUsedBinaries_DuplicateReferencePreparesOnce(t *testing.T) {
	png := uniformImage(t, "png", 4, 4)
	doc := &FB2Document{
		Body: &FB2BodySection{Content: []*FB2ContentItem{
			{Paragraph: &FB2Paragraph{Kind: ParagraphKindNormal, Content: []*FB2InlineElement{
				{Type: InlineTypeImage, Attrs: map[string]string{"href": "#img1"}},
			}}},
			{Paragraph: &FB2Paragraph{Kind: ParagraphKindImage, Content: []*FB2InlineElement{
				{Type: InlineTypeImage, Attrs: map[string]string{"href": "#img1"}},
			}}},
		}},
		Binary: map[string]FB2Binary{"img1": {Data: png}},
	}

	used := UsedBinaries(doc)
	if len(used) != 1 {
		t.Fatalf("UsedBinaries returned %d ids, want 1 (two references to the same id)", len(used))
	}

	calls := 0
	prev := preparePreviewImage
	preparePreviewImage = func(data []byte, p PreviewImagePolicy) ([]byte, string, error) {
		calls++
		return PreparePreviewImage(data, p)
	}
	t.Cleanup(func() { preparePreviewImage = prev })

	set, err := BuildPreviewImages(context.Background(), used, testPreviewImageBase(), testPreviewImagePolicy(), 0)
	if err != nil {
		t.Fatalf("BuildPreviewImages: %v", err)
	}
	if calls != 1 {
		t.Errorf("prepare called %d times, want 1 — a repeated reference must not repeat the work", calls)
	}
	if set.Len() != 1 {
		t.Errorf("prepared %d images, want 1", set.Len())
	}
}

// A binary no markup references — the cover is the standing example — must
// not reach preparation at all: measured on 23 production books, one
// prepared 82 pictures its HTML never pointed at. The prepare counter must
// see only the referenced one.
func TestUsedBinaries_UnreferencedBinaryIsNeverPrepared(t *testing.T) {
	png := uniformImage(t, "png", 4, 4)
	doc := &FB2Document{
		Body: &FB2BodySection{Content: []*FB2ContentItem{
			{Paragraph: &FB2Paragraph{Kind: ParagraphKindImage, Content: []*FB2InlineElement{
				{Type: InlineTypeImage, Attrs: map[string]string{"href": "#shown"}},
			}}},
		}},
		Binary: map[string]FB2Binary{
			"shown": {Data: png},
			// The cover lives in description/coverpage, not in the body, so
			// it is precisely an unreferenced binary: excluded here.
			"cover": {Data: png},
		},
	}

	used := UsedBinaries(doc)
	if len(used) != 1 {
		t.Fatalf("UsedBinaries returned %d ids, want 1 (the unreferenced binary must be filtered out)", len(used))
	}
	if _, ok := used["cover"]; ok {
		t.Errorf("the unreferenced binary survived the filter")
	}

	calls := 0
	prev := preparePreviewImage
	preparePreviewImage = func(data []byte, p PreviewImagePolicy) ([]byte, string, error) {
		calls++
		return PreparePreviewImage(data, p)
	}
	t.Cleanup(func() { preparePreviewImage = prev })

	if _, err := BuildPreviewImages(context.Background(), used, testPreviewImageBase(), testPreviewImagePolicy(), 0); err != nil {
		t.Fatalf("BuildPreviewImages: %v", err)
	}
	if calls != 1 {
		t.Errorf("prepare called %d times, want 1 — the unreferenced binary must never be prepared", calls)
	}
}

// A picture referenced from a footnote is rendered with the portion that
// cites the note, so it must be prepared. A mutation that scans only the
// body forgets it — the reference would resolve to nothing at render time.
// This test verifies that a reachable note's image is prepared.
func TestUsedBinaries_FootnoteImageIsIncluded(t *testing.T) {
	png := uniformImage(t, "png", 4, 4)
	doc := &FB2Document{
		Body: &FB2BodySection{Content: []*FB2ContentItem{
			{Paragraph: noteRefPara("n1", "сноска")},
		}},
		Notes: []*FB2BodySection{
			{ID: "n1", Content: []*FB2ContentItem{
				{Paragraph: &FB2Paragraph{Kind: ParagraphKindNormal, Content: []*FB2InlineElement{
					{Type: InlineTypeText, Content: "текст сноски"},
					{Type: InlineTypeImage, Attrs: map[string]string{"href": "#note_pic"}},
				}}},
			}},
		},
		Binary: map[string]FB2Binary{"note_pic": {Data: png}},
	}

	used := UsedBinaries(doc)
	if _, ok := used["note_pic"]; !ok {
		t.Errorf("a picture referenced from a reachable footnote was not collected — got %v", used)
	}
}

// A picture inside a table cell is markup the renderer emits, so it must be
// prepared. Cells hang off FB2Paragraph.Table, not off the inline content —
// a scan that only walks Content misses them.
func TestUsedBinaries_TableCellImageIsIncluded(t *testing.T) {
	png := uniformImage(t, "png", 4, 4)
	doc := &FB2Document{
		Body: &FB2BodySection{Content: []*FB2ContentItem{
			{Paragraph: &FB2Paragraph{
				Kind: ParagraphKindTable,
				Table: &FB2Table{Rows: [][]*FB2TableCell{
					{{Content: []*FB2InlineElement{
						{Type: InlineTypeImage, Attrs: map[string]string{"href": "#tbl_pic"}},
					}}},
				}},
			}},
		}},
		Binary: map[string]FB2Binary{"tbl_pic": {Data: png}},
	}

	used := UsedBinaries(doc)
	if _, ok := used["tbl_pic"]; !ok {
		t.Errorf("a picture referenced from a table cell was not collected — got %v", used)
	}
}

// The reference normalization must match what the renderer applies in
// renderImage and what the parser stores: whitespace trimmed, the leading
// '#' stripped, the id lowercased. An image nested inside another inline
// element (a link, an emphasis) is still a reference.
func TestUsedBinaries_NormalizesLikeTheRenderer(t *testing.T) {
	png := uniformImage(t, "png", 4, 4)
	doc := &FB2Document{
		Body: &FB2BodySection{Content: []*FB2ContentItem{
			{Paragraph: &FB2Paragraph{Kind: ParagraphKindNormal, Content: []*FB2InlineElement{
				{Type: InlineTypeImage, Attrs: map[string]string{"href": "  #Pic1 "}},
				{Type: InlineTypeLink, Attrs: map[string]string{"href": "#n1"}, Children: []*FB2InlineElement{
					{Type: InlineTypeImage, Attrs: map[string]string{"href": "#PIC2"}},
				}},
			}}},
		}},
		Binary: map[string]FB2Binary{
			"pic1": {Data: png},
			"pic2": {Data: png},
		},
	}

	used := UsedBinaries(doc)
	if len(used) != 2 {
		t.Fatalf("UsedBinaries returned %d ids, want 2 — got %v", len(used), used)
	}
	for _, id := range []string{"pic1", "pic2"} {
		if _, ok := used[id]; !ok {
			t.Errorf("id %q missing — normalization must match the renderer's", id)
		}
	}
}

// A reference to a binary the book does not carry collects nothing — the
// renderer answers it with a placeholder, and preparation has no bytes to
// work on.
func TestUsedBinaries_DanglingReferenceCollectsNothing(t *testing.T) {
	doc := &FB2Document{
		Body: &FB2BodySection{Content: []*FB2ContentItem{
			{Paragraph: &FB2Paragraph{Kind: ParagraphKindNormal, Content: []*FB2InlineElement{
				{Type: InlineTypeImage, Attrs: map[string]string{"href": "#ghost"}},
			}}},
		}},
		Binary: map[string]FB2Binary{"real": {Data: uniformImage(t, "png", 4, 4)}},
	}

	if used := UsedBinaries(doc); len(used) != 0 {
		t.Errorf("UsedBinaries returned %v, want empty — the reference dangles and 'real' is unreferenced", used)
	}
}

// A picture in a body section that carries an id (for internal links) must
// still be prepared: the id does not make it a footnote, and the body is
// always rendered. This is the regression guard for the bug where a body
// section with an id was incorrectly filtered as an unreachable note.
func TestUsedBinaries_BodySectionWithIdIsIncluded(t *testing.T) {
	png := uniformImage(t, "png", 4, 4)
	doc := &FB2Document{
		Body: &FB2BodySection{Content: []*FB2ContentItem{
			{Section: &FB2BodySection{
				ID: "chapter-1",
				Content: []*FB2ContentItem{
					{Paragraph: &FB2Paragraph{Kind: ParagraphKindNormal, Content: []*FB2InlineElement{
						{Type: InlineTypeText, Content: "Chapter 1"},
						{Type: InlineTypeImage, Attrs: map[string]string{"href": "#body_pic"}},
					}}},
				},
			}},
		}},
		Binary: map[string]FB2Binary{"body_pic": {Data: png}},
	}

	used := UsedBinaries(doc)
	if _, ok := used["body_pic"]; !ok {
		t.Errorf("a picture in a body section with id was not collected — got %v", used)
	}
}

// A picture in a nested body section (section inside section) that carries an
// id must also be prepared. The walk is recursive, and the reachability check
// only applies to notes, not to any body section regardless of nesting.
func TestUsedBinaries_NestedBodySectionWithIdIsIncluded(t *testing.T) {
	png := uniformImage(t, "png", 4, 4)
	doc := &FB2Document{
		Body: &FB2BodySection{Content: []*FB2ContentItem{
			{Section: &FB2BodySection{
				ID: "outer-section",
				Content: []*FB2ContentItem{
					{Section: &FB2BodySection{
						ID: "inner-section",
						Content: []*FB2ContentItem{
							{Paragraph: &FB2Paragraph{Kind: ParagraphKindNormal, Content: []*FB2InlineElement{
								{Type: InlineTypeText, Content: "Inner section"},
								{Type: InlineTypeImage, Attrs: map[string]string{"href": "#nested_pic"}},
							}}},
						},
					}},
				},
			}},
		}},
		Binary: map[string]FB2Binary{"nested_pic": {Data: png}},
	}

	used := UsedBinaries(doc)
	if _, ok := used["nested_pic"]; !ok {
		t.Errorf("a picture in a nested body section with id was not collected — got %v", used)
	}
}

// A picture in an unreferenced footnote must not be prepared: the note
// never renders, so its images are dead weight.
func TestUsedBinaries_UnreachableFootnoteImageIsExcluded(t *testing.T) {
	png := uniformImage(t, "png", 4, 4)
	doc := &FB2Document{
		Body: &FB2BodySection{Content: []*FB2ContentItem{
			{Paragraph: &FB2Paragraph{Kind: ParagraphKindNormal, Content: []*FB2InlineElement{
				{Type: InlineTypeText, Content: "No footnote reference"},
			}}},
		}},
		Notes: []*FB2BodySection{
			{ID: "unused-note", Content: []*FB2ContentItem{
				{Paragraph: &FB2Paragraph{Kind: ParagraphKindNormal, Content: []*FB2InlineElement{
					{Type: InlineTypeText, Content: "This note is never reached"},
					{Type: InlineTypeImage, Attrs: map[string]string{"href": "#unreachable_pic"}},
				}}},
			}},
		},
		Binary: map[string]FB2Binary{"unreachable_pic": {Data: png}},
	}

	used := UsedBinaries(doc)
	if _, ok := used["unreachable_pic"]; ok {
		t.Errorf("a picture in an unreferenced footnote was incorrectly collected — got %v", used)
	}
}
