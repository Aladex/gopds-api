// Package wikidata extracts the reference data of the author entity build's
// Wikidata evidence (design E, phase E4w): the people who write — writers,
// translators, screenwriters and every occupation under them — with their
// labels, aliases, pseudonyms, names in their native language, birth and
// death years and notable works. Wikidata is CC0.
//
// An extract is a fixed list of chunks, one query each, read from a Source:
// QLever's public SPARQL endpoint by default, or a directory of the same
// chunks (WriteDir's output; a dump converted to it later). The extract's ID
// is derived from the chunks' bytes, so the same data is the same extract
// wherever and whenever it was fetched.
package wikidata

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultEndpoint is QLever's public Wikidata SPARQL endpoint: current
// Wikidata, no 60-second limit, large results in one response.
const DefaultEndpoint = "https://qlever.dev/api/wikidata"

// UserAgent names the project to the endpoint, as its usage policy asks.
const UserAgent = "gopds-api-wikidata/1 (https://github.com/Aladex/gopds-api)"

// The kinds of chunk.
const (
	KindPeople    = "people"
	KindLabel     = "label"
	KindAlias     = "alias"
	KindPseudonym = "pseudonym"
	KindNative    = "native"
	KindWorks     = "works"
)

// LangRussian is the language of the label a page shows first,
// LangEnglish the one it shows otherwise.
const (
	LangRussian = "ru"
	LangEnglish = "en"
)

// Languages are the languages of the labels and aliases read: the catalog's
// languages, and "mul" for the labels Wikidata gives every language.
var Languages = []string{LangRussian, LangEnglish, "uk", "be", "bg", "de", "fr", "es", "it", "pl", "cs", "mul"}

const prefixes = `PREFIX wd: <http://www.wikidata.org/entity/> PREFIX wdt: <http://www.wikidata.org/prop/direct/> ` +
	`PREFIX rdfs: <http://www.w3.org/2000/01/rdf-schema#> PREFIX skos: <http://www.w3.org/2004/02/skos/core#> `

// writers are the people: humans whose occupation is in the subclass tree of
// writer, translator or screenwriter.
const writers = `{ SELECT DISTINCT ?p WHERE { VALUES ?root { wd:Q36180 wd:Q333634 wd:Q28389 } ` +
	`?occ wdt:P279* ?root . ?p wdt:P106 ?occ . ?p wdt:P31 wd:Q5 . } }`

// Chunk is one query of an extract.
type Chunk struct {
	Name  string
	Kind  string
	Lang  string
	Query string
}

// Chunks are an extract's queries, in the order they are read.
func Chunks() []Chunk {
	out := []Chunk{{Name: KindPeople, Kind: KindPeople, Query: prefixes +
		`SELECT ?p (MIN(YEAR(?b)) AS ?born) (MIN(YEAR(?d)) AS ?died) WHERE { ` + writers +
		` OPTIONAL { ?p wdt:P569 ?b } OPTIONAL { ?p wdt:P570 ?d } } GROUP BY ?p`}}
	for _, l := range Languages {
		out = append(out,
			Chunk{Name: "label_" + l, Kind: KindLabel, Lang: l, Query: prefixes +
				`SELECT ?p ?name WHERE { ` + writers + ` ?p rdfs:label ?name . FILTER(LANG(?name) = '` + l + `') }`},
			Chunk{Name: "alias_" + l, Kind: KindAlias, Lang: l, Query: prefixes +
				`SELECT ?p ?name WHERE { ` + writers + ` ?p skos:altLabel ?name . FILTER(LANG(?name) = '` + l + `') }`})
	}
	return append(out,
		Chunk{Name: "pseudonym", Kind: KindPseudonym, Query: prefixes +
			`SELECT ?p ?name WHERE { ` + writers + ` ?p wdt:P742 ?name }`},
		Chunk{Name: "native", Kind: KindNative, Query: prefixes +
			`SELECT ?p ?name WHERE { ` + writers + ` ?p wdt:P1559 ?name }`},
		Chunk{Name: KindWorks, Kind: KindWorks, Query: prefixes +
			`SELECT ?p ?w ?title WHERE { ` + writers + ` ?p wdt:P800 ?w . ?w rdfs:label ?title . ` +
			`FILTER(LANG(?title) IN ('ru','en')) }`})
}

