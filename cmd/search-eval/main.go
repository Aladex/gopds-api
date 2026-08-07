// Command search-eval measures catalog search relevance on a local dump.
//
// It runs a reviewed query set against the current search path (capture mode)
// and reports Recall@k, MRR and the zero-result rate, so relevance changes are
// measured rather than guessed. Compare mode runs the same set the same way and
// judges the aggregates against a capture-mode baseline; the metric functions
// below stay the judge either way. Both modes reach the catalog through
// PGSearchRepository, for books and authors alike — the pre-overhaul paths they
// were first written against no longer exist.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"gopds-api/database"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// Command modes, query kinds and the verdicts a compare run reports.
const (
	modeCapture = "capture"
	modeCompare = "compare"

	kindBooks   = "books"
	kindAuthors = "authors"

	verdictPass       = "pass"
	verdictRegression = "regression"

	// defaultRepeat is how many measured rounds the whole set gets after an
	// unrecorded warm-up round: enough for a stable p95 without a long run.
	defaultRepeat = 20

	// p50 and p95 are the reported percentiles.
	p50 = 0.5
	p95 = 0.95

	// minArgs is the program name plus a mode; the mode's own flags follow it.
	minArgs       = 2
	argsAfterMode = 2

	// exitUsage is the status a wrong invocation exits with.
	exitUsage = 2

	// reportPerm keeps the written report readable only by its owner; it can
	// carry query text from the reviewed set.
	reportPerm = 0o600
)

func main() {
	if len(os.Args) < minArgs {
		usage()
		os.Exit(exitUsage)
	}

	switch os.Args[1] {
	case modeCapture:
		capture(os.Args[argsAfterMode:])
	case modeCompare:
		// The exit code travels back rather than being taken inside compare,
		// so the deferred database close still runs on a regression.
		os.Exit(compare(os.Args[argsAfterMode:]))
	default:
		usage()
		os.Exit(exitUsage)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: search-eval capture -input <queries.json> -out <report.json> [db flags]")
	fmt.Fprintln(os.Stderr, "       search-eval compare -input <queries.json> -baseline <baseline.json> -out <report.json> [db flags]")
}

// querySet is the reviewed input file: one entry per measured search request.
type querySet struct {
	Description string      `json:"description"`
	Queries     []evalQuery `json:"queries"`
}

// evalQuery describes one search request and the textual rule deciding which
// catalog rows are relevant for it. Rules are matched against the same
// normalized form the future search index uses, so a rule written before the
// normalization function exists still means the same thing after it lands.
type evalQuery struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"` // "books" or "authors"
	Query    string `json:"query"`
	Author   string `json:"author_query,omitempty"`
	Language string `json:"language,omitempty"`
	TopK     int    `json:"top_k"`

	ExpectedTitle  string `json:"expected_normalized_title,omitempty"`
	ExpectedAuthor string `json:"expected_normalized_author,omitempty"`
}

// capturedResult is one returned item as a user would see it.
type capturedResult struct {
	ID         int64    `json:"id"`
	Title      string   `json:"title,omitempty"`
	Authors    []string `json:"authors,omitempty"`
	FullName   string   `json:"full_name,omitempty"`
	BooksCount int      `json:"books_count,omitempty"`
}

// queryReport records what the current search returned for one eval query,
// which IDs were relevant, and how that scored. Results/Total come from the
// first measured round; durations come from measured rounds only. Min is
// reported beside the percentiles because it is the least-disturbed sample —
// the one round where nothing else was competing for the machine — and on a
// developer stand that is a steadier estimate of what the query costs than a
// percentile over rounds of varying load.
type queryReport struct {
	evalQuery

	Results         []capturedResult `json:"results"`
	Total           int              `json:"total"`
	DurationsMillis []int64          `json:"durations_ms"`
	MinMillis       int64            `json:"min_ms"`
	P50Millis       int64            `json:"p50_ms"`
	P95Millis       int64            `json:"p95_ms"`
	CaptureNote     string           `json:"capture_note,omitempty"`

	RelevantIDs    []int64 `json:"relevant_ids"`
	RecallAtK      float64 `json:"recall_at_k"`
	ReciprocalRank float64 `json:"reciprocal_rank"`
	ZeroResult     bool    `json:"zero_result"`
}

