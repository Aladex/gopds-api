package services_test

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authorentity"
	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Registration of a merge stratum and the apply of its eval (E4d): a swap
// census asked every swap candidate with all four participants; a report
// that passed swap registers it, and the apply inserts a decision for each
// item the pair agreed same and both judges confirmed; the next build
// merges them. A script block and a withdrawal undo the merges at the next
// build, with the absorbed IDs revived.

// The fills of the merge fixture's swap sides (seedMergePersons, 0x20 + i).
const (
	fillKing      = 0x20 // «Кинг Стивен», two books; «Стивен Кинг» is 0x21
	fillAsimov    = 0x22
	fillBradbury  = 0x24
	fillKingLatin = 0x26 // «King Stephen»; «Stephen King» is 0x27
)

// applyFixture is the merge fixture built, a merge configuration and a
// completed swap census on it.
type applyFixture struct {
	e     *llmEnv
	m     *mergeConfig
	c     *entityCatalog
	run   int64
	admin *services.AuthorEntityAdmin
}

// newApplyFixture builds the fixture and runs the swap census with respond
// as every participant's answers.
func newApplyFixture(t *testing.T, respond func(*fakeRequest) fakeReply) *applyFixture {
	t.Helper()
	e := newLLMEnv(t)
	c := &entityCatalog{t: t, db: e.db, next: 1000}
	c.exec(`INSERT INTO author_acceptance_class
		(policy_version, decision_class, script, config_version, evidence_report_sha256, evidence_ref, source)
		VALUES ('4', 'structured_person', 'Cyrl', 'normalizer-v1', ?, 'internal/authornorm/policy_evidence/fixture.json',
		 'shipped')`, bytes.Repeat([]byte{0xee}, 32))
	c.seedMergeFixture()
	c.build()
	m := e.mergeConfig()
	e.fake.setRespond(respond)
	run, err := services.StartAuthorLLMMergeEval(context.Background(), e.db, m.version,
		models.AuthorLLMEvalModeMerge1, nil)
	require.NoError(t, err)
	stepMergeEval(t, e, m, run, stageEvalSizes, false)
	return &applyFixture{e: e, m: m, c: c, run: run, admin: services.NewAuthorEntityAdmin(e.db)}
}

// passReport stores a new version of the census's report whose decision
// passed kind under gate — a stub of the gate on the run's own answers (the
// fixture is far too small for the real bound) — and returns its id.
func (f *applyFixture) passReport(kind, gate string) int64 {
	f.c.t.Helper()
	latest, err := database.GetAuthorLLMEvalReport(context.Background(), f.e.db, f.run)
	require.NoError(f.c.t, err)
	var doc map[string]any
	require.NoError(f.c.t, json.Unmarshal([]byte(latest.Report), &doc))
	doc["gate"] = gate
	evaluation := doc["evaluation"].(map[string]any)
	evaluation["decision"] = map[string]any{"registrable": true,
		"strata": []any{map[string]any{"stratum": kind, "passed": true}}}
	raw, err := json.Marshal(doc)
	require.NoError(f.c.t, err)
	var id int64
	_, err = f.e.db.QueryOne(pg.Scan(&id), `INSERT INTO author_llm_eval_report (run_id, config_version, version,
			report, report_sha256)
		SELECT run_id, config_version, max(version) + 1, ?::jsonb, sha256(convert_to(?::jsonb::text, 'UTF8'))
		FROM author_llm_eval_report WHERE run_id = ? GROUP BY run_id, config_version RETURNING id`,
		string(raw), string(raw), f.run)
	require.NoError(f.c.t, err)
	return id
}

// registerSwap registers swap from a passing report and returns the
// registration and the report.
func (f *applyFixture) registerSwap() (registration, report int64) {
	f.c.t.Helper()
	report = f.passReport(authorentity.CandidateSwap, database.MergeGateJudgeConsensus)
	registration, err := f.admin.RegisterMergeKind(context.Background(), f.run, authorentity.CandidateSwap, 0)
	require.NoError(f.c.t, err)
	return registration, report
}

// decisions counts the live decisions.
func (f *applyFixture) decisions() int {
	return f.c.count(`SELECT count(*) FROM author_entity_merge_decision WHERE revoked_at IS NULL`)
}

// together reports whether the two sides of a pair are one entity.
func (f *applyFixture) together(a, b byte) bool {
	return f.c.entityOf(a) == f.c.entityOf(b)
}

// spoiled answers as mergeStub, except that production_b answers different
// on «Азимов» and judge_b on «Брэдбери»: neither pair is accepted.
func spoiled(req *fakeRequest) fakeReply {
	reply := mergeStub(req)
	if (req.Model == "deepseek-v4-pro" && strings.Contains(req.User, "Азимов")) ||
		(req.Model == "claude-opus-5" && strings.Contains(req.User, "Брэдбери")) {
		reply.Content = strings.ReplaceAll(reply.Content, `"`+llmreq.MergeSame+`"`, `"`+llmreq.MergeDifferent+`"`)
	}
	return reply
}