// Source reads an extract's chunks.
type Source interface {
	// Open returns the chunk's SPARQL TSV.
	Open(ctx context.Context, c Chunk) (io.ReadCloser, error)
	// Name is where the chunks come from, for the manifest.
	Name() string
}

// QLever reads chunks from a SPARQL endpoint, one request per chunk.
type QLever struct {
	Endpoint string
	Client   *http.Client
}

// Name is the endpoint.
func (q *QLever) Name() string { return q.Endpoint }

// Open posts the chunk's query and returns the response body.
func (q *QLever) Open(ctx context.Context, c Chunk) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.Endpoint,
		strings.NewReader(url.Values{"query": {c.Query}}.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/tab-separated-values")
	req.Header.Set("User-Agent", UserAgent)
	resp, err := q.Client.Do(req) // #nosec G704 -- the operator's configured endpoint
	if err != nil {
		return nil, fmt.Errorf("fetching chunk %s: %w", c.Name, err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("fetching chunk %s: %s", c.Name, resp.Status)
	}
	return resp.Body, nil
}

// Dir reads chunks from <Path>/<chunk name>.tsv.
type Dir struct{ Path string }

// Name is the directory.
func (d *Dir) Name() string { return "file://" + d.Path }

// Open opens the chunk's file.
func (d *Dir) Open(_ context.Context, c Chunk) (io.ReadCloser, error) {
	return os.Open(filepath.Join(d.Path, c.Name+".tsv")) // #nosec G304 -- the operator's extract directory
}

// Row is one parsed row of a chunk: the item, and per kind its years, its
// name with the name's language, or its work with the work's title.
type Row struct {
	QID        string
	Born, Died int
	Name, Lang string
	Work       string
}

// Parsed is what Parse read of a chunk.
type Parsed struct {
	Rows, Skipped int
	SHA256        string
}

var (
	iriPattern     = regexp.MustCompile(`^<http://www\.wikidata\.org/entity/(Q\d+)>$`)
	literalPattern = regexp.MustCompile(`^"(.*)"(?:@([A-Za-z-]+)|\^\^<[^>]*>)?$`)
)

// maxLine bounds one TSV line: a name is short, a line far longer is not one;
// lineBuffer is the scanner's first buffer.
const (
	maxLine    = 1 << 20
	lineBuffer = 64 << 10
)

// The cells of a row: the item, then a name or the years, or the work and
// its title.
const (
	cellItem = iota
	cellName
	cellTitle
	cellsPeople = cellName
	cellsName   = cellName + 1
	cellsWork   = cellTitle + 1
)

// Parse reads a chunk's SPARQL TSV and hands every row to fn, hashing the
// bytes it read. A line that is not a row of the chunk is skipped and
// counted: one bad line must not stop the extract.
func Parse(c Chunk, r io.Reader, fn func(Row) error) (Parsed, error) {
	h := sha256.New()
	sc := bufio.NewScanner(io.TeeReader(r, h))
	sc.Buffer(make([]byte, lineBuffer), maxLine)
	var res Parsed
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			first = false
			continue
		}
		row, ok := parseRow(c, strings.Split(line, "\t"))
		if !ok {
			res.Skipped++
			continue
		}
		if err := fn(row); err != nil {
			return res, err
		}
		res.Rows++
	}
	if err := sc.Err(); err != nil {
		return res, fmt.Errorf("reading chunk %s: %w", c.Name, err)
	}
	// The scanner stops at the last line; the hash covers every byte.
	if _, err := io.Copy(io.Discard, io.TeeReader(r, h)); err != nil {
		return res, err
	}
	res.SHA256 = hex.EncodeToString(h.Sum(nil))
	return res, nil
}

