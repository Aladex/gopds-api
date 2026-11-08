// Command book-variants-report runs the book grouping rule over the whole
// catalog and prints how many cards it makes, as JSON. It only reads: every
// connection is read-only, and it names no book or author. It is the check to
// run on production data before a change to the rule is released.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"gopds-api/internal/bookvariants"

	"github.com/go-pg/pg/v10"
)

const (
	defaultTimeout = 30 * time.Minute
	// defaultChunk keeps each statement short on a database that serves
	// readers: the catalog is read 25,000 IDs at a time.
	defaultChunk = 25000
)

func main() {
	var (
		addr    = flag.String("host", envOr("GOPDS_POSTGRES_DBHOST", "127.0.0.1:5432"), "database host:port")
		user    = flag.String("user", envOr("GOPDS_POSTGRES_DBUSER", "gopds"), "database user")
		pass    = flag.String("password", os.Getenv("GOPDS_POSTGRES_DBPASS"), "database password")
		name    = flag.String("database", envOr("GOPDS_POSTGRES_DBNAME", "gopds"), "database name")
		chunk   = flag.Int64("chunk", defaultChunk, "book IDs read per statement")
		timeout = flag.Duration("timeout", defaultTimeout, "how long the whole read may take")
	)
	flag.Parse()

	db := pg.Connect(&pg.Options{
		Addr: *addr, User: *user, Password: *pass, Database: *name,
		OnConnect: func(ctx context.Context, conn *pg.Conn) error {
			_, err := conn.ExecContext(ctx, "SET default_transaction_read_only = on")
			return err
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	err := run(ctx, db, *chunk, os.Stdout, os.Stderr)
	cancel()
	if closeErr := db.Close(); closeErr != nil {
		fmt.Fprintf(os.Stderr, "closing the database: %v\n", closeErr)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "book variants report: %v\n", err)
		os.Exit(1)
	}
}

// censusBooksSQL reads one chunk of visible books with what the rule reads.
const censusBooksSQL = `
SELECT b.id, coalesce(b.lang, '') AS lang, coalesce(b.title, '') AS title, b.registerdate,
    coalesce(b.docdate, '') AS docdate, coalesce(b.cover, false) AS cover, coalesce(b.annotation, '') <> '' AS annotation,
    coalesce(s.source_document_id, '') AS document_id, coalesce(s.source_document_version, '') AS doc_version,
    coalesce(trim(s.source_publisher), '') <> '' AS publisher, coalesce(trim(s.source_year), '') <> '' AS year,
    (SELECT array_agg(DISTINCT x::text ORDER BY x::text)
        FROM book_author_display AS d, unnest(d.entity_ids) AS x WHERE d.book_id = b.id) AS entity_ids,
    EXISTS (SELECT 1 FROM book_author_display AS d
        WHERE d.book_id = b.id AND coalesce(cardinality(d.entity_ids), 0) = 0) AS unlinked,
    (SELECT array_agg(e.display_name) FROM author_entity AS e
        WHERE e.id IN (SELECT unnest(d.entity_ids) FROM book_author_display AS d WHERE d.book_id = b.id)) AS entity_names,
    (SELECT array_agg(bs.ser_id::text || ':' || bs.ser_no::text)
        FROM opds_catalog_bseries AS bs WHERE bs.book_id = b.id AND bs.ser_no > 0) AS series
FROM opds_catalog_book AS b
LEFT JOIN book_metadata_snapshot AS s ON s.book_id = b.id AND s.is_current
WHERE b.approved AND NOT b.duplicate_hidden AND b.id >= ? AND b.id < ?`

// censusRow is one row of censusBooksSQL.
type censusRow struct {
	ID           int64     `pg:"id"`
	Lang         string    `pg:"lang"`
	Title        string    `pg:"title"`
	RegisterDate time.Time `pg:"registerdate"`
	DocDate      string    `pg:"docdate"`
	Cover        bool      `pg:"cover"`
	Annotation   bool      `pg:"annotation"`
	DocumentID   string    `pg:"document_id"`
	DocVersion   string    `pg:"doc_version"`
	Publisher    bool      `pg:"publisher"`
	Year         bool      `pg:"year"`
	EntityIDs    []string  `pg:"entity_ids,array"`
	Unlinked     bool      `pg:"unlinked"`
	EntityNames  []string  `pg:"entity_names,array"`
	Series       []string  `pg:"series,array"`
}

func (r *censusRow) input() bookvariants.Input {
	in := bookvariants.Input{
		BookID: r.ID, Lang: r.Lang, Title: r.Title, EntityIDs: r.EntityIDs, Unlinked: r.Unlinked,
		EntityNames: r.EntityNames, Series: map[int64]int64{}, DocumentID: r.DocumentID,
		DocVersion: r.DocVersion, DocDate: r.DocDate, RegisterDate: r.RegisterDate,
		Cover: r.Cover, Annotation: r.Annotation, Publisher: r.Publisher, Year: r.Year,
	}
	for _, s := range r.Series {
		series, no, ok := strings.Cut(s, ":")
		if !ok {
			continue
		}
		id, err1 := strconv.ParseInt(series, 10, 64)
		n, err2 := strconv.ParseInt(no, 10, 64)
		if err1 == nil && err2 == nil && n > 0 {
			in.Series[id] = n
		}
	}
	return in
}

// run reads the catalog chunk by chunk, groups it and writes the report.
func run(ctx context.Context, db pg.DBI, chunk int64, out, progress io.Writer) error {
	started := time.Now()
	var maxID int64
	if _, err := db.QueryOneContext(ctx, pg.Scan(&maxID), `SELECT coalesce(max(id), 0) FROM opds_catalog_book`); err != nil {
		return fmt.Errorf("reading the last book ID: %w", err)
	}
	t := newTally()
	for from := int64(0); from <= maxID; from += chunk {
		var rows []censusRow
		if _, err := db.QueryContext(ctx, &rows, censusBooksSQL, from, from+chunk); err != nil {
			return fmt.Errorf("reading books %d..%d: %w", from, from+chunk, err)
		}
		for i := range rows {
			in := rows[i].input()
			t.add(&in)
		}
		fmt.Fprintf(progress, "read to %d: %d books\n", from+chunk, t.books)
	}
	report := t.report()
	fmt.Fprintf(progress, "%d books in %s\n", report.Books, time.Since(started).Round(time.Millisecond))
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

// Report is what the census prints.
type Report struct {
	Books             int            `json:"books"`
	Cards             int            `json:"cards"`
	OwnCards          int            `json:"own_cards"`
	OwnCardReasons    map[string]int `json:"own_card_reasons"`
	VariantCards      int            `json:"variant_cards"`
	BooksInVariants   int            `json:"books_in_variant_cards"`
	RevisionCards     int            `json:"revision_cards"`
	Sizes             map[string]int `json:"sizes"`
	MaxSize           int            `json:"max_size"`
	SeriesSplitBases  int            `json:"series_split_bases"`
	SeriesSplitBooks  int            `json:"series_split_books"`
	UnnumberedLoners  int            `json:"unnumbered_loners"`
	KeptTrailingBrack int            `json:"kept_trailing_brackets"`
	MarkedCards       map[string]int `json:"marked_cards"`
}

// tally gathers the books by base key.
type tally struct {
	books   int
	bases   map[string][]bookvariants.Input
	own     map[string]int
	kept    int
	ownSeen int
}

func newTally() *tally {
	return &tally{bases: map[string][]bookvariants.Input{}, own: map[string]int{}}
}

func (t *tally) add(in *bookvariants.Input) {
	t.books++
	key, tags, groupable := bookvariants.NormalizeTitle(in.Title)
	if strings.HasSuffix(strings.TrimSpace(in.Title), "]") && len(tags) == 0 && key != "" {
		t.kept++
	}
	base := bookvariants.BaseKey(in)
	if base != "" {
		t.bases[base] = append(t.bases[base], *in)
		return
	}
	t.ownSeen++
	switch {
	case !groupable:
		t.own["junk title"]++
	case len(in.EntityIDs) == 0:
		t.own["no author entity"]++
	case in.Unlinked:
		t.own["partly unlinked"]++
	default:
		t.own["placeholder author"]++
	}
}

func (t *tally) report() Report {
	r := Report{
		Books: t.books, OwnCards: t.ownSeen, OwnCardReasons: t.own, Sizes: map[string]int{},
		KeptTrailingBrack: t.kept, MarkedCards: map[string]int{},
	}
	r.Cards = t.ownSeen
	r.Sizes["1"] = t.ownSeen
	for base, books := range t.bases {
		groups := bookvariants.Build(base, books)
		if len(groups) > 1 || groups[0].Key != bookvariants.GroupKey(base, "") {
			r.SeriesSplitBases++
			r.SeriesSplitBooks += len(books)
		}
		for i := range groups {
			g := &groups[i]
			n := len(g.Members)
			r.Cards++
			r.Sizes[sizeBucket(n)]++
			r.MaxSize = max(r.MaxSize, n)
			if g.Key == bookvariants.GroupKey(base, "own:"+strconv.FormatInt(g.Members[0].BookID, 10)) {
				r.UnnumberedLoners++
			}
			if n > 1 {
				r.VariantCards++
				r.BooksInVariants += n
			}
			if g.Kind == bookvariants.KindRevisions {
				r.RevisionCards++
			}
			if g.Mark != bookvariants.MarkNone {
				r.MarkedCards[string(g.Mark)]++
			}
		}
	}
	return r
}

// sizeBuckets are the card sizes the plan's table counts: up to 3 one by
// one, then 4-5, 6-10 and more.
var sizeBuckets = []struct {
	upTo  int
	label string
}{{1, "1"}, {2, "2"}, {3, "3"}, {5, "4-5"}, {10, "6-10"}}

func sizeBucket(n int) string {
	for _, b := range sizeBuckets {
		if n <= b.upTo {
			return b.label
		}
	}
	return ">10"
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
