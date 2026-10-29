package database

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"gopds-api/internal/authornorm"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Registration of a merge stratum (migration 68, plan §4.7).

// mergeEvalReportOn writes a merge configuration, a completed eval run of
// it and the run's report by hand, and returns the configuration's version
// and the report's id and digest.
func mergeEvalReportOn(t *testing.T, tx pg.DBI, fill byte) (version string, reportID int64, sha []byte) {
	t.Helper()
	version = "llm1-" + hex64(fill)
	_, err := tx.Exec(`INSERT INTO author_llm_config (version, base_url, identity, prompt_sha256, schema_sha256,
		validator_version, tokenizer_version, context_version)
		VALUES (?, 'http://llm.local/v1', '{"task":"merge"}', ?, ?, 'v', 't', 'c')`, version, hex64('c'), hex64('d'))
	require.NoError(t, err)
	var run int64
	_, err = tx.QueryOne(pg.Scan(&run), `INSERT INTO author_llm_run (kind, mode, status, config_version, finished_at)
		VALUES ('eval', 'pilot', 'completed', ?, now()) RETURNING id`, version)
	require.NoError(t, err)
	sum := sha256.Sum256([]byte("report:" + version))
	_, err = tx.QueryOne(pg.Scan(&reportID), `INSERT INTO author_llm_eval_report (run_id, config_version, report,
		report_sha256) VALUES (?, ?, '{}', ?) RETURNING id`, run, version, sum[:])
	require.NoError(t, err)
	return version, reportID, sum[:]
}

func registration(version string, report int64, sha []byte, kind string) *MergeRegistrationInput {
	return &MergeRegistrationInput{ConfigVersion: version, Kind: kind, ReportID: report, ReportSHA256: sha,
		Gate: MergeGateJudgeConsensus}
}

// A registration is history: never deleted, changed only by its withdrawal
// and only once; a block never changes. One registration per configuration
// and kind; the gate is judge_consensus and nothing else; the report is
// cited by id and its own digest. Registrations and blocks mark a build.
func TestMergeRegistrationIsHistory(t *testing.T) {
	f := withAuthorSchemaTx(t)
	ctx := context.Background()
	version, report, sha := mergeEvalReportOn(t, f.tx, 'a')
	marks := func() int {
		return f.count(`SELECT count(*) FROM author_autopilot_mark WHERE xact = pg_current_xact_id()`)
	}
	require.Zero(t, marks())
	id, err := RegisterMergeKind(ctx, f.tx, registration(version, report, sha, "swap"))
	require.NoError(t, err)
	assert.Equal(t, 1, marks(), "a registration is build input")

	for _, gate := range []string{"", "pair_agreement", "judge_consensus "} {
		in := registration(version, report, sha, "initials")
		in.Gate = gate
		_, err = RegisterMergeKind(ctx, f.tx, in)
		assert.ErrorIs(t, err, ErrMergeRegistrationInvalid, "gate %q", gate)
	}
	restrictOrCheck := func(code string, err error) {
		t.Helper()
		var pgErr pg.Error
		require.True(t, errors.As(err, &pgErr), "%v", err)
		assert.Equal(t, code, pgErr.Field('C'), "%v", err)
	}
	restrictOrCheck("23514", f.refused(`INSERT INTO author_entity_merge_registration (config_version, kind,
		evidence_report_id, evidence_report_sha256, gate) VALUES (?, 'initials', ?, ?, 'pair_agreement')`,
		version, report, sha))
	other := sha256.Sum256([]byte("another report"))
	restrictOrCheck("23503", f.refused(`INSERT INTO author_entity_merge_registration (config_version, kind,
		evidence_report_id, evidence_report_sha256, gate) VALUES (?, 'initials', ?, ?, 'judge_consensus')`,
		version, report, other[:]))
	f.exec(`SAVEPOINT again`)
	_, err = RegisterMergeKind(ctx, f.tx, registration(version, report, sha, "swap"))
	assert.ErrorIs(t, err, ErrMergeRegistrationExists)
	f.exec(`ROLLBACK TO SAVEPOINT again`)

	require.NoError(t, BlockMergeScript(ctx, f.tx, id, "Latn", 0))
	require.NoError(t, BlockMergeScript(ctx, f.tx, id, "Latn", 0), "a block already there is kept")
	assert.ErrorIs(t, BlockMergeScript(ctx, f.tx, id, "Grek", 0), ErrMergeRegistrationInvalid)
	restrictOrCheck("23001", f.refused(`DELETE FROM author_entity_merge_registration WHERE id = ?`, id))
	restrictOrCheck("23001", f.refused(`UPDATE author_entity_merge_registration SET kind = 'initials' WHERE id = ?`, id))
	restrictOrCheck("23001", f.refused(`UPDATE author_entity_merge_script_block SET script = 'Cyrl'
		WHERE registration_id = ?`, id))
	restrictOrCheck("23001", f.refused(`DELETE FROM author_entity_merge_script_block WHERE registration_id = ?`, id))

	require.NoError(t, WithdrawMergeRegistration(ctx, f.tx, id, 0))
	assert.ErrorIs(t, WithdrawMergeRegistration(ctx, f.tx, id, 0), ErrMergeRegistrationNotLive)
	restrictOrCheck("23001", f.refused(`UPDATE author_entity_merge_registration SET withdrawn_at = now() WHERE id = ?`, id))
	assert.ErrorIs(t, BlockMergeScript(ctx, f.tx, id, "Cyrl", 0), ErrMergeRegistrationNotLive)
	got, err := MergeRegistrationOf(ctx, f.tx, version, "swap")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, []string{"Latn"}, got.BlockedScripts)
	assert.NotNil(t, got.WithdrawnAt)
	assert.Equal(t, report, got.EvidenceReportID)
}

