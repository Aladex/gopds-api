package converter

// preview_chunker_test.go pins the portioning rules of the preview: cutting
// only between blocks, the hard byte ceiling on rendered HTML, footnotes
// traveling with their references, and defined outcomes for every orphan.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// docFromParas wraps paragraphs into a sectionless document body.
func docFromParas(paras ...*FB2Paragraph) *FB2Document {
	doc := &FB2Document{Body: &FB2BodySection{}}
	for _, p := range paras {
		doc.Body.Content = append(doc.Body.Content, &FB2ContentItem{Paragraph: p})
	}
	return doc
}

// renderAllChunks renders every chunk and returns the HTML pieces. Both
// policies are passed through because rendering a chunk also resolves image
// references: chunk policy bounds the HTML, image policy decides which
// binaries the index admits.
func renderAllChunks(
	t *testing.T,
	ctx context.Context,
	chunks []*PreviewChunk,
	binaries map[string]FB2Binary,
	chunkPolicy PreviewPolicy,
	imagePolicy PreviewImagePolicy,
) []string {
	t.Helper()
	out := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		out = append(out, renderPreview(t, ctx, chunk, binaries, chunkPolicy, imagePolicy))
	}
	return out
}

// A section larger than the ceiling is cut between block nodes, and no
// portion exceeds the ceiling measured in rendered HTML bytes.
func TestChunkPreview_SplitsOversizedSection(t *testing.T) {
	chunkPolicy := PreviewPolicy{MaxChunkBytes: 512}
	imagePolicy := testPreviewImagePolicy()
	const paraCount = 40
	markers := make([]string, 0, paraCount)
	section := &FB2BodySection{Title: "Очень длинная глава"}
	for i := 0; i < paraCount; i++ {
		marker := fmt.Sprintf("МАРКЕР АБЗАЦА %02d", i)
		// ~100 bytes of text per paragraph so a few fit under the ceiling.
		text := marker + " " + strings.Repeat("текст обычного абзаца ", 3)
		markers = append(markers, marker)
		section.Content = append(section.Content, &FB2ContentItem{Paragraph: textPara(text)})
	}
	doc := &FB2Document{Body: &FB2BodySection{
		Content: []*FB2ContentItem{{Section: section}},
	}}

	chunks, err := ChunkPreview(context.Background(), doc, previewImagesFor(context.Background(), doc, imagePolicy), chunkPolicy)
	if err != nil {
		t.Fatalf("ChunkPreview: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected the oversized section to be cut into several chunks, got %d", len(chunks))
	}

	pieces := renderAllChunks(t, context.Background(), chunks, nil, chunkPolicy, imagePolicy)
	for i, piece := range pieces {
		if piece == "" {
			t.Errorf("chunk %d renders empty", i)
		}
		if len(piece) > chunkPolicy.MaxChunkBytes {
			t.Errorf("chunk %d is %d bytes of HTML, ceiling is %d", i, len(piece), chunkPolicy.MaxChunkBytes)
		}
	}

	joined := strings.Join(pieces, "")
	for _, marker := range markers {
		if n := countOccurrences(joined, marker); n != 1 {
			t.Errorf("marker %q appears %d times, expected exactly 1 — loss or duplication while cutting", marker, n)
		}
	}
	if err := markerOrder(joined, markers); err != nil {
		t.Errorf("reading order broken: %v", err)
	}
	if !strings.Contains(pieces[0], "Очень длинная глава") {
		t.Errorf("the section heading did not survive the cut")
	}
}

// One indivisible block larger than the ceiling is a typed refusal, not a
// silently oversized portion.
func TestChunkPreview_IndivisibleBlockTooLarge(t *testing.T) {
	chunkPolicy := PreviewPolicy{MaxChunkBytes: 512}
	imagePolicy := testPreviewImagePolicy()
	doc := docFromParas(textPara(strings.Repeat("очень длинный абзац ", 200)))

	chunks, err := ChunkPreview(context.Background(), doc, previewImagesFor(context.Background(), doc, imagePolicy), chunkPolicy)
	if err == nil {
		t.Fatalf("expected a typed error for a block larger than the ceiling, got %d chunks", len(chunks))
	}
	if !errors.Is(err, ErrPreviewBlockTooLarge) {
		t.Fatalf("expected ErrPreviewBlockTooLarge, got %v", err)
	}
	if chunks != nil {
		t.Errorf("chunks must be nil on refusal, got %d", len(chunks))
	}
}

