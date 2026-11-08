package services

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
	logrustest "github.com/sirupsen/logrus/hooks/test" //nolint:depguard // tests assert on the structured log entries

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
	assert.Equal(t, AuthorDisplayModelConfig{Batch: 1000, Every: 24 * time.Hour, Poll: 2 * time.Second, SummaryBatch: 40,
		SummaryTimeout: 5 * time.Second, VariantBatch: 40, VariantTimeout: 5 * time.Second}, c)
}

// With the read model settled, the loop builds the book cards a batch a pass,
// beside the summary, without counting it as work: the first walk is the
// backfill, and it ends with the event that says how many books it rebuilt.
func TestAuthorDisplayModelLoopBuildsTheCardsWhenIdle(t *testing.T) {
	s := localWorkerDB(t)
	f := &workerFixture{t: t, db: s}
	hook := logrustest.NewLocal(logging.GetLogger())
	t.Cleanup(hook.Reset)
	f.persistBook(author(tolstoy(t)))
	f.persistBook(author(tolstoy(t)))
	f.exec(`UPDATE opds_catalog_book SET approved = true`)
	ctx := context.Background()
	loop := NewAuthorDisplayModelLoop(s, AuthorDisplayModelConfig{Batch: 10, SummaryBatch: 1, VariantBatch: 1,
		Every: 24 * time.Hour, Poll: time.Hour})

	for i := 0; i < 30 && len(eventsNamed(hook, AuthorMetadataEventVariantWalkFinished)) == 0; i++ {
		_, err := loop.RunOnce(ctx)
		require.NoError(t, err)
	}

	require.Len(t, eventsNamed(hook, AuthorMetadataEventVariantWalkFinished), 1, "the first walk did not finish")
	assert.Equal(t, 2, f.count(`SELECT count(*) FROM book_variant_member`), "every listed book has a card row")
	assert.Zero(t, f.count(`SELECT count(*) FROM book_variant_dirty`))
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM book_variant_walk WHERE finished_at IS NOT NULL`))
	ready, err := database.BookVariantsReady(ctx, s)
	require.NoError(t, err)
	assert.True(t, ready)
}

// A book the model refuses is skipped and logged once, by its ID and nothing
// else; the worker carries on with the rest.
func TestAuthorDisplayModelLoopLogsAndSkipsARefusedBook(t *testing.T) {
	hook := logrustest.NewLocal(logging.GetLogger())
	t.Cleanup(hook.Reset)
	s := localWorkerDB(t)
	f := &workerFixture{t: t, db: s}
	f.persistBook(author(tolstoy(t)))
	f.persistBook(author(tolstoy(t)))
	var books []int64
	_, err := s.Query(&books, `SELECT id FROM opds_catalog_book ORDER BY id`)
	require.NoError(t, err)
	require.Len(t, books, 2)
	f.exec(`ALTER TABLE book_author_display ADD CONSTRAINT poisoned_book CHECK (book_id <> ` +
		strconv.FormatInt(books[0], 10) + `) NOT VALID`)

	loop := NewAuthorDisplayModelLoop(s, AuthorDisplayModelConfig{Batch: 10, Every: 24 * time.Hour, Poll: time.Hour})
	worked, err := loop.RunOnce(context.Background())
	require.NoError(t, err)
	assert.True(t, worked)
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM book_author_display WHERE book_id = ?`, books[1]))
	assert.Zero(t, f.count(`SELECT count(*) FROM book_author_display_dirty`))

	skipped := eventsNamed(hook, AuthorMetadataEventDisplayBookSkipped)
	require.Len(t, skipped, 1)
	assert.Equal(t, books[0], skipped[0].Data["book_id"])
	assert.Equal(t, "23514", skipped[0].Data["sqlstate"])
}

