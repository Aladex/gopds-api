package converter

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
)

// Image policy used to live inside the renderer, one decision per reference.
// It now runs once, before any portion is rendered, so these tests follow it
// there: what the renderer may address is exactly what this function accepted.
func TestAcceptPreviewImage(t *testing.T) {
	png := uniformImage(t, "png", 8, 8)
	jpeg := uniformImage(t, "jpeg", 8, 8)
	gif := uniformImage(t, "gif", 8, 8)

	cases := []struct {
		name string
		data []byte
		want bool
	}{
		{"png", png, true},
		{"jpeg", jpeg, true},
		{"gif", gif, true},
		{"empty", nil, false},
		{"prose", []byte("это не картинка, а просто текст"), false},
		{"html claiming to be an image", []byte(`<?xml version="1.0"?><html></html>`), false},
		// An XML document that can carry script must never be served under the
		// reader's origin, whatever the book declares it to be.
		{"svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), false},
		{"truncated png header", png[:4], false},
	}
	policy := testPreviewPolicy()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AcceptPreviewImage(tc.data, policy); got != tc.want {
				t.Fatalf("AcceptPreviewImage = %v, want %v", got, tc.want)
			}
		})
	}
}

// The byte cap and the pixel cap are separate limits and each has to bite on
// its own: a small payload can declare an enormous canvas, and a large payload
// can declare a small one.
func TestAcceptPreviewImage_CapsBiteSeparately(t *testing.T) {
	png := uniformImage(t, "png", 32, 32)

	tight := testPreviewPolicy()
	tight.MaxImageBytes = len(png) - 1
	if AcceptPreviewImage(png, tight) {
		t.Error("a payload over the byte cap was accepted")
	}

	small := testPreviewPolicy()
	small.MaxImagePixels = 32*32 - 1
	if AcceptPreviewImage(png, small) {
		t.Error("a picture over the pixel cap was accepted")
	}

	if !AcceptPreviewImage(png, testPreviewPolicy()) {
		t.Error("the same picture was refused under a policy that allows it")
	}
}

// A forged header costs a header read, not an allocation: the dimensions are
// checked before anything is decoded. Seventy bytes claiming 20000x20000 once
// bought 1.5 GB on the EPUB path.
func TestAcceptPreviewImage_RefusesAForgedHeader(t *testing.T) {
	var buf strings.Builder
	buf.WriteString("BM")
	var head []byte
	head = binary.LittleEndian.AppendUint32(head, 0)
	head = binary.LittleEndian.AppendUint32(head, 0)
	head = binary.LittleEndian.AppendUint32(head, 54)
	head = binary.LittleEndian.AppendUint32(head, 40)
	head = binary.LittleEndian.AppendUint32(head, 20000)
	head = binary.LittleEndian.AppendUint32(head, 20000)
	head = binary.LittleEndian.AppendUint16(head, 1)
	head = binary.LittleEndian.AppendUint16(head, 24)
	head = append(head, make([]byte, 40)...)
	buf.Write(head)

	if AcceptPreviewImage([]byte(buf.String()), testPreviewPolicy()) {
		t.Fatal("a header claiming 400 megapixels was accepted")
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