// acceptanceSnapshot is everything of K's acceptance policy a merge
// registration must leave as it is.
type acceptanceSnapshot struct {
	Version    int
	NextPolicy int
	Policy     authornorm.AcceptancePolicy
	Pairs      map[string]bool
	Rows       []string
	Candidates []AcceptanceCandidate
}

func takeAcceptanceSnapshot(t *testing.T, f *authorSchemaFixture, kVersion string) acceptanceSnapshot {
	t.Helper()
	ctx := context.Background()
	var s acceptanceSnapshot
	var err error
	s.Version, err = CurrentAcceptancePolicyVersion(ctx, f.tx)
	require.NoError(t, err)
	_, err = f.tx.QueryOne(pg.Scan(&s.NextPolicy), `
		SELECT greatest(coalesce(max(policy_version::int), 0), ?0) + 1 FROM author_acceptance_class`,
		authornorm.BasePolicyVersion)
	require.NoError(t, err)
	s.Policy, err = LoadCurrentAcceptancePolicy(ctx, f.tx, authornorm.NormalizerVersion)
	require.NoError(t, err)
	s.Pairs, err = AuthorLLMRegisteredPairs(ctx, f.tx, kVersion)
	require.NoError(t, err)
	_, err = f.tx.Query(&s.Rows, `SELECT concat_ws('|', id, policy_version, decision_class, script, config_version,
		source) FROM author_acceptance_class ORDER BY id`)
	require.NoError(t, err)
	s.Candidates, err = AcceptanceCandidates(ctx, f.tx, authornorm.NormalizerVersion, s.Version, nil, 100)
	require.NoError(t, err)
	return s
}

// A merge registration (and a script block in it) takes no policy version
// and leaves K's acceptance policy as it was: the current version, the
// policy, K's registered pairs, the next version a K resolve start would
// register at, and the credits the acceptance pass selects (§4.7).
func TestMergeRegistrationLeavesAcceptancePolicy(t *testing.T) {
	storeDB = jobsDB(t)
	t.Cleanup(func() { storeDB = nil })
	f := withStoreTx(t)
	ctx := context.Background()
	f.waitingInput(t, structuredSource(t), 2)
	f.registerClass("2", "structured_person", authornorm.NormalizerVersion)
	// K's own registration from its eval report: an LLM pair at policy 3.
	kVersion, kReport, kSHA := mergeEvalReportOn(t, f.tx, 'e')
	f.exec(`INSERT INTO author_acceptance_class (policy_version, decision_class, script, config_version,
		evidence_report_sha256, evidence_report_id, evidence_ref, source, registered_by_user_id)
		VALUES ('3', ?, 'Latn', ?, ?, ?, 'author_llm_eval_report/1.json', 'llm_eval', ?)`,
		string(authornorm.LLMClass(authornorm.ClassStructuredPerson)), kVersion, kSHA, kReport, f.user())
	before := takeAcceptanceSnapshot(t, f, kVersion)
	require.Equal(t, 3, before.Version)
	require.NotEmpty(t, before.Candidates, "the fixture has credits the pass selects")
	require.True(t, before.Pairs[AuthorLLMPair(authornorm.ClassStructuredPerson, "Latn")])

	version, report, sha := mergeEvalReportOn(t, f.tx, 'a')
	id, err := RegisterMergeKind(ctx, f.tx, registration(version, report, sha, "swap"))
	require.NoError(t, err)
	require.NoError(t, BlockMergeScript(ctx, f.tx, id, "Latn", 0))

	after := takeAcceptanceSnapshot(t, f, kVersion)
	assert.Equal(t, before, after)
	assert.Equal(t, 4, after.NextPolicy, "a K resolve start still registers at max + 1")
}