// catalogFingerprint pins the data the baseline was measured on, so a later
// metric shift can be told apart from a changed dump.
type catalogFingerprint struct {
	Books     int64  `json:"books"`
	Authors   int64  `json:"authors"`
	MaxBookID int64  `json:"max_book_id"`
	GitCommit string `json:"git_commit"`
	// GitDirty records that the tree carried uncommitted changes when the run
	// was measured, so the commit alone does not describe the code. The Phase
	// 8 artifact named 2bffb91 while measuring the uncommitted Phase 8 work;
	// nothing in the file said so.
	GitDirty bool `json:"git_dirty"`
}

// aggregateReport summarizes the whole run. Recall and MRR average only over
// scoreable queries (non-empty relevance set); the zero-result rate covers all.
type aggregateReport struct {
	TotalQueries   int     `json:"total_queries"`
	ScoredQueries  int     `json:"scored_queries"`
	RecallAtK      float64 `json:"recall_at_k"`
	MRR            float64 `json:"mrr"`
	ZeroResultRate float64 `json:"zero_result_rate"`
}

// evalReport is the written artifact of a capture run.
type evalReport struct {
	CapturedAt time.Time          `json:"captured_at"`
	Mode       string             `json:"mode"`
	Database   string             `json:"database"`
	Catalog    catalogFingerprint `json:"catalog"`
	Queries    []queryReport      `json:"queries"`
	Aggregate  aggregateReport    `json:"aggregate"`
	Comparison *comparisonReport  `json:"comparison,omitempty"`
}

// comparisonReport records how a compare run scored against the capture-mode
// baseline it was given.
//
// Every aggregate here is computed over the queries the two runs share, never
// over each run's own set. Comparing whole-run aggregates across different
// corpora subtracts averages with different denominators: when the set grew
// from 12 queries to 13, the published "+0.0665 recall" partly measured the
// query that had been added. Shared-subset deltas mean what they say, and the
// names that differ are reported instead of being averaged away.
type comparisonReport struct {
	SharedQueries     int      `json:"shared_queries"`
	AddedQueries      []string `json:"added_queries,omitempty"`
	MissingQueries    []string `json:"missing_queries,omitempty"`
	BaselineRecallAtK float64  `json:"baseline_recall_at_k"`
	BaselineMRR       float64  `json:"baseline_mrr"`
	BaselineZeroRate  float64  `json:"baseline_zero_result_rate"`
	CurrentRecallAtK  float64  `json:"current_recall_at_k"`
	CurrentMRR        float64  `json:"current_mrr"`
	CurrentZeroRate   float64  `json:"current_zero_result_rate"`
	RecallDelta       float64  `json:"recall_delta"`
	MRRDelta          float64  `json:"mrr_delta"`
	ZeroRateDelta     float64  `json:"zero_result_rate_delta"`
	RegressedQueries  []string `json:"regressed_queries,omitempty"`
	LostQueries       []string `json:"lost_queries,omitempty"`
	Verdict           string   `json:"verdict"` // "pass" or "regression"
}

// connectEval opens the eval connection and makes it the package-global one.
func connectEval(addr, user, pass, name string) *pg.DB {
	db := pg.Connect(&pg.Options{Addr: addr, User: user, Password: pass, Database: name, OnConnect: database.DisableJIT})
	if _, err := db.Exec("SELECT 1"); err != nil {
		fatalf("connecting to %s/%s: %v", addr, name, err)
	}
	database.SetDB(db)
	return db
}

