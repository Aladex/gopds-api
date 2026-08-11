package converter

// preview.go holds the shared types of the preview pipeline: the cutting of a
// parsed book into size-bounded portions and the rendering of a portion into
// safe HTML. The book file is untrusted input, so every policy decision
// (which tags, which attributes, which links, which images) is made here and
// verified against the rendered bytes, never assumed from the source.

import "errors"

// ErrPreviewBlockTooLarge marks a book that cannot be portioned within the
// ceiling: a single indivisible block (with the footnotes it drags in)
// renders larger than a whole portion is allowed to be. The honest outcome
// for such a book is a refusal, not a silent overflow.
var ErrPreviewBlockTooLarge = errors.New("fb2 preview: indivisible block exceeds the chunk ceiling")

// PreviewPolicy carries the budgets and limits of the preview output. All
// sizes are counted in bytes of the final rendered HTML, never in model
// units — the model is not what the reader's browser receives.
type PreviewPolicy struct {
	MaxChunkBytes  int // Hard ceiling on the rendered HTML of one portion
	MaxImageBytes  int // Per-image decoded payload cap
	MaxImagePixels int // Per-image pixel cap, against decompression bombs
}

// chunkBlock is one indivisible block-level unit of the flattened document:
// either a section header (with its depth) or a content paragraph. Cutting
// happens only between blocks.
type chunkBlock struct {
	header *FB2BodySection // Set for a section header block
	depth  int             // Section depth, 1 for top level
	para   *FB2Paragraph   // Set for a paragraph block
}

// PreviewChunk is one portion of the book: an ordered run of blocks plus the
// footnotes referenced from them. Notes travel with the portion because they
// expand in place — there is no notes page to navigate to.
type PreviewChunk struct {
	Index  int
	blocks []chunkBlock
	notes  []*FB2BodySection
}
