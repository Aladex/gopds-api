// Package scanfixture is test support for the characterization of the
// new-book scan path and of the reader outputs built on top of it.
//
// The characterization tests pin what legacy ingestion writes and what REST,
// OPDS and the Telegram bot render from it, so that the transactional dual
// write of author metadata (phase 9) can be shown to change none of it. They
// need books written by the real ProcessBook, committed, in a database nobody
// else uses: this package creates a scratch database from the real migration
// files, generates a small fixed set of FB2 files and runs them through a
// ProcessBook function the caller supplies.
//
// Only _test files may import this package; TestOnlyTestFilesImportScanfixture
// enforces that. It deliberately does not import services, so the services
// package's own tests can use it without an import cycle.
package scanfixture

import (
	"archive/zip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/migrate"
	"gopds-api/internal/testdb"

	"github.com/go-pg/pg/v10"
)

// ArchiveName is the archive the characterization books are ingested from.
const ArchiveName = "characterization.zip"

// Entry names of the characterization books, in ingestion order.
const (
	// EntryMultiAuthor has four title-info authors whose name parts do not
	// line up, a translator, a document-info author, a series and two genres.
	EntryMultiAuthor = "multi-author.fb2"
	// EntryNoAuthor has a translator and a document-info author but no
	// title-info author.
	EntryNoAuthor = "no-author.fb2"
	// EntryLatin has Latin names in mixed case, a declared language that
	// differs from its body text and a non-numeric series number.
	EntryLatin = "latin-mismatch.fb2"
)

// Entries lists the characterization books in ingestion order.
func Entries() []string {
	return []string{EntryMultiAuthor, EntryNoAuthor, EntryLatin}
}

// ModuleRoot is the repository root, found from this file's location.
func ModuleRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("scanfixture: cannot locate its own source file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// ScratchDB creates a database on the configured server, migrates it from the
// real files and installs it as the database package's global connection for
// the duration of the test. Both are undone on cleanup, also when the test
// fails. Without a configured database, or in short mode, the test is skipped.
func ScratchDB(t testing.TB) *pg.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	cfg, ok := testdb.Configured()
	if !ok {
		t.Skip(testdb.SkipReason)
	}
	admin, err := testdb.Connect(cfg, nil)
	if err != nil {
		t.Fatalf("connecting to the configured database: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })

	name := fmt.Sprintf("scan_characterization_%d", time.Now().UnixNano())
	if _, err = admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatalf("creating the scratch database: %v", err)
	}
	// Registered before the scratch connection's own cleanup, so it runs
	// after it; FORCE ends whatever a failed test left connected.
	t.Cleanup(func() {
		if _, dropErr := admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)"); dropErr != nil {
			t.Errorf("dropping the scratch database %s: %v", name, dropErr)
		}
	})

	scratch := pg.Connect(&pg.Options{Addr: cfg.Host, User: cfg.User, Password: cfg.Password, Database: name})
	t.Cleanup(func() { _ = scratch.Close() })

	if _, err = migrate.Run(context.Background(), scratch, os.DirFS(ModuleRoot()),
		"database_migrations", migrate.AppBaseline()); err != nil {
		t.Fatalf("migrating the scratch database: %v", err)
	}
	// The one column the production catalog has and the migration files do
	// not create (found by diffing information_schema of a fresh migration
	// against the restored catalog): genre titles. models.Genre and
	// database.UpdateBookTags write it, so without it ProcessBook fails on
	// every book with a genre. The characterization has to see the schema
	// production runs on; the missing migration is reported separately.
	if _, err = scratch.Exec(`ALTER TABLE public.opds_catalog_genre
		ADD COLUMN IF NOT EXISTS title character varying NOT NULL DEFAULT ''`); err != nil {
		t.Fatalf("adding the production-only genre title column: %v", err)
	}

	previous := database.GetDB()
	database.SetDB(scratch)
	t.Cleanup(func() { database.SetDB(previous) })
	return scratch
}

// Books returns the characterization FB2 files, keyed by entry name. The
// only date in them, the document date, is the current year.
func Books(now time.Time) map[string][]byte {
	year := now.Year()
	return map[string][]byte{
		EntryMultiAuthor: []byte(fmt.Sprintf(multiAuthorFB2, year)),
		EntryNoAuthor:    []byte(fmt.Sprintf(noAuthorFB2, year)),
		EntryLatin:       []byte(fmt.Sprintf(latinFB2, year)),
	}
}

