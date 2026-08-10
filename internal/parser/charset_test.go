package parser

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

// Encoded byte literals below were produced once by iconv (an encoder
// independent of the x/text charmaps under test) and pasted as hex:
//
//	echo -n 'Привет' | iconv -f UTF-8 -t WINDOWS-1251 | xxd -p  # cff0e8e2e5f2
//	echo -n 'Привет' | iconv -f UTF-8 -t KOI8-R      | xxd -p  # f0d2c9d7c5d4
//	echo -n 'Привет' | iconv -f UTF-8 -t ISO-8859-5  | xxd -p  # bfe0d8d2d5e2
//	echo -n 'café'   | iconv -f UTF-8 -t ISO-8859-1  | xxd -p  # 636166e9
var (
	cp1251Privet  = []byte{0xcf, 0xf0, 0xe8, 0xe2, 0xe5, 0xf2}
	koi8rPrivet   = []byte{0xf0, 0xd2, 0xc9, 0xd7, 0xc5, 0xd4}
	latin5Privet  = []byte{0xbf, 0xe0, 0xd8, 0xd2, 0xd5, 0xe2}
	latin1Cafe    = []byte{0x63, 0x61, 0x66, 0xe9}
	replacementCh = "�"
)

func charsetTestDoc(decl, marker string) []byte {
	return []byte(`<?xml version="1.0" encoding="` + decl + `"?>` +
		`<FictionBook><body><section><p>` + marker + `</p></section></body></FictionBook>`)
}