func parseRow(c Chunk, cells []string) (Row, bool) {
	m := iriPattern.FindStringSubmatch(cells[cellItem])
	if m == nil {
		return Row{}, false
	}
	row := Row{QID: m[1]}
	switch c.Kind {
	case KindPeople:
		// The born and died cells, either empty.
		if len(cells) > cellsPeople {
			row.Born = year(cells[cellName])
		}
		if len(cells) > cellsPeople+1 {
			row.Died = year(cells[cellTitle])
		}
		return row, true
	case KindWorks:
		if len(cells) < cellsWork {
			return Row{}, false
		}
		w := iriPattern.FindStringSubmatch(cells[cellName])
		if w == nil {
			return Row{}, false
		}
		row.Work = w[1]
		row.Name, row.Lang = literal(cells[cellTitle])
	default:
		if len(cells) < cellsName {
			return Row{}, false
		}
		row.Name, row.Lang = literal(cells[cellName])
	}
	if strings.TrimSpace(row.Name) == "" {
		return Row{}, false
	}
	return row, true
}

// literal is a SPARQL literal's text, unescaped, and its language.
func literal(cell string) (text, lang string) {
	m := literalPattern.FindStringSubmatch(cell)
	if m == nil {
		return cell, ""
	}
	var b strings.Builder
	escaped := false
	for _, ch := range m[1] {
		switch {
		case escaped:
			switch ch {
			case 't':
				b.WriteByte(' ')
			case 'n', 'r':
				b.WriteByte(' ')
			default:
				b.WriteRune(ch)
			}
			escaped = false
		case ch == '\\':
			escaped = true
		default:
			b.WriteRune(ch)
		}
	}
	return b.String(), m[2]
}

func year(cell string) int {
	text, _ := literal(cell)
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return 0
	}
	return n
}

// Manifest records an extract: where and when it was read and, per chunk,
// its query, rows and the sha256 of its bytes.
type Manifest struct {
	Source      string          `json:"source"`
	Endpoint    string          `json:"endpoint"`
	ExtractedAt time.Time       `json:"extracted_at"`
	Chunks      []ChunkManifest `json:"chunks"`
}

// ChunkManifest is one chunk of a manifest.
type ChunkManifest struct {
	Name    string `json:"name"`
	Query   string `json:"query"`
	Rows    int    `json:"rows"`
	Skipped int    `json:"skipped"`
	SHA256  string `json:"sha256"`
}

// contentIDLength is how many hex digits of the content hash the ID keeps.
const contentIDLength = 16

// ContentID is the extract's ID: a hash of its chunks' names and bytes,
// never of the time or the place it was read.
func (m *Manifest) ContentID() string {
	h := sha256.New()
	for _, c := range m.Chunks {
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00", c.Name, c.SHA256)
	}
	return "wd-" + hex.EncodeToString(h.Sum(nil))[:contentIDLength]
}

// ErrIncomplete is an extract some chunk of which was not read.
var ErrIncomplete = errors.New("wikidata: the extract is incomplete")

// WriteDir reads every chunk from src into dir, one <name>.tsv each, and
// returns the manifest (also written to dir/MANIFEST.json by the caller's
// choice). A failed chunk stops it: an extract is whole or nothing.
func WriteDir(ctx context.Context, src Source, dir string, now func() time.Time) (*Manifest, error) {
	m := &Manifest{Source: src.Name(), Endpoint: src.Name(), ExtractedAt: now().UTC()}
	for _, c := range Chunks() {
		cm, err := writeChunk(ctx, src, dir, c)
		if err != nil {
			return m, err
		}
		m.Chunks = append(m.Chunks, cm)
	}
	return m, nil
}

func writeChunk(ctx context.Context, src Source, dir string, c Chunk) (ChunkManifest, error) {
	body, err := src.Open(ctx, c)
	if err != nil {
		return ChunkManifest{}, err
	}
	defer body.Close()
	f, err := os.Create(filepath.Join(dir, c.Name+".tsv")) // #nosec G304 -- the operator's output directory
	if err != nil {
		return ChunkManifest{}, err
	}
	res, err := Parse(c, io.TeeReader(body, f), func(Row) error { return nil })
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return ChunkManifest{}, err
	}
	return ChunkManifest{Name: c.Name, Query: c.Query, Rows: res.Rows, Skipped: res.Skipped, SHA256: res.SHA256}, nil
}
