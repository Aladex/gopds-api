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
	"context"
	"errors"
	"fmt"
	"image"
	"regexp"
	"sort"
	"strconv"
	"strings"

	// Registered for image.DecodeConfig: dimensions are read from the header
	// of every format the library actually holds, before anything is decoded.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"

	"gopds-api/internal/fb2image"
)

// ErrPreviewImageBaseInvalid is the typed refusal NewPreviewImageBase returns
// for any input that would produce an address the renderer may not emit: an
// empty revision, one carrying characters that do not belong in a URL path
// segment, or a built path that slips a scheme, a host, or a traversal.
// Callers gate the whole pipeline on this — an invalid base means no address
// is built at all, never an address that downstream assumes is safe.
var ErrPreviewImageBaseInvalid = errors.New("fb2 preview: image base is invalid")

// revisionPattern is the set of characters a book revision may carry. It is
// deliberately narrow: letters, digits, dot, dash and underscore — the
// characters every URL path segment in RFC 3986's "unreserved" set accepts
// without percent-encoding. Anything else (whitespace, slash, colon, query
// separators) would either break the address at the handler's route match or
// smuggle structure the base never agreed to.
var revisionPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// PreviewImageBase is the absolute, same-origin path prefix every preview
// image address is served under. It exists as a type, not a string, so that
// the only way to produce one is through NewPreviewImageBase — which refuses
// inputs that would yield an address the renderer is not allowed to emit.
//
// The base carries the book id and the book revision: a handler that answers
// /preview/{bookID}/{revision}/{ordinal} needs both to find the bytes, and
// embedding them in the address makes the URL self-describing — the renderer
// does not have to be told which book it is rendering for.
type PreviewImageBase struct {
	path string
}

// String returns the absolute path prefix the base was built with, for tests
// that need to ask the code (rather than hard-code) what shape an address
// takes. The output is same-origin by construction.
func (b PreviewImageBase) String() string {
	return b.path
}

// URLFor returns the address of one ordinal under this base. Ordinals come
// from PreviewImages, never from the book; assembling the address here keeps
// the format in one place.
func (b PreviewImageBase) URLFor(ordinal int) string {
	return b.path + "/" + strconv.Itoa(ordinal)
}

// NewPreviewImageBase builds a base from a book id and a revision string. The
// revision is opaque to this package — it is whatever the caller uses to
// version a book's binaries — but it has to be safe to embed in a URL path
// segment, or the address the renderer emits would not match the route the
// handler answers.
//
// The format is fixed in this constructor: /preview/{bookID}/{revision}.
// Putting it here, not in the test, means a test that needs to know the shape
// of a src asks the code, not its own fixture.
func NewPreviewImageBase(bookID int64, revision string) (PreviewImageBase, error) {
	// A non-positive book id has no catalog row behind it: zero never
	// identifies a book, and a negative id is meaningless. Refusing here is
	// cheaper than discovering "/preview/0/rev1" in a handler log three
	// weeks later, after a reader clicked a link that pointed nowhere.
	if bookID <= 0 {
		return PreviewImageBase{}, fmt.Errorf("%w: book id %d is not positive", ErrPreviewImageBaseInvalid, bookID)
	}
	// revision == "" is covered by revisionPattern below (the `+` requires
	// at least one character); a separate empty check would be unreachable
	// as its own refusal.
	if !revisionPattern.MatchString(revision) {
		return PreviewImageBase{}, fmt.Errorf(
			"%w: revision %q has characters outside [A-Za-z0-9._-] or is empty",
			ErrPreviewImageBaseInvalid, revision,
		)
	}
	// `..` is two characters the per-character regex above admits; a path
	// segment carrying it is still a traversal, even with no slash to chain
	// it through. Refuse it explicitly — the regex cannot, because it
	// describes one character at a time.
	if strings.Contains(revision, "..") {
		return PreviewImageBase{}, fmt.Errorf("%w: revision %q contains a path-traversal sequence", ErrPreviewImageBaseInvalid, revision)
	}
	// The format is fixed here: revision was validated above, bookID is an
	// int64 the constructor formats itself, so the result is always a
	// same-origin absolute path. There is no defense-in-depth re-check: a
	// future change to the format has to bring its own tests, not lean on a
	// string-contains guard that the current code never reaches.
	return PreviewImageBase{path: "/preview/" + strconv.FormatInt(bookID, 10) + "/" + revision}, nil
}

// PreviewImages is the set of pictures a portion is allowed to reference,
// built once from a base and a book's binaries. The index is private on
// purpose: the set is meant to be immutable from the moment it is built, and
// exporting the map would let any caller mint an address for a binary the
// pipeline never prepared. Readers use Ordinal, URL and Len — none of which
// can mutate the set.
type PreviewImages struct {
	base  PreviewImageBase
	index map[string]int // binary id -> ordinal under base
}

// URL returns the address of one accepted binary, or "" if it was not
// accepted. The caller escapes it; nothing here comes from the book except
// the id used to look it up.
func (p PreviewImages) URL(id string) string {
	n, ok := p.index[id]
	if !ok {
		return ""
	}
	return p.base.URLFor(n)
}

