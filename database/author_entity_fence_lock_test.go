package database

import (
	"context"
	"testing"
	"time"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A book deletion and a fenced entity build (E4b-2 review P2): the build
// holds the author layer shared and then takes the marks SHARE at its fence;
// a deletion takes the layer exclusively before it deletes any row that
// marks, so it waits for the build instead of holding a mark the fence
// waits for. Both commit; neither is a deadlock victim.
func TestABookDeletionWaitsForAFencedBuild(t *testing.T) {
	scratch := jobsDB(t)
	ctx := context.Background()
	setup, err := scratch.Begin()
	require.NoError(t, err)
	next := authorSchemaIDBase
	f := &authorSchemaFixture{t: t, tx: setup, next: &next}
	book := f.book()
	f.exec(`INSERT INTO opds_catalog_series (id, ser, lang_code) VALUES (?, 'series', 1)`, book)
	f.exec(`INSERT INTO opds_catalog_bseries (ser_no, book_id, ser_id) VALUES (0, ?, ?)`, book, book)
	require.NoError(t, setup.Commit())

	// The build's apply: the layer shared (LockAuthorEntityBuild) after its
	// plan's snapshot.
	build, err := scratch.Begin()
	require.NoError(t, err)
	defer func() { _ = build.Rollback() }()
	var snapshot string
	_, err = build.QueryOne(pg.Scan(&snapshot), `SELECT pg_current_snapshot()::text`)
	require.NoError(t, err)
	require.NoError(t, lockAuthorLayerShared(ctx, build))

	deleted := make(chan error, 1)
	go func() {
		deleted <- scratch.RunInTransaction(ctx, func(tx *pg.Tx) error {
			_, delErr := deleteBooksWithLayer(tx, []int64{book})
			return delErr
		})
	}()
	waiting := func() bool {
		var n int
		_, qErr := scratch.QueryOne(pg.Scan(&n), `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`)
		require.NoError(t, qErr)
		return n > 0
	}
	deadline := time.Now().Add(10 * time.Second)
	for !waiting() {
		require.True(t, time.Now().Before(deadline), "the deletion did not wait for the layer")
		time.Sleep(20 * time.Millisecond)
	}

	// The fence: SHARE on the marks, then the check.
	_, err = build.Exec(`SET LOCAL lock_timeout = '5s'`)
	require.NoError(t, err)
	err = FenceAuthorEntityBuild(ctx, build, snapshot)
	require.NoError(t, err, "the fence waited for a deletion that waits for the build")
	require.NoError(t, build.Commit())
	require.NoError(t, <-deleted)
	var books int
	_, err = scratch.QueryOne(pg.Scan(&books), `SELECT count(*) FROM opds_catalog_book WHERE id = ?`, book)
	require.NoError(t, err)
	assert.Zero(t, books)
}