// WriteArchive writes the characterization books into dir/ArchiveName in
// ingestion order and returns its path.
func WriteArchive(t testing.TB, dir string, now time.Time) string {
	t.Helper()
	path := filepath.Join(dir, ArchiveName)
	// #nosec G304 -- a test fixture writing a fixed file name into the caller's test directory
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating the fixture archive: %v", err)
	}
	w := zip.NewWriter(f)
	books := Books(now)
	for _, entry := range Entries() {
		zw, createErr := w.Create(entry)
		if createErr != nil {
			t.Fatalf("adding %s: %v", entry, createErr)
		}
		if _, writeErr := zw.Write(books[entry]); writeErr != nil {
			t.Fatalf("writing %s: %v", entry, writeErr)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatalf("closing the fixture archive: %v", err)
	}
	if err = f.Close(); err != nil {
		t.Fatalf("closing the fixture file: %v", err)
	}
	return path
}

// ProcessBook is the signature of services.(*BookScanService).ProcessBook.
type ProcessBook func(zipFile *zip.File, archiveName string) (int64, error)

// Ingest writes the characterization archive into dir and runs every entry
// through process, in order. It returns the new book IDs by entry name.
func Ingest(t testing.TB, dir string, now time.Time, process ProcessBook) map[string]int64 {
	t.Helper()
	reader, err := zip.OpenReader(WriteArchive(t, dir, now))
	if err != nil {
		t.Fatalf("opening the fixture archive: %v", err)
	}
	defer func() { _ = reader.Close() }()

	ids := make(map[string]int64, len(reader.File))
	for _, file := range reader.File {
		id, processErr := process(file, ArchiveName)
		if processErr != nil {
			t.Fatalf("ingesting %s: %v", file.Name, processErr)
		}
		ids[file.Name] = id
	}
	return ids
}

// The fixtures stay tiny: a description and one short paragraph of body text,
// enough for the language detector's twenty-rune minimum.

const multiAuthorFB2 = `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0" xmlns:l="http://www.w3.org/1999/xlink">
<description>
<title-info>
<genre>prose_classic</genre>
<genre> PROSE_HISTORY </genre>
<author><first-name>лев</first-name><middle-name>Николаевич</middle-name><last-name>ТОЛСТОЙ</last-name></author>
<author><last-name>пушкин</last-name></author>
<author><first-name>Анна</first-name></author>
<author><nickname>Аноним</nickname></author>
<book-title>война  и   МИР</book-title>
<annotation><p>Роман-эпопея о войне и мире.</p></annotation>
<lang>ru</lang>
<src-lang>fr</src-lang>
<translator><first-name>Пётр</first-name><last-name>Переводчиков</last-name></translator>
<sequence name="Эпопея" number="2"/>
</title-info>
<document-info>
<author><nickname>верстальщик</nickname></author>
<date>%d</date>
<id>characterization-multi</id>
<version>1.0</version>
</document-info>
</description>
<body><section><p>Ещё в начале июля, в жаркое время, под вечер, молодой человек вышел из каморки.</p></section></body>
</FictionBook>
`

const noAuthorFB2 = `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description>
<title-info>
<genre>antology</genre>
<book-title>Сборник без автора</book-title>
<lang>ru</lang>
<translator><first-name>Мария</first-name><last-name>Переводова</last-name></translator>
</title-info>
<document-info>
<author><first-name>Иван</first-name><last-name>Составитель</last-name></author>
<date>%d</date>
<id>characterization-no-author</id>
</document-info>
</description>
<body><section><p>Сборник рассказов разных лет, собранных в одну небольшую книгу для чтения.</p></section></body>
</FictionBook>
`

const latinFB2 = `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description>
<title-info>
<genre>sf</genre>
<author><first-name>jOHN</first-name><last-name>o'BRIEN</last-name></author>
<author><first-name>ЛЕВ</first-name><last-name>толстой</last-name></author>
<book-title>the GREAT book</book-title>
<lang>ru</lang>
<sequence name="Saga" number="first"/>
</title-info>
<document-info>
<date>%d</date>
<id>characterization-latin</id>
</document-info>
</description>
<body><section><p>It was a bright cold day in April, and the clocks were striking thirteen.</p></section></body>
</FictionBook>
`
