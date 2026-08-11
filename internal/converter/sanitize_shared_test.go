package converter

import (
	"bytes"
	"context"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopds-api/internal/parser"
)

// sharedSanitizerNames lists the sanitizer functions (normalized: lowercase,
// "sanitize" prefix stripped) that must have exactly one implementation in the
// whole tree. Duplicated copies of this code have already produced two real
// defects that were fixed in one copy and not the other; a second copy is a
// defect by itself, before any byte diverges.
var sharedSanitizerNames = []string{
	// The broken-self-closing repair, the block that diverged twice.
	"brokenselfclosingtags",
	"ispartofselfclosingtag",
	"hasattributebefore",
	// The rest of the ordered prefix all three entry points run.
	"controlchars",
	"invalidtagopenings",
	"islikelyxmltagstart",
	"invalidprocessinginstructions",
	"hasprefixfold",
	"invalidampersands",
	"isvalidentity",
	"xmlversion",
	"brokenendtags",
	"brokenlangtag",
	"missingxlinkprefix",
	"isnamechar",
	"isxmlspace",
	"isxmlnamebyte",
	"extracttagnameat",
}

// moduleRoot ascends from the test working directory (go test runs the
// binary in the package directory) to the directory holding go.mod. Deriving
// the root from the working directory rather than runtime.Caller keeps the
// test working under -trimpath, where debug paths are relative.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getting working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test working directory")
		}
		dir = parent
	}
}

func normalizeSanitizerName(name string) string {
	return strings.TrimPrefix(strings.ToLower(name), "sanitize")
}