// An image refused by image policy leaves the placeholder — an explicit,
// visible outcome — and the text around it is unaffected. The picture is a
// resource of its own now, so it never enters the portion's byte budget: what
// refuses it is the per-image cap, not the portion ceiling.
func TestChunkPreview_OversizedImageIsDropped(t *testing.T) {
	jpegData := uniformImage(t, "jpeg", 64, 64)
	chunkPolicy := PreviewPolicy{MaxChunkBytes: 4096}
	imagePolicy := PreviewImagePolicy{MaxBytes: len(jpegData) - 1, MaxPixels: 32 << 20, MaxSide: 4096}
	imagePara := &FB2Paragraph{
		Kind: ParagraphKindImage,
		Content: []*FB2InlineElement{{
			Type:  InlineTypeImage,
			Attrs: map[string]string{"href": "#big1"},
		}},
	}
	doc := docFromParas(textPara("ТЕКСТ ДО КАРТИНКИ"), imagePara, textPara("ТЕКСТ ПОСЛЕ КАРТИНКИ"))
	binaries := map[string]FB2Binary{"big1": {Data: jpegData, MIME: "image/jpeg"}}

	chunks, err := ChunkPreview(context.Background(), doc, previewImagesFor(context.Background(), doc, imagePolicy), chunkPolicy)
	if err != nil {
		t.Fatalf("an oversized image must not refuse the book: %v", err)
	}
	pieces := renderAllChunks(t, context.Background(), chunks, binaries, chunkPolicy, imagePolicy)
	joined := strings.Join(pieces, "")
	if strings.Contains(joined, "<img") {
		t.Errorf("an image over the per-image cap was still addressed: %q", shorten(joined))
	}
	if !strings.Contains(joined, "[image]") {
		t.Errorf("the dropped image left no placeholder — a silent loss")
	}
	for _, marker := range []string{"ТЕКСТ ДО КАРТИНКИ", "ТЕКСТ ПОСЛЕ КАРТИНКИ"} {
		if !strings.Contains(joined, marker) {
			t.Errorf("text around the image is missing: %q", marker)
		}
	}
	for i, piece := range pieces {
		if len(piece) > chunkPolicy.MaxChunkBytes {
			t.Errorf("chunk %d is %d bytes of HTML, ceiling is %d", i, len(piece), chunkPolicy.MaxChunkBytes)
		}
	}
}

// A footnote lands in the same portion as the text referencing it, even when
// packing would otherwise put the reference at the end of the previous chunk.
func TestChunkPreview_NoteStaysWithReference(t *testing.T) {
	chunkPolicy := PreviewPolicy{MaxChunkBytes: 512}
	imagePolicy := testPreviewImagePolicy()
	filler := strings.Repeat("абзац-заполнитель почти на весь чанк ", 5) // ~180 bytes rendered
	doc := docFromParas(
		textPara(filler),
		textPara(filler),
		noteRefPara("n1", "ССЫЛКА-НА-СНОСКУ"),
	)
	doc.Notes = []*FB2BodySection{noteSection("n1", "ТЕКСТ ПЕРВОЙ СНОСКИ "+strings.Repeat("подробно ", 8))}

	chunks, err := ChunkPreview(context.Background(), doc, previewImagesFor(context.Background(), doc, imagePolicy), chunkPolicy)
	if err != nil {
		t.Fatalf("ChunkPreview: %v", err)
	}
	pieces := renderAllChunks(t, context.Background(), chunks, nil, chunkPolicy, imagePolicy)
	for i, piece := range pieces {
		if len(piece) > chunkPolicy.MaxChunkBytes {
			t.Errorf("chunk %d is %d bytes of HTML, ceiling is %d — note bytes must count toward packing", i, len(piece), chunkPolicy.MaxChunkBytes)
		}
		if strings.Contains(piece, "ССЫЛКА-НА-СНОСКУ") && !strings.Contains(piece, "ТЕКСТ ПЕРВОЙ СНОСКИ") {
			t.Errorf("chunk %d holds the reference but not the footnote — the reader cannot expand it", i)
		}
		if strings.Contains(piece, "ТЕКСТ ПЕРВОЙ СНОСКИ") && !strings.Contains(piece, "ССЫЛКА-НА-СНОСКУ") {
			t.Errorf("chunk %d holds the footnote but not its reference", i)
		}
	}
	if !strings.Contains(strings.Join(pieces, ""), "ТЕКСТ ПЕРВОЙ СНОСКИ") {
		t.Errorf("the referenced footnote never rendered")
	}
}

