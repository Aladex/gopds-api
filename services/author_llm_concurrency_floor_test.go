package services_test

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/internal/authornorm/llmreq"

	"github.com/stretchr/testify/require"
)

// concurrency_min is the floor of the AIMD window: a transient failure halves
// the window down to it, never below, and a window found below it (stored
// before the floor was raised) is lifted to it by the next settle. The
// default floor of 1 halves as before.
func TestAuthorLLMConcurrencyFloor(t *testing.T) {
	for _, tc := range []struct {
		name               string
		floor, window, got int
	}{
		{"default floor halves", 1, 4, 2},
		{"default floor keeps one", 1, 1, 1},
		{"halving stops at the floor", 6, 8, 6},
		{"a window below the floor is lifted", 6, 1, 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newLLMEnv(t)
			e.settings.ConcurrencyMin = tc.floor
			run := e.productionRun(e.version)
			const answers = 55
			for i := range answers {
				e.reviewPair(&run, fmt.Sprintf("floor-%d", i), givenFamily, 1)
			}
			// The first job call of production_a is a 5xx, as the gateway's.
			var failed atomic.Bool
			e.fake.setRespond(checked(func(req *fakeRequest) fakeReply {
				if req.Model == "gpt-6-luna" && failed.CompareAndSwap(false, true) {
					return fakeReply{Status: http.StatusInternalServerError, Body: `{"error":{"message":"internal"}}`}
				}
				return fakeReply{}
			}))
			w := e.worker()
			passChecks(t, w)
			e.exec(`UPDATE author_llm_provider_state SET concurrency = ? WHERE config_version = ? AND slot = ?`,
				tc.window, e.version, string(llmreq.SlotProductionA))

			claimProcess(t, w, llmreq.SlotProductionA)
			require.True(t, failed.Load())
			require.Equal(t, tc.got, e.state(llmreq.SlotProductionA).Concurrency)

			// Answers lift a window of 1 to the floor and raise it from there:
			// one step per 50 in a row.
			e.exec(`UPDATE author_llm_provider_state SET concurrency = 1 WHERE config_version = ? AND slot = ?`,
				e.version, string(llmreq.SlotProductionA))
			for e.count(`SELECT count(*) FROM author_llm_job WHERE slot = ? AND status = 'pending'`,
				string(llmreq.SlotProductionA)) > 0 {
				_, err := w.RunOnce(context.Background())
				require.NoError(t, err)
				time.Sleep(15 * time.Millisecond) // past the test backoff
			}
			require.Equal(t, tc.floor+1, e.state(llmreq.SlotProductionA).Concurrency)
		})
	}
}
