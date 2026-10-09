package services

// Phase 20, RED 1 + 2: the privacy canary. A unique token sits in every piece
// of source text a book can carry — author and translator names, nickname,
// title, annotation, body — and in the text of every injected error. Every
// new author-metadata component is driven through its success, error, retry
// and cancellation paths with the logger at its most verbose level, and the
// hook keeps every entry: no filtering by level or by words. The token must
// not appear in any message, any structured field or any serialized line.
// The second half pins the safe fields that must be there, so a component
// that logs nothing at all does not pass.

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopds-api/internal/authornorm"
	"gopds-api/internal/parser"
	"gopds-api/logging"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	//nolint:depguard // the test raises the logger to its most verbose level
	"github.com/sirupsen/logrus"
	//nolint:depguard // asserting on emitted log output needs logrus' own test hook, and logging wraps logrus
	logrustest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// canary is the token; the check is case-insensitive, because search keys
// and display names change case.
const canary = "zqxcanary"

// canaryFB2 carries the token in every source text field. Its second author
// is an initialed name, so the local stream opens a review item for it.
const canaryFB2 = `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description><title-info>
<author><first-name>Zqxcanary</first-name><middle-name>Zqxcanaryevich</middle-name>
<last-name>Zqxcanaryov</last-name><nickname>zqxcanary</nickname></author>
<author><first-name>Z.</first-name><last-name>Zqxcanarski</last-name></author>
<translator><first-name>Zqxcanarytr</first-name><last-name>Zqxcanarytrov</last-name></translator>
<book-title>Zqxcanary Title</book-title>
<annotation><p>zqxcanary annotation</p></annotation>
<lang>ru</lang>
</title-info>
<document-info><author><nickname>zqxcanarydoc</nickname></author></document-info>
</description><body><p>zqxcanary body</p></body></FictionBook>`

// brokenFB2 is a document error that still carries the token.
const brokenFB2 = `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0"><description><title-info>
<book-title>Zqxcanary Broken</book-title><author><last-name>Zqxcanaryov</`

// errCanary is an injected failure whose text carries the token.
var errCanary = errors.New("upstream failure mentioning Zqxcanary Title by Zqxcanaryov")

// privacyLog captures every entry at every level for one test.
func privacyLog(t *testing.T) *logrustest.Hook {
	t.Helper()
	hook := logrustest.NewLocal(logging.GetLogger())
	t.Cleanup(hook.Reset)
	previous := logging.GetLogger().GetLevel()
	logging.GetLogger().SetLevel(logrus.TraceLevel)
	t.Cleanup(func() { logging.GetLogger().SetLevel(previous) })
	return hook
}

// leaks returns every captured line that carries the token in its message, a
// field or its serialized form.
func leaks(t *testing.T, hook *logrustest.Hook) []string {
	t.Helper()
	var found []string
	for _, e := range hook.AllEntries() {
		var b strings.Builder
		b.WriteString(e.Message)
		for k, v := range e.Data {
			fmt.Fprintf(&b, " %s=%+v", k, v)
		}
		line, err := e.String()
		require.NoError(t, err)
		b.WriteString(line)
		if strings.Contains(strings.ToLower(b.String()), canary) {
			found = append(found, b.String())
		}
	}
	return found
}

// eventsNamed returns the captured events of one name.
func eventsNamed(hook *logrustest.Hook, name AuthorMetadataEventName) []logrus.Entry {
	var out []logrus.Entry
	for _, e := range hook.AllEntries() {
		if e.Message == string(name) {
			out = append(out, *e)
		}
	}
	return out
}

// privacyArchive writes a zip of entries into dir.
func privacyArchive(t *testing.T, dir, name string, entries map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for entry, body := range entries {
		f, err := w.Create(entry)
		require.NoError(t, err)
		_, err = io.WriteString(f, body)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o600))
}

