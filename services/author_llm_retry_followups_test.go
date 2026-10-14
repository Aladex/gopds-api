package services_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Follow-ups of the pilot fixes' review (fe41f09): the inputs run 15 lost to
// apply_failed are asked again like the transient ones; reopening an earlier
// run's transient input is scoped to the run's plan and fenced by the run's
// state; and a second enqueue of the same run never starts a job of it over.

// judgeADownForTokens answers byFields, but judge_a answers 503 about every
// input carrying one of tokens while down holds.
func judgeADownForTokens(tokens []string, mu *sync.Mutex, down *bool) func(req *fakeRequest) fakeReply {
	answer := byFields(nil)
	return func(req *fakeRequest) fakeReply {
		mu.Lock()
		failing := *down
		mu.Unlock()
		if failing && req.Model == judgeAModel && !isFingerprint(req) {
			for _, it := range req.Items {
				for _, tok := range it.Tokens {
					if contains(tokens, tok.Text) {
						return fakeReply{Status: http.StatusServiceUnavailable, Body: `{"error":{"message":"busy"}}`}
					}
				}
			}
		}
		return answer(req)
	}
}

// applyAsBefore writes, over an input's llm_judge_failed verdict, what
// resolve runs before fe41f09 wrote (run 15's shape): its open review item
// closed by the service without a result, every credit unresolved
// llm_judge_failed.
func (e *llmEnv) applyAsBefore(t *testing.T, last string) {
	const reason = "llm_judge_failed"
	t.Helper()
	fp := e.fingerprintOf(last)
	_, err := e.db.Exec(`UPDATE book_contributor_credit_selection SET state = 'unresolved', basis = NULL,
			policy_version = NULL, unresolved_reason = ?1, decided_at = now()
		WHERE source_fingerprint = ?0`, fp, reason)
	require.NoError(t, err)
	_, err = e.db.Exec(`UPDATE contributor_review_item SET status = 'closed', resolution = 'automatic', finished_at = now()
		WHERE source_fingerprint = ? AND status = 'open'`, fp)
	require.NoError(t, err)
}

// selectionsOf are the selection states of the credits with this last name.
func (e *llmEnv) selectionsOf(t *testing.T, last string) []string {
	t.Helper()
	var states []string
	_, err := e.db.Query(&states, `SELECT s.state || ':' || coalesce(s.unresolved_reason, '-') FROM book_contributor_credit c
		JOIN book_contributor_credit_selection s ON s.credit_id = c.id WHERE c.source_last_name = ? ORDER BY c.id`, last)
	require.NoError(t, err)
	return states
}

