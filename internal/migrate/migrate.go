// Package migrate applies the SQL files in database_migrations in order, once
// each.
//
// Until now the only thing that ran them was PostgreSQL's own
// docker-entrypoint-initdb.d, which fires exactly once, on an empty data
// directory. That works for a fresh developer database and does nothing at all
// for a database that already holds books: every change since the first release
// has been applied to production by hand, and nothing recorded that it had
// been.
package migrate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/go-pg/pg/v10"
)

// Applier runs statements against a database. *pg.DB satisfies it.
type Applier interface {
	ExecContext(ctx context.Context, query interface{}, params ...interface{}) (pg.Result, error)
}

// Result reports what a run did.
type Result struct {
	// Applied names the migrations this run executed, in order.
	Applied []string
	// Baselined names migrations recorded as already present without being
	// executed, which happens once on a database that predates this package.
	Baselined []string
}

const createLedger = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version    text PRIMARY KEY,
	applied_at timestamptz NOT NULL DEFAULT now()
)`

// Run applies every .sql file in dir that the ledger does not already list.
//
// The order is the file name's, which is why the names carry a numeric prefix.
// Each file runs inside its own transaction: a migration that fails leaves no
// half-applied change behind and is not recorded, so the next run tries it
// again.
//
// On a database that already carries the schema but no ledger — production,
// every developer machine set up before this existed — running the files would
// mean running the first one again over live tables. Such a database is
// baselined instead: every file present at that moment is recorded as applied
// without being executed. established decides which case this is.
func Run(
	ctx context.Context,
	db *pg.DB,
	files fs.FS,
	dir string,
	established func(context.Context, *pg.DB) (bool, error),
) (Result, error) {
	var result Result

	names, err := sqlFiles(files, dir)
	if err != nil {
		return result, err
	}

	ledgerExisted, err := ledgerExists(ctx, db)
	if err != nil {
		return result, fmt.Errorf("looking for the migration ledger: %w", err)
	}
	if _, createErr := db.ExecContext(ctx, createLedger); createErr != nil {
		return result, fmt.Errorf("creating the migration ledger: %w", createErr)
	}

	if !ledgerExisted {
		old, checkErr := established(ctx, db)
		if checkErr != nil {
			return result, fmt.Errorf("deciding whether the database predates the ledger: %w", checkErr)
		}
		if old {
			for _, name := range names {
				if recordErr := record(ctx, db, name); recordErr != nil {
					return result, fmt.Errorf("baselining %s: %w", name, recordErr)
				}
				result.Baselined = append(result.Baselined, name)
			}
			return result, nil
		}
	}

	done, err := alreadyApplied(ctx, db)
	if err != nil {
		return result, fmt.Errorf("reading the migration ledger: %w", err)
	}

	for _, name := range names {
		if _, seen := done[name]; seen {
			continue
		}

		statements, err := fs.ReadFile(files, path.Join(dir, name))
		if err != nil {
			return result, fmt.Errorf("reading %s: %w", name, err)
		}

		if err := apply(ctx, db, name, string(statements)); err != nil {
			return result, fmt.Errorf("applying %s: %w", name, err)
		}
		result.Applied = append(result.Applied, name)
	}

	return result, nil
}

// PredatesLedger reports whether the database already holds the application's
// schema. auth_user is the table the very first migration creates, so its
// presence means the files have been run before, by hand or by initdb.
func PredatesLedger(ctx context.Context, db *pg.DB) (bool, error) {
	var exists bool
	_, err := db.QueryOneContext(ctx, pg.Scan(&exists),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		                WHERE table_schema = current_schema() AND table_name = 'auth_user')`)
	return exists, err
}

func sqlFiles(files fs.FS, dir string) ([]string, error) {
	entries, err := fs.ReadDir(files, dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	if len(names) == 0 {
		return nil, errors.New("no .sql files found: refusing to treat that as an up-to-date database")
	}
	return names, nil
}

func ledgerExists(ctx context.Context, db *pg.DB) (bool, error) {
	var exists bool
	_, err := db.QueryOneContext(ctx, pg.Scan(&exists),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		                WHERE table_schema = current_schema() AND table_name = 'schema_migrations')`)
	return exists, err
}

func alreadyApplied(ctx context.Context, db *pg.DB) (map[string]struct{}, error) {
	var versions []string
	if _, err := db.QueryContext(ctx, &versions, `SELECT version FROM schema_migrations`); err != nil {
		return nil, err
	}

	done := make(map[string]struct{}, len(versions))
	for _, v := range versions {
		done[v] = struct{}{}
	}
	return done, nil
}

func record(ctx context.Context, db *pg.DB, name string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO schema_migrations (version) VALUES (?) ON CONFLICT DO NOTHING`, name)
	return err
}

// apply runs one file and records it, both or neither.
func apply(ctx context.Context, db *pg.DB, name, statements string) error {
	return db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if _, err := tx.ExecContext(ctx, statements); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version) VALUES (?)`, name)
		return err
	})
}

// Pending reports what a run would do, changing nothing.
//
// The second return says the database predates the ledger, in which case the
// names would be recorded rather than executed.
func Pending(
	ctx context.Context,
	db *pg.DB,
	files fs.FS,
	dir string,
	established func(context.Context, *pg.DB) (bool, error),
) (pending []string, baseline bool, err error) {
	names, err := sqlFiles(files, dir)
	if err != nil {
		return nil, false, err
	}

	ledgerExisted, err := ledgerExists(ctx, db)
	if err != nil {
		return nil, false, fmt.Errorf("looking for the migration ledger: %w", err)
	}
	if !ledgerExisted {
		old, checkErr := established(ctx, db)
		if checkErr != nil {
			return nil, false, fmt.Errorf("deciding whether the database predates the ledger: %w", checkErr)
		}
		if old {
			return names, true, nil
		}
		return names, false, nil
	}

	done, err := alreadyApplied(ctx, db)
	if err != nil {
		return nil, false, fmt.Errorf("reading the migration ledger: %w", err)
	}

	for _, name := range names {
		if _, seen := done[name]; !seen {
			pending = append(pending, name)
		}
	}
	return pending, false, nil
}
