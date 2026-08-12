package converter

// Bugfix tests: red tests for two defects found in code review.
// Bug 1: Synthetic anchor not reserved
// Bug 2: Images prepared from unreachable notes

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// TestBlockAnchor_SyntheticAnchorReserved fixes Bug 1. The original
// blockAnchor code selected a free anchor by checking usedAnchors, but did
// not mark the chosen anchor as used before returning. This caused subsequent
// calls to select the same anchor, producing duplicate IDs in the HTML.
//
// The test creates three sections: first has a real id, second and third are
// unnamed. Both unnamed sections must receive different synthetic anchors.
func TestBlockAnchor_SyntheticAnchorReserved(t *testing.T) {
	pngData := uniformImage(t, "png", 4, 4)
	binaries := map[string]FB2Binary{"ill1": {Data: pngData, MIME: "image/png"}}

	// Three sections: first with id, second and third without
	doc := &FB2Document{
		Body: &FB2BodySection{Content: []*FB2ContentItem{
			{Section: &FB2BodySection{
				Title: "Первая",
				ID:    "!auto-1", // real id from the book
				Content: []*FB2ContentItem{
					{Paragraph: textPara("Текст первой секции")},
				},
			}},
			{Section: &FB2BodySection{
				Title: "Вторая",
				Content: []*FB2ContentItem{
					{Paragraph: textPara("Текст второй секции")},
				},
			}},
			{Section: &FB2BodySection{
				Title: "Третья",
				Content: []*FB2ContentItem{
					{Paragraph: textPara("Текст третьей секции")},
				},
			}},
		}},
	}

	images, err := BuildPreviewImages(context.Background(), binaries, testPreviewImageBase(), testPreviewImagePolicy(), 0)
	if err != nil {
		t.Fatalf("BuildPreviewImages: %v", err)
	}

	chunks, err := ChunkPreview(context.Background(), doc, images.Projection(), testPreviewPolicy())
	if err != nil {
		t.Fatalf("ChunkPreview: %v", err)
	}

	// Render all chunks
	pieces := renderAllChunks(t, context.Background(), chunks, binaries, testPreviewPolicy(), testPreviewImagePolicy())
	joined := strings.Join(pieces, "")

	// Collect all id attributes
	idRegex := regexp.MustCompile(`id="([^"]+)"`)
	ids := idRegex.FindAllStringSubmatch(joined, -1)

	// Count occurrences of each id
	idCounts := make(map[string]int)
	for _, match := range ids {
		if len(match) > 1 {
			idCounts[match[1]]++
		}
	}

	// Check for duplicates
	for id, count := range idCounts {
		if count > 1 {
			t.Errorf("Duplicate id %q appears %d times in HTML", id, count)
		}
	}

	// Verify we got three distinct anchors for three sections
	if len(idCounts) < 3 {
		t.Errorf("Expected at least 3 unique anchors for 3 sections, got %d: %v", len(idCounts), idCounts)
	}

	// Specifically verify the expected anchors exist
	// First section has real id, should get pv-!auto-1
	foundFirst := false
	foundSecond := false
	foundThird := false
	for id := range idCounts {
		if id == "pv-!auto-1" {
			foundFirst = true
		}
		if strings.HasPrefix(id, "pv-!auto-") && id != "pv-!auto-1" {
			// Should be either pv-!auto-0 or pv-!auto-2
			// depending on section index
			if !foundSecond {
				foundSecond = true
			} else {
				foundThird = true
			}
		}
	}

	if !foundFirst {
		t.Error("Anchor for first section (with real id) not found")
	}
	if !foundSecond {
		t.Error("Anchor for second unnamed section not found")
	}
	if !foundThird {
		t.Error("Anchor for third unnamed section not found")
	}

	auditPreviewHTML(t, joined)
}

// TestUsedBinaries_OnlyReachableNotes fixes Bug 2. The original UsedBinaries
// walked ALL notes, preparing images even from notes that were never
// referenced in the body. This wasted preparation time, cache space, and could
// reject the entire book if an unreachable note had a large image.
//
// The test creates two notes with images, but only one is referenced from the
// body. Only the reachable note's image should be prepared.
func TestUsedBinaries_OnlyReachableNotes(t *testing.T) {
	png := uniformImage(t, "png", 8, 8)

	// Body references only note-1, not note-2
	doc := &FB2Document{
		Body: &FB2BodySection{Content: []*FB2ContentItem{
			{Paragraph: noteRefPara("note-1", "ссылка на первую сноску")},
		}},
		Notes: []*FB2BodySection{
			{
				ID: "note-1",
				Content: []*FB2ContentItem{
					{Paragraph: &FB2Paragraph{Kind: ParagraphKindNormal, Content: []*FB2InlineElement{
						{Type: InlineTypeText, Content: "Текст достижимой сноски"},
						{Type: InlineTypeImage, Attrs: map[string]string{"href": "#reachable_img"}},
					}}},
				},
			},
			{
				ID: "note-2",
				Content: []*FB2ContentItem{
					{Paragraph: &FB2Paragraph{Kind: ParagraphKindNormal, Content: []*FB2InlineElement{
						{Type: InlineTypeText, Content: "Текст недостижимой сноски"},
						{Type: InlineTypeImage, Attrs: map[string]string{"href": "#unreachable_img"}},
					}}},
				},
			},
		},
		Binary: map[string]FB2Binary{
			"reachable_img":   {Data: png, MIME: "image/png"},
			"unreachable_img": {Data: png, MIME: "image/png"},
		},
	}

	used := UsedBinaries(doc)

	// Only reachable_img should be in the used set
	if len(used) != 1 {
		t.Errorf("UsedBinaries returned %d ids, want 1 (only the reachable note's image)", len(used))
	}

	if _, ok := used["reachable_img"]; !ok {
		t.Error("reachable_img missing from UsedBinaries")
	}

	if _, ok := used["unreachable_img"]; ok {
		t.Error("unreachable_img should not be in UsedBinaries - the note is never referenced")
	}

	// Verify through actual preparation count
	calls := 0
	prev := preparePreviewImage
	preparePreviewImage = func(data []byte, p PreviewImagePolicy) ([]byte, string, error) {
		calls++
		return PreparePreviewImage(data, p)
	}
	t.Cleanup(func() { preparePreviewImage = prev })

	_, err := BuildPreviewImages(context.Background(), used, testPreviewImageBase(), testPreviewImagePolicy(), 0)
	if err != nil {
		t.Fatalf("BuildPreviewImages: %v", err)
	}

	if calls != 1 {
		t.Errorf("prepare called %d times, want 1 — only the reachable note's image should be prepared", calls)
	}
}