// charsetTestDocEncoded splices pre-encoded marker bytes into a document with
// the given declaration, so the file bytes really are in the declared
// single-byte charset.
func charsetTestDocEncoded(decl string, marker []byte) []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="` + decl + `"?>`)
	b.WriteString(`<FictionBook><body><section><p>`)
	b.Write(marker)
	b.WriteString(`</p></section></body></FictionBook>`)
	return b.Bytes()
}

func utf16WithBOM(s string, littleEndian bool) []byte {
	units := utf16.Encode([]rune(s))
	var buf bytes.Buffer
	if littleEndian {
		buf.Write([]byte{0xFF, 0xFE})
		for _, u := range units {
			_ = binary.Write(&buf, binary.LittleEndian, u)
		}
	} else {
		buf.Write([]byte{0xFE, 0xFF})
		for _, u := range units {
			_ = binary.Write(&buf, binary.BigEndian, u)
		}
	}
	return buf.Bytes()
}

func TestDecodeToUTF8_PassthroughAndBOM(t *testing.T) {
	ruMarker := "Привет"

	t.Run("valid utf-8 passes through", func(t *testing.T) {
		in := charsetTestDoc(labelUTF8, ruMarker)
		out, err := DecodeToUTF8(in)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(string(out), ruMarker) {
			t.Errorf("marker lost: %.120s", out)
		}
	})

	t.Run("utf-8 BOM is stripped", func(t *testing.T) {
		in := append([]byte{0xEF, 0xBB, 0xBF}, charsetTestDoc(labelUTF8, ruMarker)...)
		out, err := DecodeToUTF8(in)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if bytes.HasPrefix(out, []byte{0xEF, 0xBB, 0xBF}) {
			t.Error("BOM not stripped")
		}
		if !strings.Contains(string(out), ruMarker) {
			t.Errorf("marker lost: %.120s", out)
		}
	})

	t.Run("utf-16le BOM", func(t *testing.T) {
		in := utf16WithBOM(string(charsetTestDoc("utf-16", ruMarker)), true)
		out, err := DecodeToUTF8(in)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(string(out), ruMarker) {
			t.Errorf("marker lost: %.200s", out)
		}
		if strings.Contains(string(out), `encoding="utf-16"`) {
			t.Error("declaration not normalized to utf-8 after transcoding")
		}
	})

	t.Run("utf-16be BOM", func(t *testing.T) {
		in := utf16WithBOM(string(charsetTestDoc("utf-16", ruMarker)), false)
		out, err := DecodeToUTF8(in)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(string(out), ruMarker) {
			t.Errorf("marker lost: %.200s", out)
		}
	})

	t.Run("utf-32le BOM is a typed not-supported error", func(t *testing.T) {
		in := []byte{0xFF, 0xFE, 0x00, 0x00, 0x3C, 0x00, 0x00, 0x00}
		if _, err := DecodeToUTF8(in); !errors.Is(err, ErrUnsupportedCharset) {
			t.Errorf("expected ErrUnsupportedCharset, got %v", err)
		}
	})

	t.Run("utf-32be BOM is a typed not-supported error", func(t *testing.T) {
		in := []byte{0x00, 0x00, 0xFE, 0xFF, 0x00, 0x00, 0x00, 0x3C}
		if _, err := DecodeToUTF8(in); !errors.Is(err, ErrUnsupportedCharset) {
			t.Errorf("expected ErrUnsupportedCharset, got %v", err)
		}
	})

	t.Run("utf-16 without BOM is a typed not-supported error", func(t *testing.T) {
		// Detectable only by the prolog signature, not by null-byte density:
		// Cyrillic UTF-16LE carries 0x04 high bytes, not 0x00.
		in := utf16WithBOM(string(charsetTestDoc("utf-16", ruMarker)), true)
		in = in[2:] // strip the BOM
		if _, err := DecodeToUTF8(in); !errors.Is(err, ErrUnsupportedCharset) {
			t.Errorf("expected ErrUnsupportedCharset, got %v", err)
		}
	})
}

func TestDecodeToUTF8_ValidUTF8DefeatsLyingDeclaration(t *testing.T) {
	ruMarker := "Съешь ещё этих мягких французских булок"

	for _, decl := range []string{labelCP1251, labelLatin1} {
		t.Run(decl, func(t *testing.T) {
			in := charsetTestDoc(decl, ruMarker) // bytes are valid UTF-8
			out, err := DecodeToUTF8(in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(string(out), ruMarker) {
				t.Errorf("valid UTF-8 re-encoded through lying declaration %q: %.160s", decl, out)
			}
		})
	}
}

func TestDecodeToUTF8_DeclaredSingleByteCharsets(t *testing.T) {
	tests := []struct {
		name   string
		decl   string
		marker []byte
		want   string
	}{
		{labelCP1251, labelCP1251, cp1251Privet, "Привет"},
		{labelKOI8R, labelKOI8R, koi8rPrivet, "Привет"},
		{labelLatin5, labelLatin5, latin5Privet, "Привет"},
		{labelLatin1, labelLatin1, latin1Cafe, "café"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := DecodeToUTF8(charsetTestDocEncoded(tt.decl, tt.marker))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(string(out), tt.want) {
				t.Errorf("marker not decoded; got: %.160s", out)
			}
			if !strings.Contains(string(out), `encoding="utf-8"`) {
				t.Errorf("declaration not normalized to utf-8; got: %.80s", out)
			}
		})
	}

	t.Run("declaration with spaces and upper case", func(t *testing.T) {
		var b bytes.Buffer
		b.WriteString(`<?xml version="1.0"   ENCODING = "` + labelCP1251 + `" ?>`)
		b.WriteString(`<FictionBook><body><section><p>`)
		b.Write(cp1251Privet)
		b.WriteString(`</p></section></body></FictionBook>`)
		out, err := DecodeToUTF8(b.Bytes())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(string(out), "Привет") {
			t.Errorf("declaration variant not honored; got: %.160s", out)
		}
	})

	t.Run("declaration with single quotes", func(t *testing.T) {
		var b bytes.Buffer
		b.WriteString(`<?xml version='1.0' encoding='koi8-r'?>`)
		b.WriteString(`<FictionBook><body><section><p>`)
		b.Write(koi8rPrivet)
		b.WriteString(`</p></section></body></FictionBook>`)
		out, err := DecodeToUTF8(b.Bytes())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(string(out), "Привет") {
			t.Errorf("single-quoted declaration not honored; got: %.160s", out)
		}
	})

	t.Run("declared charset over non-xml garbage decodes; the parse stage judges", func(t *testing.T) {
		// The charset stage resolves bytes, not documents: garbage in a
		// declared charset decodes without error, and the "no FictionBook
		// root" verdict belongs to the parse stage, which sees the same
		// sanitized text on every charset path.
		garbage := bytes.Repeat([]byte{0xC0, 0xC1, 0xC2}, 100)
		in := append([]byte(`<?xml version="1.0" encoding="`+labelCP1251+`"?>`), garbage...)
		if _, err := DecodeToUTF8(in); err != nil {
			t.Fatalf("the charset stage must not judge content, got %v", err)
		}
		if _, err := NewFB2Parser(false).Parse(bytes.NewReader(in)); !errors.Is(err, ErrDamagedContent) {
			t.Errorf("expected ErrDamagedContent from the parse stage, got %v", err)
		}
	})
}

func TestDecodeToUTF8_DeclaredUTF8Repair(t *testing.T) {
	// buildBigDoc returns a valid UTF-8 document of at least minLen bytes,
	// with the tail marker past the 64 KiB line.
	buildBigDoc := func() []byte {
		var b strings.Builder
		b.WriteString(`<?xml version="1.0" encoding="utf-8"?>`)
		b.WriteString(`<FictionBook><body><section>`)
		para := "<p>The quick brown fox jumps over the lazy dog. </p>"
		for b.Len() < 100*1024 {
			b.WriteString(para)
		}
		b.WriteString(`<p>ХВОСТОВОЙ МАРКЕР КНИГИ</p>`)
		b.WriteString(`</section></body></FictionBook>`)
		return []byte(b.String())
	}

	t.Run("one corrupt byte beyond 64 KiB is repaired", func(t *testing.T) {
		in := buildBigDoc()
		in[70*1024] = 0xFF // never valid anywhere in UTF-8
		out, err := DecodeToUTF8(in)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !utf8.Valid(out) {
			t.Error("repaired output is not valid UTF-8")
		}
		if !strings.Contains(string(out), "ХВОСТОВОЙ МАРКЕР КНИГИ") {
			t.Error("tail marker lost")
		}
		if !strings.Contains(string(out), replacementCh) {
			t.Error("corrupt byte not replaced with U+FFFD")
		}
	})

	t.Run("widespread damage is a typed error, not a repair", func(t *testing.T) {
		in := buildBigDoc()
		for i := 0; i < 100; i++ {
			in[1024+i*500] = 0xFF
		}
		if _, err := DecodeToUTF8(in); !errors.Is(err, ErrDamagedContent) {
			t.Errorf("expected ErrDamagedContent, got %v", err)
		}
	})

	t.Run("one corrupt byte inside markup is repaired", func(t *testing.T) {
		in := charsetTestDoc(labelUTF8, "Привет")
		// Damage the '<' of the closing root tag: one bad byte inside
		// markup. Local damage is repaired by replacement — a single bad
		// byte must not cost the reader the whole book.
		tail := bytes.LastIndex(in, []byte("</FictionBook>"))
		if tail == -1 {
			t.Fatal("fixture broken: no closing root tag")
		}
		in[tail] = 0xFF
		out, err := DecodeToUTF8(in)
		if err != nil {
			t.Fatalf("one corrupt byte must not refuse the book: %v", err)
		}
		if !utf8.Valid(out) {
			t.Error("repaired output is not valid UTF-8")
		}
		if !strings.Contains(string(out), "Привет") {
			t.Errorf("marker lost: %.160s", out)
		}
		if !strings.Contains(string(out), replacementCh) {
			t.Error("corrupt byte not replaced with U+FFFD")
		}
	})

	t.Run("declared utf-8 garbage is repaired; the parse stage judges", func(t *testing.T) {
		// Within the repair budget the repair branch fixes bytes, even when
		// there is no book here at all; conjuring documents — or refusing
		// non-documents — is the parse stage's call, made on the same
		// sanitized text for every charset.
		in := []byte(`<?xml version="1.0" encoding="utf-8"?>` + "plain text, no markup ")
		in = append(in, 0xFF)
		if _, err := DecodeToUTF8(in); err != nil {
			t.Fatalf("within the corrupt-byte budget the repair branch fixes bytes, got %v", err)
		}
		if _, err := NewFB2Parser(false).Parse(bytes.NewReader(in)); !errors.Is(err, ErrDamagedContent) {
			t.Errorf("expected ErrDamagedContent from the parse stage, got %v", err)
		}
	})

	t.Run("repair budget boundary is exact", func(t *testing.T) {
		at := buildBigDoc()
		for i := 0; i < maxRepairableCorruptBytes; i++ {
			at[1024+i*500] = 0xFF
		}
		if _, err := DecodeToUTF8(at); err != nil {
			t.Errorf("exactly %d corrupt bytes must repair, got %v", maxRepairableCorruptBytes, err)
		}

		over := buildBigDoc()
		for i := 0; i < maxRepairableCorruptBytes+1; i++ {
			over[1024+i*500] = 0xFF
		}
		if _, err := DecodeToUTF8(over); !errors.Is(err, ErrDamagedContent) {
			t.Errorf("%d corrupt bytes must be a typed error, got %v", maxRepairableCorruptBytes+1, err)
		}
	})
}

func TestDecodeToUTF8_RefusalToGuess(t *testing.T) {
	t.Run("undeclared windows-1251 is a typed error", func(t *testing.T) {
		in := charsetTestDocEncoded("", cp1251Privet)
		in = bytes.Replace(in, []byte(` encoding=""`), nil, 1)
		if _, err := DecodeToUTF8(in); !errors.Is(err, ErrUndeclaredCharset) {
			t.Errorf("expected ErrUndeclaredCharset, got %v", err)
		}
	})

	t.Run("undeclared koi8-r is a typed error", func(t *testing.T) {
		var b bytes.Buffer
		b.WriteString(`<FictionBook><body><section><p>`)
		b.Write(koi8rPrivet)
		b.WriteString(`</p></section></body></FictionBook>`)
		if _, err := DecodeToUTF8(b.Bytes()); !errors.Is(err, ErrUndeclaredCharset) {
			t.Errorf("expected ErrUndeclaredCharset, got %v", err)
		}
	})

	t.Run("no prolog and invalid utf-8 is a typed error", func(t *testing.T) {
		in := []byte{'<', '?', 0xC0, 0xC1, '?', '>'}
		if _, err := DecodeToUTF8(in); !errors.Is(err, ErrUndeclaredCharset) {
			t.Errorf("expected ErrUndeclaredCharset, got %v", err)
		}
	})

	t.Run("unsupported declared charset is a distinct typed error", func(t *testing.T) {
		in := charsetTestDocEncoded("cp866", cp1251Privet)
		_, err := DecodeToUTF8(in)
		if !errors.Is(err, ErrUnsupportedDeclaredCharset) {
			t.Errorf("expected ErrUnsupportedDeclaredCharset, got %v", err)
		}
		if errors.Is(err, ErrUndeclaredCharset) {
			t.Error("a present-but-unsupported declaration must not classify as undeclared")
		}
	})
}

func TestDecodeToUTF8_UTF16RepairableDamage(t *testing.T) {
	// A control character, a bare '<' and a bare '&' in the text are locally
	// repairable by the downstream sanitizers. The charset stage must not
	// reject the book before they run: it judges bytes only (BOM, UTF-8
	// validity, the declaration) and never the content — the root verdict
	// belongs to the parse stage.
	doc := `<?xml version="1.0" encoding="utf-16"?>` +
		`<FictionBook><body><section><p>Привет` + "\x01" + ` 2 < 3 & 4</p></section></body></FictionBook>`
	in := utf16WithBOM(doc, true)
	out, err := DecodeToUTF8(in)
	if err != nil {
		t.Fatalf("utf-16 with locally repairable damage must decode, got %v", err)
	}
	if !strings.Contains(string(out), "Привет") {
		t.Errorf("marker lost: %.200s", out)
	}
	if !utf8.Valid(out) {
		t.Error("decoded utf-16 output is not valid UTF-8")
	}
}

func TestDecodeToUTF8_LongDeclaration(t *testing.T) {
	// A prolog longer than any fixed scan window must still be found and
	// normalized: with a cap, the stale declaration survives and the
	// downstream CharsetReader re-encodes already-decoded content.
	longProlog := `<?xml version="1.0" padding="` + strings.Repeat("x", 5000) + `" encoding="windows-1251"?>`

	t.Run("declared single-byte charset past any scan cap", func(t *testing.T) {
		var b bytes.Buffer
		b.WriteString(longProlog)
		b.WriteString(`<FictionBook><body><section><p>`)
		b.Write(cp1251Privet)
		b.WriteString(`</p></section></body></FictionBook>`)
		out, err := DecodeToUTF8(b.Bytes())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(string(out), "Привет") {
			t.Errorf("marker not decoded; got: %.160s", out)
		}
		if !strings.Contains(string(out), `encoding="utf-8"`) {
			t.Errorf("long declaration not normalized to utf-8; got: %.80s", out)
		}
	})

	t.Run("long lying declaration over valid utf-8 is normalized", func(t *testing.T) {
		// Valid UTF-8 content under the long lying prolog.
		var b bytes.Buffer
		b.WriteString(longProlog)
		b.WriteString(`<FictionBook><body><section><p>Привет</p></section></body></FictionBook>`)
		out, err := DecodeToUTF8(b.Bytes())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(string(out), "Привет") {
			t.Errorf("valid UTF-8 damaged through the long lying declaration: %.160s", out)
		}
		if !strings.Contains(string(out), `encoding="utf-8"`) {
			t.Errorf("long lying declaration not normalized; got: %.80s", out)
		}
	})
}

func TestDecodeToUTF8_ShortSingleByteDeclaredUTF8(t *testing.T) {
	// Pinned decision: a short single-byte text lying about being UTF-8
	// stays under the corrupt-byte budget and is REPAIRED (into U+FFFD
	// chains), not refused. The alternative — telling "a few bad bytes in a
	// UTF-8 book" from "a short book in the wrong charset" — would need a
	// ratio heuristic, exactly the apparatus this package removed; the
	// catalog census (88k books) found no such book. Recorded as a known
	// limitation in the phase-1 report.
	in := charsetTestDocEncoded(labelUTF8, cp1251Privet)
	out, err := DecodeToUTF8(in)
	if err != nil {
		t.Fatalf("pinned decision: short misdeclared content is repaired, got %v", err)
	}
	if !utf8.Valid(out) {
		t.Error("repaired output is not valid UTF-8")
	}
	if !strings.Contains(string(out), replacementCh) {
		t.Error("expected the misdeclared bytes to become U+FFFD replacements")
	}
}

func TestDecodeToUTF8_BOMDefeatsDeclaration(t *testing.T) {
	// A UTF-8 BOM is authoritative: the file is UTF-8 no matter what the
	// declaration claims, and the declaration is normalized so downstream
	// never re-decodes.
	in := append([]byte{0xEF, 0xBB, 0xBF}, charsetTestDoc(labelCP1251, "Привет")...)
	out, err := DecodeToUTF8(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out), "Привет") {
		t.Errorf("BOM-marked UTF-8 re-encoded through the contradicting declaration: %.160s", out)
	}
	if !strings.Contains(string(out), `encoding="utf-8"`) {
		t.Errorf("contradicting declaration not normalized; got: %.80s", out)
	}
}

// TestDecodeToUTF8_XML11VersionSameVerdictOnAllPaths pins the ninth-iteration
// fix: the root check must judge only the first element's name, never the
// prolog. encoding/xml rejects version="1.1" even with Strict=false, so a
// token-scanning root check turned prolog damage (which sanitizeXMLVersion
// repairs downstream) into ErrDamagedContent on the checked paths while the
// valid-UTF-8 path passed — the same book downloaded or not depending on its
// charset. All four paths must give the same verdict.
func TestDecodeToUTF8_XML11VersionSameVerdictOnAllPaths(t *testing.T) {
	doc11 := func(decl string) string {
		return `<?xml version="1.1" encoding="` + decl + `"?>` +
			`<FictionBook><body><section><p>Привет</p></section></body></FictionBook>`
	}

	t.Run("valid utf-8", func(t *testing.T) {
		if _, err := DecodeToUTF8([]byte(doc11(labelUTF8))); err != nil {
			t.Fatalf("valid utf-8 path: %v", err)
		}
	})

	t.Run("utf-16 with BOM", func(t *testing.T) {
		if _, err := DecodeToUTF8(utf16WithBOM(doc11("utf-16"), true)); err != nil {
			t.Fatalf("utf-16 path: %v", err)
		}
	})

	t.Run("declared single-byte", func(t *testing.T) {
		var b bytes.Buffer
		b.WriteString(`<?xml version="1.1" encoding="windows-1251"?>`)
		b.WriteString(`<FictionBook><body><section><p>`)
		b.Write(cp1251Privet)
		b.WriteString(`</p></section></body></FictionBook>`)
		if _, err := DecodeToUTF8(b.Bytes()); err != nil {
			t.Fatalf("single-byte path: %v", err)
		}
	})

	t.Run("repaired utf-8", func(t *testing.T) {
		in := append([]byte(doc11(labelUTF8)), 0xFF)
		if _, err := DecodeToUTF8(in); err != nil {
			t.Fatalf("repair path: %v", err)
		}
	})
}

// TestDecodeToUTF8_XMLStylesheetIsNotTheDeclaration pins the name boundary
// after "<?xml": an xml-stylesheet processing instruction is not the
// document's declaration, so its pseudo-attributes neither classify the
// document's charset nor get rewritten by normalization.
func TestDecodeToUTF8_XMLStylesheetIsNotTheDeclaration(t *testing.T) {
	t.Run("stylesheet encoding does not classify the document", func(t *testing.T) {
		var b bytes.Buffer
		b.WriteString(`<?xml-stylesheet type="text/xsl" encoding="windows-1251" href="style.xsl"?>`)
		b.WriteString(`<FictionBook><body><section><p>`)
		b.Write(cp1251Privet)
		b.WriteString(`</p></section></body></FictionBook>`)
		if _, err := DecodeToUTF8(b.Bytes()); !errors.Is(err, ErrUndeclaredCharset) {
			t.Errorf("expected ErrUndeclaredCharset, got %v", err)
		}
	})

	t.Run("stylesheet encoding is never rewritten", func(t *testing.T) {
		in := []byte(`<?xml version="1.0" encoding="utf-8"?>` +
			`<?xml-stylesheet type="text/css" encoding="windows-1251" href="style.css"?>` +
			`<FictionBook><body><section><p>Привет</p></section></body></FictionBook>`)
		out, err := DecodeToUTF8(in)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(string(out), `<?xml-stylesheet type="text/css" encoding="windows-1251" href="style.css"?>`) {
			t.Errorf("stylesheet pseudo-attributes rewritten: %.200s", out)
		}
	})
}

// TestDecodeToUTF8_PrologDamageSameVerdictOnAllPaths keeps the tenth-iteration
// matrix after the twelfth iteration removed the root scan from the charset
// stage entirely: every construct here closes, so every path decodes the
// book. Prolog damage that never closes is covered by
// TestDecodeToUTF8_DamagedPrologAcceptedOnAllPaths — the charset stage no
// longer judges that either. The same book must get the same verdict on all
// four paths: valid UTF-8, UTF-16 with a BOM, a declared single-byte charset,
// and repaired declared UTF-8.
func TestDecodeToUTF8_PrologDamageSameVerdictOnAllPaths(t *testing.T) {
	cases := []struct {
		name   string
		prolog string
	}{
		{"bracket inside a comment in the subset",
			`<!DOCTYPE FictionBook [ <!-- [ --> <!ELEMENT FictionBook ANY> ]>`},
		{"bracket inside a PI in the subset",
			`<!DOCTYPE FictionBook [ <?render mode=[?> <!ELEMENT FictionBook ANY> ]>`},
		// The next two carry a bracket that would force a premature close of
		// the declaration plus an end tag behind it: without the wholesale
		// comment/PI skip, the rescan after the false close meets the end
		// tag and refuses the book.
		{"comment with a closing bracket and an end tag in the subset",
			`<!DOCTYPE FictionBook [ <!-- ]> </foo> --> <!ELEMENT FictionBook ANY> ]>`},
		{"PI with a closing bracket and an end tag in the subset",
			`<!DOCTYPE FictionBook [ <?render ]> </foo> ?> <!ELEMENT FictionBook ANY> ]>`},
		{"doctype without the closing angle bracket",
			`<!DOCTYPE FictionBook [ <!ELEMENT FictionBook ANY> ]`},
		{"control: doctype with a real internal subset",
			`<!DOCTYPE FictionBook [ <!ENTITY badge "<i><b>NEW</b></i>"> <!ELEMENT FictionBook ANY> ]>`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := func(decl string) string {
				return `<?xml version="1.0" encoding="` + decl + `"?>` + tc.prolog +
					`<FictionBook><body><section><p>Привет</p></section></body></FictionBook>`
			}

			t.Run("valid utf-8", func(t *testing.T) {
				if _, err := DecodeToUTF8([]byte(doc(labelUTF8))); err != nil {
					t.Fatalf("valid utf-8 path: %v", err)
				}
			})

			t.Run("utf-16 with BOM", func(t *testing.T) {
				if _, err := DecodeToUTF8(utf16WithBOM(doc("utf-16"), true)); err != nil {
					t.Fatalf("utf-16 path: %v", err)
				}
			})

			t.Run("declared single-byte", func(t *testing.T) {
				var b bytes.Buffer
				b.WriteString(`<?xml version="1.0" encoding="windows-1251"?>`)
				b.WriteString(tc.prolog)
				b.WriteString(`<FictionBook><body><section><p>`)
				b.Write(cp1251Privet)
				b.WriteString(`</p></section></body></FictionBook>`)
				if _, err := DecodeToUTF8(b.Bytes()); err != nil {
					t.Fatalf("single-byte path: %v", err)
				}
			})

			t.Run("repaired utf-8", func(t *testing.T) {
				in := append([]byte(doc(labelUTF8)), 0xFF)
				if _, err := DecodeToUTF8(in); err != nil {
					t.Fatalf("repair path: %v", err)
				}
			})
		})
	}
}

// buildFourPaths encodes one prolog-and-tail combination for all four
// charset paths — valid UTF-8, UTF-16 with a BOM, declared windows-1251, and
// declared UTF-8 with one corrupt byte (the repair path). The tail must
// contain the greeting "Привет" exactly once, so the single-byte path can
// splice in real cp1251 bytes and every path transcodes actual non-ASCII
// content.
func buildFourPaths(t *testing.T, prolog, tail string) map[string][]byte {
	t.Helper()
	const greeting = "Привет"
	parts := strings.SplitN(tail, greeting, 2)
	if len(parts) != 2 {
		t.Fatalf("tail must contain %q exactly once", greeting)
	}
	build := func(decl string) []byte {
		return []byte(`<?xml version="1.0" encoding="` + decl + `"?>` + prolog + tail)
	}
	docs := make(map[string][]byte, 4)
	docs["valid utf-8"] = build(labelUTF8)
	docs["utf-16 with BOM"] = utf16WithBOM(string(build("utf-16")), true)
	docs["repaired utf-8"] = append(build(labelUTF8), 0xFF)
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="windows-1251"?>`)
	b.WriteString(prolog)
	b.WriteString(parts[0])
	b.Write(cp1251Privet)
	b.WriteString(parts[1])
	docs["declared single-byte"] = b.Bytes()
	return docs
}

