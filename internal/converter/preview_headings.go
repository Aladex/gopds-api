package converter

// preview_headings.go exposes the section headings of a portion. The preview
// manifest's table of contents is built from this accessor, and its one hard
// requirement is that the anchor it reports is the very id the renderer emits
// into the portion's HTML — a TOC pointing at ids that do not exist is worse
// than no TOC. That is why the accessor does not re-derive the anchor format:
// it asks the renderer's own anchor table.

import "strings"

// PreviewHeading is one section heading of a portion: the visible title, the
// section depth (1 for top level), and the chunk-local anchor the renderer
// emitted for it. The anchor is empty when the section carries no id, because
// the renderer emits no anchor for such a section — the heading is still
// listed, the reader just cannot deep-link to it.
type PreviewHeading struct {
	Title  string
	Depth  int
	Anchor string
}

// Headings returns the portion's section headings in document order. Sections
// without a title are skipped, matching renderSectionHeader, which renders
// nothing for them. The anchor lookup reuses newPreviewRender's anchor table,
// so the "first id wins" dedup and the id normalization cannot drift apart
// between the HTML and the TOC built on top of it.
func (c *PreviewChunk) Headings() []PreviewHeading {
	r := newPreviewRender(c, PreviewImages{}, PreviewPolicy{}, false)
	var out []PreviewHeading
	for _, block := range c.blocks {
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
			Anchor: r.anchors[anchorKey(block.header.ID)],
		})
	}
	return out
}
