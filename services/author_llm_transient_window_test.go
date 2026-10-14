package services_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gopds-api/config"
	"gopds-api/database"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/llm"
	"gopds-api/services"
)

// A slow but working provider does not fail items: run 16's judge, held to
// one concurrent call by a persistent 503, ran jobs past the old 45-minute
// window and 47 of them failed. With the default window a job keeps retrying
// through hours of failures and is answered when the provider recovers; the
// window is still finite.
func TestAuthorLLMDefaultWindowOutlastsHoursOfFailures(t *testing.T) {
	e := newLLMEnv(t)
	e.settings.MaxAttempts = 1
	run := e.run(&database.AuthorLLMRunSpec{})
	e.reviewPair(&run, "slow", givenFamily, 1)

	var mu sync.Mutex
	clock := time.Now()
	down := true
	at := func() *time.Time {
		mu.Lock()
		defer mu.Unlock()
		now := clock
		return &now
	}
	advance := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		clock = clock.Add(d)
	}
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		mu.Lock()
		failing := down
		mu.Unlock()
		if failing && !isFingerprint(req) {
			return fakeReply{Status: http.StatusServiceUnavailable, Body: `{"error":{"message":"busy"}}`}
		}
		return fakeReply{}
	})
	client := llm.NewClient(config.LLMConfig{BaseURL: e.srv.URL + "/v1", Timeout: 30 * time.Second})
	// No TransientWindow: the worker's default.
	w, err := services.NewAuthorLLMWorker(e.db, client, &services.AuthorLLMWorkerConfig{
		Settings: e.settings, Identity: e.identity, ConfigVersion: e.version, ArchivesDir: e.archives,
		Clock: at,
	})
	require.NoError(t, err)
	passChecks(t, w)

	failedJobs := func() int {
		return e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND status = 'failed'`, run)
	}
	attempt := func() {
		t.Helper()
		claim, _, err := w.Claim(context.Background(), llmreq.SlotProductionA)
		require.NoError(t, err)
		require.NotNil(t, claim, "the failing job is claimable again")
		require.NoError(t, w.Process(context.Background(), llmreq.SlotProductionA, claim))
	}

	// Six hours of failures, an attempt every hour: far past the old window.
	for range 6 {
		attempt()
		require.Zero(t, failedJobs(), "a transient failure within the window failed the job")
		advance(time.Hour)
	}

	// The provider recovers: the job is answered.
	mu.Lock()
	down = false
	mu.Unlock()
	attempt()
	require.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND slot = 'production_a'
		AND status = 'answered'`, run))

	// Still finite: a job failing for longer than the window ends.
	e.reviewPair(&run, "dead", givenFamily, 1)
	mu.Lock()
	down = true
	mu.Unlock()
	attempt()
	advance(25 * time.Hour)
	attempt()
	require.Equal(t, 1, failedJobs(), "a job failing past the window ended")
}
