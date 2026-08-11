package converter

// preview_render_test.go pins the safety policy of the preview renderer. The
// book file is untrusted: every link scheme, every declared image type, every
// anchor id is hostile until the rendered bytes prove otherwise. The main
// test is the output invariant — the rendered fragment is re-parsed and the
// whole document checked, because string searches do not see what the
// browser builds. CSP does not back this up: the app already allows
// unsafe-inline, so a sanitizer miss executes under the reader's session.

import (
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"testing"
)

// linkPara builds a paragraph whose only content is a link with the given
// href wrapping the visible text.
func linkPara(href, visible string) *FB2Paragraph {
	return &FB2Paragraph{
		Kind: ParagraphKindNormal,
		Text: visible,
		Content: []*FB2InlineElement{{
			Type:     InlineTypeLink,
			Attrs:    map[string]string{"href": href},
			Children: []*FB2InlineElement{{Type: InlineTypeText, Content: visible}},
		}},
	}
}

// Every scheme that smuggles script or an external fetch into an href is
// stripped together with the attribute; the visible text stays. Only a
// fragment pointing inside the same portion survives.
func TestRenderChunkHTML_HrefSchemes(t *testing.T) {
	hostile := []string{
		"javascript:alert(1)",
		"JaVaScRiPt:alert(1)",
		"   javascript:alert(1)",
		"\x01\x02javascript:alert(1)",
		"java\tscript:alert(1)",
		"java\nscript:alert(1)",
		"vbscript:msgbox(1)",
		"data:text/html,<script>alert(1)</script>",
		"file:///etc/passwd",
		"blob:https://evil.example/uuid",
		"//evil.example/x",
		`https:\\evil.example`,
		"http://user:pass@evil.example",
	}
	for _, href := range hostile {
		t.Run(fmt.Sprintf("stripped_%q", href), func(t *testing.T) {
			out := renderPreview(t, paraChunk(0, linkPara(href, "ВИДИМЫЙ ТЕКСТ")), nil, testPreviewPolicy(), testPreviewImagePolicy())
			if !strings.Contains(out, "ВИДИМЫЙ ТЕКСТ") {
				t.Errorf("the visible text went away with the attribute")
			}
			if strings.Contains(out, "<a ") || strings.Contains(out, "<a>") {
				t.Errorf("href %q survived as a link in %q", href, shorten(out))
			}
		})
	}

	t.Run("fragment to a local anchor survives", func(t *testing.T) {
		target := textPara("ЦЕЛЕВОЙ АБЗАЦ")
		target.ID = "note1"
		chunk := paraChunk(0, linkPara("#note1", "ССЫЛКА НА ЯКОРЬ"), target)
		out := renderPreview(t, chunk, nil, testPreviewPolicy(), testPreviewImagePolicy())
		if !strings.Contains(out, `href="#pv0-note1"`) {
			t.Errorf("the fragment link did not survive: %q", shorten(out))
		}
		if !strings.Contains(out, `id="pv0-note1"`) {
			t.Errorf("the target anchor is missing: %q", shorten(out))
		}
	})

	t.Run("fragment with surrounding whitespace survives", func(t *testing.T) {
		target := textPara("ЦЕЛЕВОЙ АБЗАЦ")
		target.ID = "note1"
		chunk := paraChunk(0, linkPara(" \t#note1\n", "ССЫЛКА С ПРОБЕЛАМИ"), target)
		out := renderPreview(t, chunk, nil, testPreviewPolicy(), testPreviewImagePolicy())
		if !strings.Contains(out, `href="#pv0-note1"`) {
			t.Errorf("a padded fragment link did not survive: %q", shorten(out))
		}
	})

	t.Run("fragment to another portion unwraps", func(t *testing.T) {
		// #elsewhere exists in the book but not in this chunk: cross-portion
		// navigation does not exist, so the link degrades to text.
		out := renderPreview(
			t,
			paraChunk(0, linkPara("#elsewhere", "ССЫЛКА НА ДРУГУЮ ПОРЦИЮ")),
			nil,
			testPreviewPolicy(),
			testPreviewImagePolicy(),
		)
		if !strings.Contains(out, "ССЫЛКА НА ДРУГУЮ ПОРЦИЮ") {
			t.Errorf("the visible text went away with the attribute")
		}
		if strings.Contains(out, "<a ") {
			t.Errorf("a cross-portion link survived: %q", shorten(out))
		}
	})
}

