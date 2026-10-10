package llmres

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
)

// The excerpt reader authornorm-context-v1 is a pure function from FB2 entry
// bytes to a bounded excerpt: the charset resolver of the parser decides the
// encoding, markup is stripped, whitespace collapses, truncation cuts at word
// boundaries, and the SHA-256 over the canonical payload is deterministic.

const testFB2 = `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description>
<title-info>
<author><first-name>Thilliez,</first-name><last-name>Franck</last-name></author>
<author><first-name>Чужой</first-name><last-name>Автор</last-name></author>
<translator><first-name>Нора</first-name><last-name>Галь</last-name></translator>
<book-title>Гатака</book-title>
<annotation><p>Первая строка аннотации.</p><p>Вторая строка с <emphasis>разметкой</emphasis> внутри.</p></annotation>
</title-info>
<publish-info><book-name>Гатака</book-name><publisher>АСТ</publisher><city>Москва</city><year>MMXI</year></publish-info>
</description>
<body><section><title><p>Глава 1</p></title><p>Тело книги начинается здесь.</p><image href="#pic.png"/></section></body>
<binary id="pic.png">iVBORw0KGgo=</binary>
</FictionBook>`

func buildContextOrFail(t *testing.T, fb2 []byte, exclude string) BookContext {
	t.Helper()
	ctx, err := BuildContext(fb2, exclude)
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}
	return ctx
}

func TestBuildContextFields(t *testing.T) {
	ctx := buildContextOrFail(t, []byte(testFB2), "Thilliez, Franck")

	if ctx.Title != "Гатака" {
		t.Errorf("title = %q", ctx.Title)
	}
	// The author being processed is excluded; other credits keep their roles.
	if len(ctx.OtherCredits) != 2 {
		t.Fatalf("other credits = %v", ctx.OtherCredits)
	}
	if ctx.OtherCredits[0] != (Credit{Role: "author", Name: "Чужой Автор"}) {
		t.Errorf("credit[0] = %+v", ctx.OtherCredits[0])
	}
	if ctx.OtherCredits[1] != (Credit{Role: "translator", Name: "Нора Галь"}) {
		t.Errorf("credit[1] = %+v", ctx.OtherCredits[1])
	}
	if got := ctx.Annotation; got != "Первая строка аннотации. Вторая строка с разметкой внутри." {
		t.Errorf("annotation = %q", got)
	}
	if ctx.PublishInfo != "Гатака, АСТ, Москва, MMXI" {
		t.Errorf("publish info = %q", ctx.PublishInfo)
	}
	if !strings.HasPrefix(ctx.BodyStart, "Глава 1 Тело книги начинается здесь.") {
		t.Errorf("body start = %q", ctx.BodyStart)
	}
	if strings.Contains(ctx.BodyStart, "iVBORw0KGgo") {
		t.Error("binary content leaked into the excerpt")
	}
}

func TestBuildContextKeepsTheAuthorItselfWhenNotExcluded(t *testing.T) {
	ctx := buildContextOrFail(t, []byte(testFB2), "")
	if len(ctx.OtherCredits) != 3 {
		t.Fatalf("other credits = %v", ctx.OtherCredits)
	}
}

func TestBuildContextDeterministicSHA(t *testing.T) {
	a := buildContextOrFail(t, []byte(testFB2), "Thilliez, Franck")
	b := buildContextOrFail(t, []byte(testFB2), "Thilliez, Franck")
	if a.SHA256() != b.SHA256() {
		t.Fatal("same bytes must give the same SHA-256")
	}
	other := buildContextOrFail(t, []byte(testFB2), "")
	if other.SHA256() == a.SHA256() {
		t.Fatal("different excerpts must give different SHA-256")
	}
}

func TestBuildContextTruncatesAtWordBoundary(t *testing.T) {
	longParagraph := strings.Repeat("слово ", 600) // 3600 runes of body text
	fb2 := `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description><title-info><author><last-name>Калугина</last-name></author><book-title>Т</book-title></title-info></description>
<body><section><p>` + longParagraph + `</p></section></body>
</FictionBook>`
	ctx := buildContextOrFail(t, []byte(fb2), "")
	if n := runeLen(ctx.BodyStart); n > BodyStartMaxRunes {
		t.Fatalf("body start = %d runes, cap is %d", n, BodyStartMaxRunes)
	}
	if strings.HasSuffix(ctx.BodyStart, "сло") {
		t.Fatal("truncation must cut at a word boundary, not mid-word")
	}
	if !strings.HasSuffix(ctx.BodyStart, "слово") {
		t.Fatalf("body start should end at a full word, got tail %q", tail(ctx.BodyStart, 10))
	}
}

