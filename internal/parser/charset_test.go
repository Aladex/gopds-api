package parser

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"gopds-api/internal/authornorm"
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

// Markers for the Western, Central-European, Greek, Turkish, Baltic and
// Ukrainian charsets, produced the same way as the literals above — iconv, an
// encoder independent of the x/text charmaps under test:
//
//	printf '%s' "L’expresso — 5 €" | iconv -f UTF-8 -t WINDOWS-1252 | xxd -p  # 4c92657870726573736f209720352080
//	printf '%s' "Łódź i źrebię"    | iconv -f UTF-8 -t WINDOWS-1250 | xxd -p  # a3f3649f2069209f72656269ea
//	printf '%s' "Αθήνα Ελλάδα"     | iconv -f UTF-8 -t WINDOWS-1253 | xxd -p  # c1e8deede120c5ebebdce4e1
//	printf '%s' "İstanbul gün"     | iconv -f UTF-8 -t WINDOWS-1254 | xxd -p  # dd7374616e62756c2067fc6e
//	printf '%s' "Lietuvos ūris"    | iconv -f UTF-8 -t WINDOWS-1257 | xxd -p  # 4c69657475766f7320fb726973
//	printf '%s' "Čeština a ľudí"   | iconv -f UTF-8 -t ISO-8859-2   | xxd -p  # c865b974696e61206120b57564ed
//	printf '%s' "€ Švédsko œuf"    | iconv -f UTF-8 -t ISO-8859-15  | xxd -p  # a420a676e964736b6f20bd7566
//	printf '%s' "Їжачок і їжак"    | iconv -f UTF-8 -t KOI8-U       | xxd -p  # b7d6c1decfcb20a620a7d6c1cb
var (
	cp1252Marker = []byte{0x4c, 0x92, 0x65, 0x78, 0x70, 0x72, 0x65, 0x73, 0x73, 0x6f, 0x20, 0x97, 0x20, 0x35, 0x20, 0x80}
	cp1250Marker = []byte{0xa3, 0xf3, 0x64, 0x9f, 0x20, 0x69, 0x20, 0x9f, 0x72, 0x65, 0x62, 0x69, 0xea}
	cp1253Marker = []byte{0xc1, 0xe8, 0xde, 0xed, 0xe1, 0x20, 0xc5, 0xeb, 0xeb, 0xdc, 0xe4, 0xe1}
	cp1254Marker = []byte{0xdd, 0x73, 0x74, 0x61, 0x6e, 0x62, 0x75, 0x6c, 0x20, 0x67, 0xfc, 0x6e}
	cp1257Marker = []byte{0x4c, 0x69, 0x65, 0x74, 0x75, 0x76, 0x6f, 0x73, 0x20, 0xfb, 0x72, 0x69, 0x73}
	latin2Marker = []byte{0xc8, 0x65, 0xb9, 0x74, 0x69, 0x6e, 0x61, 0x20, 0x61, 0x20, 0xb5, 0x75, 0x64, 0xed}
	latin9Marker = []byte{0xa4, 0x20, 0xa6, 0x76, 0xe9, 0x64, 0x73, 0x6b, 0x6f, 0x20, 0xbd, 0x75, 0x66}
	koi8uMarker  = []byte{0xb7, 0xd6, 0xc1, 0xde, 0xcf, 0xcb, 0x20, 0xa6, 0x20, 0xa7, 0xd6, 0xc1, 0xcb}
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

// The single-byte charsets FB2 in the wild declares beyond the Cyrillic set:
// windows-1252 (the measured 2% refusal of the first prod pilot — Western
// European books), 1250/1257 (Central European and Baltic), 1253/1254 (Greek,
// Turkish), the ISO counterparts 8859-2/8859-15, and koi8-u (Ukrainian; the
// XML decoder's charset reader already knew it, the byte decoder refused it).
func TestDecodeToUTF8_DeclaredWesternCharsets(t *testing.T) {
	tests := []struct {
		name   string
		decl   string
		marker []byte
		want   string
	}{
		{labelCP1252, labelCP1252, cp1252Marker, "L’expresso — 5 €"},
		{labelCP1250, labelCP1250, cp1250Marker, "Łódź i źrebię"},
		{labelCP1253, labelCP1253, cp1253Marker, "Αθήνα Ελλάδα"},
		{labelCP1254, labelCP1254, cp1254Marker, "İstanbul gün"},
		{labelCP1257, labelCP1257, cp1257Marker, "Lietuvos ūris"},
		{labelLatin2, labelLatin2, latin2Marker, "Čeština a ľudí"},
		{labelLatin9, labelLatin9, latin9Marker, "€ Švédsko œuf"},
		{labelKOI8U, labelKOI8U, koi8uMarker, "Їжачок і їжак"},
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
}

// Every alias of every new charset resolves to the same decoder, and the
// declaration stays case-insensitive end to end.
func TestDecodeToUTF8_WesternCharsetAliases(t *testing.T) {
	tests := []struct {
		label  string
		marker []byte
		want   string
	}{
		{labelCP1252, cp1252Marker, "L’expresso — 5 €"},
		{labelCP1252AliasFlat, cp1252Marker, "L’expresso — 5 €"},
		{labelCP1252AliasDash, cp1252Marker, "L’expresso — 5 €"},
		{"WINDOWS-1252", cp1252Marker, "L’expresso — 5 €"},
		{labelCP1250AliasFlat, cp1250Marker, "Łódź i źrebię"},
		{labelCP1250AliasDash, cp1250Marker, "Łódź i źrebię"},
		{labelCP1253AliasFlat, cp1253Marker, "Αθήνα Ελλάδα"},
		{labelCP1253AliasDash, cp1253Marker, "Αθήνα Ελλάδα"},
		{labelCP1254AliasFlat, cp1254Marker, "İstanbul gün"},
		{labelCP1254AliasDash, cp1254Marker, "İstanbul gün"},
		{labelCP1257AliasFlat, cp1257Marker, "Lietuvos ūris"},
		{labelCP1257AliasDash, cp1257Marker, "Lietuvos ūris"},
		{labelLatin2Alias, latin2Marker, "Čeština a ľudí"},
		{labelLatin2AliasFlat, latin2Marker, "Čeština a ľudí"},
		{labelLatin9Alias, latin9Marker, "€ Švédsko œuf"},
		{labelLatin9AliasFlat, latin9Marker, "€ Švédsko œuf"},
		{labelKOI8UAlias, koi8uMarker, "Їжачок і їжак"},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			out, err := DecodeToUTF8(charsetTestDocEncoded(tt.label, tt.marker))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(string(out), tt.want) {
				t.Errorf("alias not honored; got: %.160s", out)
			}
		})
	}
}

