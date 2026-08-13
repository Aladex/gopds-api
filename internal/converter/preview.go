package converter

// preview.go holds the shared types of the preview pipeline: the cutting of a
// parsed book into size-bounded portions and the rendering of a portion into
// safe HTML. The book file is untrusted input, so every policy decision
// (which tags, which attributes, which links, which images) is made here and
// verified against the rendered bytes, never assumed from the source.

import (
	"errors"
	"sync"
)

// Refusals about size. Two of them are facts about the book and travel to
// the reader as such; the third is this package failing and must not.
var (
	// ErrPreviewBlockTooLarge marks a book carrying a single indivisible
	// block that renders past MaxPortionBytes.
	//
	// A lone block over MaxChunkBytes is allowed — a paragraph has no seam to
	// cut, and refusing meant a book like Kafka's "Замок" could not be read
	// at all. Past MaxPortionBytes there is no reading left to protect: the
	// portion has stopped being a portion.
	ErrPreviewBlockTooLarge = errors.New("fb2 preview: indivisible block exceeds the portion ceiling")

	// ErrPreviewBookTooLarge is the same judgement about the whole book
	// rather than one portion: every portion fits and their sum does not.
	// A footnote is re-embedded in every portion that cites it, so a source
	// under 1 MiB with four thousand references to a 512 KiB note renders
	// about 2 GiB — measured by review — while passing every per-portion
	// check.
	ErrPreviewBookTooLarge = errors.New("fb2 preview: rendered book exceeds the total ceiling")

	// ErrPreviewPortionOverflow marks a portion of several blocks that came
	// out over the packing ceiling. The packing loop let something through it
	// should have moved on: a defect here, not a property of the book, and
	// reporting it to a reader as one would be a lie they cannot act on.
	ErrPreviewPortionOverflow = errors.New("fb2 preview: portion of several blocks exceeds the chunk ceiling")
)

// Refusals returned by PreparePreviewImage. Each names a distinct reason so
// callers can count by cause and tune policy against what actually bites.
//
// They are returned wrapped (fmt.Errorf("%w: ...", Err...)), never plain, so
// errors.Is tells the four apart. Every one means "no address is issued for
// this binary": the reader sees the placeholder, the handler is never asked
// for bytes it could not have produced.
var (
	// ErrPreviewImageUnsupported: the bytes carry no recognized image magic
	// (prose, HTML, empty payload), or the only magic they carry is SVG.
	// SVG is an XML document that can carry script and would run under the
	// reader's origin, so it is refused by format alone.
	ErrPreviewImageUnsupported = errors.New("fb2 preview: image format is not supported")

	// ErrPreviewImageCorrupt: the bytes look like an image but do not decode,
	// or fb2image.Normalize refused to produce a payload from them. Anything
	// Normalize turns down — a forged BMP header past its dimension cap, a
	// truncated stream — lands here, because Normalize is the authority and
	// its refusal reason is opaque.
	ErrPreviewImageCorrupt = errors.New("fb2 preview: image payload is corrupt or undecodable")

	// ErrPreviewImageTooLarge: the input itself is over the per-image byte
	// cap, checked before anything is decoded.
	ErrPreviewImageTooLarge = errors.New("fb2 preview: image payload exceeds the byte cap")

	// ErrPreviewImageDimensions: the decoded header declares more pixels
	// than the per-image pixel cap allows for one canvas. DecodeConfig reads
	// the header only, so a forged 20000x20000 declaration costs a header
	// read, not the allocation it asks for. The cap does not sum frames:
	// an animated payload with small-per-frame canvases is not refused
	// here — animation is admitted on the same terms as static pictures,
	// see PreviewImagePolicy.MaxPixels for the trade-off.
	ErrPreviewImageDimensions = errors.New("fb2 preview: image dimensions exceed the pixel cap")

	// ErrPreviewImageTooLargeResult: the bytes Normalize produced — the very
	// bytes the reader would receive — are over the byte cap. Re-encoding a
	// BMP as PNG can come out bigger than the source.
	ErrPreviewImageTooLargeResult = errors.New("fb2 preview: image payload exceeds the byte cap after normalize")

	// ErrPreviewImagePolicyInvalid: the policy itself is wrong — one of its
	// limits is non-positive. Distinct from the per-image refusals on
	// purpose: a misconfigured policy is a caller bug, and counting it
	// alongside "this binary was too big" would mix a coding error with a
	// property of the data. Both PreparePreviewImage and BuildPreviewImages
	// surface it before touching a single binary, so an empty policy never
	// masquerades as "every picture was refused".
	ErrPreviewImagePolicyInvalid = errors.New("fb2 preview: image policy is not configured")
)

