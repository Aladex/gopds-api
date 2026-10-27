package services_test

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/internal/migrate"
	"gopds-api/internal/scanfixture"
	"gopds-api/internal/testdb"
	"gopds-api/models"
	"gopds-api/services"
)

// The author autopilot (migration 62), a level-triggered reconciler, on the
// real scanner's catalog: it waits for the scan to be quiet, resolves the
// open inputs of the registered strata never asked, builds the entities once
// the selections changed, does nothing while any run is active, and
// converges whatever the order of commits and of an administrator's starts —
// on a clock the test moves, with the LLM endpoint faked.

const autopilotQuiet = 30 * time.Minute

// autopilotClock is the autopilots' clock, moved by the test.
type autopilotClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *autopilotClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *autopilotClock) set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = at
}

func (c *autopilotClock) advance(d time.Duration) { c.set(c.now().Add(d)) }

type autopilotEnv struct {
	*llmEnv
	clock *autopilotClock
	// admin, when set, is the LLM admin the autopilot is given instead of
	// the configuration's.
	admin *services.AuthorLLMAdmin
}

func newAutopilotEnv(t *testing.T) *autopilotEnv {
	t.Helper()
	return &autopilotEnv{llmEnv: newLLMEnv(t), clock: &autopilotClock{at: time.Now()}}
}

// autopilot is one pod's worker: a minute of backoff doubling up to eight.
func (e *autopilotEnv) autopilot() *services.AuthorAutopilot {
	return services.NewAuthorAutopilot(e.db, func(context.Context) (*services.AuthorLLMAdmin, error) {
		if e.admin != nil {
			return e.admin, nil
		}
		return e.llmAdmin(), nil
	}, services.AuthorAutopilotConfig{Quiet: autopilotQuiet, Lease: 2 * time.Minute, Backoff: time.Minute,
		MaxBackoff: 8 * time.Minute, Now: e.clock.now})
}

// tick runs one tick of a pod and reports whether it started a run.
func (e *autopilotEnv) tick(a *services.AuthorAutopilot) bool {
	e.t.Helper()
	started, err := a.RunOnce(context.Background())
	require.NoError(e.t, err)
	return started
}

// state is the autopilot's row.
func (e *autopilotEnv) state() *database.AuthorAutopilotState {
	e.t.Helper()
	st, err := database.GetAuthorAutopilot(context.Background(), e.db)
	require.NoError(e.t, err)
	return st
}

// newestSnapshotAt is when the newest snapshot was written.
func (e *autopilotEnv) newestSnapshotAt() time.Time {
	e.t.Helper()
	var at time.Time
	_, err := e.db.QueryOne(pg.Scan(&at), `SELECT max(created_at) FROM book_metadata_snapshot`)
	require.NoError(e.t, err)
	return at
}

// settle moves the clock past the quiet period after the newest snapshot.
func (e *autopilotEnv) settle() { e.clock.set(e.newestSnapshotAt().Add(autopilotQuiet + time.Second)) }

// scanBooks scans one fixture book per author, numbered from first, as a
// new archive; the local normalizer has not run on them yet.
func (e *autopilotEnv) scanBooks(name string, first int, authors ...[3]string) {
	e.t.Helper()
	path := filepath.Join(e.archives, name)
	writeEvalArchive(e.t, path, authors, first)
	scanArchive(e.t, e.llmEnv, path)
}

