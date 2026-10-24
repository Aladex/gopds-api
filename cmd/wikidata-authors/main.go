// Command wikidata-authors handles the Wikidata reference of the author
// entity build (design E, phase E4w) outside the server:
//
//	fetch     -out DIR [-endpoint URL]  read every chunk from a SPARQL endpoint into DIR, one <chunk>.tsv each,
//	                                    with DIR/MANIFEST.json: the endpoint, the time, every query, rows, sha256
//	import    -dir DIR                  write DIR's chunks into the database as one extract and publish it — the
//	                                    same path the server's fetch takes; the same data again changes nothing
//	decisions -out FILE                 plan a build over the database and its published extract, writing nothing,
//	                                    and list every Wikidata merge and move as TSV for a hand check
//
// The server fetches from QLever by itself (the «Сканирование» card); this
// command is for a source the pod cannot reach, a directory converted from
// a dump, and the checks.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authorentity"
	"gopds-api/internal/wikidata"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
)

const (
	// defaultTimeout bounds a whole run: the fetch is some 320 MB.
	defaultTimeout = 2 * time.Hour
	// requestTimeout bounds one chunk's request.
	requestTimeout = 10 * time.Minute
	// minArgs is the program name and a mode.
	minArgs = 2
	// exitUsage is the status of a wrong invocation.
	exitUsage = 2
	// dirPerm and filePerm are what the command creates.
	dirPerm  = 0o750
	filePerm = 0o600
)

func main() {
	if len(os.Args) < minArgs {
		usage()
		os.Exit(exitUsage)
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	var err error
	switch os.Args[1] {
	case "fetch":
		err = fetch(ctx, os.Args[2:])
	case "import":
		err = withDB(os.Args[2:], func(fs *flag.FlagSet) func(*pg.DB) error {
			dir := fs.String("dir", "", "directory of the extract's chunks")
			return func(db *pg.DB) error { return importDir(ctx, db, *dir, os.Stdout) }
		})
	case "decisions":
		err = withDB(os.Args[2:], func(fs *flag.FlagSet) func(*pg.DB) error {
			out := fs.String("out", "", "TSV file to write")
			return func(db *pg.DB) error { return decisions(ctx, db, *out, os.Stderr) }
		})
	default:
		usage()
		cancel()
		os.Exit(exitUsage)
	}
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "wikidata-authors: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: wikidata-authors fetch -out DIR [-endpoint URL] | import -dir DIR | decisions -out FILE")
}

// fetch reads every chunk into a directory and writes its manifest.
func fetch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	out := fs.String("out", "", "directory to write the chunks and MANIFEST.json into")
	endpoint := fs.String("endpoint", wikidata.DefaultEndpoint, "SPARQL endpoint")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("fetch: -out is required")
	}
	if err := os.MkdirAll(*out, dirPerm); err != nil {
		return err
	}
	src := &wikidata.QLever{Endpoint: *endpoint, Client: &http.Client{Timeout: requestTimeout}}
	m, err := wikidata.WriteDir(ctx, src, *out, time.Now)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(struct {
		*wikidata.Manifest
		ContentID string `json:"content_id"`
	}{m, m.ContentID()}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*out, "MANIFEST.json"), raw, filePerm); err != nil {
		return err
	}
	fmt.Printf("extract %s: %d chunks in %s\n", m.ContentID(), len(m.Chunks), *out)
	return nil
}

