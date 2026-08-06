package migrate

import (
	"context"
	"fmt"
	"os"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-pg/pg/v10"
)

// These run against a real PostgreSQL because what they check is the behavior
// of transactions and of a schema that already exists — neither of which a fake
// reproduces. They skip cleanly when no database is configured; `make db-reset`
// prepares a suitable one.

func testDB(t *testing.T) *pg.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	host := os.Getenv("GOPDS_POSTGRES_DBHOST")
	user := os.Getenv("GOPDS_POSTGRES_DBUSER")
	name := os.Getenv("GOPDS_POSTGRES_DBNAME")
	if host == "" || user == "" || name == "" {
		t.Skip("no database configured: set GOPDS_POSTGRES_DBHOST/DBUSER/DBNAME")
	}

	db := pg.Connect(&pg.Options{
		Addr:     host,
		User:     user,
		Password: os.Getenv("GOPDS_POSTGRES_DBPASS"),
		Database: name,
	})
	if _, err := db.Exec("SELECT 1"); err != nil {
		_ = db.Close()
		t.Skipf("connecting to %s/%s: %v", host, name, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// Each test works inside its own schema, so a run cannot see or damage the
// application's tables — including the real schema_migrations.
func isolatedSchema(t *testing.T, db *pg.DB) {
	t.Helper()

	schema := fmt.Sprintf("migrate_test_%d", time.Now().UnixNano())
	if _, err := db.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatalf("creating schema %s: %v", schema, err)
	}
	if _, err := db.Exec("SET search_path TO " + schema); err != nil {
		t.Fatalf("switching to schema %s: %v", schema, err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("SET search_path TO public")
		_, _ = db.Exec("DROP SCHEMA " + schema + " CASCADE")
	})
}

func files(entries map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, body := range entries {
		fsys["migrations/"+name] = &fstest.MapFile{Data: []byte(body)}
	}
	return fsys
}

func versions(t *testing.T, db *pg.DB) []string {
	t.Helper()
	var got []string
	if _, err := db.Query(&got, `SELECT version FROM schema_migrations ORDER BY version`); err != nil {
		t.Fatalf("reading the ledger: %v", err)
	}
	return got
}

func fresh(context.Context, *pg.DB) (bool, error)       { return false, nil }
func preexisting(context.Context, *pg.DB) (bool, error) { return true, nil }

// emptyDB is a database with nothing in it: the boundary never comes up.
var emptyDB = Baseline{Established: fresh, Through: "01-first.sql"}

// heldThrough describes a ledgerless database that already carries everything
// up to and including the named file.
func heldThrough(name string) Baseline {
	return Baseline{Established: preexisting, Through: name}
}

func TestRunAppliesEveryFileOnAnEmptyDatabase(t *testing.T) {
	db := testDB(t)
	isolatedSchema(t, db)

	fsys := files(map[string]string{
		"01-first.sql":  "CREATE TABLE first (id int)",
		"02-second.sql": "CREATE TABLE second (id int)",
	})

	res, err := Run(context.Background(), db, fsys, "migrations", emptyDB)
	if err != nil {
		t.Fatalf("running migrations: %v", err)
	}
	if len(res.Applied) != 2 {
		t.Errorf("applied %v, want both files", res.Applied)
	}
	if len(res.Baselined) != 0 {
		t.Errorf("baselined %v on an empty database", res.Baselined)
	}

	for _, table := range []string{"first", "second"} {
		var exists bool
		_, err := db.QueryOne(pg.Scan(&exists),
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			                WHERE table_schema = current_schema() AND table_name = ?)`, table)
		if err != nil || !exists {
			t.Errorf("table %s was not created (err=%v)", table, err)
		}
	}
}

// The property that makes the runner safe to call from a starting service.
func TestRunIsIdempotent(t *testing.T) {
	db := testDB(t)
	isolatedSchema(t, db)

	fsys := files(map[string]string{"01-first.sql": "CREATE TABLE first (id int)"})

	if _, err := Run(context.Background(), db, fsys, "migrations", emptyDB); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// A second run must not execute CREATE TABLE again, which would fail.
	res, err := Run(context.Background(), db, fsys, "migrations", emptyDB)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(res.Applied) != 0 {
		t.Errorf("second run applied %v, want nothing", res.Applied)
	}
}

func TestRunAppliesOnlyWhatIsNew(t *testing.T) {
	db := testDB(t)
	isolatedSchema(t, db)

	first := files(map[string]string{"01-first.sql": "CREATE TABLE first (id int)"})
	if _, err := Run(context.Background(), db, first, "migrations", emptyDB); err != nil {
		t.Fatalf("first run: %v", err)
	}

	both := files(map[string]string{
		"01-first.sql":  "CREATE TABLE first (id int)",
		"02-second.sql": "CREATE TABLE second (id int)",
	})
	res, err := Run(context.Background(), db, both, "migrations", emptyDB)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(res.Applied) != 1 || res.Applied[0] != "02-second.sql" {
		t.Errorf("applied %v, want only the new file", res.Applied)
	}
}

// The case that protects production: a database that already carries the schema
// must have its history recorded, not replayed.
func TestRunBaselinesADatabaseThatPredatesTheLedger(t *testing.T) {
	db := testDB(t)
	isolatedSchema(t, db)

	// Stand in for a database built by initdb or by hand: the table exists,
	// and the file that would create it is about to be considered.
	if _, err := db.Exec("CREATE TABLE first (id int)"); err != nil {
		t.Fatalf("preparing the existing schema: %v", err)
	}

	fsys := files(map[string]string{
		"01-first.sql":  "CREATE TABLE first (id int)",
		"02-second.sql": "CREATE TABLE second (id int)",
	})

	res, err := Run(context.Background(), db, fsys, "migrations", heldThrough("02-second.sql"))
	if err != nil {
		t.Fatalf("baselining: %v", err)
	}
	if len(res.Applied) != 0 {
		t.Errorf("applied %v against a database that already had the schema", res.Applied)
	}
	if len(res.Baselined) != 2 {
		t.Errorf("baselined %v, want every file", res.Baselined)
	}

	// The point of the exercise: the existing table survived untouched, and a
	// later run has nothing left to do.
	res, err = Run(context.Background(), db, fsys, "migrations", heldThrough("02-second.sql"))
	if err != nil {
		t.Fatalf("run after baselining: %v", err)
	}
	if len(res.Applied) != 0 || len(res.Baselined) != 0 {
		t.Errorf("a run after baselining did %v / %v, want nothing", res.Applied, res.Baselined)
	}
}

// A file that fails must leave nothing behind, or the next run starts from a
// half-built schema with no record of it.
func TestRunRollsBackAFailedMigration(t *testing.T) {
	db := testDB(t)
	isolatedSchema(t, db)

	fsys := files(map[string]string{
		"01-ok.sql": "CREATE TABLE ok (id int)",
		"02-broken.sql": "CREATE TABLE half (id int);\n" +
			"THIS IS NOT SQL;",
	})

	_, err := Run(context.Background(), db, fsys, "migrations", emptyDB)
	if err == nil {
		t.Fatal("a broken migration was reported as successful")
	}

	var halfExists bool
	if _, qErr := db.QueryOne(pg.Scan(&halfExists),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		                WHERE table_schema = current_schema() AND table_name = 'half')`); qErr != nil {
		t.Fatalf("checking for the rolled-back table: %v", qErr)
	}
	if halfExists {
		t.Error("the first half of a failed migration was left behind")
	}

	// The file before it stands, and the failed one is not recorded.
	got := versions(t, db)
	if len(got) != 1 || got[0] != "01-ok.sql" {
		t.Errorf("ledger holds %v, want only the migration that succeeded", got)
	}
}

func TestRunRefusesAnEmptyDirectory(t *testing.T) {
	db := testDB(t)
	isolatedSchema(t, db)

	fsys := fstest.MapFS{"migrations/notes.txt": &fstest.MapFile{Data: []byte("not sql")}}

	if _, err := Run(context.Background(), db, fsys, "migrations", emptyDB); err == nil {
		t.Error("a directory with no migrations was accepted as up to date")
	}
}

func TestPredatesLedgerRecognisesAnEstablishedSchema(t *testing.T) {
	db := testDB(t)
	isolatedSchema(t, db)

	ctx := context.Background()

	old, err := PredatesLedger(ctx, db)
	if err != nil {
		t.Fatalf("checking an empty schema: %v", err)
	}
	if old {
		t.Error("an empty schema was taken for an established database")
	}

	if _, createErr := db.Exec("CREATE TABLE auth_user (id int)"); createErr != nil {
		t.Fatalf("creating auth_user: %v", createErr)
	}

	old, err = PredatesLedger(ctx, db)
	if err != nil {
		t.Fatalf("checking an established schema: %v", err)
	}
	if !old {
		t.Error("a schema holding auth_user was taken for empty")
	}
}

func TestPendingChangesNothing(t *testing.T) {
	db := testDB(t)
	isolatedSchema(t, db)

	fsys := files(map[string]string{
		"01-first.sql":  "CREATE TABLE first (id int)",
		"02-second.sql": "CREATE TABLE second (id int)",
	})
	ctx := context.Background()

	toRecord, toApply, err := Pending(ctx, db, fsys, "migrations", emptyDB)
	if err != nil {
		t.Fatalf("asking what is pending: %v", err)
	}
	if len(toRecord) != 0 {
		t.Errorf("an empty schema would have %v recorded rather than run", toRecord)
	}
	if len(toApply) != 2 {
		t.Errorf("reported %v as pending, want both files", toApply)
	}

	// Nothing was created, and no ledger was left behind.
	var ledger bool
	if _, queryErr := db.QueryOne(pg.Scan(&ledger),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		                WHERE table_schema = current_schema() AND table_name = 'schema_migrations')`); queryErr != nil {
		t.Fatalf("checking for the ledger: %v", queryErr)
	}
	if ledger {
		t.Error("a dry run created the ledger table")
	}
}

func TestPendingShrinksAsMigrationsAreApplied(t *testing.T) {
	db := testDB(t)
	isolatedSchema(t, db)

	fsys := files(map[string]string{
		"01-first.sql":  "CREATE TABLE first (id int)",
		"02-second.sql": "CREATE TABLE second (id int)",
	})
	ctx := context.Background()

	if _, err := Run(ctx, db, files(map[string]string{"01-first.sql": "CREATE TABLE first (id int)"}),
		"migrations", emptyDB); err != nil {
		t.Fatalf("applying the first: %v", err)
	}

	_, toApply, err := Pending(ctx, db, fsys, "migrations", emptyDB)
	if err != nil {
		t.Fatalf("asking what is pending: %v", err)
	}
	if len(toApply) != 1 || toApply[0] != "02-second.sql" {
		t.Errorf("reported %v as pending, want only the unapplied file", toApply)
	}
}

/*
 * The reason the boundary exists.
 *
 * A database built before a migration was written still answers "yes, the
 * schema is here". Recording that migration as applied would skip it for good
 * and leave a ledger swearing it had run — a lie no later run can detect,
 * because a recorded migration is never looked at again.
 */
func TestRunAppliesWhatCameAfterTheBoundary(t *testing.T) {
	db := testDB(t)
	isolatedSchema(t, db)

	// The database stopped at the first file; the second was written later.
	if _, err := db.Exec("CREATE TABLE first (id int)"); err != nil {
		t.Fatalf("preparing the existing schema: %v", err)
	}

	fsys := files(map[string]string{
		"01-first.sql":  "CREATE TABLE first (id int)",
		"02-second.sql": "CREATE TABLE second (id int)",
	})

	res, err := Run(context.Background(), db, fsys, "migrations", heldThrough("01-first.sql"))
	if err != nil {
		t.Fatalf("running against a database behind the boundary: %v", err)
	}
	if len(res.Baselined) != 1 || res.Baselined[0] != "01-first.sql" {
		t.Errorf("baselined %v, want only what the database already held", res.Baselined)
	}
	if len(res.Applied) != 1 || res.Applied[0] != "02-second.sql" {
		t.Errorf("applied %v, want the file written after the boundary", res.Applied)
	}

	// The proof is in the schema, not in the ledger: the later migration ran.
	var exists bool
	if _, qErr := db.QueryOne(pg.Scan(&exists),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		                WHERE table_schema = current_schema() AND table_name = 'second')`); qErr != nil {
		t.Fatalf("checking the later table: %v", qErr)
	}
	if !exists {
		t.Error("the migration after the boundary was recorded but never ran")
	}
}