// A footnote referenced from two portions is inlined into both, with anchors
// unique per portion so two chunks in one DOM never collide.
func TestChunkPreview_NoteInTwoChunksUniqueIDs(t *testing.T) {
	chunkPolicy := PreviewPolicy{MaxChunkBytes: 512}
	imagePolicy := testPreviewImagePolicy()
	filler := strings.Repeat("разделитель между ссылками на одну сноску ", 6)
	doc := docFromParas(
		noteRefPara("n1", "ПЕРВАЯ-ССЫЛКА"),
		textPara(filler),
		textPara(filler),
		noteRefPara("n1", "ВТОРАЯ-ССЫЛКА"),
	)
	doc.Notes = []*FB2BodySection{noteSection("n1", "ОБЩИЙ ТЕКСТ СНОСКИ "+strings.Repeat("ещё подробнее ", 6))}

	chunks, err := ChunkPreview(context.Background(), doc, previewImagesFor(context.Background(), doc, imagePolicy), chunkPolicy)
	if err != nil {
		t.Fatalf("ChunkPreview: %v", err)
	}
	pieces := renderAllChunks(t, context.Background(), chunks, nil, chunkPolicy, imagePolicy)

	var withFirst, withSecond []string
	for _, piece := range pieces {
		if strings.Contains(piece, "ПЕРВАЯ-ССЫЛКА") {
			withFirst = append(withFirst, piece)
		}
		if strings.Contains(piece, "ВТОРАЯ-ССЫЛКА") {
			withSecond = append(withSecond, piece)
		}
	}
	if len(withFirst) != 1 || len(withSecond) != 1 {
		t.Fatalf("references scattered: first in %d chunks, second in %d", len(withFirst), len(withSecond))
	}
	if !strings.Contains(withFirst[0], "ОБЩИЙ ТЕКСТ СНОСКИ") {
		t.Errorf("the first reference's chunk lacks the footnote text")
	}
	if !strings.Contains(withSecond[0], "ОБЩИЙ ТЕКСТ СНОСКИ") {
		t.Errorf("the second reference's chunk lacks the footnote text")
	}

	// The anchors must differ between portions, or two chunks in one DOM
	// would duplicate the id.
	idsFirst := auditPreviewHTML(t, withFirst[0])
	idsSecond := auditPreviewHTML(t, withSecond[0])
	for id := range idsFirst {
		if idsSecond[id] {
			t.Errorf("id %q appears in both portions — collision when both are in the DOM", id)
		}
	}
}

