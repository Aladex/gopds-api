package services_test

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The eval run end to end on a fixture provider: books ingested by the real
// scanner and local normalizer, the stages driven by the real driver and
// answered by the real worker against the fake endpoint, a pause and a
// restart at every stage.

// evalSizes are the method's sizes shrunk to a fixture catalog.
var evalSizes = services.AuthorLLMEvalSizes{Holdout: 3, Dev: 2, Anchor: 4, Canary: 2, Repeat: 2,
	FingerprintRepeats: 3, AnchorMinSupport: 1, AnchorMinSharePercent: 95}

// evalBook is one fixture book with one author whose name is in its body.
func evalBook(id int, first, middle, last string, now time.Time) []byte {
	middleTag, firstTag := "", ""
	if middle != "" {
		middleTag = "<middle-name>" + middle + "</middle-name>"
	}
	if first != "" {
		firstTag = "<first-name>" + first + "</first-name>"
	}
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description>
<title-info>
<genre>prose</genre>
<author>%[6]s%[2]s<last-name>%[3]s</last-name></author>
<book-title>Книга номер %[4]d</book-title>
<lang>ru</lang>
</title-info>
<document-info><date>%[5]d</date><id>llm-eval-%[4]d</id></document-info>
</description>
<body><section><p>%[1]s %[3]s написал эту книгу, и часы пробили тринадцать раз в тот холодный день.</p></section></body>
</FictionBook>
`, first, middleTag, last, id, now.Year(), firstTag))
}

// evalAuthors are the fixture's authors: initials (ambiguous, Cyrillic) and
// plain first-last names the shipped policy selects (the anchor candidates).
var evalAuthors = [][3]string{
	{"А.", "Б.", "Петров"}, {"В.", "Г.", "Сидоров"}, {"Д.", "Е.", "Кузнецов"}, {"Ж.", "З.", "Смирнов"},
	{"И.", "К.", "Попов"}, {"Л.", "М.", "Соколов"}, {"Н.", "О.", "Лебедев"},
	{"Иван", "", "Морозов"}, {"Пётр", "", "Волков"}, {"John", "", "Smith"}, {"Mary", "", "Brown"},
	{"Анна", "", "Зайцева"}, {"Peter", "", "Jones"},
	// Two words in the last-name field only: another ambiguous stratum.
	{"", "", "Джон Брэндон"}, {"", "", "Лу Синь"},
}

// ingestEvalCatalogue scans the fixture books and runs the local normalizer.
func ingestEvalCatalogue(t *testing.T, e *llmEnv) {
	t.Helper()
	path := filepath.Join(e.archives, "eval.zip")
	writeEvalArchive(t, path, evalAuthors, 0)
	scanArchive(t, e, path)
	drainLocal(t, e.db)
}

// writeEvalArchive writes one fixture book per author into a zip, numbered
// from first.
func writeEvalArchive(t *testing.T, path string, authors [][3]string, first int) {
	t.Helper()
	f, err := os.Create(path)
	require.NoError(t, err)
	zw := zip.NewWriter(f)
	for i, a := range authors {
		w, createErr := zw.Create(fmt.Sprintf("%d.fb2", first+i))
		require.NoError(t, createErr)
		_, err = w.Write(evalBook(first+i, a[0], a[1], a[2], time.Now()))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	require.NoError(t, f.Close())
}

// scanArchive runs the real scanner over every book of an archive.
func scanArchive(t *testing.T, e *llmEnv, path string) {
	t.Helper()
	reader, err := zip.OpenReader(path)
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()
	scanner := services.NewBookScanService(e.archives, t.TempDir(), services.NewLanguageDetector(false, 5*time.Second), true, nil)
	for _, file := range reader.File {
		_, err = scanner.ProcessBook(file, filepath.Base(path))
		require.NoError(t, err)
	}
}

// byFields answers every item with the roles of its FB2 fields, the way a
// careful model would on these names; the fixed check gets each model's own
// prompt token count, as distinct tokenizers would.
func byFields(prompts map[string]int64) func(req *fakeRequest) fakeReply {
	return func(req *fakeRequest) fakeReply {
		results := make([]any, 0, len(req.Items))
		for _, it := range req.Items {
			roles := make([]any, 0, len(it.Tokens))
			for _, tok := range it.Tokens {
				role := map[string]string{"first": "given", "middle": "additional", "last": "family",
					"nickname": "nickname"}[tok.Field]
				roles = append(roles, map[string]any{"i": tok.I, "role": role})
			}
			results = append(results, map[string]any{"id": it.ID, "form": "person",
				"roles": roles, "case_fix": []any{}})
		}
		out, _ := json.Marshal(map[string]any{"results": results})
		reply := fakeReply{Content: string(out)}
		if isFingerprint(req) {
			reply.Prompt = prompts[req.Model]
		}
		return reply
	}
}

// distinctLabs gives the four models the prompt counts of three tokenizers:
// the two OpenAI models share one.
var distinctLabs = map[string]int64{"gpt-6-luna": 700, "deepseek-v4-pro": 800, "gpt-6-sol": 700, "claude-opus-5": 900}

func (e *llmEnv) runStage(run int64) (stage, status string) {
	e.t.Helper()
	r, err := database.GetAuthorLLMRun(context.Background(), e.db, run)
	require.NoError(e.t, err)
	if r.Stage != nil {
		stage = *r.Stage
	}
	return stage, string(r.Status)
}

// evalUntilDone steps the driver and the worker until the run completes,
// pausing at the start of every stage and restarting both (new instances, as
// after a pod restart) before resuming. It returns the stages seen.
func (e *llmEnv) evalUntilDone(run int64) []string {
	e.t.Helper()
	ctx := context.Background()
	driver := services.NewAuthorLLMEvalDriver(e.db, &e.identity, e.version, evalSizes, e.archives, nil, time.Second)
	w := e.worker()
	var stages []string
	for range 400 {
		stage, status := e.runStage(run)
		if status == string(models.AuthorLLMRunCompleted) {
			return stages
		}
		if len(stages) == 0 || stages[len(stages)-1] != stage {
			stages = append(stages, stage)
			// Pause: nothing moves, nothing is sent.
			require.NoError(e.t, database.PauseAuthorLLMRun(ctx, e.db, run, models.AuthorLLMRunPausedAdmin))
			calls := e.count(`SELECT count(*) FROM author_llm_call`)
			jobs := e.count(`SELECT count(*) FROM author_llm_job`)
			for range 3 {
				_, err := driver.Step(ctx)
				require.NoError(e.t, err)
				_, err = w.RunOnce(ctx)
				require.NoError(e.t, err)
			}
			now, _ := e.runStage(run)
			require.Equal(e.t, stage, now, "a paused run moved on")
			require.Equal(e.t, calls, e.count(`SELECT count(*) FROM author_llm_call`), "a paused run sent a call")
			require.Equal(e.t, jobs, e.count(`SELECT count(*) FROM author_llm_job`), "a paused run enqueued work")
			// Restart: fresh driver and worker, then resume.
			driver = services.NewAuthorLLMEvalDriver(e.db, &e.identity, e.version, evalSizes, e.archives, nil, time.Second)
			w = e.worker()
			require.NoError(e.t, database.ResumeAuthorLLMRun(ctx, e.db, run))
		}
		_, err := driver.Step(ctx)
		require.NoError(e.t, err)
		_, err = w.RunOnce(ctx)
		require.NoError(e.t, err)
		time.Sleep(5 * time.Millisecond)
	}
	e.t.Fatal("the eval run did not complete")
	return stages
}

type storedReport struct {
	Mode             string        `json:"mode"`
	Skipped          []skippedItem `json:"skipped"`
	Dropped          int           `json:"dropped_items"`
	FullRunOnHoldout int           `json:"full_run_on_holdout"`
	Sets             []struct {
		SetVersion string `json:"set_version"`
		SHA256     string `json:"sha256"`
		Items      int    `json:"items"`
	} `json:"sets"`
	Fingerprints []struct {
		Participant  string   `json:"participant"`
		Models       []string `json:"models"`
		PromptTokens int64    `json:"prompt_tokens"`
	} `json:"fingerprints"`
	Evaluation struct {
		Strata map[string]struct {
			Holdout int `json:"holdout"`
			Combos  map[string]struct {
				Accepted int `json:"accepted"`
				Errors   int `json:"errors"`
			} `json:"combos"`
		} `json:"strata"`
		Anchor []struct {
			Participant    string
			Matched, Total int
		} `json:"anchor"`
		Repeat []struct {
			Participant    string
			Matched, Total int
		} `json:"repeat"`
		Decision struct {
			Registrable bool     `json:"registrable"`
			Reasons     []string `json:"reasons"`
			Strata      []struct {
				Stratum string `json:"stratum"`
				Passed  bool   `json:"passed"`
			} `json:"strata"`
		} `json:"decision"`
	} `json:"evaluation"`
}

// downloadReport reads a run's report as the admin downloads it and checks
// its digest.
func downloadReport(t *testing.T, e *llmEnv, run int64) storedReport {
	t.Helper()
	raw, sha, err := services.AuthorLLMEvalReportFile(context.Background(), e.db, run)
	require.NoError(t, err)
	sum := sha256.Sum256(raw)
	require.Equal(t, hex.EncodeToString(sum[:]), sha, "the digest is not the downloaded bytes'")
	var r storedReport
	require.NoError(t, json.Unmarshal(raw, &r))
	return r
}

func TestAuthorLLMEvalRunsEveryStageWithPausesAndRestarts(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	e.fake.setRespond(byFields(distinctLabs))
	ctx := context.Background()
	settings := services.DefaultAuthorLLMEvalSettings()
	// Both arms: the excerpt arm stays measurable when a run asks for it.
	settings.NoExcerpt = false
	run, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModeFull, &settings, nil)
	require.NoError(t, err)

	stages := e.evalUntilDone(run)
	assert.Equal(t, []string{"freeze", "fingerprint", "anchor", "judge", "candidates", "repeat", "report"}, stages)

	r := downloadReport(t, e, run)
	assert.Equal(t, "full", r.Mode)
	assert.Equal(t, 1, r.FullRunOnHoldout)
	strata, ok := r.Evaluation.Strata["initials.cyrl"]
	require.True(t, ok, "strata = %v", r.Evaluation.Strata)
	assert.Equal(t, 3, strata.Holdout)
	for _, combo := range []string{"A1", "B1", "A10", "B10"} {
		assert.Equal(t, 3, strata.Combos[combo].Accepted, "combo %s", combo)
		assert.Zero(t, strata.Combos[combo].Errors, "combo %s", combo)
	}
	// Three accepted items cannot pass a 0.5% bound: the run decides, and
	// decides no. Fixture-sized anchors and repeats are short of the design's
	// evidence, so the run is not registrable either — and says only that.
	assert.False(t, r.Evaluation.Decision.Registrable)
	for _, reason := range r.Evaluation.Decision.Reasons {
		assert.Regexp(t, `^(anchor|repeat) evidence: `, reason)
	}
	for _, d := range r.Evaluation.Decision.Strata {
		assert.False(t, d.Passed, "stratum %s", d.Stratum)
	}
	for _, a := range r.Evaluation.Anchor {
		assert.Equal(t, a.Total, a.Matched, "anchor %s", a.Participant)
		assert.Equal(t, 4, a.Total)
	}
	for _, rep := range r.Evaluation.Repeat {
		assert.Equal(t, 2, rep.Total, "repeat %s", rep.Participant)
		assert.Equal(t, rep.Total, rep.Matched)
	}
	learned := map[string]int64{}
	for _, f := range r.Fingerprints {
		learned[f.Participant] = f.PromptTokens
	}
	assert.Equal(t, map[string]int64{"production_a": 700, "production_b": 800, "judge_a": 700, "judge_b": 900}, learned)

	// The run learned its participants: V0 ids and references on the run.
	runRow, err := database.GetAuthorLLMRun(ctx, e.db, run)
	require.NoError(t, err)
	assert.Contains(t, string(runRow.ExpectedModels), "deepseek-v4-pro")
	assert.Contains(t, string(runRow.Reference), `"prompt_tokens": 800`)
	// Excerpts were frozen and sent in arm B; ten items per request were
	// sent in calls carrying several items.
	assert.Positive(t, e.count(`SELECT count(*) FROM author_llm_eval_item WHERE context_sha256 IS NOT NULL`))
	assert.Positive(t, e.count(`SELECT count(*) FROM author_llm_job WHERE arm = 'B' AND context_state = 'attached'`))
	assert.Positive(t, e.count(`SELECT count(*) FROM author_llm_call WHERE kind = 'jobs' AND items > 1`))
	// Every set the report cites is frozen with that digest.
	for _, s := range r.Sets {
		var sum []byte
		_, err = e.db.QueryOne(pg.Scan(&sum), `SELECT sha256 FROM author_llm_eval_set WHERE set_version = ?`, s.SetVersion)
		require.NoError(t, err)
		assert.Equal(t, hex.EncodeToString(sum), s.SHA256, "set %s", s.SetVersion)
	}

	// A second full run measures the same frozen sets: nothing is sampled
	// again, and the report counts it as the second attempt.
	sets := e.count(`SELECT count(*) FROM author_llm_eval_set`)
	items := e.count(`SELECT count(*) FROM author_llm_eval_item`)
	again, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModeFull, &settings, nil)
	require.NoError(t, err)
	e.evalUntilDone(again)
	assert.Equal(t, sets, e.count(`SELECT count(*) FROM author_llm_eval_set`))
	assert.Equal(t, items, e.count(`SELECT count(*) FROM author_llm_eval_item`))
	assert.Equal(t, 2, downloadReport(t, e, again).FullRunOnHoldout)
}

func TestAuthorLLMEvalPilotFreezesOneStratumAndFullTheRest(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	e.fake.setRespond(byFields(distinctLabs))
	ctx := context.Background()
	settings := services.DefaultAuthorLLMEvalSettings()
	pilot, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModePilot, &settings, nil)
	require.NoError(t, err)
	e.evalUntilDone(pilot)
	var versions []string
	_, err = e.db.Query(&versions, `SELECT set_version FROM author_llm_eval_set ORDER BY 1`)
	require.NoError(t, err)
	assert.Equal(t, []string{"v1.anchor", "v1.fingerprint", "v1.s.initials.cyrl"}, versions,
		"the pilot froze another stratum")
	r := downloadReport(t, e, pilot)
	assert.Equal(t, "pilot", r.Mode)
	assert.Zero(t, r.FullRunOnHoldout)
	assert.Len(t, r.Evaluation.Strata, 1)

	var before string
	_, err = e.db.QueryOne(pg.Scan(&before), `SELECT encode(sha256, 'hex') FROM author_llm_eval_set WHERE set_version = 'v1.s.initials.cyrl'`)
	require.NoError(t, err)
	full, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModeFull, &settings, nil)
	require.NoError(t, err)
	e.evalUntilDone(full)
	var after string
	var creator int64
	_, err = e.db.QueryOne(pg.Scan(&after, &creator),
		`SELECT encode(sha256, 'hex'), created_by_run_id FROM author_llm_eval_set WHERE set_version = 'v1.s.initials.cyrl'`)
	require.NoError(t, err)
	assert.Equal(t, before, after, "the pilot stratum is the same in the full run")
	assert.Equal(t, pilot, creator)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_eval_set WHERE set_version = 'v1.canary'`))
	assert.Greater(t, e.count(`SELECT count(*) FROM author_llm_eval_set WHERE set_version LIKE 'v1.s.%'`), 1,
		"the full run froze the other strata")
}