// privacyRun seeds catalog books for entries of one archive and starts a
// smoke run over them.
func (f *workerFixture) privacyRun(archive string, entries ...string) *models.AuthorMetadataRun {
	f.t.Helper()
	var ids []int64
	for _, entry := range entries {
		var id int64
		_, err := f.db.QueryOne(pg.Scan(&id), `INSERT INTO opds_catalog_book
			(filename, path, format, registerdate, docdate, lang, title, annotation, md5)
			VALUES (?, ?, 'fb2', now(), '', 'ru', 'catalog', '', '') RETURNING id`, entry, archive)
		require.NoError(f.t, err)
		ids = append(ids, id)
	}
	run, err := NewAuthorMetadataRunService(f.db).StartRun(context.Background(), &StartRunRequest{
		Mode: models.AuthorMetadataRunSmoke, BookIDs: ids,
	})
	require.NoError(f.t, err)
	return run
}

func privacyExtraction(
	t *testing.T, db *pg.DB, dir string, source ArchiveSource, extractor MetadataExtractor,
) *AuthorMetadataExtractionWorker {
	t.Helper()
	cfg := DefaultExtractionWorkerConfig(dir, extractor)
	cfg.Retry = AuthorMetadataRetryPolicy{
		MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond,
		Jitter: func(time.Duration) time.Duration { return 0 },
	}
	w, err := NewAuthorMetadataExtractionWorker(db, source, &cfg)
	require.NoError(t, err)
	return w
}

func productionExtractor() parser.MetadataExtractor {
	return parser.MetadataExtractor{MetadataMaxBytes: AuthorMetadataMaxBytes, ExtractorVersion: AuthorMetadataExtractorVersion}
}

// failingExtractor fails every extraction with the canary error, after
// running hook (used to cancel the caller's context).
type privacyFailingExtractor struct{ hook func() }

func (e privacyFailingExtractor) Extract(parser.ExtractBookInput) (authornorm.SourceMetadata, error) {
	if e.hook != nil {
		e.hook()
	}
	return authornorm.SourceMetadata{}, errCanary
}

// privacyReadSource opens archives whose entries fail to read with the
// canary error.
type privacyReadSource struct{}

func (privacyReadSource) Open(context.Context, string) (ArchiveReader, error) {
	return privacyReadArchive{}, nil
}

type privacyReadArchive struct{}

func (privacyReadArchive) Close() error { return nil }

func (privacyReadArchive) OpenEntry(string) (io.ReadCloser, error) { return privacyReadEntry{}, nil }

type privacyReadEntry struct{}

func (privacyReadEntry) Close() error { return nil }

func (privacyReadEntry) Read([]byte) (int, error) { return 0, errCanary }

// drainExtraction runs the worker until it has nothing to claim.
func drainExtraction(t *testing.T, ctx context.Context, w *AuthorMetadataExtractionWorker) {
	t.Helper()
	for i := 0; i < 10; i++ {
		n, err := w.ProcessAvailable(ctx)
		if err != nil || n == 0 {
			return
		}
	}
}

