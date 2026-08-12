package utils

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// fixtureImageB64 is the base64 payload of the <binary> element in
// epubRegressionFixture; the EPUB must embed exactly these bytes.
const fixtureImageB64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="

// epubRegressionFixture exercises every structure that used to lose, reorder,
// or duplicate content on the download path: text directly under <body>,
// text between two sections, text after the last section, sibling top-level
// sections, a nested section, several paragraphs inside one chapter (their
// relative order matters), an empty section, a second ordinary <body>, a
// notes body, a footnote with <sup> markup, an inline image, and internal
// links to both a section and a paragraph anchor.
const epubRegressionFixture = `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0" xmlns:l="http://www.w3.org/1999/xlink">
  <description>
    <title-info>
      <genre>prose</genre>
      <author>
        <first-name>Regression</first-name>
        <last-name>Author</last-name>
      </author>
      <book-title>Regression Book</book-title>
      <lang>ru</lang>
    </title-info>
  </description>
  <body>
    <p>МАРКЕР ПРОЛОГА</p>
    <section id="ch1">
      <title><p>Первая глава</p></title>
      <p>МАРКЕР ПЕРВОЙ ГЛАВЫ со сноской<a l:href="#note1" type="note"><sup>1</sup></a>.</p>
      <p>МАРКЕР ВНУТРИ А</p>
      <p>Переход ко <a l:href="#ch2">ССЫЛКА-МАРКЕР</a> внутри книги и к <a l:href="#p2">ЯКОРЬ-МАРКЕР</a> абзаца.</p>
      <p>МАРКЕР ВНУТРИ Б</p>
      <section id="ch1-1">
        <title><p>Подраздел полтора</p></title>
        <p>МАРКЕР ПОДРАЗДЕЛА</p>
      </section>
    </section>
    <p>МАРКЕР МЕЖДУ ГЛАВАМИ</p>
    <section id="empty"></section>
    <section id="ch2">
      <title><p>Вторая глава</p></title>
      <p id="p2">МАРКЕР ВТОРОЙ ГЛАВЫ с картинкой <image l:href="#img1"/>.</p>
    </section>
    <p>МАРКЕР ЭПИЛОГА</p>
  </body>
  <body>
    <p>МАРКЕР ВТОРОГО ТЕЛА</p>
    <section id="ch3">
      <title><p>Третья глава</p></title>
      <p>МАРКЕР ТРЕТЬЕЙ ГЛАВЫ</p>
    </section>
    <p>МАРКЕР ФИНАЛА</p>
  </body>
  <body name="notes">
    <section id="note1">
      <title><p>1</p></title>
      <p>МАРКЕР СНОСКИ с возвратом к <a l:href="#ch1">ССЫЛКА-ИЗ-СНОСКИ</a>.</p>
    </section>
  </body>
  <binary id="img1" content-type="image/png">iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk
+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==</binary>
</FictionBook>`

// zipWithSingleFB2 packs the fixture into a zip archive the way the book
// storage does, returning the archive path.
func zipWithSingleFB2(t *testing.T, fb2 []byte) string {
	t.Helper()
	zipPath := filepath.Join(t.TempDir(), "book.zip")

	zipFile, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("Failed to create temp ZIP: %v", err)
	}
	zipWriter := zip.NewWriter(zipFile)
	entry, err := zipWriter.Create("book.fb2")
	if err != nil {
		zipFile.Close()
		t.Fatalf("Failed to create FB2 entry: %v", err)
	}
	if _, err := entry.Write(fb2); err != nil {
		zipFile.Close()
		t.Fatalf("Failed to write FB2: %v", err)
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatalf("Failed to close ZIP writer: %v", err)
	}
	if err := zipFile.Close(); err != nil {
		t.Fatalf("Failed to close ZIP file: %v", err)
	}
	return zipPath
}

// unzipToMap reads a generated EPUB into a filename → content map.
func unzipToMap(t *testing.T, epubData []byte) map[string][]byte {
	t.Helper()
	zipReader, err := zip.NewReader(bytes.NewReader(epubData), int64(len(epubData)))
	if err != nil {
		t.Fatalf("EPUB is not a valid ZIP: %v", err)
	}
	files := make(map[string][]byte, len(zipReader.File))
	for _, f := range zipReader.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("Failed to open %s: %v", f.Name, err)
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("Failed to read %s: %v", f.Name, err)
		}
		files[f.Name] = content
	}
	return files
}

