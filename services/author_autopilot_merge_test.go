package services_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopds-api/database"
	"gopds-api/internal/authorentity"
	"gopds-api/models"
	"gopds-api/services"
)

// The autopilot's merge step (E4e, plan §6) on the merge fixture, swap
// registered from merge1's census: once the scan is quiet, the registered
// strata resolved and no pass is to do, it starts the merge
// configuration's ordinary merge run over the candidates due, and the
// decisions the run inserts mark the pass that lands them. Its switch is
// off until an administrator turns it on; it holds on an unanchored veto,
// a drift, a pause and the budget; a pass that keeps failing its census
// is spaced out.

// mergePilot is the merge fixture with an autopilot of both
// configurations on a clock the test moves.
type mergePilot struct {
	*applyFixture
	clock        *autopilotClock
	registration int64
	// budget, when set, is the merge configuration's one-off budget.
	budget int64
}

func newMergePilot(t *testing.T) *mergePilot {
	t.Helper()
	f := newApplyFixture(t, mergeStub)
	registration, _ := f.registerSwap()
	p := &mergePilot{applyFixture: f, clock: &autopilotClock{}, registration: registration}
	var newest time.Time
	_, err := f.e.db.QueryOne(pg.Scan(&newest), `SELECT max(created_at) FROM book_metadata_snapshot`)
	require.NoError(t, err)
	p.clock.set(newest.Add(autopilotQuiet + time.Second))
	return p
}

// pilot is one pod's worker: the name normalization's admin for the
// resolve check (no resolve pilot in this fixture), the merge
// configuration's for the merge step.
func (p *mergePilot) pilot() *services.AuthorAutopilot {
	return services.NewAuthorAutopilot(p.e.db, func(context.Context) (*services.AuthorLLMAdmin, error) {
		return p.e.llmAdmin(), nil
	}, services.AuthorAutopilotConfig{Quiet: autopilotQuiet, Lease: 2 * time.Minute, Backoff: time.Minute,
		MaxBackoff: 8 * time.Minute, Now: p.clock.now}).WithMerge(func(context.Context) (*services.AuthorLLMAdmin, error) {
		settings := p.e.settings
		if p.budget > 0 {
			settings.Budget.OneOffTokens = p.budget
		}
		return services.NewAuthorLLMMergeAdmin(p.e.db, &p.m.identity, p.m.version, "", &settings), nil
	})
}

func (p *mergePilot) switchMerge(on bool) {
	p.c.t.Helper()
	require.NoError(p.c.t, services.SetAuthorAutopilotMerge(context.Background(), p.e.db, on, 0))
}

func (p *mergePilot) tick(a *services.AuthorAutopilot) bool {
	p.c.t.Helper()
	started, err := a.RunOnce(context.Background())
	require.NoError(p.c.t, err)
	return started
}

func (p *mergePilot) state() *database.AuthorAutopilotState {
	p.c.t.Helper()
	st, err := database.GetAuthorAutopilot(context.Background(), p.e.db)
	require.NoError(p.c.t, err)
	return st
}