// windows-1252 and iso-8859-1 agree on 0xA0–0xFF and differ exactly in
// 0x80–0x9F (€ ‚ ƒ „ … † ‡ ˆ ‰ Š ‹ Œ Ž ' ' " " • – — ˜ ™ š › œ ž Ÿ).
// Every book of the prod pilot's refused sample carried bytes there — em
// dashes, apostrophes, ellipses — so treating 1252 as latin1 would corrupt
// precisely the books this change exists to accept.
func TestDecodeToUTF8_Windows1252IsNotLatin1(t *testing.T) {
	out, err := DecodeToUTF8(charsetTestDocEncoded(labelCP1252, []byte{0x80, 0x92, 0x85, 0x97}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := string(out)
	for _, want := range []string{"€", "’", "…", "—"} {
		if !strings.Contains(body, want) {
			t.Errorf("byte in the 0x80-0x9F range did not decode as windows-1252; want %q in: %.120s", want, out)
		}
	}
	for _, control := range []string{"\u0092", "\u0085", "\u0097", "\u0080"} {
		if strings.Contains(body, control) {
			t.Errorf("latin1-style C1 control rune %q decoded; the charmap is not windows-1252", control)
		}
	}
}

// The label set stays closed: a declaration this build cannot name is a typed
// refusal, not an open label lookup. The refused ones here are real encodings
// a label lookup would happily decode — accepting one is a decision that
// needs measured need behind it, like windows-1252 had.
func TestDecodeToUTF8_UnmeasuredCharsetsStayRefused(t *testing.T) {
	for _, label := range []string{"windows-1255", "windows-1256", "windows-874", "shift_jis", "gb18030", "euc-kr", "utf-7", "macintosh"} {
		t.Run(label, func(t *testing.T) {
			// A high byte keeps the content off the valid-UTF-8 path, so the
			// declaration is actually consulted.
			if _, err := DecodeToUTF8(charsetTestDocEncoded(label, []byte{0xe9})); !errors.Is(err, ErrUnsupportedDeclaredCharset) {
				t.Errorf("error = %v, want ErrUnsupportedDeclaredCharset", err)
			}
		})
	}
}

// The XML decoder's charset reader and the byte decoder must resolve labels
// through the same closed table: koi8-u was once known to one and refused by
// the other, and a future caller feeding raw bytes straight to the decoder
// would hit whatever disagreement crept back. Every label of the table,
// through both doors, decodes the same bytes to the same text.
func TestMakeCharsetReaderUsesTheSharedTable(t *testing.T) {
	table := []struct {
		label  string
		marker []byte
		want   string
	}{
		{labelCP1251, cp1251Privet, "Привет"},
		{labelKOI8R, koi8rPrivet, "Привет"},
		{labelLatin5, latin5Privet, "Привет"},
		{labelLatin1, latin1Cafe, "café"},
		{labelKOI8U, koi8uMarker, "Їжачок і їжак"},
		{labelCP1252, cp1252Marker, "L’expresso — 5 €"},
		{labelCP1250, cp1250Marker, "Łódź i źrebię"},
		{labelCP1253, cp1253Marker, "Αθήνα Ελλάδα"},
		{labelCP1254, cp1254Marker, "İstanbul gün"},
		{labelCP1257, cp1257Marker, "Lietuvos ūris"},
		{labelLatin2, latin2Marker, "Čeština a ľudí"},
		{labelLatin9, latin9Marker, "€ Švédsko œuf"},
		// The corrected ISO tables must serve both doors: the C1 byte the
		// library would have replaced with U+FFFD comes out as U+0085 here too.
		{labelLatin2, []byte{0x41, 0x85, 0x42}, "A\u0085B"},
		{labelLatin9, []byte{0x41, 0x85, 0x42}, "A\u0085B"},
	}
	for _, tt := range table {
		t.Run(tt.label, func(t *testing.T) {
			reader, err := makeCharsetReader(tt.label, bytes.NewReader(tt.marker))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			out, err := io.ReadAll(reader)
			if err != nil {
				t.Fatalf("read failed: %v", err)
			}
			if string(out) != tt.want {
				t.Errorf("reader table disagrees with the byte decoder; got %q, want %q", out, tt.want)
			}
		})
	}
}

// The full-byte oracle: every supported single-byte charset, all 256 input
// bytes, each decoded rune checked against an independent mapping. The
// expected tables were generated from system codecs (Python's cp125x,
// iso8859-x, koi8-r, latin-1) with two deliberate reconciliations, noted
// where they apply:
//
//   - koi8-u 0xAE/0xBE follow the WHATWG index x/text uses (U+045E/U+040E);
//     RFC 2319 and system codecs keep box-drawing there. The WHATWG choice
//     is the accepted behavior — this is not byte-for-byte agreement with
//     every KOI8-U codec.
//   - iso-8859-5 keeps the library's U+FFFD for 0x80-0x9F: it was supported
//     before this table with exactly that behavior, and previously accepted
//     input must stay byte-identical.
//
// For the windows pages, positions the standards leave undefined decode to
// U+FFFD (the library's replacement), which both the system codecs and the
// x/text tables agree on.
var fullByteOracle = map[string]string{
	"windows-1251": "\u0000\u0001\u0002\u0003\u0004\u0005\u0006\u0007\u0008\u0009\u000A\u000B\u000C\u000D\u000E\u000F" +
		"\u0010\u0011\u0012\u0013\u0014\u0015\u0016\u0017\u0018\u0019\u001A\u001B\u001C\u001D\u001E\u001F" +
		" !\u0022#$%&'()*+,-./" +
		"0123456789:;<=>?" +
		"@ABCDEFGHIJKLMNO" +
		"PQRSTUVWXYZ[\u005C]^_" +
		"`abcdefghijklmno" +
		"pqrstuvwxyz{|}~\u007F" +
		"ЂЃ‚ѓ„…†‡€‰Љ‹ЊЌЋЏ" +
		"ђ‘’“”•–—�™љ›њќћџ" +
		" ЎўЈ¤Ґ¦§Ё©Є«¬\u00AD®Ї" +
		"°±Ііґµ¶·ё№є»јЅѕї" +
		"АБВГДЕЖЗИЙКЛМНОП" +
		"РСТУФХЦЧШЩЪЫЬЭЮЯ" +
		"абвгдежзийклмноп" +
		"рстуфхцчшщъыьэюя",
	"koi8-r": "\u0000\u0001\u0002\u0003\u0004\u0005\u0006\u0007\u0008\u0009\u000A\u000B\u000C\u000D\u000E\u000F" +
		"\u0010\u0011\u0012\u0013\u0014\u0015\u0016\u0017\u0018\u0019\u001A\u001B\u001C\u001D\u001E\u001F" +
		" !\u0022#$%&'()*+,-./" +
		"0123456789:;<=>?" +
		"@ABCDEFGHIJKLMNO" +
		"PQRSTUVWXYZ[\u005C]^_" +
		"`abcdefghijklmno" +
		"pqrstuvwxyz{|}~\u007F" +
		"─│┌┐└┘├┤┬┴┼▀▄█▌▐" +
		"░▒▓⌠■∙√≈≤≥ ⌡°²·÷" +
		"═║╒ё╓╔╕╖╗╘╙╚╛╜╝╞" +
		"╟╠╡Ё╢╣╤╥╦╧╨╩╪╫╬©" +
		"юабцдефгхийклмно" +
		"пярстужвьызшэщчъ" +
		"ЮАБЦДЕФГХИЙКЛМНО" +
		"ПЯРСТУЖВЬЫЗШЭЩЧЪ",
	"iso-8859-5": "\u0000\u0001\u0002\u0003\u0004\u0005\u0006\u0007\u0008\u0009\u000A\u000B\u000C\u000D\u000E\u000F" +
		"\u0010\u0011\u0012\u0013\u0014\u0015\u0016\u0017\u0018\u0019\u001A\u001B\u001C\u001D\u001E\u001F" +
		" !\u0022#$%&'()*+,-./" +
		"0123456789:;<=>?" +
		"@ABCDEFGHIJKLMNO" +
		"PQRSTUVWXYZ[\u005C]^_" +
		"`abcdefghijklmno" +
		"pqrstuvwxyz{|}~\u007F" +
		"����������������" +
		"����������������" +
		" ЁЂЃЄЅІЇЈЉЊЋЌ\u00ADЎЏ" +
		"АБВГДЕЖЗИЙКЛМНОП" +
		"РСТУФХЦЧШЩЪЫЬЭЮЯ" +
		"абвгдежзийклмноп" +
		"рстуфхцчшщъыьэюя" +
		"№ёђѓєѕіїјљњћќ§ўџ",
	"iso-8859-1": "\u0000\u0001\u0002\u0003\u0004\u0005\u0006\u0007\u0008\u0009\u000A\u000B\u000C\u000D\u000E\u000F" +
		"\u0010\u0011\u0012\u0013\u0014\u0015\u0016\u0017\u0018\u0019\u001A\u001B\u001C\u001D\u001E\u001F" +
		" !\u0022#$%&'()*+,-./" +
		"0123456789:;<=>?" +
		"@ABCDEFGHIJKLMNO" +
		"PQRSTUVWXYZ[\u005C]^_" +
		"`abcdefghijklmno" +
		"pqrstuvwxyz{|}~\u007F" +
		"\u0080\u0081\u0082\u0083\u0084\u0085\u0086\u0087\u0088\u0089\u008A\u008B\u008C\u008D\u008E\u008F" +
		"\u0090\u0091\u0092\u0093\u0094\u0095\u0096\u0097\u0098\u0099\u009A\u009B\u009C\u009D\u009E\u009F" +
		" ¡¢£¤¥¦§¨©ª«¬\u00AD®¯" +
		"°±²³´µ¶·¸¹º»¼½¾¿" +
		"ÀÁÂÃÄÅÆÇÈÉÊËÌÍÎÏ" +
		"ÐÑÒÓÔÕÖ×ØÙÚÛÜÝÞß" +
		"àáâãäåæçèéêëìíîï" +
		"ðñòóôõö÷øùúûüýþÿ",
	"koi8-u": "\u0000\u0001\u0002\u0003\u0004\u0005\u0006\u0007\u0008\u0009\u000A\u000B\u000C\u000D\u000E\u000F" +
		"\u0010\u0011\u0012\u0013\u0014\u0015\u0016\u0017\u0018\u0019\u001A\u001B\u001C\u001D\u001E\u001F" +
		" !\u0022#$%&'()*+,-./" +
		"0123456789:;<=>?" +
		"@ABCDEFGHIJKLMNO" +
		"PQRSTUVWXYZ[\u005C]^_" +
		"`abcdefghijklmno" +
		"pqrstuvwxyz{|}~\u007F" +
		"─│┌┐└┘├┤┬┴┼▀▄█▌▐" +
		"░▒▓⌠■∙√≈≤≥ ⌡°²·÷" +
		"═║╒ёє╔ії╗╘╙╚╛ґў╞" +
		"╟╠╡ЁЄ╣ІЇ╦╧╨╩╪ҐЎ©" +
		"юабцдефгхийклмно" +
		"пярстужвьызшэщчъ" +
		"ЮАБЦДЕФГХИЙКЛМНО" +
		"ПЯРСТУЖВЬЫЗШЭЩЧЪ",
	"windows-1250": "\u0000\u0001\u0002\u0003\u0004\u0005\u0006\u0007\u0008\u0009\u000A\u000B\u000C\u000D\u000E\u000F" +
		"\u0010\u0011\u0012\u0013\u0014\u0015\u0016\u0017\u0018\u0019\u001A\u001B\u001C\u001D\u001E\u001F" +
		" !\u0022#$%&'()*+,-./" +
		"0123456789:;<=>?" +
		"@ABCDEFGHIJKLMNO" +
		"PQRSTUVWXYZ[\u005C]^_" +
		"`abcdefghijklmno" +
		"pqrstuvwxyz{|}~\u007F" +
		"€�‚�„…†‡�‰Š‹ŚŤŽŹ" +
		"�‘’“”•–—�™š›śťžź" +
		" ˇ˘Ł¤Ą¦§¨©Ş«¬\u00AD®Ż" +
		"°±˛ł´µ¶·¸ąş»Ľ˝ľż" +
		"ŔÁÂĂÄĹĆÇČÉĘËĚÍÎĎ" +
		"ĐŃŇÓÔŐÖ×ŘŮÚŰÜÝŢß" +
		"ŕáâăäĺćçčéęëěíîď" +
		"đńňóôőö÷řůúűüýţ˙",
	"windows-1252": "\u0000\u0001\u0002\u0003\u0004\u0005\u0006\u0007\u0008\u0009\u000A\u000B\u000C\u000D\u000E\u000F" +
		"\u0010\u0011\u0012\u0013\u0014\u0015\u0016\u0017\u0018\u0019\u001A\u001B\u001C\u001D\u001E\u001F" +
		" !\u0022#$%&'()*+,-./" +
		"0123456789:;<=>?" +
		"@ABCDEFGHIJKLMNO" +
		"PQRSTUVWXYZ[\u005C]^_" +
		"`abcdefghijklmno" +
		"pqrstuvwxyz{|}~\u007F" +
		"€�‚ƒ„…†‡ˆ‰Š‹Œ�Ž�" +
		"�‘’“”•–—˜™š›œ�žŸ" +
		" ¡¢£¤¥¦§¨©ª«¬\u00AD®¯" +
		"°±²³´µ¶·¸¹º»¼½¾¿" +
		"ÀÁÂÃÄÅÆÇÈÉÊËÌÍÎÏ" +
		"ÐÑÒÓÔÕÖ×ØÙÚÛÜÝÞß" +
		"àáâãäåæçèéêëìíîï" +
		"ðñòóôõö÷øùúûüýþÿ",
	"windows-1253": "\u0000\u0001\u0002\u0003\u0004\u0005\u0006\u0007\u0008\u0009\u000A\u000B\u000C\u000D\u000E\u000F" +
		"\u0010\u0011\u0012\u0013\u0014\u0015\u0016\u0017\u0018\u0019\u001A\u001B\u001C\u001D\u001E\u001F" +
		" !\u0022#$%&'()*+,-./" +
		"0123456789:;<=>?" +
		"@ABCDEFGHIJKLMNO" +
		"PQRSTUVWXYZ[\u005C]^_" +
		"`abcdefghijklmno" +
		"pqrstuvwxyz{|}~\u007F" +
		"€�‚ƒ„…†‡�‰�‹����" +
		"�‘’“”•–—�™�›����" +
		" ΅Ά£¤¥¦§¨©�«¬\u00AD®―" +
		"°±²³΄µ¶·ΈΉΊ»Ό½ΎΏ" +
		"ΐΑΒΓΔΕΖΗΘΙΚΛΜΝΞΟ" +
		"ΠΡ�ΣΤΥΦΧΨΩΪΫάέήί" +
		"ΰαβγδεζηθικλμνξο" +
		"πρςστυφχψωϊϋόύώ�",
	"windows-1254": "\u0000\u0001\u0002\u0003\u0004\u0005\u0006\u0007\u0008\u0009\u000A\u000B\u000C\u000D\u000E\u000F" +
		"\u0010\u0011\u0012\u0013\u0014\u0015\u0016\u0017\u0018\u0019\u001A\u001B\u001C\u001D\u001E\u001F" +
		" !\u0022#$%&'()*+,-./" +
		"0123456789:;<=>?" +
		"@ABCDEFGHIJKLMNO" +
		"PQRSTUVWXYZ[\u005C]^_" +
		"`abcdefghijklmno" +
		"pqrstuvwxyz{|}~\u007F" +
		"€�‚ƒ„…†‡ˆ‰Š‹Œ���" +
		"�‘’“”•–—˜™š›œ��Ÿ" +
		" ¡¢£¤¥¦§¨©ª«¬\u00AD®¯" +
		"°±²³´µ¶·¸¹º»¼½¾¿" +
		"ÀÁÂÃÄÅÆÇÈÉÊËÌÍÎÏ" +
		"ĞÑÒÓÔÕÖ×ØÙÚÛÜİŞß" +
		"àáâãäåæçèéêëìíîï" +
		"ğñòóôõö÷øùúûüışÿ",
	"windows-1257": "\u0000\u0001\u0002\u0003\u0004\u0005\u0006\u0007\u0008\u0009\u000A\u000B\u000C\u000D\u000E\u000F" +
		"\u0010\u0011\u0012\u0013\u0014\u0015\u0016\u0017\u0018\u0019\u001A\u001B\u001C\u001D\u001E\u001F" +
		" !\u0022#$%&'()*+,-./" +
		"0123456789:;<=>?" +
		"@ABCDEFGHIJKLMNO" +
		"PQRSTUVWXYZ[\u005C]^_" +
		"`abcdefghijklmno" +
		"pqrstuvwxyz{|}~\u007F" +
		"€�‚�„…†‡�‰�‹�¨ˇ¸" +
		"�‘’“”•–—�™�›�¯˛�" +
		" �¢£¤�¦§Ø©Ŗ«¬\u00AD®Æ" +
		"°±²³´µ¶·ø¹ŗ»¼½¾æ" +
		"ĄĮĀĆÄÅĘĒČÉŹĖĢĶĪĻ" +
		"ŠŃŅÓŌÕÖ×ŲŁŚŪÜŻŽß" +
		"ąįāćäåęēčéźėģķīļ" +
		"šńņóōõö÷ųłśūüżž˙",
	"iso-8859-2": "\u0000\u0001\u0002\u0003\u0004\u0005\u0006\u0007\u0008\u0009\u000A\u000B\u000C\u000D\u000E\u000F" +
		"\u0010\u0011\u0012\u0013\u0014\u0015\u0016\u0017\u0018\u0019\u001A\u001B\u001C\u001D\u001E\u001F" +
		" !\u0022#$%&'()*+,-./" +
		"0123456789:;<=>?" +
		"@ABCDEFGHIJKLMNO" +
		"PQRSTUVWXYZ[\u005C]^_" +
		"`abcdefghijklmno" +
		"pqrstuvwxyz{|}~\u007F" +
		"\u0080\u0081\u0082\u0083\u0084\u0085\u0086\u0087\u0088\u0089\u008A\u008B\u008C\u008D\u008E\u008F" +
		"\u0090\u0091\u0092\u0093\u0094\u0095\u0096\u0097\u0098\u0099\u009A\u009B\u009C\u009D\u009E\u009F" +
		" Ą˘Ł¤ĽŚ§¨ŠŞŤŹ\u00ADŽŻ" +
		"°ą˛ł´ľśˇ¸šşťź˝žż" +
		"ŔÁÂĂÄĹĆÇČÉĘËĚÍÎĎ" +
		"ĐŃŇÓÔŐÖ×ŘŮÚŰÜÝŢß" +
		"ŕáâăäĺćçčéęëěíîď" +
		"đńňóôőö÷řůúűüýţ˙",
	"iso-8859-15": "\u0000\u0001\u0002\u0003\u0004\u0005\u0006\u0007\u0008\u0009\u000A\u000B\u000C\u000D\u000E\u000F" +
		"\u0010\u0011\u0012\u0013\u0014\u0015\u0016\u0017\u0018\u0019\u001A\u001B\u001C\u001D\u001E\u001F" +
		" !\u0022#$%&'()*+,-./" +
		"0123456789:;<=>?" +
		"@ABCDEFGHIJKLMNO" +
		"PQRSTUVWXYZ[\u005C]^_" +
		"`abcdefghijklmno" +
		"pqrstuvwxyz{|}~\u007F" +
		"\u0080\u0081\u0082\u0083\u0084\u0085\u0086\u0087\u0088\u0089\u008A\u008B\u008C\u008D\u008E\u008F" +
		"\u0090\u0091\u0092\u0093\u0094\u0095\u0096\u0097\u0098\u0099\u009A\u009B\u009C\u009D\u009E\u009F" +
		" ¡¢£€¥Š§š©ª«¬\u00AD®¯" +
		"°±²³Žµ¶·ž¹º»ŒœŸ¿" +
		"ÀÁÂÃÄÅÆÇÈÉÊËÌÍÎÏ" +
		"ÐÑÒÓÔÕÖ×ØÙÚÛÜÝÞß" +
		"àáâãäåæçèéêëìíîï" +
		"ðñòóôõö÷øùúûüýþÿ",
}

// TestDecodeToUTF8_FullByteOracle drives the whole 0x00-0xFF byte range
// through DecodeToUTF8 for every supported single-byte label and requires
// each byte's rune to match the independent table. The C1 range is where
// x/text's ISO-8859-2/-15 tables substitute U+FFFD for the U+0080-U+009F
// the standards define; this oracle is what forced the corrected tables.
func TestDecodeToUTF8_FullByteOracle(t *testing.T) {
	for label, expected := range fullByteOracle {
		t.Run(label, func(t *testing.T) {
			var body []byte
			for b := 0; b < 256; b++ {
				body = append(body, byte(b))
			}
			doc := append([]byte(`<?xml version="1.0" encoding="`+label+`"?><p>`), body...)
			doc = append(doc, []byte(`</p>`)...)
			out, err := DecodeToUTF8(doc)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			start := bytes.Index(out, []byte("<p>")) + len("<p>")
			end := bytes.Index(out, []byte("</p>"))
			got := []rune(string(out[start:end]))
			want := []rune(expected)
			if len(got) != len(want) {
				t.Fatalf("decoded %d runes, want %d", len(got), len(want))
			}
			for i, r := range want {
				if got[i] != r {
					t.Errorf("byte 0x%02X decoded to U+%04X, want U+%04X", i, got[i], r)
				}
			}
		})
	}
}

// A defined C1 control character inside a title or a name survives
// extraction as itself: the source-field contract stores what the file said,
// and an ISO page's 0x85 IS U+0085 — not damage to be replaced. This is the
// production shape of the B1 finding, at the level that credits the source.
func TestExtractISOControlCharactersSurvive(t *testing.T) {
	for _, label := range []string{labelLatin2, labelLatin9} {
		t.Run(label, func(t *testing.T) {
			// PLACEHOLDER keeps the raw byte out of the source itself; the
			// spliced single 0x85 makes the document genuine ISO-8859 bytes.
			doc := []byte(`<?xml version="1.0" encoding="` + label + `"?>` +
				`<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">` +
				`<description><title-info>` +
				`<author><first-name>A` + "PLACEHOLDER" + `B</first-name><last-name>Test</last-name></author>` +
				`<book-title>A` + "PLACEHOLDER" + `B</book-title>` +
				`</title-info></description>` +
				`<body><section><p>texte</p></section></body></FictionBook>`)
			raw := bytes.ReplaceAll(doc, []byte("PLACEHOLDER"), []byte{0x85})
			md, err := extractBytes(t, bytes.NewReader(raw), "", 1<<20)
			if err != nil {
				t.Fatalf("Extract error: %v", err)
			}
			if md.Outcome != authornorm.OutcomeExtracted {
				t.Fatalf("outcome = %q, want extracted", md.Outcome)
			}
			const want = "A\u0085B"
			if md.Title == nil || *md.Title != want {
				t.Errorf("title = %q, want %q", titleText(md.Title), want)
			}
			if len(md.Contributors) != 1 {
				t.Fatalf("contributors = %d, want 1", len(md.Contributors))
			}
			if got := md.Contributors[0].Value.First(); got == nil || *got != want {
				t.Errorf("first name = %v, want %q", got, want)
			}
		})
	}
}
