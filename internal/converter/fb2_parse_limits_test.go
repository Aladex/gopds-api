package converter

// fb2_parse_limits_test.go pins the limited parse entry: the node and binary
// gates must stop the parse, not measure it. Every refusal test therefore
// asserts on the processed-token counter, not on the error text — a parse
// that walks the whole document and refuses afterwards produces the same
// error and still spends the work the gates exist to protect. The unlimited
// entries (the EPUB download path) must behave exactly as before.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fb2DocWithParagraphs builds a valid book of n one-word paragraphs, which is
// n+3 element nodes (FictionBook, body, section, plus one per paragraph).
func fb2DocWithParagraphs(n int) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><FictionBook><body><section>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "<p>w%d</p>", i)
	}
	b.WriteString(`</section></body></FictionBook>`)
	return []byte(b.String())
}

// fb2DocWithBinaries builds a valid book carrying n distinct one-byte
// binaries after a one-paragraph body. The binaries sit at the END of the
// document, so stopping at the binary cap leaves provably unprocessed tokens
// behind.
func fb2DocWithBinaries(n int) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><FictionBook><body><section><p>x</p></section></body>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<binary id="b%d" content-type="image/png">AA==</binary>`, i)
	}
	b.WriteString(`</FictionBook>`)
	return []byte(b.String())
}

// The node cap must stop the parse at the exceeding element: the error is
// typed, the node counter shows exactly cap+1 (the exceeding element is
// counted, then the parse stops), and the token counter proves the rest of
// the document was never walked. A mutation that counts but keeps walking
// (refusing only after the loop) is caught by the token counter, not by the
// error — both produce the same one.
func TestParseFB2CompleteLimited_StopsAtNodeLimit(t *testing.T) {
	const limit = 50
	doc := fb2DocWithParagraphs(200) // 203 element nodes

	_, _, full, err := ParseFB2CompleteLimited(context.Background(), doc, false, FB2ParseLimits{})
	if err != nil {
		t.Fatalf("unlimited parse of the fixture: %v", err)
	}
	if full.Nodes != 203 {
		t.Fatalf("fixture holds %d nodes, want 203 — the fixture, not the gate, is wrong", full.Nodes)
	}

	_, _, stats, err := ParseFB2CompleteLimited(context.Background(), doc, false, FB2ParseLimits{MaxNodes: limit})
	if !errors.Is(err, ErrFB2NodeLimit) {
		t.Fatalf("err = %v, want ErrFB2NodeLimit", err)
	}
	if stats.Nodes != limit+1 {
		t.Errorf("processed %d nodes before the refusal, want exactly %d (cap + the exceeding element)", stats.Nodes, limit+1)
	}
	if stats.Tokens >= full.Tokens {
		t.Errorf("processed %d tokens out of %d — the parse walked the whole document and refused afterwards, "+
			"which is a refusal by result, not by work", stats.Tokens, full.Tokens)
	}
}

// A document of exactly the cap in nodes parses whole. Kills the off-by-one
// that refuses at >= instead of >.
func TestParseFB2CompleteLimited_NodesExactlyAtLimitPass(t *testing.T) {
	const limit = 50
	doc := fb2DocWithParagraphs(limit - 3) // exactly 50 element nodes

	_, _, stats, err := ParseFB2CompleteLimited(context.Background(), doc, false, FB2ParseLimits{MaxNodes: limit})
	if err != nil {
		t.Fatalf("err = %v, want nil for a document of exactly the node cap", err)
	}
	if stats.Nodes != limit {
		t.Errorf("counted %d nodes, want %d — the counter itself is off", stats.Nodes, limit)
	}
}

// The binary cap must stop the parse at the exceeding <binary> element. The
// fixture's binaries sit at the end of the document, so a parse that counts
// but does not stop processes measurably more tokens than one that stops.
func TestParseFB2CompleteLimited_StopsAtBinaryLimit(t *testing.T) {
	const limit = 1
	doc := fb2DocWithBinaries(3)

	_, _, full, err := ParseFB2CompleteLimited(context.Background(), doc, false, FB2ParseLimits{})
	if err != nil {
		t.Fatalf("unlimited parse of the fixture: %v", err)
	}
	if full.Binaries != 3 {
		t.Fatalf("fixture holds %d binaries, want 3 — the fixture, not the gate, is wrong", full.Binaries)
	}

	_, _, stats, err := ParseFB2CompleteLimited(context.Background(), doc, false, FB2ParseLimits{MaxBinaries: limit})
	if !errors.Is(err, ErrFB2BinaryLimit) {
		t.Fatalf("err = %v, want ErrFB2BinaryLimit", err)
	}
	if stats.Binaries != limit+1 {
		t.Errorf("processed %d binaries before the refusal, want exactly %d", stats.Binaries, limit+1)
	}
	if stats.Tokens >= full.Tokens {
		t.Errorf("processed %d tokens out of %d — the parse walked past the exceeding binary", stats.Tokens, full.Tokens)
	}
}

// Exactly the cap in binaries parses whole.
func TestParseFB2CompleteLimited_BinariesExactlyAtLimitPass(t *testing.T) {
	doc := fb2DocWithBinaries(2)

	_, _, stats, err := ParseFB2CompleteLimited(context.Background(), doc, false, FB2ParseLimits{MaxBinaries: 2})
	if err != nil {
		t.Fatalf("err = %v, want nil for a document at exactly the binary cap", err)
	}
	if stats.Binaries != 2 {
		t.Errorf("counted %d binaries, want 2", stats.Binaries)
	}
}

// Zero limits mean unbounded, and the unbounded limited entry must agree with
// the EPUB wrapper on the same document — the limited entry is the preview's
// gate, not a second parser with its own idea of a book.
func TestParseFB2CompleteLimited_ZeroLimitsMatchUnlimited(t *testing.T) {
	doc := fb2DocWithParagraphs(20)

	docRef, bookRef, err := ParseFB2Complete(context.Background(), doc, false)
	if err != nil {
		t.Fatalf("ParseFB2Complete: %v", err)
	}
	docLim, bookLim, stats, err := ParseFB2CompleteLimited(context.Background(), doc, false, FB2ParseLimits{})
	if err != nil {
		t.Fatalf("ParseFB2CompleteLimited with zero limits: %v", err)
	}
	if len(docLim.Body.Content) != len(docRef.Body.Content) {
		t.Errorf("body content differs: limited %d items, unlimited %d", len(docLim.Body.Content), len(docRef.Body.Content))
	}
	if bookLim.Title != bookRef.Title {
		t.Errorf("metadata differs: limited %q, unlimited %q", bookLim.Title, bookRef.Title)
	}
	if stats.Nodes != 23 {
		t.Errorf("counted %d nodes, want the 23 the fixture holds", stats.Nodes)
	}
}

// The EPUB path is the unlimited wrapper and must stay one: a document far
// over the preview's node cap still parses. A mutation that moves the limits
// into the shared loop without a zero-means-off convention fails here.
func TestParseFB2Complete_StaysUnlimitedForTheEPUBPath(t *testing.T) {
	doc := fb2DocWithParagraphs(5000) // 5003 element nodes, far over any preview cap

	parsed, _, err := ParseFB2Complete(context.Background(), doc, false)
	if err != nil {
		t.Fatalf("the EPUB path must stay unlimited: %v", err)
	}
	if got := len(parsed.Body.Content[0].Section.Content); got != 5000 {
		t.Errorf("parsed %d paragraphs, want all 5000", got)
	}
}

// The salvage fallback re-parses from scratch when the main decoder breaks
// after a verified root, so it is a second parse that needs the same caps.
// parseFB2CompleteFallback takes them explicitly; this pins that a document
// over the node cap is refused by the fallback too, early (token counter),
// with the same typed error.
func TestParseFB2CompleteFallback_EnforcesNodeLimit(t *testing.T) {
	const limit = 50
	doc := fb2DocWithParagraphs(200)

	_, _, stats, err := parseFB2CompleteFallback(context.Background(), doc, false, FB2ParseLimits{MaxNodes: limit})
	if !errors.Is(err, ErrFB2NodeLimit) {
		t.Fatalf("err = %v, want ErrFB2NodeLimit from the fallback parse", err)
	}
	if stats.Nodes != limit+1 {
		t.Errorf("the fallback processed %d nodes before refusing, want exactly %d", stats.Nodes, limit+1)
	}
}

// Same pin for the binary cap on the fallback path.
func TestParseFB2CompleteFallback_EnforcesBinaryLimit(t *testing.T) {
	doc := fb2DocWithBinaries(3)

	_, _, stats, err := parseFB2CompleteFallback(context.Background(), doc, false, FB2ParseLimits{MaxBinaries: 1})
	if !errors.Is(err, ErrFB2BinaryLimit) {
		t.Fatalf("err = %v, want ErrFB2BinaryLimit from the fallback parse", err)
	}
	if stats.Binaries != 2 {
		t.Errorf("the fallback processed %d binaries before refusing, want exactly 2", stats.Binaries)
	}
}

// fb2DocWithEarlyCDBreak builds a book whose root is valid but an early
// unclosed <![CDATA[ forces the main decoder into the lexical salvage path
// (parseFB2BodyLoose), followed by n plain paragraphs. The salvage path is
// where the node cap was missing: without the gate it walks every paragraph.
func fb2DocWithEarlyCDBreak(n int) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><FictionBook><body><section>`)
	b.WriteString(`<p><![CDATA[ сломанный хвост </p>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<p>абзац %d</p>`, i)
	}
	b.WriteString(`</section></body></FictionBook>`)
	return []byte(b.String())
}