func noteOf(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// edit renames a sample book of each pair's second side, as renew does,
// without building: the change only marks a pass.
func (p *mergePilot) edit(fills ...byte) {
	p.c.t.Helper()
	for _, fill := range fills {
		p.c.exec(`UPDATE opds_catalog_book SET title = title || ' (3-е изд.)' WHERE id = (SELECT s.book_id
			FROM book_contributor_credit c JOIN book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
			WHERE c.source_fingerprint = ? LIMIT 1)`, bytes.Repeat([]byte{fill + 1}, 32))
	}
}

// buildStarted is the pass the last tick started, run to completion.
func (p *mergePilot) buildStarted() {
	p.c.t.Helper()
	st := p.state()
	require.Equal(p.c.t, database.AuthorAutopilotStateBuildStarted, noteOf(st.State))
	runToCompletion(p.c.t, services.NewAuthorEntityBuildLoop(p.e.db, services.AuthorEntityBuildConfig{}), p.e.db,
		*st.StateRunID)
}

// mergeStarted is the merge run the last tick started, checked to be the
// ordinary one — mode full, nobody's — and run to completion.
func (p *mergePilot) mergeStarted(candidates int) int64 {
	p.c.t.Helper()
	st := p.state()
	require.Equal(p.c.t, database.AuthorAutopilotStateMergeStarted, noteOf(st.State))
	assert.Equal(p.c.t, services.AutopilotMergeStarted, noteOf(st.MergeNote))
	assert.Equal(p.c.t, candidates, st.MergeCandidates)
	assert.Equal(p.c.t, database.AuthorAutopilotActionMerge, noteOf(st.LastAction))
	run := *st.StateRunID
	var r struct {
		Kind string `pg:"kind"`
		Mode string `pg:"mode"`
		By   *int64 `pg:"created_by_user_id"`
	}
	_, err := p.e.db.QueryOne(&r, `SELECT kind, mode, created_by_user_id FROM author_llm_run WHERE id = ?`, run)
	require.NoError(p.c.t, err)
	assert.Equal(p.c.t, string(models.AuthorLLMRunMerge), r.Kind)
	assert.Equal(p.c.t, models.AuthorLLMEvalModeFull, r.Mode, "never an eval")
	assert.Nil(p.c.t, r.By)
	p.stepMergeRun(run, false)
	return run
}

func (p *mergePilot) mergeRuns() int {
	return p.c.count(`SELECT count(*) FROM author_llm_run WHERE kind = 'merge'`)
}

// The tick's order and the whole path: a pass to do goes first (a
// card-field edit alone marks one), then the merge run over the
// candidates that pass left, then the pass its decisions mark; the pass's
// own writes mark nothing, so the tick after it is idle. The resolve
// check came first in every tick. An administrator's eval is refused while
// the autopilot's merge run is active.
func TestTheAutopilotBuildsThenMergesThenBuilds(t *testing.T) {
	p := newMergePilot(t)
	ctx := context.Background()
	p.switchMerge(true)
	p.renew(fillAsimov)
	p.edit(fillKing)
	require.Len(t, p.due(), 1, "the edit is not built yet")
	a := p.pilot()

	require.True(t, p.tick(a))
	assert.Equal(t, services.AutopilotResolveNoPilot, noteOf(p.state().ResolveNote))
	p.buildStarted()
	require.Len(t, p.due(), 2)

	require.True(t, p.tick(a))
	_, err := services.StartAuthorLLMMergeEval(ctx, p.e.db, p.m.version, models.AuthorLLMEvalModeMerge5, nil)
	assert.ErrorIs(t, err, database.ErrAuthorLLMActiveRun, "an administrator's eval beside the autopilot's run")
	run := p.mergeStarted(2)
	assert.Len(t, p.asked(run), 2)
	assert.Equal(t, 2, p.decisions())

	require.True(t, p.tick(a), "the decisions mark a pass")
	p.buildStarted()
	assert.True(t, p.together(fillKing, fillKing+1))
	assert.True(t, p.together(fillAsimov, fillAsimov+1))

	assert.False(t, p.tick(a))
	st := p.state()
	assert.Equal(t, database.AuthorAutopilotStateIdle, noteOf(st.State))
	assert.Equal(t, services.AutopilotMergeNothing, noteOf(st.MergeNote))
	assert.Zero(t, p.c.count(`SELECT count(*) FROM author_autopilot_mark WHERE build_run_id IS NULL`),
		"the pass's own writes marked a pass")
	assert.Equal(t, 1, p.mergeRuns())
}

// The merge step is off until an administrator switches it on: the
// candidates due wait, the card says why. A switch-off that commits while
// a tick plans undoes the run the tick starts.
func TestTheMergeStepWaitsForItsSwitch(t *testing.T) {
	p := newMergePilot(t)
	p.renew(fillKing)
	a := p.pilot()
	st := p.state()
	assert.False(t, st.MergeEnabled, "on by default")

	assert.False(t, p.tick(a))
	st = p.state()
	assert.Equal(t, services.AutopilotMergeSwitchedOff, noteOf(st.MergeNote))
	assert.Zero(t, p.mergeRuns())

	p.switchMerge(true)
	restore := services.SetAuthorAutopilotHook(func(action string) error {
		if action == "merge" {
			p.switchMerge(false)
		}
		return nil
	})
	assert.False(t, p.tick(a))
	restore()
	assert.Zero(t, p.mergeRuns(), "the run outlived the switch-off")

	p.switchMerge(true)
	require.True(t, p.tick(a))
	p.mergeStarted(1)
}

// Nothing due starts nothing: the registration's pass, then an idle tick.
func TestNothingDueStartsNoMergeRun(t *testing.T) {
	p := newMergePilot(t)
	p.switchMerge(true)
	a := p.pilot()
	require.True(t, p.tick(a), "the registration marks a pass")
	p.buildStarted()
	assert.False(t, p.tick(a))
	assert.Equal(t, services.AutopilotMergeNothing, noteOf(p.state().MergeNote))
	assert.Zero(t, p.mergeRuns())
}

// A lapse is asked again: a card change after the merge splits the pair
// at the next pass, and its new candidate goes with the next merge run.
func TestALapseIsAskedAgainInTheNextMergeRun(t *testing.T) {
	p := newMergePilot(t)
	p.switchMerge(true)
	p.renew(fillKing)
	a := p.pilot()
	require.True(t, p.tick(a))
	p.mergeStarted(1)
	require.True(t, p.tick(a))
	p.buildStarted()
	require.True(t, p.together(fillKing, fillKing+1))

	p.edit(fillKing)
	require.True(t, p.tick(a))
	p.buildStarted()
	assert.False(t, p.together(fillKing, fillKing+1), "a lapse splits until it is asked again")
	renewed := p.due()
	require.Len(t, renewed, 1)

	require.True(t, p.tick(a))
	run := p.mergeStarted(1)
	assert.Equal(t, renewed, p.asked(run))
	require.True(t, p.tick(a))
	p.buildStarted()
	assert.True(t, p.together(fillKing, fillKing+1))
}

// A blocked script's candidates never go with the autopilot's run.
func TestTheAutopilotAsksNoBlockedScript(t *testing.T) {
	p := newMergePilot(t)
	p.switchMerge(true)
	p.renew(fillKing, fillKingLatin)
	require.NoError(t, p.admin.BlockMergeScript(context.Background(), p.registration, authorentity.ScriptLatn, 0))
	a := p.pilot()
	require.True(t, p.tick(a), "the block marks a pass")
	p.buildStarted()
	require.True(t, p.tick(a))
	run := p.mergeStarted(1)
	assert.Equal(t, []string{hex.EncodeToString(p.digestOf(fillKing))}, p.asked(run))
}

// unanchor revokes «Азимов»'s applied merge — a live veto — and records
// the veto unanchored, as a pass after a rewrite finds it; it returns the
// veto.
func (p *mergePilot) unanchor() int64 {
	p.c.t.Helper()
	ctx := context.Background()
	_, err := p.admin.ApplyMergeEval(ctx, p.registration, 0)
	require.NoError(p.c.t, err)
	var asimov int64
	_, err = p.e.db.QueryOne(pg.Scan(&asimov), `SELECT d.id FROM author_entity_merge_decision d
		JOIN author_entity_merge_decision_member m ON m.decision_id = d.id WHERE m.source_fingerprint = ?`,
		bytes.Repeat([]byte{fillAsimov}, 32))
	require.NoError(p.c.t, err)
	_, err = p.admin.RevokeMergeDecision(ctx, asimov, 0)
	require.NoError(p.c.t, err)
	p.renew(fillKing)
	var veto int64
	_, err = p.e.db.QueryOne(pg.Scan(&veto), `SELECT id FROM author_entity_merge_veto`)
	require.NoError(p.c.t, err)
	p.c.exec(`UPDATE author_entity_merge_veto_state SET state = 'unanchored' WHERE veto_id = ?`, veto)
	require.NotEmpty(p.c.t, p.due())
	return veto
}

// An unanchored veto holds the merge step; a retire or a re-anchor
// releases it once the pass it marks has run.
func TestAnUnanchoredVetoHoldsTheMergeStep(t *testing.T) {
	release := map[string]func(p *mergePilot, veto int64){
		"retire": func(p *mergePilot, veto int64) {
			require.NoError(t, p.admin.RetireMergeVeto(context.Background(), veto, 0))
		},
		"re-anchor": func(p *mergePilot, veto int64) {
			sides := [2]uuid.UUID{}
			for i, fill := range []byte{fillAsimov, fillAsimov + 1} {
				var id uuid.UUID
				_, err := p.e.db.QueryOne(pg.Scan(&id), `SELECT entity_id FROM author_entity_assignment
					WHERE source_fingerprint = ? LIMIT 1`, bytes.Repeat([]byte{fill}, 32))
				require.NoError(t, err)
				sides[i] = id
			}
			_, err := p.admin.ReanchorMergeVeto(context.Background(), veto, sides[0], sides[1], 0)
			require.NoError(t, err)
		},
	}
	for name, fn := range release {
		t.Run(name, func(t *testing.T) {
			p := newMergePilot(t)
			p.switchMerge(true)
			veto := p.unanchor()
			a := p.pilot()
			assert.False(t, p.tick(a))
			assert.Equal(t, services.AutopilotMergeVetoHeld, noteOf(p.state().MergeNote))
			assert.Zero(t, p.mergeRuns())

			fn(p, veto)
			require.True(t, p.tick(a), "the release marks a pass")
			p.buildStarted()
			require.True(t, p.tick(a))
			p.mergeStarted(len(p.due()))
		})
	}
}

// A drifted participant and a paused one hold the merge step; so does a
// budget the run's estimate would pass.
func TestTheMergeStepsLimits(t *testing.T) {
	p := newMergePilot(t)
	p.switchMerge(true)
	p.renew(fillKing)
	a := p.pilot()
	for reason, note := range map[string]string{
		string(models.AuthorLLMPausedDrift): services.AutopilotMergeDriftHeld,
		"auth":                              services.AutopilotMergeLLMPaused,
	} {
		p.c.exec(`UPDATE author_llm_provider_state SET paused_reason = ?, paused_at = now()
			WHERE config_version = ? AND slot = 'production_a'`, reason, p.m.version)
		assert.False(t, p.tick(a), reason)
		assert.Equal(t, note, noteOf(p.state().MergeNote), reason)
		p.c.exec(`UPDATE author_llm_provider_state SET paused_reason = NULL, paused_at = NULL
			WHERE config_version = ?`, p.m.version)
	}
	assert.Zero(t, p.mergeRuns())

	var spent int64
	_, err := p.e.db.QueryOne(pg.Scan(&spent), `SELECT coalesce(sum(tokens), 0) FROM author_llm_token_tally`)
	require.NoError(t, err)
	estimate := int64(4*1520 + 1_450_000)
	p.budget = spent + estimate - 1
	assert.False(t, p.tick(a))
	st := p.state()
	assert.Equal(t, services.AutopilotMergeBudget, noteOf(st.MergeNote))
	assert.Equal(t, 1, st.MergeCandidates)
	p.budget = spent + estimate
	require.True(t, p.tick(a), "the estimate fits the budget exactly")
	p.mergeStarted(1)
}

// failPass ends the autopilot's pass as failed with class, at the clock.
func (p *mergePilot) failPass(class string) int64 {
	p.c.t.Helper()
	st := p.state()
	require.Equal(p.c.t, database.AuthorAutopilotStateBuildStarted, noteOf(st.State))
	p.c.exec(`UPDATE author_entity_build_run SET status = 'failed', last_error_class = ?, finished_at = ?
		WHERE id = ?`, class, p.clock.now(), *st.StateRunID)
	return *st.StateRunID
}

// A pass that failed input_moved gave its marks back: the next quiet tick
// builds again, at once.
func TestAPassThatFailedInputMovedIsBuiltAgainAtOnce(t *testing.T) {
	p := newMergePilot(t)
	p.edit(fillKing)
	a := p.pilot()
	require.True(t, p.tick(a))
	p.failPass(services.AuthorEntityBuildErrorInputMoved)
	require.True(t, p.tick(a))
	p.buildStarted()
}

// Passes that keep failing their census are spaced out: the next waits a
// backoff that doubles with each one in a row, the card says so, and the
// merge step waits for the pass, not for the old census. A pass that
// passes ends the streak.
func TestARepeatingCensusFailureBacksOff(t *testing.T) {
	p := newMergePilot(t)
	p.switchMerge(true)
	p.renew(fillAsimov)
	p.edit(fillKing)
	a := p.pilot()
	ctx := context.Background()

	require.True(t, p.tick(a))
	first := p.failPass(services.AuthorEntityBuildErrorCensus)
	assert.False(t, p.tick(a), "a pass right after a census failure")
	st := p.state()
	assert.Equal(t, database.AuthorAutopilotStateBuildBackoff, noteOf(st.State))
	assert.Equal(t, first, *st.StateRunID)
	assert.Equal(t, services.AutopilotMergeBuildDue, noteOf(st.MergeNote), "the merge step ran beside a pass to do")
	assert.Zero(t, p.mergeRuns())
	status, err := services.ReadAuthorAutopilotStatus(ctx, p.e.db, autopilotQuiet)
	require.NoError(t, err)
	assert.Equal(t, 1, status.CensusFailures)
	require.NotNil(t, status.CensusRetryAt)
	assert.WithinDuration(t, p.clock.now().Add(5*time.Minute), *status.CensusRetryAt, time.Second)

	p.clock.advance(5*time.Minute - time.Second)
	assert.False(t, p.tick(a))
	p.clock.advance(time.Second)
	require.True(t, p.tick(a))
	p.failPass(services.AuthorEntityBuildErrorCensus)
	p.clock.advance(5 * time.Minute)
	assert.False(t, p.tick(a), "the second failure waits ten minutes")
	p.clock.advance(5 * time.Minute)
	require.True(t, p.tick(a))
	p.buildStarted()
	status, err = services.ReadAuthorAutopilotStatus(ctx, p.e.db, autopilotQuiet)
	require.NoError(t, err)
	assert.Zero(t, status.CensusFailures)
	assert.Nil(t, status.CensusRetryAt)

	require.True(t, p.tick(a))
	p.mergeStarted(2)
}

// The card's block shows the merge step's switch.
// A pass held for an administrator is not retried: the autopilot starts no
// pass and no merge run however long it waits, the card names the decisions,
// and an administrator's pass ends the hold (D5).
func TestAHeldPassWaitsForAnAdministrator(t *testing.T) {
	p := newMergePilot(t)
	p.switchMerge(true)
	p.renew(fillAsimov)
	p.edit(fillKing)
	a := p.pilot()
	ctx := context.Background()

	require.True(t, p.tick(a))
	held := p.failPass(services.AuthorEntityBuildErrorDecisionsHeld)
	p.c.exec(`UPDATE author_entity_build_run SET report = '{"applied_not_same":[7,9]}' WHERE id = ?`, held)
	for range 3 {
		assert.False(t, p.tick(a), "a held pass was retried")
		st := p.state()
		assert.Equal(t, database.AuthorAutopilotStateBuildHeld, noteOf(st.State))
		assert.Equal(t, held, *st.StateRunID)
		assert.Equal(t, services.AutopilotMergeBuildHeld, noteOf(st.MergeNote))
		p.clock.advance(24 * time.Hour)
	}
	assert.Zero(t, p.mergeRuns())
	status, err := services.ReadAuthorAutopilotStatus(ctx, p.e.db, autopilotQuiet)
	require.NoError(t, err)
	assert.Equal(t, []int64{7, 9}, status.HeldDecisions)

	p.c.build()
	status, err = services.ReadAuthorAutopilotStatus(ctx, p.e.db, autopilotQuiet)
	require.NoError(t, err)
	assert.Empty(t, status.HeldDecisions)
	assert.True(t, p.tick(a), "the autopilot goes on after the administrator's pass")
}

func TestTheAutopilotStatusShowsTheMergeSwitch(t *testing.T) {
	p := newMergePilot(t)
	ctx := context.Background()
	admin := services.NewAuthorEntityAdmin(p.e.db)
	status, err := admin.SetAutopilotMerge(ctx, true, 0)
	require.NoError(t, err)
	assert.True(t, status.MergeEnabled)
	assert.True(t, status.Enabled, "the autopilot's own switch is apart")
	status, err = admin.SetAutopilotMerge(ctx, false, 0)
	require.NoError(t, err)
	assert.False(t, status.MergeEnabled)
	assert.False(t, errors.Is(err, database.ErrAuthorAutopilotOff))
}

// The hold keeps speaking for as long as it lasts, at the pace the alert's
// window expects, and falls silent with the administrator's pass.
func TestAHeldPassKeepsAnnouncingItself(t *testing.T) {
	p := newMergePilot(t)
	p.renew(fillAsimov)
	p.edit(fillKing)
	a := p.pilot()
	require.True(t, p.tick(a))
	held := p.failPass(services.AuthorEntityBuildErrorDecisionsHeld)
	p.c.exec(`UPDATE author_entity_build_run SET report = '{"applied_not_same":[7,9]}' WHERE id = ?`, held)

	logs := captureLogs(t)
	lines := func() int { return strings.Count(logs.String(), "msg=author_metadata.autopilot_held") }
	p.tick(a)
	require.Equal(t, 1, lines(), "the hold is announced at once")
	assert.Contains(t, logs.String(), "count=2")
	assert.Contains(t, logs.String(), "level=warning")

	p.clock.advance(5 * time.Minute)
	p.tick(a)
	assert.Equal(t, 1, lines(), "not on every tick")

	p.clock.advance(2 * time.Hour)
	p.tick(a)
	assert.Equal(t, 2, lines(), "again once the hour has passed")

	p.c.build()
	p.clock.advance(2 * time.Hour)
	p.tick(a)
	assert.Equal(t, 2, lines(), "an administrator's pass ends it")
}