func TestBuildContextCapsEveryField(t *testing.T) {
	long := strings.Repeat("абвгд ", 400)
	fb2 := `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description><title-info>
<author><last-name>Калугина</last-name></author>
<book-title>` + long + `</book-title>
<annotation><p>` + long + `</p></annotation>
</title-info>
<publish-info><publisher>` + long + `</publisher></publish-info>
</description>
<body><p>` + long + `</p></body>
</FictionBook>`
	ctx := buildContextOrFail(t, []byte(fb2), "")
	if n := runeLen(ctx.Title); n > TitleMaxRunes {
		t.Errorf("title = %d runes, cap %d", n, TitleMaxRunes)
	}
	if n := runeLen(ctx.Annotation); n > AnnotationMaxRunes {
		t.Errorf("annotation = %d runes, cap %d", n, AnnotationMaxRunes)
	}
	if n := runeLen(ctx.PublishInfo); n > PublishInfoMaxRunes {
		t.Errorf("publish info = %d runes, cap %d", n, PublishInfoMaxRunes)
	}
	if n := ctx.TotalLen(); n > TotalMaxRunes {
		t.Errorf("total = %d runes, cap %d", n, TotalMaxRunes)
	}
}

func TestBuildContextEncodings(t *testing.T) {
	// Fixtures of internal/converter cover every charset the resolver admits.
	tests := []struct {
		fixture   string
		wantBody  string // substring expected in the decoded body text
		wantError bool
	}{
		{"encoding_cp1251.fb2", "Съешь", false},
		{"encoding_koi8r.fb2", "Съешь", false},
		{"encoding_iso8859_5.fb2", "Съешь", false},
		{"encoding_utf8_bom.fb2", "Съешь", false},
		{"encoding_utf16le_bom.fb2", "Съешь", false},
		{"encoding_utf16be_bom.fb2", "Съешь", false},
		{"encoding_utf8_declared_cp1251.fb2", "Съешь", false},
		{"encoding_cp1251_nodecl.fb2", "", true}, // undeclared, invalid UTF-8
		{"encoding_koi8r_nodecl.fb2", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			raw, err := os.ReadFile("../../converter/testdata/" + tt.fixture)
			if err != nil {
				t.Fatal(err)
			}
			ctx, err := BuildContext(raw, "")
			if tt.wantError {
				if err == nil {
					t.Fatalf("BuildContext(%s) succeeded, want error", tt.fixture)
				}
				return
			}
			if err != nil {
				t.Fatalf("BuildContext(%s): %v", tt.fixture, err)
			}
			if ctx.Title != "Encoding Test" {
				t.Errorf("title = %q", ctx.Title)
			}
			if !strings.Contains(ctx.BodyStart, tt.wantBody) {
				t.Errorf("body start %q lacks %q", ctx.BodyStart, tt.wantBody)
			}
		})
	}
}

func TestBuildContextGarbage(t *testing.T) {
	if _, err := BuildContext([]byte("not xml at all"), ""); err == nil {
		t.Fatal("garbage must fail, not panic or succeed")
	}
	if _, err := BuildContext(nil, ""); err == nil {
		t.Fatal("empty input must fail")
	}
}

func TestContextRelevant(t *testing.T) {
	ctx := buildContextOrFail(t, []byte(testFB2), "")

	// A non-initial source token appearing as a whole word makes the excerpt
	// relevant, case and ё folding aside.
	tokens := Tokenize([]FieldValue{{FieldFirst, "галь"}})
	if !ContextRelevant(&ctx, tokens) {
		t.Error("translator surname in the excerpt must make it relevant")
	}

	// Case/ё folding: the excerpt has "здесь", the token says "Здесь".
	tokensFold := Tokenize([]FieldValue{{FieldFirst, "ЗДЕСЬ"}})
	if !ContextRelevant(&ctx, tokensFold) {
		t.Error("case folding must match ЗДЕСЬ against здесь")
	}

	irrelevant := Tokenize([]FieldValue{{FieldFirst, "Несуществующий"}, {FieldLast, "Персонаж"}})
	if ContextRelevant(&ctx, irrelevant) {
		t.Error("no source token in the excerpt must be irrelevant")
	}

	// Initials never make an excerpt relevant, even when the initial letter
	// appears in the excerpt as a standalone word.
	const fb2WithInitial = `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description><title-info><book-title>Г</book-title></title-info></description>
<body><p>Г удивлённо посмотрел вокруг.</p></body>
</FictionBook>`
	ctxInitial := buildContextOrFail(t, []byte(fb2WithInitial), "")
	initials := Tokenize([]FieldValue{{FieldFirst, "Г."}})
	if ContextRelevant(&ctxInitial, initials) {
		t.Error("an initial must not make the excerpt relevant")
	}

	// A substring inside a longer word is not a whole-word hit.
	substring := Tokenize([]FieldValue{{FieldFirst, "ание"}})
	if ContextRelevant(&ctx, substring) {
		t.Error("substring inside a word must not count")
	}
}

