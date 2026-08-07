// Command search-load measures what concurrent search does to the database.
//
// Every latency number the lexical-search work produced so far was taken one
// query at a time. That answers "how long does this query take" and says
// nothing about the question that decides a rollout: what happens when twenty
// readers search at once against a plan that touches tens of thousands of heap
// pages. A query that is comfortable alone can saturate I/O, evict everything
// else from the buffer pool, and slow down endpoints that have nothing to do
// with search.
//
// It drives the real service over the real repository, so the connection pool,
// the service validation and the SQL are all in the path. Per concurrency
// level it reports throughput and latency percentiles, and the PostgreSQL
// buffer counters around the run — the hit/read split is what separates a
// CPU-bound plan from an I/O-bound one, and those two conclusions point at
// different fixes.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"time"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
)

const (
	// reportPerm keeps the written report readable only by its owner.
	reportPerm = 0o600

	// defaultSeconds is how long one concurrency level is measured.
	defaultSeconds = 15

	// durationsHint is a worker's initial sample capacity.
	durationsHint = 256

	// p50, p95 and p99: the last one matters here in a way it does not for a
	// single-query measurement, because saturation shows up in the tail first.
	p50 = 0.5
	p95 = 0.95
	p99 = 0.99
)

// querySet is the reviewed corpus, read in the same shape search-eval uses.
type querySet struct {
	Queries []struct {
		Name     string `json:"name"`
		Kind     string `json:"kind"`
		Query    string `json:"query"`
		Author   string `json:"author_query,omitempty"`
		Language string `json:"language,omitempty"`
		TopK     int    `json:"top_k"`
	} `json:"queries"`
}

// levelReport is one concurrency level's result.
type levelReport struct {
	Concurrency   int     `json:"concurrency"`
	Requests      int     `json:"requests"`
	Errors        int     `json:"errors"`
	Seconds       float64 `json:"seconds"`
	Throughput    float64 `json:"requests_per_second"`
	P50Millis     int64   `json:"p50_ms"`
	P95Millis     int64   `json:"p95_ms"`
	P99Millis     int64   `json:"p99_ms"`
	MaxMillis     int64   `json:"max_ms"`
	BlocksHit     int64   `json:"blocks_hit"`
	BlocksRead    int64   `json:"blocks_read"`
	CacheHitRatio float64 `json:"cache_hit_ratio"`
}

type loadReport struct {
	StartedAt time.Time     `json:"started_at"`
	Database  string        `json:"database"`
	Profile   string        `json:"profile"`
	Queries   []string      `json:"queries"`
	Levels    []levelReport `json:"levels"`
}

func main() {
	fs := flag.NewFlagSet("search-load", flag.ExitOnError)
	var (
		input    = fs.String("input", "database/testdata/search_catalog_queries.json", "reviewed query set JSON")
		only     = fs.String("only", "", "measure only this query name; empty means the whole mix")
		levels   = fs.String("levels", "1,2,4,8,16", "comma-separated concurrency levels")
		seconds  = fs.Int("seconds", defaultSeconds, "measured seconds per level")
		out      = fs.String("out", "", "optional report output path")
		addr     = fs.String("host", envOr("GOPDS_POSTGRES_DBHOST", "127.0.0.1:5432"), "database host:port")
		user     = fs.String("user", envOr("GOPDS_POSTGRES_DBUSER", "gopds"), "database user")
		password = fs.String("password", os.Getenv("GOPDS_POSTGRES_DBPASS"), "database password")
		name     = fs.String("database", envOr("GOPDS_POSTGRES_DBNAME", "gopds"), "database name")
	)
	_ = fs.Parse(os.Args[1:])

	set, err := loadQuerySet(*input, *only)
	if err != nil {
		fatalf("loading query set: %v", err)
	}
	if len(set) == 0 {
		fatalf("no queries selected")
	}
	steps, err := parseLevels(*levels)
	if err != nil {
		fatalf("parsing levels: %v", err)
	}

	// The pool is left at go-pg's default, because that is what production
	// runs: cmd/gopds builds pg.Options without PoolSize, and the configured
	// postgres.max_conns is read into the config struct and never used.
	db := pg.Connect(&pg.Options{
		Addr: *addr, User: *user, Password: *password, Database: *name,
		OnConnect: database.DisableJIT,
	})
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("SELECT 1"); err != nil {
		fatalf("connecting to %s/%s: %v", *addr, *name, err)
	}

	// The service logs one completion entry per request. Useful in production,
	// meaningless at a few hundred requests a second, and the writing itself
	// would be measured as part of the search.
	logging.GetLogger().SetOutput(io.Discard)

	svc := services.NewSearchService(database.NewPGSearchRepository(db))
	profile := "mixed"
	if *only != "" {
		profile = *only
	}

	report := loadReport{
		StartedAt: time.Now().UTC(),
		Database:  fmt.Sprintf("%s@%s/%s", *user, *addr, *name),
		Profile:   profile,
	}
	for _, q := range set {
		report.Queries = append(report.Queries, q.Query)
	}

	report.Levels = runLevels(db, svc, set, steps, time.Duration(*seconds)*time.Second, profile)

	writeReport(*out, &report)
}