func TestPendingSeparatesWhatIsRecordedFromWhatIsRun(t *testing.T) {
	db := testDB(t)
	isolatedSchema(t, db)

	if _, err := db.Exec("CREATE TABLE first (id int)"); err != nil {
		t.Fatalf("preparing the existing schema: %v", err)
	}

	fsys := files(map[string]string{
		"01-first.sql":  "CREATE TABLE first (id int)",
		"02-second.sql": "CREATE TABLE second (id int)",
	})

	toRecord, toApply, err := Pending(context.Background(), db, fsys, "migrations", heldThrough("01-first.sql"))
	if err != nil {
		t.Fatalf("asking what is pending: %v", err)
	}
	// An operator about to touch production has to see which is which.
	if len(toRecord) != 1 || toRecord[0] != "01-first.sql" {
		t.Errorf("would record %v, want only what is already there", toRecord)
	}
	if len(toApply) != 1 || toApply[0] != "02-second.sql" {
		t.Errorf("would run %v, want what came after the boundary", toApply)
	}
}

// A boundary naming no file means the caller and the directory disagree about
// this application's history. Guessing either way is silently destructive.
func TestRunRefusesABoundaryThatNamesNoFile(t *testing.T) {
	db := testDB(t)
	isolatedSchema(t, db)

	if _, err := db.Exec("CREATE TABLE first (id int)"); err != nil {
		t.Fatalf("preparing the existing schema: %v", err)
	}

	fsys := files(map[string]string{"01-first.sql": "CREATE TABLE first (id int)"})

	for _, boundary := range []string{"", "99-renamed.sql"} {
		if _, err := Run(context.Background(), db, fsys, "migrations", heldThrough(boundary)); err == nil {
			t.Errorf("boundary %q was accepted", boundary)
		}
	}
}

// The boundary is a fact about this repository, so it has to name a file that
// is actually in it — a rename would otherwise be found by production.
func TestPreLedgerBoundaryNamesARealMigration(t *testing.T) {
	entries, err := os.ReadDir("../../database_migrations")
	if err != nil {
		t.Fatalf("reading the migrations directory: %v", err)
	}
	for _, e := range entries {
		if e.Name() == PreLedgerBoundary {
			return
		}
	}
	t.Errorf("%s is not among the migrations", PreLedgerBoundary)
}