// The whole path on the scratch database: a report that did not pass swap
// registers nothing, one gated otherwise is refused; a passing one
// registers swap; the apply inserts the pairs the production rule accepted
// (not the one production_b doubted, not the one judge_b did), the build
// merges them by model with the report in the rule; applying again inserts
// nothing; a Latin block splits the Latin pair, a withdrawal the rest, each
// with the absorbed ID back.
func TestAMergeEvalAppliesThroughItsRegistration(t *testing.T) {
	f := newApplyFixture(t, spoiled)
	ctx := context.Background()
	_, err := f.admin.RegisterMergeKind(ctx, f.run, authorentity.CandidateSwap, 0)
	require.ErrorIs(t, err, services.ErrAuthorEntityMergeNotPassed, "the census's own report did not pass swap")
	f.passReport(authorentity.CandidateSwap, "pair_agreement")
	_, err = f.admin.RegisterMergeKind(ctx, f.run, authorentity.CandidateSwap, 0)
	require.ErrorIs(t, err, services.ErrAuthorEntityMergeGate)
	_, err = f.admin.ApplyMergeEval(ctx, 1_000_000, 0)
	require.ErrorIs(t, err, services.ErrAuthorEntityMergeNotRegistered)

	registration, report := f.registerSwap()
	_, err = f.admin.RegisterMergeKind(ctx, f.run, authorentity.CandidateSwap, 0)
	require.ErrorIs(t, err, database.ErrMergeRegistrationExists)
	_, err = f.admin.RegisterMergeKind(ctx, f.run, authorentity.CandidateInitials, 0)
	require.ErrorIs(t, err, services.ErrAuthorEntityMergeNotPassed, "the report passed swap only")
	assert.Zero(t, f.c.count(`SELECT count(*) FROM author_acceptance_class WHERE source = 'llm_eval'`),
		"a merge registration is no acceptance class")

	rep, err := f.admin.ApplyMergeEval(ctx, registration, 0)
	require.NoError(t, err)
	assert.Equal(t, 4, rep.Items, "the census asked every swap candidate")
	assert.Equal(t, 2, rep.Accepted, "the pair and both judges same: «Кинг», «King»")
	assert.Equal(t, 2, rep.Inserted)
	assert.Zero(t, rep.LapsedBeforeApply)
	t.Logf("apply: items %d, accepted %d, inserted %d", rep.Items, rep.Accepted, rep.Inserted)

	build := f.c.build()
	require.NotNil(t, build.Model)
	assert.Equal(t, 2, build.Model.Applied)
	assert.Equal(t, 2, build.Model.Unions)
	assert.True(t, f.together(fillKing, fillKing+1))
	assert.True(t, f.together(fillKingLatin, fillKingLatin+1))
	assert.False(t, f.together(fillAsimov, fillAsimov+1), "production_b answered different")
	assert.False(t, f.together(fillBradbury, fillBradbury+1), "judge_b answered different")
	var rules []string
	_, err = f.e.db.Query(&rules, `SELECT DISTINCT rule FROM author_entity_assignment WHERE method = 'model'`)
	require.NoError(t, err)
	assert.Equal(t, []string{"model:swap@" + jsonInt(report)}, rules)
	t.Logf("build after apply: applied %d, unions %d", build.Model.Applied, build.Model.Unions)
	// Each decision cites the four eval jobs that answered same on its
	// item, in slot order.
	assert.Equal(t, 2, f.c.count(`SELECT count(*) FROM author_entity_merge_decision d
		WHERE cardinality(d.verdict_ids) = 4 AND (SELECT array_agg(j.slot ORDER BY k.ord)
			FROM unnest(d.verdict_ids) WITH ORDINALITY AS k(id, ord)
			JOIN author_llm_job j ON j.id = k.id AND j.status = 'answered' AND j.repeat_no = 0
			JOIN author_llm_eval_item i ON i.id = j.eval_item_id AND i.source_fingerprint = d.digest)
			= ARRAY['production_a', 'production_b', 'judge_a', 'judge_b']`), "a decision without its four verdicts")

	again, err := f.admin.ApplyMergeEval(ctx, registration, 0)
	require.NoError(t, err)
	assert.Zero(t, again.Inserted)
	assert.Equal(t, 2, again.AlreadyDecided)

	// A Latin block: the Latin pair splits, its absorbed ID back.
	latinAbsorbed := f.c.entityOf(fillKingLatin + 1)
	require.NoError(t, f.admin.BlockMergeScript(ctx, registration, authorentity.ScriptLatn, 0))
	blocked := f.c.build()
	assert.Equal(t, map[string]int{database.MergeWithheldScriptBlocked: 1}, blocked.Model.Lapsed)
	assert.Equal(t, 1, blocked.Model.Applied)
	assert.False(t, f.together(fillKingLatin, fillKingLatin+1))
	assert.True(t, f.together(fillKing, fillKing+1), "the Cyrillic merge stays")
	assert.Equal(t, 1, blocked.Model.Revivals.Exact)
	assert.Empty(t, f.c.mergedInto(f.c.entityOf(fillKingLatin)))
	assert.Empty(t, f.c.mergedInto(f.c.entityOf(fillKingLatin+1)))
	t.Logf("build after the Latin block: applied %d, lapsed %v, revived exact %d", blocked.Model.Applied,
		blocked.Model.Lapsed, blocked.Model.Revivals.Exact)
	assert.NotEqual(t, latinAbsorbed, f.c.entityOf(fillKingLatin+1), "the absorbed side has its own ID again")

	// The withdrawal: the rest splits too.
	require.NoError(t, f.admin.WithdrawMergeRegistration(ctx, registration, 0))
	withdrawn := f.c.build()
	assert.Equal(t, map[string]int{database.MergeWithheldWithdrawn: 2}, withdrawn.Model.Lapsed)
	assert.Zero(t, withdrawn.Model.Applied)
	assert.False(t, f.together(fillKing, fillKing+1))
	assert.Equal(t, 1, withdrawn.Model.Revivals.Exact)
	t.Logf("build after the withdrawal: applied %d, lapsed %v, revived exact %d", withdrawn.Model.Applied,
		withdrawn.Model.Lapsed, withdrawn.Model.Revivals.Exact)
	_, err = f.admin.ApplyMergeEval(ctx, registration, 0)
	assert.ErrorIs(t, err, services.ErrAuthorEntityMergeNotRegistered)
	assert.Zero(t, f.c.count(`SELECT count(*) FROM author_entity WHERE merged_into IS NOT NULL`))
}

