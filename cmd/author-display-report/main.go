// Command author-display-report compares the catalog's legacy authors with
// the author layer, book by book, and prints the counts and sample book IDs
// of each category as JSON. It reads only, and names nobody: what a sample
// book shows is looked up in the interface.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"gopds-api/database"

	"github.com/go-pg/pg/v10"
)

// defaultTimeout bounds the whole read; a catalog of half a million books
// takes minutes.
const defaultTimeout = 30 * time.Minute

func main() {
	var (
		addr    = flag.String("host", envOr("GOPDS_POSTGRES_DBHOST", "127.0.0.1:5432"), "database host:port")
		user    = flag.String("user", envOr("GOPDS_POSTGRES_DBUSER", "gopds"), "database user")
		pass    = flag.String("password", os.Getenv("GOPDS_POSTGRES_DBPASS"), "database password")
		name    = flag.String("database", envOr("GOPDS_POSTGRES_DBNAME", "gopds"), "database name")
		afterID = flag.Int64("after-id", 0, "start after this book ID")
		timeout = flag.Duration("timeout", defaultTimeout, "how long the whole read may take")
	)
	flag.Parse()

	db := pg.Connect(&pg.Options{Addr: *addr, User: *user, Password: *pass, Database: *name})
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	err := run(ctx, db, *afterID, os.Stdout, os.Stderr)
	cancel()
	if closeErr := db.Close(); closeErr != nil {
		fmt.Fprintf(os.Stderr, "closing the database: %v\n", closeErr)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "author display report: %v\n", err)
		os.Exit(1)
	}
}

// run reads every book above afterID and writes the report to out, and how
// many books it read in how long to progress.
func run(ctx context.Context, db pg.DBI, afterID int64, out, progress io.Writer) error {
	started := time.Now()
	report, err := database.CompareAuthorDisplay(ctx, db, afterID, 0)
	if err != nil {
		return err
	}
	fmt.Fprintf(progress, "%d books in %s\n", report.Books, time.Since(started).Round(time.Millisecond))
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
