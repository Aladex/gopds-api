package archivestore

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopds-api/internal/objectstore"
)

func tempCopies(t *testing.T, s *Store) []string {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(s.tempDir, "*"))
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func TestWholeArchiveCopyIsRemovedAfterUse(t *testing.T) {
	data := buildArchive(t, 20, 1000, 40)
	fx := newFixture(t, &Config{}, data)

	archive, err := fx.store.OpenWhole(context.Background(), fx.path)
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.File) != 20 {
		t.Fatalf("%d entries", len(archive.File))
	}
	if n := len(tempCopies(t, fx.store)); n != 1 {
		t.Fatalf("%d copies while in use", n)
	}
	rc, err := archive.File[3].Open()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, rc); err != nil {
		t.Fatal(err)
	}
	_ = rc.Close()
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal("a second Close failed:", err)
	}

	if names := tempCopies(t, fx.store); len(names) != 0 {
		t.Fatalf("left %v", names)
	}
	if _, err := os.Stat(fx.path); !os.IsNotExist(err) {
		t.Fatal("the copy landed where a local archive would be")
	}
}

func TestWholeArchiveCopyIsRemovedOnFailure(t *testing.T) {
	data := buildArchive(t, 200, 1000, 41)
	cases := map[string]func(*fixture){
		"store goes down": func(fx *fixture) { fx.server.FailNext(1000) },
		"not a zip": func(fx *fixture) {
			fx.server.Put(testPrefix+testArchive, []byte("this is not a zip archive"))
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newFixture(t, &Config{}, data)
			breakIt(fx)
			_, err := fx.store.OpenWhole(context.Background(), fx.path)
			if err == nil {
				t.Fatal("a broken fetch succeeded")
			}
			if names := tempCopies(t, fx.store); len(names) != 0 {
				t.Fatalf("left %v", names)
			}
			// The slot came back with the failure: the next archive goes on.
			fx.server.FailNext(0)
			fx.server.Put(testPrefix+testArchive, data)
			archive, err := fx.store.OpenWhole(context.Background(), fx.path)
			if err != nil {
				t.Fatalf("the next fetch failed: %v", err)
			}
			_ = archive.Close()
		})
	}
}

func TestWholeArchiveCopyIsRemovedWhenTheStreamDies(t *testing.T) {
	fx := newFixture(t, &Config{}, buildArchive(t, 200, 1000, 42))
	fx.server.DropAfter(1000)
	archive, err := fx.store.OpenWhole(context.Background(), fx.path)
	if err == nil {
		// Each cut segment still makes progress, so the copy completes in
		// pieces; that is fine as long as it is whole and cleaned up.
		if len(archive.File) != 200 {
			t.Fatalf("%d entries", len(archive.File))
		}
		_ = archive.Close()
	}
	if names := tempCopies(t, fx.store); len(names) != 0 {
		t.Fatalf("left %v", names)
	}
}

func TestWholeArchiveChangingDuringACopyFails(t *testing.T) {
	data := buildArchive(t, 200, 1000, 43)
	fx := newFixture(t, &Config{}, data)
	info, err := fx.store.client.Head(context.Background(), testPrefix+testArchive)
	if err != nil {
		t.Fatal(err)
	}
	info.ETag = "something-older"
	file, err := os.CreateTemp(t.TempDir(), "copy")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	err = fx.store.download(context.Background(), file, info)
	if !errors.Is(err, objectstore.ErrChanged) {
		t.Fatalf("err = %v, want ErrChanged", err)
	}
}

func TestWholeArchiveOverTheBudgetIsRefused(t *testing.T) {
	fx := newFixture(t, &Config{TempBudgetBytes: 100}, buildArchive(t, 20, 1000, 44))
	_, err := fx.store.OpenWhole(context.Background(), fx.path)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if names := tempCopies(t, fx.store); len(names) != 0 {
		t.Fatalf("left %v", names)
	}
}

func TestWholeArchivesAreCopiedOneAtATime(t *testing.T) {
	fx := newFixture(t, &Config{}, buildArchive(t, 20, 1000, 45))
	first, err := fx.store.OpenWhole(context.Background(), fx.path)
	if err != nil {
		t.Fatal(err)
	}

	waiting, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, waitErr := fx.store.OpenWhole(waiting, fx.path); !errors.Is(waitErr, context.DeadlineExceeded) {
		t.Fatalf("a second copy was not held back: %v", waitErr)
	}
	if n := len(tempCopies(t, fx.store)); n != 1 {
		t.Fatalf("%d copies at once", n)
	}

	_ = first.Close()
	second, err := fx.store.OpenWhole(context.Background(), fx.path)
	if err != nil {
		t.Fatal(err)
	}
	_ = second.Close()
}

func TestStartClearsLeftoverCopies(t *testing.T) {
	cacheDir := t.TempDir()
	tmp := filepath.Join(cacheDir, "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	leftover := filepath.Join(tmp, "crashed"+tempSuffix)
	if err := os.WriteFile(leftover, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fx := newFixture(t, &Config{}, buildArchive(t, 1, 10, 46))
	if _, err := New(&Config{Client: fx.store.client, Root: fx.root, CacheDir: cacheDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Fatal("the leftover copy survived a start")
	}
}

func TestOpenBatchChoosesByHowManyEntries(t *testing.T) {
	fx := newFixture(t, &Config{}, buildArchive(t, 20, 1000, 47))
	few, err := fx.store.OpenBatch(context.Background(), fx.path, wholeFetchMinEntries-1)
	if err != nil {
		t.Fatal(err)
	}
	if few.ranged == nil {
		t.Error("a few entries made a whole copy")
	}
	_ = few.Close()
	many, err := fx.store.OpenBatch(context.Background(), fx.path, wholeFetchMinEntries)
	if err != nil {
		t.Fatal(err)
	}
	if many.ranged != nil {
		t.Error("many entries went by ranges")
	}
	_ = many.Close()
}