// The lexical salvage path (parseFB2BodyLoose) must carry the same node cap
// as the main token loop. The cap is enforced at the exceeding element, not
// measured after the fact: the node counter must land near the limit, not at
// the full paragraph count. A mutation that counts but keeps walking produces
// the same typed error, so only the counter distinguishes "stopped" from
// "walked everything then refused" — the assertion is on stats.Nodes.
func TestLoosePathRespectsNodeLimit(t *testing.T) {
	const limit = 50
	doc := fb2DocWithEarlyCDBreak(5000)

	_, _, stats, err := ParseFB2CompleteLimited(context.Background(), doc, false, FB2ParseLimits{MaxNodes: limit})
	if !errors.Is(err, ErrFB2NodeLimit) {
		t.Fatalf("err = %v, want ErrFB2NodeLimit — the loose path bypassed the node cap", err)
	}
	if stats.Nodes > 2*limit {
		t.Errorf("loose path processed %d nodes before stopping, want at most ~%d — "+
			"the cap was counted but not enforced (refusal by result, not by work)",
			stats.Nodes, 2*limit)
	}
}

// The salvage path must observe context cancellation. The main token loop
// reaches the decoder error in fewer than ctxCheckInterval tokens, so a
// pre-canceled ctx is not caught there — only the salvage path, where the
// check must exist, can observe it. Targets parseFB2BodyCore directly because
// completeDecodeFallback does not pass context errors through (only typed
// parse refusals), which is correct for the EPUB path contract.
func TestLoosePathRespectsContext(t *testing.T) {
	doc := fb2DocWithEarlyCDBreak(5000)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := parseFB2BodyCore(ctx, doc, FB2ParseLimits{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled — the loose path did not observe ctx", err)
	}
}

// A small broken book within the cap must still be salvaged. The salvage path
// exists because broken XML is common in the catalog; the node cap bounds the
// work, it does not refuse recoverable books.
func TestLoosePathStillSalvagesSmallBrokenBook(t *testing.T) {
	doc := fb2DocWithEarlyCDBreak(10)

	parsed, _, _, err := ParseFB2CompleteLimited(context.Background(), doc, false, FB2ParseLimits{MaxNodes: 100})
	if err != nil {
		t.Fatalf("salvage of a small broken book within the cap failed: %v", err)
	}
	if parsed == nil || parsed.Body == nil {
		t.Fatalf("salvage returned no document")
	}
	paragraphs := 0
	for _, item := range parsed.Body.Content {
		if item.Paragraph != nil {
			paragraphs++
		}
	}
	if paragraphs < 10 {
		t.Errorf("salvage extracted %d paragraphs, want at least 10", paragraphs)
	}
}