func jsonInt(n int64) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}

// A participant of the merge configuration paused for a fingerprint drift
// holds the apply: nothing is inserted until it is resumed.
func TestADriftedParticipantHoldsTheApply(t *testing.T) {
	f := newApplyFixture(t, mergeStub)
	registration, _ := f.registerSwap()
	f.c.exec(`UPDATE author_llm_provider_state SET paused_reason = 'drift', paused_at = now()
		WHERE config_version = ? AND slot = 'judge_a'`, f.m.version)
	_, err := f.admin.ApplyMergeEval(context.Background(), registration, 0)
	require.ErrorIs(t, err, services.ErrAuthorEntityMergeDriftHeld)
	assert.Zero(t, f.decisions())
	f.c.exec(`UPDATE author_llm_provider_state SET paused_reason = NULL, paused_at = NULL
		WHERE config_version = ?`, f.m.version)
	rep, err := f.admin.ApplyMergeEval(context.Background(), registration, 0)
	require.NoError(t, err)
	assert.Equal(t, 4, rep.Inserted)
}

// BuildRunningDuringApply: a build pending or running refuses the insert
// whole; nothing is inserted.
func TestBuildRunningDuringApply(t *testing.T) {
	f := newApplyFixture(t, mergeStub)
	registration, _ := f.registerSwap()
	_, err := f.admin.Start(context.Background(), 0)
	require.NoError(t, err)
	_, err = f.admin.ApplyMergeEval(context.Background(), registration, 0)
	require.ErrorIs(t, err, database.ErrAuthorEntityBuildActive)
	assert.Zero(t, f.decisions())
}

// FreezeThenBuildChangesCardThenApply: a sample book of «Стивен Кинг» is
// renamed after the census asked it; the build after that has the pair
// under another digest, so the apply lapses that verdict before applying
// it and inserts the others; the next build does not merge the pair, and
// its new candidate is left to be asked.
func TestFreezeThenBuildChangesCardThenApply(t *testing.T) {
	f := newApplyFixture(t, mergeStub)
	registration, _ := f.registerSwap()
	before := f.c.digests()
	f.c.exec(`UPDATE opds_catalog_book SET title = title || ' (2-е изд.)' WHERE id = (SELECT s.book_id
		FROM book_contributor_credit c JOIN book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
		WHERE c.source_fingerprint = ? LIMIT 1)`, bytes.Repeat([]byte{fillKing + 1}, 32))
	f.c.build()
	changed := f.c.digests()
	require.NotEqual(t, before, changed, "the renamed book is on the card")

	rep, err := f.admin.ApplyMergeEval(context.Background(), registration, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, rep.LapsedBeforeApply)
	assert.Equal(t, 3, rep.Inserted)
	build := f.c.build()
	assert.Equal(t, 3, build.Model.Applied)
	assert.False(t, f.together(fillKing, fillKing+1))
	assert.True(t, f.together(fillAsimov, fillAsimov+1))
	assert.Equal(t, 1, f.c.count(`SELECT count(*) FROM author_entity_merge_candidate c WHERE c.kind = 'swap'
		AND NOT EXISTS (SELECT 1 FROM author_entity_merge_decision d WHERE d.digest = c.digest)`),
		"the changed pair's candidate is due to be asked")
}

