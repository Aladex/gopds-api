package parser

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"

	"gopds-api/internal/authornorm"
)

const testExtractorVersion = "extractor-test-v1"

func fixtureBytes(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "metadata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func extractBytes(t *testing.T, r io.Reader, knownMD5 string, limit int64) (authornorm.SourceMetadata, error) {
	t.Helper()
	ex := MetadataExtractor{MetadataMaxBytes: limit, ExtractorVersion: testExtractorVersion}
	return ex.Extract(ExtractBookInput{
		Reader:      r,
		BookID:      42,
		ArchivePath: "lib/001.zip",
		EntryName:   "book.fb2",
		KnownMD5:    knownMD5,
	})
}

func extractFixture(t *testing.T, name, knownMD5 string) authornorm.SourceMetadata {
	t.Helper()
	b := fixtureBytes(t, name)
	md, err := extractBytes(t, bytes.NewReader(b), knownMD5, 1<<20)
	if err != nil {
		t.Fatalf("Extract(%s) error: %v", name, err)
	}
	return md
}

func knownMD5Of(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

func descriptionEndOffset(b []byte) int64 {
	i := bytes.Index(b, []byte("</description>"))
	if i == -1 {
		return -1
	}
	return int64(i + len("</description>"))
}

// countingReader records how many bytes were actually pulled from the source.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func assertContributor(t *testing.T, c *authornorm.Contributor, role authornorm.ContributorRole, position int,
	first, middle, last, nickname *string, display string) {
	t.Helper()
	if c.Role != role {
		t.Errorf("role = %q, want %q", c.Role, role)
	}
	if c.Position != position {
		t.Errorf("position = %d, want %d", c.Position, position)
	}
	fields := []struct {
		name string
		got  *string
		want *string
	}{
		{"first", c.Value.First(), first},
		{"middle", c.Value.Middle(), middle},
		{"last", c.Value.Last(), last},
		{"nickname", c.Value.Nickname(), nickname},
	}
	for _, f := range fields {
		if (f.got == nil) != (f.want == nil) {
			t.Errorf("%s nil-ness mismatch: got %v, want %v", f.name, f.got, f.want)
			continue
		}
		if f.got != nil && *f.got != *f.want {
			t.Errorf("%s = %q, want %q", f.name, *f.got, *f.want)
		}
	}
	if c.Value.DisplayName() != display {
		t.Errorf("display = %q, want %q", c.Value.DisplayName(), display)
	}
}

func strp(s string) *string { return &s }