// imagePara builds a paragraph holding a single inline image reference.
func imagePara(id string) *FB2Paragraph {
	href := ""
	if id != "" {
		href = "#" + id
	}
	return &FB2Paragraph{
		Kind:    ParagraphKindNormal,
		Content: []*FB2InlineElement{{Type: InlineTypeImage, Attrs: map[string]string{"href": href}}},
	}
}

// Images are typed by their magic bytes and bounded by bytes and pixels. The
// declared content-type and the id are book-controlled text and decide
// nothing. SVG is an XML document with scripts and is never inlined.
func TestRenderChunkHTML_Images(t *testing.T) {
	pngData := uniformImage(t, "png", 8, 8)
	jpegData := uniformImage(t, "jpeg", 8, 8)
	svgData := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)

	cases := []struct {
		name        string
		id          string
		binary      FB2Binary
		wantSrc     string // expected address, empty means the placeholder
		wantNoImage bool
	}{
		{"declared png but bytes are jpeg", "img1", FB2Binary{Data: jpegData, MIME: "image/png"}, "/preview/img/1", false},
		{"svg id with raster bytes", "x.svg", FB2Binary{Data: pngData, MIME: "image/svg+xml"}, "/preview/img/1", false},
		{"png bytes declared as svg", "img3", FB2Binary{Data: pngData, MIME: "image/svg+xml"}, "/preview/img/1", false},
		{"svg bytes declared as png", "img4", FB2Binary{Data: svgData, MIME: "image/png"}, "", true},
		{"empty payload", "img5", FB2Binary{Data: nil, MIME: "image/png"}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			binaries := map[string]FB2Binary{tc.id: tc.binary}
			out := renderPreview(t, paraChunk(0, imagePara(tc.id)), binaries, testPreviewPolicy(), testPreviewImagePolicy())
			if tc.wantSrc != "" && !strings.Contains(out, `src="`+tc.wantSrc+`"`) {
				t.Errorf("expected src %q in %q", tc.wantSrc, shorten(out))
			}
			if tc.wantNoImage {
				if strings.Contains(out, "<img") {
					t.Errorf("a payload that must be refused got an address: %q", shorten(out))
				}
				if !strings.Contains(out, "[image]") {
					t.Errorf("the dropped image left no placeholder")
				}
			}
		})
	}

	t.Run("reference to a missing id", func(t *testing.T) {
		out := renderPreview(t, paraChunk(0, imagePara("nope")), nil, testPreviewPolicy(), testPreviewImagePolicy())
		if !strings.Contains(out, "[image]") {
			t.Errorf("a missing image left no placeholder: %q", shorten(out))
		}
		if strings.Contains(out, "<img") {
			t.Errorf("a missing image rendered an <img> tag")
		}
	})

	t.Run("empty href", func(t *testing.T) {
		out := renderPreview(t, paraChunk(0, imagePara("")), nil, testPreviewPolicy(), testPreviewImagePolicy())
		if !strings.Contains(out, "[image]") || strings.Contains(out, "<img") {
			t.Errorf("an image without a reference must be a placeholder, got %q", shorten(out))
		}
	})

	t.Run("one image referenced twice renders twice", func(t *testing.T) {
		binaries := map[string]FB2Binary{"dup": {Data: pngData, MIME: "image/png"}}
		out := renderPreview(t, paraChunk(0, imagePara("dup"), imagePara("dup")), binaries, testPreviewPolicy(), testPreviewImagePolicy())
		if n := countOccurrences(out, "<img"); n != 2 {
			t.Errorf("expected 2 <img> tags for two references, got %d", n)
		}
	})

	t.Run("over the byte limit", func(t *testing.T) {
		// The image budget is its own policy now: tightening the byte cap
		// does not touch the HTML ceiling. The picture is dropped from the
		// index, so the renderer emits the placeholder.
		imagePolicy := testPreviewImagePolicy()
		imagePolicy.MaxBytes = 10 // the 8x8 PNG is larger
		binaries := map[string]FB2Binary{"big": {Data: pngData, MIME: "image/png"}}
		out := renderPreview(t, paraChunk(0, imagePara("big")), binaries, testPreviewPolicy(), imagePolicy)
		if strings.Contains(out, "data:image") {
			t.Errorf("an image over the byte limit was inlined")
		}
		if !strings.Contains(out, "[image]") {
			t.Errorf("the dropped image left no placeholder")
		}
	})

	t.Run("over the pixel limit at a small compressed size", func(t *testing.T) {
		imagePolicy := testPreviewImagePolicy()
		imagePolicy.MaxPixels = 100
		binaries := map[string]FB2Binary{"wide": {Data: uniformImage(t, "png", 16, 16), MIME: "image/png"}}
		out := renderPreview(t, paraChunk(0, imagePara("wide")), binaries, testPreviewPolicy(), imagePolicy)
		if strings.Contains(out, "data:image") {
			t.Errorf("a 256-pixel image passed a 100-pixel limit")
		}
		if !strings.Contains(out, "[image]") {
			t.Errorf("the dropped image left no placeholder")
		}
	})

	t.Run("forged header declares millions of pixels", func(t *testing.T) {
		// A tiny real PNG whose IHDR claims 10000x10000: the decoder is never
		// reached, the declared dimensions alone decide.
		forged := forgePNGDimensions(t, uniformImage(t, "png", 1, 1), 10000, 10000)
		binaries := map[string]FB2Binary{"bomb": {Data: forged, MIME: "image/png"}}
		out := renderPreview(t, paraChunk(0, imagePara("bomb")), binaries, testPreviewPolicy(), testPreviewImagePolicy())
		if strings.Contains(out, "data:image") {
			t.Errorf("a forged 100-megapixel header was inlined — the pixel cap was not consulted")
		}
		if !strings.Contains(out, "[image]") {
			t.Errorf("the dropped image left no placeholder")
		}
	})
}