// runSet measures every query of the set, interleaved, and prints one line
// each.
//
// One round visits every query once; rounds repeat. The previous shape ran one
// query twenty times before moving on, which measured a best case no reader
// ever sees: consecutive runs of the same needle inherit everything the
// previous run left in the buffer pool and the OS cache. Worse, a machine busy
// for ten seconds landed entirely inside one query's twenty samples and left
// the rest untouched, so a single report mixed numbers taken under different
// conditions without saying so. Interleaving spreads each query's samples over
// the whole session: neighbors evict each other the way a real workload does,
// and a hiccup is shared rather than assigned to whichever query it happened
// to hit.
//
// It does not make the comparison against the Phase 1 baseline sound. That
// baseline is one frozen sample of code that no longer exists, taken under
// conditions nothing here can reproduce, and no amount of repetition now can
// fix its side of the subtraction.
func runSet(repo *database.PGSearchRepository, db *pg.DB, set querySet, rounds int) []queryReport {
	reports := make([]queryReport, len(set.Queries))
	for i := range set.Queries {
		reports[i] = queryReport{evalQuery: set.Queries[i]}
	}

	// Warm-up round, unrecorded: the first execution of each query pays for
	// plan caching and first-touch page faults that no later round repeats.
	for i := range set.Queries {
		if _, _, _, err := runQuery(repo, &set.Queries[i]); err != nil {
			fatalf("query %q: %v", set.Queries[i].Name, err)
		}
	}

	for round := 0; round < rounds; round++ {
		for i := range set.Queries {
			q := &set.Queries[i]
			start := time.Now()
			results, total, note, err := runQuery(repo, q)
			if err != nil {
				fatalf("query %q: %v", q.Name, err)
			}
			reports[i].DurationsMillis = append(reports[i].DurationsMillis, time.Since(start).Milliseconds())
			if round == 0 {
				reports[i].Results = results
				reports[i].Total = total
				reports[i].CaptureNote = note
			}
		}
	}

	for i := range reports {
		if err := scoreQuery(db, &reports[i]); err != nil {
			fatalf("query %q: %v", reports[i].Name, err)
		}
		rep := &reports[i]
		fmt.Fprintf(os.Stderr,
			"%-36s total=%-6d relevant=%-5d recall=%.3f rr=%.2f min=%dms p50=%dms p95=%dms\n",
			rep.Name, rep.Total, len(rep.RelevantIDs), rep.RecallAtK, rep.ReciprocalRank,
			rep.MinMillis, rep.P50Millis, rep.P95Millis)
	}
	return reports
}

// scoreQuery turns one query's collected rounds into its metrics and resolves
// which catalog rows the reviewed rule calls relevant.
func scoreQuery(db *pg.DB, rep *queryReport) error {
	rep.MinMillis = minDuration(rep.DurationsMillis)
	rep.P50Millis = percentile(rep.DurationsMillis, p50)
	rep.P95Millis = percentile(rep.DurationsMillis, p95)

	if rep.Results == nil {
		rep.Results = []capturedResult{}
	}
	ids := make([]int64, 0, len(rep.Results))
	for _, r := range rep.Results {
		ids = append(ids, r.ID)
	}

	relevant, err := resolveRelevant(context.Background(), db, &rep.evalQuery)
	if err != nil {
		return fmt.Errorf("resolving relevance: %w", err)
	}
	if relevant == nil {
		relevant = []int64{}
	}
	rep.RelevantIDs = relevant

	want := make(map[int64]struct{}, len(relevant))
	for _, id := range relevant {
		want[id] = struct{}{}
	}
	rep.RecallAtK = recallAt(ids, want, rep.TopK)
	rep.ReciprocalRank = reciprocalRank(ids, want)
	rep.ZeroResult = rep.Total == 0
	return nil
}

// minDuration is the fastest recorded round, or zero when nothing was measured.
func minDuration(durations []int64) int64 {
	if len(durations) == 0 {
		return 0
	}
	least := durations[0]
	for _, d := range durations[1:] {
		if d < least {
			least = d
		}
	}
	return least
}

