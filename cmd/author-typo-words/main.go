// Command author-typo-words writes the frozen word table of the typo rule from
// a catalog: it plans a build over the database, reading only, and counts the
// words of its sides, and prints the table. A new table is a new version of
// the rule; the steps are at the top of internal/authorentity/typo_words.go.
// Run it against a fresh prod copy.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"gopds-api/internal/authorentity"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
)

// defaultTimeout bounds the plan; the whole catalog takes minutes.
const defaultTimeout = 30 * time.Minute

func main() {
	var (
		addr    = flag.String("host", envOr("GOPDS_POSTGRES_DBHOST", "127.0.0.1:5432"), "database host:port")
		user    = flag.String("user", envOr("GOPDS_POSTGRES_DBUSER", "gopds"), "database user")
		pass    = flag.String("password", os.Getenv("GOPDS_POSTGRES_DBPASS"), "database password")
		name    = flag.String("database", envOr("GOPDS_POSTGRES_DBNAME", "gopds"), "database name")
		rule    = flag.Int("rule", authorentity.TypoRuleVersion+1, "rule version of the new table")
		timeout = flag.Duration("timeout", defaultTimeout, "how long the plan may take")
	)
	flag.Parse()

	db := pg.Connect(&pg.Options{Addr: *addr, User: *user, Password: *pass, Database: *name, OnConnect: readOnly})
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	err := run(ctx, db, *rule, os.Stdout)
	cancel()
	if closeErr := db.Close(); closeErr != nil {
		fmt.Fprintf(os.Stderr, "closing the database: %v\n", closeErr)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "author typo words: %v\n", err)
		os.Exit(1)
	}
}

// readOnly makes every connection refuse writes.
func readOnly(ctx context.Context, conn *pg.Conn) error {
	_, err := conn.ExecContext(ctx, `SET default_transaction_read_only = on`)
	return err
}

// run counts the catalog's words and writes the table of the rule to out.
func run(ctx context.Context, db *pg.DB, rule int, out io.Writer) error {
	counts, err := services.AuthorEntityTypoWords(ctx, db)
	if err != nil {
		return err
	}
	table, err := authorentity.RenderTypoWords(rule, counts)
	if err != nil {
		return err
	}
	_, err = out.Write(table)
	return err
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