// Anchor ids are book-controlled text too: they are sanitized, prefixed per
// portion so they cannot collide with the app shell, and deduplicated.
func TestRenderChunkHTML_Anchors(t *testing.T) {
	quoted := textPara("АБЗАЦ С КАВЫЧКОЙ")
	quoted.ID = `we"id`
	spaced := textPara("АБЗАЦ С ПРОБЕЛАМИ")
	spaced.ID = "a b"
	unicodeID := textPara("АБЗАЦ С ЮНИКОДОМ")
	unicodeID.ID = "якорь"
	dup1 := textPara("ПЕРВЫЙ ДУБЛЬ")
	dup1.ID = "dup"
	dup2 := textPara("ВТОРОЙ ДУБЛЬ")
	dup2.ID = "dup"
	link := linkPara("#dup", "ССЫЛКА НА ДУБЛЬ")

	chunk := paraChunk(3, quoted, spaced, unicodeID, dup1, dup2, link)
	out := renderPreview(t, chunk, nil, testPreviewPolicy(), testPreviewImagePolicy())
	ids := auditPreviewHTML(t, out)

	for _, want := range []string{`pv3-we"id`, "pv3-ab", "pv3-якорь", "pv3-dup"} {
		if !ids[want] {
			t.Errorf("anchor %q missing from the fragment", want)
		}
	}
	if n := countOccurrences(out, `id="pv3-dup"`); n != 1 {
		t.Errorf("duplicate source ids produced %d anchors, expected exactly 1 (first wins)", n)
	}
	if !strings.Contains(out, `href="#pv3-dup"`) {
		t.Errorf("the link to a duplicated id did not resolve to its first anchor")
	}
	for id := range ids {
		if !strings.HasPrefix(id, "pv3-") {
			t.Errorf("anchor %q lacks the per-portion prefix — it can collide with the app shell", id)
		}
	}
}

