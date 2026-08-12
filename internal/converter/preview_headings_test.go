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
	wantAnchors := []string{"pv0-ch1", "pv0-ch1a", "pv0-ch2"}
	for i, h := range headings {
		if h.Title != wantTitles[i] {
			t.Errorf("heading %d: title = %q, want %q", i, h.Title, wantTitles[i])
		}
		if h.Depth != wantDepths[i] {
			t.Errorf("heading %d: depth = %d, want %d", i, h.Depth, wantDepths[i])
		}
		if h.Anchor != wantAnchors[i] {
			t.Errorf("heading %d (%q): anchor = %q, want %q", i, h.Title, h.Anchor, wantAnchors[i])
		}
	}

	// Every anchor the TOC would carry must exist as an id in the rendered
	// portion — this is the whole point of the accessor.
	html, err := RenderChunkHTML(chunks[0], PreviewImages{}, testPreviewPolicy())
	if err != nil {
		t.Fatalf("RenderChunkHTML: %v", err)
	}
	for _, h := range headings {
		if !strings.Contains(html, `id="`+h.Anchor+`"`) {
			t.Errorf("anchor %q for %q is not in the rendered HTML — the TOC would point nowhere",
				h.Anchor, h.Title)
		}
	}
}

// A section without an id gets a synthetic anchor so the TOC can still deep-link
// to it. The synthetic value must be emitted into the HTML as a real id.
func TestPreviewChunk_HeadingsWithoutIDGetSyntheticAnchor(t *testing.T) {
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
	if headings[0].Anchor == "" {
		t.Fatalf("a section without an id got an empty anchor")
	}

	html, err := RenderChunkHTML(chunks[0], PreviewImages{}, testPreviewPolicy())
	if err != nil {
		t.Fatalf("RenderChunkHTML: %v", err)
	}
	if !strings.Contains(html, `id="`+headings[0].Anchor+`"`) {
		t.Errorf("synthetic anchor %q for %q is not in the rendered HTML",
			headings[0].Anchor, headings[0].Title)
	}

	// Re-chunking the same document under the same policy must produce the same
	// synthetic anchor: the TOC and the HTML are cached separately and must not
	// drift apart on a rebuild.
	chunks2, err := ChunkPreview(context.Background(), doc, PreviewImages{}, testPreviewPolicy())
	if err != nil {
		t.Fatalf("second ChunkPreview: %v", err)
	}
	headings2 := chunks2[0].Headings()
	if len(headings2) != 1 {
		t.Fatalf("expected 1 heading on rebuild, got %d", len(headings2))
	}
	if headings[0].Anchor != headings2[0].Anchor {
		t.Errorf("synthetic anchor is not deterministic: %q vs %q",
			headings[0].Anchor, headings2[0].Anchor)
	}
}

// Two anonymous sections in the same chunk must receive distinct anchors so the
// TOC can distinguish them.
func TestPreviewChunk_HeadingsTwoAnonymousSectionsDiffer(t *testing.T) {
	doc := &FB2Document{Body: &FB2BodySection{
		Content: []*FB2ContentItem{
			{Section: &FB2BodySection{
				Title:   "РАССКАЗ ПЕРВЫЙ",
				Content: []*FB2ContentItem{{Paragraph: textPara("текст первого")}},
			}},
			{Section: &FB2BodySection{
				Title:   "РАССКАЗ ВТОРОЙ",
				Content: []*FB2ContentItem{{Paragraph: textPara("текст второго")}},
			}},
		},
	}}

	chunks, err := ChunkPreview(context.Background(), doc, PreviewImages{}, testPreviewPolicy())
	if err != nil {
		t.Fatalf("ChunkPreview: %v", err)
	}
	headings := chunks[0].Headings()
	if len(headings) != 2 {
		t.Fatalf("expected 2 headings, got %d", len(headings))
	}
	if headings[0].Anchor == "" || headings[1].Anchor == "" {
		t.Fatalf("anonymous sections must have synthetic anchors")
	}
	if headings[0].Anchor == headings[1].Anchor {
		t.Errorf("two anonymous sections got the same anchor %q", headings[0].Anchor)
	}

	html, err := RenderChunkHTML(chunks[0], PreviewImages{}, testPreviewPolicy())
	if err != nil {
		t.Fatalf("RenderChunkHTML: %v", err)
	}
	for _, h := range headings {
		if !strings.Contains(html, `id="`+h.Anchor+`"`) {
			t.Errorf("anchor %q for %q is missing from the HTML", h.Anchor, h.Title)
		}
	}
}

