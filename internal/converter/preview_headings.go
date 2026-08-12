package converter

// preview_headings.go exposes the section headings of a portion. The preview
// manifest's table of contents is built from this accessor, and its one hard
// requirement is that the anchor it reports is the very id the renderer emits
// into the portion's HTML — a TOC pointing at ids that do not exist is worse
// than no TOC, and a TOC pointing at the wrong section is worse still. That
// is why the accessor does not derive anchors at all: it reads the chunk's
// anchor table, assigned once by the chunker and shared with the
// renderer, so there is no second walk that could apply different rules.

import "strings"

// PreviewHeading is one section heading of a portion: the visible title, the
// section depth (1 for top level), and the chunk-local anchor the renderer
// emitted for it. A section without an id of its own carries the synthetic
// anchor the assignment pass reserved for it, so every listed heading can be
// deep-linked.
type PreviewHeading struct {
	Title  string
	Depth  int
	Anchor string
}

// Headings returns the portion's section headings in document order. Sections
// without a title are skipped, matching renderSectionHeader, which renders
// nothing for them — but only from the listing: their anchors were still
// reserved by the assignment pass, and the anchors reported here are the
// values that pass produced, identical to what the renderer emitted.
func (c *PreviewChunk) Headings() []PreviewHeading {
	anchors := c.anchorTable().byBlock
	var out []PreviewHeading
	for i, block := range c.blocks {
		if block.header == nil {
			continue
		}
		title := strings.TrimSpace(block.header.Title)
		if title == "" {
			continue
		}
		out = append(out, PreviewHeading{
			Title:  title,
			Depth:  block.depth,
			Anchor: anchors[i],
		})
	}
	return out
}