type opfItem struct {
	ID        string `xml:"id,attr"`
	Href      string `xml:"href,attr"`
	MediaType string `xml:"media-type,attr"`
}

type opfPackage struct {
	Manifest struct {
		Items []opfItem `xml:"item"`
	} `xml:"manifest"`
	Spine struct {
		ItemRefs []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

// parseOPF extracts the manifest and spine from the package document.
func parseOPF(t *testing.T, files map[string][]byte) opfPackage {
	t.Helper()
	opf, ok := files["OEBPS/content.opf"]
	if !ok {
		t.Fatal("content.opf missing")
	}
	var pkg opfPackage
	if err := xml.Unmarshal(opf, &pkg); err != nil {
		t.Fatalf("content.opf does not parse: %v", err)
	}
	return pkg
}

// spineChapters returns the chapter file names in real reading order — the
// order a reader walks them, which is the OPF spine, not the alphabetical
// order of file names.
func spineChapters(t *testing.T, files map[string][]byte) []string {
	t.Helper()
	pkg := parseOPF(t, files)
	hrefByID := make(map[string]string, len(pkg.Manifest.Items))
	for _, item := range pkg.Manifest.Items {
		hrefByID[item.ID] = item.Href
	}
	var chapters []string
	for _, ref := range pkg.Spine.ItemRefs {
		href := hrefByID[ref.IDRef]
		if strings.HasPrefix(href, "index") && strings.HasSuffix(href, ".xhtml") {
			if _, ok := files["OEBPS/"+href]; !ok {
				t.Errorf("Spine references %s, which is missing from the archive", href)
				continue
			}
			chapters = append(chapters, href)
		}
	}
	if len(chapters) == 0 {
		t.Fatal("No chapter files in the spine")
	}
	return chapters
}

var linkAttrRe = regexp.MustCompile(`(?:href|src)="([^"]+)"`)

// checkAllLinksResolve verifies that every internal reference in every XHTML
// and NCX file — chapters, nav.xhtml, toc.xhtml, notes.xhtml, toc.ncx —
// points at an existing file and, when it carries a fragment, at an existing
// anchor in that file.
func checkAllLinksResolve(t *testing.T, files map[string][]byte) {
	t.Helper()
	for name, content := range files {
		if !strings.HasSuffix(name, ".xhtml") && !strings.HasSuffix(name, ".ncx") {
			continue
		}
		for _, match := range linkAttrRe.FindAllSubmatch(content, -1) {
			href := string(match[1])
			if strings.Contains(href, "://") {
				t.Errorf("External reference '%s' in %s: none are expected in this book", href, name)
				continue
			}
			filePart, anchor, _ := strings.Cut(href, "#")
			if filePart == "" {
				filePart = strings.TrimPrefix(name, "OEBPS/")
			}
			target, exists := files["OEBPS/"+filePart]
			if !exists {
				t.Errorf("Link '%s' in %s points to a missing file", href, name)
				continue
			}
			if anchor != "" && !strings.Contains(string(target), `id="`+anchor+`"`) {
				t.Errorf("Link '%s' in %s does not resolve to an anchor", href, name)
			}
		}
	}
}

var navAnchorRe = regexp.MustCompile(`<a href="([^"]+)">([^<]+)</a>`)

// requireLinkByText returns the href of the link wrapping the given visible
// text. A resolver bug that drops the <a> and keeps only the text passes
// every target-side check (the anchor still exists, the surviving links
// still resolve), so the test must demand the link itself, not its target.
func requireLinkByText(t *testing.T, content, text string) string {
	t.Helper()
	re := regexp.MustCompile(`<a href="([^"]+)">` + regexp.QuoteMeta(text) + `</a>`)
	match := re.FindStringSubmatch(content)
	if match == nil {
		t.Fatalf("No link with text '%s' found — the link itself is missing", text)
	}
	return match[1]
}

// requireLinkResolvesTo verifies the link was rewritten to point at the file
// holding the target anchor: a non-empty file part, the expected fragment,
// an existing file, and the anchor present in it.
func requireLinkResolvesTo(t *testing.T, files map[string][]byte, href, anchor string) {
	t.Helper()
	filePart, fragment, _ := strings.Cut(href, "#")
	if filePart == "" {
		t.Errorf("Link '%s' was not rewritten to the file holding #%s", href, anchor)
		return
	}
	if fragment != anchor {
		t.Errorf("Link '%s' carries fragment #%s, expected #%s", href, fragment, anchor)
	}
	target, ok := files["OEBPS/"+filePart]
	if !ok {
		t.Errorf("Link '%s' points at a missing file", href)
		return
	}
	if !strings.Contains(string(target), `id="`+anchor+`"`) {
		t.Errorf("Link '%s' points at a file without the #%s anchor", href, anchor)
	}
}

// tocEntry is one line of the EPUB 3 navigation document.
type tocEntry struct {
	Href  string
	Title string
}

func parseNav(t *testing.T, files map[string][]byte) []tocEntry {
	t.Helper()
	nav, ok := files["OEBPS/nav.xhtml"]
	if !ok {
		t.Fatal("nav.xhtml missing")
	}
	var entries []tocEntry
	for _, match := range navAnchorRe.FindAllSubmatch(nav, -1) {
		entries = append(entries, tocEntry{Href: string(match[1]), Title: string(match[2])})
	}
	return entries
}

// TestEpubConversion_FullPath is the download-safety regression test: it
// walks the exact production path FB2 bytes → ParseFB2Complete → GenerateEPUB
// and verifies that no content is lost, duplicated, or reordered, that the
// table of contents points at the right chapters, that every internal link
// in every file resolves, and that images keep their bytes and type. The
// MOBI test skips without kindlegen, so this is the only guard of the chain.
func TestEpubConversion_FullPath(t *testing.T) {
	zipPath := zipWithSingleFB2(t, []byte(epubRegressionFixture))
	processor := NewBookProcessor("book.fb2", zipPath)

	epubReader, err := processor.Epub()
	if err != nil {
		t.Fatalf("EPUB conversion failed: %v", err)
	}
	defer epubReader.Close()

	epubData, err := io.ReadAll(epubReader)
	if err != nil {
		t.Fatalf("Failed to read EPUB: %v", err)
	}
	files := unzipToMap(t, epubData)

	// Chapters in real reading order: the OPF spine.
	chapterNames := spineChapters(t, files)
	var chapters strings.Builder
	for _, name := range chapterNames {
		chapters.Write(files["OEBPS/"+name])
		chapters.WriteByte('\n')
	}
	chapterText := chapters.String()

	var allXHTML strings.Builder
	for name, content := range files {
		if strings.HasSuffix(name, ".xhtml") {
			allXHTML.Write(content)
		}
	}

	// Every marker exactly once across the whole book: no loss, no duplication.
	markers := []string{
		"МАРКЕР ПРОЛОГА",
		"МАРКЕР ПЕРВОЙ ГЛАВЫ",
		"МАРКЕР ВНУТРИ А",
		"МАРКЕР ВНУТРИ Б",
		"МАРКЕР ПОДРАЗДЕЛА",
		"МАРКЕР МЕЖДУ ГЛАВАМИ",
		"МАРКЕР ВТОРОЙ ГЛАВЫ",
		"МАРКЕР ЭПИЛОГА",
		"МАРКЕР ВТОРОГО ТЕЛА",
		"МАРКЕР ТРЕТЬЕЙ ГЛАВЫ",
		"МАРКЕР ФИНАЛА",
		"МАРКЕР СНОСКИ",
		"ССЫЛКА-МАРКЕР",
		"ЯКОРЬ-МАРКЕР",
		"ССЫЛКА-ИЗ-СНОСКИ",
	}
	for _, marker := range markers {
		if count := strings.Count(allXHTML.String(), marker); count != 1 {
			t.Errorf("Marker '%s' appears %d times, expected exactly 1", marker, count)
		}
	}

	// Reading order along the spine: prologue, chapter 1 with its paragraphs
	// in source order (two of them share one chapter — a swap inside a
	// chapter must fail here), its subsection, the inter-chapter text (which
	// must NOT jump ahead of chapter 1), chapter 2, the epilogue (which must
	// NOT be dropped after the last section), the second body, chapter 3.
	order := []string{
		"МАРКЕР ПРОЛОГА",
		"МАРКЕР ПЕРВОЙ ГЛАВЫ",
		"МАРКЕР ВНУТРИ А",
		"МАРКЕР ВНУТРИ Б",
		"МАРКЕР ПОДРАЗДЕЛА",
		"МАРКЕР МЕЖДУ ГЛАВАМИ",
		"МАРКЕР ВТОРОЙ ГЛАВЫ",
		"МАРКЕР ЭПИЛОГА",
		"МАРКЕР ВТОРОГО ТЕЛА",
		"МАРКЕР ТРЕТЬЕЙ ГЛАВЫ",
		"МАРКЕР ФИНАЛА",
	}
	prev := -1
	for _, marker := range order {
		idx := strings.Index(chapterText, marker)
		if idx == -1 {
			t.Fatalf("Marker '%s' missing from chapters", marker)
		}
		if idx < prev {
			t.Errorf("Marker '%s' is out of reading order", marker)
		}
		prev = idx
	}

	// The inline formatting of the source survives: the footnote reference
	// keeps its <sup> markup.
	if !strings.Contains(chapterText, "<sup>1</sup>") {
		t.Error("Footnote reference lost its <sup> markup")
	}

	// Anonymous paragraph runs form their own spine entries: the inter-chapter
	// text is not glued to the prologue, the epilogue is not glued to
	// chapter 2.
	fileOf := func(marker string) string {
		for _, name := range chapterNames {
			if strings.Contains(string(files["OEBPS/"+name]), marker) {
				return name
			}
		}
		return ""
	}
	if fileOf("МАРКЕР ПРОЛОГА") == fileOf("МАРКЕР МЕЖДУ ГЛАВАМИ") {
		t.Error("Inter-chapter text was merged with the prologue chapter")
	}
	if fileOf("МАРКЕР ВТОРОЙ ГЛАВЫ") == fileOf("МАРКЕР ЭПИЛОГА") {
		t.Error("Epilogue was merged with the chapter 2 file")
	}

	// The TOC must contain exactly these entries, in this order — the whole
	// list, not a subset: an extra synthetic entry (a "Section" for the root
	// container, a duplicate) fails the length check just as a missing one
	// does. The empty section shows up as one untitled "Section" entry; the
	// title page and the notes page bracket the real chapters.
	expectedTOC := []struct {
		title         string
		checkContains bool
	}{
		{"Author Regression Regression Book", false}, // title page: author and title render as separate paragraphs
		{"Первая глава", true},
		{"Подраздел полтора", true},
		{"Section", false}, // the empty section has no heading to find
		{"Вторая глава", true},
		{"Третья глава", true},
		{"Примечания", true},
	}
	navEntries := parseNav(t, files)
	if len(navEntries) != len(expectedTOC) {
		t.Fatalf("TOC has %d entries, expected exactly %d: %v", len(navEntries), len(expectedTOC), navEntries)
	}
	for i, want := range expectedTOC {
		entry := navEntries[i]
		if entry.Title != want.title {
			t.Errorf("TOC entry %d is '%s', expected '%s'", i, entry.Title, want.title)
			continue
		}
		targetFile, _, _ := strings.Cut(entry.Href, "#")
		target, ok := files["OEBPS/"+targetFile]
		if !ok {
			t.Errorf("TOC entry '%s' points at missing file '%s'", want.title, targetFile)
			continue
		}
		if want.checkContains && !strings.Contains(string(target), want.title) {
			t.Errorf("TOC entry '%s' points at %s, which does not contain that heading", want.title, targetFile)
		}
	}

	// The footnote link resolves to a real anchor in notes.xhtml, and the
	// note links back into the main body.
	if !strings.Contains(chapterText, `href="notes.xhtml#note-note1"`) {
		t.Error("Footnote link does not point at notes.xhtml#note-note1")
	}
	notes, ok := files["OEBPS/notes.xhtml"]
	if !ok {
		t.Fatal("notes.xhtml missing")
	}
	if !strings.Contains(string(notes), `id="note-note1"`) {
		t.Error("notes.xhtml has no note-note1 anchor")
	}

	// The paragraph-level link was rewritten to the chapter holding the
	// anchor, not left dangling inside the source chapter.
	if !strings.Contains(chapterText, `id="p2"`) {
		t.Error("No chapter carries the p2 paragraph anchor")
	}

	// The internal links must be present as links — their targets existing
	// is not enough: a resolver that strips the <a> and keeps the text
	// passes every check above. Each link is demanded by its visible text,
	// then its href must resolve to the file holding the target anchor.
	requireLinkResolvesTo(t, files, requireLinkByText(t, chapterText, "ССЫЛКА-МАРКЕР"), "ch2")
	requireLinkResolvesTo(t, files, requireLinkByText(t, chapterText, "ЯКОРЬ-МАРКЕР"), "p2")
	requireLinkResolvesTo(t, files, requireLinkByText(t, string(notes), "ССЫЛКА-ИЗ-СНОСКИ"), "ch1")

	// Every internal link in every file — chapters, nav, toc, notes, ncx —
	// resolves to an existing anchor in an existing file.
	checkAllLinksResolve(t, files)

	// The image is embedded with its original bytes, referenced from the
	// chapter, and declared in the manifest with the right media type.
	expectedImage, err := base64.StdEncoding.DecodeString(fixtureImageB64)
	if err != nil {
		t.Fatalf("Fixture image base64 does not decode: %v", err)
	}
	imgSrcRe := regexp.MustCompile(`<img src="(images/[^"]+)"`)
	var imgRefs []string
	for _, match := range imgSrcRe.FindAllStringSubmatch(chapterText, -1) {
		imgRefs = append(imgRefs, match[1])
	}
	// Exactly one reference: the source mentions the image once, and only the
	// spine chapters count — the generated cover page legitimately reuses the
	// same file. A duplicated <img> in the text fails this count.
	if len(imgRefs) != 1 {
		t.Fatalf("Chapters reference the image %d times, expected exactly 1", len(imgRefs))
	}
	pkg := parseOPF(t, files)
	for _, ref := range imgRefs {
		data, ok := files["OEBPS/"+ref]
		if !ok {
			t.Errorf("img src '%s' has no file in the archive", ref)
			continue
		}
		if !bytes.Equal(data, expectedImage) {
			t.Errorf("Embedded image '%s' bytes differ from the source", ref)
		}
		declared := false
		for _, item := range pkg.Manifest.Items {
			if item.Href == ref {
				declared = true
				// Exact type, not the image/* prefix: a PNG declared as
				// image/jpeg passes a prefix check but misleads the reader.
				if item.MediaType != "image/png" {
					t.Errorf("Manifest declares '%s' as '%s', expected exactly image/png", ref, item.MediaType)
				}
			}
		}
		if !declared {
			t.Errorf("img src '%s' has no manifest entry", ref)
		}
	}
}

// TestEpubConversion_FirstChapterNotLost pins the production bug where the
// first chapter of a body that starts with a <section> vanished from the
// EPUB: the parser made that section the document root, and the chapter
// builder then skipped the root in favor of its children.
func TestEpubConversion_FirstChapterNotLost(t *testing.T) {
	fixture := `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
  <description>
    <title-info>
      <genre>prose</genre>
      <author>
        <first-name>Regression</first-name>
        <last-name>Author</last-name>
      </author>
      <book-title>First Chapter Book</book-title>
      <lang>ru</lang>
    </title-info>
  </description>
  <body>
    <section id="a1">
      <title><p>Глава раз</p></title>
      <p>МАРКЕР ГЛАВЫ РАЗ</p>
      <section id="a1-1">
        <title><p>Вложение</p></title>
        <p>МАРКЕР ВЛОЖЕНИЯ</p>
      </section>
    </section>
    <section id="a2">
      <title><p>Глава два</p></title>
      <p>МАРКЕР ГЛАВЫ ДВА</p>
    </section>
  </body>
</FictionBook>`

	zipPath := zipWithSingleFB2(t, []byte(fixture))
	processor := NewBookProcessor("book.fb2", zipPath)

	epubReader, err := processor.Epub()
	if err != nil {
		t.Fatalf("EPUB conversion failed: %v", err)
	}
	defer epubReader.Close()

	epubData, err := io.ReadAll(epubReader)
	if err != nil {
		t.Fatalf("Failed to read EPUB: %v", err)
	}
	files := unzipToMap(t, epubData)

	var allXHTML strings.Builder
	for name, content := range files {
		if strings.HasSuffix(name, ".xhtml") {
			allXHTML.Write(content)
		}
	}

	for _, marker := range []string{"МАРКЕР ГЛАВЫ РАЗ", "МАРКЕР ВЛОЖЕНИЯ", "МАРКЕР ГЛАВЫ ДВА"} {
		if count := strings.Count(allXHTML.String(), marker); count != 1 {
			t.Errorf("Marker '%s' appears %d times, expected exactly 1", marker, count)
		}
	}
}

// TestEpubConversion_BookWithoutSectionsHasTOC pins the navigation of a book
// whose body holds no <section> at all. The text still reaches the spine,
// and the table of contents must still list the title page and the notes
// page: an early exit on "no sections" used to emit a literally empty <ol>.
func TestEpubConversion_BookWithoutSectionsHasTOC(t *testing.T) {
	fixture := `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0" xmlns:l="http://www.w3.org/1999/xlink">
  <description>
    <title-info>
      <genre>prose</genre>
      <author>
        <first-name>Regression</first-name>
        <last-name>Author</last-name>
      </author>
      <book-title>Sectionless Book</book-title>
      <lang>ru</lang>
    </title-info>
  </description>
  <body>
    <p>МАРКЕР ЕДИНСТВЕННОГО АБЗАЦА со сноской<a l:href="#note1" type="note"><sup>1</sup></a>.</p>
  </body>
  <body name="notes">
    <section id="note1">
      <title><p>1</p></title>
      <p>МАРКЕР СНОСКИ БЕЗ СЕКЦИЙ</p>
    </section>
  </body>
</FictionBook>`

	zipPath := zipWithSingleFB2(t, []byte(fixture))
	processor := NewBookProcessor("book.fb2", zipPath)

	epubReader, err := processor.Epub()
	if err != nil {
		t.Fatalf("EPUB conversion failed: %v", err)
	}
	defer epubReader.Close()

	epubData, err := io.ReadAll(epubReader)
	if err != nil {
		t.Fatalf("Failed to read EPUB: %v", err)
	}
	files := unzipToMap(t, epubData)

	// The paragraph survives in the spine.
	chapterNames := spineChapters(t, files)
	var chapters strings.Builder
	for _, name := range chapterNames {
		chapters.Write(files["OEBPS/"+name])
	}
	if !strings.Contains(chapters.String(), "МАРКЕР ЕДИНСТВЕННОГО АБЗАЦА") {
		t.Error("Sectionless book text is missing from the spine chapters")
	}

	// The TOC is not empty: exactly the title page and the notes page.
	navEntries := parseNav(t, files)
	if len(navEntries) != 2 {
		t.Fatalf("TOC has %d entries, expected exactly 2 (title page + notes): %v", len(navEntries), navEntries)
	}
	if navEntries[0].Title != "Author Regression Sectionless Book" {
		t.Errorf("TOC entry 0 is '%s', expected the title page", navEntries[0].Title)
	}
	if navEntries[1].Title != "Примечания" {
		t.Errorf("TOC entry 1 is '%s', expected 'Примечания'", navEntries[1].Title)
	}

	// The footnote link still resolves into the notes page.
	if !strings.Contains(chapters.String(), `href="notes.xhtml#note-note1"`) {
		t.Error("Footnote link does not point at notes.xhtml#note-note1")
	}
	checkAllLinksResolve(t, files)
}

// TestEpubConversion_ImageOnlyParagraphsSurvive pins the shared-parser defect
// on the download path: a paragraph holding nothing but an <image> was dropped
// together with the image, so the EPUB embedded the picture bytes and no
// chapter referenced them. Both source forms — a bare <p><image/></p> and an
// emphasis-wrapped one — must reach the spine as <img> references at their
// original positions, while a truly empty paragraph must not turn into an
// empty <p>.
func TestEpubConversion_ImageOnlyParagraphsSurvive(t *testing.T) {
	fixture := `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0" xmlns:l="http://www.w3.org/1999/xlink">
  <description>
    <title-info>
      <genre>prose</genre>
      <author>
        <first-name>Regression</first-name>
        <last-name>Author</last-name>
      </author>
      <book-title>Image Paragraphs Book</book-title>
      <lang>ru</lang>
    </title-info>
  </description>
  <body>
    <section id="ch1">
      <title><p>Глава с иллюстрациями</p></title>
      <p>МАРКЕР ДО КАРТИНКИ</p>
      <p>
        <image l:href="#img1"/>
      </p>
      <p>МАРКЕР МЕЖДУ КАРТИНКАМИ</p>
      <p><emphasis><image l:href="#img2"/></emphasis></p>
      <p></p>
      <p>МАРКЕР ПОСЛЕ КАРТИНОК</p>
    </section>
  </body>
  <binary id="img1" content-type="image/png">` + fixtureImageB64 + `</binary>
  <binary id="img2" content-type="image/png">` + fixtureImageB64 + `</binary>
</FictionBook>`

	zipPath := zipWithSingleFB2(t, []byte(fixture))
	processor := NewBookProcessor("book.fb2", zipPath)

	epubReader, err := processor.Epub()
	if err != nil {
		t.Fatalf("EPUB conversion failed: %v", err)
	}
	defer epubReader.Close()

	epubData, err := io.ReadAll(epubReader)
	if err != nil {
		t.Fatalf("Failed to read EPUB: %v", err)
	}
	files := unzipToMap(t, epubData)

	chapterNames := spineChapters(t, files)
	var chapters strings.Builder
	for _, name := range chapterNames {
		chapters.Write(files["OEBPS/"+name])
		chapters.WriteByte('\n')
	}
	chapterText := chapters.String()

	// Both images are referenced from the spine, each exactly once. The ids
	// sort to image_001/image_002, and the source order must survive: img1
	// between the first two markers, img2 between the last two.
	imgSrcRe := regexp.MustCompile(`<img src="(images/image_\d+\.png)"`)
	var imgRefs []string
	for _, match := range imgSrcRe.FindAllStringSubmatch(chapterText, -1) {
		imgRefs = append(imgRefs, match[1])
	}
	if len(imgRefs) != 2 {
		t.Fatalf("Chapters reference %d images, expected exactly 2 (both image-only paragraphs dropped?)", len(imgRefs))
	}
	if imgRefs[0] != "images/image_001.png" || imgRefs[1] != "images/image_002.png" {
		t.Errorf("Image references out of source order: %v", imgRefs)
	}

	position := func(needle string) int {
		idx := strings.Index(chapterText, needle)
		if idx == -1 {
			t.Fatalf("'%s' missing from chapters", needle)
		}
		return idx
	}
	before := position("МАРКЕР ДО КАРТИНКИ")
	img1 := position(`<img src="images/image_001.png"`)
	middle := position("МАРКЕР МЕЖДУ КАРТИНКАМИ")
	img2 := position(`<img src="images/image_002.png"`)
	after := position("МАРКЕР ПОСЛЕ КАРТИНОК")
	if before >= img1 || img1 >= middle || middle >= img2 || img2 >= after {
		t.Errorf("Reading order broken: before=%d img1=%d middle=%d img2=%d after=%d",
			before, img1, middle, img2, after)
	}

	// Both pictures are embedded with their original bytes and declared in
	// the manifest: a reference without the payload is a broken image.
	expectedImage, err := base64.StdEncoding.DecodeString(fixtureImageB64)
	if err != nil {
		t.Fatalf("Fixture image base64 does not decode: %v", err)
	}
	pkg := parseOPF(t, files)
	for _, ref := range imgRefs {
		data, ok := files["OEBPS/"+ref]
		if !ok {
			t.Errorf("img src '%s' has no file in the archive", ref)
			continue
		}
		if !bytes.Equal(data, expectedImage) {
			t.Errorf("Embedded image '%s' bytes differ from the source", ref)
		}
		declared := false
		for _, item := range pkg.Manifest.Items {
			if item.Href == ref {
				declared = true
			}
		}
		if !declared {
			t.Errorf("img src '%s' has no manifest entry", ref)
		}
	}

	// The truly empty paragraph between the markers must not render as an
	// empty <p>: keeping it would trade lost pictures for layout noise.
	if strings.Contains(chapterText, "<p></p>") {
		t.Error("An empty source paragraph rendered as an empty <p>")
	}

	checkAllLinksResolve(t, files)
}
