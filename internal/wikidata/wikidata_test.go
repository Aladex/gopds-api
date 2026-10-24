package wikidata

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A chunk's SPARQL TSV becomes rows: the item's QID from its IRI, literals
// unescaped with their language, years as numbers; a row that is not one is
// counted and skipped, never fatal — the data is wild.
func TestParseReadsSPARQLRows(t *testing.T) {
	label := Chunk{Name: "label_ru", Kind: KindLabel, Lang: "ru"}
	tsv := "?p\t?name\n" +
		"<http://www.wikidata.org/entity/Q39829>\t\"Стивен Кинг\"@ru\n" +
		"<http://www.wikidata.org/entity/Q1>\t\"Md \\\"Q\\\" \\\\ BD\"@ru\n" +
		"not an iri\t\"x\"@ru\n" +
		"<http://www.wikidata.org/entity/Q2>\t\"\"@ru\n"
	var rows []Row
	res, err := Parse(label, strings.NewReader(tsv), func(r Row) error { rows = append(rows, r); return nil })
	require.NoError(t, err)
	assert.Equal(t, []Row{
		{QID: "Q39829", Name: "Стивен Кинг", Lang: "ru"},
		{QID: "Q1", Name: `Md "Q" \ BD`, Lang: "ru"},
	}, rows)
	assert.Equal(t, 2, res.Rows)
	assert.Equal(t, 2, res.Skipped)
	assert.Len(t, res.SHA256, 64)

	people := Chunk{Name: "people", Kind: KindPeople}
	rows = nil
	_, err = Parse(people, strings.NewReader("?p\t?born\t?died\n"+
		"<http://www.wikidata.org/entity/Q39829>\t1947\t\n"+
		"<http://www.wikidata.org/entity/Q7243>\t\"1828\"^^<http://www.w3.org/2001/XMLSchema#int>\t1910\n"),
		func(r Row) error { rows = append(rows, r); return nil })
	require.NoError(t, err)
	assert.Equal(t, []Row{{QID: "Q39829", Born: 1947}, {QID: "Q7243", Born: 1828, Died: 1910}}, rows)

	works := Chunk{Name: "works", Kind: KindWorks}
	rows = nil
	_, err = Parse(works, strings.NewReader("?p\t?w\t?title\n"+
		"<http://www.wikidata.org/entity/Q39829>\t<http://www.wikidata.org/entity/Q277260>\t\"Сияние\"@ru\n"),
		func(r Row) error { rows = append(rows, r); return nil })
	require.NoError(t, err)
	assert.Equal(t, []Row{{QID: "Q39829", Work: "Q277260", Name: "Сияние", Lang: "ru"}}, rows)
}

// The extract's ID is derived from its content alone: the same chunks with
// the same bytes are the same extract whenever and wherever they were
// fetched; any changed byte is another.
func TestTheExtractIDIsItsContent(t *testing.T) {
	m := Manifest{Endpoint: DefaultEndpoint, ExtractedAt: time.Now(), Chunks: []ChunkManifest{
		{Name: "people", Rows: 2, SHA256: strings.Repeat("a", 64)},
		{Name: "label_ru", Rows: 2, SHA256: strings.Repeat("b", 64)},
	}}
	later := m
	later.ExtractedAt = time.Now().Add(time.Hour)
	later.Endpoint = "file:///dump"
	assert.Equal(t, m.ContentID(), later.ContentID())
	assert.Regexp(t, `^wd-[0-9a-f]{16}$`, m.ContentID())
	changed := m
	changed.Chunks = append([]ChunkManifest{}, m.Chunks...)
	changed.Chunks[1].SHA256 = strings.Repeat("c", 64)
	assert.NotEqual(t, m.ContentID(), changed.ContentID())
}

// QLever is asked one chunk at a time, by POST, with the chunk's query and a
// User-Agent naming the project; a failed chunk is an error, not a short
// extract. A directory written by WriteDir reads back byte for byte.
func TestQLeverAndDirSources(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		got = append(got, r.Form.Get("query"))
		assert.Contains(t, r.UserAgent(), "gopds-api")
		assert.Equal(t, "text/tab-separated-values", r.Header.Get("Accept"))
		if strings.Contains(r.Form.Get("query"), "P742") {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "?p\t?name\n<http://www.wikidata.org/entity/Q1>\t\"A\"@ru\n")
	}))
	defer srv.Close()
	src := &QLever{Endpoint: srv.URL, Client: srv.Client()}
	chunks := Chunks()
	body, err := src.Open(context.Background(), chunks[1])
	require.NoError(t, err)
	raw, err := io.ReadAll(body)
	require.NoError(t, err)
	require.NoError(t, body.Close())
	assert.Equal(t, chunks[1].Query, got[0])

	var pseudonyms Chunk
	for _, c := range chunks {
		if c.Kind == KindPseudonym {
			pseudonyms = c
		}
	}
	_, err = src.Open(context.Background(), pseudonyms)
	assert.ErrorContains(t, err, "503")

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, chunks[1].Name+".tsv"), raw, 0o600))
	body, err = (&Dir{Path: dir}).Open(context.Background(), chunks[1])
	require.NoError(t, err)
	again, err := io.ReadAll(body)
	require.NoError(t, err)
	require.NoError(t, body.Close())
	assert.Equal(t, raw, again)
}

// The chunks cover every name kind in the languages the catalog needs, the
// people first, each query naming the writers' occupation trees.
func TestTheChunksCoverTheNamesTheBuildReads(t *testing.T) {
	chunks := Chunks()
	require.NotEmpty(t, chunks)
	assert.Equal(t, KindPeople, chunks[0].Kind)
	kinds := map[string]int{}
	for _, c := range chunks {
		kinds[c.Kind]++
		assert.Contains(t, c.Query, "wd:Q36180", c.Name)
		assert.Contains(t, c.Query, "wd:Q333634", c.Name)
		assert.Contains(t, c.Query, "wd:Q28389", c.Name)
	}
	assert.Equal(t, len(Languages), kinds[KindLabel])
	assert.Equal(t, len(Languages), kinds[KindAlias])
	assert.Equal(t, 1, kinds[KindPseudonym])
	assert.Equal(t, 1, kinds[KindNative])
	assert.Equal(t, 1, kinds[KindWorks])
}