// BuildBetweenFreezeAndApplyNoChange: a build between the census and the
// apply that changes nothing keeps every digest: everything applies.
func TestBuildBetweenFreezeAndApplyNoChange(t *testing.T) {
	f := newApplyFixture(t, mergeStub)
	registration, _ := f.registerSwap()
	f.c.build()
	rep, err := f.admin.ApplyMergeEval(context.Background(), registration, 0)
	require.NoError(t, err)
	assert.Equal(t, 4, rep.Inserted)
	assert.Zero(t, rep.LapsedBeforeApply)
	assert.Equal(t, 4, f.c.build().Model.Applied)
}

// waitingOnStartLock reports a session waiting for an advisory lock.
func waitingOnStartLock(t *testing.T, db *pg.DB) bool {
	t.Helper()
	var n int
	_, err := db.QueryOne(pg.Scan(&n), `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`)
	require.NoError(t, err)
	return n > 0
}

// BuildStartsAfterActiveCheck: a build start that comes while the insert
// holds its transaction past the active-build check waits on the start
// lock; the insert commits, then the start, and that very build (N+1)
// applies the decisions.
func TestBuildStartsAfterActiveCheck(t *testing.T) {
	f := newApplyFixture(t, mergeStub)
	registration, _ := f.registerSwap()
	ctx := context.Background()
	type started struct {
		run int64
		rep services.AuthorEntityBuildReport
		err error
	}
	done := make(chan started, 1)
	database.TestHookMergeInsertChecked = func() {
		database.TestHookMergeInsertChecked = nil
		go func() {
			run, err := f.admin.Start(ctx, 0)
			if err != nil {
				done <- started{err: err}
				return
			}
			loop := services.NewAuthorEntityBuildLoop(f.e.db, services.AuthorEntityBuildConfig{})
			for range 100 {
				if _, err = loop.RunOnce(ctx); err != nil {
					done <- started{err: err}
					return
				}
				latest, _, latestErr := database.LatestAuthorEntityBuilds(ctx, f.e.db)
				if latestErr == nil && latest.ID == run.ID && latest.Status == database.AuthorEntityRunCompleted {
					var rep services.AuthorEntityBuildReport
					done <- started{run: run.ID, rep: rep, err: json.Unmarshal(latest.Report, &rep)}
					return
				}
			}
			done <- started{err: assert.AnError}
		}()
		// Either the start waits on the lock, or (without it) the whole
		// build lands before the insert commits.
		for range 500 {
			if waitingOnStartLock(t, f.e.db) || len(done) > 0 {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	t.Cleanup(func() { database.TestHookMergeInsertChecked = nil })
	_, latestBefore, err := database.LatestAuthorEntityBuilds(ctx, f.e.db)
	require.NoError(t, err)

	rep, err := f.admin.ApplyMergeEval(ctx, registration, 0)
	require.NoError(t, err)
	assert.Equal(t, 4, rep.Inserted)
	got := <-done
	require.NoError(t, got.err)
	assert.Equal(t, latestBefore.ID+1, got.run, "the build that waited is N+1")
	require.NotNil(t, got.rep.Model, "build N+1 saw no decision")
	assert.Equal(t, 4, got.rep.Model.Applied, "build N+1 applies the decisions it waited for")
}

// InsertWaitsForStart: a start holding the start lock makes the insert
// wait; once the start commits, the insert sees the active build and
// refuses.
func TestInsertWaitsForStart(t *testing.T) {
	f := newApplyFixture(t, mergeStub)
	registration, _ := f.registerSwap()
	ctx := context.Background()
	release := make(chan struct{})
	held := make(chan struct{})
	database.TestHookBuildStartLocked = func() {
		database.TestHookBuildStartLocked = nil
		close(held)
		<-release
	}
	t.Cleanup(func() { database.TestHookBuildStartLocked = nil })
	startErr := make(chan error, 1)
	go func() {
		_, err := f.admin.Start(ctx, 0)
		startErr <- err
	}()
	<-held
	applied := make(chan error, 1)
	go func() {
		_, err := f.admin.ApplyMergeEval(ctx, registration, 0)
		applied <- err
	}()
	require.Eventually(t, func() bool { return waitingOnStartLock(t, f.e.db) }, 5*time.Second, 10*time.Millisecond,
		"the insert did not wait on the start lock")
	select {
	case err := <-applied:
		t.Fatalf("the insert did not wait for the start: %v", err)
	default:
	}
	close(release)
	require.NoError(t, <-startErr)
	require.ErrorIs(t, <-applied, database.ErrAuthorEntityBuildActive)
	assert.Zero(t, f.decisions())
}

// RevokeDuringBuildNeedsNoStartLock: with a build pending and another
// session holding the start lock, a revoke commits at once — it never takes
// the lock — and the pending build splits the pair (a build past its
// snapshot is stopped by the fence instead, FenceDecisionRevoke).
func TestRevokeDuringBuildNeedsNoStartLock(t *testing.T) {
	f := newApplyFixture(t, mergeStub)
	registration, _ := f.registerSwap()
	ctx := context.Background()
	rep, err := f.admin.ApplyMergeEval(ctx, registration, 0)
	require.NoError(t, err)
	require.Equal(t, 4, rep.Inserted)
	f.c.build()
	require.True(t, f.together(fillKing, fillKing+1))
	var decision int64
	_, err = f.e.db.QueryOne(pg.Scan(&decision), `SELECT d.id FROM author_entity_merge_decision d
		JOIN author_entity_merge_decision_member m ON m.decision_id = d.id
		WHERE m.source_fingerprint = ?`, bytes.Repeat([]byte{fillKing}, 32))
	require.NoError(t, err)
	run, err := f.admin.Start(ctx, 0)
	require.NoError(t, err)

	holder, err := f.e.db.Begin()
	require.NoError(t, err)
	t.Cleanup(func() { _ = holder.Rollback() })
	_, err = holder.Exec(`SELECT pg_advisory_xact_lock(x'65626c64'::int, 0)`)
	require.NoError(t, err)
	revoked := make(chan error, 1)
	go func() {
		_, revokeErr := f.admin.RevokeMergeDecision(ctx, decision, 0)
		revoked <- revokeErr
	}()
	select {
	case err = <-revoked:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the revoke waited for the start lock")
	}
	require.NoError(t, holder.Rollback())
	runToCompletion(t, services.NewAuthorEntityBuildLoop(f.e.db, services.AuthorEntityBuildConfig{}), f.e.db, run.ID)
	assert.False(t, f.together(fillKing, fillKing+1), "the pending build split the revoked pair")
	assert.True(t, f.together(fillAsimov, fillAsimov+1))
}

// InsertRefusedWhileVetoUnanchored: while the last build found a live veto
// unanchored, no decision is inserted — apply-from-eval included (§4.4
// step 5); retiring the veto releases it.
func TestInsertRefusedWhileVetoUnanchored(t *testing.T) {
	f := newApplyFixture(t, mergeStub)
	registration, _ := f.registerSwap()
	ctx := context.Background()
	// A revoke of an earlier decision on «Азимов» leaves a live veto; the
	// build after a rewrite would find it unanchored.
	rep, err := f.admin.ApplyMergeEval(ctx, registration, 0)
	require.NoError(t, err)
	require.Equal(t, 4, rep.Inserted)
	var asimov int64
	_, err = f.e.db.QueryOne(pg.Scan(&asimov), `SELECT d.id FROM author_entity_merge_decision d
		JOIN author_entity_merge_decision_member m ON m.decision_id = d.id WHERE m.source_fingerprint = ?`,
		bytes.Repeat([]byte{fillAsimov}, 32))
	require.NoError(t, err)
	_, err = f.admin.RevokeMergeDecision(ctx, asimov, 0)
	require.NoError(t, err)
	var veto int64
	_, err = f.e.db.QueryOne(pg.Scan(&veto), `SELECT id FROM author_entity_merge_veto`)
	require.NoError(t, err)
	f.c.exec(`INSERT INTO author_entity_merge_veto_state (veto_id, build_run_id, state, held_books, lost_books,
		gone_books, widened_fingerprints) VALUES (?, 0, 'unanchored', 0, 1, 0, 1)`, veto)
	before := f.decisions()

	_, err = database.InsertAuthorEntityMergeDecisions(ctx, f.e.db, []database.MergeVerdict{{
		ConfigVersion: f.m.version, Kind: authorentity.CandidateSwap, Digest: [32]byte{1}}}, nil)
	require.ErrorIs(t, err, database.ErrAuthorEntityVetoUnanchored)
	_, err = f.admin.ApplyMergeEval(ctx, registration, 0)
	require.ErrorIs(t, err, database.ErrAuthorEntityVetoUnanchored)
	assert.Equal(t, before, f.decisions())

	require.NoError(t, f.admin.RetireMergeVeto(ctx, veto, 0))
	_, err = f.admin.ApplyMergeEval(ctx, registration, 0)
	require.NoError(t, err)
}

// The insert itself checks the registration of each verdict's kind, in its
// statement: a kind not registered for the configuration, withdrawn, or
// the script blocked inserts nothing and is counted by reason.
func TestTheInsertChecksTheRegistration(t *testing.T) {
	f := newApplyFixture(t, mergeStub)
	registration, report := f.registerSwap()
	ctx := context.Background()
	var digests [][]byte
	_, err := f.e.db.Query(&digests, `SELECT digest FROM author_entity_merge_candidate WHERE kind = 'swap'
		ORDER BY script, digest`)
	require.NoError(t, err)
	require.Len(t, digests, 4)
	verdict := func(i int, kind string) database.MergeVerdict {
		v := database.MergeVerdict{ConfigVersion: f.m.version, Kind: kind, ReportID: report}
		copy(v.Digest[:], digests[i])
		return v
	}
	insert := func(vs ...database.MergeVerdict) *database.MergeInsertReport {
		t.Helper()
		rep, insertErr := database.InsertAuthorEntityMergeDecisions(ctx, f.e.db, vs, nil)
		require.NoError(t, insertErr)
		return rep
	}

	// Another configuration's verdict: its kind is not registered there.
	other := verdict(0, authorentity.CandidateSwap)
	other.ConfigVersion = f.e.version
	rep := insert(other)
	assert.Equal(t, map[string]int{database.MergeWithheldUnregistered: 1}, rep.Withheld)
	assert.Zero(t, rep.Inserted)

	// A Latin block withholds the Latin pair alone.
	require.NoError(t, f.admin.BlockMergeScript(ctx, registration, authorentity.ScriptLatn, 0))
	rep = insert(verdict(0, authorentity.CandidateSwap), verdict(3, authorentity.CandidateSwap))
	assert.Equal(t, 1, rep.Inserted, "the Cyrillic verdict")
	assert.Equal(t, map[string]int{database.MergeWithheldScriptBlocked: 1}, rep.Withheld)

	// A digest that is no stored candidate of the verdict's kind lapses.
	rep = insert(verdict(1, authorentity.CandidateInitials))
	assert.Equal(t, 1, rep.LapsedBeforeApply)

	require.NoError(t, f.admin.WithdrawMergeRegistration(ctx, registration, 0))
	rep = insert(verdict(1, authorentity.CandidateSwap), verdict(0, authorentity.CandidateSwap))
	assert.Zero(t, rep.Inserted)
	assert.Equal(t, map[string]int{database.MergeWithheldWithdrawn: 1}, rep.Withheld)
	assert.Equal(t, 1, rep.AlreadyDecided)
	assert.Equal(t, 1, f.decisions())
}

// swapContested is the swap stratum's contested review in the run's
// latest report, and its error count.
func (f *applyFixture) swapContested(t *testing.T) (review llmeval.ContestedMetrics, errs int) {
	t.Helper()
	latest, err := database.GetAuthorLLMEvalReport(context.Background(), f.e.db, f.run)
	require.NoError(t, err)
	var doc struct {
		Evaluation llmeval.Report `json:"evaluation"`
	}
	require.NoError(t, json.Unmarshal([]byte(latest.Report), &doc))
	m := doc.Evaluation.Strata[authorentity.CandidateSwap].Combos["A1"]
	require.NotNil(t, m.ContestedReview)
	return *m.ContestedReview, m.Errors
}

// A contested accept (the pair agreed same, judge_b answered different on
// «Брэдбери») is listed for review and errs while pending; labeled
// different it still errs, labeled same it is cleared in the recomputed
// report — and it is never applied. An uncontested census item the pair
// disagreed on («Азимов») and an anchor cannot be labeled, by the
// application or past it; those all four agreed on («Кинг») are the
// census's audit sample (migration 70), labeled like it.
func TestAContestedAcceptIsAdjudicatedAndNeverApplied(t *testing.T) {
	f := newApplyFixture(t, spoiled)
	ctx := context.Background()
	review, errs := f.swapContested(t)
	require.Equal(t, 1, review.Pending)
	require.Equal(t, 1, errs, "the pending contested accept is the swap stratum's one error")

	items, err := services.AuthorLLMContestedItems(ctx, f.e.db, f.run)
	require.NoError(t, err)
	require.Len(t, items.Items, 1)
	contested := items.Items[0]
	assert.Equal(t, map[string][]int64{"cyrl": {contested.EvalItemID}}, review.IDs)
	assert.Contains(t, contested.A.Names[0]+contested.B.Names[0], "Брэдбери")
	assert.Equal(t, map[string]string{"production_a": "same", "production_b": "same", "judge_a": "same",
		"judge_b": "different"}, contested.Verdicts)

	var uncontested []int64
	_, err = f.e.db.Query(&uncontested, `SELECT id FROM author_llm_eval_item
		WHERE item_set = 'holdout' AND stratum LIKE 'swap.%' AND id <> ? ORDER BY id`, contested.EvalItemID)
	require.NoError(t, err)
	require.Len(t, uncontested, 3)
	audit, err := services.AuthorLLMAuditItems(ctx, f.e.db, f.run)
	require.NoError(t, err)
	require.Len(t, audit.Items, 2, "the two items all four agreed on")
	uncontested = slices.DeleteFunc(uncontested, func(id int64) bool {
		return slices.ContainsFunc(audit.Items, func(it services.AuthorLLMAdversarialItem) bool { return it.EvalItemID == id })
	})
	require.Len(t, uncontested, 1, "«Азимов»")
	var anchor int64
	_, err = f.e.db.QueryOne(pg.Scan(&anchor), `SELECT min(id) FROM author_llm_eval_item WHERE item_set = 'anchor'`)
	require.NoError(t, err)
	require.NotZero(t, anchor, "the fixture's census has anchors")
	for _, id := range append(uncontested, anchor) {
		assert.ErrorIs(t, services.AdjudicateAuthorLLMEvalItem(ctx, f.e.db, id, "same", "", "test"),
			database.ErrAuthorLLMNotAdjudicable, "item %d", id)
		_, err = f.e.db.Exec(`INSERT INTO author_llm_eval_adjudication (eval_item_id, label, adjudicated_by)
			VALUES (?, 'same', 'test')`, id)
		var pgErr pg.Error
		require.ErrorAs(t, err, &pgErr, "the trigger let item %d be labeled", id)
		assert.Equal(t, "23514", pgErr.Field('C'))
	}

	recompute := func() {
		t.Helper()
		_, _, recomputeErr := services.RecomputeAuthorLLMMergeEvalReport(ctx, f.e.db, &f.m.identity, f.m.version, f.run)
		require.NoError(t, recomputeErr)
	}
	require.NoError(t, services.AdjudicateAuthorLLMEvalItem(ctx, f.e.db, contested.EvalItemID, "different", "", "test"))
	recompute()
	review, errs = f.swapContested(t)
	assert.Equal(t, 1, review.AdjudicatedDifferent)
	assert.Equal(t, 1, errs, "labeled different it stays an error")
	require.NoError(t, services.AdjudicateAuthorLLMEvalItem(ctx, f.e.db, contested.EvalItemID, "same", "", "test"))
	recompute()
	review, errs = f.swapContested(t)
	assert.Equal(t, 1, review.AdjudicatedSame)
	assert.Zero(t, review.Failed)
	assert.Zero(t, errs, "labeled same it is cleared")

	registration, _ := f.registerSwap()
	rep, err := f.admin.ApplyMergeEval(ctx, registration, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, rep.Accepted, "a cleared contested accept is still not applied")
	assert.Zero(t, f.c.count(`SELECT count(*) FROM author_entity_merge_decision d
		JOIN author_entity_merge_decision_member m ON m.decision_id = d.id
		WHERE m.source_fingerprint = decode(repeat('24', 32), 'hex')`))
}

// driftOnCall returns a hook that pauses a merge participant for drift on
// its nth call: the insert transaction that runs it sees the drift at its
// own check, after the apply's.
func (f *applyFixture) driftOnCall(n int) func() {
	calls := 0
	return func() {
		calls++
		if calls == n {
			_, err := f.e.db.Exec(`UPDATE author_llm_provider_state SET paused_reason = 'drift', paused_at = now()
				WHERE config_version = ? AND slot = 'judge_b'`, f.m.version)
			if err != nil {
				panic(err)
			}
		}
	}
}

// A drift that comes after the apply's own check — between it and the
// first insert transaction — holds the apply: nothing is inserted and the
// report counts every verdict refused.
func TestADriftAfterTheApplysCheckHoldsTheInsert(t *testing.T) {
	f := newApplyFixture(t, mergeStub)
	registration, _ := f.registerSwap()
	database.TestHookMergeInsertChecked = f.driftOnCall(1)
	t.Cleanup(func() { database.TestHookMergeInsertChecked = nil })
	rep, err := f.admin.ApplyMergeEval(context.Background(), registration, 0)
	require.ErrorIs(t, err, services.ErrAuthorEntityMergeDriftHeld)
	assert.Zero(t, rep.Inserted)
	assert.Equal(t, 4, rep.Refused)
	assert.Equal(t, database.MergeRefusedDriftHeld, rep.RefusedReason)
	assert.Zero(t, f.decisions())
}

// syntheticSwaps stores n swap candidates of the stored census beside the
// fixture's, each side one fingerprint credited on one shared book, and
// returns their verdicts for the merge configuration.
func (f *applyFixture) syntheticSwaps(n int) []database.MergeVerdict {
	f.c.t.Helper()
	snap := f.c.book("Сборник")
	for _, side := range []struct {
		name   string
		offset int
	}{{"a", 0}, {"b", 1}} {
		f.c.exec(`INSERT INTO book_contributor_credit (snapshot_id, role, position, source_display_name,
				source_fingerprint)
			SELECT ?, 'author', 2 * g + ?, 'fixture', sha256(convert_to('syn-' || ? || '-' || g, 'UTF8'))
			FROM generate_series(1, ?) AS g`, snap, side.offset, side.name, n)
	}
	f.c.exec(`INSERT INTO author_entity_merge_candidate (digest, kind, script, item_kind, side_a_entity, side_b_entity,
			members_sha256, input_sha256, first_build_run_id)
		SELECT sha256(convert_to('syn-d-' || g, 'UTF8')), 'swap', 'Cyrl', 'entity', gen_random_uuid(),
			gen_random_uuid(), sha256(convert_to('syn-m-' || g, 'UTF8')), sha256(convert_to('syn-i-' || g, 'UTF8')), 1
		FROM generate_series(1, ?) AS g`, n)
	f.c.exec(`INSERT INTO author_entity_merge_candidate_member (digest, side, source_fingerprint, extractor_version,
			ordinal)
		SELECT sha256(convert_to('syn-d-' || g, 'UTF8')), s, sha256(convert_to('syn-' || s || '-' || g, 'UTF8')),
			'extractor-v1', 0
		FROM generate_series(1, ?) AS g, unnest(ARRAY['a', 'b']) AS s`, n)
	var digests [][]byte
	_, err := f.e.db.Query(&digests, `SELECT sha256(convert_to('syn-d-' || g, 'UTF8')) FROM generate_series(1, ?) AS g
		ORDER BY g`, n)
	require.NoError(f.c.t, err)
	out := make([]database.MergeVerdict, len(digests))
	for i, d := range digests {
		out[i] = database.MergeVerdict{ConfigVersion: f.m.version, Kind: authorentity.CandidateSwap}
		copy(out[i].Digest[:], d)
	}
	return out
}

// syntheticDecisions counts the decisions on the synthetic candidates, and
// their member and book rows.
func (f *applyFixture) syntheticDecisions() (decisions, members, books int) {
	in := `(SELECT id FROM author_entity_merge_decision WHERE digest IN
		(SELECT sha256(convert_to('syn-d-' || g, 'UTF8')) FROM generate_series(1, 10000) AS g))`
	return f.c.count(`SELECT count(*) FROM ` + in + ` d`),
		f.c.count(`SELECT count(*) FROM author_entity_merge_decision_member WHERE decision_id IN ` + in),
		f.c.count(`SELECT count(*) FROM author_entity_merge_decision_book WHERE decision_id IN ` + in)
}

// The insert commits in transactions of 500: 502 verdicts are two
// chunks, every decision with its members and books. A guard that flips at
// the boundary — a drift, or a build started while the first chunk held
// the start lock — leaves the first chunk committed and refuses the rest,
// and the report says how many and why.
func TestTheInsertCommitsChunkByChunk(t *testing.T) {
	const n = 502
	ctx := context.Background()
	opts := func(f *applyFixture) *database.MergeInsertOptions {
		var run int64
		_, err := f.e.db.QueryOne(pg.Scan(&run), `SELECT run_id FROM author_entity_merge_registration r
			JOIN author_llm_eval_report rep ON rep.id = r.evidence_report_id`)
		require.NoError(t, err)
		return &database.MergeInsertOptions{DriftConfig: f.m.version, DriftRunID: run}
	}

	t.Run("both chunks", func(t *testing.T) {
		f := newApplyFixture(t, mergeStub)
		f.registerSwap()
		rep, err := database.InsertAuthorEntityMergeDecisions(ctx, f.e.db, f.syntheticSwaps(n), opts(f))
		require.NoError(t, err)
		assert.Equal(t, n, rep.Inserted)
		assert.Zero(t, rep.Refused)
		assert.Empty(t, rep.RefusedReason)
		decisions, members, books := f.syntheticDecisions()
		assert.Equal(t, [3]int{n, 2 * n, 2 * n}, [3]int{decisions, members, books})
	})

	t.Run("drift at the boundary", func(t *testing.T) {
		f := newApplyFixture(t, mergeStub)
		f.registerSwap()
		verdicts := f.syntheticSwaps(n)
		database.TestHookMergeInsertChecked = f.driftOnCall(2)
		t.Cleanup(func() { database.TestHookMergeInsertChecked = nil })
		rep, err := database.InsertAuthorEntityMergeDecisions(ctx, f.e.db, verdicts, opts(f))
		require.ErrorIs(t, err, database.ErrMergeApplyDriftHeld)
		assert.Equal(t, 500, rep.Inserted, "the first chunk is committed")
		assert.Equal(t, 2, rep.Refused)
		assert.Equal(t, database.MergeRefusedDriftHeld, rep.RefusedReason)
		decisions, _, _ := f.syntheticDecisions()
		assert.Equal(t, 500, decisions)
	})

	t.Run("build start at the boundary", func(t *testing.T) {
		f := newApplyFixture(t, mergeStub)
		f.registerSwap()
		verdicts := f.syntheticSwaps(n)
		started := make(chan error, 1)
		calls := 0
		database.TestHookMergeInsertChecked = func() {
			calls++
			if calls != 1 {
				return
			}
			// The start queues on the lock the first chunk holds and takes
			// it before the second chunk does.
			go func() {
				_, startErr := f.admin.Start(ctx, 0)
				started <- startErr
			}()
			for range 500 {
				if waitingOnStartLock(t, f.e.db) {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		t.Cleanup(func() { database.TestHookMergeInsertChecked = nil })
		rep, err := database.InsertAuthorEntityMergeDecisions(ctx, f.e.db, verdicts, opts(f))
		require.NoError(t, <-started)
		require.ErrorIs(t, err, database.ErrAuthorEntityBuildActive)
		assert.Equal(t, 500, rep.Inserted, "the first chunk is committed")
		assert.Equal(t, 2, rep.Refused)
		assert.Equal(t, database.MergeRefusedBuildActive, rep.RefusedReason)
		decisions, _, _ := f.syntheticDecisions()
		assert.Equal(t, 500, decisions)
	})
}
