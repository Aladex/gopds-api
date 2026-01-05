package parser

import (
	"os"
	"strings"
	"testing"
)

func TestFB2ParserParseBasic(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<FictionBook>
  <description>
    <title-info>
      <book-title>Test Title</book-title>
      <author>
        <first-name>John</first-name>
        <last-name>Doe</last-name>
      </author>
      <genre>fantasy</genre>
      <lang>EN</lang>
      <annotation>
        <p>Line1</p>
        <p>Line2</p>
      </annotation>
      <sequence name="Saga" number="1"/>
    </title-info>
    <document-info>
      <date value="2020-01-01"/>
    </document-info>
  </description>
  <body>Body text</body>
</FictionBook>`

	parser := NewFB2Parser(false)
	book, err := parser.Parse(strings.NewReader(xml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if book.Title != "Test Title" {
		t.Fatalf("unexpected title: %q", book.Title)
	}
	if len(book.Authors) != 1 || book.Authors[0].Name != "John Doe" {
		t.Fatalf("unexpected authors: %#v", book.Authors)
	}
	if len(book.Tags) != 1 || book.Tags[0] != "fantasy" {
		t.Fatalf("unexpected tags: %#v", book.Tags)
	}
	if book.Language != "en" {
		t.Fatalf("unexpected language: %q", book.Language)
	}
	if book.DocDate != "2020-01-01" {
		t.Fatalf("unexpected doc date: %q", book.DocDate)
	}
	if book.Annotation != "Line1\nLine2" {
		t.Fatalf("unexpected annotation: %q", book.Annotation)
	}
	if book.Series == nil || book.Series.Title != "Saga" || book.Series.Index != "1" {
		t.Fatalf("unexpected series: %#v", book.Series)
	}
	if book.BodySample != "Body text" {
		t.Fatalf("unexpected body sample: %q", book.BodySample)
	}
}

func TestFB2ParserCoverExtraction(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns:xlink="http://www.w3.org/1999/xlink">
  <description>
    <title-info>
      <book-title>Title</book-title>
      <coverpage>
        <image xlink:href="#c1"/>
      </coverpage>
    </title-info>
  </description>
  <binary id="c1">YWJj</binary>
</FictionBook>`

	parser := NewFB2Parser(true)
	book, err := parser.Parse(strings.NewReader(xml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(book.Cover) != "abc" {
		t.Fatalf("unexpected cover data: %q", string(book.Cover))
	}
}

func TestFB2ParserNoCover(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<FictionBook>
  <description>
    <title-info>
      <book-title>Title</book-title>
    </title-info>
  </description>
</FictionBook>`

	parser := NewFB2Parser(true)
	book, err := parser.Parse(strings.NewReader(xml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if book.Cover != nil {
		t.Fatalf("expected no cover, got %d bytes", len(book.Cover))
	}
}

func TestFB2ParserNoSeries(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<FictionBook>
  <description>
    <title-info>
      <book-title>Title</book-title>
    </title-info>
  </description>
</FictionBook>`

	parser := NewFB2Parser(false)
	book, err := parser.Parse(strings.NewReader(xml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if book.Series != nil {
		t.Fatalf("expected nil series, got %#v", book.Series)
	}
}

func TestFB2ParserNamespace(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
  <description>
    <title-info>
      <book-title>Namespaced</book-title>
    </title-info>
  </description>
</FictionBook>`

	parser := NewFB2Parser(false)
	book, err := parser.Parse(strings.NewReader(xml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if book.Title != "Namespaced" {
		t.Fatalf("unexpected title: %q", book.Title)
	}
}

func TestFB2ParserNamespace21(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.1">
  <description>
    <title-info>
      <book-title>Namespaced21</book-title>
    </title-info>
  </description>
</FictionBook>`

	parser := NewFB2Parser(false)
	book, err := parser.Parse(strings.NewReader(xml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if book.Title != "Namespaced21" {
		t.Fatalf("unexpected title: %q", book.Title)
	}
}

func TestFB2ParserLanguageStandardization(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<FictionBook>
  <description>
    <title-info>
      <book-title>Title</book-title>
      <lang>RU</lang>
    </title-info>
  </description>
</FictionBook>`

	parser := NewFB2Parser(false)
	book, err := parser.Parse(strings.NewReader(xml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if book.Language != "ru" {
		t.Fatalf("unexpected language: %q", book.Language)
	}
}

func TestFB2ParserTitleSanitization(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<FictionBook>
  <description>
    <title-info>
      <book-title>"-Hello-"</book-title>
    </title-info>
  </description>
</FictionBook>`

	parser := NewFB2Parser(false)
	book, err := parser.Parse(strings.NewReader(xml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if book.Title != "Hello" {
		t.Fatalf("unexpected title: %q", book.Title)
	}
}

func TestFB2ParserMalformedXML(t *testing.T) {
	xml := `<FictionBook><description></FictionBook>`

	parser := NewFB2Parser(false)
	_, err := parser.Parse(strings.NewReader(xml))
	if err == nil {
		t.Fatalf("expected parse error")
	}
}

// Integration tests with real FB2 files

func TestParseSimpleFB2File(t *testing.T) {
	file, err := os.Open("testdata/simple.fb2")
	if err != nil {
		t.Skipf("testdata/simple.fb2 not found: %v", err)
	}
	defer file.Close()

	parser := NewFB2Parser(false)
	book, err := parser.Parse(file)
	if err != nil {
		t.Fatalf("failed to parse simple.fb2: %v", err)
	}

	if book.Title != "Simple Test Book" {
		t.Fatalf("expected title 'Simple Test Book', got %q", book.Title)
	}
	if book.Language != "en" {
		t.Fatalf("expected language 'en', got %q", book.Language)
	}
	if book.BodySample == "" {
		t.Fatal("expected non-empty body sample")
	}
	t.Logf("Parsed: %s (lang: %s)", book.Title, book.Language)
}

func TestParseCompleteFB2File(t *testing.T) {
	file, err := os.Open("testdata/complete.fb2")
	if err != nil {
		t.Skipf("testdata/complete.fb2 not found: %v", err)
	}
	defer file.Close()

	parser := NewFB2Parser(true)
	book, err := parser.Parse(file)
	if err != nil {
		t.Fatalf("failed to parse complete.fb2: %v", err)
	}

	if book.Title != "Complete Test Book" {
		t.Fatalf("expected title 'Complete Test Book', got %q", book.Title)
	}

	if len(book.Authors) != 2 {
		t.Fatalf("expected 2 authors, got %d", len(book.Authors))
	}
	if book.Authors[0].Name != "Ivan Petrov" {
		t.Fatalf("unexpected first author: %q", book.Authors[0].Name)
	}
	if book.Authors[1].Name != "Maria Sidorova" {
		t.Fatalf("unexpected second author: %q", book.Authors[1].Name)
	}

	if len(book.Tags) != 2 {
		t.Fatalf("expected 2 tags, got %d: %v", len(book.Tags), book.Tags)
	}

	if book.Language != "ru" {
		t.Fatalf("expected language 'ru', got %q", book.Language)
	}

	if book.DocDate != "2023-06-15" {
		t.Fatalf("expected doc date '2023-06-15', got %q", book.DocDate)
	}

	if book.Series == nil {
		t.Fatal("expected series, got nil")
	}
	if book.Series.Title != "Space Opera Series" {
		t.Fatalf("expected series title 'Space Opera Series', got %q", book.Series.Title)
	}
	if book.Series.Index != "2" {
		t.Fatalf("expected series index '2', got %q", book.Series.Index)
	}

	if book.Annotation == "" {
		t.Fatal("expected non-empty annotation")
	}

	if book.Cover == nil {
		t.Fatal("expected cover image, got nil")
	}

	t.Logf("Parsed: %s by %v", book.Title, book.Authors)
	t.Logf("Series: %s #%s", book.Series.Title, book.Series.Index)
	t.Logf("Cover: %d bytes", len(book.Cover))
}

func TestParseFB21File(t *testing.T) {
	file, err := os.Open("testdata/fb21.fb2")
	if err != nil {
		t.Skipf("testdata/fb21.fb2 not found: %v", err)
	}
	defer file.Close()

	parser := NewFB2Parser(false)
	book, err := parser.Parse(file)
	if err != nil {
		t.Fatalf("failed to parse fb21.fb2: %v", err)
	}

	if book.Title != "FB2.1 Format Book" {
		t.Fatalf("expected title 'FB2.1 Format Book', got %q", book.Title)
	}
	if len(book.Authors) != 1 {
		t.Fatalf("expected 1 author, got %d", len(book.Authors))
	}
	if book.Authors[0].Name != "Alexei Tolstoy" {
		t.Fatalf("unexpected author: %q", book.Authors[0].Name)
	}
	if book.Language != "ru" {
		t.Fatalf("expected language 'ru', got %q", book.Language)
	}

	t.Logf("Parsed FB2.1: %s", book.Title)
}