// writeReport persists the run when an output path was named.
func writeReport(path string, report *loadReport) {
	if path == "" {
		return
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fatalf("encoding report: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), reportPerm); err != nil {
		fatalf("writing %s: %v", path, err)
	}
	fmt.Fprintf(os.Stderr, "\nwrote %s\n", path)
}

// runLevels walks the concurrency ladder, printing each rung as it lands.
func runLevels(db *pg.DB, svc services.PublicSearch, cases []searchCase,
	steps []int, duration time.Duration, profile string,
) []levelReport {
	fmt.Fprintf(os.Stderr, "profile %q over %d queries, %s per level\n\n", profile, len(cases), duration)
	fmt.Fprintf(os.Stderr, "%-6s %9s %8s %8s %8s %8s %9s %12s %12s\n",
		"conc", "req/s", "p50", "p95", "p99", "max", "errors", "blocks hit", "blocks read")

	levels := make([]levelReport, 0, len(steps))
	for _, concurrency := range steps {
		level := runLevel(db, svc, cases, concurrency, duration)
		levels = append(levels, level)
		fmt.Fprintf(os.Stderr, "%-6d %9.1f %6dms %6dms %6dms %6dms %9d %12d %12d\n",
			level.Concurrency, level.Throughput, level.P50Millis, level.P95Millis,
			level.P99Millis, level.MaxMillis, level.Errors, level.BlocksHit, level.BlocksRead)
	}
	return levels
}

// searchCase is one request the load generator can issue.
type searchCase struct {
	Query    string
	Author   string
	Language string
	Limit    int
}

// runLevel drives one concurrency level for the given duration.
//
// Workers start at different offsets into the query list so that at any moment
// the mix is spread across it rather than every worker hammering the same
// needle, which would measure a cache the real workload does not share.
func runLevel(db *pg.DB, svc services.PublicSearch, cases []searchCase, concurrency int, duration time.Duration) levelReport {
	// Workers stop between requests rather than being canceled inside one.
	// A request killed mid-flight leaves its pooled connection in a bad state
	// and contributes a duration that measures the deadline, not the search.
	// The level therefore overruns by at most one request, and throughput is
	// computed from the elapsed time rather than from the requested one.
	done := make(chan struct{})
	go func() {
		time.Sleep(duration)
		close(done)
	}()

	hitBefore, readBefore := bufferCounters(db)
	started := time.Now()

	var (
		mu        sync.Mutex
		durations []int64
		errors    int
		wg        sync.WaitGroup
	)
	for worker := 0; worker < concurrency; worker++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			local, localErrors := driveWorker(svc, cases, offset, done)
			mu.Lock()
			durations = append(durations, local...)
			errors += localErrors
			mu.Unlock()
		}(worker)
	}
	wg.Wait()

	elapsed := time.Since(started).Seconds()
	hitAfter, readAfter := bufferCounters(db)

	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	level := levelReport{
		Concurrency: concurrency,
		Requests:    len(durations),
		Errors:      errors,
		Seconds:     elapsed,
		P50Millis:   percentile(durations, p50),
		P95Millis:   percentile(durations, p95),
		P99Millis:   percentile(durations, p99),
		BlocksHit:   hitAfter - hitBefore,
		BlocksRead:  readAfter - readBefore,
	}
	if len(durations) > 0 {
		level.MaxMillis = durations[len(durations)-1]
		level.Throughput = float64(len(durations)) / elapsed
	}
	if total := level.BlocksHit + level.BlocksRead; total > 0 {
		level.CacheHitRatio = float64(level.BlocksHit) / float64(total)
	}
	return level
}

