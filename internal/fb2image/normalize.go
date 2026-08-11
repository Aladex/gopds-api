// Package fb2image names and, where necessary, re-encodes the pictures a book
// carries, so the catalog, the web poster and the EPUB all decide the same
// thing about the same bytes.
//
// The scanner and the EPUB generator used to each answer this question on
// their own. Keeping one answer here is deliberate: the two copies of the FB2
// sanitizing chain drifted apart and produced defects, and image typing is the
// same shape of problem.
package fb2image

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"

	// Registered for image.Decode: the formats that have to be re-encoded.
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
)

// Refusals returned by Normalize. Each names a distinct reason so callers
// can map them into their own policy categories — most importantly, "too
// large for our internal cap" is not the same category as "the bytes are
// corrupt", because one drives policy tuning and the other drives catalog
// cleanup. They are returned wrapped (fmt.Errorf("%w: ...", Err...)) so
// errors.Is tells them apart.
var (
	// ErrUnknownFormat: no registered decoder recognized the bytes. An
	// empty payload also lands here — there is no magic to identify.
	ErrUnknownFormat = errors.New("fb2image: image format is not recognized")

	// ErrUndecodable: the magic matched a known format but the bytes would
	// not decode (truncated stream, broken header, zero dimensions). The
	// picture exists in name only.
	ErrUndecodable = errors.New("fb2image: image payload is undecodable")

	// ErrTooLarge: the decoded header declares more than our own cap
	// allows — wider or taller than maxDimension, or more pixels than
	// maxPixels. Distinct from "corrupt" because the picture is fine; our
	// ceiling is the policy choice, and the catalog will want to count
	// this separately.
	ErrTooLarge = errors.New("fb2image: image dimensions exceed the internal cap")

	// ErrEncodeFailed: re-encoding the decoded picture to PNG did not
	// succeed. png.Encode practically never fails on a well-formed image,
	// so reaching this branch is itself signal.
	ErrEncodeFailed = errors.New("fb2image: re-encoding the payload failed")
)

const (
	svgNamespace = "http://www.w3.org/2000/svg"

	MimeJPEG = "image/jpeg"
	MimePNG  = "image/png"
	MimeGIF  = "image/gif"
	MimeWEBP = "image/webp"
	MimeSVG  = "image/svg+xml"

	extJPG  = ".jpg"
	extPNG  = ".png"
	extGIF  = ".gif"
	extWEBP = ".webp"
	extSVG  = ".svg"

	// Ceilings on what will be decoded. The payload's own header decides how
	// much a decoder allocates, so these are the only thing standing between
	// a forged BMP header and the process's memory.
	//
	// Set from the library rather than from a round number: across all 263 BMP
	// and TIFF files in the catalog the largest is 1155x790 and the widest side
	// anywhere is 1328, with a median of 0.06 megapixels. These leave roughly
	// three times that in each direction.
	//
	// They bound the pixel count, not the bytes: a TIFF carrying 16-bit samples
	// decodes to eight bytes a pixel, so the ceiling is tens of megabytes
	// rather than the 1.5 GB an unchecked header bought.
	maxDimension = 4096
	maxPixels    = 4 << 20

	// How far into a payload to look for the "<" that any SVG must have.
	// Without this cheap rejection every JPEG in the library pays for an XML
	// decoder that is certain to fail.
	svgSniffWindow = 1024
)

// renderable are the types a reader can be expected to draw, in the order the
// magic check tries them. A payload that already is one of these is passed
// through untouched: the catalog holds five and a half million pictures and
// re-encoding them all to correct a couple of hundred would be a poor trade.
var renderable = []string{MimePNG, MimeJPEG, MimeGIF, MimeWEBP, MimeSVG}

// Normalize names an image by its bytes and returns it in a form a reader can
// draw. Formats outside the renderable set — BMP and TIFF, which EPUB does not
// require a reader to support — are re-encoded as PNG. A payload that decodes
// as no image at all, or that declares more pixels than will be decoded, is
// returned as a typed error so callers can tell "wrong policy" from "broken
// bytes" and count by cause; the caller is meant to drop it, because shipping
// it anyway produced cover.bin files that nothing opened.
//
// The declared content-type and the extension inside the FB2 image id are both
// book-controlled text, and both had to be confirmed against the magic bytes
// anyway, so neither is consulted.
func Normalize(data []byte) (payload []byte, mime string, err error) {
	if len(data) == 0 {
		return nil, "", ErrUnknownFormat
	}

	for _, m := range renderable {
		if matchesMagic(m, data) {
			return data, m, nil
		}
	}

	// Reaching transcode with bytes no convertible format owns would turn
	// every prose payload into an Undecodable BMP attempt, burying the real
	// signal ("nothing here identifies this as a picture at all"). The
	// isConvertible magic check is the boundary between "format question"
	// and "decode question" — only payloads whose magic we recognize reach
	// transcode, so its typed errors are about that format specifically.
	if !isConvertible(data) {
		return nil, "", ErrUnknownFormat
	}

	// Not a format readers draw. If Go can decode it we hand back a PNG;
	// transcode carries its own typed refusals (too large, undecodable,
	// encode-failed) so callers can distinguish policy from corruption.
	return transcode(data)
}