func TestExtractMultiContributor(t *testing.T) {
	md := extractFixture(t, "multi_contributor.fb2", "")
	if md.Outcome != authornorm.OutcomeExtracted {
		t.Fatalf("outcome = %q, want %q", md.Outcome, authornorm.OutcomeExtracted)
	}
	if len(md.Contributors) != 4 {
		t.Fatalf("contributors = %d, want 4 (whitespace-only author must be skipped)", len(md.Contributors))
	}
	assertContributor(t, &md.Contributors[0], authornorm.RoleAuthor, 0,
		strp("Иван"), nil, strp("Петров"), strp("ivanp"), "Иван Петров ivanp")
	if md.Contributors[0].SourceID == nil || *md.Contributors[0].SourceID != "au-1" {
		t.Errorf("author 0 SourceID = %v, want %q", md.Contributors[0].SourceID, "au-1")
	}
	assertContributor(t, &md.Contributors[1], authornorm.RoleAuthor, 1,
		strp("Анна"), strp("Сергеевна"), strp("Сидорова"), nil, "Анна Сергеевна Сидорова")
	if md.Contributors[1].SourceID != nil {
		t.Errorf("author 1 SourceID = %v, want nil (attribute absent)", md.Contributors[1].SourceID)
	}
	// Present-but-empty first-name stays a non-nil pointer to "" and is
	// distinguishable from an absent element.
	assertContributor(t, &md.Contributors[2], authornorm.RoleAuthor, 2,
		strp(""), nil, strp("Пустов"), nil, "Пустов")
	assertContributor(t, &md.Contributors[3], authornorm.RoleTranslator, 0,
		strp("John"), nil, strp("Smith"), nil, "John Smith")

	if md.Title != "Метаданные тест" {
		t.Errorf("title = %q", md.Title)
	}
	if md.Lang == nil || *md.Lang != "ru" {
		t.Errorf("lang = %v, want %q", md.Lang, "ru")
	}
	if md.SrcLang == nil || *md.SrcLang != "en" {
		t.Errorf("src-lang = %v, want %q", md.SrcLang, "en")
	}
	if md.DocumentID == nil || *md.DocumentID != "doc-multi" {
		t.Errorf("document id = %v, want %q", md.DocumentID, "doc-multi")
	}
	if md.DocumentVersion == nil || *md.DocumentVersion != "1" {
		t.Errorf("document version = %v, want %q", md.DocumentVersion, "1")
	}
	if err := md.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestExtractChildOrder(t *testing.T) {
	md := extractFixture(t, "child_order.fb2", "")
	if len(md.Contributors) != 2 {
		t.Fatalf("contributors = %d, want 2", len(md.Contributors))
	}
	// Display keeps the original XML child order; no cultural reordering.
	assertContributor(t, &md.Contributors[0], authornorm.RoleAuthor, 0,
		strp("Олег"), nil, strp("Соколов"), nil, "Соколов Олег")
	assertContributor(t, &md.Contributors[1], authornorm.RoleAuthor, 1,
		strp("Вера"), nil, strp("Лисина"), strp("fox"), "fox Вера Лисина")
}

func TestExtractDocumentInfoAuthorExcluded(t *testing.T) {
	md := extractFixture(t, "docinfo_author.fb2", "")
	if len(md.Contributors) != 1 {
		t.Fatalf("contributors = %d, want 1: document-info/author must never create a credit", len(md.Contributors))
	}
	assertContributor(t, &md.Contributors[0], authornorm.RoleAuthor, 0,
		strp("Мария"), nil, strp("Книгина"), nil, "Мария Книгина")
	if md.DocumentID == nil || *md.DocumentID != "doc-1" {
		t.Errorf("document id = %v, want %q", md.DocumentID, "doc-1")
	}
	if md.DocumentVersion == nil || *md.DocumentVersion != "2.5" {
		t.Errorf("document version = %v, want %q", md.DocumentVersion, "2.5")
	}
}

func TestExtractTranslatorOnlyIsNoAuthor(t *testing.T) {
	md := extractFixture(t, "translator_only.fb2", "")
	if md.Outcome != authornorm.OutcomeNoAuthor {
		t.Fatalf("outcome = %q, want %q (translator credits do not count as authors)",
			md.Outcome, authornorm.OutcomeNoAuthor)
	}
	if len(md.Contributors) != 1 {
		t.Fatalf("contributors = %d, want 1 (translator credit kept)", len(md.Contributors))
	}
	assertContributor(t, &md.Contributors[0], authornorm.RoleTranslator, 0,
		strp("Ольга"), nil, strp("Переводская"), nil, "Ольга Переводская")
}

func TestExtractEmptyAuthorsCreateNoCredits(t *testing.T) {
	md := extractFixture(t, "empty_authors.fb2", "")
	if len(md.Contributors) != 0 {
		t.Fatalf("contributors = %d, want 0", len(md.Contributors))
	}
	if md.Outcome != authornorm.OutcomeNoAuthor {
		t.Fatalf("outcome = %q, want %q", md.Outcome, authornorm.OutcomeNoAuthor)
	}
	if err := md.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestExtractSequencesAndPublishInfo(t *testing.T) {
	md := extractFixture(t, "sequences_publish.fb2", "")
	wantSeqs := []authornorm.Sequence{
		{Index: 0, Parent: -1, Name: "Миры", Number: strp("2")},
		{Index: 1, Parent: 0, Name: "Хроники", Number: nil},
		{Index: 2, Parent: -1, Name: "Миры", Number: strp("3")},
		{Index: 3, Parent: -1, Name: "Серия издателя", Number: nil},
	}
	if len(md.Sequences) != len(wantSeqs) {
		t.Fatalf("sequences = %d, want %d", len(md.Sequences), len(wantSeqs))
	}
	for i, want := range wantSeqs {
		got := md.Sequences[i]
		if got.Index != want.Index || got.Parent != want.Parent || got.Name != want.Name {
			t.Errorf("sequence %d = %+v, want %+v", i, got, want)
		}
		if (got.Number == nil) != (want.Number == nil) {
			t.Errorf("sequence %d number nil-ness mismatch: got %v, want %v", i, got.Number, want.Number)
		} else if got.Number != nil && *got.Number != *want.Number {
			t.Errorf("sequence %d number = %q, want %q", i, *got.Number, *want.Number)
		}
	}
	if len(md.ISBNs) != 2 || md.ISBNs[0] != "978-5-17-1" || md.ISBNs[1] != "5-17-02" {
		t.Errorf("isbns = %v, want raw list of two", md.ISBNs)
	}
	if len(md.Publisher) != 1 || md.Publisher[0] != "АСТ" {
		t.Errorf("publisher = %v", md.Publisher)
	}
	if len(md.City) != 1 || md.City[0] != "Москва" {
		t.Errorf("city = %v", md.City)
	}
	if len(md.Year) != 1 || md.Year[0] != "2019" {
		t.Errorf("year = %v", md.Year)
	}
	if md.DocumentID == nil || *md.DocumentID != "doc-seq" {
		t.Errorf("document id = %v", md.DocumentID)
	}
	if md.DocumentVersion == nil || *md.DocumentVersion != "3" {
		t.Errorf("document version = %v", md.DocumentVersion)
	}
}

func TestExtractNamespacesByLocalName(t *testing.T) {
	for _, name := range []string{"ns_default.fb2", "ns_prefixed.fb2", "ns_none.fb2"} {
		t.Run(name, func(t *testing.T) {
			md := extractFixture(t, name, "")
			if md.Outcome != authornorm.OutcomeExtracted {
				t.Fatalf("outcome = %q, want extracted", md.Outcome)
			}
			if len(md.Contributors) != 1 {
				t.Fatalf("contributors = %d, want 1", len(md.Contributors))
			}
			if got := md.Contributors[0].Value.First(); got == nil || *got != "Нс" {
				t.Errorf("first = %v, want %q", got, "Нс")
			}
			if md.Title == "" {
				t.Error("title is empty")
			}
		})
	}
}

func TestExtractEncodings(t *testing.T) {
	cyrillic := struct{ first, middle, last, title string }{
		"Фёдор", "Михайлович", "Достоевский", "Братья Карамазовы",
	}
	cases := []struct {
		fixture             string
		first, middle, last *string
		title               string
	}{
		{"enc_utf8.fb2", strp(cyrillic.first), strp(cyrillic.middle), strp(cyrillic.last), cyrillic.title},
		{"enc_utf8_bom.fb2", strp(cyrillic.first), strp(cyrillic.middle), strp(cyrillic.last), cyrillic.title},
		{"enc_utf16le_bom.fb2", strp(cyrillic.first), strp(cyrillic.middle), strp(cyrillic.last), cyrillic.title},
		{"enc_utf16be_bom.fb2", strp(cyrillic.first), strp(cyrillic.middle), strp(cyrillic.last), cyrillic.title},
		{"enc_cp1251.fb2", strp(cyrillic.first), strp(cyrillic.middle), strp(cyrillic.last), cyrillic.title},
		{"enc_koi8r.fb2", strp(cyrillic.first), strp(cyrillic.middle), strp(cyrillic.last), cyrillic.title},
		{"enc_latin1.fb2", strp("André"), nil, strp("Müller"), "Café"},
		{"enc_declared_utf8_damaged.fb2", strp(cyrillic.first), strp(cyrillic.middle), strp(cyrillic.last), ""},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			md := extractFixture(t, tc.fixture, "")
			if md.Outcome != authornorm.OutcomeExtracted {
				t.Fatalf("outcome = %q, want extracted", md.Outcome)
			}
			if len(md.Contributors) != 1 {
				t.Fatalf("contributors = %d, want 1", len(md.Contributors))
			}
			c := md.Contributors[0]
			assertContributor(t, &c, authornorm.RoleAuthor, 0, tc.first, tc.middle, tc.last, nil, c.Value.DisplayName())
			if tc.title != "" && md.Title != tc.title {
				t.Errorf("title = %q, want %q", md.Title, tc.title)
			}
			if tc.fixture == "enc_declared_utf8_damaged.fb2" && !strings.ContainsRune(md.Title, '�') {
				t.Errorf("damaged fixture: title = %q, want replacement rune inside", md.Title)
			}
		})
	}
}

