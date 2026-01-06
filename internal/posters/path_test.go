package posters

import (
	"path/filepath"
	"testing"
)

func TestRelativePath(t *testing.T) {
	got := RelativePath("/dir/sub.book", "name.fb2")
	want := "dir/sub-book/name-fb2.jpg"
	if got != want {
		t.Fatalf("RelativePath() = %q, want %q", got, want)
	}
}

func TestFilePath(t *testing.T) {
	got := FilePath("/posters", "dir/sub.book", "name.fb2")
	want := filepath.Join("/posters", "dir/sub-book/name-fb2.jpg")
	if got != want {
		t.Fatalf("FilePath() = %q, want %q", got, want)
	}
}
