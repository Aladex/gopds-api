// Command author-llm-rescore previews a completed eval run's report under
// this build's validators: every reply the run stored is judged again, and
// the gate is computed from the stored answers and from the rejudged ones.
// It prints one row per stratum — passed and why, before and after, with the
// judges' and the production pair's invalid shares — as TSV, and can write
// the full preview as JSON. It reads only, through a read-only session, and
// names nobody. The preview is never evidence for a registration
// (services.AuthorLLMRescoreNotRegistrable).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
)

const (
	// defaultTimeout bounds the whole read.
	defaultTimeout = 30 * time.Minute
	// exitUsage is the exit code of a call without a run.
	exitUsage = 2
	// jsonFileMode keeps the written preview private to its owner.
	jsonFileMode = 0o600
)

func main() {
	var (
		addr    = flag.String("host", envOr("GOPDS_POSTGRES_DBHOST", "127.0.0.1:5432"), "database host:port")
		user    = flag.String("user", envOr("GOPDS_POSTGRES_DBUSER", "gopds"), "database user")
		pass    = flag.String("password", os.Getenv("GOPDS_POSTGRES_DBPASS"), "database password")
		name    = flag.String("database", envOr("GOPDS_POSTGRES_DBNAME", "gopds"), "database name")
		runID   = flag.Int64("run", 0, "the completed eval run to preview")
		jsonOut = flag.String("json", "", "also write the full preview as JSON to this file")
		timeout = flag.Duration("timeout", defaultTimeout, "how long the whole read may take")
	)
	flag.Parse()
	if *runID <= 0 {
		fmt.Fprintln(os.Stderr, "author llm rescore: -run is required")
		os.Exit(exitUsage)
	}

	db := pg.Connect(&pg.Options{Addr: *addr, User: *user, Password: *pass, Database: *name,
		OnConnect: func(ctx context.Context, conn *pg.Conn) error {
			// Every statement of the session is read-only: a write fails.
			_, err := conn.ExecContext(ctx, `SET default_transaction_read_only = on`)
			return err
		}})
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	err := run(ctx, db, *runID, *jsonOut, os.Stdout, os.Stderr)
	cancel()
	if closeErr := db.Close(); closeErr != nil {
		fmt.Fprintf(os.Stderr, "closing the database: %v\n", closeErr)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "author llm rescore: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, db *pg.DB, runID int64, jsonOut string, out, progress io.Writer) error {
	started := time.Now()
	p, err := services.PreviewAuthorLLMEvalRescore(ctx, db, runID)
	if err != nil {
		return err
	}
	fmt.Fprintf(progress, "%s in %s\n", p, time.Since(started).Round(time.Millisecond))
	if jsonOut != "" {
		raw, marshalErr := json.MarshalIndent(p, "", "  ")
		if marshalErr != nil {
			return marshalErr
		}
		if writeErr := os.WriteFile(jsonOut, raw, jsonFileMode); writeErr != nil {
			return writeErr
		}
	}
	return writeTSV(out, p)
}

// tsvHeader names the columns of writeTSV.
var tsvHeader = []string{"stratum", "holdout", "passed_before", "reason_before", "passed_after", "reason_after",
	"judge_invalid_before", "judge_invalid_after", "pair_invalid_before", "pair_invalid_after"}

// writeTSV prints one row per stratum of either report, sorted.
func writeTSV(out io.Writer, p *services.AuthorLLMRescorePreview) error {
	names := map[string]bool{}
	for name := range p.Before.Strata {
		names[name] = true
	}
	for name := range p.After.Strata {
		names[name] = true
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	slices.Sort(sorted)
	if _, err := fmt.Fprintln(out, strings.Join(tsvHeader, "\t")); err != nil {
		return err
	}
	for _, name := range sorted {
		before, after := p.Before.Strata[name], p.After.Strata[name]
		db, da := decision(&p.Before, name), decision(&p.After, name)
		row := []string{name, fmt.Sprint(max(before.Holdout, after.Holdout)),
			fmt.Sprint(db.Passed), db.Reason, fmt.Sprint(da.Passed), da.Reason,
			shares(before.JudgeInvalid), shares(after.JudgeInvalid),
			pairShares(&before), pairShares(&after)}
		if _, err := fmt.Fprintln(out, strings.Join(row, "\t")); err != nil {
			return err
		}
	}
	return nil
}

// decision is a report's decision on one stratum.
func decision(r *llmeval.Report, stratum string) llmeval.StratumDecision {
	for _, d := range r.Decision.Strata {
		if d.Stratum == stratum {
			return d
		}
	}
	return llmeval.StratumDecision{Stratum: stratum, Reason: "not measured"}
}

// shares renders invalid shares as participant=invalid/total.
func shares(s []llmeval.InvalidShare) string {
	parts := make([]string, 0, len(s))
	for _, x := range s {
		parts = append(parts, fmt.Sprintf("%s=%d/%d", x.Participant, x.Invalid, x.Total))
	}
	return strings.Join(parts, " ")
}

// pairShares renders the production pair's invalid shares at one item per
// request without the excerpt, the combo every run measures.
func pairShares(r *llmeval.StratumReport) string {
	return shares(r.Combos["A1"].Invalid)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