// runFourPaths reports each charset path's DecodeToUTF8 verdict; nil means
// the path accepted the document.
func runFourPaths(t *testing.T, prolog, tail string) map[string]error {
	t.Helper()
	errs := make(map[string]error, 4)
	for path, doc := range buildFourPaths(t, prolog, tail) {
		_, errs[path] = DecodeToUTF8(doc)
	}
	return errs
}

// parseFourPaths runs the full metadata pipeline — charset resolution,
// sanitizers, XML parse — on one document encoded for each of the four
// charset paths. The root check lives in the parse stage and sees the
// sanitized text, so every path must return the same verdict.
func parseFourPaths(t *testing.T, prolog, tail string) map[string]error {
	t.Helper()
	errs := make(map[string]error, 4)
	for path, doc := range buildFourPaths(t, prolog, tail) {
		_, errs[path] = NewFB2Parser(false).Parse(bytes.NewReader(doc))
	}
	return errs
}

// TestDecodeToUTF8_DamagedPrologAcceptedOnAllPaths pins the twelfth-iteration
// rule: the charset stage does not judge the prolog at all. A construct that
// never closes is the repair pipeline's business — the sanitizers turn a
// stray "<?" into text and expose the root behind it — so one book gets one
// verdict regardless of its charset. All four paths accept; whatever the
// parse stage makes of the sanitized text is uniform by construction, because
// every charset path decodes into the same bytes.
func TestDecodeToUTF8_DamagedPrologAcceptedOnAllPaths(t *testing.T) {
	cases := []struct {
		name   string
		prolog string
	}{
		{"unterminated comment before the root", `<!-- banner without an end`},
		{"unterminated PI before the root", `<?render mode=[`},
		{"unterminated CDATA before the root", `<![CDATA[ banner without an end`},
		{"doctype with an unterminated quote", `<!DOCTYPE FictionBook [ <!ENTITY x "broken> ]`},
	}
	const tail = `<FictionBook><body><section><p>Привет</p></section></body></FictionBook>`
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for path, err := range runFourPaths(t, tc.prolog, tail) {
				if err != nil {
					t.Errorf("%s: the charset stage must not judge the prolog, got %v", path, err)
				}
			}
		})
	}
}