// The main test of the phase: a hostile document is rendered, the result is
// re-parsed, and the whole output is checked against the invariant.
func TestRenderChunkHTML_OutputInvariant(t *testing.T) {
	nasty := &FB2Paragraph{
		Kind: ParagraphKindNormal,
		ID:   `p><script>alert(1)</script>`,
		Content: []*FB2InlineElement{
			{Type: InlineTypeText, Content: `<script>alert("xss")</script>`},
			{Type: InlineTypeStrong, Attrs: map[string]string{"onclick": "alert(1)", "style": "color:red"}, Children: []*FB2InlineElement{
				{Type: InlineTypeText, Content: "ЖИРНЫЙ"},
			}},
			{Type: "iframe", Attrs: map[string]string{"src": "https://evil.example"}, Children: []*FB2InlineElement{
				{Type: InlineTypeText, Content: "НЕИЗВЕСТНЫЙ ТИП"},
			}},
			{Type: InlineTypeLink, Attrs: map[string]string{
				"href": "javascript:alert(1)", "target": "_blank", "title": "hint",
			}, Children: []*FB2InlineElement{
				{Type: InlineTypeText, Content: "ВРЕДОНОСНАЯ ССЫЛКА"},
			}},
			{Type: InlineTypeLink, Attrs: map[string]string{"href": "#p2"}, Children: []*FB2InlineElement{
				{Type: InlineTypeText, Content: "РАБОЧАЯ ССЫЛКА"},
			}},
		},
	}
	second := textPara("ВТОРОЙ АБЗАЦ С ЯКОРЕМ")
	second.ID = "p2"
	table := &FB2Paragraph{
		Kind: ParagraphKindTable,
		Table: &FB2Table{Rows: [][]*FB2TableCell{
			{{Header: true, Content: []*FB2InlineElement{{Type: InlineTypeText, Content: "ШАПКА"}}}},
			{{Content: []*FB2InlineElement{{Type: InlineTypeText, Content: "ЯЧЕЙКА"}}}},
			{{Content: []*FB2InlineElement{{
				Type:     InlineTypeLink,
				Attrs:    map[string]string{"href": "javascript:alert(1)"},
				Children: []*FB2InlineElement{{Type: InlineTypeText, Content: "ЯЧЕЙКА-ССЫЛКА"}},
			}}}},
		}},
	}
	image := imagePara("ok")
	poem := &FB2Paragraph{
		Kind:    ParagraphKindPoemLine,
		Content: []*FB2InlineElement{{Type: InlineTypeText, Content: "СТРОКА СТИХА"}},
	}
	ref := noteRefPara("n1", "СНОСКА")

	chunk := paraChunk(0, nasty, second, table, image, poem, ref)
	chunk.notes = []*FB2BodySection{noteSection("n1", "ТЕКСТ СНОСКИ ИНВАРИАНТА")}
	binaries := map[string]FB2Binary{"ok": {Data: uniformImage(t, "png", 4, 4), MIME: "image/png"}}

	out := renderPreview(t, chunk, binaries, testPreviewPolicy(), testPreviewImagePolicy())
	auditPreviewHTML(t, out)

	// The document must still be recognizable: safety is not emptiness.
	markers := []string{
		"ЖИРНЫЙ", "НЕИЗВЕСТНЫЙ ТИП", "ВРЕДОНОСНАЯ ССЫЛКА", "РАБОЧАЯ ССЫЛКА",
		"ВТОРОЙ АБЗАЦ С ЯКОРЕМ", "ШАПКА", "ЯЧЕЙКА", "ЯЧЕЙКА-ССЫЛКА",
		"СТРОКА СТИХА", "ТЕКСТ СНОСКИ ИНВАРИАНТА",
	}
	for _, marker := range markers {
		if !strings.Contains(out, marker) {
			t.Errorf("content marker %q did not survive rendering", marker)
		}
	}
	if !strings.Contains(out, `<img src="/preview/img/1" loading="lazy"`) {
		t.Errorf("the valid image did not render")
	}
}

