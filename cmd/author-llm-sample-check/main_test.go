package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"gopds-api/internal/scanfixture"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The command reads the catalog in a read-only transaction and prints the
// sampling report as JSON: counts and fingerprints, no name of anyone.
func TestSampleCheckReportsTheCatalog(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	scanner := services.NewBookScanService(t.TempDir(), t.TempDir(),
		services.NewLanguageDetector(false, 5*time.Second), false, nil)
	scanfixture.Ingest(t, t.TempDir(), time.Now(), scanner.ProcessBook)
	var before int
	_, err := db.QueryOne(pg.Scan(&before), `SELECT count(*) FROM author_llm_eval_set`)
	require.NoError(t, err)

	var out, progress bytes.Buffer
	require.NoError(t, run(context.Background(), db, &out, &progress))
	var report services.AuthorLLMSamplingReport
	require.NoError(t, json.Unmarshal(out.Bytes(), &report))
	assert.NotNil(t, report.Strata)
	assert.Contains(t, progress.String(), "fingerprints in")
	for _, name := range []string{"толстой", "Толстой", "brien", "пушкин", "Автор"} {
		assert.NotContains(t, out.String()+progress.String(), name, "the report names nobody")
	}
	var after int
	_, err = db.QueryOne(pg.Scan(&after), `SELECT count(*) FROM author_llm_eval_set`)
	require.NoError(t, err)
	assert.Equal(t, before, after, "the check froze something")
}