// registerInitials passes initials.cyrl in a stage-0 eval and completes the
// card's resolve pilot on it, asking at most pilot inputs (0: all).
func (e *autopilotEnv) registerInitials(pilot int) {
	e.t.Helper()
	ctx := context.Background()
	e.completedEvalOfMode(e.t, models.AuthorLLMEvalModeStage0, []llmeval.StratumDecision{
		passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")})
	run, err := e.llmAdmin().Start(ctx, "resolve", "pilot", e.adminUser())
	require.NoError(e.t, err)
	if pilot == 0 {
		e.resolveUntilDone(run.ID)
		return
	}
	d := services.NewAuthorLLMResolveDriver(e.db, &e.identity, e.version, services.AuthorLLMResolveSizes{Pilot: pilot},
		time.Second)
	w := e.worker()
	for range 200 {
		_, err = d.Step(ctx)
		require.NoError(e.t, err)
		_, err = w.RunOnce(ctx)
		require.NoError(e.t, err)
		if _, status := e.runStage(run.ID); status == string(models.AuthorLLMRunCompleted) {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	e.t.Fatal("the pilot never completed")
}

// newInitials are a later scan's books by authors of initials, which the
// local normalizer leaves to review.
var newInitials = [][3]string{{"П.", "Р.", "Голубев"}, {"С.", "Т.", "Виноградов"}}

// plainName is a later scan's book by an author the local policy selects.
var plainName = [3]string{"Олег", "", "Новиков"}

// withNewBooks is a catalog whose initials the card registered and resolved
// (and whose first entities a pass built), and a later scan of authors that
// is quiet.
func withNewBooks(t *testing.T, authors ...[3]string) *autopilotEnv {
	t.Helper()
	e := newAutopilotEnv(t)
	ingestEvalCatalogue(t, e.llmEnv)
	e.registerInitials(0)
	e.adminBuild()
	e.scanBooks("new.zip", 100, authors...)
	drainLocal(t, e.db)
	e.settle()
	return e
}

// adminBuild runs an administrator's entity pass to completion.
func (e *autopilotEnv) adminBuild() {
	e.t.Helper()
	run, err := services.NewAuthorEntityAdmin(e.db).Start(context.Background(), e.adminUser())
	require.NoError(e.t, err)
	e.buildUntilDone(run.ID)
}

// autopilotRuns counts the resolve runs no administrator started.
func (e *autopilotEnv) autopilotRuns() int {
	return e.count(`SELECT count(*) FROM author_llm_run WHERE kind = 'resolve' AND created_by_user_id IS NULL`)
}

// autopilotBuilds counts the entity passes no administrator started.
func (e *autopilotEnv) autopilotBuilds() int {
	return e.count(`SELECT count(*) FROM author_entity_build_run WHERE created_by_user_id IS NULL`)
}

// buildUntilDone runs an entity build worker until the pass completes.
func (e *autopilotEnv) buildUntilDone(run int64) {
	e.t.Helper()
	runToCompletion(e.t, services.NewAuthorEntityBuildLoop(e.db, services.AuthorEntityBuildConfig{}), e.db, run)
}

// entityOf is the entity the author credit with the last name is assigned
// to; "" when it has none.
func (e *autopilotEnv) entityOf(last string) string {
	e.t.Helper()
	var ids []string
	_, err := e.db.Query(&ids, `
		SELECT a.entity_id::text FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
		JOIN author_entity_assignment a ON a.source_fingerprint = c.source_fingerprint
			AND a.extractor_version = s.extractor_version
		WHERE c.role = 'author' AND c.source_last_name = ?`, last)
	require.NoError(e.t, err)
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

// converge ticks a pod, running every run it starts to its end, until a
// tick starts nothing and nothing is active; it returns the runs started.
func (e *autopilotEnv) converge(a *services.AuthorAutopilot) []string {
	e.t.Helper()
	var started []string
	for range 12 {
		if !e.tick(a) {
			st := e.state()
			if deref(st.State) != database.AuthorAutopilotStateLLMRunActive &&
				deref(st.State) != database.AuthorAutopilotStateEntityBuildActive {
				return started
			}
			e.t.Fatalf("the autopilot waits for run %d of another (%s)", deref(st.StateRunID), deref(st.State))
		}
		st := e.state()
		started = append(started, deref(st.LastAction))
		switch deref(st.LastAction) {
		case database.AuthorAutopilotActionResolve:
			require.False(e.t, e.tick(a), "a run started while the resolve run runs")
			e.resolveUntilDone(*st.LastActionRunID)
		case database.AuthorAutopilotActionBuild:
			require.False(e.t, e.tick(a), "a run started while the entity pass runs")
			e.buildUntilDone(*st.LastActionRunID)
		}
	}
	e.t.Fatal("the autopilot never converged")
	return nil
}

// changeSelection changes one selection for real in its own transaction and
// returns its commit: the mark of a writer that commits later.
func (e *autopilotEnv) changeSelection(last string) (commit func()) {
	e.t.Helper()
	tx, err := e.db.Begin()
	require.NoError(e.t, err)
	res, err := tx.Exec(`UPDATE book_contributor_credit_selection s SET decided_at = decided_at + interval '1 second'
		FROM book_contributor_credit c WHERE c.id = s.credit_id AND c.source_last_name = ?`, last)
	require.NoError(e.t, err)
	require.Positive(e.t, res.RowsAffected())
	return func() { require.NoError(e.t, tx.Commit()) }
}

// The quiet gate: nothing while local normalization jobs wait or a snapshot
// is younger than the quiet period; then the tick acts.
func TestAuthorAutopilotWaitsForTheScanToBeQuiet(t *testing.T) {
	e := newAutopilotEnv(t)
	a := e.autopilot()
	e.scanBooks("first.zip", 0, evalAuthors[7:9]...)
	newest := e.newestSnapshotAt()

	e.clock.set(newest.Add(autopilotQuiet + time.Hour))
	assert.False(t, e.tick(a))
	assert.Equal(t, database.AuthorAutopilotStateNormalizing, deref(e.state().State))
	drainLocal(t, e.db)

	e.clock.set(newest.Add(autopilotQuiet - time.Millisecond))
	assert.False(t, e.tick(a))
	assert.Equal(t, database.AuthorAutopilotStateNotQuiet, deref(e.state().State))
	assert.Zero(t, e.autopilotBuilds())

	e.clock.set(newest.Add(autopilotQuiet))
	require.True(t, e.tick(a), "a quiet scan started nothing")
	st := e.state()
	assert.Equal(t, database.AuthorAutopilotStateBuildStarted, deref(st.State))
	assert.Equal(t, services.AutopilotResolveNoPilot, deref(st.ResolveNote))
}

// The whole way: the new books' inputs of the registered strata go to one
// full resolve run of no administrator's, registering nothing; nothing
// starts while it runs; then an entity pass; then nothing more, however
// often it ticks — the local worker re-running included.
func TestAuthorAutopilotResolvesTheNewBooksThenBuildsTheirEntities(t *testing.T) {
	e := withNewBooks(t, newInitials...)
	ctx := context.Background()
	registrations := e.count(`SELECT count(*) FROM author_acceptance_class`)
	for _, au := range newInitials {
		require.Equal(t, "review", e.selections()[au[2]].State, au[2])
	}
	a := e.autopilot()

	require.True(t, e.tick(a))
	st := e.state()
	require.Equal(t, database.AuthorAutopilotActionResolve, deref(st.LastAction))
	assert.Equal(t, len(newInitials), st.ResolveInputs)
	run, err := database.GetAuthorLLMRun(ctx, e.db, *st.LastActionRunID)
	require.NoError(t, err)
	assert.Equal(t, models.AuthorLLMRunResolve, run.Kind)
	assert.Equal(t, models.AuthorLLMEvalModeFull, run.Mode)
	assert.Nil(t, run.CreatedByUserID, "the autopilot's run is no administrator's")
	assert.Equal(t, []string{"initials.cyrl"}, keysOf(e.resolvePlanOf(t, run.ID).Strata))

	assert.False(t, e.tick(a), "a run started beside the resolve run")
	assert.Equal(t, database.AuthorAutopilotStateLLMRunActive, deref(e.state().State))
	assert.Equal(t, run.ID, deref(e.state().StateRunID))
	assert.Zero(t, e.autopilotBuilds())
	e.resolveUntilDone(run.ID)

	assert.Equal(t, []string{database.AuthorAutopilotActionBuild}, e.converge(a))
	for _, au := range newInitials {
		assert.Equal(t, "selected", e.selections()[au[2]].State, au[2])
		assert.NotEmpty(t, e.entityOf(au[2]), "%s has no entity", au[2])
	}
	assert.Equal(t, registrations, e.count(`SELECT count(*) FROM author_acceptance_class`),
		"the autopilot registered a pair")
	assert.Equal(t, database.AuthorAutopilotStateIdle, deref(e.state().State))
	assert.Equal(t, services.AutopilotResolveNothing, deref(e.state().ResolveNote))

	drainLocal(t, e.db)
	e.clock.advance(time.Hour)
	for range 3 {
		assert.False(t, e.tick(a), "the autopilot loops")
	}
	assert.Equal(t, 1, e.autopilotRuns())
	assert.Equal(t, 1, e.autopilotBuilds())
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// The registered strata's backlog at the deploy is what the autopilot is
// for: the first quiet tick resolves the inputs the card's pilot never
// asked, with no new scan.
func TestAuthorAutopilotResolvesTheRegisteredBacklog(t *testing.T) {
	e := newAutopilotEnv(t)
	ingestEvalCatalogue(t, e.llmEnv)
	e.registerInitials(2)
	unasked := len(initialsCyrl) - 2
	e.settle()
	a := e.autopilot()
	require.True(t, e.tick(a))
	st := e.state()
	require.Equal(t, database.AuthorAutopilotActionResolve, deref(st.LastAction))
	assert.Equal(t, unasked, st.ResolveInputs)
	e.resolveUntilDone(*st.LastActionRunID)
	e.converge(a)
	for _, last := range initialsCyrl {
		assert.NotEmpty(t, e.entityOf(last), last)
	}
	assert.Equal(t, 1, e.autopilotRuns())
}

// Whatever keeps the resolve from starting — the card's own limits, or
// nothing due — the tick says why, starts no LLM run and registers
// nothing, and the new books are built all the same.
func TestAuthorAutopilotSaysWhyItStartsNoResolveAndStillBuilds(t *testing.T) {
	cases := []struct {
		name, note string
		authors    [][3]string
		arrange    func(e *autopilotEnv)
	}{
		{name: "the LLM layer is off", note: services.AutopilotResolveLLMOff, arrange: func(e *autopilotEnv) {
			e.admin = services.NewAuthorLLMAdmin(e.db, "", services.AuthorLLMDisabledWorkersOff, &e.settings)
		}},
		{name: "a participant is paused", note: services.AutopilotResolveLLMPaused, arrange: func(e *autopilotEnv) {
			require.Equal(e.t, 1, e.count(`WITH p AS (UPDATE author_llm_provider_state SET paused_reason = 'auth',
				paused_at = now() WHERE config_version = ? AND slot = 'judge_b' RETURNING 1) SELECT count(*) FROM p`,
				e.version))
		}},
		{name: "the endpoint is paused", note: services.AutopilotResolveLLMPaused, arrange: func(e *autopilotEnv) {
			require.Equal(e.t, 1, e.count(`WITH p AS (UPDATE author_llm_endpoint_state e SET paused_reason = 'quota_exhausted',
				paused_at = now() FROM author_llm_config c WHERE c.base_url = e.base_url AND c.version = ? RETURNING 1)
				SELECT count(*) FROM p`, e.version))
		}},
		{name: "the budget is spent", note: services.AutopilotResolveBudget, arrange: func(e *autopilotEnv) {
			e.settings.Budget.OneOffTokens = int64(e.count(`SELECT coalesce(sum(tokens), 0) FROM author_llm_token_tally`))
		}},
		{name: "a newer eval failed the registered stratum", note: services.AutopilotResolveNoRegistered,
			arrange: func(e *autopilotEnv) {
				e.completedEvalOfMode(e.t, models.AuthorLLMEvalModeStage0, []llmeval.StratumDecision{
					{Stratum: "initials.cyrl"}, passedStratum("single_field.cyrl", "A", 1, "confirm")})
			}},
		// An administrator's full run asked the new books and was closed
		// before any answer: their review stays open, asked; nothing is due.
		{name: "everything was asked", note: services.AutopilotResolveNothing, arrange: func(e *autopilotEnv) {
			ctx := context.Background()
			run, err := e.llmAdmin().Start(ctx, "resolve", "full", e.adminUser())
			require.NoError(e.t, err)
			_, err = e.resolveDriver().Step(ctx)
			require.NoError(e.t, err)
			require.Positive(e.t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ?`, run.ID))
			e.endLLMRun(run.ID)
			for _, au := range newInitials {
				require.Equal(e.t, "review", e.selections()[au[2]].State)
			}
		}},
		{name: "no new book in a registered stratum", note: services.AutopilotResolveNothing,
			authors: [][3]string{plainName}, arrange: func(*autopilotEnv) {}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			authors := tc.authors
			if authors == nil {
				authors = newInitials
			}
			e := withNewBooks(t, authors...)
			tc.arrange(e)
			registrations := e.count(`SELECT count(*) FROM author_acceptance_class`)
			runs := e.count(`SELECT count(*) FROM author_llm_run`)
			a := e.autopilot()
			assert.Equal(t, []string{database.AuthorAutopilotActionBuild}, e.converge(a))
			assert.Equal(t, tc.note, deref(e.state().ResolveNote))
			assert.Equal(t, runs, e.count(`SELECT count(*) FROM author_llm_run`), "an LLM run was started")
			assert.Equal(t, registrations, e.count(`SELECT count(*) FROM author_acceptance_class`))
			// A name left to review gets no entity; one the local policy
			// selected does.
			for _, au := range authors {
				if e.selections()[au[2]].State == "selected" {
					assert.NotEmpty(t, e.entityOf(au[2]), "%s has no entity", au[2])
				}
			}
		})
	}
}

// Without the card's resolve pilot the autopilot never starts the chain
// itself — no eval, no pilot — and says so.
func TestAuthorAutopilotNeedsTheCardsResolvePilot(t *testing.T) {
	e := newAutopilotEnv(t)
	ingestEvalCatalogue(t, e.llmEnv)
	e.completedEvalOfMode(t, models.AuthorLLMEvalModeStage0, []llmeval.StratumDecision{
		passedStratum("initials.cyrl", "A", 1, "confirm", "restructure")})
	runs := e.count(`SELECT count(*) FROM author_llm_run`)
	e.settle()
	e.converge(e.autopilot())
	assert.Equal(t, services.AutopilotResolveNoPilot, deref(e.state().ResolveNote))
	assert.Equal(t, runs, e.count(`SELECT count(*) FROM author_llm_run`), "the autopilot started an LLM run")
	assert.Zero(t, e.count(`SELECT count(*) FROM author_acceptance_class WHERE source = 'llm_eval'`))
}

// The resolve run leaves out a stratum a newer eval passed and the card has
// not registered: the autopilot never extends the policy.
func TestAuthorAutopilotAsksOnlyTheRegisteredStrata(t *testing.T) {
	e := withNewBooks(t, newInitials...)
	e.completedEvalOfMode(t, models.AuthorLLMEvalModeStage2, []llmeval.StratumDecision{
		passedStratum("single_field.cyrl", "A", 1, "confirm", "restructure")})
	a := e.autopilot()
	require.True(t, e.tick(a))
	st := e.state()
	require.Equal(t, database.AuthorAutopilotActionResolve, deref(st.LastAction))
	assert.Equal(t, []string{"initials.cyrl"}, keysOf(e.resolvePlanOf(t, *st.LastActionRunID).Strata))
	assert.Zero(t, e.count(`SELECT count(*) FROM author_acceptance_class WHERE decision_class = 'llm.single_field'`))
}

// endLLMRun ends an administrator's LLM run as the card does: paused, then
// closed.
func (e *autopilotEnv) endLLMRun(run int64) {
	e.t.Helper()
	ctx := context.Background()
	_, err := e.llmAdmin().Pause(ctx, run)
	require.NoError(e.t, err)
	_, err = e.llmAdmin().Close(ctx, run, e.adminUser())
	require.NoError(e.t, err)
}

// While any LLM run or entity pass is active, whoever started it, a tick
// does nothing and says which run it waits for; after it ended the
// autopilot acts.
func TestAuthorAutopilotDoesNothingWhileARunIsActive(t *testing.T) {
	t.Run("an LLM run", func(t *testing.T) {
		e := withNewBooks(t, newInitials...)
		adminRun := e.run(&database.AuthorLLMRunSpec{Kind: models.AuthorLLMRunEval, Mode: models.AuthorLLMEvalModeDev})
		a := e.autopilot()
		for range 3 {
			assert.False(t, e.tick(a))
		}
		st := e.state()
		assert.Equal(t, database.AuthorAutopilotStateLLMRunActive, deref(st.State))
		assert.Equal(t, adminRun, deref(st.StateRunID))
		assert.Zero(t, e.autopilotRuns())
		assert.Zero(t, e.autopilotBuilds())
		assert.Zero(t, st.Failures, "waiting is no failure")
		e.endLLMRun(adminRun)
		assert.Equal(t, []string{database.AuthorAutopilotActionResolve, database.AuthorAutopilotActionBuild}, e.converge(a))
	})
	t.Run("an entity pass", func(t *testing.T) {
		e := withNewBooks(t, newInitials...)
		ctx := context.Background()
		adminRun, err := services.NewAuthorEntityAdmin(e.db).Start(ctx, e.adminUser())
		require.NoError(t, err)
		a := e.autopilot()
		for range 3 {
			assert.False(t, e.tick(a))
		}
		st := e.state()
		assert.Equal(t, database.AuthorAutopilotStateEntityBuildActive, deref(st.State))
		assert.Equal(t, adminRun.ID, deref(st.StateRunID))
		assert.Zero(t, e.autopilotRuns(), "a resolve run started beside an entity pass")
		e.buildUntilDone(adminRun.ID)
		assert.Equal(t, []string{database.AuthorAutopilotActionResolve, database.AuthorAutopilotActionBuild}, e.converge(a))
	})
}

// Round 1–2's lost events converge.
func TestAuthorAutopilotConvergesWhateverTheOrderOfCommits(t *testing.T) {
	// A writer that began before a pass started and commits after it
	// completed: its change is built by the next pass.
	t.Run("a commit after a pass", func(t *testing.T) {
		e := withNewBooks(t, plainName)
		a := e.autopilot()
		commit := e.changeSelection(plainName[2])
		assert.Equal(t, []string{database.AuthorAutopilotActionBuild}, e.converge(a))
		commit()
		assert.Equal(t, []string{database.AuthorAutopilotActionBuild}, e.converge(a), "the late change was not built")
		assert.Equal(t, 2, e.autopilotBuilds())
	})
	// A change that commits while a pass runs: the next pass builds it.
	t.Run("a commit during a pass", func(t *testing.T) {
		e := withNewBooks(t, plainName)
		a := e.autopilot()
		require.True(t, e.tick(a))
		pass := *e.state().LastActionRunID
		e.changeSelection(plainName[2])()
		e.buildUntilDone(pass)
		assert.Equal(t, []string{database.AuthorAutopilotActionBuild}, e.converge(a))
		assert.Equal(t, 2, e.autopilotBuilds())
	})
	// A pass that fails gives its changes back: a later tick builds them.
	t.Run("a failed pass", func(t *testing.T) {
		e := withNewBooks(t, plainName)
		a := e.autopilot()
		require.True(t, e.tick(a))
		pass := *e.state().LastActionRunID
		_, err := e.db.Exec(`UPDATE author_entity_build_run SET status = 'failed', last_error_class = 'internal',
			finished_at = now() WHERE id = ?`, pass)
		require.NoError(t, err)
		assert.Equal(t, []string{database.AuthorAutopilotActionBuild}, e.converge(a), "the failed pass's changes were lost")
		assert.NotEmpty(t, e.entityOf(plainName[2]))
	})
	// An administrator's resolve run that starts while the autopilot's pass
	// runs and selects the new books: they are built after it.
	t.Run("an administrator's LLM run during a pass", func(t *testing.T) {
		e := withNewBooks(t, newInitials...)
		ctx := context.Background()
		e.admin = services.NewAuthorLLMAdmin(e.db, "", services.AuthorLLMDisabledWorkersOff, &e.settings)
		a := e.autopilot()
		require.True(t, e.tick(a))
		pass := *e.state().LastActionRunID
		adminRun, err := e.llmAdmin().Start(ctx, "resolve", "full", e.adminUser())
		require.NoError(t, err)
		assert.False(t, e.tick(a))
		e.buildUntilDone(pass)
		assert.False(t, e.tick(a), "a pass started beside an administrator's LLM run")
		assert.Equal(t, database.AuthorAutopilotStateLLMRunActive, deref(e.state().State))
		e.resolveUntilDone(adminRun.ID)
		assert.Equal(t, []string{database.AuthorAutopilotActionBuild}, e.converge(a))
		for _, au := range newInitials {
			assert.Equal(t, "selected", e.selections()[au[2]].State, au[2])
			assert.NotEmpty(t, e.entityOf(au[2]), au[2])
		}
	})
	// An administrator's pass after the autopilot's resolve run takes its
	// changes: the autopilot has nothing left to build.
	t.Run("an administrator's pass after the resolve", func(t *testing.T) {
		e := withNewBooks(t, newInitials...)
		a := e.autopilot()
		require.True(t, e.tick(a))
		e.resolveUntilDone(*e.state().LastActionRunID)
		e.adminBuild()
		assert.Empty(t, e.converge(a), "the autopilot built again what the administrator's pass built")
		for _, au := range newInitials {
			assert.NotEmpty(t, e.entityOf(au[2]), au[2])
		}
	})
}

// An input whose answer fails transiently is asked again with the next run,
// but makes no run due on its own: no run after run.
func TestAuthorAutopilotDoesNotLoopOnATransientFailure(t *testing.T) {
	e := withNewBooks(t, newInitials...)
	e.transientWindow = 50 * time.Millisecond
	var mu sync.Mutex
	down := true
	e.fake.setRespond(judgeADownForTokens([]string{newInitials[0][2]}, &mu, &down))
	a := e.autopilot()
	e.converge(a)
	require.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE source_fingerprint = ?
		AND outcome_reason = 'llm_judge_failed'`, e.fingerprintOf(newInitials[0][2])))
	e.clock.advance(time.Hour)
	for range 3 {
		assert.False(t, e.tick(a))
	}
	assert.Equal(t, 1, e.autopilotRuns(), "a transient failure started run after run")
}

// A switch-off that commits after a pod read the switch and before it
// writes refuses that write: no resolve run, no pass, no failure; and
// switched off the autopilot does not even plan.
func TestSwitchingTheAutopilotOff(t *testing.T) {
	for _, tc := range []struct {
		action  string
		authors [][3]string
	}{{"resolve", newInitials}, {"build", [][3]string{plainName}}} {
		t.Run("between the check and the "+tc.action, func(t *testing.T) {
			e := withNewBooks(t, tc.authors...)
			ctx := context.Background()
			a := e.autopilot()
			runs := e.count(`SELECT count(*) FROM author_llm_run`)
			switched := false
			restore := services.SetAuthorAutopilotHook(func(action string) error {
				if action == tc.action && !switched {
					switched = true
					require.NoError(t, services.SetAuthorAutopilot(ctx, e.db, false, e.adminUser()))
				}
				return nil
			})
			defer restore()
			started, err := a.RunOnce(ctx)
			require.NoError(t, err)
			require.True(t, switched, "the tick did not get there")
			assert.False(t, started)
			assert.Equal(t, runs, e.count(`SELECT count(*) FROM author_llm_run`), "an LLM run was started")
			assert.Zero(t, e.autopilotBuilds(), "a pass was started")
			assert.Zero(t, e.state().Failures)
		})
	}
	t.Run("off", func(t *testing.T) {
		e := withNewBooks(t, newInitials...)
		ctx := context.Background()
		a := e.autopilot()
		planned := 0
		restore := services.SetAuthorAutopilotHook(func(string) error {
			planned++
			return nil
		})
		defer restore()
		require.NoError(t, services.SetAuthorAutopilot(ctx, e.db, false, e.adminUser()))
		for range 3 {
			assert.False(t, e.tick(a))
		}
		assert.Zero(t, planned, "the autopilot planned while off")
		assert.Zero(t, e.autopilotRuns())
		require.NoError(t, services.SetAuthorAutopilot(ctx, e.db, true, e.adminUser()))
		assert.True(t, e.tick(a))
	})
}

// A failed tick is recorded and the next one waits for a backoff that
// doubles; a tick that goes through clears it.
func TestAFailedAutopilotTickBacksOff(t *testing.T) {
	e := withNewBooks(t, plainName)
	ctx := context.Background()
	a := e.autopilot()
	attempts := 0
	failing := true
	restore := services.SetAuthorAutopilotHook(func(string) error {
		attempts++
		if failing {
			return errors.New("the tick failed")
		}
		return nil
	})
	defer restore()
	fail := func() *database.AuthorAutopilotState {
		t.Helper()
		started, err := a.RunOnce(ctx)
		require.Error(t, err)
		assert.False(t, started)
		return e.state()
	}
	st := fail()
	assert.Equal(t, 1, st.Failures)
	assert.Equal(t, "internal", deref(st.LastErrorClass))
	require.NotNil(t, st.NextAttemptAt)
	assert.WithinDuration(t, e.clock.now().Add(time.Minute), *st.NextAttemptAt, time.Millisecond)
	assert.False(t, e.tick(a), "a failed tick was retried before its backoff")
	assert.Equal(t, 1, attempts)

	e.clock.advance(time.Minute)
	st = fail()
	assert.Equal(t, 2, st.Failures)
	assert.WithinDuration(t, e.clock.now().Add(2*time.Minute), *st.NextAttemptAt, time.Millisecond, "the backoff doubles")

	failing = false
	e.clock.advance(2 * time.Minute)
	require.True(t, e.tick(a))
	st = e.state()
	assert.Zero(t, st.Failures, "a tick that went through left the failure")
	assert.Nil(t, st.NextAttemptAt)
}

// Two pods tick at once, again and again, and after a lease ran out: never
// two runs, and the same convergence.
func TestAuthorAutopilotOnTwoPods(t *testing.T) {
	e := withNewBooks(t, newInitials...)
	ctx := context.Background()
	pods := []*services.AuthorAutopilot{e.autopilot(), e.autopilot()}
	race := func() {
		var wg sync.WaitGroup
		for _, p := range pods {
			wg.Add(1)
			go func(p *services.AuthorAutopilot) {
				defer wg.Done()
				for range 5 {
					_, err := p.RunOnce(ctx)
					assert.NoError(t, err)
				}
			}(p)
		}
		wg.Wait()
	}
	race()
	e.clock.advance(3 * time.Minute)
	race()
	assert.Equal(t, 1, e.autopilotRuns())
	e.resolveUntilDone(*e.state().LastActionRunID)
	race()
	e.clock.advance(3 * time.Minute)
	race()
	assert.Equal(t, 1, e.autopilotBuilds())
	e.buildUntilDone(*e.state().LastActionRunID)
	race()
	assert.Equal(t, 1, e.autopilotRuns())
	assert.Equal(t, 1, e.autopilotBuilds())
	assert.Equal(t, database.AuthorAutopilotStateIdle, deref(e.state().State))
}

// A pod's live lease lets no other pod tick; once it ran out the next one
// goes on from the state.
func TestAuthorAutopilotLeaseOutlivesAPod(t *testing.T) {
	e := withNewBooks(t, plainName)
	first, next := e.autopilot(), e.autopilot()
	require.True(t, e.tick(first))
	e.buildUntilDone(*e.state().LastActionRunID)
	e.changeSelection(plainName[2])()
	assert.False(t, e.tick(next), "a pod ticked under another one's lease")
	e.clock.advance(2*time.Minute + time.Second)
	assert.True(t, e.tick(next))
	assert.Equal(t, 2, e.autopilotBuilds())
}

// The resolve run and its record are one transaction: when the record is
// refused, no run is left and nothing is registered.
func TestTheAutopilotsRunIsUndoneWithItsRecord(t *testing.T) {
	e := withNewBooks(t, newInitials...)
	runs := e.count(`SELECT count(*) FROM author_llm_run`)
	refused := errors.New("the autopilot was switched off")
	res, err := e.llmAdmin().StartAutopilotResolve(context.Background(),
		func(*pg.Tx, int64, int) error { return refused })
	require.ErrorIs(t, err, refused)
	assert.Nil(t, res)
	assert.Equal(t, runs, e.count(`SELECT count(*) FROM author_llm_run`))
}

// A start on registered pairs only refuses a plan holding a stratum the
// configuration has not registered: no run, no registration.
func TestARegisteredOnlyStartRefusesAnUnregisteredStratum(t *testing.T) {
	e := withNewBooks(t, newInitials...)
	ctx := context.Background()
	ev, err := database.LatestAuthorLLMResolveEval(ctx, e.db, e.version)
	require.NoError(t, err)
	runs, pairs := e.count(`SELECT count(*) FROM author_llm_run`), e.count(`SELECT count(*) FROM author_acceptance_class`)
	_, _, err = database.StartAuthorLLMResolveRun(ctx, e.db, &database.AuthorLLMResolveStart{
		ConfigVersion: e.version, Mode: models.AuthorLLMEvalModeFull, Evidence: ev, RegisteredOnly: true,
		Settings: database.AuthorLLMResolveSettings{ReportID: ev.ReportID, Strata: map[string]database.AuthorLLMResolveStratum{
			"initials.cyrl":     {Class: "initials", Script: "Cyrl", Arm: "A", ItemsPerRequest: 1, Tiers: []string{"confirm"}},
			"single_field.cyrl": {Class: "single_field", Script: "Cyrl", Arm: "A", ItemsPerRequest: 1, Tiers: []string{"confirm"}},
		}}})
	require.ErrorIs(t, err, database.ErrAuthorLLMNotRegistered)
	assert.Equal(t, runs, e.count(`SELECT count(*) FROM author_llm_run`))
	assert.Equal(t, pairs, e.count(`SELECT count(*) FROM author_acceptance_class`))
}

// The card's block: the switch, the quiet period and until when the scan is
// not quiet, the jobs still waiting, a pass to do, and what the last tick
// found and did.
func TestTheAutopilotStatus(t *testing.T) {
	e := newAutopilotEnv(t)
	ctx := context.Background()
	e.scanBooks("first.zip", 0, evalAuthors[7:9]...)
	st, err := services.ReadAuthorAutopilotStatus(ctx, e.db, autopilotQuiet)
	require.NoError(t, err)
	assert.True(t, st.Enabled, "the autopilot is on by default")
	assert.Equal(t, int64(autopilotQuiet/time.Second), st.QuietSeconds)
	require.NotNil(t, st.QuietUntil)
	assert.WithinDuration(t, e.newestSnapshotAt().Add(autopilotQuiet), *st.QuietUntil, time.Millisecond)
	assert.Positive(t, st.WaitingJobs)
	assert.True(t, st.BuildPending, "the mark migration 62 seeds is no pass to do")
	assert.Zero(t, st.SkippedUnits)
	assert.Nil(t, st.SkippedRetryAt)

	drainLocal(t, e.db)
	st, err = services.ReadAuthorAutopilotStatus(ctx, e.db, autopilotQuiet)
	require.NoError(t, err)
	assert.Zero(t, st.WaitingJobs)
	assert.True(t, st.BuildPending, "new selections leave no pass to do")

	e.settle()
	require.True(t, e.tick(e.autopilot()))
	st, err = services.ReadAuthorAutopilotStatus(ctx, e.db, autopilotQuiet)
	require.NoError(t, err)
	assert.False(t, st.BuildPending, "the pass took nothing")
	assert.Equal(t, database.AuthorAutopilotStateBuildStarted, deref(st.State))
	assert.Equal(t, database.AuthorAutopilotActionBuild, deref(st.LastAction))
}

// untakenMarks counts the marks no pass took: the passes to do.
func (e *autopilotEnv) untakenMarks() int {
	return e.count(`SELECT count(*) FROM author_autopilot_mark WHERE build_run_id IS NULL`)
}

// The selection marks (migration 62): a transaction that changed a
// selection leaves one, visible once it committed; one that changed none
// leaves none; a pass takes the visible ones as it starts and leaves a later
// commit's; a completed pass's are dropped and a failed one's given back.
func TestTheSelectionMarks(t *testing.T) {
	e := newAutopilotEnv(t)
	ctx := context.Background()
	seeded := e.untakenMarks()
	e.scanBooks("first.zip", 0, evalAuthors[7:9]...)
	drainLocal(t, e.db)
	require.Greater(t, e.untakenMarks(), seeded, "new selections left no mark")

	entityAdmin := services.NewAuthorEntityAdmin(e.db)
	run, err := entityAdmin.Start(ctx, 0)
	require.NoError(t, err)
	assert.Zero(t, e.untakenMarks(), "the pass took nothing")

	_, err = e.db.Exec(`UPDATE book_contributor_credit_selection SET decided_at = decided_at`)
	require.NoError(t, err)
	_, err = e.db.Exec(`UPDATE book_contributor_credit_selection SET state = state WHERE false`)
	require.NoError(t, err)
	assert.Zero(t, e.untakenMarks(), "a statement that changed nothing marked")

	open := e.changeSelection(evalAuthors[7][2])
	assert.Zero(t, e.untakenMarks(), "an uncommitted change is visible")
	open()
	assert.Equal(t, 1, e.untakenMarks())

	e.buildUntilDone(run.ID)
	require.NoError(t, database.SettleAuthorAutopilotMarks(ctx, e.db))
	assert.Zero(t, e.count(`SELECT count(*) FROM author_autopilot_mark WHERE build_run_id IS NOT NULL`),
		"a completed pass's marks were kept")
	assert.Equal(t, 1, e.untakenMarks(), "the change committed during the pass was taken by it")

	failed, err := entityAdmin.Start(ctx, 0)
	require.NoError(t, err)
	assert.Zero(t, e.untakenMarks())
	_, err = e.db.Exec(`UPDATE author_entity_build_run SET status = 'failed', last_error_class = 'internal',
		finished_at = now() WHERE id = ?`, failed.ID)
	require.NoError(t, err)
	require.NoError(t, database.SettleAuthorAutopilotMarks(ctx, e.db))
	assert.Equal(t, 1, e.untakenMarks(), "a failed pass's marks were not given back")
}

// A local job of the normalizer version the release replaced waits for
// nothing — the re-normalization queues its input again under the current
// one — so it never holds the quiet gate.
func TestAuthorAutopilotIgnoresJobsOfAnOlderNormalizer(t *testing.T) {
	e := newAutopilotEnv(t)
	ctx := context.Background()
	e.scanBooks("first.zip", 0, evalAuthors[7:9]...)
	drainLocal(t, e.db)
	var in database.RenormalizeInput
	_, err := e.db.QueryOne(&in, `SELECT source_fingerprint, extractor_version FROM contributor_normalization_job LIMIT 1`)
	require.NoError(t, err)
	queued, err := database.QueueRenormalization(ctx, e.db, in, "authornorm-local-v1")
	require.NoError(t, err)
	require.True(t, queued)
	require.Equal(t, 1, e.count(`SELECT count(*) FROM contributor_normalization_job
		WHERE status IN ('pending', 'queued') AND normalizer_version = 'authornorm-local-v1'`))
	e.settle()
	assert.True(t, e.tick(e.autopilot()), "an old normalizer's job held the tick back")
}

// A run an administrator starts between the tick's check and its own start
// makes the tick start nothing more: an LLM run refuses the resolve start
// and holds the pass back; an entity pass refuses the autopilot's pass. A
// cross-kind start that lands in the same instant as the autopilot's pass
// overlaps it, harmlessly: the next ticks wait for both and build again.
func TestAuthorAutopilotStartsNothingBesideARunStartedMeanwhile(t *testing.T) {
	t.Run("an LLM run before the resolve start", func(t *testing.T) {
		e := withNewBooks(t, newInitials...)
		var adminRun int64
		restore := services.SetAuthorAutopilotHook(func(action string) error {
			if action == "resolve" && adminRun == 0 {
				adminRun = e.run(&database.AuthorLLMRunSpec{Kind: models.AuthorLLMRunEval, Mode: models.AuthorLLMEvalModeDev})
			}
			return nil
		})
		defer restore()
		assert.False(t, e.tick(e.autopilot()))
		assert.Zero(t, e.autopilotRuns())
		assert.Zero(t, e.autopilotBuilds(), "a pass started beside an LLM run")
		assert.Equal(t, database.AuthorAutopilotStateLLMRunActive, deref(e.state().State))
		assert.Equal(t, services.AutopilotResolveRunActive, deref(e.state().ResolveNote))
	})
	t.Run("an entity pass before the autopilot's", func(t *testing.T) {
		e := withNewBooks(t, plainName)
		ctx := context.Background()
		var adminRun int64
		restore := services.SetAuthorAutopilotHook(func(action string) error {
			if action == "build" && adminRun == 0 {
				run, err := services.NewAuthorEntityAdmin(e.db).Start(ctx, e.adminUser())
				require.NoError(t, err)
				adminRun = run.ID
			}
			return nil
		})
		defer restore()
		assert.False(t, e.tick(e.autopilot()))
		assert.Zero(t, e.autopilotBuilds())
		assert.Equal(t, database.AuthorAutopilotStateEntityBuildActive, deref(e.state().State))
		assert.Zero(t, e.state().Failures, "a pass started meanwhile is no failure")
	})
	t.Run("an LLM run with the autopilot's pass", func(t *testing.T) {
		e := withNewBooks(t, newInitials...)
		ctx := context.Background()
		e.admin = services.NewAuthorLLMAdmin(e.db, "", services.AuthorLLMDisabledWorkersOff, &e.settings)
		var adminRun int64
		restore := services.SetAuthorAutopilotHook(func(action string) error {
			if action == "build" && adminRun == 0 {
				run, err := e.llmAdmin().Start(ctx, "resolve", "full", e.adminUser())
				require.NoError(t, err)
				adminRun = run.ID
			}
			return nil
		})
		defer restore()
		a := e.autopilot()
		require.True(t, e.tick(a))
		pass := *e.state().LastActionRunID
		assert.False(t, e.tick(a))
		e.buildUntilDone(pass)
		e.resolveUntilDone(adminRun)
		assert.Equal(t, []string{database.AuthorAutopilotActionBuild}, e.converge(a))
		for _, au := range newInitials {
			assert.NotEmpty(t, e.entityOf(au[2]), au[2])
		}
	})
}

// scratchDBBefore62 is a scratch database migrated up to 61, installed as
// the database package's connection: the schema the binary before the
// autopilot runs on.
func scratchDBBefore62(t *testing.T) (db *pg.DB, migrateRest func()) {
	t.Helper()
	cfg, ok := testdb.Configured()
	if !ok || testing.Short() {
		t.Skip(testdb.SkipReason)
	}
	admin, err := testdb.Connect(cfg, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })
	name := fmt.Sprintf("autopilot_before_62_%d", time.Now().UnixNano())
	_, err = admin.Exec("CREATE DATABASE " + name)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)") })
	db = pg.Connect(&pg.Options{Addr: cfg.Host, User: cfg.User, Password: cfg.Password, Database: name})
	t.Cleanup(func() { _ = db.Close() })
	root := scanfixture.ModuleRoot()
	before := fstest.MapFS{}
	entries, err := os.ReadDir(filepath.Join(root, "database_migrations"))
	require.NoError(t, err)
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") || entry.Name() >= "62-" {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(root, "database_migrations", entry.Name()))
		require.NoError(t, readErr)
		before["database_migrations/"+entry.Name()] = &fstest.MapFile{Data: data}
	}
	ctx := context.Background()
	_, err = migrate.Run(ctx, db, before, "database_migrations", migrate.AppBaseline())
	require.NoError(t, err)
	_, err = db.Exec(`ALTER TABLE public.opds_catalog_genre ADD COLUMN IF NOT EXISTS title character varying NOT NULL DEFAULT ''`)
	require.NoError(t, err)
	previous := database.GetDB()
	database.SetDB(db)
	t.Cleanup(func() { database.SetDB(previous) })
	return db, func() {
		_, migrateErr := migrate.Run(ctx, db, os.DirFS(root), "database_migrations", migrate.AppBaseline())
		require.NoError(t, migrateErr)
	}
}

// A book selected after the binary before the autopilot completed its last
// pass, before migration 62 put the marks' triggers in: the mark the
// migration seeds builds it on the first quiet tick.
func TestTheAutopilotBuildsWhatWasSelectedBeforeItsMigration(t *testing.T) {
	db, migrateRest := scratchDBBefore62(t)
	ctx := context.Background()
	dir := t.TempDir()
	scan := func(name string, first int, authors ...[3]string) {
		t.Helper()
		path := filepath.Join(dir, name)
		writeEvalArchive(t, path, authors, first)
		reader, err := zip.OpenReader(path)
		require.NoError(t, err)
		defer func() { _ = reader.Close() }()
		scanner := services.NewBookScanService(dir, t.TempDir(), services.NewLanguageDetector(false, 5*time.Second), true, nil)
		for _, file := range reader.File {
			_, err = scanner.ProcessBook(file, filepath.Base(path))
			require.NoError(t, err)
		}
		drainLocal(t, db)
	}
	scan("old.zip", 0, evalAuthors[7:9]...)
	// The old binary's pass: its own insert, and the pass worked to the end,
	// with no merge candidates (their tables come with 63).
	restore := services.WithoutAuthorEntityCandidates()
	var pass int64
	_, err := db.QueryOne(pg.Scan(&pass), `INSERT INTO public.author_entity_build_run (created_by_user_id) VALUES (NULL)
		RETURNING id`)
	require.NoError(t, err)
	runToCompletion(t, services.NewAuthorEntityBuildLoop(db, services.AuthorEntityBuildConfig{}), db, pass)
	restore()
	scan("later.zip", 100, plainName)

	migrateRest()
	clock := &autopilotClock{at: time.Now().Add(time.Hour)}
	pilot := entityAutopilot(db, clock.now)
	started, err := pilot.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, started, "the selections made before the migration were never built")
	run, _, err := database.LatestAuthorEntityBuilds(ctx, db)
	require.NoError(t, err)
	runToCompletion(t, services.NewAuthorEntityBuildLoop(db, services.AuthorEntityBuildConfig{}), db, run.ID)
	var entities int
	_, err = db.QueryOne(pg.Scan(&entities), `
		SELECT count(*) FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
		JOIN author_entity_assignment a ON a.source_fingerprint = c.source_fingerprint
			AND a.extractor_version = s.extractor_version
		WHERE c.source_last_name = ?`, plainName[2])
	require.NoError(t, err)
	assert.Equal(t, 1, entities, "the book selected before the migration has no entity")
	started, err = pilot.RunOnce(ctx)
	require.NoError(t, err)
	assert.False(t, started, "the seeded mark started a second pass")
}