// Property test: arbitrary inline trees — deep nesting, unknown node types,
// hostile attributes and text — always render into the same output invariant.
func TestRenderChunkHTML_PropertyArbitraryInlineTrees(t *testing.T) {
	rng := rand.New(rand.NewSource(20260811))
	texts := []string{
		"просто текст",
		`<script>alert(1)</script>`,
		`"><img src=x onerror=alert(1)>`,
		"&amp; &lt; &gt;",
		"юникод яzık смесь",
		"'\"`<>",
		strings.Repeat("длинная строка ", 30),
	}
	hrefs := []string{
		"#p1", "#note", "javascript:alert(1)", "data:text/html,x", "//evil.example",
		"", "   ", "#несуществующий", "java\tscript:x",
	}
	types := []string{
		InlineTypeText, InlineTypeStrong, InlineTypeEmphasis, InlineTypeCode,
		InlineTypeSup, InlineTypeSub, InlineTypeLink, InlineTypeImage, InlineTypeBreak,
		"unknown", "script", "iframe", "object", "style", "form",
	}

	// A node budget bounds the tree size: depth alone allows a mean-1.5
	// branching factor to explode combinatorially.
	nodeBudget := 0
	var build func(depth int) []*FB2InlineElement
	build = func(depth int) []*FB2InlineElement {
		if depth > 45 {
			// Deep enough to pass the renderer's own nesting guard; further
			// growth only blows up the generator, not the renderer.
			return []*FB2InlineElement{{Type: InlineTypeText, Content: texts[rng.Intn(len(texts))]}}
		}
		count := rng.Intn(4)
		out := make([]*FB2InlineElement, 0, count)
		for i := 0; i < count; i++ {
			if nodeBudget <= 0 {
				break
			}
			nodeBudget--
			el := &FB2InlineElement{Type: types[rng.Intn(len(types))]}
			switch el.Type {
			case InlineTypeText:
				el.Content = texts[rng.Intn(len(texts))]
			case InlineTypeLink:
				el.Attrs = map[string]string{"href": hrefs[rng.Intn(len(hrefs))]}
				el.Children = build(depth + 1)
			case InlineTypeImage:
				el.Attrs = map[string]string{"href": hrefs[rng.Intn(len(hrefs))]}
			case InlineTypeBreak:
			default:
				// Unknown types carry arbitrary attributes and children —
				// the tree a parser fix or a hostile file could produce.
				if rng.Intn(2) == 0 {
					el.Attrs = map[string]string{
						"onclick": "alert(1)",
						"href":    hrefs[rng.Intn(len(hrefs))],
						"src":     "https://evil.example/x.js",
					}
				}
				el.Children = build(depth + 1)
			}
			out = append(out, el)
		}
		return out
	}

	binaries := map[string]FB2Binary{"note": {Data: uniformImage(t, "png", 2, 2), MIME: "image/png"}}
	// The invariant is size-independent: give the render room for the biggest
	// budgeted tree so the HTML ceiling check never interferes. The image
	// policy stays at defaults — the binaries here are small real PNGs.
	propertyPolicy := PreviewPolicy{MaxChunkBytes: 8 << 20}
	imagePolicy := testPreviewImagePolicy()
	renderedSomething := false
	for iteration := 0; iteration < 300; iteration++ {
		nodeBudget = 200
		para := &FB2Paragraph{Kind: ParagraphKindNormal, Content: build(0)}
		if rng.Intn(3) == 0 {
			para.ID = "p1"
		}
		out, err := RenderChunkHTML(paraChunk(0, para), BuildPreviewImages(binaries, "/preview/img", imagePolicy), propertyPolicy)
		if err != nil {
			t.Fatalf("iteration %d: RenderChunkHTML: %v", iteration, err)
		}
		if out != "" {
			renderedSomething = true
		}
		func() {
			// Report failures against the iteration, not the helper line.
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("iteration %d: audit panicked: %v", iteration, r)
				}
			}()
			auditPreviewHTML(t, out)
		}()
		if t.Failed() {
			t.Fatalf("iteration %d broke the output invariant; tree seed is deterministic, re-run reproduces", iteration)
		}
	}
	if !renderedSomething {
		t.Errorf("300 random trees all rendered empty — the renderer produces nothing, which trivially satisfies the invariant")
	}
}

