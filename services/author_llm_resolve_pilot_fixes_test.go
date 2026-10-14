package services_test

import (
	"archive/zip"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/services"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The resolve pilot (run 15, v0.51.0) found two defects. A burst of 503 from
// the gateway — minutes long, the provider's normal weather — exhausted
// judge_a's items in five quick attempts, a quarter of the pilot ending
// llm_judge_failed for good. And 20 agreed and confirmed verdicts failed to
// apply (23503): the LLM result took the fingerprint of the request's fields,
// and a credit whose FB2 has an empty element, or its elements in another
// order, has another one — the credit's selection could not point at it.

// rawAuthorBook is one fixture book whose <author> element is written as
// given: the element order and empty elements of real FB2 files.
func rawAuthorBook(id int, author, last string) []byte {
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description>
<title-info>
<genre>prose</genre>
<author>%[1]s</author>
<book-title>Книга номер %[2]d</book-title>
<lang>ru</lang>
</title-info>
<document-info><date>%[4]d</date><id>llm-raw-%[2]d</id></document-info>
</description>
<body><section><p>%[3]s написал эту книгу, и часы пробили тринадцать раз в тот холодный день.</p></section></body>
</FictionBook>
`, author, id, last, time.Now().Year()))
}

// ingestRawAuthors scans books whose authors are raw FB2 and runs the local
// normalizer.
func ingestRawAuthors(t *testing.T, e *llmEnv, authors map[string]string, first int) {
	t.Helper()
	path := filepath.Join(e.archives, fmt.Sprintf("raw-%d.zip", first))
	f, err := os.Create(path)
	require.NoError(t, err)
	zw := zip.NewWriter(f)
	i := 0
	for last, author := range authors {
		w, createErr := zw.Create(fmt.Sprintf("%d.fb2", first+i))
		require.NoError(t, createErr)
		_, err = w.Write(rawAuthorBook(first+i, author, last))
		require.NoError(t, err)
		i++
	}
	require.NoError(t, zw.Close())
	require.NoError(t, f.Close())
	scanArchive(t, e, path)
	drainLocal(t, e.db)
}

// The two shapes of run 15's 20 apply_failed verdicts: an empty element in
// the author (6 of them) and the last name before single-letter initials (14).
var run15Shapes = map[string]string{
	"Орлов":      `<first-name>П.</first-name><middle-name></middle-name><last-name>Орлов</last-name>`,
	"Беляев":     `<last-name>Беляев</last-name><first-name>Р</first-name><middle-name>С</middle-name>`,
	"Фролова Т.": `<first-name></first-name><last-name>Фролова Т.</last-name>`,
}

func TestAuthorLLMResolveAppliesInputsWhoseFieldsDoNotRehash(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ingestRawAuthors(t, e, run15Shapes, 300)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(run.ID)

	// The shapes are real: each is an input of the stratum, sent and
	// confirmed.
	sel := e.selections()
	for last := range run15Shapes {
		assert.Equal(t, "selected", sel[last].State, last)
		assert.Equal(t, "llm", deref(sel[last].Method), last)
	}
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND outcome_reason = 'apply_failed'`, run.ID))
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND settled_at IS NOT NULL
		AND stratum IS NULL`, run.ID), "every settled verdict names its stratum")
	// The selected LLM result is the credit's input.
	assert.Zero(t, e.count(`SELECT count(*) FROM book_contributor_credit_selection s
		JOIN contributor_normalization_result r ON r.id = s.result_id WHERE r.source_fingerprint <> s.source_fingerprint`))
}

// An input whose credits a rescan removed after its verdict was confirmed is
// settled input_gone, with its stratum — not apply_failed, and the report
// has no empty stratum.
func TestAuthorLLMResolveSettlesAGoneInput(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	var once sync.Once
	services.SetApplyHookForTest(t, func(input *llmreq.ItemInput) {
		for _, f := range input.Fields {
			if f.Text == "Соколов" {
				once.Do(func() {
					_, err := e.db.Exec(`UPDATE book_metadata_snapshot SET is_current = false WHERE id IN (
						SELECT snapshot_id FROM book_contributor_credit WHERE source_last_name = 'Соколов')`)
					require.NoError(t, err)
				})
			}
		}
	})
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(run.ID)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND outcome_reason = 'input_gone'
		AND stratum = 'initials.cyrl'`, run.ID))
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND outcome_reason = 'apply_failed'`, run.ID))
	raw, _, err := e.llmAdmin().EvalReport(ctx, run.ID, 0)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), `"":`, "an empty stratum key in the report")
	// A verdict settled before its stratum was recorded is counted under
	// unknown, never under an empty key.
	_, err = e.db.Exec(`UPDATE author_llm_verdict SET stratum = NULL WHERE run_id = ? AND outcome_reason = 'input_gone'`, run.ID)
	require.NoError(t, err)
	counts, err := database.AuthorLLMResolveCounts(ctx, e.db, run.ID)
	require.NoError(t, err)
	for _, c := range counts {
		assert.NotEmpty(t, c.Stratum)
		if deref(c.Reason) == "input_gone" {
			assert.Equal(t, database.AuthorLLMUnknownStratum, c.Stratum)
		}
	}
}

// A verdict whose application failed is counted under its input's stratum.
func TestAuthorLLMResolveApplyFailedKeepsTheStratum(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	services.PanicOnApplyForTest(t, func(last string) bool { return last == "Смирнов" })
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(run.ID)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND outcome_reason = 'apply_failed'
		AND stratum = 'initials.cyrl'`, run.ID))
}