// With the read model idle the worker advances the author summary a batch per
// rest: the first walk needs no one to start it and comes as a batch that is
// not reported as work, so the loop rests between batches. An entity the table
// refuses is logged by ID and stops nothing.
func TestAuthorDisplayModelLoopFillsTheSummaryWhenIdleAndAtItsOwnPace(t *testing.T) {
	s := localWorkerDB(t)
	f := &workerFixture{t: t, db: s}
	hook := logrustest.NewLocal(logging.GetLogger())
	bad, good := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, id := range []uuid.UUID{bad, good} {
		f.exec(`INSERT INTO author_entity (id, kind, display_name, sort_name, search_key) VALUES (?, 'person', 'Имя', 'имя', 'имя')`, id)
	}
	f.persistBook(author(tolstoy(t)))
	entities := []string{bad.String(), good.String()}
	f.exec(`UPDATE opds_catalog_book SET approved = true`)
	f.exec(`ALTER TABLE author_entity_summary ADD CONSTRAINT poisoned_entity CHECK (entity_id <> '` + bad.String() + `') NOT VALID`)
	ctx := context.Background()
	loop := NewAuthorDisplayModelLoop(s, AuthorDisplayModelConfig{Batch: 10, SummaryBatch: 1, Every: 24 * time.Hour, Poll: time.Hour})

	// The read model first: marks and its walk are work, the summary waits.
	rested := false
	for i := 0; i < 20 && !rested; i++ {
		worked, err := loop.RunOnce(ctx)
		require.NoError(t, err)
		if rested = !worked; !rested {
			assert.Zero(t, f.count(`SELECT walk_read FROM author_entity_summary_walk`), "the summary waits for the read model")
		}
	}
	require.True(t, rested, "a summary batch must not keep the loop from resting")
	require.Equal(t, 1, f.count(`SELECT count(*) FROM book_author_display WHERE entity_ids IS NULL`))
	f.exec(`UPDATE book_author_display SET source = 'layer',
		credit_id = (SELECT min(id) FROM book_contributor_credit), entity_ids = ?::uuid[]`,
		"{"+strings.Join(entities, ",")+"}")
	f.exec(`DELETE FROM author_entity_summary_dirty`)
	f.exec(`UPDATE author_entity_summary_walk SET walk_cursor = NULL, walk_read = 0, walk_skipped = 0, finished_at = NULL`)

	steps := 0
	for ; steps < 10; steps++ {
		worked, err := loop.RunOnce(ctx)
		require.NoError(t, err)
		assert.False(t, worked, "a summary batch is no reason to skip the rest")
		if len(eventsNamed(hook, AuthorMetadataEventSummaryWalkFinished)) > 0 {
			break
		}
	}
	assert.GreaterOrEqual(t, steps, 1, "a batch per pass")
	assert.Positive(t, f.count(`SELECT count(*) FROM author_entity_summary WHERE entity_id = ?`, good))
	assert.Zero(t, f.count(`SELECT count(*) FROM author_entity_summary WHERE entity_id = ?`, bad))
	skipped := eventsNamed(hook, AuthorMetadataEventSummaryEntitySkipped)
	require.Len(t, skipped, 1)
	assert.Equal(t, bad.String(), skipped[0].Data["entity_id"])
	assert.Equal(t, "23514", skipped[0].Data["sqlstate"])
}

// summaryWorker is a worker with a settled read model on its own database, and
// entities that each list the one book, in ID order: nothing committed for them
// yet, no walk running. The test's transactions are the real ones: a timeout
// here is the server's and its connection really goes.
type summaryWorker struct {
	*workerFixture
	hook *logrustest.Hook
	ids  []uuid.UUID
}