// Headings must not be used as the basis of the anchor: two sections can share
// the same title, and an empty title is skipped rather than producing an empty
// or colliding anchor.
func TestPreviewChunk_HeadingsDuplicateTitlesDiffer(t *testing.T) {
	doc := &FB2Document{Body: &FB2BodySection{
		Content: []*FB2ContentItem{
			{Section: &FB2BodySection{
				Title:   "ГЛАВА 1",
				Content: []*FB2ContentItem{{Paragraph: textPara("текст первой части")}},
			}},
			{Section: &FB2BodySection{
				Title:   "ГЛАВА 1",
				Content: []*FB2ContentItem{{Paragraph: textPara("текст второй части")}},
			}},
		},
	}}

	chunks, err := ChunkPreview(context.Background(), doc, PreviewImages{}, testPreviewPolicy())
	if err != nil {
		t.Fatalf("ChunkPreview: %v", err)
	}
	headings := chunks[0].Headings()
	if len(headings) != 2 {
		t.Fatalf("expected 2 headings, got %d", len(headings))
	}
	if headings[0].Anchor == headings[1].Anchor {
		t.Errorf("sections with the same title got the same anchor %q", headings[0].Anchor)
	}
}

// A section that already carries an id keeps its original anchor; cross-links
// and footnotes inside the book rely on it.
func TestPreviewChunk_HeadingsPreserveOriginalID(t *testing.T) {
	doc := &FB2Document{Body: &FB2BodySection{
		Content: []*FB2ContentItem{
			{Section: &FB2BodySection{
				ID:      "ch1",
				Title:   "ГЛАВА",
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
	if headings[0].Anchor != "pv0-ch1" {
		t.Errorf("section with id ch1 got anchor %q, want pv0-ch1", headings[0].Anchor)
	}
}

// A synthetic anchor must never shadow a real book id, even if that id happens
// to match the synthetic prefix form. The malformed id "!auto-0" is not valid
// XML, but the renderer still resolves the collision rather than dropping one
// of the anchors.
func TestPreviewChunk_HeadingsSyntheticAnchorAvoidsCollision(t *testing.T) {
	doc := &FB2Document{Body: &FB2BodySection{
		Content: []*FB2ContentItem{
			{Section: &FB2BodySection{
				ID:      "!auto-0",
				Title:   "ЗАНЯВШИЙ ПРЕФИКС",
				Content: []*FB2ContentItem{{Paragraph: textPara("текст с исходным id")}},
			}},
			{Section: &FB2BodySection{
				Title:   "БЕЗЫМЯННАЯ",
				Content: []*FB2ContentItem{{Paragraph: textPara("текст без id")}},
			}},
		},
	}}

	chunks, err := ChunkPreview(context.Background(), doc, PreviewImages{}, testPreviewPolicy())
	if err != nil {
		t.Fatalf("ChunkPreview: %v", err)
	}
	headings := chunks[0].Headings()
	if len(headings) != 2 {
		t.Fatalf("expected 2 headings, got %d", len(headings))
	}

	html, err := RenderChunkHTML(chunks[0], PreviewImages{}, testPreviewPolicy())
	if err != nil {
		t.Fatalf("RenderChunkHTML: %v", err)
	}
	// auditPreviewHTML fails on duplicate ids, which is exactly what happens if
	// the synthetic anchor collides with the source id.
	auditPreviewHTML(t, html)

	for _, h := range headings {
		if !strings.Contains(html, `id="`+h.Anchor+`"`) {
			t.Errorf("anchor %q for %q is missing from the HTML", h.Anchor, h.Title)
		}
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
