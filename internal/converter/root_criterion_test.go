package converter

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"gopds-api/internal/parser"
)

// The thirteenth-iteration criterion: one document gets one verdict. The
// first start element the XML decoder reaches after the shared sanitizers
// must be FictionBook — in the metadata scanner and in the download
// converter alike. A nested FictionBook, a bare <body>, or a lexical
// "fictionbook" substring prove nothing: they cannot tell a book from HTML.

// rcCP1251Greeting is "Привет" in windows-1251, produced by iconv (an
// encoder independent of the charmaps under test):
//
//	echo -n 'Привет' | iconv -f UTF-8 -t WINDOWS-1251 | xxd -p  # cff0e8e2e5f2
var rcCP1251Greeting = []byte{0xcf, 0xf0, 0xe8, 0xe2, 0xe5, 0xf2}

const rcGreeting = "Привет"

func rcUTF16WithBOM(s string) []byte {
	units := utf16.Encode([]rune(s))
	var buf bytes.Buffer
	buf.Write([]byte{0xFF, 0xFE})
	for _, u := range units {
		_ = binary.Write(&buf, binary.LittleEndian, u)
	}
	return buf.Bytes()
}

// rcBuildFourPaths encodes one prolog-and-tail combination for all four
// charset paths — valid UTF-8, UTF-16 with a BOM, declared windows-1251 with
// real cp1251 bytes, and declared UTF-8 with one corrupt byte (the repair
// path). The tail must contain the greeting exactly once, so the single-byte
// path can splice in real cp1251 bytes.
func rcBuildFourPaths(t *testing.T, prolog, tail string) map[string][]byte {
	t.Helper()
	parts := strings.SplitN(tail, rcGreeting, 2)
	if len(parts) != 2 {
		t.Fatalf("tail must contain %q exactly once", rcGreeting)
	}
	build := func(decl string) []byte {
		return []byte(`<?xml version="1.0" encoding="` + decl + `"?>` + prolog + tail)
	}
	docs := make(map[string][]byte, 4)
	docs["valid utf-8"] = build("utf-8")
	docs["utf-16 with BOM"] = rcUTF16WithBOM(string(build("utf-16")))
	docs["repaired utf-8"] = append(build("utf-8"), 0xFF)
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="windows-1251"?>`)
	b.WriteString(prolog)
	b.WriteString(parts[0])
	b.Write(rcCP1251Greeting)
	b.WriteString(parts[1])
	docs["declared single-byte"] = b.Bytes()
	return docs
}

// rcVerdicts records whether each consumer path accepted one document.
type rcVerdicts struct {
	meta     error
	complete error
	body     error
}

// rcRunAllPaths runs one document through the metadata scanner and both
// converter entry points, on every charset path.
func rcRunAllPaths(t *testing.T, prolog, tail string) map[string]rcVerdicts {
	t.Helper()
	out := make(map[string]rcVerdicts, 4)
	for path, doc := range rcBuildFourPaths(t, prolog, tail) {
		_, metaErr := parser.NewFB2Parser(false).Parse(bytes.NewReader(doc))
		_, _, completeErr := ParseFB2Complete(context.Background(), doc, false)
		_, bodyErr := ParseFB2Body(context.Background(), doc)
		out[path] = rcVerdicts{metaErr, completeErr, bodyErr}
	}
	return out
}

// TestRootCriterion_MetadataAndConverterAgree is the thirteenth-iteration
// contract: every input below gets the same verdict from the metadata
// scanner (FB2Parser.Parse) and from the download path (ParseFB2Complete,
// ParseFB2Body), on every charset path. Real books pass everywhere; input
// whose root is unreachable or foreign is refused everywhere — a converter
// refusal is always the typed ErrNotFictionBook.
func TestRootCriterion_MetadataAndConverterAgree(t *testing.T) {
	const bookTail = `<FictionBook><body><section><p>Привет</p></section></body></FictionBook>`
	const htmlTail = `<html><body><p>Привет</p></body></html>`
	cases := []struct {
		name   string
		prolog string
		tail   string
		accept bool
	}{
		// Real books: accepted by both pipelines.
		{"control: real book", ``, bookTail, true},
		{"unterminated PI before the root", `<?render mode=[`, bookTail, true},
		{"real FictionBook without body", ``,
			`<FictionBook><description><title-info><book-title>Привет</book-title></title-info></description></FictionBook>`, true},
		// Prolog damage the sanitizers cannot neutralize: the decoder never
		// reaches the root, and both pipelines refuse.
		{"unterminated comment before the root", `<!-- banner without an end`, bookTail, false},
		{"unterminated CDATA before the root", `<![CDATA[ banner without an end`, bookTail, false},
		{"doctype with an unterminated quote", `<!DOCTYPE FictionBook [ <!ENTITY x "broken> ]`, bookTail, false},
		// Non-books: a body element or a FictionBook marker that is not the
		// root proves nothing; both pipelines refuse.
		{"plain html", ``, htmlTail, false},
		{"FictionBook nested under a wrapper", ``,
			`<wrapper><FictionBook><body><p>Привет</p></body></FictionBook></wrapper>`, false},
		{"bang construct cannot smuggle a root", `<!BROKEN <FictionBook>>`, htmlTail, false},
		{"lowercase root tag", ``, `<fictionbook><body><p>Привет</p></body></fictionbook>`, false},
		{"look-alike root tag", ``, `<FictionBookish><body><p>Привет</p></body></FictionBookish>`, false},
		{"fictionbook substring in broken xml", ``, `broken doc mentioning fictionbook <p>Привет</p>`, false},
		{"fictionbook substring behind an unterminated comment", `<!-- fictionbook`, `<p>Привет</p>`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for path, v := range rcRunAllPaths(t, tc.prolog, tc.tail) {
				if got := v.meta == nil; got != tc.accept {
					t.Errorf("%s: metadata accept=%v, want %v (err=%v)", path, got, tc.accept, v.meta)
				}
				if got := v.complete == nil; got != tc.accept {
					t.Errorf("%s: ParseFB2Complete accept=%v, want %v (err=%v)", path, got, tc.accept, v.complete)
				}
				if got := v.body == nil; got != tc.accept {
					t.Errorf("%s: ParseFB2Body accept=%v, want %v (err=%v)", path, got, tc.accept, v.body)
				}
				if tc.accept {
					continue
				}
				if !errors.Is(v.complete, ErrNotFictionBook) {
					t.Errorf("%s: ParseFB2Complete refusal must be a typed ErrNotFictionBook, got %v", path, v.complete)
				}
				if !errors.Is(v.body, ErrNotFictionBook) {
					t.Errorf("%s: ParseFB2Body refusal must be a typed ErrNotFictionBook, got %v", path, v.body)
				}
			}
		})
	}

	// An unterminated XML declaration cannot go through rcBuildFourPaths (it
	// writes its own declaration), so each path is built by hand. The
	// declaration swallows the rest of the document by XML rules, so the
	// root is unreachable and every path refuses; the refusal may come from
	// the charset stage itself, so no specific typed error is pinned.
	t.Run("unterminated XML declaration", func(t *testing.T) {
		docs := map[string][]byte{
			"valid utf-8":     []byte(`<?xml version="1.0" encoding="utf-8"` + bookTail),
			"utf-16 with BOM": rcUTF16WithBOM(`<?xml version="1.0" encoding="utf-16"` + bookTail),
			"repaired utf-8":  append([]byte(`<?xml version="1.0" encoding="utf-8"`+bookTail), 0xFF),
		}
		var b bytes.Buffer
		b.WriteString(`<?xml version="1.0" encoding="windows-1251"`)
		b.WriteString(`<FictionBook><body><section><p>`)
		b.Write(rcCP1251Greeting)
		b.WriteString(`</p></section></body></FictionBook>`)
		docs["declared single-byte"] = b.Bytes()

		for path, doc := range docs {
			_, metaErr := parser.NewFB2Parser(false).Parse(bytes.NewReader(doc))
			_, _, completeErr := ParseFB2Complete(context.Background(), doc, false)
			_, bodyErr := ParseFB2Body(context.Background(), doc)
			if metaErr == nil || completeErr == nil || bodyErr == nil {
				t.Errorf("%s: an unterminated declaration must refuse everywhere, got meta=%v complete=%v body=%v",
					path, metaErr, completeErr, bodyErr)
			}
		}
	})
}

// TestParseFB2Complete_RescueRequiresVerifiedRoot pins both halves of the
// thirteenth-iteration salvage rule: once the decoder has verified the
// FictionBook root, later damage is salvaged lexically; damage that keeps
// the decoder from ever reaching the root is a refusal — the same one the
// metadata scanner gives.
func TestParseFB2Complete_RescueRequiresVerifiedRoot(t *testing.T) {
	t.Run("damage after the verified root is salvaged", func(t *testing.T) {
		in := []byte(`<?xml version="1.0" encoding="utf-8"?>` +
			`<FictionBook><body><section><p>SALVAGE MARKER</p><![CDATA[never closed`)
		doc, _, err := ParseFB2Complete(context.Background(), in, false)
		if err != nil {
			t.Fatalf("a verified book must be salvaged, got %v", err)
		}
		if joined := joinedParagraphTexts(doc); !strings.Contains(joined, "SALVAGE MARKER") {
			t.Errorf("salvaged text lost; got: %.200s", joined)
		}
	})

	t.Run("damage before the root is a refusal", func(t *testing.T) {
		in := []byte(`<?xml version="1.0" encoding="utf-8"?>` +
			`<!-- fictionbook <p>SALVAGE MARKER</p>`)
		if _, _, err := ParseFB2Complete(context.Background(), in, false); !errors.Is(err, ErrNotFictionBook) {
			t.Errorf("expected ErrNotFictionBook when the root is unreachable, got %v", err)
		}
		if _, err := ParseFB2Body(context.Background(), in); !errors.Is(err, ErrNotFictionBook) {
			t.Errorf("expected ErrNotFictionBook when the root is unreachable, got %v", err)
		}
	})
}

// TestParseFB2Complete_DamagedPrologPipelineBudget pins the whole-pipeline
// cost of a hostile prolog: charset resolution, every sanitizer, the XML
// decoder and the refusal — not just the charset stage. The input is a
// 0xFF-terminated run of unterminated comments, so the repair path and every
// sanitizer sweep the full document before the decoder fails without ever
// reaching the root. Best of 7 runs.
//
// Two assertions. The growth ratio guards the shape everywhere: 4x is
// linear for 4x the input, 16x is quadratic, and 8 sits between them. The
// absolute budgets (10 ms at 256 KiB, 35 ms at 1 MiB) hold only where wall
// time is meaningful: the race detector reprices CPU-bound work by an order
// of magnitude on this machine (28 ms and 116 ms for the same input), so
// under -race only the ratio is asserted.
func TestParseFB2Complete_DamagedPrologPipelineBudget(t *testing.T) {
	build := func(size int) []byte {
		var b bytes.Buffer
		b.WriteString(`<?xml version="1.0" encoding="utf-8"?>`)
		for b.Len() < size {
			b.WriteString("<!--")
		}
		b.WriteByte(0xFF) // one corrupt byte: the repair path, not the valid-UTF-8 fast path
		return b.Bytes()
	}
	bestTime := func(in []byte) time.Duration {
		best := time.Duration(1<<63 - 1)
		for i := 0; i < 7; i++ {
			start := time.Now()
			if _, _, err := ParseFB2Complete(context.Background(), in, false); !errors.Is(err, ErrNotFictionBook) {
				t.Fatalf("a hostile prolog must be refused, got %v", err)
			}
			if d := time.Since(start); d < best {
				best = d
			}
		}
		return best
	}
	small := bestTime(build(256 << 10))
	large := bestTime(build(1 << 20))
	ratio := float64(large) / float64(small)
	t.Logf("256 KiB: %v, 1 MiB: %v, ratio %.2f", small, large, ratio)
	if ratio >= 8 {
		t.Errorf("pipeline grows faster than linearly: 256 KiB took %v, 1 MiB took %v (%.1fx, quadratic would be 16x)",
			small, large, ratio)
	}
	if raceDetectorEnabled {
		t.Logf("race detector build: absolute budgets not asserted (wall time repriced)")
		return
	}
	if small > 10*time.Millisecond {
		t.Errorf("256 KiB hostile prolog took %v, budget is 10 ms", small)
	}
	if large > 35*time.Millisecond {
		t.Errorf("1 MiB hostile prolog took %v, budget is 35 ms", large)
	}
}
