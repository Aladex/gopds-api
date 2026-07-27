package main

import (
	assets "gopds-api"
	"slices"
	"testing"
)

// TestInitializeDistFolders verifies the embedded frontend tree can be walked.
// It fails when the embed input is missing or has an unexpected layout.
func TestInitializeDistFolders(t *testing.T) {
	distFolders = nil
	t.Cleanup(func() { distFolders = nil })

	if err := initializeDistFolders(); err != nil {
		t.Fatalf("initializeDistFolders() = %v, want nil", err)
	}
}

// TestListRootFilesIncludesIndex verifies the SPA entry point is discoverable
// among the embedded root files, which is what the static routes are built from.
func TestListRootFilesIncludesIndex(t *testing.T) {
	files := listRootFiles()
	if len(files) == 0 {
		t.Fatal("listRootFiles() returned no files")
	}
	if !slices.Contains(files, "/index.html") {
		t.Fatalf("listRootFiles() = %v, want it to contain %q", files, "/index.html")
	}
}

// TestNewHTTPFSOpensIndex verifies the http.FileSystem wrapper resolves the SPA
// entry point through the embedded assets.
func TestNewHTTPFSOpensIndex(t *testing.T) {
	f, err := NewHTTPFS(assets.Assets).Open("booksdump-frontend/build/index.html")
	if err != nil {
		t.Fatalf("opening embedded index.html: %v", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		t.Fatalf("stat embedded index.html: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("embedded index.html is empty")
	}
}