// A merge registration, a block and a withdrawal commit while another
// session holds K's acceptance table exclusively: none of them waits for
// it (§4.7).
func TestMergeRegistrationTakesNoAcceptanceLock(t *testing.T) {
	scratch := jobsDB(t)
	ctx := context.Background()
	version, report, sha := mergeEvalReportOn(t, scratch, '9')

	holder, err := scratch.Begin()
	require.NoError(t, err)
	t.Cleanup(func() { _ = holder.Rollback() })
	_, err = holder.Exec(`LOCK TABLE author_acceptance_class IN ACCESS EXCLUSIVE MODE`)
	require.NoError(t, err)

	started := time.Now()
	err = scratch.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if _, lockErr := tx.Exec(`SET LOCAL lock_timeout = '1s'`); lockErr != nil {
			return lockErr
		}
		id, regErr := RegisterMergeKind(ctx, tx, registration(version, report, sha, "swap"))
		if regErr != nil {
			return regErr
		}
		if blockErr := BlockMergeScript(ctx, tx, id, "Latn", 0); blockErr != nil {
			return blockErr
		}
		return WithdrawMergeRegistration(ctx, tx, id, 0)
	})
	require.NoError(t, err, "a merge registration waited for K's acceptance table")
	assert.Less(t, time.Since(started), time.Second)
}

// The build reads why a model decision is withheld: not registered, its
// registration withdrawn, or its script blocked. A revoke is never
// withheld.
func TestMergeDecisionsCarryTheirRegistration(t *testing.T) {
	f := withAuthorSchemaTx(t)
	ctx := context.Background()
	version, report, sha := mergeEvalReportOn(t, f.tx, 'a')
	swap, err := RegisterMergeKind(ctx, f.tx, registration(version, report, sha, "swap"))
	require.NoError(t, err)
	for _, fill := range []byte{0x10, 0x11, 0x20, 0x21, 0x30, 0x31} {
		f.displayCredit(f.snapshot(&snapshotSpec{book: f.book(), extractor: "v1"}), "author", 0, "fixture", fill)
	}
	plain, err := InsertMergeDecision(ctx, f.tx, &MergeDecisionInput{Candidate: decisionCandidate(0x10),
		Source: "model"})
	require.NoError(t, err)
	registered, err := InsertMergeDecision(ctx, f.tx, &MergeDecisionInput{Candidate: decisionCandidate(0x20),
		Source: "model"})
	require.NoError(t, err)
	f.exec(`ALTER TABLE author_entity_merge_decision DISABLE TRIGGER author_entity_merge_decision_history`)
	f.exec(`UPDATE author_entity_merge_decision SET config_version = ?, registration_id = ? WHERE id = ?`,
		version, swap, registered)
	f.exec(`ALTER TABLE author_entity_merge_decision ENABLE TRIGGER author_entity_merge_decision_history`)
	withheld := func() map[int64]string {
		t.Helper()
		live, loadErr := LoadMergeDecisions(ctx, f.tx)
		require.NoError(t, loadErr)
		out := map[int64]string{}
		for _, d := range live {
			out[d.ID] = d.Withheld
		}
		return out
	}
	assert.Equal(t, map[int64]string{plain: MergeWithheldUnregistered, registered: ""}, withheld())
	require.NoError(t, BlockMergeScript(ctx, f.tx, swap, "Latn", 0))
	assert.Empty(t, withheld()[registered], "a block of another script")
	require.NoError(t, BlockMergeScript(ctx, f.tx, swap, "Cyrl", 0))
	assert.Equal(t, MergeWithheldScriptBlocked, withheld()[registered])
	require.NoError(t, WithdrawMergeRegistration(ctx, f.tx, swap, 0))
	assert.Equal(t, MergeWithheldWithdrawn, withheld()[registered])
	revoke, err := RevokeMergeDecision(ctx, f.tx, registered, 0)
	require.NoError(t, err)
	assert.Equal(t, map[int64]string{plain: MergeWithheldUnregistered, revoke: ""}, withheld())
}

// The start of a build and a decision insert take one lock: a start that
// comes while an insert holds it waits for the insert's commit.
func TestBuildStartTakesTheStartLock(t *testing.T) {
	scratch := jobsDB(t)
	ctx := context.Background()
	insert, err := scratch.Begin()
	require.NoError(t, err)
	t.Cleanup(func() { _ = insert.Rollback() })
	require.NoError(t, lockEntityBuildStart(ctx, insert))

	started := make(chan error, 1)
	go func() {
		_, startErr := StartAuthorEntityBuild(ctx, scratch, 0)
		started <- startErr
	}()
	select {
	case err = <-started:
		t.Fatalf("the start did not wait for the start lock: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	require.NoError(t, insert.Commit())
	require.NoError(t, <-started)
}
