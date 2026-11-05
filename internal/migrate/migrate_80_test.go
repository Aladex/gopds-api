package migrate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"gopds-api/internal/testdb"

	"github.com/go-pg/pg/v10"
)

const migration80 = "80-catalog-production-shape.sql"

// The production shape of what migration 80 owns, as the catalog states it.
const (
	genreTitleColumn = "opds_catalog_genre.title character varying(256) NOT NULL DEFAULT ''::character varying"
	passwordColumn   = "auth_user.password character varying(512) NOT NULL DEFAULT <none>"
	fullNameColumn   = "opds_catalog_author.full_name character varying(1024) NOT NULL DEFAULT <none>"
	titleTrgmIndex   = "CREATE INDEX idx_book_title_trgm ON public.opds_catalog_book USING gin (title gin_trgm_ops)"
)

// The statements a restored production schema carries for each piece, without
// migration 80.
const (
	titlePiece    = `ALTER TABLE public.opds_catalog_genre ADD COLUMN title character varying(256) DEFAULT ''::character varying NOT NULL;`
	fullNamePiece = `ALTER TABLE public.opds_catalog_author ALTER COLUMN full_name TYPE character varying(1024);`
	passwordPiece = `ALTER TABLE public.auth_user ALTER COLUMN password TYPE character varying(512);`
	indexPiece    = `CREATE INDEX idx_book_title_trgm ON public.opds_catalog_book USING gin (title public.gin_trgm_ops);`

	// productionOnly brings a database migrated to 79 to production's shape.
	productionOnly = titlePiece + fullNamePiece + passwordPiece + indexPiece
)

// The width of the columns before 80, as the catalog states them.
const (
	narrowFullName = "opds_catalog_author.full_name character varying(128) NOT NULL DEFAULT <none>"
	narrowPassword = "auth_user.password character varying(128) NOT NULL DEFAULT <none>"
)

// migration80LockGuard is the test's own limit on a run that waits for a held
// table. It is well past the file's lock_timeout, and it is the only thing that
// ends the wait when the file has no lock_timeout.
const migration80LockGuard = 15 * time.Second

// migrationSets returns every migration on disk, and the same set without 80.
func migrationSets(t *testing.T) (through79, all fstest.MapFS) {
	t.Helper()
	through79, all = fstest.MapFS{}, fstest.MapFS{}
	entries, err := os.ReadDir("../../database_migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		data, readErr := os.ReadFile("../../database_migrations/" + e.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		all["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
		if e.Name() != migration80 {
			through79["database_migrations/"+e.Name()] = &fstest.MapFile{Data: data}
		}
	}
	return through79, all
}