func TestExtractEncodingErrors(t *testing.T) {
	cases := []struct {
		fixture string
		want    error
	}{
		{"enc_utf32le_bom.fb2", ErrUnsupportedCharset},
		{"enc_declared_utf16_no_bom.fb2", ErrUnsupportedCharset},
		{"enc_undeclared_bad.fb2", ErrUndeclaredCharset},
		{"enc_unsupported_declared.fb2", ErrUnsupportedDeclaredCharset},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			b := fixtureBytes(t, tc.fixture)
			_, err := extractBytes(t, bytes.NewReader(b), "", 1<<20)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Extract error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestExtractLimitBoundary(t *testing.T) {
	b := fixtureBytes(t, "limit.fb2")
	n := descriptionEndOffset(b)
	if n <= 0 {
		t.Fatal("fixture has no </description>")
	}
	known := knownMD5Of(b)

	// Exactly metadata_max_bytes is allowed.
	if _, err := extractBytes(t, bytes.NewReader(b), known, n); err != nil {
		t.Errorf("limit = N (known MD5): error = %v, want nil", err)
	}
	// The limit bounds only the metadata phase: with an unknown MD5 the body
	// is drained past the limit into the hash and the run still succeeds.
	md, err := extractBytes(t, bytes.NewReader(b), "", n)
	if err != nil {
		t.Errorf("limit = N (computed MD5): error = %v, want nil", err)
	} else if md.BookMD5 != known {
		t.Errorf("limit = N (computed MD5): md5 = %q, want %q", md.BookMD5, known)
	}

	// The first byte past the limit fails.
	_, err = extractBytes(t, bytes.NewReader(b), known, n-1)
	if !errors.Is(err, ErrMetadataLimit) {
		t.Errorf("limit = N-1: error = %v, want ErrMetadataLimit", err)
	}
}

func TestExtractKnownMD5ReadsNoBodyBytes(t *testing.T) {
	b := fixtureBytes(t, "body_guard.fb2")
	n := descriptionEndOffset(b)
	if n <= 0 {
		t.Fatal("fixture has no </description>")
	}
	known := knownMD5Of(b)

	cr := &countingReader{r: bytes.NewReader(b)}
	md, err := extractBytes(t, cr, known, 1<<20)
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}
	if cr.n != n {
		t.Errorf("bytes read = %d, want exactly %d (end of </description>): no body/binary byte may be read", cr.n, n)
	}
	if md.BookMD5 != known {
		t.Errorf("md5 = %q, want echo of known %q", md.BookMD5, known)
	}
	if len(md.Contributors) != 1 {
		t.Fatalf("contributors = %d, want 1", len(md.Contributors))
	}
	// The annotation subtree is skipped: its nested name-like elements never
	// become components of any credit.
	assertContributor(t, &md.Contributors[0], authornorm.RoleAuthor, 0,
		strp("Страж"), nil, strp("Телев"), nil, "Страж Телев")
	if md.Title != "Телохранитель" {
		t.Errorf("title = %q, want %q", md.Title, "Телохранитель")
	}
}

func TestExtractUnknownMD5DrainsBodyIntoHashOnly(t *testing.T) {
	b := fixtureBytes(t, "body_guard.fb2")
	known := knownMD5Of(b)

	cr := &countingReader{r: bytes.NewReader(b)}
	md, err := extractBytes(t, cr, "", 1<<20)
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}
	if cr.n != int64(len(b)) {
		t.Errorf("bytes read = %d, want full entry %d (drain into hash)", cr.n, len(b))
	}
	if md.BookMD5 != known {
		t.Errorf("computed md5 = %q, want %q", md.BookMD5, known)
	}
	// The extraction payload is identical to the known-MD5 run: the body is
	// hashed, never parsed or accumulated.
	knownRun := extractFixture(t, "body_guard.fb2", known)
	if md.Title != knownRun.Title || len(md.Contributors) != len(knownRun.Contributors) ||
		len(md.Sequences) != len(knownRun.Sequences) || md.Outcome != knownRun.Outcome {
		t.Errorf("payload differs from known-MD5 run: %+v vs %+v", md, knownRun)
	}
}