// 1. Run 15's 20 apply_failed were the fingerprint defect: their request
// fields do not rebuild the credit's fingerprint. Such an input is asked
// again by the next run and applied once; an apply_failed of any other cause
// stays final, and so does an admin's decision on a defect-class input.
func TestAuthorLLMResolveAsksTheFingerprintDefectAgain(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ingestRawAuthors(t, e, run15Shapes, 300)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	// The pilot fails to apply two inputs of the defect class and one whose
	// fields rebuild its fingerprint (another cause).
	services.PanicOnApplyForTest(t, func(last string) bool {
		return last == "Беляев" || last == "Орлов" || last == "Смирнов"
	})
	pilot, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(pilot.ID)
	require.Equal(t, 3, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND outcome_reason = 'apply_failed'`, pilot.ID))
	// An admin decides one of the defect-class inputs before the full run.
	_, err = e.db.Exec(`UPDATE book_contributor_credit_selection SET state = 'unresolved', unresolved_reason = 'normalizer_failed',
			decided_by_user_id = ?1, decided_at = now()
		WHERE source_fingerprint = ?0`, e.fingerprintOf("Орлов"), e.adminUser())
	require.NoError(t, err)
	_, err = e.db.Exec(`UPDATE contributor_review_item SET status = 'closed', resolution = 'left_unresolved',
			resolved_by_user_id = ?1, finished_at = now()
		WHERE source_fingerprint = ?0 AND status = 'open'`, e.fingerprintOf("Орлов"), e.adminUser())
	require.NoError(t, err)

	services.PanicOnApplyForTest(t, func(string) bool { return false })
	full, err := e.llmAdmin().Start(ctx, "resolve", "full", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(full.ID)

	sel := e.selections()
	assert.Equal(t, "selected", sel["Беляев"].State, "the fingerprint defect was not asked again")
	assert.Equal(t, "llm", deref(sel["Беляев"].Method))
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE source_fingerprint = ?
		AND result_id IS NOT NULL`, e.fingerprintOf("Беляев")), "applied once")
	assert.Equal(t, "review", sel["Смирнов"].State, "an apply_failed of another cause was asked again")
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND source_fingerprint = ?`,
		full.ID, e.fingerprintOf("Смирнов")))
	assert.Equal(t, []string{"unresolved:normalizer_failed"}, e.selectionsOf(t, "Орлов"), "the admin's decision was overridden")
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND source_fingerprint = ?`,
		full.ID, e.fingerprintOf("Орлов")))
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ?`, full.ID),
		"the full run asked more than the defect-class input")
}

// 3. Reopening an earlier run's transient input follows the run's plan: an
// input of a stratum the run does not plan stays as it is.
func TestAuthorLLMResolveReopensOnlyPlannedTransientInputs(t *testing.T) {
	e := newLLMEnv(t)
	e.transientWindow = 50 * time.Millisecond
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	evalRun := e.completedFullEval(t, true, []llmeval.StratumDecision{
		passedStratum("initials.cyrl", "A", 1, "confirm", "restructure"),
		passedStratum("single_field.cyrl", "A", 1, "confirm", "restructure"),
	}, nil)
	var mu sync.Mutex
	down := true
	e.fake.setRespond(judgeADownForTokens([]string{"Лебедев", "Синь"}, &mu, &down))
	pilot, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(pilot.ID)
	require.Equal(t, 2, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND outcome_reason = 'llm_judge_failed'`, pilot.ID))
	e.applyAsBefore(t, "Лебедев")
	e.applyAsBefore(t, "Лу Синь")
	// The next report withdraws single_field.cyrl.
	e.publishReport(t, evalRun, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")})

	mu.Lock()
	down = false
	mu.Unlock()
	full, err := e.llmAdmin().Start(ctx, "resolve", "full", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(full.ID)
	assert.Equal(t, "selected", e.selections()["Лебедев"].State)
	assert.Equal(t, []string{"unresolved:llm_judge_failed"}, e.selectionsOf(t, "Лу Синь"),
		"an input of an unplanned stratum was reopened")
	assert.Zero(t, e.count(`SELECT count(*) FROM contributor_review_item WHERE status = 'open'
		AND source_fingerprint = ?`, e.fingerprintOf("Лу Синь")))
}

// 3. ...and to the pilot's sample: a pilot of one input reopens at most the
// one it asks.
func TestAuthorLLMResolveReopensOnlyThePilotSample(t *testing.T) {
	e := newLLMEnv(t)
	e.transientWindow = 50 * time.Millisecond
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	var mu sync.Mutex
	down := true
	e.fake.setRespond(judgeADownForTokens([]string{"Лебедев", "Попов"}, &mu, &down))
	first, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(first.ID)
	e.applyAsBefore(t, "Лебедев")
	e.applyAsBefore(t, "Попов")
	mu.Lock()
	down = false
	mu.Unlock()

	second, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	d := services.NewAuthorLLMResolveDriver(e.db, &e.identity, e.version, services.AuthorLLMResolveSizes{Pilot: 1}, time.Second)
	_, err = d.Step(ctx)
	require.NoError(t, err)
	asked := e.count(`SELECT count(DISTINCT source_fingerprint) FROM author_llm_job WHERE run_id = ?`, second.ID)
	assert.Equal(t, 1, asked, "the pilot of one asked another number of inputs")
	reopened := e.count(`SELECT count(*) FROM contributor_review_item WHERE status = 'open'
		AND source_fingerprint IN (?, ?)`, e.fingerprintOf("Лебедев"), e.fingerprintOf("Попов"))
	assert.Equal(t, 1, reopened, "the pilot reopened an input outside its sample")
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM contributor_review_item i JOIN author_llm_job j
		ON j.source_fingerprint = i.source_fingerprint AND j.run_id = ?
		WHERE i.status = 'open' AND j.slot = 'production_a'`, second.ID), "the reopened input is not the one asked")
}

// 3. A run paused while it enqueues reopens nothing and asks nothing; after
// the resume it does both.
func TestAuthorLLMResolvePausedDuringEnqueueReopensNothing(t *testing.T) {
	e := newLLMEnv(t)
	e.transientWindow = 50 * time.Millisecond
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	var mu sync.Mutex
	down := true
	e.fake.setRespond(judgeADownForTokens([]string{"Лебедев"}, &mu, &down))
	pilot, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(pilot.ID)
	e.applyAsBefore(t, "Лебедев")
	mu.Lock()
	down = false
	mu.Unlock()

	full, err := e.llmAdmin().Start(ctx, "resolve", "full", e.adminUser())
	require.NoError(t, err)
	paused := false
	services.SetResolveEnqueuePlannedHookForTest(t, func() {
		if !paused {
			paused = true
			require.NoError(t, database.PauseAuthorLLMRun(ctx, e.db, full.ID, models.AuthorLLMRunPausedAdmin))
		}
	})
	_, err = e.resolveDriver().Step(ctx)
	require.NoError(t, err)
	require.True(t, paused)
	assert.Equal(t, []string{"unresolved:llm_judge_failed"}, e.selectionsOf(t, "Лебедев"), "a paused run reopened an input")
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ?`, full.ID), "a paused run asked")
	stage, _ := e.runStage(full.ID)
	assert.Equal(t, "enqueue", stage)

	require.NoError(t, database.ResumeAuthorLLMRun(ctx, e.db, full.ID))
	e.resolveUntilDone(full.ID)
	assert.Equal(t, "selected", e.selections()["Лебедев"].State)
}

