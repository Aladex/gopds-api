package services_test

import (
	"archive/zip"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/sirupsen/logrus"                       //nolint:depguard // tests assert on the structured log entries
	logrustest "github.com/sirupsen/logrus/hooks/test" //nolint:depguard // tests assert on the structured log entries
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regressions from the phase-3 review: each test is a reviewer's scenario.

// withEvalSizes swaps the fixture sizes for one test.
func withEvalSizes(t *testing.T, sizes services.AuthorLLMEvalSizes) {
	t.Helper()
	old := evalSizes
	evalSizes = sizes
	t.Cleanup(func() { evalSizes = old })
}

// withEvalAuthors swaps the fixture catalog for one test.
func withEvalAuthors(t *testing.T, authors [][3]string) {
	t.Helper()
	old := evalAuthors
	evalAuthors = authors
	t.Cleanup(func() { evalAuthors = old })
}

func reasonsOf(r *storedReport) string { return strings.Join(r.Evaluation.Decision.Reasons, "; ") }

// B1: an anchor set smaller than requested is never frozen — it could never
// be repaired — and the run reports, unregistrable, why.
func TestAuthorLLMEvalIncompleteAnchorIsNotFrozen(t *testing.T) {
	sizes := evalSizes
	sizes.Anchor = 8 // four per script; the fixture has three of each
	withEvalSizes(t, sizes)
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	e.fake.setRespond(byFields(distinctLabs))
	settings := services.DefaultAuthorLLMEvalSettings()
	run, err := services.StartAuthorLLMEval(context.Background(), e.db, e.version, models.AuthorLLMEvalModeFull, &settings, nil)
	require.NoError(t, err)

	stages := e.evalUntilDone(run)
	assert.Equal(t, []string{"freeze", "fingerprint", "anchor", "report"}, stages, "money was spent without an anchor")
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_eval_set WHERE set_version = 'v1.anchor'`))
	r := downloadReport(t, e, run)
	assert.False(t, r.Evaluation.Decision.Registrable)
	assert.Contains(t, reasonsOf(&r), "anchor evidence")
	assert.True(t, slices.ContainsFunc(r.Skipped, func(s skippedItem) bool { return s.Reason == "anchor_incomplete" }),
		"skipped %+v", r.Skipped)
}

// B1: the design's evidence is 300 anchors and 200 repeats; a fixture-sized
// run is reported but never registrable.
func TestAuthorLLMEvalFixtureSizedRunIsNotRegistrable(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	e.fake.setRespond(byFields(distinctLabs))
	settings := services.DefaultAuthorLLMEvalSettings()
	run, err := services.StartAuthorLLMEval(context.Background(), e.db, e.version, models.AuthorLLMEvalModePilot, &settings, nil)
	require.NoError(t, err)
	e.evalUntilDone(run)
	r := downloadReport(t, e, run)
	assert.False(t, r.Evaluation.Decision.Registrable)
	assert.Contains(t, reasonsOf(&r), "anchor evidence")
	assert.Contains(t, reasonsOf(&r), "repeat evidence")
}

// B2: repeat items whose book changed are not repeated (they would only be
// dropped); with none left the reproducibility evidence is missing and the
// report says so for every participant.
func TestAuthorLLMEvalDroppedRepeatsFailTheEvidence(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	e.fake.setRespond(byFields(distinctLabs))
	ctx := context.Background()
	settings := services.DefaultAuthorLLMEvalSettings()
	run, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModePilot, &settings, nil)
	require.NoError(t, err)
	driver := services.NewAuthorLLMEvalDriver(e.db, &e.identity, e.version, evalSizes, e.archives, nil, time.Second)
	_, err = driver.Step(ctx)
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(e.archives, "eval.zip")))

	e.evalUntilDone(run)
	r := downloadReport(t, e, run)
	assert.Positive(t, r.Dropped)
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE purpose = 'eval_repeat'`),
		"dropped items were repeated")
	for _, p := range []string{"production_a", "production_b", "judge_a", "judge_b"} {
		assert.Contains(t, reasonsOf(&r), "repeat evidence: "+p)
	}
	assert.False(t, r.Evaluation.Decision.Registrable)
}

