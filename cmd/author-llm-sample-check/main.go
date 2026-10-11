// Command author-llm-sample-check runs the LLM eval freeze's population and
// anchor reads and its per-row classification — the tokenizer and the strata
// — against a catalog, inside a read-only transaction, and prints what the
// freeze would sample and what it would leave out, by reason, as JSON. It
// names nobody: a skipped row is shown by its fingerprint only. It is the
// check run against production data before an eval run starts there.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"gopds-api/services"

	"github.com/go-pg/pg/v10"
)

// defaultTimeout bounds the whole read.
const defaultTimeout = 30 * time.Minute

func main() {
	var (
		addr    = flag.String("host", envOr("GOPDS_POSTGRES_DBHOST", "127.0.0.1:5432"), "database host:port")
		user    = flag.String("user", envOr("GOPDS_POSTGRES_DBUSER", "gopds"), "database user")
		pass    = flag.String("password", os.Getenv("GOPDS_POSTGRES_DBPASS"), "database password")
		name    = flag.String("database", envOr("GOPDS_POSTGRES_DBNAME", "gopds"), "database name")
		timeout = flag.Duration("timeout", defaultTimeout, "how long the whole read may take")
	)
	flag.Parse()

	db := pg.Connect(&pg.Options{Addr: *addr, User: *user, Password: *pass, Database: *name})
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	err := run(ctx, db, os.Stdout, os.Stderr)
	cancel()
	if closeErr := db.Close(); closeErr != nil {
		fmt.Fprintf(os.Stderr, "closing the database: %v\n", closeErr)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "author llm sample check: %v\n", err)
		os.Exit(1)
	}
}

// run reads inside a read-only transaction — a write would fail, never land —
// and writes the report to out and its duration to progress.
func run(ctx context.Context, db *pg.DB, out, progress io.Writer) error {
	started := time.Now()
	var report *services.AuthorLLMSamplingReport
	err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if _, err := tx.ExecContext(ctx, `SET TRANSACTION READ ONLY`); err != nil {
			return err
		}
		var err error
		report, err = services.AuthorLLMSamplingCheck(ctx, tx, services.DefaultAuthorLLMEvalSizes())
		return err
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(progress, "%d fingerprints in %s\n", report.Fingerprints, time.Since(started).Round(time.Millisecond))
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
