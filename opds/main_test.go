package opds

import (
	"fmt"
	"os"
	"testing"

	"gopds-api/database"

	"github.com/go-pg/pg/v10"
)

// The integration tests in this package exercise handlers that read the catalog
// through the package-global database connection. That connection is normally
// established in main(), so inside a test binary it stays nil and every handler
// dereferences it — which is why these tests used to abort the whole package
// with a segfault instead of failing or skipping.
//
// TestMain wires it up from GOPDS_POSTGRES_* so the suite behaves predictably:
// it runs against a real database when one is configured, and skips cleanly
// when there is none. `make db-reset` prepares a suitable local database.

const skipReason = "no database configured: set GOPDS_POSTGRES_DBHOST/DBUSER/DBNAME " +
	"(see `make db-reset`), or run with -short to skip integration tests"

// databaseAvailable reports whether the tests could reach a database, and is
// read by the integration tests through requireDatabase.
var databaseAvailable bool

func TestMain(m *testing.M) {
	db, err := connectFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "opds: integration tests will skip: %v\n", err)
	} else {
		databaseAvailable = true
		database.SetDB(db)
	}

	code := m.Run()

	// Closed explicitly rather than deferred: os.Exit does not run defers.
	if db != nil {
		_ = db.Close()
	}

	os.Exit(code)
}

func connectFromEnv() (*pg.DB, error) {
	host := os.Getenv("GOPDS_POSTGRES_DBHOST")
	user := os.Getenv("GOPDS_POSTGRES_DBUSER")
	name := os.Getenv("GOPDS_POSTGRES_DBNAME")
	if host == "" || user == "" || name == "" {
		return nil, fmt.Errorf("GOPDS_POSTGRES_DBHOST, DBUSER and DBNAME must all be set")
	}

	db := pg.Connect(&pg.Options{
		Addr:     host,
		User:     user,
		Password: os.Getenv("GOPDS_POSTGRES_DBPASS"),
		Database: name,
	})
	if _, err := db.Exec("SELECT 1"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connecting to %s/%s: %w", host, name, err)
	}
	return db, nil
}

// requireDatabase skips the calling test unless a database is reachable. Tests
// call this instead of dereferencing a nil connection.
func requireDatabase(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if !databaseAvailable {
		t.Skip(skipReason)
	}
}

// anyPublicCuratedCollectionID returns the id of a collection the OPDS handlers
// will actually serve. Tests must not hard-code an id: which collections exist
// depends on the dataset that was restored, and the previously assumed id 1 is
// absent from production data, where ids start well above it.
func anyPublicCuratedCollectionID(t *testing.T) int64 {
	t.Helper()
	requireDatabase(t)

	var id int64
	_, err := database.GetDB().QueryOne(pg.Scan(&id),
		`SELECT id FROM book_collections
		 WHERE is_public AND is_curated
		 ORDER BY id LIMIT 1`)
	if err != nil {
		t.Skipf("no public curated collection in the database: %v", err)
	}
	return id
}