// writeReport persists one run's artifact.
func writeReport(path string, report *evalReport) {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fatalf("encoding report: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), reportPerm); err != nil {
		fatalf("writing %s: %v", path, err)
	}
	fmt.Fprintf(os.Stderr, "wrote %s\n", path)
}

func capture(args []string) {
	fs := flag.NewFlagSet("capture", flag.ExitOnError)
	var (
		input  = fs.String("input", "", "reviewed query set JSON (required)")
		out    = fs.String("out", "", "report output path (required)")
		repeat = fs.Int("repeat", defaultRepeat, "measured rounds over the whole set, after one unrecorded warm-up round")
		addr   = fs.String("host", envOr("GOPDS_POSTGRES_DBHOST", "127.0.0.1:5432"), "database host:port")
		user   = fs.String("user", envOr("GOPDS_POSTGRES_DBUSER", "gopds"), "database user")
		pass   = fs.String("password", os.Getenv("GOPDS_POSTGRES_DBPASS"), "database password")
		name   = fs.String("database", envOr("GOPDS_POSTGRES_DBNAME", "gopds"), "database name")
	)
	_ = fs.Parse(args)

	if *input == "" || *out == "" || *repeat < 1 {
		usage()
		os.Exit(exitUsage)
	}

	set, err := loadQuerySet(*input)
	if err != nil {
		fatalf("loading query set: %v", err)
	}

	db := connectEval(*addr, *user, *pass, *name)
	defer func() { _ = db.Close() }()

	report := evalReport{
		CapturedAt: time.Now().UTC(),
		Mode:       modeCapture,
		Database:   fmt.Sprintf("%s@%s/%s", *user, *addr, *name),
		Catalog:    fingerprint(context.Background(), db),
	}
	report.Queries = runSet(database.NewPGSearchRepository(db), db, set, *repeat)
	report.Aggregate = aggregate(report.Queries)
	writeReport(*out, &report)
}

// compare re-runs the query set against the current search path and judges the
// aggregates against a capture-mode baseline. Books and authors both go through
// PGSearchRepository: until Phase 8 the author lane still ran the pre-overhaul
// code, so its numbers were carried over from the baseline unchanged and the
// aggregates flattered nothing but also proved nothing about authors. A changed
// catalog fingerprint means the baseline no longer describes this data and
// the run refuses to compare.
// compare returns the process exit status: zero when the gate passes.
func compare(args []string) int {
	fs := flag.NewFlagSet("compare", flag.ExitOnError)
	var (
		input    = fs.String("input", "", "reviewed query set JSON (required)")
		baseline = fs.String("baseline", "", "capture-mode baseline report JSON (required)")
		out      = fs.String("out", "", "report output path (required)")
		repeat   = fs.Int("repeat", defaultRepeat, "measured rounds over the whole set, after one unrecorded warm-up round")
		addr     = fs.String("host", envOr("GOPDS_POSTGRES_DBHOST", "127.0.0.1:5432"), "database host:port")
		user     = fs.String("user", envOr("GOPDS_POSTGRES_DBUSER", "gopds"), "database user")
		pass     = fs.String("password", os.Getenv("GOPDS_POSTGRES_DBPASS"), "database password")
		name     = fs.String("database", envOr("GOPDS_POSTGRES_DBNAME", "gopds"), "database name")
	)
	_ = fs.Parse(args)

	if *input == "" || *baseline == "" || *out == "" || *repeat < 1 {
		usage()
		os.Exit(exitUsage)
	}

	set, err := loadQuerySet(*input)
	if err != nil {
		fatalf("loading query set: %v", err)
	}
	base, err := loadBaseline(*baseline)
	if err != nil {
		fatalf("loading baseline: %v", err)
	}

	db := connectEval(*addr, *user, *pass, *name)
	defer func() { _ = db.Close() }()

	report := evalReport{
		CapturedAt: time.Now().UTC(),
		Mode:       modeCompare,
		Database:   fmt.Sprintf("%s@%s/%s", *user, *addr, *name),
		Catalog:    fingerprint(context.Background(), db),
	}

	if report.Catalog.Books != base.Catalog.Books ||
		report.Catalog.Authors != base.Catalog.Authors ||
		report.Catalog.MaxBookID != base.Catalog.MaxBookID {
		fatalf("catalog changed since the baseline: %+v vs baseline %+v — recapture the baseline instead of comparing",
			report.Catalog, base.Catalog)
	}

	report.Queries = runSet(database.NewPGSearchRepository(db), db, set, *repeat)
	report.Aggregate = aggregate(report.Queries)
	cmp := compareAggregates(report.Queries, base.Queries)
	report.Comparison = &cmp
	writeReport(*out, &report)

	printComparison(&cmp)
	if cmp.Verdict != verdictPass {
		return 1
	}
	return 0
}

