package fb2sanitize

import (
	"strings"
	"testing"
)

// TestApply_RunsTheWholeChain feeds one document carrying a specimen of
// every one of the nine steps through the public entry point and checks the
// exact combined result: dropping any single step changes the output.
//
// The fixture also pins every known order dependency between steps that do
// not commute. The dependency map comes from a differential survey (every
// transposition of two steps against corpus-mutation fuzzing) plus manual
// analysis; each dependency below has a specimen the corresponding swap
// breaks:
//
//   - controlChars before brokenSelfClosingTags — "<empty-line/\x01<p>":
//     the control byte must become a space so the repair can see "/ <".
//   - invalidTagOpenings before brokenSelfClosingTags — "<empty-line/<3":
//     the stray "<" is escaped first and starves the repair.
//   - invalidProcessingInstructions before brokenSelfClosingTags —
//     "<empty-line/<?j>": the stray "<?" is escaped first, same shape.
//   - brokenEndTags before brokenLangTag — "<lang ru</lang x<p>": the end
//     tag must be completed to "</lang>" before the lang repair looks for it.
//   - brokenSelfClosingTags before brokenLangTag — `<lang a="c"/</lang>`:
//     the self-closing repair inserts ">" inside the lang segment, which
//     rightly vetoes the lang rewrite.
//
// Pairs with no specimen here commute on every input the survey and analysis
// could produce, with one deliberate exception: xmlVersion's 200-byte
// declaration window creates a length-cliff dependency with the escape steps,
// pinned separately in TestApply_DeclarationWindowMeasuredAfterEscapes.
func TestApply_RunsTheWholeChain(t *testing.T) {
	in := `<?xml version="1.1" encoding="utf-8"?>` + "\n" +
		`<FictionBook xmlns:xlink="http://www.w3.org/1999/xlink"><body><section>` +
		"<p>a\x01b &nbsp; 2 < 3</p>" +
		`<p>q<?pi?>w</p>` +
		`<empty-line/<p>x</p>` +
		`<p>s<empty-line/<3</p>` +
		"<p>t<empty-line/\x01<p>z</p>" +
		`<p>u<empty-line/<?j>v</p>` +
		`<p>y</p <lang ru</lang>` +
		`<lang ru</lang x<p>` +
		`<lang a="c"/</lang>` +
		`<image l:href="#c"/>` +
		`</section></body></FictionBook>`
	want := `<?xml version="1.0" encoding="utf-8"?>` + "\n" +
		`<FictionBook xmlns:xlink="http://www.w3.org/1999/xlink"><body><section>` +
		`<p>a b &amp;nbsp; 2 &lt; 3</p>` +
		`<p>q&lt;?pi?>w</p>` +
		`<empty-line/><p>x</p>` +
		`<p>s<empty-line/&lt;3</p>` +
		`<p>t<empty-line/><p>z</p>` +
		`<p>u<empty-line/&lt;?j>v</p>` +
		`<p>y</p> <lang> ru</lang>` +
		`<lang> ru</lang> x<p>` +
		`<lang a="c"/></lang>` +
		`<image xlink:href="#c"/>` +
		`</section></body></FictionBook>`

	got := string(Apply([]byte(in)))
	if got != want {
		t.Errorf("Apply mismatch:\n got %q\nwant %q", got, want)
	}
}

// TestApply_DeclarationWindowMeasuredAfterEscapes pins the one dependency
// class the main fixture cannot carry: xmlVersion only repairs a declaration
// whose "?>" lies within the first 200 bytes, and that window is measured
// after the escape steps have run. An ampersand escape inside a declaration
// sitting near the cap pushes "?>" past it, and the version stays unrepaired
// — the behavior both historical pipelines shared, pinned deliberately. The
// mirrored constructions (repairs after xmlVersion lengthening a declaration
// that contains broken markup junk) exist only for pathological declarations
// and are documented in the phase report rather than pinned.
func TestApply_DeclarationWindowMeasuredAfterEscapes(t *testing.T) {
	// Raw "?>" at offset 197 (within the 200-byte window); the escape grows
	// "&" to "&amp;" and moves it to 201, so the version must stay 1.1.
	in := `<?xml version="1.1" ` + strings.Repeat("a", 176) + `&?><FictionBook/>`
	want := `<?xml version="1.1" ` + strings.Repeat("a", 176) + `&amp;?><FictionBook/>`

	got := string(Apply([]byte(in)))
	if got != want {
		t.Errorf("Apply mismatch:\n got %q\nwant %q", got, want)
	}
}