func TestAuthorMetadataPrivacyCanary(t *testing.T) {
	s := localWorkerDB(t)
	hook := privacyLog(t)
	ctx := context.Background()

	t.Run("extraction, local, review and dual write succeed", func(t *testing.T) {
		resetPipeline(t, s)
		f := &workerFixture{t: t, db: s}
		dir := t.TempDir()
		privacyArchive(t, dir, "canary.zip", map[string]string{"canary.fb2": canaryFB2})
		run := f.privacyRun("canary.zip", "canary.fb2")
		drainExtraction(t, ctx, privacyExtraction(t, s, dir, ZipArchiveSource{}, productionExtractor()))
		assert.Equal(t, 1, f.count(`SELECT count(*) FROM author_metadata_run_item WHERE run_id = ? AND status = 'extracted'`, run.ID))

		f.drain(f.worker(nil))
		drainExtraction(t, ctx, privacyExtraction(t, s, dir, ZipArchiveSource{}, productionExtractor()))
		assert.Equal(t, 1, f.count(`SELECT count(*) FROM author_metadata_run WHERE id = ? AND status = 'completed'`, run.ID))

		review, err := NewAuthorMetadataReviewService(s, DefaultAuthorMetadataReviewConfig())
		require.NoError(t, err)
		page, err := review.List(ctx, ReviewListOptions{})
		require.NoError(t, err)
		require.NotEmpty(t, page.Items, "the initialed canary author is in review")
		item := page.Items[0].ID
		_, err = review.Detail(ctx, item)
		require.NoError(t, err)
		_, err = review.Edit(ctx, item, 1, &AdminCorrection{
			GivenName: "Zqxcanary", FamilyName: "Zqxcanarski", DisplayName: "Zqxcanary Zqxcanarski",
			SortName: "Zqxcanarski, Zqxcanary", SearchKey: "zqxcanary zqxcanarski", Script: "Latn",
			Kind: models.NormalizationPerson,
		})
		require.NoError(t, err)
		_, err = review.Accept(ctx, item, 1)
		require.Error(t, err, "a closed item refuses a second decision")

		writer := NewAuthorMetadataSourceWriter()
		prepared, err := writer.Prepare([]byte(canaryFB2), "live.zip", "live.fb2", strings.Repeat("a", 32))
		require.NoError(t, err)
		var book int64
		_, err = s.QueryOne(pg.Scan(&book), `INSERT INTO opds_catalog_book
			(filename, path, format, registerdate, docdate, lang, title, annotation, md5)
			VALUES ('live.fb2', 'live.zip', 'fb2', now(), '', 'ru', 'catalog', '', ?) RETURNING id`, strings.Repeat("a", 32))
		require.NoError(t, err)
		require.NoError(t, s.RunInTransaction(ctx, func(tx *pg.Tx) error { return writer.Persist(tx, book, prepared) }))

		broken, err := writer.Prepare([]byte(brokenFB2), "live.zip", "broken.fb2", strings.Repeat("b", 32))
		require.NoError(t, err)
		require.False(t, broken.Extracted())
		require.NoError(t, s.RunInTransaction(ctx, func(tx *pg.Tx) error { return writer.Persist(tx, book, broken) }))
	})

	t.Run("extraction document error, retry and exhaustion", func(t *testing.T) {
		resetPipeline(t, s)
		f := &workerFixture{t: t, db: s}
		dir := t.TempDir()
		privacyArchive(t, dir, "broken.zip", map[string]string{"broken.fb2": brokenFB2})
		f.privacyRun("broken.zip", "broken.fb2")
		drainExtraction(t, ctx, privacyExtraction(t, s, dir, ZipArchiveSource{}, productionExtractor()))

		privacyArchive(t, dir, "canary.zip", map[string]string{"canary.fb2": canaryFB2})
		f.exec(`UPDATE author_metadata_run SET status = 'completed', finished_at = now(), extraction_completed_at = now()
			WHERE status = 'running'`)
		f.privacyRun("canary.zip", "canary.fb2")
		drainExtraction(t, ctx, privacyExtraction(t, s, dir, ZipArchiveSource{}, privacyFailingExtractor{}))
	})

	t.Run("extraction source failure, cancellation and database invariant", func(t *testing.T) {
		resetPipeline(t, s)
		f := &workerFixture{t: t, db: s}
		dir := t.TempDir()
		f.privacyRun("unread.zip", "canary.fb2")
		drainExtraction(t, ctx, privacyExtraction(t, s, dir, privacyReadSource{}, productionExtractor()))

		f.exec(`UPDATE author_metadata_run SET status = 'completed', finished_at = now(), extraction_completed_at = now()
			WHERE status IN ('running', 'paused')`)
		privacyArchive(t, dir, "canary.zip", map[string]string{"canary.fb2": canaryFB2})
		f.privacyRun("canary.zip", "canary.fb2")
		cancelCtx, cancel := context.WithCancel(ctx)
		drainExtraction(t, cancelCtx, privacyExtraction(t, s, dir, ZipArchiveSource{}, privacyFailingExtractor{hook: cancel}))
		cancel()

		f.exec(`UPDATE author_metadata_run SET status = 'completed', finished_at = now(), extraction_completed_at = now()
			WHERE status IN ('running', 'paused')`)
		f.privacyRun("canary.zip", "canary.fb2")
		f.exec(`CREATE OR REPLACE FUNCTION privacy_canary_failure() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'refused: ' || coalesce(NEW.source_title, ''); END $$`)
		f.exec(`CREATE TRIGGER privacy_canary_failure BEFORE INSERT ON book_metadata_snapshot
			FOR EACH ROW EXECUTE FUNCTION privacy_canary_failure()`)
		t.Cleanup(func() { f.exec(`DROP TRIGGER IF EXISTS privacy_canary_failure ON book_metadata_snapshot`) })
		drainExtraction(t, ctx, privacyExtraction(t, s, dir, ZipArchiveSource{}, productionExtractor()))
		f.exec(`DROP TRIGGER privacy_canary_failure ON book_metadata_snapshot`)
	})

	t.Run("local failure, lease loss and cancellation", func(t *testing.T) {
		resetPipeline(t, s)
		f := &workerFixture{t: t, db: s}
		v := source(t, "first", "Zqxcanary", "last", "Zqxcanaryov")
		f.persistBook(author(v))
		failing := func(authornorm.SourceValue, string) (authornorm.Result, error) { return authornorm.Result{}, errCanary }
		f.drain(f.worker(func(c *AuthorMetadataLocalWorkerConfig) { c.Normalize = failing }))

		resetPipeline(t, s)
		f.persistBook(author(v))
		stealing := func(sv authornorm.SourceValue, e string) (authornorm.Result, error) {
			f.exec(`UPDATE contributor_normalization_job SET lease_owner = gen_random_uuid() WHERE lease_owner IS NOT NULL`)
			return authornorm.Normalize(sv, e)
		}
		_, err := f.worker(func(c *AuthorMetadataLocalWorkerConfig) { c.Normalize = stealing }).RunOnce(ctx)
		require.NoError(t, err)

		resetPipeline(t, s)
		f.persistBook(author(v))
		cancelCtx, cancel := context.WithCancel(ctx)
		canceling := func(sv authornorm.SourceValue, e string) (authornorm.Result, error) {
			cancel()
			return authornorm.Normalize(sv, e)
		}
		_, _ = f.worker(func(c *AuthorMetadataLocalWorkerConfig) { c.Normalize = canceling }).RunOnce(cancelCtx)
	})

	t.Run("runner", func(t *testing.T) {
		failing := &fakeStageWorker{name: "local_normalization-0", err: errCanary}
		r := NewAuthorMetadataRunner(failing)
		require.NoError(t, r.Start(ctx, func(context.Context) error { return nil }))
		runnerEventually(t, func() bool { return failing.finished.Load() == 1 })
		shutdown, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		require.NoError(t, r.Shutdown(shutdown))

		calls := 0
		loopCtx, stop := context.WithCancel(ctx)
		require.NoError(t, pollLoop(loopCtx, string(AuthorMetadataStageExtraction), time.Millisecond, func(context.Context) (bool, error) {
			calls++
			if calls > 2 {
				stop()
			}
			return false, errCanary
		}))
	})

	t.Run("no canary anywhere", func(t *testing.T) {
		assert.Empty(t, leaks(t, hook), "source text reached the log")
	})

	// RED 2: the safe fields are there, so logging nothing does not pass.
	t.Run("safe fields are present", func(t *testing.T) {
		required := map[AuthorMetadataEventName][]string{
			AuthorMetadataEventExtractionItemCompleted: {"stage", "run_id", "item_id", "book_id", "status"},
			AuthorMetadataEventExtractionItemRetried:   {"stage", "run_id", "item_id", "class", "attempt_no"},
			AuthorMetadataEventExtractionRunPaused:     {"stage", "run_id", "class"},
			AuthorMetadataEventExtractionRunEnded:      {"stage", "run_id", "class"},
			AuthorMetadataEventRunCompleted:            {"stage", "run_id"},
			AuthorMetadataEventLocalJobCompleted:       {"stage", "job_id", "status"},
			AuthorMetadataEventLocalAttemptFailed:      {"stage", "job_id", "attempt_no", "class"},
			AuthorMetadataEventLocalLeaseLost:          {"stage", "job_id"},
			AuthorMetadataEventSourcePersisted:         {"stage", "book_id"},
			AuthorMetadataEventSourceSkipped:           {"stage", "book_id", "status"},
			AuthorMetadataEventReviewAction:            {"stage", "review_item_id", "status"},
			AuthorMetadataEventReviewActionFailed:      {"stage", "review_item_id", "status"},
			AuthorMetadataEventWorkersStarted:          {"stage", "count"},
			AuthorMetadataEventWorkersStopped:          {"stage"},
			AuthorMetadataEventWorkerStopped:           {"stage", "worker"},
			AuthorMetadataEventWorkerBatchFailed:       {"stage", "worker"},
		}
		// Every value an event carries is a known one: a status, class or
		// worker label the writer had to replace by its sentinel means a
		// component passed something outside the closed sets.
		for _, e := range hook.AllEntries() {
			if !strings.HasPrefix(e.Message, "author_metadata.") {
				continue
			}
			assert.NotEqual(t, string(authorMetadataUnknownEvent), e.Message)
			for key, value := range e.Data {
				assert.NotEqual(t, authorMetadataUnknownValue, value, "%s carries an unknown %s", e.Message, key)
			}
		}
		// Per branch, not only per event name: the completion event is there
		// for a successful extraction and for a per-book terminal status.
		completedStatuses := map[interface{}]bool{}
		for _, e := range eventsNamed(hook, AuthorMetadataEventExtractionItemCompleted) {
			completedStatuses[e.Data["status"]] = true
		}
		for _, status := range []string{"extracted", "metadata_parse_failed"} {
			assert.True(t, completedStatuses[status], "no extraction_item_completed event with status %s", status)
		}
		for name, fields := range required {
			events := eventsNamed(hook, name)
			if !assert.NotEmpty(t, events, "no %s event", name) {
				continue
			}
			for _, field := range fields {
				assert.Contains(t, events[0].Data, field, "%s without %s", name, field)
			}
		}
	})
}