// gatewayBurst answers byFields, but judge_a's first n calls get a 503: the
// gateway's burst of run 15, longer than the five attempts an item had.
func gatewayBurst(n int) (respond func(req *fakeRequest) fakeReply, served func() int) {
	answer := byFields(nil)
	var mu sync.Mutex
	failed := 0
	return func(req *fakeRequest) fakeReply {
			if req.Model == judgeAModel && !isFingerprint(req) {
				mu.Lock()
				defer mu.Unlock()
				if failed < n {
					failed++
					return fakeReply{Status: http.StatusServiceUnavailable, Body: `{"error":{"message":"busy"}}`}
				}
			}
			return answer(req)
		}, func() int {
			mu.Lock()
			defer mu.Unlock()
			return failed
		}
}

// judgeAModel is judge_a's model in the test configuration.
var judgeAModel = func() string {
	for _, p := range testParticipants {
		if p.Slot == llmreq.SlotJudgeA {
			return p.Model
		}
	}
	return ""
}()

func TestAuthorLLMResolveOutlastsAGatewayBurst(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	respond, failed := gatewayBurst(40)
	e.fake.setRespond(respond)
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(run.ID)
	require.Equal(t, 40, failed(), "the burst was served")
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND status = 'failed'`, run.ID))
	sel := e.selections()
	for _, last := range initialsCyrl {
		assert.Equal(t, "selected", sel[last].State, last)
	}
	// Some item outlived the old budget of five attempts.
	assert.Positive(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = 'judge_a' AND attempt_count > 5`, run.ID))
	// The participant was slowed down, not its items failed: its window
	// halved on each 503 and has not grown back yet.
	assert.Equal(t, 1, e.count(`SELECT concurrency FROM author_llm_provider_state WHERE config_version = ? AND slot = 'judge_a'`,
		e.version))
	assert.Less(t, 1, e.count(`SELECT concurrency FROM author_llm_provider_state WHERE config_version = ? AND slot = 'judge_b'`,
		e.version), "a participant that never failed kept its window")
}

// The eval's jobs have the same patience.
func TestAuthorLLMEvalOutlastsAGatewayBurst(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	respond, failed := gatewayBurst(30)
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		reply := respond(req)
		if reply.Status == 0 && isFingerprint(req) {
			reply.Prompt = distinctLabs[req.Model]
		}
		return reply
	})
	run := completedFullEval(t, e)
	require.Equal(t, 30, failed())
	assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND status = 'failed'`, run))
}

// A failure that outlasts the window is transient still: the input decides
// nothing — its review stays open and its credits in review — and the next
// run asks it again. The other outcomes stay final.
func TestAuthorLLMResolveAsksTransientFailuresAgain(t *testing.T) {
	e := newLLMEnv(t)
	e.transientWindow = 50 * time.Millisecond
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	down := true
	var mu sync.Mutex
	disagree := judgesDiffer("Петров")
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		mu.Lock()
		failing := down
		mu.Unlock()
		if failing && req.Model == judgeAModel && !isFingerprint(req) {
			lastIsLebedev := false
			for _, it := range req.Items {
				for _, tok := range it.Tokens {
					lastIsLebedev = lastIsLebedev || tok.Text == "Лебедев"
				}
			}
			if lastIsLebedev {
				return fakeReply{Status: http.StatusServiceUnavailable, Body: `{"error":{"message":"busy"}}`}
			}
		}
		return disagree(req)
	})
	pilot, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(pilot.ID)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND outcome_reason = 'llm_judge_failed'`, pilot.ID))
	sel := e.selections()
	assert.Equal(t, "review", sel["Лебедев"].State, "a transient failure decided the input")
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM contributor_review_item WHERE status = 'open'
		AND source_fingerprint IN (SELECT source_fingerprint FROM book_contributor_credit WHERE source_last_name = 'Лебедев')`))
	assert.Equal(t, "llm_judge_disagreement", deref(sel["Петров"].Reason))
	// A new book of the input goes where its local outcome sends it: the
	// transient failure decided nothing for it either.
	ingestBooks(t, e, [][3]string{{"Н.", "О.", "Лебедев"}}, 200)
	var states []string
	_, err = e.db.Query(&states, `SELECT s.state FROM book_contributor_credit c
		JOIN book_contributor_credit_selection s ON s.credit_id = c.id WHERE c.source_last_name = 'Лебедев'`)
	require.NoError(t, err)
	assert.Equal(t, []string{"review", "review"}, states)

	mu.Lock()
	down = false
	mu.Unlock()
	full, err := e.llmAdmin().Start(ctx, "resolve", "full", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(full.ID)
	sel = e.selections()
	assert.Equal(t, "selected", sel["Лебедев"].State, "the full run asked the transient input again")
	assert.Equal(t, "llm_judge_disagreement", deref(sel["Петров"].Reason), "a judge disagreement stays final")
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ?`, full.ID),
		"the full run asked only the transient input")
}