// B3: invalid settings never become a run.
func TestAuthorLLMEvalRefusesInvalidSettings(t *testing.T) {
	e := newLLMEnv(t)
	ctx := context.Background()
	for name, settings := range map[string]*services.AuthorLLMEvalSettings{
		"unknown mode":  {OutputModes: map[string]string{"production_a": "invalid_mode"}, BatchSizes: []int{1}},
		"unknown slot":  {OutputModes: map[string]string{"judge_c": "tool"}, BatchSizes: []int{1}},
		"no batch":      {OutputModes: map[string]string{}},
		"batch over 20": {BatchSizes: []int{1, 21}},
	} {
		_, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModeFull, settings, nil)
		assert.Error(t, err, name)
	}
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_run`))
	// Nil settings are the defaults.
	run, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModeDev, nil, nil)
	require.NoError(t, err)
	assert.Positive(t, run)
}

// B3: a stage that cannot be done with the run's stored settings ends the
// run — failed, not active — instead of failing every poll forever.
func TestAuthorLLMEvalImpossibleStageEndsTheRun(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	bad := []byte(`{"output_modes":{"production_a":"invalid_mode"},"batch_sizes":[1]}`)
	run, err := database.CreateAuthorLLMEvalRun(ctx, e.db, e.version, models.AuthorLLMEvalModeFull, bad, nil)
	require.NoError(t, err)
	driver := services.NewAuthorLLMEvalDriver(e.db, &e.identity, e.version, evalSizes, e.archives, nil, time.Second)
	for range 3 {
		_, _ = driver.Step(ctx)
	}
	_, status := e.runStage(run)
	assert.Equal(t, string(models.AuthorLLMRunFailedSystemic), status)
	r, err := database.GetAuthorLLMRun(ctx, e.db, run)
	require.NoError(t, err)
	require.NotNil(t, r.LastErrorClass)
	settings := services.DefaultAuthorLLMEvalSettings()
	_, err = services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModeDev, &settings, nil)
	assert.NoError(t, err, "the failed run still blocks the next one")
}

type skippedItem struct {
	Stage       string `json:"stage"`
	Reason      string `json:"reason"`
	SetVersion  string `json:"set_version"`
	Fingerprint string `json:"fingerprint"`
	EvalItemID  int64  `json:"eval_item_id"`
	BookID      int64  `json:"book_id"`
}

type failedJob struct {
	EvalItemID  int64  `json:"eval_item_id"`
	Participant string `json:"participant"`
	Purpose     string `json:"purpose"`
	Status      string `json:"status"`
	Class       string `json:"class"`
}

// Poisoned items: an input over the request limits, an excerpt that does not
// read, and an item no model answers sit among good items. Each is skipped or
// failed with its id and reason in the report; every stage moves on.
func TestAuthorLLMEvalRecordsPoisonedItems(t *testing.T) {
	long := "Ааа Ббб Ввв Ггг Ддд Еее Жжж Ззз Иии Ккк Ллл Ммм Ннн Ооо Ппп Ррр Ссс"
	authors := append(slices.Clone(evalAuthors),
		[3]string{"", "", long}, // 17 tokens: over the request limits
		[3]string{"Ð\u0098Ð²Ð°Ð½", "", "ÐŸÐµÑ‚Ñ€Ð¾Ð²"}, // mojibake: no model answers it
		[3]string{" ", " ", " "},                       // NBSP only: never reaches review
		[3]string{"", "", "!!! ???"},                   // punctuation only: likewise
	)
	withEvalAuthors(t, authors)
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)

	// The first initials book's entry no longer reads: its excerpt is
	// unavailable at freeze.
	path := filepath.Join(e.archives, "eval.zip")
	f, err := os.Create(path)
	require.NoError(t, err)
	zw := zip.NewWriter(f)
	for i, a := range evalAuthors {
		w, createErr := zw.Create(fmt.Sprintf("%d.fb2", i))
		require.NoError(t, createErr)
		body := evalBook(i, a[0], a[1], a[2], time.Now())
		if i == 0 {
			body = []byte("\x00\x01 not an fb2 \xff")
		}
		_, err = w.Write(body)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	require.NoError(t, f.Close())
	var corruptBook int64
	_, err = e.db.QueryOne(pg.Scan(&corruptBook), `SELECT book_id FROM book_metadata_snapshot WHERE entry_name = '0.fb2'`)
	require.NoError(t, err)

	answer := byFields(distinctLabs)
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		reply := answer(req)
		if isFingerprint(req) {
			return reply
		}
		// The mojibake item is left out of every reply.
		var results struct {
			Results []map[string]any `json:"results"`
		}
		require.NoError(t, json.Unmarshal([]byte(reply.Content), &results))
		kept := results.Results[:0]
		for i, r := range results.Results {
			if !strings.Contains(req.Items[i].Tokens[0].Text, "Ð") {
				kept = append(kept, r)
			}
		}
		out, _ := json.Marshal(map[string]any{"results": kept})
		return fakeReply{Content: string(out)}
	})
	settings := services.DefaultAuthorLLMEvalSettings()
	hook := logrustest.NewLocal(logging.GetLogger())
	t.Cleanup(hook.Reset)
	run, err := services.StartAuthorLLMEval(context.Background(), e.db, e.version, models.AuthorLLMEvalModeFull, &settings, nil)
	require.NoError(t, err)
	stages := e.evalUntilDone(run)
	assert.Equal(t, []string{"freeze", "fingerprint", "anchor", "judge", "candidates", "repeat", "report"}, stages)
	skipLogs := map[string][]logrus.Fields{}
	for _, entry := range hook.AllEntries() {
		if entry.Message == string(services.AuthorMetadataEventLLMEvalItemSkipped) {
			reason, _ := entry.Data["status"].(string)
			skipLogs[reason] = append(skipLogs[reason], entry.Data)
		}
	}

	var raw struct {
		Skipped    []skippedItem `json:"skipped"`
		FailedJobs []failedJob   `json:"failed_jobs"`
	}
	text, _, err := services.AuthorLLMEvalReportFile(context.Background(), e.db, run)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(text, &raw))

	var longFP []byte
	_, err = e.db.QueryOne(pg.Scan(&longFP), `SELECT source_fingerprint FROM book_contributor_credit WHERE source_display_name = ?`, long)
	require.NoError(t, err)
	assert.True(t, slices.ContainsFunc(raw.Skipped, func(s skippedItem) bool {
		return s.Reason == "input_too_long" && s.Fingerprint == hex.EncodeToString(longFP) && s.Stage == "freeze"
	}), "skipped %+v", raw.Skipped)
	// Each skip is logged by its row's identifier — the fingerprint, or the
	// book — and never by a name.
	assert.True(t, slices.ContainsFunc(skipLogs["input_too_long"], func(f logrus.Fields) bool {
		return f["fingerprint"] == hex.EncodeToString(longFP)
	}), "input_too_long logs %v", skipLogs["input_too_long"])
	assert.True(t, slices.ContainsFunc(skipLogs["excerpt_unavailable"], func(f logrus.Fields) bool {
		return f["book_id"] == corruptBook && f["fingerprint"] != nil
	}), "excerpt_unavailable logs %v", skipLogs["excerpt_unavailable"])
	for _, entries := range skipLogs {
		for _, f := range entries {
			for _, v := range f {
				assert.NotContains(t, fmt.Sprint(v), "Ааа", "a skip log carries a name")
			}
		}
	}
	assert.True(t, slices.ContainsFunc(raw.Skipped, func(s skippedItem) bool {
		return s.Reason == "excerpt_unavailable" && s.BookID == corruptBook
	}), "skipped %+v", raw.Skipped)

	var mojibakeItem int64
	_, err = e.db.QueryOne(pg.Scan(&mojibakeItem), `SELECT id FROM author_llm_eval_item
		WHERE item_set IN ('holdout', 'dev') AND input::text LIKE '%Ð%' LIMIT 1`)
	require.NoError(t, err)
	failed := slices.DeleteFunc(slices.Clone(raw.FailedJobs), func(j failedJob) bool { return j.EvalItemID != mojibakeItem })
	assert.NotEmpty(t, failed, "failed jobs %+v", raw.FailedJobs)
	for _, j := range failed {
		assert.Equal(t, "invalid", j.Status)
		assert.Equal(t, "item_missing", j.Class)
	}
	// The poisoned item failed alone: its batch companions answered.
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE status IN ('invalid', 'failed')
		AND eval_item_id <> ?`, mojibakeItem))
}