// A footnote nothing references is never rendered; a reference to a missing
// footnote degrades to plain text.
func TestChunkPreview_OrphanNotesAndReferences(t *testing.T) {
	chunkPolicy := testPreviewPolicy()
	imagePolicy := testPreviewImagePolicy()

	t.Run("unreferenced note is dropped", func(t *testing.T) {
		doc := docFromParas(textPara("ОБЫЧНЫЙ АБЗАЦ БЕЗ СНОСОК"))
		doc.Notes = []*FB2BodySection{noteSection("n1", "СНОСКА БЕЗ ССЫЛОК НА НЕЁ")}
		chunks, err := ChunkPreview(context.Background(), doc, previewImagesFor(context.Background(), doc, imagePolicy), chunkPolicy)
		if err != nil {
			t.Fatalf("ChunkPreview: %v", err)
		}
		joined := strings.Join(renderAllChunks(t, context.Background(), chunks, nil, chunkPolicy, imagePolicy), "")
		if strings.Contains(joined, "СНОСКА БЕЗ ССЫЛОК НА НЕЁ") {
			t.Errorf("an unreferenced footnote rendered — it is unreachable dead weight")
		}
		if !strings.Contains(joined, "ОБЫЧНЫЙ АБЗАЦ БЕЗ СНОСОК") {
			t.Errorf("the paragraph itself went missing")
		}
	})

	t.Run("reference to a missing note unwraps", func(t *testing.T) {
		doc := docFromParas(noteRefPara("ghost", "ССЫЛКА В НИКУДА"))
		chunks, err := ChunkPreview(context.Background(), doc, previewImagesFor(context.Background(), doc, imagePolicy), chunkPolicy)
		if err != nil {
			t.Fatalf("ChunkPreview: %v", err)
		}
		joined := strings.Join(renderAllChunks(t, context.Background(), chunks, nil, chunkPolicy, imagePolicy), "")
		if !strings.Contains(joined, "ССЫЛКА В НИКУДА") {
			t.Errorf("the link text vanished with its target")
		}
		if strings.Contains(joined, "<a ") || strings.Contains(joined, "<a>") {
			t.Errorf("a link to a missing footnote survived as a link — it leads nowhere")
		}
	})
}

// The ceiling is counted in bytes of the rendered HTML, not of the model:
// markup costs bytes too.
func TestChunkPreview_CeilingCountsRenderedHTMLBytes(t *testing.T) {
	// A 10-byte text renders as "<p>0123456789</p>\n" — 18 bytes. A
	// model-byte counter would pack four paragraphs under a 40-byte ceiling;
	// the HTML-byte truth fits only two.
	chunkPolicy := PreviewPolicy{MaxChunkBytes: 40}
	imagePolicy := testPreviewImagePolicy()
	doc := docFromParas(
		textPara("0123456789"),
		textPara("0123456789"),
		textPara("0123456789"),
		textPara("0123456789"),
	)
	chunks, err := ChunkPreview(context.Background(), doc, previewImagesFor(context.Background(), doc, imagePolicy), chunkPolicy)
	if err != nil {
		t.Fatalf("ChunkPreview: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks counting rendered HTML bytes, got %d", len(chunks))
	}
	pieces := renderAllChunks(t, context.Background(), chunks, nil, chunkPolicy, imagePolicy)
	for i, piece := range pieces {
		if len(piece) > chunkPolicy.MaxChunkBytes {
			t.Errorf("chunk %d is %d bytes of HTML, ceiling is %d", i, len(piece), chunkPolicy.MaxChunkBytes)
		}
	}
}

// A book without content still portions — into a single empty chunk, not into
// zero chunks (which downstream would read as a missing book).
func TestChunkPreview_EmptyBookIsOneEmptyChunk(t *testing.T) {
	chunks, err := ChunkPreview(context.Background(), &FB2Document{Body: &FB2BodySection{}}, PreviewImages{}, testPreviewPolicy())
	if err != nil {
		t.Fatalf("ChunkPreview: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected exactly 1 chunk for an empty book, got %d", len(chunks))
	}
	pieces := renderAllChunks(t, context.Background(), chunks, nil, testPreviewPolicy(), testPreviewImagePolicy())
	if pieces[0] != "" {
		t.Errorf("an empty book must render empty, got %q", pieces[0])
	}
}

