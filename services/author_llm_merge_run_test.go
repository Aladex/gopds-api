package services_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authorentity"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The merge run (E4d-2) on the merge fixture: merge1's census asked every
// swap candidate and swap is registered from it, so nothing is due until a
// card changes; renewing a pair (a sample book renamed, then a build) gives
// it a new digest no eval asked, which the run asks the production pair,
// the judges where the pair agreed, and inserts through the
// compare-and-insert only when the pair agreed same and both judges
// confirmed. The guards that could merge two people are each tested here:
// an unregistered or withdrawn kind, a blocked script, a card changed since
// the freeze, a frozen card that is not the digest's, and the both-judges
// rule.

// The fills of the fixture's initials sides (seedMergePersons, 0x20 + i):
// «Лукьяненко С.» 0x28 and «Лукьяненко Сергей» 0x29.
const fillLukyanenko = 0x28

// renew renames a sample book of each pair's second side and builds: each
// pair gets a new digest no eval asked.
func (f *applyFixture) renew(fills ...byte) {
	f.c.t.Helper()
	for _, fill := range fills {
		f.c.exec(`UPDATE opds_catalog_book SET title = title || ' (2-е изд.)' WHERE id = (SELECT s.book_id
			FROM book_contributor_credit c JOIN book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
			WHERE c.source_fingerprint = ? LIMIT 1)`, bytes.Repeat([]byte{fill + 1}, 32))
	}
	f.c.build()
}

// digestOf is the stored swap candidate of the pair whose side holds fill.
func (f *applyFixture) digestOf(fill byte) []byte {
	return f.digestOfKind(fill, authorentity.CandidateSwap)
}

// digestOfKind is the stored candidate of kind whose side holds fill.
func (f *applyFixture) digestOfKind(fill byte, kind string) []byte {
	f.c.t.Helper()
	var digest []byte
	_, err := f.e.db.QueryOne(pg.Scan(&digest), `SELECT c.digest FROM author_entity_merge_candidate c
		JOIN author_entity_merge_candidate_member m ON m.digest = c.digest
		WHERE m.source_fingerprint = ? AND c.kind = ?`, bytes.Repeat([]byte{fill}, 32), kind)
	require.NoError(f.c.t, err)
	return digest
}

// due is the hex digests a merge run would ask now.
func (f *applyFixture) due() []string {
	f.c.t.Helper()
	rows, err := database.MergeCandidatesDue(context.Background(), f.e.db, f.m.version)
	require.NoError(f.c.t, err)
	out := []string{}
	for _, r := range rows {
		out = append(out, hex.EncodeToString(r.Digest))
	}
	return out
}

// startMergeRun starts a merge run.
func (f *applyFixture) startMergeRun() int64 {
	f.c.t.Helper()
	run, err := services.StartAuthorLLMMergeRun(context.Background(), f.e.db, f.m.version,
		models.AuthorLLMEvalModeFull, 0)
	require.NoError(f.c.t, err)
	return run
}