// Fix round 2 (re-review B4): PostgreSQL lets a trigger raise any five-
// character ERRCODE, so a short source title can arrive as a well-formed
// SQLSTATE. Through the real extractor, worker and database, the code a
// trigger built from the title never reaches the log; the failure is still
// reported.
func TestSourceTextCannotBecomeSQLStateThroughDatabaseError(t *testing.T) {
	s := localWorkerDB(t)
	resetPipeline(t, s)
	f := &workerFixture{t: t, db: s}
	ctx := context.Background()
	hook := privacyLog(t)
	dir := t.TempDir()
	privacyArchive(t, dir, "alice.zip", map[string]string{"alice.fb2": strings.Replace(canaryFB2, "Zqxcanary Title", "ALICE", 1)})
	f.privacyRun("alice.zip", "alice.fb2")
	f.exec(`CREATE OR REPLACE FUNCTION privacy_sqlstate_smuggle() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION USING ERRCODE = NEW.source_title, MESSAGE = 'snapshot rejected'; END $$`)
	f.exec(`CREATE TRIGGER privacy_sqlstate_smuggle BEFORE INSERT ON book_metadata_snapshot
		FOR EACH ROW EXECUTE FUNCTION privacy_sqlstate_smuggle()`)
	t.Cleanup(func() { f.exec(`DROP TRIGGER IF EXISTS privacy_sqlstate_smuggle ON book_metadata_snapshot`) })

	_, err := privacyExtraction(t, s, dir, ZipArchiveSource{}, productionExtractor()).ProcessAvailable(ctx)
	require.NoError(t, err)

	var states []string
	for _, e := range hook.AllEntries() {
		line, lineErr := e.String()
		require.NoError(t, lineErr)
		assert.NotContains(t, strings.ToUpper(line+fmt.Sprint(e.Data)), "ALICE", "the title escaped through SQLSTATE")
		if v, ok := e.Data["sqlstate"]; ok {
			states = append(states, fmt.Sprint(v))
		}
	}
	require.NotEmpty(t, states, "the failure is still reported")
	for _, state := range states {
		assert.Equal(t, authorMetadataUnknownValue, state)
	}
}