// A reference inside a table cell pulls its footnote exactly like a reference
// in flowing text: cells are text too.
func TestChunkPreview_NoteReferencedFromTableCell(t *testing.T) {
	chunkPolicy := testPreviewPolicy()
	imagePolicy := testPreviewImagePolicy()
	table := &FB2Paragraph{
		Kind: ParagraphKindTable,
		Table: &FB2Table{Rows: [][]*FB2TableCell{
			{{Content: []*FB2InlineElement{{
				Type:     InlineTypeLink,
				Attrs:    map[string]string{"href": "#n1", "type": "note"},
				Children: []*FB2InlineElement{{Type: InlineTypeText, Content: "ССЫЛКА ИЗ ЯЧЕЙКИ"}},
			}}}},
		}},
	}
	doc := docFromParas(table)
	doc.Notes = []*FB2BodySection{noteSection("n1", "СНОСКА ИЗ ТАБЛИЦЫ")}
	chunks, err := ChunkPreview(context.Background(), doc, previewImagesFor(context.Background(), doc, imagePolicy), chunkPolicy)
	if err != nil {
		t.Fatalf("ChunkPreview: %v", err)
	}
	joined := strings.Join(renderAllChunks(t, context.Background(), chunks, nil, chunkPolicy, imagePolicy), "")
	if !strings.Contains(joined, "СНОСКА ИЗ ТАБЛИЦЫ") {
		t.Errorf("a footnote referenced from a table cell never rendered")
	}
}

// Packing decisions use draft sizes that never undercount the final render:
// a link rendered in its final form must not push a packed chunk over the
// ceiling. If the draft ever rendered links smaller than the final form, the
// chunker would overfill and the render would refuse the portion.
func TestChunkPreview_DraftSizesNeverUndercount(t *testing.T) {
	// Paragraph A (with its anchor) renders to 19+18=37 bytes. Paragraph B's
	// link, when it resolves, renders to 42 bytes; unwrapped it would be 18.
	// The ceiling of 60 fits A+B only in the unwrapped form — so if the draft
	// pretended links are free, both would land in one chunk and the final
	// render (79 bytes) would overflow it.
	chunkPolicy := PreviewPolicy{MaxChunkBytes: 60}
	imagePolicy := testPreviewImagePolicy()
	target := textPara("0123456789")
	target.ID = "tgt"
	doc := docFromParas(target, linkPara("#tgt", "0123456789"))

	chunks, err := ChunkPreview(context.Background(), doc, previewImagesFor(context.Background(), doc, imagePolicy), chunkPolicy)
	if err != nil {
		t.Fatalf("ChunkPreview: %v", err)
	}
	pieces := renderAllChunks(t, context.Background(), chunks, nil, chunkPolicy, imagePolicy)
	for i, piece := range pieces {
		if len(piece) > chunkPolicy.MaxChunkBytes {
			t.Errorf("chunk %d is %d bytes of HTML, ceiling is %d — the draft undercounted the link", i, len(piece), chunkPolicy.MaxChunkBytes)
		}
	}
	joined := strings.Join(pieces, "")
	if n := countOccurrences(joined, "0123456789"); n != 2 {
		t.Errorf("expected both texts to survive, got %d occurrences", n)
	}
}

// A footnote whose book id contains whitespace must reach the portion: the
// chunker and the renderer have to key notes through the same normalised form,
// or the lookup silently misses and the note text vanishes.
func TestChunkPreview_NoteIDWithSpaceLandsInChunk(t *testing.T) {
	chunkPolicy := testPreviewPolicy()
	imagePolicy := testPreviewImagePolicy()
	doc := docFromParas(noteRefPara("a b", "ССЫЛКА-НА-СНОСКУ-С-ПРОБЕЛОМ"))
	doc.Notes = []*FB2BodySection{noteSection("a b", "ТЕКСТ-СНОСКИ-С-ПРОБЕЛОМ-В-ИД")}

	chunks, err := ChunkPreview(context.Background(), doc, previewImagesFor(context.Background(), doc, imagePolicy), chunkPolicy)
	if err != nil {
		t.Fatalf("ChunkPreview: %v", err)
	}
	joined := strings.Join(renderAllChunks(t, context.Background(), chunks, nil, chunkPolicy, imagePolicy), "")
	if !strings.Contains(joined, "ТЕКСТ-СНОСКИ-С-ПРОБЕЛОМ-В-ИД") {
		t.Errorf("footnote text missing: a note whose id contains whitespace was dropped from the portion")
	}
}