func catalogScratchDB(t *testing.T) *pg.DB {
	t.Helper()
	admin := testDB(t)
	cfg, _ := testdb.Configured()
	name := fmt.Sprintf("migrate_80_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatalf("creating the scratch database: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)") })
	db := pg.Connect(&pg.Options{Addr: cfg.Host, User: cfg.User, Password: cfg.Password, Database: name})
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// describeColumn renders one column's type, nullability and default from the
// catalog; a missing column renders as "".
func describeColumn(t *testing.T, db *pg.DB, table, column string) string {
	t.Helper()
	var description string
	_, err := db.Query(pg.Scan(&description), `
		SELECT c.relname || '.' || a.attname || ' ' || format_type(a.atttypid, a.atttypmod)
			|| CASE WHEN a.attnotnull THEN ' NOT NULL' ELSE ' NULL' END
			|| ' DEFAULT ' || coalesce(pg_get_expr(d.adbin, d.adrelid), '<none>')
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		WHERE c.oid = ?::regclass AND a.attname = ? AND NOT a.attisdropped`, "public."+table, column)
	if err != nil {
		t.Fatalf("reading %s.%s: %v", table, column, err)
	}
	return description
}

func indexDefinition(t *testing.T, db *pg.DB, name string) string {
	t.Helper()
	var definition string
	_, err := db.Query(pg.Scan(&definition),
		`SELECT indexdef FROM pg_indexes WHERE schemaname = 'public' AND indexname = ?`, name)
	if err != nil {
		t.Fatalf("reading index %s: %v", name, err)
	}
	return definition
}

func requireProductionCatalog(t *testing.T, db *pg.DB) {
	t.Helper()
	for _, c := range []struct{ table, column, want string }{
		{"opds_catalog_genre", "title", genreTitleColumn},
		{"auth_user", "password", passwordColumn},
		{"opds_catalog_author", "full_name", fullNameColumn},
	} {
		if got := describeColumn(t, db, c.table, c.column); got != c.want {
			t.Errorf("%s.%s:\n got %q\nwant %q", c.table, c.column, got, c.want)
		}
	}
	if got := indexDefinition(t, db, "idx_book_title_trgm"); got != titleTrgmIndex {
		t.Errorf("idx_book_title_trgm:\n got %q\nwant %q", got, titleTrgmIndex)
	}
}

// fileNodes is the on-disk identity of the tables migration 80 names and of
// every index on them: a rewrite or an index rebuild changes it.
func fileNodes(t *testing.T, db *pg.DB) map[string]int64 {
	t.Helper()
	var rows []struct {
		Relname     string
		Relfilenode int64
	}
	_, err := db.Query(&rows, `
		SELECT c.relname, c.relfilenode
		FROM pg_class c
		WHERE c.oid IN ('public.opds_catalog_genre'::regclass, 'public.auth_user'::regclass,
			'public.opds_catalog_author'::regclass, 'public.opds_catalog_book'::regclass)
		   OR c.oid IN (SELECT indexrelid FROM pg_index WHERE indrelid IN (
			'public.opds_catalog_genre'::regclass, 'public.auth_user'::regclass,
			'public.opds_catalog_author'::regclass, 'public.opds_catalog_book'::regclass))`)
	if err != nil {
		t.Fatal(err)
	}
	nodes := make(map[string]int64, len(rows))
	for _, row := range rows {
		nodes[row.Relname] = row.Relfilenode
	}
	return nodes
}

// The migration files alone give a new install the catalog production has.
func TestFreshInstallHasTheProductionCatalog(t *testing.T) {
	db := catalogScratchDB(t)
	_, all := migrationSets(t)
	if _, err := Run(context.Background(), db, all, "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	requireProductionCatalog(t, db)

	// The widths and the default at work, on the rows a scan and a signup write.
	if _, err := db.Exec(`INSERT INTO opds_catalog_genre (genre) VALUES ('sf')`); err != nil {
		t.Fatalf("a genre without a title takes the default: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO opds_catalog_author (full_name) VALUES (repeat('я', 1024))`); err != nil {
		t.Fatalf("a long author name: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO opds_catalog_author (full_name) VALUES (repeat('я', 1025))`); err == nil {
		t.Error("an author name past production's width was accepted")
	}
}

// On a database that already has all of it, as production does, the file
// changes nothing and takes no table lock: it runs to the end beside a writer
// holding all four tables exclusively, and a statement that needed one of them
// would fail on the file's lock_timeout.
func TestMigration80IsANoOpOnAProductionShapedDatabase(t *testing.T) {
	db := catalogScratchDB(t)
	ctx := context.Background()
	through79, all := migrationSets(t)
	if _, err := Run(ctx, db, through79, "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("migrating to 79: %v", err)
	}
	if _, err := db.Exec(productionOnly); err != nil {
		t.Fatalf("bringing the database to production's shape: %v", err)
	}
	before := fileNodes(t, db)

	writer, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Rollback() })
	if _, err = writer.Exec(`LOCK TABLE public.opds_catalog_genre, public.auth_user, public.opds_catalog_author,
		public.opds_catalog_book IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}

	result, err := Run(ctx, db, all, "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("migration 80 waited for a table it has no business locking: %v", err)
	}
	if len(result.Applied) != 1 || result.Applied[0] != migration80 {
		t.Fatalf("applied %v, want only %s", result.Applied, migration80)
	}
	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}

	after := fileNodes(t, db)
	for name, node := range before {
		if after[name] != node {
			t.Errorf("%s was rewritten: relfilenode %d became %d", name, node, after[name])
		}
	}
	requireProductionCatalog(t, db)
}

// Each piece stands on its own check: with any one of them already there and
// the other three missing, as on a database a test helper once gave the column,
// the file adds the three and leaves the rows alone.
func TestMigration80AddsOnlyWhatIsMissing(t *testing.T) {
	for _, tc := range []struct {
		name      string
		present   string
		wantTitle string
	}{
		{"genre title", titlePiece + ` UPDATE public.opds_catalog_genre SET title = 'Фантастика';`, "Фантастика"},
		{"author width", fullNamePiece, ""},
		{"password width", passwordPiece, ""},
		{"title index", indexPiece, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := catalogScratchDB(t)
			ctx := context.Background()
			through79, all := migrationSets(t)
			if _, err := Run(ctx, db, through79, "database_migrations", AppBaseline()); err != nil {
				t.Fatalf("migrating to 79: %v", err)
			}
			if _, err := db.Exec(`
				INSERT INTO opds_catalog_author (full_name) VALUES ('kept');
				INSERT INTO opds_catalog_genre (genre) VALUES ('sf');
				INSERT INTO auth_user (password, username, email, date_joined)
					VALUES ('kept-hash', 'kept', 'kept@fixture.local', now());
				INSERT INTO opds_catalog_book (filename, path, format, registerdate, docdate, lang, title, annotation)
					VALUES ('kept.fb2', 'kept.zip', 'fb2', now(), '', 'ru', 'kept', '');`); err != nil {
				t.Fatalf("seeding the rows: %v", err)
			}
			if _, err := db.Exec(tc.present); err != nil {
				t.Fatalf("adding the piece that is already there: %v", err)
			}

			result, err := Run(ctx, db, all, "database_migrations", AppBaseline())
			if err != nil {
				t.Fatalf("applying 80: %v", err)
			}
			if len(result.Applied) != 1 || result.Applied[0] != migration80 {
				t.Fatalf("applied %v, want only %s", result.Applied, migration80)
			}

			requireProductionCatalog(t, db)
			var title, author, password, book string
			if _, err = db.QueryOne(pg.Scan(&title, &author, &password, &book), `SELECT
				(SELECT title FROM opds_catalog_genre WHERE genre = 'sf'),
				(SELECT full_name FROM opds_catalog_author),
				(SELECT password FROM auth_user WHERE username = 'kept'),
				(SELECT title FROM opds_catalog_book)`); err != nil {
				t.Fatal(err)
			}
			if title != tc.wantTitle || author != "kept" || password != "kept-hash" || book != "kept" {
				t.Errorf("the rows changed: genre title %q, author %q, password %q, book %q", title, author, password, book)
			}
		})
	}
}

// migrationHoldsEarlierPieces reports whether a session waiting for auth_user
// holds the genre and author tables exclusively: it has altered them, so the
// rollback that follows has something to undo.
func migrationHoldsEarlierPieces(t *testing.T, db *pg.DB) bool {
	t.Helper()
	var held int
	if _, err := db.QueryOne(pg.Scan(&held), `
		SELECT count(DISTINCT l.relation)
		FROM pg_locks l
		WHERE l.granted AND l.mode = 'AccessExclusiveLock'
		  AND l.relation IN ('public.opds_catalog_genre'::regclass, 'public.opds_catalog_author'::regclass)
		  AND l.pid IN (SELECT pid FROM pg_locks
		                WHERE NOT granted AND relation = 'public.auth_user'::regclass)`); err != nil {
		t.Fatalf("reading the locks: %v", err)
	}
	return held == 2
}

// A table the file needs is held by a writer: the file gives up on its own
// lock_timeout with lock_not_available, which a starting server retries, and
// leaves nothing behind, not even the pieces it had finished. Once the writer
// is gone the next run applies 80 and nothing else.
func TestMigration80GivesUpOnAHeldTableAndRollsBack(t *testing.T) {
	db := catalogScratchDB(t)
	ctx := context.Background()
	through79, all := migrationSets(t)
	if _, err := Run(ctx, db, through79, "database_migrations", AppBaseline()); err != nil {
		t.Fatalf("migrating to 79: %v", err)
	}

	writer, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Rollback() })
	if _, err = writer.Exec(`LOCK TABLE public.auth_user IN ROW EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}

	// The run has its own goroutine so that a file that never gives up fails
	// this test at the guard. The cleanup above then lets it finish.
	done := make(chan error, 1)
	go func() {
		_, runErr := Run(ctx, db, all, "database_migrations", AppBaseline())
		done <- runErr
	}()
	guard := time.After(migration80LockGuard)
	poll := time.NewTicker(50 * time.Millisecond)
	defer poll.Stop()
	heldEarlierPieces := false
	var runErr error
wait:
	for {
		select {
		case runErr = <-done:
			break wait
		case <-poll.C:
			heldEarlierPieces = heldEarlierPieces || migrationHoldsEarlierPieces(t, db)
		case <-guard:
			t.Fatalf("still waiting for auth_user after %s: the file's wait for a table is not bounded", migration80LockGuard)
		}
	}

	if !heldEarlierPieces {
		t.Error("the run never waited for auth_user with the earlier pieces done: the rollback below proves nothing")
	}
	var pgErr pg.Error
	if !errors.As(runErr, &pgErr) || pgErr.Field('C') != "55P03" {
		t.Fatalf("want SQLSTATE 55P03, got %v", runErr)
	}
	var failed *FileError
	if !errors.As(runErr, &failed) || failed.Name != migration80 {
		t.Fatalf("want a failure of %s, got %v", migration80, runErr)
	}

	if got := describeColumn(t, db, "opds_catalog_genre", "title"); got != "" {
		t.Errorf("the genre title survived the rollback: %q", got)
	}
	if got := describeColumn(t, db, "opds_catalog_author", "full_name"); got != narrowFullName {
		t.Errorf("the author width survived the rollback: %q", got)
	}
	if got := describeColumn(t, db, "auth_user", "password"); got != narrowPassword {
		t.Errorf("the password width changed: %q", got)
	}
	if got := indexDefinition(t, db, "idx_book_title_trgm"); got != "" {
		t.Errorf("the index survived the rollback: %q", got)
	}
	var recorded int
	if _, err = db.QueryOne(pg.Scan(&recorded),
		`SELECT count(*) FROM schema_migrations WHERE version = ?`, migration80); err != nil {
		t.Fatal(err)
	}
	if recorded != 0 {
		t.Errorf("the ledger records a migration that rolled back")
	}

	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	result, err := Run(ctx, db, all, "database_migrations", AppBaseline())
	if err != nil {
		t.Fatalf("rerunning after the writer left: %v", err)
	}
	if len(result.Applied) != 1 || result.Applied[0] != migration80 {
		t.Fatalf("the rerun applied %v, want only %s", result.Applied, migration80)
	}
	requireProductionCatalog(t, db)
}