// printComparison narrates the verdict: the shared-subset deltas, then every
// name that did not overlap or stopped behaving, each said out loud rather
// than folded into an average.
func printComparison(cmp *comparisonReport) {
	fmt.Fprintf(os.Stderr,
		"vs baseline over %d shared queries: recall %.4f (%+.4f), mrr %.4f (%+.4f), zero %.4f (%+.4f) — %s\n",
		cmp.SharedQueries,
		cmp.CurrentRecallAtK, cmp.RecallDelta,
		cmp.CurrentMRR, cmp.MRRDelta,
		cmp.CurrentZeroRate, cmp.ZeroRateDelta, cmp.Verdict)
	for _, line := range []struct {
		label string
		names []string
	}{
		{"not in the baseline, excluded from the deltas", cmp.AddedQueries},
		{"in the baseline but not measured here", cmp.MissingQueries},
		{"lower recall than the baseline", cmp.RegressedQueries},
		{"stopped finding what they used to find", cmp.LostQueries},
	} {
		if len(line.names) > 0 {
			fmt.Fprintf(os.Stderr, "%s: %s\n", line.label, strings.Join(line.names, ", "))
		}
	}
}

// loadBaseline reads a capture-mode report to compare against.
func loadBaseline(path string) (evalReport, error) {
	// #nosec G304 -- a developer tool reading the baseline the operator named on the command line
	data, err := os.ReadFile(path)
	if err != nil {
		return evalReport{}, err
	}
	var rep evalReport
	if err := json.Unmarshal(data, &rep); err != nil {
		return evalReport{}, err
	}
	if rep.Mode != "capture" {
		return evalReport{}, fmt.Errorf("baseline must be a capture-mode report, got mode %q", rep.Mode)
	}
	return rep, nil
}

// pairQueries walks this run against the baseline by name, collecting the
// overlapping pairs and noting, on the way, which queries are new, which lost
// recall and which stopped finding what they used to find.
func pairQueries(cmp *comparisonReport, queries []queryReport, baseByName map[string]*queryReport) (shared, baseShared []queryReport) {
	const eps = 1e-9
	for i := range queries {
		q := &queries[i]
		base, ok := baseByName[q.Name]
		if !ok {
			cmp.AddedQueries = append(cmp.AddedQueries, q.Name)
			continue
		}
		shared = append(shared, *q)
		baseShared = append(baseShared, *base)

		if q.RecallAtK < base.RecallAtK-eps {
			cmp.RegressedQueries = append(cmp.RegressedQueries, q.Name)
		}
		// The accepted item was found before and is not found now.
		if base.ReciprocalRank > eps && q.ReciprocalRank <= eps {
			cmp.LostQueries = append(cmp.LostQueries, q.Name)
		}
		// The query answered before and answers with nothing now.
		if !base.ZeroResult && q.ZeroResult {
			cmp.LostQueries = append(cmp.LostQueries, q.Name)
		}
	}
	return shared, baseShared
}