// 4. Two drivers enqueue the same run: the second never starts a job of the
// run over — here production_a's answer, given between the two enqueues
// while production_b still retries.
func TestAuthorLLMResolveSecondEnqueueKeepsTheRunsJobs(t *testing.T) {
	e := newLLMEnv(t)
	e.transientWindow = 50 * time.Millisecond
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	var mu sync.Mutex
	down := true
	e.fake.setRespond(judgeADownForTokens([]string{"Лебедев"}, &mu, &down))
	pilot, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(pilot.ID)
	require.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND outcome_reason = 'llm_judge_failed'`, pilot.ID))

	// From now on production_b cannot answer about the input: the pair's
	// verdict waits, the transient verdict stays the input's latest.
	answer := byFields(nil)
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		if req.Model == productionBModel && !isFingerprint(req) {
			for _, it := range req.Items {
				for _, tok := range it.Tokens {
					if tok.Text == "Лебедев" {
						return fakeReply{Status: http.StatusServiceUnavailable, Body: `{"error":{"message":"busy"}}`}
					}
				}
			}
		}
		return answer(req)
	})
	e.transientWindow = time.Hour
	full, err := e.llmAdmin().Start(ctx, "resolve", "full", e.adminUser())
	require.NoError(t, err)

	// Driver B reads the enqueue stage and waits in its enqueue; driver A
	// enqueues, and the worker answers what it can.
	release, entered := make(chan struct{}), make(chan struct{})
	var calls sync.Mutex
	n := 0
	services.SetResolveEnqueuePlannedHookForTest(t, func() {
		calls.Lock()
		n++
		first := n == 1
		calls.Unlock()
		if first {
			close(entered)
			<-release
		}
	})
	done := make(chan error, 1)
	go func() {
		_, stepErr := e.resolveDriver().Step(ctx)
		done <- stepErr
	}()
	<-entered
	_, err = e.resolveDriver().Step(ctx)
	require.NoError(t, err)
	w := e.worker()
	for range 20 {
		_, err = w.RunOnce(ctx)
		require.NoError(t, err)
		time.Sleep(5 * time.Millisecond)
	}
	fp := e.fingerprintOf("Лебедев")
	require.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND source_fingerprint = ?
		AND slot = 'production_a' AND status = 'answered'`, full.ID, fp), "production_a did not answer")
	var attempts int
	_, err = e.db.QueryOne(pg.Scan(&attempts), `SELECT attempt_count FROM author_llm_job WHERE source_fingerprint = ?
		AND slot = 'production_a' AND purpose = 'review'`, fp)
	require.NoError(t, err)

	close(release)
	require.NoError(t, <-done)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND source_fingerprint = ?
		AND slot = 'production_a' AND status = 'answered'`, full.ID, fp), "the second enqueue started the answered job over")
	assert.Equal(t, attempts, e.count(`SELECT attempt_count FROM author_llm_job WHERE source_fingerprint = ?
		AND slot = 'production_a' AND purpose = 'review'`, fp))
}

// productionBModel is production_b's model in the test configuration.
var productionBModel = func() string {
	for _, p := range testParticipants {
		if p.Slot == llmreq.SlotProductionB {
			return p.Model
		}
	}
	return ""
}()

// 3. An admin who decides the input between the enqueue's plan and its
// reopening keeps the decision: the input is neither reopened nor asked.
func TestAuthorLLMResolveReopenRechecksAnAdminDecision(t *testing.T) {
	e := newLLMEnv(t)
	e.transientWindow = 50 * time.Millisecond
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	var mu sync.Mutex
	down := true
	e.fake.setRespond(judgeADownForTokens([]string{"Лебедев"}, &mu, &down))
	pilot, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(pilot.ID)
	e.applyAsBefore(t, "Лебедев")
	mu.Lock()
	down = false
	mu.Unlock()

	full, err := e.llmAdmin().Start(ctx, "resolve", "full", e.adminUser())
	require.NoError(t, err)
	fp := e.fingerprintOf("Лебедев")
	decided := false
	services.SetResolveEnqueuePlannedHookForTest(t, func() {
		if !decided {
			decided = true
			_, hookErr := e.db.Exec(`UPDATE book_contributor_credit_selection SET unresolved_reason = 'normalizer_failed',
				decided_by_user_id = ?1, decided_at = now() WHERE source_fingerprint = ?0`, fp, e.adminUser())
			require.NoError(t, hookErr)
		}
	})
	e.resolveUntilDone(full.ID)
	require.True(t, decided)
	assert.Equal(t, []string{"unresolved:normalizer_failed"}, e.selectionsOf(t, "Лебедев"))
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND source_fingerprint = ?`, full.ID, fp),
		"an input the admin decided was asked")
}
