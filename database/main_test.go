package database

import (
	"fmt"
	"os"
	"testing"

	"github.com/go-pg/pg/v10"
)

// The tests in this package that touch rows need a real PostgreSQL: what they
// check is the behavior of the schema's own constraints, which no fake can
// reproduce. They run against a database when one is configured and skip
// cleanly when there is none — `make db-reset` prepares a suitable local one,
// and `make test-integration` points the suite at it.

const skipReason = "no database configured: set GOPDS_POSTGRES_DBHOST/DBUSER/DBNAME " +
	"(see `make db-reset`), or run with -short to skip integration tests"

func TestMain(m *testing.M) {
	conn, err := connectFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "database: integration tests will skip: %v\n", err)
	} else {
		SetDB(conn)
	}

	code := m.Run()

	// Closed explicitly rather than deferred: os.Exit does not run defers.
	if conn != nil {
		_ = conn.Close()
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

	conn := pg.Connect(&pg.Options{
		Addr:     host,
		User:     user,
		Password: os.Getenv("GOPDS_POSTGRES_DBPASS"),
		Database: name,
	})
	if _, err := conn.Exec("SELECT 1"); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("connecting to %s/%s: %w", host, name, err)
	}
	return conn, nil
}

// requireDatabase skips the calling test unless a database is reachable.
func requireDatabase(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if db == nil {
		t.Skip(skipReason)
	}
}