// transcode re-encodes a picture a reader would not draw into one it will.
//
// PNG rather than JPEG: the payloads that reach here are BMP and TIFF, which
// carry transparency, palettes and bilevel scans. Handing those to a JPEG
// encoder turns clear areas black, and the population is small enough that
// choosing a format per picture would be more policy than it is worth.
//
// Every refusal is typed: the caller counts by cause, and the difference
// between "your cap is the policy choice" (ErrTooLarge) and "the bytes are
// broken" (ErrUndecodable) is what makes those counts meaningful.
func transcode(data []byte) (payload []byte, mime string, err error) {
	// The header is book-controlled, and a decoder allocates from what it
	// claims: seventy bytes declaring 20000x20000 bought a 1.5 GB allocation
	// before failing on the missing pixels. Read the dimensions first and
	// refuse the picture on those, so a forged header costs a header read.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		// Magic identified this as a convertible format (the caller only
		// reaches transcode after the renderable loop refused it), so a
		// header that does not even read is corruption, not "unknown".
		return nil, "", fmt.Errorf("%w: DecodeConfig: %v", ErrUndecodable, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, "", fmt.Errorf("%w: non-positive dimensions %dx%d",
			ErrUndecodable, cfg.Width, cfg.Height)
	}
	if cfg.Width > maxDimension || cfg.Height > maxDimension {
		return nil, "", fmt.Errorf("%w: %dx%d over the %d-per-side cap",
			ErrTooLarge, cfg.Width, cfg.Height, maxDimension)
	}
	if cfg.Width*cfg.Height > maxPixels {
		return nil, "", fmt.Errorf("%w: %d pixels over the %d cap",
			ErrTooLarge, cfg.Width*cfg.Height, maxPixels)
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		// The header read but the picture did not — a truncated download
		// or a corrupted archive. Same bucket as the header-failure above.
		return nil, "", fmt.Errorf("%w: Decode: %v", ErrUndecodable, err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrEncodeFailed, err)
	}
	return buf.Bytes(), MimePNG, nil
}

// ExtensionFor returns the file extension for a type Normalize can return, and
// an empty string for anything else. An empty result means the payload has no
// place in an EPUB, not that a default should be invented.
func ExtensionFor(mime string) string {
	switch mime {
	case MimeJPEG:
		return extJPG
	case MimePNG:
		return extPNG
	case MimeGIF:
		return extGIF
	case MimeWEBP:
		return extWEBP
	case MimeSVG:
		return extSVG
	default:
		return ""
	}
}

// matchesMagic reports whether the payload's leading bytes identify it as the
// given type.
func matchesMagic(mime string, data []byte) bool {
	switch mime {
	case MimeJPEG:
		return bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF})
	case MimePNG:
		return bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'})
	case MimeGIF:
		return bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a"))
	case MimeWEBP:
		return len(data) >= 12 && bytes.HasPrefix(data, []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP"))
	case MimeSVG:
		// SVG is XML text and has no fixed magic, so the root element is the
		// only real evidence. An XML prolog is not: every XML document has
		// one, so accepting it would confirm `<?xml?><html>` as an image and
		// put it in the EPUB as one. Skip the prolog, then require <svg.
		return hasSVGRoot(data)
	default:
		return false
	}
}

// hasSVGRoot reports whether the payload's first element is <svg>. Reading it
// with the XML decoder rather than scanning by hand is the point: a hand-rolled
// scan has to reinvent name boundaries, CDATA, doctype subsets and the BOM, and
// gets each of them slightly wrong.
func hasSVGRoot(data []byte) bool {
	// Cheap rejection first: the magic loop reaches SVG for every payload
	// that is not one of the four binary formats.
	if !bytes.Contains(data[:min(len(data), svgSniffWindow)], []byte("<")) {
		return false
	}

	// A byte-order mark is not content, and the decoder would report it as
	// text sitting before the root.
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})

	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = false
	decoder.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) {
		return input, nil
	}
	for {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		switch t := token.(type) {
		case xml.StartElement:
			// XML is case-sensitive, so <SVG> is not <svg>; and the namespace
			// has to be the SVG one, or <x:svg xmlns:x="urn:not-svg"> would
			// qualify on its local name alone.
			return t.Name.Local == "svg" && (t.Name.Space == "" || t.Name.Space == svgNamespace)
		case xml.CharData:
			// Text before the root means this is not a document whose first
			// element is <svg>: "junk<svg/>" and a closed CDATA both land here.
			if len(bytes.TrimSpace(t)) > 0 {
				return false
			}
		}
	}
}

// Classify names the type a payload would be served as, reading only its
// leading bytes. Nothing is decoded and nothing is converted: this answers
// "may this be shown, and as what", which is a question callers ask far more
// often than they ask for the converted bytes.
//
// BMP and TIFF report the type they would become, because that is what a
// reader would eventually receive; SVG reports itself so a caller can refuse
// it; anything else reports nothing at all.
func Classify(data []byte) string {
	for _, mime := range renderable {
		if matchesMagic(mime, data) {
			return mime
		}
	}
	if isConvertible(data) {
		return MimePNG
	}
	return ""
}

// isConvertible reports the magic of the two formats transcode() accepts.
func isConvertible(data []byte) bool {
	return bytes.HasPrefix(data, []byte("BM")) ||
		bytes.HasPrefix(data, []byte{0x49, 0x49, 0x2A, 0x00}) ||
		bytes.HasPrefix(data, []byte{0x4D, 0x4D, 0x00, 0x2A})
}