func TestExtractMalformedXMLAfterRoot(t *testing.T) {
	b := fixtureBytes(t, "malformed.fb2")
	_, err := extractBytes(t, bytes.NewReader(b), knownMD5Of(b), 1<<20)
	if err == nil {
		t.Fatal("Extract error = nil, want a token error")
	}
	var syn *xml.SyntaxError
	if !errors.As(err, &syn) {
		t.Errorf("error = %v (%T), want *xml.SyntaxError (token error after recognized root)", err, err)
	}
	if errors.Is(err, ErrDamagedContent) {
		t.Errorf("error must not be ErrDamagedContent: the root was recognized")
	}
}

func TestExtractForeignRoot(t *testing.T) {
	b := fixtureBytes(t, "foreign_root.fb2")
	_, err := extractBytes(t, bytes.NewReader(b), "", 1<<20)
	if !errors.Is(err, ErrDamagedContent) {
		t.Fatalf("Extract error = %v, want ErrDamagedContent (not a FictionBook root)", err)
	}
}

func TestExtractDuplicateComponents(t *testing.T) {
	md := extractFixture(t, "duplicate_components.fb2", "")
	if len(md.Contributors) != 1 {
		t.Fatalf("contributors = %d, want 1", len(md.Contributors))
	}
	c := md.Contributors[0]
	// The first value wins the named field; every duplicate joins the display.
	assertContributor(t, &c, authornorm.RoleAuthor, 0,
		strp("Пётр"), nil, strp("Дубль"), nil, "Пётр Петро Дубль")
	if !c.Value.HasDuplicateComponent() {
		t.Error("HasDuplicateComponent() = false, want true")
	}
}

func TestExtractInputValidation(t *testing.T) {
	b := fixtureBytes(t, "limit.fb2")
	validMD5 := knownMD5Of(b)
	cases := []struct {
		name string
		ex   MetadataExtractor
		in   ExtractBookInput
	}{
		{"zero book id", MetadataExtractor{MetadataMaxBytes: 1 << 20, ExtractorVersion: testExtractorVersion},
			ExtractBookInput{Reader: bytes.NewReader(b), BookID: 0}},
		{"negative book id", MetadataExtractor{MetadataMaxBytes: 1 << 20, ExtractorVersion: testExtractorVersion},
			ExtractBookInput{Reader: bytes.NewReader(b), BookID: -3}},
		{"empty version", MetadataExtractor{MetadataMaxBytes: 1 << 20},
			ExtractBookInput{Reader: bytes.NewReader(b), BookID: 1}},
		{"zero limit", MetadataExtractor{ExtractorVersion: testExtractorVersion},
			ExtractBookInput{Reader: bytes.NewReader(b), BookID: 1}},
		{"negative limit", MetadataExtractor{MetadataMaxBytes: -1, ExtractorVersion: testExtractorVersion},
			ExtractBookInput{Reader: bytes.NewReader(b), BookID: 1}},
		{"nil reader", MetadataExtractor{MetadataMaxBytes: 1 << 20, ExtractorVersion: testExtractorVersion},
			ExtractBookInput{BookID: 1}},
		{"known md5 wrong length", MetadataExtractor{MetadataMaxBytes: 1 << 20, ExtractorVersion: testExtractorVersion},
			ExtractBookInput{Reader: bytes.NewReader(b), BookID: 1, KnownMD5: "abcd"}},
		{"known md5 not hex", MetadataExtractor{MetadataMaxBytes: 1 << 20, ExtractorVersion: testExtractorVersion},
			ExtractBookInput{Reader: bytes.NewReader(b), BookID: 1, KnownMD5: strings.Repeat("z", 32)}},
		{"known md5 uppercase", MetadataExtractor{MetadataMaxBytes: 1 << 20, ExtractorVersion: testExtractorVersion},
			ExtractBookInput{Reader: bytes.NewReader(b), BookID: 1, KnownMD5: strings.ToUpper(validMD5)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.ex.Extract(tc.in)
			if !errors.Is(err, ErrInvalidExtractorInput) {
				t.Fatalf("Extract error = %v, want ErrInvalidExtractorInput", err)
			}
		})
	}
}

// chunkReader serves the stream one byte per Read: the worst legal
// fragmentation a streaming reader may exhibit (B1).
type chunkReader struct{ r io.Reader }

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return c.r.Read(p)
}