// Two references to one note pull it into the chunk exactly once. The chunker
// dedups by the same key the renderer resolves refs with; keying the
// per-chunk "already seen" set by the raw id lets the second ref pull the
// note again and pay its cost twice.
func TestChunkPreview_NoteIDWithSpaceNotDoubleCounted(t *testing.T) {
	chunkPolicy := testPreviewPolicy()
	imagePolicy := testPreviewImagePolicy()
	doc := docFromParas(
		noteRefPara("a b", "ПЕРВАЯ-ССЫЛКА"),
		noteRefPara("a b", "ВТОРАЯ-ССЫЛКА"),
	)
	doc.Notes = []*FB2BodySection{noteSection("a b", "ТЕКСТ-СНОСКИ-С-ПРОБЕЛОМ-В-ИД")}

	chunks, err := ChunkPreview(context.Background(), doc, previewImagesFor(context.Background(), doc, imagePolicy), chunkPolicy)
	if err != nil {
		t.Fatalf("ChunkPreview: %v", err)
	}
	var totalNotes int
	for _, c := range chunks {
		totalNotes += len(c.notes)
	}
	if totalNotes != 1 {
		t.Errorf("footnote with whitespace id was pulled %d times, expected 1 — its cost must count once per chunk", totalNotes)
	}
}

// Two notes whose raw ids collapse to the same anchor key resolve to one
// winner, and the winner is the first in document order — the same "first id
// wins" rule the renderer enforces for block anchors. Keying the dedup guard
// by the raw id lets the second note overwrite the first under the shared
// normalised key and silently flip the winner, so this test pins the guard,
// not just the store.
func TestChunkPreview_DuplicateNormalizedNoteIDFirstWins(t *testing.T) {
	chunkPolicy := testPreviewPolicy()
	imagePolicy := testPreviewImagePolicy()
	doc := docFromParas(noteRefPara("a b", "ССЫЛКА-НА-ДУБЛИКАТ-КЛЮЧА"))
	// Order matters: the first note's raw id equals the normalised key, the
	// second's reduces to it. Only a raw-keyed dedup guard would admit both.
	doc.Notes = []*FB2BodySection{
		noteSection("ab", "ТЕКСТ-ПЕРВОЙ-СНОСКИ"),
		noteSection("a b", "ТЕКСТ-ВТОРОЙ-СНОСКИ"),
	}

	chunks, err := ChunkPreview(context.Background(), doc, previewImagesFor(context.Background(), doc, imagePolicy), chunkPolicy)
	if err != nil {
		t.Fatalf("ChunkPreview: %v", err)
	}
	joined := strings.Join(renderAllChunks(t, context.Background(), chunks, nil, chunkPolicy, imagePolicy), "")
	if !strings.Contains(joined, "ТЕКСТ-ПЕРВОЙ-СНОСКИ") {
		t.Errorf("the first note lost the dedup race — first id must win, got neither")
	}
	if strings.Contains(joined, "ТЕКСТ-ВТОРОЙ-СНОСКИ") {
		t.Errorf("the second note overwrote the first under the shared normalised key — first id must win")
	}
}

// A context already canceled before the call must report the cancellation and
// must not have started packing. The loop checks ctx at the top of each
// iteration, before the block's draft render, so on a canceled ctx the first
// iteration returns without having measured a single block. The proof is
// observable: chunks is nil even though the document has blocks to pack.
func TestChunkPreview_CanceledBeforeStartDoesNoWork(t *testing.T) {
	chunkPolicy := PreviewPolicy{MaxChunkBytes: 512}
	imagePolicy := testPreviewImagePolicy()
	doc := docFromParas(
		textPara("первый абзац"),
		textPara("второй абзац"),
		textPara("третий абзац"),
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	chunks, err := ChunkPreview(ctx, doc, previewImagesFor(context.Background(), doc, imagePolicy), chunkPolicy)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want a wrapping of context.Canceled", err)
	}
	if chunks != nil {
		t.Errorf("a canceled ctx must not produce chunks, got %d", len(chunks))
	}
}