// compareAggregates judges this run against the baseline over the queries the
// two share, and reports what did not overlap.
//
// Three ways to fail, not one. Aggregates that fall is the obvious one. A
// baseline query this run does not measure is the second: a gate that averages
// only what is still present can be turned green by deleting the inconvenient
// query, so a missing name fails outright. A query whose accepted item stopped
// appearing at all is the third — the plan's "no critical golden query missing
// its accepted item" — because a large enough win elsewhere hides it in the
// average. A recall drop that keeps the accepted item is reported by name but
// does not fail: ranking changes trade recall between queries by design.
//
// Floats compare with a small epsilon so representation noise is not a
// regression.
func compareAggregates(queries, baselineQueries []queryReport) comparisonReport {
	const eps = 1e-9

	baseByName := make(map[string]*queryReport, len(baselineQueries))
	for i := range baselineQueries {
		baseByName[baselineQueries[i].Name] = &baselineQueries[i]
	}
	currentByName := make(map[string]*queryReport, len(queries))
	for i := range queries {
		currentByName[queries[i].Name] = &queries[i]
	}

	var cmp comparisonReport
	shared, baseShared := pairQueries(&cmp, queries, baseByName)
	for i := range baselineQueries {
		if _, ok := currentByName[baselineQueries[i].Name]; !ok {
			cmp.MissingQueries = append(cmp.MissingQueries, baselineQueries[i].Name)
		}
	}

	current := aggregate(shared)
	baseline := aggregate(baseShared)
	cmp.SharedQueries = len(shared)
	cmp.BaselineRecallAtK, cmp.BaselineMRR, cmp.BaselineZeroRate = baseline.RecallAtK, baseline.MRR, baseline.ZeroResultRate
	cmp.CurrentRecallAtK, cmp.CurrentMRR, cmp.CurrentZeroRate = current.RecallAtK, current.MRR, current.ZeroResultRate
	cmp.RecallDelta = current.RecallAtK - baseline.RecallAtK
	cmp.MRRDelta = current.MRR - baseline.MRR
	cmp.ZeroRateDelta = current.ZeroResultRate - baseline.ZeroResultRate

	cmp.Verdict = verdictPass
	switch {
	case len(cmp.MissingQueries) > 0, len(cmp.LostQueries) > 0:
		cmp.Verdict = verdictRegression
	case cmp.RecallDelta < -eps, cmp.MRRDelta < -eps, cmp.ZeroRateDelta > eps:
		cmp.Verdict = verdictRegression
	}
	return cmp
}

func loadQuerySet(path string) (querySet, error) {
	// #nosec G304 -- a developer tool reading the query set the operator named on the command line
	data, err := os.ReadFile(path)
	if err != nil {
		return querySet{}, err
	}
	var set querySet
	if err := json.Unmarshal(data, &set); err != nil {
		return querySet{}, err
	}
	for i, q := range set.Queries {
		if q.Name == "" || q.Query == "" || (q.Kind != kindBooks && q.Kind != kindAuthors) {
			return querySet{}, fmt.Errorf("entry %d: need name, query and kind books|authors", i)
		}
		if q.TopK <= 0 {
			return querySet{}, fmt.Errorf("entry %d (%s): top_k must be positive", i, q.Name)
		}
		if q.ExpectedTitle == "" && q.ExpectedAuthor == "" {
			return querySet{}, fmt.Errorf("entry %d (%s): a relevance rule is required", i, q.Name)
		}
	}
	return set, nil
}

