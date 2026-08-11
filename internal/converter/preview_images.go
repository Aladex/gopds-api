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
	"fmt"
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
		// An address is issued only when the same call has produced the
		// bytes the handler will serve. That closes the defect where the
		// gate accepted a picture the handler could never satisfy: a header
		// past fb2image.Normalize's own cap slipped through, the renderer
		// emitted the URL, and the handler had nothing to send. The bytes
		// themselves are discarded here — Build only assigns the address,
		// the handler re-prepares on demand from the source binary.
		if _, _, err := PreparePreviewImage(binaries[id].Data, policy); err != nil {
			continue
		}
		n++
		out.Index[id] = n
	}
	return out
}

// PreparePreviewImage decides and prepares one preview picture in the same
// call. The bytes it returns are exactly what the handler later serves to the
// reader; if it cannot produce them, no address is issued for the binary and
// the reader sees the placeholder instead.
//
// fb2image.Normalize is the single authority over the bytes: the gate accepts
// a picture only when Normalize has produced a payload, never on the promise
// of a header the decoder has not confirmed. The policy's own byte and pixel
// caps are layered on top, because Normalize answers "could a reader draw
// this" — a question wider than "does our preview policy allow it".
func PreparePreviewImage(data []byte, policy PreviewPolicy) (payload []byte, mime string, err error) {
	// The byte cap on the input is checked before any decode: a binary the
	// size of a book must not reach a decoder that would allocate from the
	// header it carries.
	if len(data) > policy.MaxImageBytes {
		return nil, "", fmt.Errorf("%w: payload is %d bytes, cap is %d",
			ErrPreviewImageTooLarge, len(data), policy.MaxImageBytes)
	}
	if len(data) == 0 {
		// An empty payload carries no magic, so format is unsupported —
		// corrupt would imply bytes failed to decode, and there are none.
		return nil, "", fmt.Errorf("%w: empty payload", ErrPreviewImageUnsupported)
	}

	// The format question is decided by the bytes alone. SVG is an XML
	// document that can carry script and would run under the reader's
	// origin, so it is refused by format; an unknown magic means no decoder
	// in the library will name it and fb2image.Normalize will not produce
	// bytes either.
	switch kind := fb2image.Classify(data); kind {
	case "":
		return nil, "", fmt.Errorf("%w: no recognizable image magic", ErrPreviewImageUnsupported)
	case fb2image.MimeSVG:
		return nil, "", fmt.Errorf("%w: svg is unsafe to serve under the reader origin", ErrPreviewImageUnsupported)
	}

	// fb2image.Normalize IS the work: it re-encodes BMP/TIFF as PNG and
	// refuses what it cannot decode or what its own dimension cap rejects
	// (a forged BMP past maxDimension lands here). Whatever it returns is
	// what the reader receives — this is the boundary at which decision and
	// preparation become one call. A refusal comes back as a nil payload,
	// which the DecodeConfig below then fails on as corrupt; the two cases
	// are not separated here because the reader cannot tell them apart
	// either.
	payload, mime = fb2image.Normalize(data)

	// The renderable-as-is formats (PNG/JPEG/GIF/WEBP) pass through
	// Normalize unchanged, so the policy's own pixel ceiling has to be
	// enforced here, on the bytes we will actually serve. DecodeConfig
	// reads the header only — a forged 20000x20000 declaration costs a
	// header read, not the allocation it asks for.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(payload))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, "", fmt.Errorf("%w: header would not decode", ErrPreviewImageCorrupt)
	}
	if cfg.Width*cfg.Height > policy.MaxImagePixels {
		return nil, "", fmt.Errorf("%w: declared %dx%d, cap is %d pixels",
			ErrPreviewImageDimensions, cfg.Width, cfg.Height, policy.MaxImagePixels)
	}

	// Final size check on the bytes the reader will receive. Re-encoding a
	// BMP as PNG can come out bigger than the source, so the input gate
	// above does not see this size — only this gate does.
	if len(payload) > policy.MaxImageBytes {
		return nil, "", fmt.Errorf("%w: %d bytes after normalize, cap is %d",
			ErrPreviewImageTooLargeResult, len(payload), policy.MaxImageBytes)
	}

	return payload, mime, nil
}
