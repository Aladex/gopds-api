package services_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Round 2 of the phase-6 review: the LLM's decision outranks the local one
// under concurrency (B1), the excerpt fallback is held to arm A's own gate
// (B2, B3), the arms never share a call, and a failure after an item's
// first write rolls that item back whole.

// confirmWithoutApplying enqueues the run and answers, judges and confirms
// every input, applying nothing yet.
func (e *llmEnv) confirmWithoutApplying(run int64, d *services.AuthorLLMResolveDriver, w *services.AuthorLLMWorker) {
	e.t.Helper()
	ctx := context.Background()
	_, err := d.Step(ctx) // enqueue
	require.NoError(e.t, err)
	for range 300 {
		_, err = w.RunOnce(ctx)
		require.NoError(e.t, err)
		p, progErr := database.AuthorLLMResolveRunProgress(ctx, e.db, run)
		require.NoError(e.t, progErr)
		if p.OpenJobs == 0 && p.Unconfirmed == 0 {
			require.Positive(e.t, p.Unsettled)
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	e.t.Fatal("the run's verdicts were never all confirmed")
}

func (e *llmEnv) fingerprintOf(last string) []byte {
	e.t.Helper()
	var fp []byte
	_, err := e.db.QueryOne(pg.Scan(&fp), `SELECT source_fingerprint FROM book_contributor_credit
		WHERE source_last_name = ? LIMIT 1`, last)
	require.NoError(e.t, err)
	return fp
}

// B1: a new book joins an input whose verdict is confirmed; the local stream
// reconciles it and finds no LLM outcome yet — and at that moment the
// resolve run applies the verdict. Whatever the order, the LLM's selection
// stands on the old and the new credit, and no review item reopens.
func TestAuthorLLMResolveReconcileCannotOverwriteAConcurrentApply(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	d, w := e.resolveDriver(), e.worker()
	e.confirmWithoutApplying(run.ID, d, w)

	// The new book of «А. Б. Петров»: its credit has no selection yet.
	path := filepath.Join(e.archives, "late.zip")
	writeEvalArchive(t, path, [][3]string{{"А.", "Б.", "Петров"}}, 300)
	scanArchive(t, e, path)
	fp := e.fingerprintOf("Петров")

	applied := make(chan error, 1)
	var fired, done atomic.Bool
	database.TestHookAfterLLMOutcomeRead = func(got []byte, found bool) {
		if found || !bytes.Equal(got, fp) || fired.Swap(true) {
			return
		}
		// The reconcile read no LLM outcome: the resolve run applies now,
		// and commits if nothing holds it back.
		go func() {
			_, stepErr := d.Step(ctx)
			applied <- stepErr
		}()
		select {
		case stepErr := <-applied:
			require.NoError(t, stepErr)
			done.Store(true)
		case <-time.After(time.Second):
		}
	}
	t.Cleanup(func() { database.TestHookAfterLLMOutcomeRead = nil })
	drainLocal(t, e.db)
	require.True(t, fired.Load(), "the reconcile never read the LLM outcome")
	if !done.Load() {
		select {
		case stepErr := <-applied:
			require.NoError(t, stepErr)
		case <-time.After(30 * time.Second):
			t.Fatal("the apply never finished")
		}
	}

	var rows []struct {
		State  string  `pg:"state"`
		Method *string `pg:"method"`
	}
	_, err = e.db.Query(&rows, `SELECT s.state, r.method FROM book_contributor_credit c
		JOIN book_contributor_credit_selection s ON s.credit_id = c.id
		LEFT JOIN contributor_normalization_result r ON r.id = s.result_id
		WHERE c.source_last_name = 'Петров'`)
	require.NoError(t, err)
	require.Len(t, rows, 2, "the old and the new credit")
	for _, r := range rows {
		assert.Equal(t, "selected", r.State)
		assert.Equal(t, "llm", deref(r.Method))
	}
	assert.Zero(t, e.count(`SELECT count(*) FROM contributor_review_item WHERE status = 'open' AND source_fingerprint = ?`, fp),
		"a review item reopened")
}

// B3: one production participant reads the excerpt, the other cannot (a
// transient archive failure), both agree and the judges confirm. The stratum
// passed on arm B only: whichever participant went without the excerpt, the
// answer is not applied.
func TestAuthorLLMResolveOneProductionExcerptUnavailableNeedsArmA(t *testing.T) {
	for name, order := range map[string][2]llmreq.Slot{
		"production_b unavailable": {llmreq.SlotProductionA, llmreq.SlotProductionB},
		"production_a unavailable": {llmreq.SlotProductionB, llmreq.SlotProductionA},
	} {
		t.Run(name, func(t *testing.T) {
			e := newLLMEnv(t)
			ingestEvalCatalogue(t, e)
			ctx := context.Background()
			e.completedFullEval(t, true,
				[]llmeval.StratumDecision{passedStratum("initials.cyrl", "B", 1, "confirm", "restructure")},
				map[string]map[string]llmeval.ComboMetrics{"initials.cyrl": {"A1": {Passed: false},
					"B1": {Passed: true, AllowedTiers: []string{"confirm", "restructure"}}}})
			run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
			require.NoError(t, err)
			d, w := e.resolveDriver(), e.worker()
			_, err = d.Step(ctx) // enqueue
			require.NoError(t, err)
			drainSlot := func(slot llmreq.Slot) {
				for range 100 {
					claim, _, claimErr := w.Claim(ctx, slot)
					require.NoError(t, claimErr)
					if claim == nil {
						return
					}
					require.NoError(t, w.Process(ctx, slot, claim))
				}
			}
			archive := filepath.Join(e.archives, "eval.zip")
			drainSlot(order[0])
			require.NoError(t, os.Rename(archive, archive+".away"))
			drainSlot(order[1])
			require.NoError(t, os.Rename(archive+".away", archive))
			e.resolveUntilDone(run.ID)

			assert.Equal(t, 7, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = ?
				AND context_state = 'attached'`, run.ID, string(order[0])), "the evidence of each participant stays")
			assert.Equal(t, 7, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = ?
				AND context_state = 'unavailable'`, run.ID, string(order[1])))
			for _, last := range initialsCyrl {
				s := e.selections()[last]
				assert.Equal(t, "unresolved", s.State, last)
				assert.Equal(t, "llm_context_unavailable", deref(s.Reason), last)
			}
			assert.Zero(t, e.count(`SELECT count(*) FROM contributor_normalization_result WHERE method = 'llm'`))
		})
	}
}

// Follow-up 1: each input is asked in its stratum's arm, and an arm A item
// never shares a call with an arm B item.
func TestAuthorLLMResolveArmsNeverShareACall(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{
		passedStratum("initials.cyrl", "A", 2, "confirm", "restructure"),
		passedStratum("single_field.cyrl", "B", 2, "confirm", "restructure"),
	}, nil)
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(run.ID)
	assert.Positive(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND arm = 'A'
		AND slot IN ('production_a', 'production_b')`, run.ID))
	assert.Positive(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND arm = 'B'
		AND slot IN ('production_a', 'production_b')`, run.ID))
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND arm IS NULL
		AND slot IN ('production_a', 'production_b')`, run.ID), "a production job without its arm")
	assert.Zero(t, e.count(`SELECT count(*) FROM (
		SELECT a.call_id FROM author_llm_attempt a JOIN author_llm_job j ON j.id = a.job_id
		WHERE j.run_id = ? GROUP BY a.call_id HAVING count(DISTINCT coalesce(j.arm, '')) > 1) mixed`, run.ID),
		"a call carried both arms")
}

// Follow-up 3: a constraint failure after an item's first write rolls the
// item back whole — its selections, their audit, its review item, its LLM
// result — settles it apply_failed, and the next items are applied.
func TestAuthorLLMResolveConstraintFailureRollsBackOneItem(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ingestBooks(t, e, [][3]string{{"Ж.", "З.", "Смирнов"}}, 400) // a second credit of the input
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	var credits []int64
	_, err := e.db.Query(&credits, `SELECT id FROM book_contributor_credit WHERE source_last_name = 'Смирнов' ORDER BY id`)
	require.NoError(t, err)
	require.Len(t, credits, 2)
	fp := e.fingerprintOf("Смирнов")
	// The second credit's selection is refused: the first one is written
	// by then.
	_, err = e.db.Exec(`
		CREATE FUNCTION test_refuse_second() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.credit_id = ` + itoa(int(credits[1])) + ` AND NEW.state = 'selected' THEN
				RAISE EXCEPTION 'refused by the test' USING ERRCODE = 'check_violation';
			END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER test_refuse_second BEFORE INSERT OR UPDATE ON book_contributor_credit_selection
			FOR EACH ROW EXECUTE FUNCTION test_refuse_second();`)
	require.NoError(t, err)
	audit := func() int {
		return e.count(`SELECT count(*) FROM book_contributor_credit_selection_audit WHERE credit_id IN (?)`,
			pg.In(credits))
	}
	auditBefore := audit()

	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(run.ID)

	var states []string
	_, err = e.db.Query(&states, `SELECT state FROM book_contributor_credit_selection WHERE credit_id IN (?)
		ORDER BY credit_id`, pg.In(credits))
	require.NoError(t, err)
	assert.Equal(t, []string{"review", "review"}, states, "the first credit's write was rolled back")
	assert.Equal(t, auditBefore, audit(), "no audit of a rolled-back write")
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM contributor_review_item WHERE status = 'open'
		AND source_fingerprint = ?`, fp), "the review item stays open")
	assert.Zero(t, e.count(`SELECT count(*) FROM contributor_normalization_result WHERE method = 'llm'
		AND source_fingerprint = ?`, fp), "no LLM result of the rolled-back item")
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND outcome_reason = 'apply_failed'`, run.ID))
	for _, last := range initialsCyrl {
		if last != "Смирнов" {
			assert.Equal(t, "selected", e.selections()[last].State, last)
		}
	}
}

// B1, the other decision: an input the LLM layer left unresolved stays
// unresolved for a new book too — with the LLM's reason, and no review item
// reopened.
func TestAuthorLLMResolveUnresolvedInputStaysUnresolvedForANewBook(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	e.fake.setRespond(judgesDiffer("Петров"))
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(run.ID)
	ingestBooks(t, e, [][3]string{{"А.", "Б.", "Петров"}}, 500)
	var reasons []string
	_, err = e.db.Query(&reasons, `SELECT coalesce(s.unresolved_reason, s.state) FROM book_contributor_credit c
		JOIN book_contributor_credit_selection s ON s.credit_id = c.id WHERE c.source_last_name = 'Петров'`)
	require.NoError(t, err)
	assert.Equal(t, []string{"llm_judge_disagreement", "llm_judge_disagreement"}, reasons)
	assert.Zero(t, e.count(`SELECT count(*) FROM contributor_review_item WHERE status = 'open' AND source_fingerprint = ?`,
		e.fingerprintOf("Петров")))
}