func TestExtractFragmentedReader(t *testing.T) {
	// BOM/SN signature detection must not depend on how the reader fragments
	// its output: supported encodings extract identically one byte at a time.
	for _, name := range []string{"enc_utf16le_bom.fb2", "enc_utf16be_bom.fb2", "enc_utf8_bom.fb2", "enc_cp1251.fb2"} {
		t.Run(name, func(t *testing.T) {
			b := fixtureBytes(t, name)
			md, err := extractBytes(t, &chunkReader{r: bytes.NewReader(b)}, "", 1<<20)
			if err != nil {
				t.Fatalf("Extract under fragmented reader: %v", err)
			}
			if len(md.Contributors) != 1 {
				t.Fatalf("contributors = %d, want 1", len(md.Contributors))
			}
		})
	}
	// A refused encoding keeps its closed error class under fragmentation.
	b := fixtureBytes(t, "enc_utf32le_bom.fb2")
	_, err := extractBytes(t, &chunkReader{r: bytes.NewReader(b)}, "", 1<<20)
	if !errors.Is(err, ErrUnsupportedCharset) {
		t.Fatalf("utf-32 under fragmented reader: error = %v, want ErrUnsupportedCharset", err)
	}
}

func TestExtractMissingDescriptionFails(t *testing.T) {
	docs := []struct{ name, doc string }{
		{"empty root", `<?xml version="1.0" encoding="utf-8"?><FictionBook/>`},
		{"body without description",
			`<?xml version="1.0" encoding="utf-8"?><FictionBook><body><section><p>body text</p></section></body></FictionBook>`},
	}
	for _, tc := range docs {
		for _, known := range []bool{true, false} {
			t.Run(tc.name, func(t *testing.T) {
				b := []byte(tc.doc)
				knownMD5 := ""
				if known {
					knownMD5 = knownMD5Of(b)
				}
				_, err := extractBytes(t, bytes.NewReader(b), knownMD5, 1<<20)
				if !errors.Is(err, ErrDamagedContent) {
					t.Fatalf("known=%v: Extract error = %v, want ErrDamagedContent (no description)", known, err)
				}
			})
		}
	}
	// The body must not be scanned into the metadata window: the scan stops
	// at the <body> start tag.
	b := []byte(docs[1].doc)
	cr := &countingReader{r: bytes.NewReader(b)}
	_, err := extractBytes(t, cr, knownMD5Of(b), 1<<20)
	if err == nil {
		t.Fatal("Extract error = nil, want failure for missing description")
	}
	want := int64(bytes.Index(b, []byte("<body>")) + len("<body>"))
	if cr.n != want {
		t.Errorf("bytes read = %d, want exactly %d (body text must stay unread)", cr.n, want)
	}
}

var errSentinelIO = errors.New("sentinel i/o failure")

// failReader serves up to after bytes, then fails every Read with err.
type failReader struct {
	r     io.Reader
	after int
	err   error
	n     int
}

func (f *failReader) Read(p []byte) (int, error) {
	if f.n >= f.after {
		return 0, f.err
	}
	n, err := f.r.Read(p)
	f.n += n
	return n, err
}

func TestExtractReaderErrorPreserved(t *testing.T) {
	b := fixtureBytes(t, "limit.fb2")
	// A genuine source failure is a systemic condition, not damaged book
	// content (contract 3.3): the original error identity must survive.
	for _, tc := range []struct {
		name  string
		after int
	}{
		{"before first byte", 0},
		{"before root completes", 50},
		{"after root", 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := extractBytes(t, &failReader{r: bytes.NewReader(b), after: tc.after, err: errSentinelIO}, "", 1<<20)
			if !errors.Is(err, errSentinelIO) {
				t.Fatalf("Extract error = %v, want errors.Is(err, sentinel)", err)
			}
			if errors.Is(err, ErrDamagedContent) {
				t.Error("source i/o failure must not be classified as damaged content")
			}
		})
	}
	// A clean empty entry is a document failure, not an i/o failure.
	_, err := extractBytes(t, bytes.NewReader(nil), "", 1<<20)
	if !errors.Is(err, ErrDamagedContent) {
		t.Errorf("empty entry error = %v, want ErrDamagedContent", err)
	}
}

func TestExtractSrcTitleInfoSequences(t *testing.T) {
	md := extractFixture(t, "src_title_info.fb2", "")
	// Credits still come from title-info only: the src-title-info author is
	// the original-work author, not a credit of this edition.
	if len(md.Contributors) != 1 {
		t.Fatalf("contributors = %d, want 1 (src-title-info author must not create a credit)", len(md.Contributors))
	}
	assertContributor(t, &md.Contributors[0], authornorm.RoleAuthor, 0,
		strp("Сильвия"), nil, strp("Оригиналова"), nil, "Сильвия Оригиналова")
	// Contract 3.2 outputs all sequences with order and nesting; the
	// src-title-info subtree is no exception.
	wantSeqs := []authornorm.Sequence{
		{Index: 0, Parent: -1, Name: "Original Saga", Number: strp("7")},
		{Index: 1, Parent: 0, Name: "Original Inner", Number: nil},
	}
	if len(md.Sequences) != len(wantSeqs) {
		t.Fatalf("sequences = %d, want %d: %+v", len(md.Sequences), len(wantSeqs), md.Sequences)
	}
	for i, want := range wantSeqs {
		got := md.Sequences[i]
		if got.Index != want.Index || got.Parent != want.Parent || got.Name != want.Name ||
			(got.Number == nil) != (want.Number == nil) || (got.Number != nil && *got.Number != *want.Number) {
			t.Errorf("sequence %d = %+v, want %+v", i, got, want)
		}
	}
}