// TestInvalidAmpersands_EmptyNumericReferenceQuirk pins byte-for-byte
// compatibility of the ported scanner: the historical implementation
// accepted a bare "&#;" numeric reference (its digit loop over an empty span
// vacuously passed), so the escape must keep leaving it untouched. "&#x;"
// was always invalid and must keep being escaped.
func TestInvalidAmpersands_EmptyNumericReferenceQuirk(t *testing.T) {
	if !isValidEntity([]byte("#")) {
		t.Error(`isValidEntity("#") = false; the historical scanner accepted the bare "&#;" reference`)
	}
	if isValidEntity([]byte("#x")) {
		t.Error(`isValidEntity("#x") = true; an empty hex reference was always invalid`)
	}
	in := `<p>a&#;b &#x;c</p>`
	want := `<p>a&#;b &amp;#x;c</p>`
	if got := string(invalidAmpersands([]byte(in))); got != want {
		t.Errorf("invalidAmpersands(%q)\n got %q\nwant %q", in, got, want)
	}
}

// TestApply_CleanContentPassesThrough pins the no-damage path: intact bytes
// come back byte-identical.
func TestApply_CleanContentPassesThrough(t *testing.T) {
	in := `<?xml version="1.0" encoding="utf-8"?>` + "\n" +
		`<FictionBook><body><section><p>Привет &amp; мир</p></section></body></FictionBook>`
	got := string(Apply([]byte(in)))
	if got != in {
		t.Errorf("Apply changed clean content:\n got %q\nwant %q", got, in)
	}
}

// TestBrokenSelfClosingTags_KeepsProse pins the difference between a tag
// that lost its bracket and a slash that is simply part of the text. The
// scanner used to rewrite every "/ <" in the document, which put a literal
// "/>" into annotations the catalog then showed to readers.
func TestBrokenSelfClosingTags_KeepsProse(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "slash before a tag is prose",
			in:   `<p>Цена 100 руб. / <emphasis>скидка</emphasis> есть</p>`,
			want: `<p>Цена 100 руб. / <emphasis>скидка</emphasis> есть</p>`,
		},
		{
			name: "slash before a newline and a tag is prose",
			in:   "<p>Первая строка /\n<emphasis>вторая</emphasis></p>",
			want: "<p>Первая строка /\n<emphasis>вторая</emphasis></p>",
		},
		{
			name: "slash glued to a tag is prose",
			in:   `<p>а/<emphasis>б</emphasis></p>`,
			want: `<p>а/<emphasis>б</emphasis></p>`,
		},
		{
			// Only the missing bracket is the damage. The closing tag that
			// follows is usually a real boundary, and swallowing it turned two
			// sibling sections into a nested pair.
			name: "a tag that lost its bracket is repaired",
			in:   `<empty-line/</p>`,
			want: `<empty-line/></p>`,
		},
		{
			name: "a section boundary survives the repair",
			in:   `<section><image l:href="#i"/</section><section/>`,
			want: `<section><image l:href="#i"/></section><section/>`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(brokenSelfClosingTags([]byte(tc.in)))
			if got != tc.want {
				t.Errorf("brokenSelfClosingTags(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestBrokenSelfClosingTags_Repairs covers the repair patterns themselves.
func TestBrokenSelfClosingTags_Repairs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			// The closing tag is kept: only the missing bracket was damage.
			// Swallowing </section> merged two sibling sections into a nested
			// pair, which rewrote the book rather than repairing it.
			name: "broken image tag with section closing",
			in:   `<image xlink:href="#img1" /</section>`,
			want: `<image xlink:href="#img1" /></section>`,
		},
		{
			name: "broken self-closing with space before tag",
			in:   `<empty-line / <p>text</p>`,
			want: `<empty-line /><p>text</p>`,
		},
		{
			name: "broken self-closing with newline",
			in:   "<image href=\"#img1\" /\n<section>",
			want: `<image href="#img1" /><section>`,
		},
		{
			name: "normal self-closing tags should not change",
			in:   `<image href="#img1" /><br/>`,
			want: `<image href="#img1" /><br/>`,
		},
		{
			// An attribute assignment marks a tag that was probably
			// self-closing even when its name is not on the whitelist. This
			// shape used to be missed by one copy of the scanner, so the
			// repair covered only four tag names on one of the two paths.
			name: "attribute-carrying tag off the whitelist is repaired",
			in:   `<sequence name="Серия" number="7" /</title-info>`,
			want: `<sequence name="Серия" number="7" /></title-info>`,
		},
		{
			// No attributes and not on the whitelist: not enough evidence
			// that the slash belonged to a tag, so the bytes stay untouched.
			name: "bare tag off the whitelist is left alone",
			in:   `<coverpage /</description>`,
			want: `<coverpage /</description>`,
		},
		{
			// "<foo=bar" is not an attribute assignment: the shape requires
			// whitespace, a name, then '='.
			name: "equals glued to the name is not an attribute",
			in:   `<foo=bar /</p>`,
			want: `<foo=bar /</p>`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(brokenSelfClosingTags([]byte(tc.in)))
			if got != tc.want {
				t.Errorf("brokenSelfClosingTags(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}
