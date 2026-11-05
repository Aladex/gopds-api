package database

import (
	"fmt"
	"os"
	"testing"

	"gopds-api/internal/testdb"

	"github.com/go-pg/pg/v10"
)

// The tests in this package that touch rows need a real PostgreSQL: what they
// check is the behavior of the schema's own constraints, which no fake can
// reproduce. They run against a database when one is configured and skip
// cleanly when there is none — `make db-reset` prepares a suitable local one,
// and `make test-integration` points the suite at it.

func TestMain(m *testing.M) {
	// A configured database that does not answer fails the run here rather
	// than skipping every test below: the suite was asked for integration
	// coverage, and reporting success without it is how a green gate comes to
	// mean nothing.
	var conn *pg.DB
	if cfg, ok := testdb.Configured(); ok {
		opened, err := testdb.Connect(cfg, DisableJIT)
		if err != nil {
			fmt.Fprintf(os.Stderr, "database: %v\n", err)
			os.Exit(1)
		}
		conn = opened
		SetDB(conn)
	} else {
		fmt.Fprintf(os.Stderr, "database: %s\n", testdb.SkipReason)
	}

	code := m.Run()

	// Closed explicitly rather than deferred: os.Exit does not run defers.
	if conn != nil {
		_ = conn.Close()
	}

	os.Exit(code)
}

// requireDatabase skips the calling test unless a database is reachable.
func requireDatabase(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if db == nil {
		t.Skip(testdb.SkipReason)
	}
}