// TestScanRetainsAnnotationWithinLimit pins the final annotation design: no
// raw window byte is ever trimmed, for any encoding. The window holds the
// complete metadata prefix — annotation bytes included — exactly up to the
// description end; skipping the annotation happens on decoded tokens in the
// extraction pass (pinned by the extraction-level tests).
func TestScanRetainsAnnotationWithinLimit(t *testing.T) {
	b := fixtureBytes(t, "annotation_big.fb2")
	sink := &metadataSink{limit: 1 << 20, limited: true, capture: true}
	tee := io.TeeReader(bytes.NewReader(b), sink)
	peek, head, err := peekBOM(tee)
	if err != nil {
		t.Fatalf("peekBOM: %v", err)
	}
	scan, err := openScanSource(peek, tee, head)
	if err != nil {
		t.Fatalf("openScanSource: %v", err)
	}
	if err := scanMetadataWindow(scan, sink); err != nil {
		t.Fatalf("scanMetadataWindow: %v", err)
	}
	// Byte accounting and retention cover the whole metadata prefix, the 148
	// KiB annotation included, and stop exactly at the description end.
	if want := descriptionEndOffset(b); sink.raw != want {
		t.Errorf("raw = %d, want %d (byte accounting stops at the description end)", sink.raw, want)
	}
	if int64(sink.window.Len()) != sink.raw {
		t.Errorf("window = %d bytes, want the full %d metadata bytes (no raw-byte trimming)", sink.window.Len(), sink.raw)
	}
	if !bytes.Contains(sink.window.Bytes(), []byte("ANNOTATION-MARKER")) {
		t.Error("annotation bytes must be retained in the metadata window")
	}
	// The retained window stays well-formed for the decode/extraction passes.
	if _, err := DecodeToUTF8(sink.window.Bytes()); err != nil {
		t.Errorf("window fails DecodeToUTF8: %v", err)
	}
}

func TestExtractUTF16KnownMD5ReadBoundary(t *testing.T) {
	encode := func(s string, le bool) []byte {
		out := make([]byte, 0, len(s)*2)
		for i := 0; i < len(s); i++ {
			if le {
				out = append(out, s[i], 0)
			} else {
				out = append(out, 0, s[i])
			}
		}
		return out
	}
	for _, tc := range []struct {
		fixture string
		le      bool
	}{
		{"enc_utf16le_bom.fb2", true},
		{"enc_utf16be_bom.fb2", false},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			b := fixtureBytes(t, tc.fixture)
			tag := encode("</description>", tc.le)
			want := int64(bytes.Index(b, tag) + len(tag))
			cr := &countingReader{r: bytes.NewReader(b)}
			if _, err := extractBytes(t, cr, knownMD5Of(b), 1<<20); err != nil {
				t.Fatalf("Extract: %v", err)
			}
			if cr.n != want {
				t.Errorf("bytes read = %d, want exactly %d (encoded description boundary)", cr.n, want)
			}
		})
	}
}

// TestExtractUTF16AnnotationPreservesEncoding is the B6 regression: with no
// raw-byte trimming anywhere, a UTF-16BE 00 3C bracket is never at risk of
// being truncated mid-unit; the annotation bytes stay in the window, bounded
// by the byte limit, and the extraction pass discards the decoded annotation
// tokens.
func TestExtractUTF16AnnotationPreservesEncoding(t *testing.T) {
	const doc = `<?xml version="1.0" encoding="UTF-16"?>` +
		`<FictionBook><description><title-info>` +
		`<author><first-name>Страж</first-name><last-name>Телев</last-name></author>` +
		`<annotation>ignored</annotation>` +
		`<book-title>Телохранитель</book-title>` +
		`</title-info></description><body><section><p>x</p></section></body></FictionBook>`
	for _, le := range []bool{true, false} {
		for _, known := range []bool{true, false} {
			name := "utf-16be"
			if le {
				name = "utf-16le"
			}
			t.Run(name, func(t *testing.T) {
				b := utf16WithBOM(doc, le)
				knownMD5 := ""
				if known {
					knownMD5 = knownMD5Of(b)
				}
				md, err := extractBytes(t, bytes.NewReader(b), knownMD5, 1<<20)
				if err != nil {
					t.Fatalf("known=%v: Extract error = %v, want nil (valid UTF-16 input)", known, err)
				}
				if len(md.Contributors) != 1 {
					t.Fatalf("known=%v: contributors = %d, want 1", known, len(md.Contributors))
				}
				assertContributor(t, &md.Contributors[0], authornorm.RoleAuthor, 0,
					strp("Страж"), nil, strp("Телев"), nil, "Страж Телев")
				if md.Title != "Телохранитель" {
					t.Errorf("known=%v: title = %q, want %q", known, md.Title, "Телохранитель")
				}
			})
		}
	}
}