// N3: a pause holds every transition: no stage move, no enqueue, no report
// for a run that is paused when the write happens.
func TestAuthorLLMEvalPausedRunTakesNoTransition(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	settings := services.DefaultAuthorLLMEvalSettings()
	run, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModeFull, &settings, nil)
	require.NoError(t, err)
	driver := services.NewAuthorLLMEvalDriver(e.db, &e.identity, e.version, evalSizes, e.archives, nil, time.Second)
	_, err = driver.Step(ctx) // freeze
	require.NoError(t, err)
	require.NoError(t, database.PauseAuthorLLMRun(ctx, e.db, run, models.AuthorLLMRunPausedAdmin))

	moved, err := database.SetAuthorLLMRunStage(ctx, e.db, run, "fingerprint", "anchor")
	require.NoError(t, err)
	assert.False(t, moved, "a paused run changed stage")
	var item int64
	_, err = e.db.QueryOne(pg.Scan(&item), `SELECT id FROM author_llm_eval_item WHERE item_set = 'fingerprint'`)
	require.NoError(t, err)
	n, err := database.EnqueueAuthorLLMEvalJobs(ctx, e.db, &database.AuthorLLMEvalJobSpec{RunID: run,
		ConfigVersion: e.version, Slot: "production_a", Purpose: models.AuthorLLMPurposeEvalFingerprint,
		BatchSize: 1, OutputMode: "json_schema", ItemIDs: []int64{item}})
	require.NoError(t, err)
	assert.Zero(t, n, "a paused run was given work")
	require.NoError(t, database.StoreAuthorLLMEvalReport(ctx, e.db, run, e.version, []byte(`{}`)))
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_eval_report`), "a paused run was reported")
	_, status := e.runStage(run)
	assert.Equal(t, string(models.AuthorLLMRunPaused), status)
}
