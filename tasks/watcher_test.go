package tasks

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The sweep deletes files. That is worth tests of its own: the package had
// none, and the failure mode is silent and permanent.

// write creates a file and backdates it by age.
func write(t *testing.T, dir, name string, age time.Duration) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	stamp := time.Now().Add(-age)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatalf("backdating %s: %v", path, err)
	}
	return path
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	return err == nil
}

func TestSweepRemovesOnlyWhatIsOldEnough(t *testing.T) {
	dir := t.TempDir()

	stale := write(t, dir, "stale.mobi", 2*time.Hour)
	fresh := write(t, dir, "fresh.mobi", time.Minute)

	if removed := sweep(dir); removed != 1 {
		t.Errorf("sweep removed %d files, want 1", removed)
	}
	if exists(t, stale) {
		t.Error("a file older than the limit survived")
	}
	if !exists(t, fresh) {
		t.Error("a file younger than the limit was deleted")
	}
}

func TestSweepReachesIntoSubdirectories(t *testing.T) {
	dir := t.TempDir()

	nested := write(t, dir, filepath.Join("a", "b", "old.epub"), 3*time.Hour)

	if removed := sweep(dir); removed != 1 {
		t.Errorf("sweep removed %d files, want 1", removed)
	}
	if exists(t, nested) {
		t.Error("a stale file in a subdirectory survived")
	}
}

func TestSweepLeavesDirectoriesAlone(t *testing.T) {
	dir := t.TempDir()

	sub := filepath.Join(dir, "empty")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatalf("creating %s: %v", sub, err)
	}
	stamp := time.Now().Add(-5 * time.Hour)
	if err := os.Chtimes(sub, stamp, stamp); err != nil {
		t.Fatalf("backdating %s: %v", sub, err)
	}

	sweep(dir)

	if !exists(t, sub) {
		t.Error("an old directory was removed")
	}
}

// A symbolic link is walked as the link, not as its target. Removing one would
// take out whatever had been linked into the scratch directory, which is not
// the sweep's to delete.
func TestSweepDoesNotFollowOrRemoveSymlinks(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()

	target := write(t, outside, "precious.fb2", 5*time.Hour)

	link := filepath.Join(dir, "link.fb2")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	stamp := time.Now().Add(-5 * time.Hour)
	if err := os.Chtimes(link, stamp, stamp); err != nil {
		t.Fatalf("backdating the link: %v", err)
	}

	sweep(dir)

	if !exists(t, target) {
		t.Fatal("the file a symlink pointed at was deleted")
	}
	if !exists(t, link) {
		t.Error("the symlink itself was deleted")
	}
}

func TestSweepSurvivesAMissingDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "never-created")

	if removed := sweep(missing); removed != 0 {
		t.Errorf("sweep reported %d removals from a directory that is not there", removed)
	}
}

func TestSweepOnAnEmptyDirectoryRemovesNothing(t *testing.T) {
	if removed := sweep(t.TempDir()); removed != 0 {
		t.Errorf("sweep removed %d files from an empty directory", removed)
	}
}

// WatchDirectory loops on a ticker forever, so what is testable is its refusal
// to start. An empty path would aim the sweep at the working directory, where
// conversion keeps its own temporary files.
func TestWatchDirectoryRefusesAnEmptyPath(t *testing.T) {
	done := make(chan struct{})
	go func() {
		WatchDirectory("   ", time.Millisecond)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WatchDirectory started watching with no directory configured")
	}
}