func TestAuthorLLMEvalSetsAreDeterministic(t *testing.T) {
	digests := func() map[string]string {
		e := newLLMEnv(t)
		ingestEvalCatalogue(t, e)
		ctx := context.Background()
		settings := services.DefaultAuthorLLMEvalSettings()
		run, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModeFull, &settings, nil)
		require.NoError(t, err)
		driver := services.NewAuthorLLMEvalDriver(e.db, &e.identity, e.version, evalSizes, e.archives, nil, time.Second)
		moved, err := driver.Step(ctx)
		require.NoError(t, err)
		require.True(t, moved)
		var rows []struct {
			SetVersion string `pg:"set_version"`
			SHA        string `pg:"sha"`
		}
		_, err = e.db.Query(&rows, `SELECT set_version, encode(sha256, 'hex') AS sha FROM author_llm_eval_set`)
		require.NoError(t, err)
		out := map[string]string{}
		for _, r := range rows {
			out[r.SetVersion] = r.SHA
		}
		_ = run
		return out
	}
	first, second := digests(), digests()
	require.NotEmpty(t, first)
	assert.Equal(t, first, second, "two independent freezes of the same catalog")
}

func TestAuthorLLMEvalStopsAtTheAnchor(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	answer := byFields(distinctLabs)
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		// The second judge reads every name family first.
		if req.Model == "claude-opus-5" && !isFingerprint(req) {
			results := make([]any, 0, len(req.Items))
			for _, it := range req.Items {
				roles := []any{}
				for _, tok := range it.Tokens {
					role := "given"
					if tok.I == 0 {
						role = "family"
					}
					roles = append(roles, map[string]any{"i": tok.I, "role": role})
				}
				results = append(results, map[string]any{"id": it.ID, "form": "person",
					"roles": roles, "case_fix": []any{}})
			}
			out, _ := json.Marshal(map[string]any{"results": results})
			return fakeReply{Content: string(out)}
		}
		return answer(req)
	})
	settings := services.DefaultAuthorLLMEvalSettings()
	run, err := services.StartAuthorLLMEval(context.Background(), e.db, e.version, models.AuthorLLMEvalModeFull, &settings, nil)
	require.NoError(t, err)
	stages := e.evalUntilDone(run)
	assert.Equal(t, []string{"freeze", "fingerprint", "anchor", "report"}, stages)
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE purpose IN ('eval_judge', 'eval_candidate')`),
		"money was spent past a failed anchor")
	r := downloadReport(t, e, run)
	assert.False(t, r.Evaluation.Decision.Registrable)
	assert.True(t, strings.Contains(strings.Join(r.Evaluation.Decision.Reasons, ";"), "anchor: judge_b"),
		"reasons %v", r.Evaluation.Decision.Reasons)
}

func TestAuthorLLMEvalStopsWhenTheLabsAreTheSame(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	e.fake.setRespond(byFields(map[string]int64{"gpt-6-luna": 700, "deepseek-v4-pro": 700, "gpt-6-sol": 700,
		"claude-opus-5": 900}))
	settings := services.DefaultAuthorLLMEvalSettings()
	run, err := services.StartAuthorLLMEval(context.Background(), e.db, e.version, models.AuthorLLMEvalModeDev, &settings, nil)
	require.NoError(t, err)
	stages := e.evalUntilDone(run)
	assert.Equal(t, []string{"freeze", "fingerprint", "report"}, stages)
	r := downloadReport(t, e, run)
	assert.Contains(t, strings.Join(r.Evaluation.Decision.Reasons, ";"), "same_lab")
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE purpose = 'eval_anchor'`))
}