// The hostile-prolog performance pin lives in the converter package as
// TestParseFB2Complete_DamagedPrologPipelineBudget: the guarantee the
// thirteenth iteration needs covers the whole pipeline (charset stage, every
// sanitizer, the decoder and the refusal), not this stage alone.

// TestNormalizeEncodingDecl_ZeroCopyFastPaths pins the common-case cost: the
// valid-UTF-8 majority of the catalog must not pay a whole-file copy (or a
// "?>" hunt) for a declaration that already says utf-8 or is absent.
func TestNormalizeEncodingDecl_ZeroCopyFastPaths(t *testing.T) {
	same := func(in, out []byte) bool {
		return len(in) == len(out) && &in[0] == &out[0]
	}

	t.Run("encoding already utf-8", func(t *testing.T) {
		in := charsetTestDoc(labelUTF8, "Привет")
		if out := normalizeEncodingDecl(in); !same(in, out) {
			t.Error("declaration already utf-8 must be returned without copying")
		}
	})

	t.Run("no declaration at all", func(t *testing.T) {
		in := []byte(`<FictionBook><body><section><p>Привет</p></section></body></FictionBook>`)
		if out := normalizeEncodingDecl(in); !same(in, out) {
			t.Error("declaration-free content must be returned without copying")
		}
	})

	t.Run("stylesheet only is not a declaration", func(t *testing.T) {
		in := []byte(`<?xml-stylesheet encoding="windows-1251" href="s.css"?><FictionBook/>`)
		out := normalizeEncodingDecl(in)
		if !bytes.Equal(out, in) {
			t.Errorf("stylesheet pseudo-attributes rewritten: %.120s", out)
		}
		if !same(in, out) {
			t.Error("content without an XML declaration must pass through untouched")
		}
	})
}