// PreviewPolicy carries the budget of one preview portion: the rendered
// HTML ceiling. Image budgets lived here once, when a picture was inlined as
// a data: URL and the portion byte count had to bound both text and picture
// at once. A picture is now a resource of its own, addressed by a URL the
// server builds, so its caps travel separately under PreviewImagePolicy.
// Holding them here would make two budgets of two different things look like
// one budget of one thing.
type PreviewPolicy struct {
	// MaxChunkBytes is where the packer stops adding blocks to a portion.
	// It is not a hard ceiling: a block too large to divide is put in a
	// portion of its own and that portion goes over. See MaxPortionBytes for
	// the bound that does hold.
	MaxChunkBytes int

	// MaxPortionBytes is the size no portion may reach, whatever it holds.
	//
	// It exists because "a lone block may overflow" is not a bound at all on
	// its own: footnotes are pulled into the portion that cites them and are
	// not blocks, so one paragraph referencing five hundred notes rendered a
	// 40 MiB portion and an 80 MiB response — measured, from an 8 MiB book
	// that passed every input gate. Beyond this line the book really is too
	// large to preview, and that is a fact about the book worth telling the
	// reader.
	MaxPortionBytes int

	// MaxTotalBytes bounds the rendered HTML of the whole book.
	//
	// Per-portion ceilings do not add up to one: a footnote is re-embedded in
	// every portion that cites it, so a source under 1 MiB with four thousand
	// references to a 512 KiB note produces four thousand perfectly legal
	// portions and about 2 GiB of HTML, all of it held at once while the
	// build finishes.
	MaxTotalBytes int
}

// PreviewImagePolicy carries what a single picture must fit to be shown:
// bytes of the served payload, pixels of the decoded canvas, and the longest
// side the canvas may have. These are properties of one image, not of one
// portion, so they live apart from PreviewPolicy. The chunker and renderer
// never read them; only the image preparation path does.
type PreviewImagePolicy struct {
	MaxBytes int // Per-image decoded payload cap
	// MaxPixels bounds one canvas, declared in the header. It catches a
	// forged header that claims a huge frame, but it does not sum frames:
	// an animated GIF/WebP/APNG with small-per-frame canvases is admitted
	// (animation is shown on the same terms as static pictures), so the
	// cap is not a complete defense against a payload that expands at the
	// reader across many frames. That trade-off is deliberate; the test
	// TestPreparePreviewImage_AnimatedPayloadsAccepted pins it.
	MaxPixels int
	// MaxSide is the per-side cap, applied to width and height separately
	// in PreparePreviewImage on every format. There are two layers of
	// dimension checks, and they do not pretend to be one:
	//
	//   - The preview policy (this field, MaxPixels, MaxBytes) runs in
	//     PreparePreviewImage on the payload the reader receives, and it
	//     applies to every format.
	//   - fb2image.Normalize has its own, stricter caps (maxDimension =
	//     4096, maxPixels = 4 MP) that only fire on the transcode path
	//     (BMP/TIFF); pass-through formats (PNG/JPEG/GIF/WEBP) never see
	//     them.
	//
	// The two layers serve different masters: the preview policy is the
	// reader-facing budget, fb2image's caps protect the transcode allocator.
	// They do not match — MaxPixels is 32 MP, fb2image's maxPixels is 4 MP —
	// and that asymmetry is visible: a 3000x3000 BMP (9 MP) is refused by
	// fb2image before preview policy sees it, while a PNG of the same
	// dimensions passes. Setting MaxSide to the same 4096 keeps the per-side
	// answer consistent across formats, but only the per-side; the pixel
	// answer can still differ. That is the cost of Normalize owning the
	// transcode path.
	MaxSide int
}

// chunkBlock is one indivisible block-level unit of the flattened document:
// either a section header (with its depth) or a content paragraph. Cutting
// happens only between blocks.
type chunkBlock struct {
	header *FB2BodySection // Set for a section header block
	depth  int             // Section depth, 1 for top level
	para   *FB2Paragraph   // Set for a paragraph block
	anchor string          // Assigned anchor, unique within the book
	// ownsID marks the block the book's own id points at: the first one
	// carrying it. A later block repeating that id gets its own anchor but no
	// ownership, so a cross-reference keeps meaning the first occurrence even
	// after chunking splits the two apart.
	ownsID bool
}

// PreviewChunk is one portion of the book: an ordered run of blocks plus the
// footnotes referenced from them. Notes travel with the portion because they
// expand in place — there is no notes page to navigate to.
type PreviewChunk struct {
	Index  int
	blocks []chunkBlock
	notes  []*FB2BodySection

	// anchorTab memoizes the one-pass anchor assignment for this portion (see
	// flattenPreviewBlocks). The renderer and Headings both read it, so the
	// anchors in the HTML and the anchors in the TOC are the same values by
	// construction, not by two walks that happen to agree. Blocks must not be
	// appended after the first read — nothing in the pipeline does that.
	anchorTab  *anchorTable
	anchorOnce sync.Once
}