// Run 15's 118 llm_judge_failed inputs were applied as unresolved: review
// item closed by the service, credits unresolved. The next run gives them
// their review back and asks them again.
func TestAuthorLLMResolveAsksRun15JudgeFailuresAgain(t *testing.T) {
	e := newLLMEnv(t)
	e.transientWindow = 50 * time.Millisecond
	ingestEvalCatalogue(t, e)
	ctx := context.Background()
	e.completedFullEval(t, true, []llmeval.StratumDecision{passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")}, nil)
	var mu sync.Mutex
	down := true
	e.fake.setRespond(judgeADownFor("Лебедев", &mu, &down))
	pilot, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(pilot.ID)
	require.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ? AND outcome_reason = 'llm_judge_failed'`, pilot.ID))
	// Run 15's shape, as the earlier code applied the verdict: the review
	// item closed by the service without a result, every credit unresolved
	// llm_judge_failed on the local proposal.
	fp := e.fingerprintOf("Лебедев")
	_, err = e.db.Exec(`UPDATE book_contributor_credit_selection SET state = 'unresolved', basis = NULL,
			policy_version = NULL, unresolved_reason = 'llm_judge_failed', decided_at = now()
		WHERE source_fingerprint = ?`, fp)
	require.NoError(t, err)
	_, err = e.db.Exec(`UPDATE contributor_review_item SET status = 'closed', resolution = 'automatic', finished_at = now()
		WHERE source_fingerprint = ? AND status = 'open'`, fp)
	require.NoError(t, err)
	require.Equal(t, "unresolved", e.selections()["Лебедев"].State)

	mu.Lock()
	down = false
	mu.Unlock()
	full, err := e.llmAdmin().Start(ctx, "resolve", "full", e.adminUser())
	require.NoError(t, err)
	e.resolveUntilDone(full.ID)
	sel := e.selections()
	assert.Equal(t, "selected", sel["Лебедев"].State, "run 15's judge failure was not asked again")
	assert.Equal(t, "llm", deref(sel["Лебедев"].Method))
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE run_id = ?`, full.ID))
}

// judgeADownFor answers byFields, but judge_a answers 503 about the input
// with this last name while down holds.
func judgeADownFor(last string, mu *sync.Mutex, down *bool) func(req *fakeRequest) fakeReply {
	answer := byFields(nil)
	return func(req *fakeRequest) fakeReply {
		mu.Lock()
		failing := *down
		mu.Unlock()
		if failing && req.Model == judgeAModel && !isFingerprint(req) {
			for _, it := range req.Items {
				for _, tok := range it.Tokens {
					if tok.Text == last {
						return fakeReply{Status: http.StatusServiceUnavailable, Body: `{"error":{"message":"busy"}}`}
					}
				}
			}
		}
		return answer(req)
	}
}

// A lease that expired is a transient failure too: within the window the
// job goes back to the queue whatever its count, past it the count ends it.
func TestAuthorLLMExpiredLeaseRetriesWithinTheWindow(t *testing.T) {
	e := newLLMEnv(t)
	ctx := context.Background()
	e.settings.MaxAttempts = 1
	run := e.run(&database.AuthorLLMRunSpec{})
	e.reviewPair(&run, "window", givenFamily, 1)
	passChecks(t, e.worker())
	withWindow := func(at *time.Time) *database.AuthorLLMClaimOptions {
		o := e.claimOptions(llmreq.SlotProductionA, database.NewLeaseOwner(), at)
		o.TransientWindow = time.Hour
		return o
	}

	dead, _, err := database.ClaimAuthorLLMCall(ctx, e.db, withWindow(nil))
	require.NoError(t, err)
	require.NotNil(t, dead)
	later := dead.LeaseExpiresAt.Add(time.Second)
	again, _, err := database.ClaimAuthorLLMCall(ctx, e.db, withWindow(&later))
	require.NoError(t, err)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_job WHERE slot = 'production_a' AND status <> 'failed'
		AND last_error_class = 'lease_expired' AND transient_since IS NOT NULL`), "the job failed within its window")
	require.NotNil(t, again, "the job was claimed again")

	// Its second lease expires after the window: now it fails.
	past := again.LeaseExpiresAt.Add(2 * time.Hour)
	_, _, err = database.ClaimAuthorLLMCall(ctx, e.db, withWindow(&past))
	require.NoError(t, err)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_job WHERE slot = 'production_a' AND status = 'failed'
		AND last_error_class = 'lease_expired'`))
}