// runQuery executes one eval query through the current public search path:
// the search repository, for books and authors alike. Both modes use it, so a
// capture and a compare on the same catalog measure the same code.
func runQuery(repo *database.PGSearchRepository, q *evalQuery) (results []capturedResult, total int, note string, err error) {
	switch q.Kind {
	case kindBooks:
		page, err := repo.SearchBooks(context.Background(), models.BookSearchRequest{
			Query:       q.Query,
			AuthorQuery: q.Author,
			Language:    q.Language,
			Limit:       q.TopK,
		})
		if err != nil {
			return nil, 0, "", err
		}
		note := ""
		if q.Author != "" {
			note = "title and author travel in one SQL request; the Phase 1 baseline used a " +
				"200-requested (effective 100) title window plus a Go-side author filter"
		}
		return bookResults(page.Books), page.Total, note, nil
	case kindAuthors:
		// The repository reads an empty language as "every language" — the
		// service's normalizeLanguage folds AllLanguages down to this before
		// the call, and the eval talks to the repository directly.
		page, err := repo.SearchAuthors(context.Background(), models.AuthorSearchRequest{
			Query:    q.Query,
			Language: q.Language,
			Limit:    q.TopK,
		})
		if err != nil {
			return nil, 0, "", err
		}
		results := make([]capturedResult, 0, len(page.Authors))
		for _, a := range page.Authors {
			results = append(results, capturedResult{ID: a.ID, FullName: a.FullName, BooksCount: a.BooksCount})
		}
		return results, page.Total, "", nil
	}
	return nil, 0, "", fmt.Errorf("unknown kind %q", q.Kind)
}

func bookResults(books []models.Book) []capturedResult {
	results := make([]capturedResult, 0, len(books))
	for i := range books {
		b := &books[i]
		names := make([]string, 0, len(b.Authors))
		for _, a := range b.Authors {
			names = append(names, a.FullName)
		}
		results = append(results, capturedResult{ID: b.ID, Title: b.Title, Authors: names})
	}
	return results
}

// normalizedExpr is the Phase 1 spelling of the canonical normalization,
// inlined because migration 20 has not landed yet. It must stay byte-identical
// to the function body in database_migrations/20-search-normalization.sql.
const normalizedExpr = `trim(regexp_replace(regexp_replace(` +
	`replace(lower(normalize(%s, NFC)), 'ё', 'е'), '[^[:alnum:]]+', ' ', 'g'), '[[:space:]]+', ' ', 'g'))`

// resolveRelevant returns every visible catalog ID satisfying the query's
// relevance rule: normalized title contains the rule, and when an author rule
// is present the book must have an author whose normalized name contains it.
func resolveRelevant(ctx context.Context, db *pg.DB, q *evalQuery) ([]int64, error) {
	titleExpr := fmt.Sprintf(normalizedExpr, "b.title")
	authorExpr := fmt.Sprintf(normalizedExpr, "a.full_name")

	var ids []int64
	switch q.Kind {
	case "books":
		_, err := db.WithContext(ctx).Query(&ids, fmt.Sprintf(`
			SELECT b.id FROM opds_catalog_book b
			WHERE b.approved AND NOT b.duplicate_hidden
				AND (? = '' OR b.lang = ?)
				AND strpos(%s, ?) > 0
				AND (? = '' OR EXISTS (
					SELECT 1 FROM opds_catalog_bauthor ba
					JOIN opds_catalog_author a ON a.id = ba.author_id
					WHERE ba.book_id = b.id AND strpos(%s, ?) > 0))
			ORDER BY b.id`, titleExpr, authorExpr),
			q.Language, q.Language, q.ExpectedTitle, q.ExpectedAuthor, q.ExpectedAuthor)
		return ids, err
	case "authors":
		_, err := db.WithContext(ctx).Query(&ids, fmt.Sprintf(`
			SELECT a.id FROM opds_catalog_author a
			WHERE strpos(%s, ?) > 0
				AND EXISTS (
					SELECT 1 FROM opds_catalog_bauthor ba
					JOIN opds_catalog_book b ON b.id = ba.book_id
					WHERE ba.author_id = a.id AND b.approved AND NOT b.duplicate_hidden
						AND (? = '' OR b.lang = ?))
			ORDER BY a.id`, authorExpr),
			q.ExpectedAuthor, q.Language, q.Language)
		return ids, err
	}
	return nil, fmt.Errorf("unknown kind %q", q.Kind)
}

