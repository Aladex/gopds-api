package converter

// preview_chunker.go cuts the flattened document into portions bounded by
// the rendered-HTML ceiling. This is deliberately NOT the EPUB chapter split:
// EPUB maps one section to one file and ships notes as a separate page, while
// the preview cuts a big section into several portions and inlines the notes
// next to their references. Sharing that code would produce duplicate notes
// and broken links, so the chunker is its own algorithm over the shared
// document traversal.
//
// Sizing is done by draft renders: each block is rendered with every fragment
// link assumed to resolve, which is never smaller than the final render
// (unresolving a link only removes the wrapper). A chunk packed under the
// ceiling by draft sizes therefore stays under it after the final render.

import (
	"context"
	"fmt"
)

// ChunkPreview flattens the document and packs it into portions within the
// policy ceiling. A book that cannot be portioned — one indivisible block
// with its footnotes renders larger than a whole portion — is a typed
// refusal, not a silently oversized chunk.
//
// The ctx is consulted between blocks, not just at entry: a reader who
// closed the tab stops the work as soon as the current block is done, rather
// than paying for the rest of the book. The cost of one block is the unit of
// work here, so that is where the cancellation check sits.
func ChunkPreview(ctx context.Context, doc *FB2Document, images PreviewImages, policy PreviewPolicy) ([]*PreviewChunk, error) {
	if policy.MaxChunkBytes <= 0 {
		return nil, fmt.Errorf("fb2 preview: chunk ceiling must be positive, got %d", policy.MaxChunkBytes)
	}
	blocks := flattenPreviewBlocks(doc)
	if len(blocks) == 0 {
		return []*PreviewChunk{{Index: 0}}, nil
	}

	packer := &previewPacker{doc: doc, images: images, policy: policy, notesByID: make(map[string]*FB2BodySection)}
	loadPreviewNotes(packer, doc)

	var chunks []*PreviewChunk
	cur := &PreviewChunk{Index: 0}
	curSize := 0
	curNotes := make(map[string]bool)

	for _, block := range blocks {
		// Each iteration is one unit of work (draft render + packing
		// decision). Checking ctx between units, not at function entry,
		// means a cancel that arrives during work still stops the next
		// block — without paying for the rest of the book.
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("fb2 preview: chunker canceled: %w", err)
		}
		cost, notes, err := packer.draftBlockCost(cur.Index, block, curNotes)
		if err != nil {
			return nil, err
		}
		if curSize+cost > policy.MaxChunkBytes && len(cur.blocks) > 0 {
			chunks = append(chunks, cur)
			cur = &PreviewChunk{Index: cur.Index + 1}
			curSize = 0
			curNotes = make(map[string]bool)
			// In the fresh chunk every note referenced by this block is new.
			cost, notes, err = packer.draftBlockCost(cur.Index, block, curNotes)
			if err != nil {
				return nil, err
			}
		}
		cur.blocks = append(cur.blocks, block)
		for _, note := range notes {
			cur.notes = append(cur.notes, note)
			// Same normalised key as the cost lookup above: a second ref to
			// the same note in the same chunk must be a no-op, not a second
			// pull that pays the note's cost twice.
			curNotes[anchorKey(note.ID)] = true
		}
		curSize += cost
	}
	chunks = append(chunks, cur)
	return chunks, nil
}

// loadPreviewNotes indexes a document's footnotes into the packer's note map,
// keyed by the same normalised anchor the renderer resolves references with.
// First note with a duplicated normalised id wins, matching the renderer's
// anchor dedup; a raw-id key here would make a note like "a b" invisible to
// the lookup and silently drop the footnote text from the portion.
func loadPreviewNotes(packer *previewPacker, doc *FB2Document) {
	if doc == nil {
		return
	}
	for _, note := range doc.Notes {
		if note == nil {
			continue
		}
		key := anchorKey(note.ID)
		if key == "" {
			continue
		}
		if _, taken := packer.notesByID[key]; taken {
			continue
		}
		packer.notesByID[key] = note
		packer.allNotes = append(packer.allNotes, note)
	}
}

// previewPacker carries the document-wide inputs of the packing loop, so the
// cost measurement does not drag them through every call.
type previewPacker struct {
	doc       *FB2Document
	images    PreviewImages
	policy    PreviewPolicy
	notesByID map[string]*FB2BodySection
	allNotes  []*FB2BodySection

	// The draft context is per portion, not per block. Building it per block
	// walked every footnote in the book on every block: a document with two
	// thousand notes paid that cost thousands of times over, whether or not a
	// single note was referenced.
	draft      *previewRender
	draftIndex int
}

// draftFor returns the draft context for one portion, building it once. The
// portion index cannot be shared across portions because it spells the anchor
// prefix, and a draft that measured "pv9-" for a chunk rendered as "pv10-"
// would undercount by a byte per anchor.
func (p *previewPacker) draftFor(chunkIndex int) *previewRender {
	if p.draft == nil || p.draftIndex != chunkIndex {
		draftChunk := &PreviewChunk{Index: chunkIndex, notes: p.allNotes}
		p.draft = newPreviewRender(draftChunk, p.images, p.policy, true)
		p.draftIndex = chunkIndex
	}
	return p.draft
}

// draftBlockCost measures what adding this block to the chunk costs in
// rendered bytes: the block's draft render plus the draft render of every
// footnote the block would newly pull into the chunk. A block whose total
// cost alone exceeds the ceiling is indivisible and refuses the book.
func (p *previewPacker) draftBlockCost(chunkIndex int, block chunkBlock, alreadyInChunk map[string]bool) (int, []*FB2BodySection, error) {
	// The draft context knows every note id: references resolve into their
	// final href form, so sizes are exact for note links and upper bounds for
	// everything else.
	r := p.draftFor(chunkIndex)

	cost := len(r.renderBlock(block))

	var pulled []*FB2BodySection
	if block.para != nil {
		for _, raw := range noteRefsInParagraph(block.para, p.notesByID) {
			if alreadyInChunk[raw] {
				continue
			}
			note := p.notesByID[raw]
			if note == nil {
				continue
			}
			cost += len(r.renderNote(note))
			pulled = append(pulled, note)
		}
	}
	if cost > p.policy.MaxChunkBytes {
		return 0, nil, fmt.Errorf("%w: one block with its footnotes renders %d bytes over the %d ceiling",
			ErrPreviewBlockTooLarge, cost, p.policy.MaxChunkBytes)
	}
	return cost, pulled, nil
}

// flattenPreviewBlocks walks the document into an ordered stream of
// indivisible block units: a section header block for every section, then its
// content. The root container contributes no header of its own.
func flattenPreviewBlocks(doc *FB2Document) []chunkBlock {
	var blocks []chunkBlock
	var walk func(content []*FB2ContentItem, depth int)
	walk = func(content []*FB2ContentItem, depth int) {
		for _, item := range content {
			if item == nil {
				continue
			}
			if item.Paragraph != nil {
				blocks = append(blocks, chunkBlock{para: item.Paragraph})
			}
			if item.Section != nil {
				blocks = append(blocks, chunkBlock{header: item.Section, depth: depth})
				walk(item.Section.Content, depth+1)
			}
		}
	}
	if doc != nil && doc.Body != nil {
		walk(doc.Body.Content, 1)
	}
	return blocks
}