// TestUsedBinaries_UnreferencedNotesNotPrepared verifies that when the body
// has no note references at all, no note images are prepared.
func TestUsedBinaries_UnreferencedNotesNotPrepared(t *testing.T) {
	png := uniformImage(t, "png", 8, 8)

	doc := &FB2Document{
		Body: &FB2BodySection{Content: []*FB2ContentItem{
			{Paragraph: textPara("Текст без ссылок на сноски")},
		}},
		Notes: []*FB2BodySection{
			{
				ID: "unreachable-1",
				Content: []*FB2ContentItem{
					{Paragraph: &FB2Paragraph{Kind: ParagraphKindNormal, Content: []*FB2InlineElement{
						{Type: InlineTypeImage, Attrs: map[string]string{"href": "#img1"}},
					}}},
				},
			},
			{
				ID: "unreachable-2",
				Content: []*FB2ContentItem{
					{Paragraph: &FB2Paragraph{Kind: ParagraphKindNormal, Content: []*FB2InlineElement{
						{Type: InlineTypeImage, Attrs: map[string]string{"href": "#img2"}},
					}}},
				},
			},
		},
		Binary: map[string]FB2Binary{
			"img1": {Data: png, MIME: "image/png"},
			"img2": {Data: png, MIME: "image/png"},
		},
	}

	used := UsedBinaries(doc)

	// No images should be collected from unreferenced notes
	if len(used) != 0 {
		t.Errorf("UsedBinaries returned %d ids, want 0 (no note references in body)", len(used))
	}

	// Verify through preparation count
	calls := 0
	prev := preparePreviewImage
	preparePreviewImage = func(data []byte, p PreviewImagePolicy) ([]byte, string, error) {
		calls++
		return PreparePreviewImage(data, p)
	}
	t.Cleanup(func() { preparePreviewImage = prev })

	_, err := BuildPreviewImages(context.Background(), used, testPreviewImageBase(), testPreviewImagePolicy(), 0)
	if err != nil {
		t.Fatalf("BuildPreviewImages: %v", err)
	}

	if calls != 0 {
		t.Errorf("prepare called %d times, want 0 — no images from unreferenced notes should be prepared", calls)
	}
}

// TestUsedBinaries_ReachableNoteWithImageStillWorks verifies that a note
// with an image that IS referenced still gets its image prepared (regression
// test for the fix).
func TestUsedBinaries_ReachableNoteWithImageStillWorks(t *testing.T) {
	png := uniformImage(t, "png", 8, 8)

	doc := &FB2Document{
		Body: &FB2BodySection{Content: []*FB2ContentItem{
			{Paragraph: noteRefPara("note-1", "ссылка")},
		}},
		Notes: []*FB2BodySection{
			{
				ID: "note-1",
				Content: []*FB2ContentItem{
					{Paragraph: &FB2Paragraph{Kind: ParagraphKindNormal, Content: []*FB2InlineElement{
						{Type: InlineTypeText, Content: "Текст сноски"},
						{Type: InlineTypeImage, Attrs: map[string]string{"href": "#note_img"}},
					}}},
				},
			},
		},
		Binary: map[string]FB2Binary{
			"note_img": {Data: png, MIME: "image/png"},
		},
	}

	used := UsedBinaries(doc)

	if _, ok := used["note_img"]; !ok {
		t.Error("note_img should be in UsedBinaries - the note is referenced")
	}

	calls := 0
	prev := preparePreviewImage
	preparePreviewImage = func(data []byte, p PreviewImagePolicy) ([]byte, string, error) {
		calls++
		return PreparePreviewImage(data, p)
	}
	t.Cleanup(func() { preparePreviewImage = prev })

	set, err := BuildPreviewImages(context.Background(), used, testPreviewImageBase(), testPreviewImagePolicy(), 0)
	if err != nil {
		t.Fatalf("BuildPreviewImages: %v", err)
	}

	if calls != 1 {
		t.Errorf("prepare called %d times, want 1", calls)
	}
	if set.Len() != 1 {
		t.Errorf("prepared %d images, want 1", set.Len())
	}
}
