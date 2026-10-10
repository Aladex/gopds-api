package services

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The read-model worker drains the mark queue first, a batch at a time, and
// walks the catalog between drains; with no mark and no walk due it rests.

func TestAuthorDisplayModelLoopDrainsMarksThenWalksThenRests(t *testing.T) {
	s := localWorkerDB(t)
	f := &workerFixture{t: t, db: s}
	f.persistBook(author(tolstoy(t)))
	f.persistBook(author(tolstoy(t)))
	ctx := context.Background()
	marks := func() int { return f.count(`SELECT count(*) FROM book_author_display_dirty`) }
	rows := func() int { return f.count(`SELECT count(*) FROM book_author_display`) }
	require.Equal(t, 2, marks(), "each new snapshot marked its book")

	loop := NewAuthorDisplayModelLoop(s, AuthorDisplayModelConfig{Batch: 1, Every: 24 * time.Hour, Poll: time.Hour})
	worked, err := loop.RunOnce(ctx)
	require.NoError(t, err)
	assert.True(t, worked)
	assert.Equal(t, 1, marks(), "one batch of one mark")
	assert.Equal(t, 1, rows())

	// The rest of the queue, then the first walk, a book a step, to its end.
	for i := 0; i < 10; i++ {
		if worked, err = loop.RunOnce(ctx); err != nil || !worked {
			break
		}
	}
	require.NoError(t, err)
	assert.False(t, worked, "nothing left to do")
	assert.Zero(t, marks())
	assert.Equal(t, 2, rows())
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM book_author_display_reconcile
		WHERE finished_at IS NOT NULL AND books_read = 2 AND books_differed = 0`),
		"the walk read both books and found the marks had kept them current")

	// A new mark is work again — also one that does not fill a batch, with
	// no walk due.
	f.exec(`INSERT INTO book_author_display_dirty (book_id) SELECT min(id) FROM opds_catalog_book`)
	roomy := NewAuthorDisplayModelLoop(s, AuthorDisplayModelConfig{Batch: 10, Every: 24 * time.Hour, Poll: time.Hour})
	worked, err = roomy.RunOnce(ctx)
	require.NoError(t, err)
	assert.True(t, worked)
	assert.Zero(t, marks())
}

func TestAuthorDisplayModelLoopRunsUnderTheRunner(t *testing.T) {
	s := localWorkerDB(t)
	f := &workerFixture{t: t, db: s}
	f.persistBook(author(tolstoy(t)))

	r := NewAuthorMetadataRunner(NewAuthorDisplayModelLoop(s,
		AuthorDisplayModelConfig{Batch: 100, Every: 24 * time.Hour, Poll: 5 * time.Millisecond}))
	require.NoError(t, r.Start(context.Background(), ready))
	runnerEventually(t, func() bool {
		return f.count(`SELECT count(*) FROM book_author_display`) == 1 &&
			f.count(`SELECT count(*) FROM book_author_display_dirty`) == 0
	})

	// A mark written while it rests is picked up within a poll interval.
	f.persistBook(author(tolstoy(t)))
	runnerEventually(t, func() bool { return f.count(`SELECT count(*) FROM book_author_display`) == 2 })
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, r.Shutdown(shutdown))
}

func TestAuthorDisplayModelConfigDefaults(t *testing.T) {
	c := AuthorDisplayModelConfig{}.withDefaults()
	assert.Equal(t, AuthorDisplayModelConfig{Batch: 1000, Every: 24 * time.Hour, Poll: 2 * time.Second}, c)
}