// creditHeavyFB2 is the reviewer's blocking scenario: a valid FB2 with a
// contributor list long enough that title, annotation, publish-info and the
// credit names together exceed the total excerpt budget by more than the body
// length, followed by a non-empty first body.
func creditHeavyFB2() string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description><title-info>`)
	// 120 translators x ~40 runes of name: the credits alone approach the
	// total budget of 3600 runes.
	for i := 0; i < 120; i++ {
		b.WriteString("<translator><first-name>Contributor")
		b.WriteString(strings.Repeat("n", 20))
		b.WriteString("</first-name><last-name>Surname")
		b.WriteString(strings.Repeat("v", 10))
		b.WriteString("</last-name></translator>")
	}
	b.WriteString(`<book-title>Название книги</book-title>
<annotation><p>Короткая аннотация к книге.</p></annotation>
</title-info>
<publish-info><publisher>АСТ</publisher></publish-info>
</description>
<body><p>Текст первой главы.</p></body>
</FictionBook>`)
	return b.String()
}

func TestBuildContextCreditsOverflowBodyBudget(t *testing.T) {
	// Regression: the body budget must never go negative; the elastic body
	// empties and the credits drop from the tail instead of panicking.
	ctx := buildContextOrFail(t, []byte(creditHeavyFB2()), "")
	if n := ctx.TotalLen(); n > TotalMaxRunes {
		t.Fatalf("total = %d runes, cap %d", n, TotalMaxRunes)
	}
	if ctx.BodyStart != "" {
		t.Errorf("body must yield to the fixed fields and credits, got %q", tail(ctx.BodyStart, 20))
	}
	if len(ctx.OtherCredits) == 120 {
		t.Error("the overlong credits list must be dropped from the tail")
	}
}

func TestBuildContextSkipsAnnotationImage(t *testing.T) {
	// An image element before the first body — inside the annotation — is
	// skipped like any other image or binary element; its text never leaks
	// into the excerpt.
	fb2 := `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description><title-info>
<book-title>Т</book-title>
<annotation><p>До картинки <image href="#a.png">HOSTILE PAYLOAD</image> после картинки.</p></annotation>
</title-info></description>
<body><p>Тело.</p></body>
</FictionBook>`
	ctx := buildContextOrFail(t, []byte(fb2), "")
	if strings.Contains(ctx.Annotation, "HOSTILE") {
		t.Errorf("annotation image text leaked into the excerpt: %q", ctx.Annotation)
	}
	if ctx.Annotation != "До картинки после картинки." {
		t.Errorf("annotation = %q", ctx.Annotation)
	}
}

func TestTruncateAtWordOverlongFirstWord(t *testing.T) {
	// Property: a first word longer than a cap never yields a partial word —
	// word-boundary truncation returns nothing instead.
	caps := []int{TitleMaxRunes, AnnotationMaxRunes, PublishInfoMaxRunes, BodyStartMaxRunes}
	for _, cap := range caps {
		overlong := strings.Repeat("ж", cap+13)
		if got := truncateAtWord(overlong, cap); got != "" {
			t.Errorf("truncateAtWord(overlong word, %d) = %d runes, want empty", cap, runeLen(got))
		}
		if got := truncateAtWord(overlong+" хвост", cap); got != "" {
			t.Errorf("truncateAtWord(overlong word + tail, %d) = %q, want empty", cap, tail(got, 10))
		}
	}
	// A boundary inside the cap still cuts there.
	if got := truncateAtWord("два слова здесь", 9); got != "два" {
		t.Errorf("truncateAtWord boundary cut = %q, want %q", got, "два")
	}

	// The same holds end to end: a book-title of one overlong word leaves an
	// empty title rather than a fragment.
	fb2 := `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description><title-info><book-title>` + strings.Repeat("ж", TitleMaxRunes+13) + `</book-title></title-info></description>
<body><p>Тело.</p></body>
</FictionBook>`
	ctx := buildContextOrFail(t, []byte(fb2), "")
	if ctx.Title != "" {
		t.Errorf("title = %q, want empty", tail(ctx.Title, 10))
	}
}

func runeLen(s string) int { return len([]rune(s)) }

func tail(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

// Encoding a fixture in a single-byte charset exercises the resolver through
// the same path as a production archive entry.
func encodeCP1251(t *testing.T, utf8 string) []byte {
	t.Helper()
	out, err := charmap.Windows1251.NewEncoder().String(utf8)
	if err != nil {
		t.Fatal(err)
	}
	return []byte(out)
}

func TestBuildContextCP1251Declared(t *testing.T) {
	fb2 := `<?xml version="1.0" encoding="windows-1251"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description><title-info><author><last-name>Калугина</last-name></author><book-title>Белое солнце</book-title></title-info></description>
<body><p>Тело письмом windows-1251.</p></body>
</FictionBook>`
	ctx := buildContextOrFail(t, encodeCP1251(t, fb2), "")
	if ctx.Title != "Белое солнце" {
		t.Errorf("title = %q", ctx.Title)
	}
	if !strings.Contains(ctx.BodyStart, "Тело письмом windows-1251.") {
		t.Errorf("body = %q", ctx.BodyStart)
	}
}