// aggregate averages per-query metrics into the run summary.
func aggregate(reports []queryReport) aggregateReport {
	agg := aggregateReport{TotalQueries: len(reports)}
	var recallSum, rrSum float64
	totals := make([]int, 0, len(reports))
	for i := range reports {
		r := &reports[i]
		totals = append(totals, r.Total)
		if len(r.RelevantIDs) == 0 {
			continue
		}
		agg.ScoredQueries++
		recallSum += r.RecallAtK
		rrSum += r.ReciprocalRank
	}
	if agg.ScoredQueries > 0 {
		agg.RecallAtK = recallSum / float64(agg.ScoredQueries)
		agg.MRR = rrSum / float64(agg.ScoredQueries)
	}
	agg.ZeroResultRate = zeroResultRate(totals)
	return agg
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// fingerprint captures the identity of the catalog being measured: row
// counts, the highest book ID, and the working tree's commit.
func fingerprint(ctx context.Context, db *pg.DB) catalogFingerprint {
	var fp catalogFingerprint
	if _, err := db.WithContext(ctx).QueryOne(pg.Scan(&fp.Books),
		`SELECT count(*) FROM opds_catalog_book`); err != nil {
		fatalf("fingerprint: counting books: %v", err)
	}
	if _, err := db.WithContext(ctx).QueryOne(pg.Scan(&fp.Authors),
		`SELECT count(*) FROM opds_catalog_author`); err != nil {
		fatalf("fingerprint: counting authors: %v", err)
	}
	if _, err := db.WithContext(ctx).QueryOne(pg.Scan(&fp.MaxBookID),
		`SELECT max(id) FROM opds_catalog_book`); err != nil {
		fatalf("fingerprint: max book id: %v", err)
	}
	if out, err := exec.CommandContext(ctx, "git", "rev-parse", "HEAD").Output(); err == nil {
		fp.GitCommit = strings.TrimSpace(string(out))
	}
	if out, err := exec.CommandContext(ctx, "git", "status", "--porcelain").Output(); err == nil {
		fp.GitDirty = strings.TrimSpace(string(out)) != ""
	}
	return fp
}

// percentile returns the nearest-rank percentile of values without mutating
// the caller's slice. Empty input yields 0.
func percentile(values []int64, p float64) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := make([]int64, len(values))
	copy(sorted, values)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	rank := int(math.Ceil(p * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	return sorted[rank-1]
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "search-eval: "+format+"\n", args...)
	os.Exit(1)
}

// recallAt reports which fraction of the relevant IDs appears in the first k
// results. An empty relevance set yields 0: such a query is not scoreable and
// the caller is expected to exclude it from aggregates.
func recallAt(got []int64, want map[int64]struct{}, k int) float64 {
	if len(want) == 0 || k <= 0 {
		return 0
	}
	if k > len(got) {
		k = len(got)
	}
	hits := 0
	for _, id := range got[:k] {
		if _, ok := want[id]; ok {
			hits++
		}
	}
	return float64(hits) / float64(len(want))
}

// reciprocalRank scores the position of the first relevant result: 1 for rank
// 1, 1/2 for rank 2, and 0 when no relevant ID appears at all.
func reciprocalRank(got []int64, want map[int64]struct{}) float64 {
	if len(want) == 0 {
		return 0
	}
	for i, id := range got {
		if _, ok := want[id]; ok {
			return 1 / float64(i+1)
		}
	}
	return 0
}

// zeroResultRate reports which fraction of the queries returned no results.
func zeroResultRate(totals []int) float64 {
	if len(totals) == 0 {
		return 0
	}
	zeros := 0
	for _, total := range totals {
		if total == 0 {
			zeros++
		}
	}
	return float64(zeros) / float64(len(totals))
}
