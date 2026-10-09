package api

import (
	"testing"
	"time"

	"gopds-api/internal/scanfixture"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The scanner the admin scan endpoints build writes the author metadata
// source of every book it ingests: the dual write is part of the production
// wiring, not something a caller has to remember to switch on.
func TestProductionScanWiringWritesAuthorMetadata(t *testing.T) {
	// No provider key: genre titles stay local and nothing reaches the network.
	t.Setenv("OPENAI_API_KEY", "")
	db := scanfixture.ScratchDB(t)

	ids := scanfixture.Ingest(t, t.TempDir(), time.Now(), newBookScanService().ProcessBook)
	require.Len(t, ids, 3)

	for entry, id := range ids {
		var live int
		_, err := db.QueryOne(pg.Scan(&live), `SELECT count(*) FROM book_metadata_snapshot
			WHERE book_id = ? AND origin = 'live' AND is_current`, id)
		require.NoError(t, err)
		assert.Equal(t, 1, live, entry)
	}
}