// Text regressions: every content shape renders with the right wrapper, and
// escaping happens exactly where the tests demand it.
func TestRenderChunkHTML_TextRegressions(t *testing.T) {
	t.Run("section title is an escaped heading", func(t *testing.T) {
		section := &FB2BodySection{Title: `Заголовок <с "кавычками">`}
		chunk := &PreviewChunk{Index: 0, blocks: []chunkBlock{{header: section, depth: 1}}}
		out := renderPreview(t, chunk, nil, testPreviewPolicy(), testPreviewImagePolicy())
		if !strings.Contains(out, `<h1>Заголовок &lt;с &#34;кавычками&#34;&gt;</h1>`) {
			t.Errorf("section title misrendered: %q", shorten(out))
		}
	})

	t.Run("table cells and headers", func(t *testing.T) {
		table := &FB2Paragraph{
			Kind: ParagraphKindTable,
			Table: &FB2Table{Rows: [][]*FB2TableCell{
				{{Header: true, Content: []*FB2InlineElement{{Type: InlineTypeText, Content: "ГОЛОВА <1>"}}}},
				{{Content: []*FB2InlineElement{{Type: InlineTypeText, Content: "ТЕЛО"}}}},
			}},
		}
		out := renderPreview(t, paraChunk(0, table), nil, testPreviewPolicy(), testPreviewImagePolicy())
		if !strings.Contains(out, "<th>ГОЛОВА &lt;1&gt;</th>") {
			t.Errorf("header cell misrendered: %q", shorten(out))
		}
		if !strings.Contains(out, "<td>ТЕЛО</td>") {
			t.Errorf("body cell misrendered: %q", shorten(out))
		}
	})

	t.Run("note title and body", func(t *testing.T) {
		note := noteSection("n1", "ТЕЛО СНОСКИ")
		note.Title = "ЗАМЕТКА НА ПОЛЯХ"
		chunk := paraChunk(0, noteRefPara("n1", "СМ"))
		chunk.notes = []*FB2BodySection{note}
		out := renderPreview(t, chunk, nil, testPreviewPolicy(), testPreviewImagePolicy())
		if !strings.Contains(out, "ЗАМЕТКА НА ПОЛЯХ") {
			t.Errorf("the note title did not render")
		}
		if !strings.Contains(out, "ТЕЛО СНОСКИ") {
			t.Errorf("the note body did not render")
		}
	})

	t.Run("poem line keeps its wrapper", func(t *testing.T) {
		line := &FB2Paragraph{Kind: ParagraphKindPoemLine, Content: []*FB2InlineElement{{Type: InlineTypeText, Content: "СТРОКА"}}}
		out := renderPreview(t, paraChunk(0, line), nil, testPreviewPolicy(), testPreviewImagePolicy())
		if !strings.Contains(out, `<p class="poem-line">СТРОКА</p>`) {
			t.Errorf("poem line misrendered: %q", shorten(out))
		}
	})

	t.Run("poem break and empty line render separators", func(t *testing.T) {
		// Both kinds are contentless: an emptiness check before the kind
		// switch would render nothing at all, and the stanza rhythm is lost.
		poemBreak := &FB2Paragraph{Kind: ParagraphKindPoemBreak}
		emptyLine := &FB2Paragraph{Kind: ParagraphKindEmptyLine}
		out := renderPreview(t, paraChunk(0, poemBreak, emptyLine), nil, testPreviewPolicy(), testPreviewImagePolicy())
		if !strings.Contains(out, `<div class="stanza"></div>`) {
			t.Errorf("poem break rendered no stanza separator: %q", shorten(out))
		}
		if !strings.Contains(out, `<div class="emptyline"></div>`) {
			t.Errorf("empty line rendered no separator: %q", shorten(out))
		}
	})

	t.Run("deeply nested formatting renders inside out", func(t *testing.T) {
		deep := &FB2Paragraph{Kind: ParagraphKindNormal, Content: []*FB2InlineElement{
			{Type: InlineTypeStrong, Children: []*FB2InlineElement{
				{Type: InlineTypeEmphasis, Children: []*FB2InlineElement{
					{Type: InlineTypeCode, Children: []*FB2InlineElement{
						{Type: InlineTypeSup, Children: []*FB2InlineElement{
							{Type: InlineTypeText, Content: "ГЛУБИНА"},
						}},
					}},
				}},
			}},
		}}
		out := renderPreview(t, paraChunk(0, deep), nil, testPreviewPolicy(), testPreviewImagePolicy())
		if !strings.Contains(out, "<strong><em><code><sup>ГЛУБИНА</sup></code></em></strong>") {
			t.Errorf("nested formatting misrendered: %q", shorten(out))
		}
	})

	t.Run("source attributes other than href are dropped", func(t *testing.T) {
		para := &FB2Paragraph{Kind: ParagraphKindNormal, Content: []*FB2InlineElement{
			{Type: InlineTypeEmphasis, Attrs: map[string]string{"title": "подсказка", "class": "bookish"}, Children: []*FB2InlineElement{
				{Type: InlineTypeText, Content: "ТЕКСТ"},
			}},
		}}
		out := renderPreview(t, paraChunk(0, para), nil, testPreviewPolicy(), testPreviewImagePolicy())
		if !strings.Contains(out, "<em>ТЕКСТ</em>") {
			t.Errorf("the emphasis itself did not render: %q", shorten(out))
		}
		if strings.Contains(out, "title=") || strings.Contains(out, "подсказка") {
			t.Errorf("a source title attribute leaked into the output: %q", shorten(out))
		}
	})

	t.Run("text escaping is exact", func(t *testing.T) {
		para := textPara(`a<b>&"'`)
		out := renderPreview(t, paraChunk(0, para), nil, testPreviewPolicy(), testPreviewImagePolicy())
		if !strings.Contains(out, `a&lt;b&gt;&amp;&#34;&#39;`) {
			t.Errorf("text escaping mismatch: %q", shorten(out))
		}
	})

	t.Run("nesting past the depth guard is dropped, not crashed", func(t *testing.T) {
		// A hand-built tree deeper than the parser would ever produce: the
		// renderer's own guard cuts it off instead of recursing without a
		// bound. The content below the cut is dropped — that is the defined
		// outcome for a pathological tree.
		const depth = 40
		var bottom *FB2InlineElement
		bottom = &FB2InlineElement{Type: InlineTypeText, Content: "САМОЕ ДНО"}
		for i := 0; i < depth; i++ {
			bottom = &FB2InlineElement{Type: InlineTypeStrong, Children: []*FB2InlineElement{bottom}}
		}
		para := &FB2Paragraph{Kind: ParagraphKindNormal, Content: []*FB2InlineElement{bottom}}
		out := renderPreview(t, paraChunk(0, para), nil, testPreviewPolicy(), testPreviewImagePolicy())
		if strings.Contains(out, "САМОЕ ДНО") {
			t.Errorf("content below the depth guard rendered — the guard did not cut")
		}
		if n := countOccurrences(out, "<strong>"); n != 33 {
			t.Errorf("expected exactly 33 <strong> levels (0..32), got %d", n)
		}
	})

	t.Run("two references to one note share one note block", func(t *testing.T) {
		chunk := paraChunk(0,
			textPara("АБЗАЦ МЕЖДУ ССЫЛКАМИ"),
			noteRefPara("n1", "ССЫЛКА РАЗ"),
			noteRefPara("n1", "ССЫЛКА ДВА"),
		)
		chunk.notes = []*FB2BodySection{noteSection("n1", "ТЕКСТ ЕДИНОЙ СНОСКИ")}
		out := renderPreview(t, chunk, nil, testPreviewPolicy(), testPreviewImagePolicy())
		auditPreviewHTML(t, out)
		if n := countOccurrences(out, `class="preview-note"`); n != 1 {
			t.Errorf("one note rendered %d times in one chunk — the id would duplicate", n)
		}
		if n := countOccurrences(out, `href="#pv0-note-n1"`); n != 2 {
			t.Errorf("expected both references to point at the shared note anchor, got %d", n)
		}
	})
}

