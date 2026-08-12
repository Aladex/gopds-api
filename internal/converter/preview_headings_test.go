package converter

// preview_headings_test.go pins the read-only access to a portion's section
// headings: the manifest's table of contents is built from it, so the anchor
// it reports must be the very anchor the renderer emits into the HTML — a TOC
// that points nowhere is worse than none.

import (
	"context"
	"fmt"
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
	wantAnchors := []string{"pv-ch1", "pv-ch1a", "pv-ch2"}
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
	if headings[0].Anchor != "pv-ch1" {
		t.Errorf("section with id ch1 got anchor %q, want pv-ch1", headings[0].Anchor)
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

// The anchor a TOC entry carries must lead to that entry's own heading, not
// merely to an id that exists somewhere in the fragment. The fixture forces
// the divergence this guards against: the first section's book-supplied id
// occupies the synthetic slot the titleless middle section would take, so the
// renderer's collision bump shifts every later synthetic anchor — and a TOC
// that re-derives anchors while skipping the titleless section lands one slot
// short, pointing the last entry at the middle section's anchor.
func TestPreviewChunk_HeadingsAnchorLeadsToOwnHeading(t *testing.T) {
	doc := &FB2Document{Body: &FB2BodySection{
		Content: []*FB2ContentItem{
			{Section: &FB2BodySection{
				ID:      "!auto-1",
				Title:   "Первая",
				Content: []*FB2ContentItem{{Paragraph: textPara("текст один")}},
			}},
			{Section: &FB2BodySection{
				Content: []*FB2ContentItem{{Paragraph: textPara("секция без заголовка")}},
			}},
			{Section: &FB2BodySection{
				Title:   "Третья",
				Content: []*FB2ContentItem{{Paragraph: textPara("текст три")}},
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
	if len(headings) != 2 {
		t.Fatalf("expected 2 headings (the titleless section is skipped), got %d: %+v", len(headings), headings)
	}

	html, err := RenderChunkHTML(chunks[0], PreviewImages{}, testPreviewPolicy())
	if err != nil {
		t.Fatalf("RenderChunkHTML: %v", err)
	}
	auditPreviewHTML(t, html)

	// The anchor's <a> must be immediately followed by the heading the TOC
	// entry names. Existence alone is not enough: the wrong anchor is also a
	// real, unique id — it just belongs to the previous section.
	for _, h := range headings {
		tag := fmt.Sprintf("h%d", clampHeading(h.Depth))
		want := `<a id="` + h.Anchor + `"></a>` + "\n<" + tag + ">" + h.Title + "</" + tag + ">"
		if !strings.Contains(html, want) {
			t.Errorf("heading %q: anchor %q is not immediately followed by its own heading\nwant fragment: %s",
				h.Title, h.Anchor, want)
		}
	}

	// The titleless middle section still reserves its anchor in the HTML: the
	// reservation is what the collision bump accounts for, and dropping it is
	// the other half of the same drift.
	if !strings.Contains(html, "</a>\n<p>секция без заголовка</p>") {
		t.Errorf("the titleless section lost its anchor — the paragraph is not preceded by an <a> anchor")
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

// Two blocks can carry the same id, or two ids can normalise to the same
// anchor. They cannot share the anchor: the renderer emits a repeated id only
// once, so the second heading would have none of its own and its
// table-of-contents entry would land on the first. Resolving an href by that
// id is the separate question, and it still goes to the first occurrence.
func TestPreviewChunk_DuplicateSourceIDsGetOwnAnchors(t *testing.T) {
	const src = `<?xml version="1.0"?><FictionBook><body>` +
		`<section id="dup"><title><p>ПЕРВАЯ</p></title><p><a xlink:href="#dup">ССЫЛКА</a></p></section>` +
		`<section id="dup"><title><p>ВТОРАЯ</p></title><p>текст</p></section>` +
		`<section id="ab"><title><p>ТРЕТЬЯ</p></title><p>текст</p></section>` +
		`<section id="a b"><title><p>ЧЕТВЁРТАЯ</p></title><p>текст</p></section>` +
		`</body></FictionBook>`

	doc, err := ParseFB2Body(context.Background(), []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	chunks, err := ChunkPreview(context.Background(), doc, PreviewImages{}, testPreviewPolicy())
	if err != nil {
		t.Fatalf("chunk: %v", err)
	}

	seen := map[string]string{}
	var firstAnchor string
	for _, chunk := range chunks {
		html, rerr := RenderChunkHTML(chunk, PreviewImages{}, testPreviewPolicy())
		if rerr != nil {
			t.Fatalf("render: %v", rerr)
		}
		for _, h := range chunk.Headings() {
			if prev, dup := seen[h.Anchor]; dup {
				t.Errorf("anchor %q serves both %q and %q — the second heading is unreachable",
					h.Anchor, prev, h.Title)
			}
			seen[h.Anchor] = h.Title
			if firstAnchor == "" {
				firstAnchor = h.Anchor
			}
			// The anchor must sit immediately before its own heading, not
			// merely exist somewhere in the portion.
			marker := `id="` + h.Anchor + `"></a>` + "\n" + `<h`
			pos := strings.Index(html, marker)
			if pos == -1 {
				t.Errorf("heading %q: anchor %q does not stand before a heading", h.Title, h.Anchor)
				continue
			}
			if idx := strings.Index(html[pos:], h.Title); idx == -1 || idx > 40 {
				t.Errorf("heading %q: anchor %q stands before a different heading", h.Title, h.Anchor)
			}
		}
		if strings.Contains(html, `href="#`) && !strings.Contains(html, `href="#`+firstAnchor+`"`) {
			t.Errorf("a link by the duplicated id no longer resolves to the first occurrence: %s", shorten(html))
		}
	}
	if len(seen) != 4 {
		t.Errorf("got %d distinct anchors for 4 headings: %v", len(seen), seen)
	}
}

// A repeated id may land in a different portion than the first occurrence.
// The link must still mean the first one: deciding ownership from what
// happens to be in the portion would make the repeat locally first and send
// the reader to the wrong section. When the first occurrence is elsewhere,
// the link unwraps, exactly as it does for any other cross-portion target.
func TestPreviewChunk_FirstWinsSurvivesChunking(t *testing.T) {
	filler := strings.Repeat("СЛОВО ", 150)
	src := `<?xml version="1.0"?><FictionBook><body>` +
		`<section id="dup"><title><p>ПЕРВАЯ</p></title><p>` + filler + `</p></section>` +
		`<section id="dup"><title><p>ВТОРАЯ</p></title>` +
		`<p><a xlink:href="#dup">ССЫЛКА РЯДОМ С ПОВТОРОМ</a></p></section>` +
		`</body></FictionBook>`

	doc, err := ParseFB2Body(context.Background(), []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Block costs, measured: header 42, filler 1658, second header 46, link
	// 73. A ceiling of 1720 holds the first section (1700) and leaves less
	// room than the second header needs, so the repeat and the link travel
	// together into the next portion — away from the first occurrence, which
	// is the only arrangement that tests what this is about.
	policy := PreviewPolicy{MaxChunkBytes: 1720}
	chunks, err := ChunkPreview(context.Background(), doc, PreviewImages{}, policy)
	if err != nil {
		t.Fatalf("chunk: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("the fixture stayed in one portion (%d) — it cannot test what happens across them", len(chunks))
	}

	var secondAnchor, htmlWithLink string
	for _, chunk := range chunks {
		html, rerr := RenderChunkHTML(chunk, PreviewImages{}, policy)
		if rerr != nil {
			t.Fatalf("render: %v", rerr)
		}
		for _, h := range chunk.Headings() {
			if h.Title == "ВТОРАЯ" {
				secondAnchor = h.Anchor
			}
		}
		if strings.Contains(html, "ССЫЛКА РЯДОМ С ПОВТОРОМ") {
			htmlWithLink = html
		}
	}
	if secondAnchor == "" {
		t.Fatal("the repeated section reported no anchor of its own")
	}
	if secondAnchor == "pv-dup" {
		t.Errorf("the repeated section took the anchor of the first: %q", secondAnchor)
	}
	if htmlWithLink == "" {
		t.Fatal("no portion carried the link")
	}
	if strings.Contains(htmlWithLink, `href="#`+secondAnchor+`"`) {
		t.Errorf("the link was redirected to the repeat's own anchor %q — first-wins did not survive chunking", secondAnchor)
	}
	if strings.Contains(htmlWithLink, `href="#`) {
		t.Errorf("the link resolved inside a portion that does not hold the first occurrence: %s", shorten(htmlWithLink))
	}
	if !strings.Contains(htmlWithLink, "ССЫЛКА РЯДОМ С ПОВТОРОМ") {
		t.Error("the visible text of the unwrapped link went away")
	}
}