// TestSharedSanitizersHaveSingleImplementation walks every non-test Go file
// of the module and fails while any function of the shared repair chain is
// defined in more than one place. Two agreeing copies prove nothing: the
// next fix lands in one of them, and the two halves of the system start
// answering differently on the same book — which is exactly what happened
// twice with sanitizeBrokenSelfClosingTags. This is a name-based guard over
// top-level function declarations: it catches a copied function kept under
// its own name in any package of the module; a method, a function-valued
// variable, or a reimplementation under a new name is beyond its reach.
func TestSharedSanitizersHaveSingleImplementation(t *testing.T) {
	wanted := make(map[string]bool, len(sharedSanitizerNames))
	for _, name := range sharedSanitizerNames {
		wanted[name] = true
	}
	found := make(map[string][]string)

	root := moduleRoot(t)
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			// Skip hidden trees and directories that cannot hold module Go
			// code (the frontend and dependency caches).
			if path != root && (strings.HasPrefix(name, ".") ||
				name == "booksdump-frontend" || name == "node_modules" || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		file, err := goparser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv != nil {
				continue
			}
			funcName := normalizeSanitizerName(fd.Name.Name)
			if wanted[funcName] {
				found[funcName] = append(found[funcName], rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the module tree: %v", err)
	}

	for _, name := range sharedSanitizerNames {
		locations := found[name]
		if len(locations) != 1 {
			t.Errorf("%q must have exactly one implementation, found %d: %v",
				name, len(locations), locations)
		}
	}
}

// agreementFixture carries the three damages the duplicated sanitizer
// answered differently in production:
//   - a prose slash before markup in the annotation (must stay text);
//   - an attribute-carrying tag that lost its closing bracket (must be
//     repaired even though the tag is not on the self-closing whitelist);
//   - a whitelisted tag that lost its bracket right before </section>
//     (the closing tag must be kept, or sibling sections become nested).
const agreementFixture = `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0" xmlns:xlink="http://www.w3.org/1999/xlink">
<description><title-info>
<genre>prose</genre>
<author><first-name>Иван</first-name><last-name>Тестов</last-name></author>
<book-title>Проверка</book-title>
<annotation><p>Цена 100 руб. / <emphasis>скидка</emphasis> есть</p></annotation>
<lang>ru</lang>
<sequence name="Серия" number="7" /</title-info>
</description>
<body>
<title><p>Книга</p></title>
<section><title><p>Глава 1</p></title><p>МАРКЕР ОДИН</p><image xlink:href="#pic.png"/</section>
<section><title><p>Глава 2</p></title><p>МАРКЕР ДВА</p></section>
</body>
</FictionBook>`

const wantAgreementAnnotation = "Цена 100 руб. / скидка есть"

// assertAgreementMetadata checks the metadata outcomes that depend on the
// repair: the annotation kept its prose slash, and the series survived the
// attribute-tag repair. Empty Issues pins the strict parse: the fallback path
// (a masked repair failure) tags the book with "parsed_without_body".
func assertAgreementMetadata(t *testing.T, entry string, book *parser.BookFile) {
	t.Helper()
	if len(book.Issues) != 0 {
		t.Errorf("%s: expected a clean strict parse, got issues %v", entry, book.Issues)
	}
	if book.Annotation != wantAgreementAnnotation {
		t.Errorf("%s: annotation = %q, want %q", entry, book.Annotation, wantAgreementAnnotation)
	}
	if book.Title != "Проверка" {
		t.Errorf("%s: title = %q, want %q", entry, book.Title, "Проверка")
	}
	if book.Series == nil || book.Series.Title != "Серия" || book.Series.Index != "7" {
		t.Errorf("%s: series = %+v, want Title=Серия Index=7", entry, book.Series)
	}
	for _, marker := range []string{"МАРКЕР ОДИН", "МАРКЕР ДВА"} {
		if !strings.Contains(book.BodySample, marker) {
			t.Errorf("%s: body sample lost %q: %q", entry, marker, book.BodySample)
		}
	}
}

// assertAgreementDocument checks the structural outcome: two sibling
// sections, not a nested pair (the swallowed-</section> defect), and not the
// single flat section the loose salvage produces when the strict parse dies
// on an unrepaired tag.
func assertAgreementDocument(t *testing.T, entry string, doc *FB2Document) {
	t.Helper()
	if doc == nil || doc.Body == nil {
		t.Fatalf("%s: no document body", entry)
	}
	sections := doc.Body.SubSections()
	if len(sections) != 2 {
		t.Fatalf("%s: got %d top-level sections, want 2 siblings", entry, len(sections))
	}
	if sections[0].Title != "Глава 1" || sections[1].Title != "Глава 2" {
		t.Errorf("%s: section titles = %q, %q; want Глава 1, Глава 2",
			entry, sections[0].Title, sections[1].Title)
	}
	if len(sections[0].SubSections()) != 0 {
		t.Errorf("%s: first section swallowed its sibling: %d nested sections",
			entry, len(sections[0].SubSections()))
	}
}

// TestSanitizerAgreementAcrossEntryPoints drives the same damaged book
// through all three production entry points — the catalog scanner
// (FB2Parser.Parse) and both converter parsers — and requires the exact same
// answers. This is the contract the extraction must keep: one repair, three
// callers.
func TestSanitizerAgreementAcrossEntryPoints(t *testing.T) {
	content := []byte(agreementFixture)

	t.Run("FB2Parser.Parse", func(t *testing.T) {
		book, err := parser.NewFB2Parser(false).Parse(bytes.NewReader(content))
		if err != nil {
			t.Fatalf("Parse failed: %v", err)
		}
		assertAgreementMetadata(t, "Parse", book)
	})

	t.Run("ParseFB2Complete", func(t *testing.T) {
		doc, book, err := ParseFB2Complete(context.Background(), content, false)
		if err != nil {
			t.Fatalf("ParseFB2Complete failed: %v", err)
		}
		assertAgreementMetadata(t, "ParseFB2Complete", book)
		assertAgreementDocument(t, "ParseFB2Complete", doc)
	})

	t.Run("ParseFB2Body", func(t *testing.T) {
		doc, err := ParseFB2Body(context.Background(), content)
		if err != nil {
			t.Fatalf("ParseFB2Body failed: %v", err)
		}
		assertAgreementDocument(t, "ParseFB2Body", doc)
	})
}