func newSummaryWorker(t *testing.T, entities int) *summaryWorker {
	t.Helper()
	s := localWorkerDB(t)
	w := &summaryWorker{workerFixture: &workerFixture{t: t, db: s}, hook: logrustest.NewLocal(logging.GetLogger())}
	for range entities {
		id := uuid.Must(uuid.NewV7())
		w.ids = append(w.ids, id)
		w.exec(`INSERT INTO author_entity (id, kind, display_name, sort_name, search_key) VALUES (?, 'person', 'Имя', 'имя', 'имя')`, id)
	}
	slices.SortFunc(w.ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	w.persistBook(author(tolstoy(t)))
	w.exec(`UPDATE opds_catalog_book SET approved = true`)
	settle := NewAuthorDisplayModelLoop(s, AuthorDisplayModelConfig{Batch: 10, SummaryBatch: 1, Every: 24 * time.Hour})
	for i := 0; i < 20; i++ {
		worked, err := settle.RunOnce(context.Background())
		require.NoError(t, err)
		if !worked {
			break
		}
	}
	names := make([]string, len(w.ids))
	for i, id := range w.ids {
		names[i] = id.String()
	}
	w.exec(`UPDATE book_author_display SET source = 'layer',
		credit_id = (SELECT min(id) FROM book_contributor_credit), entity_ids = ?::uuid[]`,
		"{"+strings.Join(names, ",")+"}")
	w.exec(`TRUNCATE author_entity_summary, author_entity_summary_dirty`)
	w.exec(`UPDATE author_entity_summary_walk SET walk_cursor = NULL, walk_read = 0, walk_skipped = 0, finished_at = NULL`)
	return w
}

// slowEntity makes writing one entity's rows sleep, counting each attempt in a
// sequence, which is the one thing a rolled-back transaction leaves behind.
func (w *summaryWorker) slowEntity(id uuid.UUID, seconds int) {
	w.t.Helper()
	w.exec(`CREATE SEQUENCE summary_attempts`)
	w.exec(`CREATE FUNCTION slow_summary() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.entity_id = '` + id.String() + `' THEN
				PERFORM nextval('summary_attempts');
				PERFORM pg_sleep(` + strconv.Itoa(seconds) + `);
			END IF;
			RETURN NEW;
		END $$`)
	w.exec(`CREATE TRIGGER slow_summary BEFORE INSERT ON author_entity_summary
		FOR EACH ROW EXECUTE FUNCTION slow_summary()`)
}

func (w *summaryWorker) attempts() int {
	return w.count(`SELECT CASE WHEN is_called THEN last_value ELSE 0 END FROM summary_attempts`)
}

func (w *summaryWorker) built(id uuid.UUID) bool {
	return w.count(`SELECT count(*) FROM author_entity_summary WHERE entity_id = ?`, id) > 0
}

func (w *summaryWorker) walkAt() (cursor string, read, skipped int) {
	w.t.Helper()
	_, err := w.db.QueryOne(pg.Scan(&cursor, &read, &skipped),
		`SELECT coalesce(walk_cursor::text, ''), walk_read, walk_skipped FROM author_entity_summary_walk`)
	require.NoError(w.t, err)
	return cursor, read, skipped
}

func (w *summaryWorker) walkFinished() bool {
	return w.count(`SELECT count(*) FROM author_entity_summary_walk WHERE finished_at IS NOT NULL`) == 1
}

func (w *summaryWorker) timeoutLoop(batch int) *AuthorDisplayModelLoop {
	return NewAuthorDisplayModelLoop(w.db, AuthorDisplayModelConfig{Batch: 10, SummaryBatch: batch,
		SummaryTimeout: 200 * time.Millisecond, Every: 24 * time.Hour})
}

// A statement timeout takes the connection with it, so nothing of the entity's
// transaction can be saved. The good entity before it stays committed, the
// cursor passes it from a fresh transaction, the next one is built, the walk
// ends, and the same entity failing again in the next walk ends it again.
func TestASummaryEntityThatTimesOutIsPassedOncePerWalk(t *testing.T) {
	w := newSummaryWorker(t, 3)
	before, slow, after := w.ids[0], w.ids[1], w.ids[2]
	w.slowEntity(slow, 2)
	loop, ctx := w.timeoutLoop(2), context.Background()

	worked, err := loop.RunOnce(ctx)
	require.NoError(t, err)
	assert.False(t, worked)
	assert.True(t, w.built(before), "what came before the slow entity stays committed")
	assert.False(t, w.built(slow))
	cursor, read, skipped := w.walkAt()
	assert.Equal(t, slow.String(), cursor, "the cursor is past the slow entity")
	assert.Equal(t, 2, read)
	assert.Equal(t, 1, skipped)
	assert.False(t, w.walkFinished())
	assert.Equal(t, 1, w.attempts())

	_, err = loop.RunOnce(ctx)
	require.NoError(t, err)
	assert.True(t, w.built(after), "the good entity behind the slow one is reached")
	assert.Equal(t, 1, w.attempts(), "no batch came back to it")
	cursor, _, _ = w.walkAt()
	assert.Empty(t, cursor)
	assert.True(t, w.walkFinished(), "the walk ends though an entity never built")

	done := eventsNamed(w.hook, AuthorMetadataEventSummaryEntitySkipped)
	require.Len(t, done, 1)
	assert.Equal(t, slow.String(), done[0].Data["entity_id"])
	assert.Equal(t, "57014", done[0].Data["sqlstate"])
	finishedEvents := eventsNamed(w.hook, AuthorMetadataEventSummaryWalkFinished)
	require.Len(t, finishedEvents, 1)
	assert.Equal(t, 3, finishedEvents[0].Data["count"])

	// The next walk meets the same entity failing the same way.
	w.exec(`UPDATE author_entity_summary_walk SET finished_at = NULL`)
	for i := 0; i < 3; i++ {
		_, err = loop.RunOnce(ctx)
		require.NoError(t, err)
	}
	assert.True(t, w.walkFinished())
	assert.Equal(t, 2, w.attempts(), "once more, once")
}

// The same for a mark: the rollback puts it back, and a fresh transaction takes
// it, or every rest would come back to the entity.
func TestASummaryMarkThatTimesOutIsPassed(t *testing.T) {
	w := newSummaryWorker(t, 2)
	slow, good := w.ids[0], w.ids[1]
	w.slowEntity(slow, 2)
	w.exec(`UPDATE author_entity_summary_walk SET finished_at = now()`)
	w.exec(`INSERT INTO author_entity_summary_dirty (entity_id, marked_at) VALUES (?, now() - interval '1 minute'), (?, now())`, slow, good)
	loop, ctx := w.timeoutLoop(40), context.Background()

	_, err := loop.RunOnce(ctx)
	require.NoError(t, err)
	assert.Zero(t, w.count(`SELECT count(*) FROM author_entity_summary_dirty`), "both marks are gone")
	assert.True(t, w.built(good), "the mark behind the slow one is built")
	assert.False(t, w.built(slow))

	_, err = loop.RunOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, w.attempts(), "the next rest does not come back to it")
	skipped := eventsNamed(w.hook, AuthorMetadataEventSummaryEntitySkipped)
	require.Len(t, skipped, 1)
	assert.Equal(t, "57014", skipped[0].Data["sqlstate"])
}