// Ordinal returns the ordinal assigned to a binary id, plus whether the id is
// in the set at all. It is the read-only replacement for the index map: tests
// that used to do `set.Index["id"]` now do `set.Ordinal("id")`, and there is
// no way to write through it.
func (p PreviewImages) Ordinal(id string) (int, bool) {
	n, ok := p.index[id]
	return n, ok
}

// Len reports how many binaries passed policy and received an ordinal. Useful
// for tests that need to assert "no work was done under a canceled ctx"
// without writing through the index.
func (p PreviewImages) Len() int {
	return len(p.index)
}

// Base returns the address prefix the set was built under. Tests use it to
// ask the code what shape an address takes, instead of hard-coding one.
func (p PreviewImages) Base() PreviewImageBase {
	return p.base
}

// BuildPreviewImages applies image policy to a book's binaries once and
// returns what the renderer may reference, alongside the prepared bytes the
// handler will serve and the typed refusal for every binary the pipeline
// turned down. None of that used to survive this function: ordinals were
// kept, but the bytes PreparePreviewImage had just produced were discarded
// (forcing the handler to redo the decode and transcode on demand), and the
// refusal reasons were collapsed into a silent skip (leaving no way to count
// how many pictures were dropped, or why). Keeping both here closes that gap
// at the boundary where the work happens.
//
// Ordinals follow the sorted binary ids so the same book always yields the
// same addresses: the mapping is part of the contract between the renderer
// that emits a URL and the handler that answers it. Only binaries that pass
// policy consume an ordinal; refusals are recorded but never numbered.
//
// The ctx is consulted between binaries, not just at entry, because each
// binary drives a prepare call that may decode and transcode a real picture
// — that is the work, and that is what cancellation has to stop. A return
// with a non-nil error means the set is not authoritative: callers must
// check err first.
func BuildPreviewImages(
	ctx context.Context,
	binaries map[string]FB2Binary,
	base PreviewImageBase,
	policy PreviewImagePolicy,
) (PreviewImageSet, error) {
	// The zero value of PreviewImageBase carries an empty path. URLFor
	// would still produce "/N" out of it — real-looking addresses that
	// route nowhere. Refuse before the loop touches a single binary, so
	// the empty base cannot mint addresses even by accident.
	if base.path == "" {
		return PreviewImageSet{}, fmt.Errorf("%w: base was not built through NewPreviewImageBase", ErrPreviewImageBaseInvalid)
	}
	ids := make([]string, 0, len(binaries))
	for id := range binaries {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	set := PreviewImageSet{
		base:    base,
		byID:    make(map[string]int, len(binaries)),
		Refused: make(map[string]error, len(binaries)),
	}
	n := 0
	for _, id := range ids {
		// One binary is one unit of work: prepare may decode and re-encode
		// a real picture underneath. Check ctx between binaries, so a
		// cancel that arrives during a transcode still stops the next one
		// — rather than walking the rest of the map.
		if err := ctx.Err(); err != nil {
			return set, fmt.Errorf("fb2 preview: image build canceled: %w", err)
		}
		// Decision and preparation are one call: an address is issued only
		// when the same call has produced the bytes the handler will serve,
		// and those bytes are kept on the result. Throwing them away would
		// mean the handler re-decoding and re-transcoding the same picture
		// later — the work this function exists to do once.
		payload, mime, err := preparePreviewImage(binaries[id].Data, policy)
		if err != nil {
			set.Refused[id] = err
			continue
		}
		n++
		set.byID[id] = len(set.Images)
		set.Images = append(set.Images, PreparedPreviewImage{
			ID:      id,
			Ordinal: n,
			Payload: payload,
			MIME:    mime,
		})
	}
	return set, nil
}

// preparePreviewImage is the package-level indirection over PreparePreviewImage
// so tests can count how many times a given binary was prepared. Production
// always uses PreparePreviewImage itself; the variable is never reassigned
// outside tests. A test that swaps it must restore it on cleanup.
var preparePreviewImage = PreparePreviewImage

// PreparedPreviewImage is one binary that passed policy, with the exact bytes
// the handler will serve to the reader. The Payload is what PreparePreviewImage
// produced; nothing re-encodes it downstream.
type PreparedPreviewImage struct {
	ID      string // book-binary id, the same key the renderer looks up
	Ordinal int    // 1-based position under base
	Payload []byte // final bytes the handler serves
	MIME    string // MIME type matching Payload
}

// PreviewImageSet is the result of BuildPreviewImages: the prepared pictures
// ready to serve, and the typed refusal for every binary that did not pass.
// Images is sorted by Ordinal; Refused is keyed by binary id; the read-only
// projection the renderer uses is exposed through Projection so the underlying
// index stays private.
type PreviewImageSet struct {
	Images  []PreparedPreviewImage
	Refused map[string]error

	base PreviewImageBase
	byID map[string]int // binary id -> index into Images
}

// Projection returns the read-only view the renderer and chunker consume: a
// PreviewImages carrying the same base and the same id-to-ordinal mapping.
// Keeping it as a separate type preserves the existing renderer contract
// (URL, Ordinal, Len, Base) without exposing the prepared bytes to code that
// has no business reading them.
func (s PreviewImageSet) Projection() PreviewImages {
	index := make(map[string]int, len(s.Images))
	for _, img := range s.Images {
		index[img.ID] = img.Ordinal
	}
	return PreviewImages{base: s.base, index: index}
}

// RefusalReason unwraps the recorded reason for one id, or nil if the id was
// accepted. Callers that count by cause use errors.Is against the typed
// refusals (ErrPreviewImageUnsupported, ErrPreviewImageDimensions, etc.).
func (s PreviewImageSet) RefusalReason(id string) error {
	return s.Refused[id]
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
func PreparePreviewImage(data []byte, policy PreviewImagePolicy) (payload []byte, mime string, err error) {
	// The byte cap on the input is checked before any decode: a binary the
	// size of a book must not reach a decoder that would allocate from the
	// header it carries.
	if len(data) > policy.MaxBytes {
		return nil, "", fmt.Errorf("%w: payload is %d bytes, cap is %d",
			ErrPreviewImageTooLarge, len(data), policy.MaxBytes)
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
	// preparation become one call.
	//
	// A refusal comes back as a typed error. We map its reason onto ours:
	// "too large for our internal cap" is a policy outcome and goes to
	// ErrPreviewImageDimensions, not into the corrupt bucket — those two
	// drive different counters and different policy levers, and folding
	// them together was the original defect (sizes were reported as
	// corruption).
	var nerr error
	payload, mime, nerr = fb2image.Normalize(data)
	if nerr != nil {
		return nil, "", mapNormalizeError(nerr)
	}

	// The renderable-as-is formats (PNG/JPEG/GIF/WEBP) pass through
	// Normalize unchanged, so the policy's own pixel ceiling has to be
	// enforced here, on the bytes we will actually serve. DecodeConfig
	// reads the header only — a forged 20000x20000 declaration costs a
	// header read, not the allocation it asks for.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(payload))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, "", fmt.Errorf("%w: header would not decode", ErrPreviewImageCorrupt)
	}
	if cfg.Width*cfg.Height > policy.MaxPixels {
		return nil, "", fmt.Errorf("%w: declared %dx%d, cap is %d pixels",
			ErrPreviewImageDimensions, cfg.Width, cfg.Height, policy.MaxPixels)
	}
	// Per-side cap. fb2image.Normalize enforces the same limit on the
	// transcode path (BMP/TIFF), but pass-through formats (PNG/JPEG/GIF/
	// WEBP) skip it. Without this check the same shape of picture is
	// admitted or refused depending on the container — accidental policy,
	// not a deliberate one. The value mirrors fb2image.maxDimension so the
	// two paths agree.
	if cfg.Width > policy.MaxSide || cfg.Height > policy.MaxSide {
		return nil, "", fmt.Errorf("%w: declared %dx%d, per-side cap is %d",
			ErrPreviewImageDimensions, cfg.Width, cfg.Height, policy.MaxSide)
	}

	// Final size check on the bytes the reader will receive. Re-encoding a
	// BMP as PNG can come out bigger than the source, so the input gate
	// above does not see this size — only this gate does.
	if len(payload) > policy.MaxBytes {
		return nil, "", fmt.Errorf("%w: %d bytes after normalize, cap is %d",
			ErrPreviewImageTooLargeResult, len(payload), policy.MaxBytes)
	}

	return payload, mime, nil
}

// mapNormalizeError translates a fb2image.Normalize refusal into the preview's
// own typed reasons. ErrTooLarge is a policy outcome and goes to Dimensions —
// it tells the catalog a picture was fine but too big for our preview budget,
// distinct from corruption. Everything else from Normalize (undecodable,
// encode failed, or an unrecognized format that slipped past Classify) is the
// reader's "broken bytes" answer and goes to Corrupt.
//
// Both errors are wrapped with %w, not %v: callers may need either reason —
// the outer one to count by category, the inner one to debug which step
// refused — and errors.Is has to find them both in the same value.
func mapNormalizeError(err error) error {
	switch {
	case errors.Is(err, fb2image.ErrTooLarge):
		return fmt.Errorf("%w: fb2image.Normalize refused on size: %w",
			ErrPreviewImageDimensions, err)
	case errors.Is(err, fb2image.ErrUnknownFormat):
		// Classify already filtered the obvious cases, so reaching here is
		// rare. Treat it as corrupt: Classify said yes, so the magic
		// matched, and yet Normalize could not place it — the bytes are
		// not what the magic promised.
		return fmt.Errorf("%w: fb2image.Normalize could not place the format: %w",
			ErrPreviewImageCorrupt, err)
	default:
		// ErrUndecodable, ErrEncodeFailed, and any future decode-side
		// refusal all surface as corrupt: the reader's bytes would not
		// come out, regardless of which step gave up.
		return fmt.Errorf("%w: fb2image.Normalize refused: %w",
			ErrPreviewImageCorrupt, err)
	}
}
