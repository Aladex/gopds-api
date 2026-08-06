// Command migrate applies the SQL files in database_migrations.
//
// It is separate from the server on purpose. Schema changes on a database
// holding a live catalog are a decision, not a side effect of a deploy: the
// operator runs this, sees what it did, and then rolls the new image.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"gopds-api/internal/migrate"

	"github.com/go-pg/pg/v10"
)

const (
	migrationsDir = "database_migrations"

	// Long enough for the first run on an empty database, which builds the
	// whole schema, and short enough that a wedged connection gives up rather
	// than holding a deploy open.
	defaultTimeout = 10 * time.Minute
)

func main() {
	var (
		addr    = flag.String("host", envOr("GOPDS_POSTGRES_DBHOST", "127.0.0.1:5432"), "database host:port")
		user    = flag.String("user", envOr("GOPDS_POSTGRES_DBUSER", "gopds"), "database user")
		pass    = flag.String("password", os.Getenv("GOPDS_POSTGRES_DBPASS"), "database password")
		name    = flag.String("database", envOr("GOPDS_POSTGRES_DBNAME", "gopds"), "database name")
		dir     = flag.String("dir", migrationsDir, "directory holding the .sql files")
		dryRun  = flag.Bool("dry-run", false, "report what would run, change nothing")
		timeout = flag.Duration("timeout", defaultTimeout, "how long the whole run may take")
	)
	flag.Parse()

	db := pg.Connect(&pg.Options{Addr: *addr, User: *user, Password: *pass, Database: *name})
	defer func() {
		if err := db.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "closing the database: %v\n", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if _, err := db.ExecContext(ctx, "SELECT 1"); err != nil {
		fmt.Fprintf(os.Stderr, "cannot reach %s/%s: %v\n", *addr, *name, err)
		cancel()
		fail(db)
	}

	if *dryRun {
		if err := report(ctx, db, *dir); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			cancel()
			fail(db)
		}
		return
	}

	result, err := migrate.Run(ctx, db, os.DirFS("."), *dir, migrate.AppBaseline())
	if err != nil {
		fmt.Fprintf(os.Stderr, "migration failed: %v\n", err)
		cancel()
		fail(db)
	}

	// Both halves can happen in one run, on a database that carried the schema
	// but had fallen behind the boundary, so neither is an else of the other.
	if len(result.Baselined) > 0 {
		fmt.Printf("Recorded %d existing migrations without running them:\n", len(result.Baselined))
		for _, name := range result.Baselined {
			fmt.Printf("  = %s\n", name)
		}
		fmt.Println()
	}
	if len(result.Applied) > 0 {
		fmt.Printf("Applied %d migrations:\n", len(result.Applied))
		for _, name := range result.Applied {
			fmt.Printf("  + %s\n", name)
		}
	}
	if len(result.Baselined) == 0 && len(result.Applied) == 0 {
		fmt.Println("Already up to date.")
	}
}

// report prints what a run would do, touching nothing.
func report(ctx context.Context, db *pg.DB, dir string) error {
	toRecord, toApply, err := migrate.Pending(ctx, db, os.DirFS("."), dir, migrate.AppBaseline())
	if err != nil {
		return err
	}

	if len(toRecord) > 0 {
		fmt.Printf("This database predates the ledger: %d migrations would be recorded as already applied.\n", len(toRecord))
		for _, name := range toRecord {
			fmt.Printf("  = %s\n", name)
		}
		fmt.Println()
	}
	if len(toApply) > 0 {
		fmt.Printf("%d migrations would run:\n", len(toApply))
		for _, name := range toApply {
			fmt.Printf("  + %s\n", name)
		}
	}
	if len(toRecord) == 0 && len(toApply) == 0 {
		fmt.Println("Already up to date.")
	}
	return nil
}

// fail closes the database and leaves with a non-zero status. os.Exit runs no
// deferred call, so the connection has to be let go here by hand.
func fail(db *pg.DB) {
	if err := db.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "closing the database: %v\n", err)
	}
	os.Exit(1)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
