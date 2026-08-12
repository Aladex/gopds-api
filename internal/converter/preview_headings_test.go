package converter

// preview_headings_test.go pins the read-only access to a portion's section
// headings: the manifest's table of contents is built from it, so the anchor
// it reports must be the very anchor the renderer emits into the HTML — a TOC
// that points nowhere is worse than none.

import (
	"context"
	"strings"
	"testing"
)

// Headings returns section titles in document order with their depth and the
// anchor the renderer emits for the same section. The anchor assertion goes
// through RenderChunkHTML: the id the TOC carries must exist in the HTML it
// points into.
func TestPreviewChunk_Headings(t *testing.T) {
	doc := &FB2Document{Body: &FB2BodySection{
		Content: []*FB2ContentItem{
			{Section: &FB2BodySection{
				ID:    "ch1",
				Title: "ГЛАВА ПЕРВАЯ",
				Content: []*FB2ContentItem{
					{Paragraph: textPara("текст первой главы")},
					{Section: &FB2BodySection{
						ID:      "ch1a",
						Title:   "ПОДРАЗДЕЛ",
						Content: []*FB2ContentItem{{Paragraph: textPara("текст подраздела")}},
					}},
				},
			}},
			{Section: &FB2BodySection{
				ID:      "ch2",
				Title:   "ГЛАВА ВТОРАЯ",
				Content: []*FB2ContentItem{{Paragraph: textPara("текст второй главы")}},
			}},
		},
	}}

	chunks, err := ChunkPreview(context.Background(), doc, PreviewImages{}, testPreviewPolicy())
	if err != nil {
		t.Fatalf("ChunkPreview: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected the small document to fit one chunk, got %d", len(chunks))
	}

	headings := chunks[0].Headings()
	if len(headings) != 3 {
		t.Fatalf("expected 3 headings, got %d: %+v", len(headings), headings)
	}

	wantTitles := []string{"ГЛАВА ПЕРВАЯ", "ПОДРАЗДЕЛ", "ГЛАВА ВТОРАЯ"}
	wantDepths := []int{1, 2, 1}
	for i, h := range headings {
		if h.Title != wantTitles[i] {
			t.Errorf("heading %d: title = %q, want %q", i, h.Title, wantTitles[i])
		}
		if h.Depth != wantDepths[i] {
			t.Errorf("heading %d: depth = %d, want %d", i, h.Depth, wantDepths[i])
		}
		if h.Anchor == "" {
			t.Errorf("heading %d (%q): empty anchor for a section that carries an id", i, h.Title)
		}
	}

	// Every anchor the TOC would carry must exist as an id in the rendered
	// portion — this is the whole point of the accessor.
	html, err := RenderChunkHTML(chunks[0], PreviewImages{}, testPreviewPolicy())
	if err != nil {
		t.Fatalf("RenderChunkHTML: %v", err)
	}
	for _, h := range headings {
		if h.Anchor == "" {
			continue
		}
		if !strings.Contains(html, `id="`+h.Anchor+`"`) {
			t.Errorf("anchor %q for %q is not in the rendered HTML — the TOC would point nowhere",
				h.Anchor, h.Title)
		}
	}
}

// A section without an id renders no anchor, so its heading reports an empty
// one — the TOC entry must not invent an address the HTML does not have.
func TestPreviewChunk_HeadingsWithoutIDHaveNoAnchor(t *testing.T) {
	doc := &FB2Document{Body: &FB2BodySection{
		Content: []*FB2ContentItem{
			{Section: &FB2BodySection{
				Title:   "БЕЗЫМЯННАЯ ГЛАВА",
				Content: []*FB2ContentItem{{Paragraph: textPara("текст")}},
			}},
		},
	}}

	chunks, err := ChunkPreview(context.Background(), doc, PreviewImages{}, testPreviewPolicy())
	if err != nil {
		t.Fatalf("ChunkPreview: %v", err)
	}
	headings := chunks[0].Headings()
	if len(headings) != 1 {
		t.Fatalf("expected 1 heading, got %d", len(headings))
	}
	if headings[0].Anchor != "" {
		t.Errorf("a section without an id got anchor %q — the renderer emits no such id", headings[0].Anchor)
	}
}

// A section with no title renders no heading at all (renderSectionHeader
// returns early), so Headings skips it: a TOC entry with no visible text
// would be a dead row.
func TestPreviewChunk_HeadingsSkipUntitledSections(t *testing.T) {
	doc := &FB2Document{Body: &FB2BodySection{
		Content: []*FB2ContentItem{
			{Section: &FB2BodySection{
				ID: "untitled",
				Content: []*FB2ContentItem{
					{Paragraph: textPara("текст без заголовка")},
					{Section: &FB2BodySection{
						ID:      "titled",
						Title:   "НАЗВАННАЯ",
						Content: []*FB2ContentItem{{Paragraph: textPara("текст")}},
					}},
				},
			}},
		},
	}}

	chunks, err := ChunkPreview(context.Background(), doc, PreviewImages{}, testPreviewPolicy())
	if err != nil {
		t.Fatalf("ChunkPreview: %v", err)
	}
	headings := chunks[0].Headings()
	if len(headings) != 1 {
		t.Fatalf("expected exactly 1 heading (the titled section), got %d: %+v", len(headings), headings)
	}
	if headings[0].Title != "НАЗВАННАЯ" {
		t.Errorf("the surviving heading is %q, want %q", headings[0].Title, "НАЗВАННАЯ")
	}
}