// A book whose text changed after the freeze no longer gives the frozen
// excerpt: its items drop out of the measurement and are counted.
func TestAuthorLLMEvalDropsItemsWhoseBookChanged(t *testing.T) {
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
	withContext := e.count(`SELECT count(*) FROM author_llm_eval_item
		WHERE set_version = 'v1.s.initials.cyrl' AND context_sha256 IS NOT NULL`)
	require.Positive(t, withContext)

	// The archive is rewritten with other body text under the same entries.
	path := filepath.Join(e.archives, "eval.zip")
	f, err := os.Create(path)
	require.NoError(t, err)
	zw := zip.NewWriter(f)
	for i, a := range evalAuthors {
		w, createErr := zw.Create(fmt.Sprintf("%d.fb2", i))
		require.NoError(t, createErr)
		book := strings.Replace(string(evalBook(i, a[0], a[1], a[2], time.Now())), "часы пробили", "колокола звонили", 1)
		_, err = w.Write([]byte(book))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	require.NoError(t, f.Close())

	plain := e.count(`SELECT count(*) FROM author_llm_eval_item
		WHERE set_version = 'v1.s.initials.cyrl' AND item_set = 'holdout' AND context_sha256 IS NULL`)

	e.evalUntilDone(run)
	r := downloadReport(t, e, run)
	assert.Equal(t, withContext, r.Dropped)
	assert.Equal(t, plain, r.Evaluation.Strata["initials.cyrl"].Holdout, "the rest is measured")
	assert.Positive(t, e.count(`SELECT count(*) FROM author_llm_job WHERE with_context AND context_state = 'unavailable'`))
}
