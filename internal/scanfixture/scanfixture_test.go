package scanfixture

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const importPath = "gopds-api/internal/scanfixture"

// TestOnlyTestFilesImportScanfixture keeps the fixture out of the production
// binary: it creates databases and writes archives, and no request path may
// ever reach it.
func TestOnlyTestFilesImportScanfixture(t *testing.T) {
	root := ModuleRoot()
	var offenders []string
	var scanned int
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "booksdump-frontend", "vendor", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		scanned++
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		for _, imp := range file.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == importPath {
				rel, _ := filepath.Rel(root, path)
				offenders = append(offenders, rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if scanned < 100 {
		t.Fatalf("scanned only %d Go files under %s: the walk is not seeing the repository", scanned, root)
	}
	if len(offenders) > 0 {
		t.Fatalf("non-test files import %s: %v", importPath, offenders)
	}
}

// The probe above must be able to fail: a non-test file with the import is
// reported.
func TestImportProbeSeesAnImport(t *testing.T) {
	dir := t.TempDir()
	src := "package probe\n\nimport _ \"" + importPath + "\"\n"
	path := filepath.Join(dir, "probe.go")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := strconv.Unquote(file.Imports[0].Path.Value); p != importPath {
		t.Fatalf("parsed import %q", p)
	}
}