// driveWorker issues requests until the level ends, returning its samples and
// how many requests failed.
func driveWorker(svc services.PublicSearch, cases []searchCase, offset int, done <-chan struct{}) (samples []int64, failures int) {
	samples = make([]int64, 0, durationsHint)
	for i := 0; ; i++ {
		select {
		case <-done:
			return samples, failures
		default:
		}

		c := cases[(offset+i)%len(cases)]
		start := time.Now()
		_, err := svc.SearchBooks(context.Background(), models.BookSearchRequest{
			Query:       c.Query,
			AuthorQuery: c.Author,
			Language:    c.Language,
			Limit:       c.Limit,
		})
		elapsed := time.Since(start).Milliseconds()
		if err != nil {
			failures++
			continue
		}
		samples = append(samples, elapsed)
	}
}

// bufferCounters reads this database's cumulative buffer hit and read counts,
// so a level's delta says how much of its working set came from PostgreSQL's
// own pool and how much it had to fetch.
func bufferCounters(db *pg.DB) (hit, read int64) {
	if _, err := db.QueryOne(pg.Scan(&hit, &read),
		`SELECT coalesce(blks_hit, 0), coalesce(blks_read, 0)
		 FROM pg_stat_database WHERE datname = current_database()`); err != nil {
		fatalf("reading buffer counters: %v", err)
	}
	return hit, read
}

// percentile takes the value at the given rank of an already sorted slice.
func percentile(sorted []int64, q float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(q * float64(len(sorted)))
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

// loadQuerySet reads the reviewed corpus and keeps the book searches, which
// are the ones whose plans touch the catalog at scale.
func loadQuerySet(path, only string) ([]searchCase, error) {
	// #nosec G304 -- a developer tool reading the query set the operator named
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var set querySet
	if err := json.Unmarshal(data, &set); err != nil {
		return nil, err
	}
	cases := make([]searchCase, 0, len(set.Queries))
	for _, q := range set.Queries {
		if q.Kind != "books" {
			continue
		}
		if only != "" && q.Name != only {
			continue
		}
		limit := q.TopK
		if limit <= 0 {
			limit = 10
		}
		cases = append(cases, searchCase{
			Query: q.Query, Author: q.Author, Language: q.Language, Limit: limit,
		})
	}
	return cases, nil
}

func parseLevels(spec string) ([]int, error) {
	var levels []int
	for _, part := range splitComma(spec) {
		var n int
		if _, err := fmt.Sscanf(part, "%d", &n); err != nil || n < 1 {
			return nil, fmt.Errorf("bad concurrency level %q", part)
		}
		levels = append(levels, n)
	}
	if len(levels) == 0 {
		return nil, fmt.Errorf("no levels given")
	}
	return levels, nil
}

func splitComma(s string) []string {
	var out []string
	current := ""
	for _, r := range s {
		if r == ',' {
			if current != "" {
				out = append(out, current)
			}
			current = ""
			continue
		}
		current += string(r)
	}
	if current != "" {
		out = append(out, current)
	}
	return out
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "search-load: "+format+"\n", args...)
	os.Exit(1)
}