// pausedSummaryDB runs afterFailure once, between the first failed transaction
// and the one that passes the entity: a pod descheduled there.
type pausedSummaryDB struct {
	db           *pg.DB
	afterFailure func()
}

func (p *pausedSummaryDB) RunInTransaction(ctx context.Context, fn func(*pg.Tx) error) error {
	err := p.db.RunInTransaction(ctx, fn)
	if err != nil && p.afterFailure != nil {
		resume := p.afterFailure
		p.afterFailure = nil
		resume()
	}
	return err
}

// The pass takes the generation it failed on and no other. While this pod is
// away, another one consumes the restored mark and a real edit marks the entity
// again: that mark is still owed a rebuild, and the pass must leave it.
func TestASummaryPassLeavesANewerMark(t *testing.T) {
	const timeout = 200 * time.Millisecond
	w := newSummaryWorker(t, 1)
	id, ctx := w.ids[0], context.Background()
	previous := database.GetDB()
	database.SetDB(w.db)
	defer database.SetDB(previous)
	w.exec(`INSERT INTO author_entity_credit_assignment (credit_id, entity_id, method, rule)
		SELECT min(id), ?, 'manual', 'test' FROM book_contributor_credit`, id)
	drainDisplay := func() {
		require.NoError(t, w.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
			_, err := database.DrainAuthorDisplayMarks(ctx, tx, 10)
			return err
		}))
	}
	drainDisplay()
	w.exec(`TRUNCATE author_entity_summary, author_entity_summary_dirty`)
	w.exec(`INSERT INTO author_entity_summary_dirty (entity_id) VALUES (?)`, id)
	w.slowEntity(id, 2)

	paused := &pausedSummaryDB{db: w.db, afterFailure: func() {
		w.exec(`DROP TRIGGER slow_summary ON author_entity_summary`)
		other, err := database.DrainAuthorSummaryMarks(ctx, w.db, 1, timeout)
		require.NoError(t, err)
		require.Equal(t, 1, other.Entities, "the other pod consumes the restored mark")
		var book int64
		_, err = w.db.QueryOne(pg.Scan(&book), `SELECT min(id) FROM opds_catalog_book`)
		require.NoError(t, err)
		title := "Newer title"
		_, err = database.UpdateBook(models.BookUpdateRequest{ID: book, Title: &title})
		require.NoError(t, err)
		drainDisplay()
		require.Equal(t, 1, w.count(`SELECT count(*) FROM author_entity_summary_dirty WHERE entity_id = ?`, id),
			"the edit marks the entity again")
	}}

	done, err := database.DrainAuthorSummaryMarks(ctx, paused, 1, timeout)
	require.NoError(t, err)
	require.Len(t, done.Skipped, 1)
	assert.Equal(t, "57014", done.Skipped[0].SQLState)
	assert.Equal(t, 1, w.count(`SELECT count(*) FROM author_entity_summary_dirty WHERE entity_id = ?`, id),
		"the newer mark stays")

	_, err = database.DrainAuthorSummaryMarks(ctx, w.db, 1, timeout)
	require.NoError(t, err)
	assert.Equal(t, 1, w.count(`SELECT count(*) FROM author_entity_summary
		WHERE entity_id = ? AND lang = '' AND works @> ARRAY['Newer title']`, id), "and the edit reaches the summary")
}

