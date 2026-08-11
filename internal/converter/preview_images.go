package converter

// preview_images.go decides which of a book's binaries may be shown in a
// preview, and under what address.
//
// The decision lives here rather than in the renderer on purpose. Inlining a
// picture as a data: URL made the portion ceiling do two jobs at once — bound
// the text and bound the picture — and measurement on 24 production books
// showed what that costs: the median illustration is 500px wide and 48 KB,
// which is a 64 KB data URL, the whole portion budget spent on one image with
// no room for a word of text. Half the illustrations in real books were
// dropped. A picture book came out as seventeen placeholders and no pictures.
//
// So a picture is now a resource of its own, addressed by a URL the server
// builds. The principle the plan set out is unchanged and was never about the
// scheme: the address must not come from the book. An index we assign
// satisfies that exactly as a data: URL did.
//
// Splitting the decision from the rendering also keeps one authority over
// image policy. The renderer asks a prepared set whether an id may be shown;
// it never decodes, never transcodes, and never re-encodes the same binary
// once per reference.

import (
	"bytes"
	"image"
	"sort"
	"strconv"

	// Registered for image.DecodeConfig: dimensions are read from the header
	// of every format the library actually holds, before anything is decoded.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"

	"gopds-api/internal/fb2image"
)

// PreviewImages is the set of pictures a portion is allowed to reference: the
// base address the server serves them from, and the ordinal of every binary
// that passed policy. A binary missing from Index is one the reader will see
// as a placeholder — an explicit marker, never a broken image.
type PreviewImages struct {
	Base  string         // Address prefix the server answers on
	Index map[string]int // Binary id -> ordinal under Base
}

// URL returns the address of one accepted binary, or "" if it was not
// accepted. The caller escapes it; nothing here comes from the book except
// the id used to look it up.
func (p PreviewImages) URL(id string) string {
	n, ok := p.Index[id]
	if !ok {
		return ""
	}
	return p.Base + "/" + strconv.Itoa(n)
}

// BuildPreviewImages applies image policy to a book's binaries once and
// returns what the renderer may reference. Ordinals follow the sorted binary
// ids so the same book always yields the same addresses: the mapping is part
// of the contract between the renderer that emits a URL and the handler that
// answers it.
func BuildPreviewImages(binaries map[string]FB2Binary, base string, policy PreviewPolicy) PreviewImages {
	out := PreviewImages{Base: base, Index: make(map[string]int, len(binaries))}
	ids := make([]string, 0, len(binaries))
	for id := range binaries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	n := 0
	for _, id := range ids {
		if !AcceptPreviewImage(binaries[id].Data, policy) {
			continue
		}
		n++
		out.Index[id] = n
	}
	return out
}

// AcceptPreviewImage reports whether a payload may be served as a preview
// picture. The declared content-type and the id are book-controlled text, so
// only the bytes decide.
//
// SVG is refused outright: it is an XML document that can carry script, and
// serving one under the reader's origin would hand the book a way to run
// there. The dimensions are read from the header before any decoding, because
// a decoder allocates from what the header claims — a 70-byte payload
// declaring 20000x20000 bought 1.5 GB in the EPUB path once.
func AcceptPreviewImage(data []byte, policy PreviewPolicy) bool {
	if len(data) == 0 || len(data) > policy.MaxImageBytes {
		return false
	}
	kind := fb2image.Classify(data)
	if kind == "" || kind == fb2image.MimeSVG {
		return false
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return false
	}
	return cfg.Width*cfg.Height <= policy.MaxImagePixels
}
