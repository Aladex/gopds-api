package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopds-api/internal/authorentity"
	"gopds-api/internal/wikidata"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fetch writes every chunk as the endpoint sent it and a manifest whose
// content ID is the chunks' — what an import of the directory publishes.
func TestFetchWritesTheChunksAndTheirManifest(t *testing.T) {
	var agents []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agents = append(agents, r.UserAgent())
		_, _ = io.WriteString(w, "?p\t?name\n<http://www.wikidata.org/entity/Q1>\t\"Name\"@en\n")
	}))
	defer srv.Close()
	dir := t.TempDir()
	require.NoError(t, fetch(context.Background(), []string{"-out", dir, "-endpoint", srv.URL}))

	raw, err := os.ReadFile(filepath.Join(dir, "MANIFEST.json"))
	require.NoError(t, err)
	var m struct {
		wikidata.Manifest
		ContentID string `json:"content_id"`
	}
	require.NoError(t, json.Unmarshal(raw, &m))
	assert.Equal(t, srv.URL, m.Endpoint)
	assert.Len(t, m.Chunks, len(wikidata.Chunks()))
	assert.Equal(t, m.Manifest.ContentID(), m.ContentID)
	assert.Equal(t, wikidata.Chunks()[0].Query, m.Chunks[0].Query, "the queries are recorded")
	assert.Len(t, agents, len(wikidata.Chunks()), "one request per chunk")
	assert.Equal(t, wikidata.UserAgent, agents[0])

	// The directory reads back as the same extract.
	again, err := wikidata.WriteDir(context.Background(), &wikidata.Dir{Path: dir}, t.TempDir(), m.ExtractedAt.Local)
	require.NoError(t, err)
	assert.Equal(t, m.ContentID, again.ContentID())
}

func TestDecisionsListBothSidesOfEveryMerge(t *testing.T) {
	r := &authorentity.Result{ResultID: 1, Kind: "person", DisplayName: "Стивен Кинг"}
	en := &authorentity.Result{ResultID: 2, Kind: "person", DisplayName: "Stephen King"}
	in := &authorentity.Input{
		Credits: []authorentity.Credit{{ID: 10, BookID: 1, Result: r}, {ID: 11, BookID: 2, Result: r},
			{ID: 12, BookID: 3, Result: en}},
		Books: map[int64]authorentity.Book{1: {Title: "Оно"}, 2: {Title: "Мизери"}, 3: {Title: "It"}},
		Reference: &authorentity.Reference{Items: map[string]*authorentity.Item{
			"Q39829": {Label: "Стивен Кинг"}}},
	}
	var out bytes.Buffer
	require.NoError(t, writeDecisions(&out, in, []authorentity.WikidataDecision{
		{Kind: authorentity.WikidataCrossScript, QID: "Q39829", Member: []int64{12}, Anchor: []int64{10, 11}}}))
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, "cross_script\tQ39829\tСтивен Кинг\t12\tStephen King\t1\tIt\tСтивен Кинг\t2\tОно | Мизери",
		lines[1])
}