// TestExtractUTF16AnnotationAttributeDelimiter is the B6 attribute corner: the
// Cyrillic м (U+043C) encodes as 3C 04 (LE) / 04 3C (BE). With no raw-byte
// trimming for any encoding, raw bytes are never consulted as markup, so the
// attribute character cannot be mistaken for a tag delimiter.
func TestExtractUTF16AnnotationAttributeDelimiter(t *testing.T) {
	const doc = `<?xml version="1.0" encoding="UTF-16"?>` +
		`<FictionBook><description><title-info>` +
		`<author><first-name>Страж</first-name><last-name>Телев</last-name></author>` +
		`<annotation id="м">ignored</annotation>` +
		`<book-title>Телохранитель</book-title>` +
		`</title-info></description><body><section><p>x</p></section></body></FictionBook>`
	for _, le := range []bool{true, false} {
		for _, known := range []bool{true, false} {
			name := "utf-16be"
			if le {
				name = "utf-16le"
			}
			t.Run(name, func(t *testing.T) {
				b := utf16WithBOM(doc, le)
				knownMD5 := ""
				if known {
					knownMD5 = knownMD5Of(b)
				}
				md, err := extractBytes(t, bytes.NewReader(b), knownMD5, 1<<20)
				if err != nil {
					t.Fatalf("known=%v: Extract error = %v, want nil (valid UTF-16 input)", known, err)
				}
				if md.Title != "Телохранитель" {
					t.Errorf("known=%v: title = %q, want %q", known, md.Title, "Телохранитель")
				}
			})
		}
	}
}

// oneShotErrorReader fails exactly one Read with err, then serves clean EOF:
// a legitimate truncated-stream source (B3 corner).
type oneShotErrorReader struct {
	err    error
	failed bool
}

func (r *oneShotErrorReader) Read([]byte) (int, error) {
	if !r.failed {
		r.failed = true
		return 0, r.err
	}
	return 0, io.EOF
}

// TestExtractOneShotUnexpectedEOFPreserved is the B3 corner: io.ReadFull
// synthesizes io.ErrUnexpectedEOF for a clean short head and also propagates a
// source's own one-shot io.ErrUnexpectedEOF unchanged; the extractor must
// distinguish them so the source error keeps its identity (contract 3.3).
func TestExtractOneShotUnexpectedEOFPreserved(t *testing.T) {
	_, err := extractBytes(t, &oneShotErrorReader{err: io.ErrUnexpectedEOF}, "", 1<<20)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Extract error = %v, want errors.Is(err, io.ErrUnexpectedEOF)", err)
	}
	if errors.Is(err, ErrDamagedContent) {
		t.Error("one-shot source io.ErrUnexpectedEOF must not be classified as damaged content")
	}
}

// annotationBoundaryDoc wraps a title-info fragment (placed between the author
// and the book-title) in a complete FictionBook document.
func annotationBoundaryDoc(decl, fragment string) string {
	return `<?xml version="1.0" encoding="` + decl + `"?>` +
		`<FictionBook><description><title-info>` +
		`<author><first-name>Страж</first-name><last-name>Телев</last-name></author>` +
		fragment +
		`<book-title>Телохранитель</book-title>` +
		`</title-info></description><body><section><p>x</p></section></body></FictionBook>`
}

// encodeBoundaryDoc renders doc in one of the supported charsets; cp1251
// covers the single-byte path alongside UTF-8, UTF-16LE and UTF-16BE.
func encodeBoundaryDoc(t *testing.T, enc, doc string) []byte {
	t.Helper()
	switch enc {
	case "utf-8":
		return []byte(doc)
	case "utf-16le":
		return utf16WithBOM(doc, true)
	case "utf-16be":
		return utf16WithBOM(doc, false)
	case "cp1251":
		out, err := charmap.Windows1251.NewEncoder().String(doc)
		if err != nil {
			t.Fatalf("encode cp1251: %v", err)
		}
		return []byte(out)
	}
	t.Fatalf("unknown test encoding %q", enc)
	return nil
}

// asciiTagEncoded encodes a pure-ASCII tag the same way encodeBoundaryDoc
// would (without any BOM), for locating the description boundary.
func asciiTagEncoded(enc, tag string) []byte {
	switch enc {
	case "utf-16le", "utf-16be":
		out := make([]byte, 0, len(tag)*2)
		for i := 0; i < len(tag); i++ {
			if enc == "utf-16le" {
				out = append(out, tag[i], 0)
			} else {
				out = append(out, 0, tag[i])
			}
		}
		return out
	}
	return []byte(tag)
}