// A cancel that arrives while the packer is running must stop before the end
// of the document. There is no production hook to fire the cancel at an exact
// block, so this is a timing test: enough blocks that the loop cannot finish
// inside the cancel window. The assertion is shape (error wraps
// context.Canceled and the packer did not produce its full output), not an
// exact chunk count.
func TestChunkPreview_CancelMidWorkStopsBeforeEnd(t *testing.T) {
	chunkPolicy := PreviewPolicy{MaxChunkBytes: 256}
	imagePolicy := testPreviewImagePolicy()
	// The chunker is fast: each iteration is microseconds. To make the
	// cancel land mid-loop without a production hook, give it enough work
	// that the loop cannot finish inside the cancel window: many blocks,
	// each carrying enough text that the draft render has something to do.
	const blockCount = 20000
	filler := strings.Repeat("текст ", 12) // ~70 bytes — small enough to fit a block under the ceiling
	paras := make([]*FB2Paragraph, 0, blockCount)
	for i := 0; i < blockCount; i++ {
		paras = append(paras, textPara(fmt.Sprintf("%04d %s", i, filler)))
	}
	doc := docFromParas(paras...)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	images := previewImagesFor(context.Background(), doc, imagePolicy)
	type result struct {
		chunks []*PreviewChunk
		err    error
	}
	done := make(chan result, 1)
	go func() {
		chunks, err := ChunkPreview(ctx, doc, images, chunkPolicy)
		done <- result{chunks, err}
	}()

	// Let the packer chew through some blocks before the cancel reaches the
	// next iteration's ctx check.
	time.Sleep(5 * time.Millisecond)
	cancel()

	select {
	case r := <-done:
		if !errors.Is(r.err, context.Canceled) {
			t.Fatalf("err = %v, want a wrapping of context.Canceled", r.err)
		}
		// On cancel the function returns nil chunks; the proof that it did
		// not silently complete is the error itself, since the only path
		// that returns a non-nil error is the ctx check.
		if r.chunks != nil {
			t.Errorf("a canceled ctx must not produce chunks, got %d", len(r.chunks))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ChunkPreview did not return within 10s of cancel")
	}
}

// With a non-canceled context the chunker behaves exactly as it did before
// ctx was added: it produces chunks that contain every block exactly once,
// in reading order, none over the ceiling. This is the regression guard —
// if a future change makes the ctx check misfire on a live context, this
// test catches it.
func TestChunkPreview_LiveContextMatchesNoContextBaseline(t *testing.T) {
	chunkPolicy := PreviewPolicy{MaxChunkBytes: 512}
	imagePolicy := testPreviewImagePolicy()
	markers := []string{"ПЕРВЫЙ", "ВТОРОЙ", "ТРЕТИЙ"}
	doc := docFromParas(
		textPara(markers[0]),
		textPara(markers[1]),
		textPara(markers[2]),
	)

	chunks, err := ChunkPreview(context.Background(), doc, previewImagesFor(context.Background(), doc, imagePolicy), chunkPolicy)
	if err != nil {
		t.Fatalf("a live ctx must not produce an error: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("a live ctx produced zero chunks")
	}
	joined := strings.Join(renderAllChunks(t, context.Background(), chunks, nil, chunkPolicy, imagePolicy), "")
	for _, m := range markers {
		if n := countOccurrences(joined, m); n != 1 {
			t.Errorf("marker %q appears %d times, expected exactly 1", m, n)
		}
	}
	if err := markerOrder(joined, markers); err != nil {
		t.Errorf("reading order broken: %v", err)
	}
}
