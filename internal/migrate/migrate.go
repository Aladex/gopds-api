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
// baselined instead: the files it is known to hold are recorded as applied
// without being executed, and everything after base.Through is applied
// normally in the same run.
//
// Baselining the whole directory instead — which this did until the boundary
// was named — silently swallows any migration the database has never seen: it
// is recorded as applied, never runs, and the ledger then swears it did.
func Run(
	ctx context.Context,
	db *pg.DB,
	files fs.FS,
	dir string,
	base Baseline,
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
		old, checkErr := base.Established(ctx, db)
		if checkErr != nil {
			return result, fmt.Errorf("deciding whether the database predates the ledger: %w", checkErr)
		}
		if old {
			held, boundErr := upTo(names, base.Through)
			if boundErr != nil {
				return result, boundErr
			}
			for _, name := range held {
				if recordErr := record(ctx, db, name); recordErr != nil {
					return result, fmt.Errorf("baselining %s: %w", name, recordErr)
				}
				result.Baselined = append(result.Baselined, name)
			}
			// Deliberately no early return: whatever came after the boundary
			// is new to this database and has to run.
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

// PreLedgerBoundary is the last migration a ledgerless database is taken to
// hold already.
//
// It has to be stated rather than inferred. "The schema is here, so everything
// has been applied" was true exactly once — the day the ledger was introduced,
// when this was the newest file. Every migration added afterwards makes it a
// wider claim than the evidence supports: a database built before that
// migration existed still answers yes to PredatesLedger, and recording the new
// file as applied would skip it for good, leaving a ledger that says it ran.
//
// So the pair means: such a database holds everything up to and including this
// file, and nothing after it. Adding a migration must not move this line.
const PreLedgerBoundary = "19-add-interface-lang.sql"

// Baseline says what a database that predates the ledger already contains.
//
// The two halves are useless apart — whether the schema is there, and how much
// of it — which is why they travel together and why a caller cannot supply one
// and forget the other.
type Baseline struct {
	// Established reports whether the schema is already in place.
	Established func(context.Context, *pg.DB) (bool, error)
	// Through names the last migration such a database is known to hold.
	// Anything after it is applied rather than recorded.
	Through string
}

// AppBaseline is this application's own answer to both questions.
func AppBaseline() Baseline {
	return Baseline{Established: PredatesLedger, Through: PreLedgerBoundary}
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

// upTo returns the files a baselined database is taken to hold: everything up
// to and including boundary.
//
// A boundary that names no file is refused rather than guessed at. Both ways of
// guessing are wrong in a way nobody would notice: treating it as "none" reruns
// the first migration over live tables, and treating it as "all" is the very
// swallowing this exists to stop.
func upTo(names []string, boundary string) ([]string, error) {
	if boundary == "" {
		return nil, errors.New("no baseline boundary given: refusing to guess how much of the schema is already there")
	}
	for i, name := range names {
		if name == boundary {
			return names[:i+1], nil
		}
	}
	return nil, fmt.Errorf("baseline boundary %q is not among the migrations: it was renamed or removed, and no database can be baselined until it is put back", boundary)
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
		// 01-initial.sql is a pg_dump: it empties the session search_path and
		// never puts it back. Left alone, that poisons the pooled connection:
		// the ledger insert below fails right here, and every later migration
		// naming extension objects without a schema (gin_trgm_ops lives in
		// public) fails the same way. Remember the path this transaction
		// started with and restore it after the file ran — on commit the
		// session is back to exactly its prior state, on rollback the SET
		// vanishes with everything else.
		var searchPath string
		if _, err := tx.QueryOneContext(ctx, pg.Scan(&searchPath),
			`SELECT current_setting('search_path')`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, statements); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`SELECT set_config('search_path', ?, false)`, searchPath); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version) VALUES (?)`, name)
		return err
	})
}

// Pending reports what a run would do, changing nothing.
//
// Two lists rather than one and a flag: a ledgerless database that has fallen
// behind the boundary gets both at once — the old files recorded, the newer
// ones executed — and an operator about to touch production should see which
// is which before it happens.
func Pending(
	ctx context.Context,
	db *pg.DB,
	files fs.FS,
	dir string,
	base Baseline,
) (toRecord, toApply []string, err error) {
	names, err := sqlFiles(files, dir)
	if err != nil {
		return nil, nil, err
	}

	ledgerExisted, err := ledgerExists(ctx, db)
	if err != nil {
		return nil, nil, fmt.Errorf("looking for the migration ledger: %w", err)
	}
	if !ledgerExisted {
		old, checkErr := base.Established(ctx, db)
		if checkErr != nil {
			return nil, nil, fmt.Errorf("deciding whether the database predates the ledger: %w", checkErr)
		}
		if !old {
			return nil, names, nil
		}
		held, boundErr := upTo(names, base.Through)
		if boundErr != nil {
			return nil, nil, boundErr
		}
		return held, names[len(held):], nil
	}

	done, err := alreadyApplied(ctx, db)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the migration ledger: %w", err)
	}

	for _, name := range names {
		if _, seen := done[name]; !seen {
			toApply = append(toApply, name)
		}
	}
	return nil, toApply, nil
}
