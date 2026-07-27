package assets

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// countFilesUnder walks the embedded FS and counts regular files under root.
func countFilesUnder(t *testing.T, root string) int {
	t.Helper()

	count := 0
	err := fs.WalkDir(Assets, root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %q in embedded assets: %v", root, err)
	}
	return count
}

// TestEmbeddedAssetsContainFrontendIndex guards the embed contract between the
// frontend build output directory and the Go binary. The binary must always be
// buildable from a clean checkout, with either a real build or a placeholder.
func TestEmbeddedAssetsContainFrontendIndex(t *testing.T) {
	const indexPath = "booksdump-frontend/build/index.html"

	info, err := fs.Stat(Assets, indexPath)
	if err != nil {
		t.Fatalf("embedded assets must contain %q: %v", indexPath, err)
	}
	if info.Size() == 0 {
		t.Fatalf("embedded %q is empty", indexPath)
	}
}

// placeholderMarker identifies the development stand-in installed by
// `make frontend-placeholder` when no real frontend build exists.
const placeholderMarker = "gopds development placeholder"

// TestEmbeddedFrontendReferencesItsAssets guards the contract between whatever
// bundler builds the frontend and the Go binary that embeds it: index.html must
// reference a script, and that script must actually be embedded alongside it.
// A bundler writing its output elsewhere produces an index.html pointing at
// files that never made it into the binary, which only shows up at runtime as a
// blank page.
func TestEmbeddedFrontendReferencesItsAssets(t *testing.T) {
	const buildDir = "booksdump-frontend/build"

	index, err := fs.ReadFile(Assets, buildDir+"/index.html")
	if err != nil {
		t.Fatalf("reading embedded index.html: %v", err)
	}
	if strings.Contains(string(index), placeholderMarker) {
		t.Skip("placeholder build embedded; run `make build-frontend` to check the real asset contract")
	}

	scriptSrc := regexp.MustCompile(`<script[^>]+src="([^"]+\.js)"`)
	matches := scriptSrc.FindAllStringSubmatch(string(index), -1)
	if len(matches) == 0 {
		t.Fatal("embedded index.html references no script")
	}

	for _, m := range matches {
		ref := strings.TrimPrefix(m[1], "/")
		if strings.HasPrefix(ref, "http") {
			continue // externally hosted, not our concern
		}
		if _, err := fs.Stat(Assets, buildDir+"/"+ref); err != nil {
			t.Errorf("index.html references %q, which is not embedded: %v", m[1], err)
		}
	}
}

// TestEmbeddedAssetsContainEmailTemplates ensures email templates stay embedded.
func TestEmbeddedAssetsContainEmailTemplates(t *testing.T) {
	if got := countFilesUnder(t, "email/templates"); got == 0 {
		t.Fatal("embedded assets contain no email templates")
	}
}

// TestEmbeddedAssetsContainStaticAssets ensures static assets stay embedded.
func TestEmbeddedAssetsContainStaticAssets(t *testing.T) {
	if got := countFilesUnder(t, "static_assets"); got == 0 {
		t.Fatal("embedded assets contain no static assets")
	}
}