// stepMergeRun steps the merge run's driver and the merge worker until the
// run completes; enqueueOnly stops once the enqueue is done.
func (f *applyFixture) stepMergeRun(run int64, enqueueOnly bool) {
	f.c.t.Helper()
	ctx := context.Background()
	driver := services.NewAuthorLLMMergeRunDriver(f.e.db, f.m.version, time.Millisecond)
	w := f.e.mergeWorker(f.m)
	for range 300 {
		stage, status := f.e.runStage(run)
		if status == string(models.AuthorLLMRunCompleted) || (enqueueOnly && stage != "enqueue") {
			return
		}
		require.Equal(f.c.t, string(models.AuthorLLMRunRunning), status, "stopped in %s", stage)
		_, err := driver.Step(ctx)
		require.NoError(f.c.t, err)
		if !enqueueOnly {
			_, err = w.RunOnce(ctx)
			require.NoError(f.c.t, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.c.t.Fatal("the merge run did not complete")
}

// outcomes are a merge run's settled verdicts by digest: "applied", or the
// outcome reason.
func (f *applyFixture) outcomes(run int64) map[string]string {
	f.c.t.Helper()
	var rows []struct {
		Digest string `pg:"digest"`
		Reason string `pg:"reason"`
	}
	_, err := f.e.db.Query(&rows, `SELECT encode(source_fingerprint, 'hex') AS digest,
			coalesce(outcome_reason, 'applied') AS reason
		FROM author_llm_verdict WHERE run_id = ? AND settled_at IS NOT NULL`, run)
	require.NoError(f.c.t, err)
	out := map[string]string{}
	for _, r := range rows {
		out[r.Digest] = r.Reason
	}
	return out
}

// asked is the hex digests a merge run asked the production pair about.
func (f *applyFixture) asked(run int64) []string {
	f.c.t.Helper()
	var out []string
	_, err := f.e.db.Query(&out, `SELECT DISTINCT encode(source_fingerprint, 'hex') FROM author_llm_job
		WHERE run_id = ? AND purpose = 'merge_check' AND slot = 'production_a' ORDER BY 1`, run)
	require.NoError(f.c.t, err)
	return out
}

// The whole path: nothing is due after the census; a renewed pair is due,
// the run freezes its card, asks it (every job sends the frozen card) and
// inserts the decision citing its four jobs and the registration's report;
// the build merges the pair; nothing is due after. The initials candidates,
// a kind not registered, are never asked.
func TestAMergeRunAsksTheNewCandidatesAndApplies(t *testing.T) {
	f := newApplyFixture(t, mergeStub)
	registration, report := f.registerSwap()
	ctx := context.Background()
	assert.Empty(t, f.due(), "the census asked every swap candidate")
	_, err := services.StartAuthorLLMMergeRun(ctx, f.e.db, f.m.version, models.AuthorLLMEvalModeFull, 0)
	require.ErrorIs(t, err, services.ErrAuthorLLMPrerequisite)
	require.Positive(t, f.c.count(`SELECT count(*) FROM author_entity_merge_candidate WHERE kind = 'initials'`))

	f.renew(fillKing)
	digest := f.digestOf(fillKing)
	require.Equal(t, []string{hex.EncodeToString(digest)}, f.due())
	run := f.startMergeRun()
	f.stepMergeRun(run, false)

	assert.Equal(t, []string{hex.EncodeToString(digest)}, f.asked(run), "the swap pair alone")
	assert.Equal(t, map[string]string{hex.EncodeToString(digest): "applied"}, f.outcomes(run))
	assert.Equal(t, 4, f.c.count(`SELECT count(*) FROM author_llm_job j
		JOIN author_llm_merge_input i ON i.digest = j.source_fingerprint AND i.kind = 'swap' AND i.script = 'Cyrl'
		WHERE j.purpose = 'merge_check' AND j.input = i.input`), "every job sends the frozen card")
	var d struct {
		Source       string  `pg:"source"`
		Registration int64   `pg:"registration_id"`
		Report       int64   `pg:"eval_report_id"`
		Verdicts     []int64 `pg:"verdict_ids,array"`
		Jobs         []int64 `pg:"jobs,array"`
	}
	_, err = f.e.db.QueryOne(&d, `SELECT d.source, d.registration_id, d.eval_report_id, d.verdict_ids,
			(SELECT array_agg(j.id ORDER BY array_position(ARRAY['production_a', 'production_b', 'judge_a',
				'judge_b'], j.slot)) FROM author_llm_job j WHERE j.source_fingerprint = d.digest
				AND j.purpose = 'merge_check') AS jobs
		FROM author_entity_merge_decision d WHERE d.digest = ?`, digest)
	require.NoError(t, err)
	assert.Equal(t, "model", d.Source)
	assert.Equal(t, registration, d.Registration)
	assert.Equal(t, report, d.Report)
	assert.Len(t, d.Verdicts, 4)
	assert.Equal(t, d.Jobs, d.Verdicts, "the decision cites the four answers")

	f.c.build()
	assert.True(t, f.together(fillKing, fillKing+1))
	assert.False(t, f.together(fillAsimov, fillAsimov+1), "asked by the census, never applied")
	assert.Empty(t, f.due())
	raw, _, err := services.AuthorLLMResolveReportFile(ctx, f.e.db, run)
	require.NoError(t, err)
	var doc services.AuthorLLMMergeRunReportDoc
	require.NoError(t, json.Unmarshal(raw, &doc))
	assert.Equal(t, 1, doc.Strata["swap.cyrl"].Applied)
	assert.Equal(t, []int64{registration}, doc.Registrations)
}

// A blocked script's candidates and a withdrawn kind's are never asked: a
// Latin block leaves the renewed Latin pair out of the run, a withdrawal
// leaves nothing due and refuses the start.
func TestAMergeRunAsksNoBlockedScriptNorWithdrawnKind(t *testing.T) {
	f := newApplyFixture(t, mergeStub)
	registration, _ := f.registerSwap()
	ctx := context.Background()
	f.renew(fillKing, fillKingLatin)
	latin := hex.EncodeToString(f.digestOf(fillKingLatin))
	require.Contains(t, f.due(), latin)
	require.NoError(t, f.admin.BlockMergeScript(ctx, registration, authorentity.ScriptLatn, 0))
	assert.Equal(t, []string{hex.EncodeToString(f.digestOf(fillKing))}, f.due())
	run := f.startMergeRun()
	f.stepMergeRun(run, false)
	assert.Equal(t, []string{hex.EncodeToString(f.digestOf(fillKing))}, f.asked(run))

	f.renew(fillAsimov)
	require.Len(t, f.due(), 1)
	require.NoError(t, f.admin.WithdrawMergeRegistration(ctx, registration, 0))
	assert.Empty(t, f.due())
	_, err := services.StartAuthorLLMMergeRun(ctx, f.e.db, f.m.version, models.AuthorLLMEvalModeFull, 0)
	require.ErrorIs(t, err, services.ErrAuthorLLMPrerequisite)
}

// Between the enqueue and the apply: a Latin block withholds the Latin
// pair (merge_script_blocked), a card changed and built lapses its pair
// (merge_lapsed_before_apply) and leaves its new candidate due; the
// untouched pair applies. A withdrawal then withholds a later run's pair
// (merge_withdrawn). Nothing withheld or lapsed is merged.
func TestTheInsertGuardsAMergeRunsVerdicts(t *testing.T) {
	f := newApplyFixture(t, mergeStub)
	registration, _ := f.registerSwap()
	ctx := context.Background()
	f.renew(fillKing, fillKingLatin, fillAsimov)
	king, latin, asimov := f.digestOf(fillKing), f.digestOf(fillKingLatin), f.digestOf(fillAsimov)
	run := f.startMergeRun()
	f.stepMergeRun(run, true)
	require.Len(t, f.asked(run), 3)
	require.NoError(t, f.admin.BlockMergeScript(ctx, registration, authorentity.ScriptLatn, 0))
	f.renew(fillAsimov)
	f.stepMergeRun(run, false)
	assert.Equal(t, map[string]string{
		hex.EncodeToString(king):   "applied",
		hex.EncodeToString(latin):  models.AuthorLLMMergeScriptBlocked,
		hex.EncodeToString(asimov): models.AuthorLLMMergeLapsedBeforeApply,
	}, f.outcomes(run))
	assert.Equal(t, []string{hex.EncodeToString(f.digestOf(fillAsimov))}, f.due(), "the changed pair is due again")
	f.c.build()
	assert.True(t, f.together(fillKing, fillKing+1))
	assert.False(t, f.together(fillKingLatin, fillKingLatin+1))
	assert.False(t, f.together(fillAsimov, fillAsimov+1))

	next := f.startMergeRun()
	f.stepMergeRun(next, true)
	require.NoError(t, f.admin.WithdrawMergeRegistration(ctx, registration, 0))
	f.stepMergeRun(next, false)
	assert.Equal(t, map[string]string{hex.EncodeToString(f.digestOf(fillAsimov)): models.AuthorLLMMergeWithdrawn},
		f.outcomes(next))
	f.c.build()
	assert.False(t, f.together(fillAsimov, fillAsimov+1))
}

// judgedApart answers as mergeStub, except that production_b answers
// different on «Азимов», judge_b on «Брэдбери», and every participant on
// «Stephen King»: the pair disagrees on the first, the judges do not
// confirm the second, and the third is an agreed different the judges
// confirm.
func judgedApart(req *fakeRequest) fakeReply {
	reply := mergeStub(req)
	if (req.Model == "deepseek-v4-pro" && strings.Contains(req.User, "Азимов")) ||
		(req.Model == "claude-opus-5" && strings.Contains(req.User, "Брэдбери")) ||
		strings.Contains(req.User, "Stephen") {
		reply.Content = strings.ReplaceAll(reply.Content, `"`+llmreq.MergeSame+`"`, `"`+llmreq.MergeDifferent+`"`)
	}
	return reply
}

// Only a pair agreed same and confirmed by both judges is applied: a pair
// split (llm_disagreement), a judge's doubt (llm_judge_disagreement) and a
// confirmed different (merge_not_same) are not; the build's conflict rule
// reads the production pair's answers different.
func TestAMergeRunAppliesOnlyWhatBothJudgesConfirmedSame(t *testing.T) {
	f := newApplyFixture(t, mergeStub)
	f.registerSwap()
	f.renew(fillKing, fillAsimov, fillBradbury, fillKingLatin)
	f.e.fake.setRespond(judgedApart)
	run := f.startMergeRun()
	f.stepMergeRun(run, false)
	king, asimov := f.digestOf(fillKing), f.digestOf(fillAsimov)
	bradbury, latin := f.digestOf(fillBradbury), f.digestOf(fillKingLatin)
	assert.Equal(t, map[string]string{
		hex.EncodeToString(king):     "applied",
		hex.EncodeToString(asimov):   string(models.UnresolvedLLMDisagreement),
		hex.EncodeToString(bradbury): string(models.UnresolvedLLMJudgeDisagreement),
		hex.EncodeToString(latin):    models.AuthorLLMMergeNotSame,
	}, f.outcomes(run))
	assert.Equal(t, 1, f.decisions())

	notSame, err := database.LoadMergeNotSame(context.Background(), f.e.db)
	require.NoError(t, err)
	var got []string
	for _, d := range notSame {
		got = append(got, hex.EncodeToString(d[:]))
	}
	assert.Contains(t, got, hex.EncodeToString(asimov))
	assert.Contains(t, got, hex.EncodeToString(latin))
	assert.NotContains(t, got, hex.EncodeToString(bradbury), "only judge_b answered different")
	f.c.build()
	assert.True(t, f.together(fillKing, fillKing+1))
	for _, fill := range []byte{fillAsimov, fillBradbury, fillKingLatin} {
		assert.False(t, f.together(fill, fill+1))
	}
}

// A card frozen under a digest that it does not hash to — another
// question — is never asked: the enqueue records it and asks nothing.
func TestAMergeRunAsksNoFrozenCardThatIsNotItsDigests(t *testing.T) {
	f := newApplyFixture(t, mergeStub)
	f.registerSwap()
	f.renew(fillKing)
	digest := f.digestOf(fillKing)
	other, err := json.Marshal(mergeQuestion("Стивен Кинг", "Кинг Стивен"))
	require.NoError(t, err)
	f.c.exec(`INSERT INTO author_llm_merge_input (digest, kind, script, input) VALUES (?, 'swap', 'Cyrl', ?::jsonb)`,
		digest, string(other))
	run := f.startMergeRun()
	f.stepMergeRun(run, false)
	assert.Empty(t, f.asked(run))
	assert.Equal(t, 1, f.c.count(`SELECT count(*) FROM author_llm_eval_skip WHERE reason = 'card_mismatch'`))
	assert.Zero(t, f.decisions())
}

// The initials candidates are due once initials is registered: the
// registration alone opens the kind.
func TestAMergeRunAsksAKindOnceItIsRegistered(t *testing.T) {
	f := newApplyFixture(t, mergeStub)
	f.registerSwap()
	assert.Empty(t, f.due())
	f.passReport(authorentity.CandidateInitials, database.MergeGateJudgeConsensus)
	_, err := f.admin.RegisterMergeKind(context.Background(), f.run, authorentity.CandidateInitials, 0)
	require.NoError(t, err)
	assert.Contains(t, f.due(), hex.EncodeToString(f.digestOfKind(fillLukyanenko, authorentity.CandidateInitials)))
}

// A build pending when the run's verdicts are ready holds them: nothing is
// inserted or settled, the run waits; once the build completed, the next
// step applies them.
func TestAMergeRunWaitsOutABuild(t *testing.T) {
	f := newApplyFixture(t, mergeStub)
	f.registerSwap()
	ctx := context.Background()
	f.renew(fillKing)
	run := f.startMergeRun()
	build, err := f.admin.Start(ctx, 0)
	require.NoError(t, err)
	driver := services.NewAuthorLLMMergeRunDriver(f.e.db, f.m.version, time.Millisecond)
	w := f.e.mergeWorker(f.m)
	for range 100 {
		_, err = driver.Step(ctx)
		require.NoError(t, err)
		_, err = w.RunOnce(ctx)
		require.NoError(t, err)
	}
	stage, status := f.e.runStage(run)
	assert.Equal(t, "resolve", stage)
	assert.Equal(t, string(models.AuthorLLMRunRunning), status)
	assert.Equal(t, 1, f.c.count(`SELECT count(*) FROM author_llm_verdict WHERE purpose = 'merge_check'
		AND confirmed_at IS NOT NULL AND settled_at IS NULL`), "the confirmed verdict waits")
	assert.Zero(t, f.decisions())

	runToCompletion(t, services.NewAuthorEntityBuildLoop(f.e.db, services.AuthorEntityBuildConfig{}), f.e.db, build.ID)
	f.stepMergeRun(run, false)
	assert.Equal(t, map[string]string{hex.EncodeToString(f.digestOf(fillKing)): "applied"}, f.outcomes(run))
}

// An agreed merge verdict waits for both judges: while they cannot answer
// (paused), it is neither confirmed nor settled; once they answer, it
// applies.
func TestAnAgreedMergeVerdictWaitsForTheJudges(t *testing.T) {
	f := newApplyFixture(t, mergeStub)
	f.registerSwap()
	ctx := context.Background()
	f.renew(fillKing)
	run := f.startMergeRun()
	f.e.exec(`UPDATE author_llm_provider_state SET paused_reason = 'auth', paused_at = now()
		WHERE config_version = ? AND slot IN ('judge_a', 'judge_b')`, f.m.version)
	driver := services.NewAuthorLLMMergeRunDriver(f.e.db, f.m.version, time.Millisecond)
	w := f.e.mergeWorker(f.m)
	for range 100 {
		_, err := driver.Step(ctx)
		require.NoError(t, err)
		_, err = w.RunOnce(ctx)
		require.NoError(t, err)
	}
	assert.Equal(t, 1, f.c.count(`SELECT count(*) FROM author_llm_verdict WHERE purpose = 'merge_check'
		AND verdict = 'agree' AND confirmed_at IS NULL AND settled_at IS NULL`), "the agreed verdict waits")

	f.e.exec(`UPDATE author_llm_provider_state SET paused_reason = NULL, paused_at = NULL
		WHERE config_version = ? AND slot IN ('judge_a', 'judge_b')`, f.m.version)
	f.stepMergeRun(run, false)
	assert.Equal(t, map[string]string{hex.EncodeToString(f.digestOf(fillKing)): "applied"}, f.outcomes(run))
}

// The enqueue's insert reads the due rule again: between the due read (and
// the card freeze) and the insert, a Latin block withholds the Latin pair
// and a build that changed a card replaces its census row — neither is
// asked, the untouched pair is. A withdrawal at that moment leaves a later
// run with no question at all.
func TestMergeEnqueueRechecksDueAtInsert(t *testing.T) {
	f := newApplyFixture(t, mergeStub)
	registration, _ := f.registerSwap()
	ctx := context.Background()
	f.renew(fillKing, fillKingLatin, fillAsimov)
	king, latin, asimov := f.digestOf(fillKing), f.digestOf(fillKingLatin), f.digestOf(fillAsimov)
	require.ElementsMatch(t, []string{hex.EncodeToString(king), hex.EncodeToString(latin),
		hex.EncodeToString(asimov)}, f.due())

	once := false
	services.SetMergeEnqueueHookForTest(t, func() {
		if once {
			return
		}
		once = true
		require.NoError(t, f.admin.BlockMergeScript(ctx, registration, authorentity.ScriptLatn, 0))
		f.renew(fillAsimov)
	})
	run := f.startMergeRun()
	f.stepMergeRun(run, true)
	require.True(t, once)
	assert.Equal(t, []string{hex.EncodeToString(king)}, f.asked(run))
	f.stepMergeRun(run, false)

	renewed := f.digestOf(fillAsimov)
	require.NotEqual(t, asimov, renewed)
	require.Equal(t, []string{hex.EncodeToString(renewed)}, f.due())
	services.SetMergeEnqueueHookForTest(t, func() {
		require.NoError(t, f.admin.WithdrawMergeRegistration(ctx, registration, 0))
	})
	run = f.startMergeRun()
	f.stepMergeRun(run, true)
	assert.Empty(t, f.asked(run), "withdrawn after the due read")
	assert.Zero(t, f.c.count(fmt.Sprintf(`SELECT count(*) FROM author_llm_job WHERE run_id = %d AND purpose = 'merge_check'`, run)))
}