// An ordinary book must render recognizably — without this pin the whole
// suite could go green on a renderer that "secured" everything into emptiness.
func TestRenderChunkHTML_OrdinaryBookRecognizable(t *testing.T) {
	pngData := uniformImage(t, "png", 8, 8)
	chapter1 := &FB2BodySection{Title: "Глава первая"}
	chapter1.Content = []*FB2ContentItem{
		{Paragraph: textPara("Начало обычной книги с простым абзацем.")},
		{Paragraph: &FB2Paragraph{Kind: ParagraphKindNormal, Content: []*FB2InlineElement{
			{Type: InlineTypeText, Content: "Абзац с "},
			{Type: InlineTypeEmphasis, Children: []*FB2InlineElement{{Type: InlineTypeText, Content: "курсивом"}}},
			{Type: InlineTypeText, Content: " и "},
			{Type: InlineTypeStrong, Children: []*FB2InlineElement{{Type: InlineTypeText, Content: "жирным"}}},
			{Type: InlineTypeText, Content: "."},
		}}},
		{Paragraph: &FB2Paragraph{Kind: ParagraphKindTable, Table: &FB2Table{Rows: [][]*FB2TableCell{
			{{Header: true, Content: []*FB2InlineElement{{Type: InlineTypeText, Content: "Колонка"}}}},
			{{Content: []*FB2InlineElement{{Type: InlineTypeText, Content: "Значение"}}}},
		}}}},
		{Paragraph: poemLinePara("Первая строка стиха")},
		{Paragraph: poemLinePara("Вторая строка стиха")},
		{Paragraph: imagePara("ill1")},
	}
	chapter2 := &FB2BodySection{Title: "Глава вторая"}
	chapter2.Content = []*FB2ContentItem{
		{Paragraph: textPara("Текст второй главы.")},
		{Paragraph: noteRefPara("note-9", "ссылка на сноску")},
	}
	doc := &FB2Document{Body: &FB2BodySection{Content: []*FB2ContentItem{
		{Section: chapter1},
		{Section: chapter2},
	}}}
	doc.Notes = []*FB2BodySection{noteSection("note-9", "Текст сноски обычной книги")}
	binaries := map[string]FB2Binary{"ill1": {Data: pngData, MIME: "image/png"}}
	doc.Binary = binaries

	chunks, err := ChunkPreview(doc, previewImagesFor(doc, testPreviewImagePolicy()), testPreviewPolicy())
	if err != nil {
		t.Fatalf("ChunkPreview: %v", err)
	}
	pieces := renderAllChunks(t, chunks, binaries, testPreviewPolicy(), testPreviewImagePolicy())
	joined := strings.Join(pieces, "")
	auditPreviewHTML(t, joined)

	markers := []string{
		"Глава первая", "Начало обычной книги с простым абзацем.",
		"курсивом", "жирным", "Колонка", "Значение",
		"Первая строка стиха", "Вторая строка стиха",
		"Глава вторая", "Текст второй главы.", "ссылка на сноску",
		"Текст сноски обычной книги",
	}
	for _, marker := range markers {
		if !strings.Contains(joined, marker) {
			t.Errorf("an ordinary book lost %q in rendering", marker)
		}
	}
	if !strings.Contains(joined, "<em>курсивом</em>") || !strings.Contains(joined, "<strong>жирным</strong>") {
		t.Errorf("inline formatting did not survive")
	}
	if !strings.Contains(joined, `<img src="/preview/img/1" loading="lazy"`) {
		t.Errorf("the illustration did not render")
	}
	if !strings.Contains(joined, `class="poem-line"`) {
		t.Errorf("poem lines lost their wrapper")
	}
	if err := markerOrder(joined, []string{"Глава первая", "Глава вторая"}); err != nil {
		t.Errorf("chapter order broken: %v", err)
	}
}