// endingTheBatch runs one pass that end interrupts while the first entity sleeps,
// and shows nothing was passed: no entity is to blame, so it stays due.
func (w *summaryWorker) endingTheBatch(ctx context.Context, end func()) {
	w.t.Helper()
	slow, good := w.ids[0], w.ids[1]
	w.slowEntity(slow, 30)
	w.exec(`INSERT INTO author_entity_summary_dirty (entity_id) VALUES (?)`, slow)
	go func() {
		// A timer would race the worker: end only once it really sleeps.
		for range 1000 {
			if w.count(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event = 'PgSleep'`) > 0 {
				end()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	loop := NewAuthorDisplayModelLoop(w.db, AuthorDisplayModelConfig{Batch: 10, SummaryBatch: 2,
		SummaryTimeout: 25 * time.Second, Every: 24 * time.Hour})
	_, err := loop.RunOnce(ctx)
	require.Error(w.t, err, "the batch ends")
	assert.Equal(w.t, 1, w.attempts())
	assert.Equal(w.t, 1, w.count(`SELECT count(*) FROM author_entity_summary_dirty WHERE entity_id = ?`, slow), "the mark stays")
	assert.False(w.t, w.built(good), "the entity behind it waits for the next batch")
	assert.Empty(w.t, eventsNamed(w.hook, AuthorMetadataEventSummaryEntitySkipped), "no entity is to blame")
}

func TestACancellationInsideASummaryEntityEndsTheBatch(t *testing.T) {
	w := newSummaryWorker(t, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.endingTheBatch(ctx, cancel)
}

func TestALostConnectionInsideASummaryEntityEndsTheBatch(t *testing.T) {
	w := newSummaryWorker(t, 2)
	w.endingTheBatch(context.Background(), func() {
		w.exec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event = 'PgSleep'`)
	})
}