// TestExtractAnnotationTokenBoundaries is the B7+B8 regression and its
// companion coverage: annotation handling must not depend on assumptions
// about how the decoder (or the scan sanitizer's multi-byte prefetch)
// consumes input around token boundaries. A CDATA section — unlike ordinary
// character data — does not consume the following tag's opening bracket, and
// a cp1251 byte that looks like a UTF-8 lead makes the sanitizer read ahead
// into the next tag. No raw window byte is ever trimmed, for any encoding;
// the extraction pass discards decoded annotation tokens. Every variant
// asserts exact extracted metadata, the full-entry MD5 on both paths, and
// exact source consumption, under normal and one-byte-fragmented readers.
func TestExtractAnnotationTokenBoundaries(t *testing.T) {
	variants := []struct{ name, fragment string }{
		// The B7 probe: whitespace-only CDATA directly before the annotation.
		{"cdata-whitespace", `<![CDATA[ ]]><annotation>ignored</annotation>`},
		{"comment-before", `<!-- note --><annotation>ignored</annotation>`},
		{"nested-elements", `<annotation><p>nested <b>bold</b> text</p></annotation>`},
		{"entity-refs-inside", `<annotation>x &amp; y &lt;z&gt;</annotation>`},
		{"entity-ref-before", `&amp;<annotation>ignored</annotation>`},
		// The B8 probes: cp1251 bytes that mimic UTF-8 lead bytes (а = 0xE0,
		// р = 0xF0) make the raw scan sanitizer prefetch into the annotation
		// start tag; UTF-8/UTF-16 encode the same characters as valid units.
		{"cyrillic-three-byte-lead-before", `а<annotation>ignored</annotation>`},
		{"cyrillic-four-byte-lead-before", `р<annotation>ignored</annotation>`},
		{"repeated-annotation-cyrillic", `<annotation>one</annotation>а<annotation>two</annotation>`},
	}
	encodings := []struct{ name, decl string }{
		{"utf-8", "UTF-8"},
		{"utf-16le", "UTF-16"},
		{"utf-16be", "UTF-16"},
		{"cp1251", "windows-1251"},
	}
	for _, v := range variants {
		for _, enc := range encodings {
			for _, known := range []bool{true, false} {
				for _, frag := range []bool{false, true} {
					name := fmt.Sprintf("%s/%s/known=%v/frag=%v", v.name, enc.name, known, frag)
					t.Run(name, func(t *testing.T) {
						b := encodeBoundaryDoc(t, enc.name, annotationBoundaryDoc(enc.decl, v.fragment))
						knownMD5 := ""
						if known {
							knownMD5 = knownMD5Of(b)
						}
						var src io.Reader = bytes.NewReader(b)
						if frag {
							src = &chunkReader{r: src}
						}
						cr := &countingReader{r: src}
						md, err := extractBytes(t, cr, knownMD5, 1<<20)
						if err != nil {
							t.Fatalf("Extract error = %v, want nil (valid %s input)", err, enc.name)
						}
						if len(md.Contributors) != 1 {
							t.Fatalf("contributors = %d, want 1", len(md.Contributors))
						}
						assertContributor(t, &md.Contributors[0], authornorm.RoleAuthor, 0,
							strp("Страж"), nil, strp("Телев"), nil, "Страж Телев")
						if md.Title != "Телохранитель" {
							t.Errorf("title = %q, want %q", md.Title, "Телохранитель")
						}
						if want := knownMD5Of(b); md.BookMD5 != want {
							t.Errorf("md5 = %q, want %q", md.BookMD5, want)
						}
						// The known path stops exactly after </description>; the
						// computed path drains the whole entry into the hash.
						var wantRead int64
						if known {
							tag := asciiTagEncoded(enc.name, "</description>")
							wantRead = int64(bytes.Index(b, tag) + len(tag))
						} else {
							wantRead = int64(len(b))
						}
						if cr.n != wantRead {
							t.Errorf("bytes read = %d, want exactly %d", cr.n, wantRead)
						}
					})
				}
			}
		}
	}
}

// TestExtractLargeAnnotationLimit pins the interaction of a large retained
// annotation with the metadata byte budget: the budget bounds every raw
// metadata-phase byte, annotation included, so an annotation that fits just
// under the limit extracts correctly (its decoded tokens are discarded by the
// extraction pass), and the first byte past the limit fails with
// ErrMetadataLimit on both MD5 paths.
func TestExtractLargeAnnotationLimit(t *testing.T) {
	b := fixtureBytes(t, "annotation_big.fb2")
	n := descriptionEndOffset(b)
	if n <= 0 {
		t.Fatal("fixture has no </description>")
	}
	known := knownMD5Of(b)
	for _, useKnown := range []bool{true, false} {
		knownMD5 := ""
		if useKnown {
			knownMD5 = known
		}
		t.Run(fmt.Sprintf("known=%v", useKnown), func(t *testing.T) {
			// The whole metadata window — 148 KiB annotation included — fits.
			md, err := extractBytes(t, bytes.NewReader(b), knownMD5, n)
			if err != nil {
				t.Fatalf("limit = N: Extract error = %v, want nil", err)
			}
			if len(md.Contributors) != 1 {
				t.Fatalf("limit = N: contributors = %d, want 1", len(md.Contributors))
			}
			assertContributor(t, &md.Contributors[0], authornorm.RoleAuthor, 0,
				strp("Аннота"), nil, strp("Циева"), nil, "Аннота Циева")
			if md.Title != "Большая аннотация" {
				t.Errorf("limit = N: title = %q, want %q", md.Title, "Большая аннотация")
			}
			if md.BookMD5 != known {
				t.Errorf("limit = N: md5 = %q, want %q", md.BookMD5, known)
			}
			// One byte less, and the metadata phase exceeds the budget.
			_, err = extractBytes(t, bytes.NewReader(b), knownMD5, n-1)
			if !errors.Is(err, ErrMetadataLimit) {
				t.Errorf("limit = N-1: error = %v, want ErrMetadataLimit", err)
			}
		})
	}
}
