package services_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression from the phase-3 review, round 2 (R2-B1): a pause that lands
// between the INSERTs of a stage's enqueue must not leave the stage with part
// of its jobs.

// firstInsertGate holds the driver's connection right after the first job
// INSERT of one purpose, while its transaction is still open.
type firstInsertGate struct {
	purpose string
	reached chan struct{}
	release chan struct{}
	once    sync.Once
}

func newFirstInsertGate(purpose string) *firstInsertGate {
	return &firstInsertGate{purpose: purpose, reached: make(chan struct{}), release: make(chan struct{})}
}

func (g *firstInsertGate) BeforeQuery(ctx context.Context, _ *pg.QueryEvent) (context.Context, error) {
	return ctx, nil
}

func (g *firstInsertGate) AfterQuery(_ context.Context, ev *pg.QueryEvent) error {
	if ev.Err != nil {
		return nil
	}
	q, err := ev.FormattedQuery()
	if err != nil {
		return nil
	}
	s := string(q)
	if strings.Contains(s, "INSERT INTO author_llm_job") && strings.Contains(s, "'"+g.purpose+"'") {
		g.once.Do(func() {
			close(g.reached)
			<-g.release
		})
	}
	return nil
}

// jobsByPurpose counts the run's jobs per purpose.
func (e *llmEnv) jobsByPurpose() map[string]int {
	e.t.Helper()
	var rows []struct {
		Purpose string `pg:"purpose"`
		N       int    `pg:"n"`
	}
	_, err := e.db.Query(&rows, `SELECT purpose, count(*) AS n FROM author_llm_job GROUP BY purpose`)
	require.NoError(e.t, err)
	out := map[string]int{}
	for _, r := range rows {
		out[r.Purpose] = r.N
	}
	return out
}

// stepTo steps a plain driver and worker until the run is at stage, before
// that stage enqueued anything.
func (e *llmEnv) stepTo(run int64, stage string) {
	e.t.Helper()
	ctx := context.Background()
	driver := services.NewAuthorLLMEvalDriver(e.db, &e.identity, e.version, evalSizes, e.archives, nil, time.Second)
	w := e.worker()
	for range 400 {
		if now, _ := e.runStage(run); now == stage {
			return
		}
		_, err := driver.Step(ctx)
		require.NoError(e.t, err)
		_, err = w.RunOnce(ctx)
		require.NoError(e.t, err)
	}
	e.t.Fatalf("the run never reached %s", stage)
}

// pauseWaitsOrLands pauses the run from another connection and returns once
// the pause either committed or is waiting on a lock.
func (e *llmEnv) pauseWaitsOrLands(run int64) <-chan error {
	e.t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- database.PauseAuthorLLMRun(context.Background(), e.db, run, models.AuthorLLMRunPausedAdmin)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			out := make(chan error, 1)
			out <- err
			return out
		default:
		}
		if e.count(`SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE 'UPDATE author_llm_run SET status = ''paused''%'`) > 0 {
			return done
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatal("the pause neither committed nor waited")
	return done
}

func TestAuthorLLMEvalPauseBetweenEnqueueInsertsLosesNoJobs(t *testing.T) {
	// The reference: the same run with no pause in an enqueue.
	ref := newLLMEnv(t)
	ingestEvalCatalogue(t, ref)
	ref.fake.setRespond(byFields(distinctLabs))
	settings := services.DefaultAuthorLLMEvalSettings()
	refRun, err := services.StartAuthorLLMEval(context.Background(), ref.db, ref.version, models.AuthorLLMEvalModeFull, &settings, nil)
	require.NoError(t, err)
	ref.evalUntilDone(refRun)
	want := ref.jobsByPurpose()
	require.Equal(t, 12, want[string(models.AuthorLLMPurposeEvalFingerprint)], "three repeats of four participants")

	for _, tc := range []struct {
		stage   string
		purpose models.AuthorLLMJobPurpose
	}{
		{"fingerprint", models.AuthorLLMPurposeEvalFingerprint},
		{"anchor", models.AuthorLLMPurposeEvalAnchor},
		{"judge", models.AuthorLLMPurposeEvalJudge},
		{"candidates", models.AuthorLLMPurposeEvalCandidate},
		{"repeat", models.AuthorLLMPurposeEvalRepeat},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			e := newLLMEnv(t)
			ingestEvalCatalogue(t, e)
			e.fake.setRespond(byFields(distinctLabs))
			ctx := context.Background()
			run, err := services.StartAuthorLLMEval(ctx, e.db, e.version, models.AuthorLLMEvalModeFull, &settings, nil)
			require.NoError(t, err)
			e.stepTo(run, tc.stage)
			require.Zero(t, e.jobsByPurpose()[string(tc.purpose)])

			// The driver's own connection, held after its first INSERT.
			hooked := pg.Connect(e.db.Options())
			t.Cleanup(func() { _ = hooked.Close() })
			gate := newFirstInsertGate(string(tc.purpose))
			hooked.AddQueryHook(gate)
			driver := services.NewAuthorLLMEvalDriver(hooked, &e.identity, e.version, evalSizes, e.archives, nil, time.Second)
			stepped := make(chan error, 1)
			go func() {
				_, stepErr := driver.Step(ctx)
				stepped <- stepErr
			}()
			select {
			case <-gate.reached:
			case <-time.After(30 * time.Second):
				t.Fatal("the stage never enqueued")
			}
			paused := e.pauseWaitsOrLands(run)
			close(gate.release)
			require.NoError(t, <-stepped)
			require.NoError(t, <-paused)
			_, status := e.runStage(run)
			require.Equal(t, string(models.AuthorLLMRunPaused), status)

			// All of the stage's jobs, or none of them.
			got := e.jobsByPurpose()[string(tc.purpose)]
			assert.Contains(t, []int{0, want[string(tc.purpose)]}, got, "a pause split the %s enqueue", tc.stage)

			require.NoError(t, database.ResumeAuthorLLMRun(ctx, e.db, run))
			stages := e.evalUntilDone(run)
			assert.Equal(t, tc.stage, stages[0])
			assert.Equal(t, "report", stages[len(stages)-1])
			assert.Equal(t, want, e.jobsByPurpose(), "the resumed run is missing work")
			r := downloadReport(t, e, run)
			for _, reason := range r.Evaluation.Decision.Reasons {
				assert.Regexp(t, `^(anchor|repeat) evidence: `, reason)
			}
		})
	}
}