// TestRenderChunkHTML_AnchorCollisionAfterNormalising pins the pairing the
// separate whitespace and duplicate cases could not: two ids that differ only
// by characters the sanitiser strips. Keyed on the raw id they were distinct
// entries; emitted through the sanitiser they were the same anchor, so the
// document carried two identical ids under an invariant promising none.
func TestRenderChunkHTML_AnchorCollisionAfterNormalising(t *testing.T) {
	// Both orders matter, and for different reasons. Clean id first, and the
	// question is whether the second one duplicates it. Dirty id first, and
	// the question is whether the anchor survives at all: keying the map on
	// the raw string files it under "a b", so a lookup by the normalised key
	// finds nothing and the block loses its anchor silently.
	for _, order := range []struct {
		name          string
		first, second string
	}{
		{"clean id first", "ab", "a b"},
		{"dirty id first", "a b", "ab"},
	} {
		t.Run(order.name, func(t *testing.T) {
			assertOneAnchorSurvives(t, order.first, order.second)
		})
	}

	// The case that pins the normalisation itself rather than the emitted-once
	// bookkeeping: a dirty id with no clean twin. With the map keyed on the raw
	// string, or the lookup done on it, nothing matches the normalised key and
	// the block loses its anchor entirely — silently, and the link with it.
	t.Run("dirty id alone still gets its anchor", func(t *testing.T) {
		only := textPara("ЕДИНСТВЕННЫЙ")
		only.ID = "a b"
		link := &FB2Paragraph{
			Kind: ParagraphKindNormal,
			Content: []*FB2InlineElement{{
				Type:     InlineTypeLink,
				Attrs:    map[string]string{"href": "#a b"},
				Children: []*FB2InlineElement{{Type: InlineTypeText, Content: "ССЫЛКА"}},
			}},
		}
		out := renderPreview(t, paraChunk(0, only, link), nil, testPreviewPolicy(), testPreviewImagePolicy())
		if got := countOccurrences(out, `id="pv0-ab"`); got != 1 {
			t.Fatalf("expected the anchor pv0-ab, got %d: %s", got, out)
		}
		if got := countOccurrences(out, `href="#pv0-ab"`); got != 1 {
			t.Fatalf("expected the link to resolve, got %d: %s", got, out)
		}
	})
}

func assertOneAnchorSurvives(t *testing.T, firstID, secondID string) {
	t.Helper()
	first := textPara("ПЕРВЫЙ")
	first.ID = firstID
	second := textPara("ВТОРОЙ")
	second.ID = secondID
	// A link written either way must land on the one surviving anchor.
	link := &FB2Paragraph{
		Kind: ParagraphKindNormal,
		Content: []*FB2InlineElement{{
			Type:     InlineTypeLink,
			Attrs:    map[string]string{"href": "#a b"},
			Children: []*FB2InlineElement{{Type: InlineTypeText, Content: "ССЫЛКА"}},
		}},
	}

	out := renderPreview(t, paraChunk(0, first, second, link), nil, testPreviewPolicy(), testPreviewImagePolicy())

	ids := regexp.MustCompile(`id="([^"]+)"`).FindAllStringSubmatch(out, -1)
	seen := map[string]int{}
	for _, m := range ids {
		seen[m[1]]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Fatalf("id %q appears %d times: %s", id, n, out)
		}
	}
	if got := countOccurrences(out, `id="pv0-ab"`); got != 1 {
		t.Fatalf("expected exactly one anchor pv0-ab, got %d: %s", got, out)
	}
	if got := countOccurrences(out, `href="#pv0-ab"`); got != 1 {
		t.Fatalf("the link resolved %d times to the surviving anchor, want 1: %s", got, out)
	}
}
