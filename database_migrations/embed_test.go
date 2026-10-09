package migrations

import (
	"bytes"
	"io/fs"
	"os"
	"sort"
	"strings"
	"testing"
)

// sqlNames lists the .sql files at the root of fsys, sorted.
func sqlNames(t *testing.T, fsys fs.FS) []string {
	t.Helper()
	entries, err := fs.ReadDir(fsys, Dir)
	if err != nil {
		t.Fatalf("listing migrations: %v", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// The binary carries every migration on disk, byte for byte. A file left out
// of the embed would never run on a server start, and the ledger would not
// even know it exists; a stale copy would run the wrong statements.
func TestEmbeddedMigrationsMatchTheDirectory(t *testing.T) {
	onDisk := os.DirFS(".")
	want := sqlNames(t, onDisk)
	got := sqlNames(t, FS())

	if len(want) == 0 {
		t.Fatal("no .sql files on disk: the comparison would prove nothing")
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("embedded migrations differ from the directory\nembedded: %v\non disk:  %v", got, want)
	}
	for _, name := range want {
		disk, err := fs.ReadFile(onDisk, name)
		if err != nil {
			t.Fatalf("reading %s from disk: %v", name, err)
		}
		embedded, err := fs.ReadFile(FS(), name)
		if err != nil {
			t.Fatalf("reading %s from the binary: %v", name, err)
		}
		if !bytes.Equal(disk, embedded) {
			t.Errorf("%s: embedded content differs from the file on disk", name)
		}
	}
}

// Only migrations travel in the binary: the initdb helper script beside them
// is not a migration and must never be fed to the runner.
func TestEmbeddedMigrationsHoldOnlySQL(t *testing.T) {
	entries, err := fs.ReadDir(FS(), Dir)
	if err != nil {
		t.Fatalf("listing migrations: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			t.Errorf("embedded a non-migration entry %q", e.Name())
		}
	}
}
