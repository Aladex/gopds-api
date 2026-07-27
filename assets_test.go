package assets

import (
	"io/fs"
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