// withDB parses the database flags and a mode's own, connects, and runs it.
func withDB(args []string, mode func(fs *flag.FlagSet) func(*pg.DB) error) error {
	fs := flag.NewFlagSet("db", flag.ExitOnError)
	addr := fs.String("host", envOr("GOPDS_POSTGRES_DBHOST", "127.0.0.1:5432"), "database host:port")
	user := fs.String("user", envOr("GOPDS_POSTGRES_DBUSER", "gopds"), "database user")
	pass := fs.String("password", os.Getenv("GOPDS_POSTGRES_DBPASS"), "database password")
	name := fs.String("database", envOr("GOPDS_POSTGRES_DBNAME", "gopds"), "database name")
	run := mode(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	db := pg.Connect(&pg.Options{Addr: *addr, User: *user, Password: *pass, Database: *name})
	err := run(db)
	if closeErr := db.Close(); err == nil {
		err = closeErr
	}
	return err
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// importDir writes a directory's chunks as one extract and publishes it.
func importDir(ctx context.Context, db *pg.DB, dir string, out io.Writer) error {
	if dir == "" {
		return errors.New("import: -dir is required")
	}
	src := &wikidata.Dir{Path: dir}
	if _, err := database.StartWikidataFetch(ctx, db, 0, src.Name()); err != nil {
		return err
	}
	started := time.Now()
	if _, err := services.NewWikidataFetchLoop(db, src, services.WikidataFetchConfig{}).RunOnce(ctx); err != nil {
		return err
	}
	latest, _, err := database.LatestWikidataExtracts(ctx, db)
	if err != nil {
		return err
	}
	if latest.Status == database.WikidataFailed && latest.LastError != nil {
		return fmt.Errorf("import failed: %s", *latest.LastError)
	}
	content := ""
	if latest.ContentID != nil {
		content = *latest.ContentID
	}
	fmt.Fprintf(out, "extract %d %s: %s, %d people, %d names, %d works, in %s\n", latest.ID, content, latest.Status,
		latest.People, latest.Names, latest.Works, time.Since(started).Round(time.Second))
	return nil
}

// decisions plans a build and lists its Wikidata decisions.
func decisions(ctx context.Context, db *pg.DB, path string, progress io.Writer) error {
	if path == "" {
		return errors.New("decisions: -out is required")
	}
	in, err := database.LoadAuthorEntityInput(ctx, db)
	if err != nil {
		return err
	}
	if in.Old, err = database.LoadAuthorEntityState(ctx, db); err != nil {
		return err
	}
	if in.Reference, err = database.LoadWikidataReference(ctx, db, in); err != nil {
		return err
	}
	if in.Reference == nil {
		return errors.New("decisions: no published extract")
	}
	plan := authorentity.Build(in)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, filePerm) // #nosec G304 -- the operator's output file
	if err != nil {
		return err
	}
	if err = writeDecisions(f, in, plan.WikidataDecisions()); err != nil {
		_ = f.Close()
		return err
	}
	raw, err := json.MarshalIndent(plan.Report.Wikidata, "", "  ")
	if err != nil {
		_ = f.Close()
		return err
	}
	fmt.Fprintf(progress, "%s\n%d decisions, %d rows the build would write\n", raw, len(plan.WikidataDecisions()),
		plan.Changes())
	return f.Close()
}

// maxTitles bounds the titles listed per side.
const maxTitles = 5

// writeDecisions writes one TSV row per decision: the kind, the item, and
// per side its display names, books and first titles.
func writeDecisions(w io.Writer, in *authorentity.Input, ds []authorentity.WikidataDecision) error {
	byID := make(map[int64]*authorentity.Credit, len(in.Credits))
	for i := range in.Credits {
		byID[in.Credits[i].ID] = &in.Credits[i]
	}
	side := func(ids []int64) []string {
		names, titles := []string{}, []string{}
		books := map[int64]bool{}
		for _, id := range ids {
			c := byID[id]
			if !slices.Contains(names, c.DisplayName) {
				names = append(names, c.DisplayName)
			}
			if !books[c.BookID] {
				books[c.BookID] = true
				if len(titles) < maxTitles {
					titles = append(titles, in.Books[c.BookID].Title)
				}
			}
		}
		return []string{strings.Join(names, " | "), strconv.Itoa(len(books)), strings.Join(titles, " | ")}
	}
	if _, err := fmt.Fprintln(w, "kind\tqid\titem\t"+
		"member_credits\tmember\tmember_books\tmember_titles\t"+
		"anchor\tanchor_books\tanchor_titles"); err != nil {
		return err
	}
	for _, d := range ds {
		label := ""
		if it := in.Reference.Items[d.QID]; it != nil {
			label = it.Label
		}
		ids := make([]string, len(d.Member))
		for i, id := range d.Member {
			ids[i] = strconv.FormatInt(id, 10)
		}
		row := append([]string{d.Kind, d.QID, label, strings.Join(ids, ",")}, side(d.Member)...)
		row = append(row, side(d.Anchor)...)
		for i := range row {
			row[i] = strings.NewReplacer("\t", " ", "\n", " ").Replace(row[i])
		}
		if _, err := fmt.Fprintln(w, strings.Join(row, "\t")); err != nil {
			return err
		}
	}
	return nil
}
