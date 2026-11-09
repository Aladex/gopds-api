package database

import (
	"context"
	"sync"
	"time"

	"gopds-api/logging"
)

// variantsReadyTTL is how long a reader of the card read model trusts its
// answer about readiness; the answer only ever turns from false to true.
const variantsReadyTTL = 60 * time.Second

// VariantsReadiness remembers whether the card read model may serve cards
// (BookVariantsReady), asking at most once a minute. The site's lists and the
// bots each hold one. The zero value is ready to use.
type VariantsReadiness struct {
	mu      sync.Mutex
	ready   bool
	checked time.Time
}

// Ready says whether cards may be served, asking check when the last answer
// is older than a minute. A failed check is not ready, and is asked again
// next time: the caller falls back to files.
func (r *VariantsReadiness) Ready(ctx context.Context, check func(context.Context) (bool, error)) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.checked.IsZero() && time.Since(r.checked) < variantsReadyTTL {
		return r.ready
	}
	ready, err := check(ctx)
	if err != nil {
		logging.Warnf("book variants readiness: %v", err)
		return false
	}
	r.ready, r.checked = ready, time.Now()
	return ready
}
