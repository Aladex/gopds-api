package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/scanfixture"
	"gopds-api/services"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The command reads the catalog to its end and prints the report as JSON:
// counts and book IDs, no name of anyone.
func TestReportCoversTheWholeCatalog(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	scanner := services.NewBookScanService(t.TempDir(), t.TempDir(),
		services.NewLanguageDetector(false, 5*time.Second), false, nil)
	books := scanfixture.Ingest(t, t.TempDir(), time.Now(), scanner.ProcessBook)

	var out, progress bytes.Buffer
	require.NoError(t, run(context.Background(), db, 0, &out, &progress))

	var report database.AuthorDisplayReport
	require.NoError(t, json.Unmarshal(out.Bytes(), &report))
	assert.Equal(t, len(scanfixture.Entries()), report.Books)
	assert.Nil(t, report.NextAfterID)
	assert.Equal(t, []int64{books[scanfixture.EntryMultiAuthor], books[scanfixture.EntryLatin]},
		report.Categories[database.ReportDisplayLayer].Samples)
	assert.Equal(t, []int64{books[scanfixture.EntryMultiAuthor]},
		report.Categories[database.ReportAuthorCountDiffers].Samples, "the legacy scan glued two authors into one")
	assert.Equal(t, []int64{books[scanfixture.EntryNoAuthor]},
		report.Categories[database.ReportDisplayLegacyNoCredits].Samples)

	for _, name := range []string{"толстой", "Толстой", "brien", "пушкин", "Автор"} {
		assert.NotContains(t, out.String()+progress.String(), name, "the report names nobody")
	}
}
