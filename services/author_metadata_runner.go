package services

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"gopds-api/config"
	"gopds-api/logging"

	"github.com/go-pg/pg/v10"
)

// The runner of the embedded author metadata workers (plan phase 15). It owns
// every worker goroutine: main builds the workers, hands them over, and stops
// them all through one Shutdown before the database pool closes. Main knows
// nothing of stage loops, and a new stage — the phase-8 extraction worker —
// plugs in as one more AuthorMetadataStageWorker without a change here.
//
// No state lives in the process: a worker's progress is its leases in the
// database, so a stopped or crashed process leaves at most leases, which
// expire and are claimed again by the next start.

var (
	// ErrAuthorMetadataRunnerStarted marks a second Start of one runner.
	ErrAuthorMetadataRunnerStarted = errors.New("services: author metadata runner already started")
	// ErrAuthorMetadataRunnerShutdownTimeout marks a shutdown whose deadline
	// passed before every worker returned.
	ErrAuthorMetadataRunnerShutdownTimeout = errors.New("services: author metadata workers did not stop in time")
)

// AuthorMetadataStageWorker is one long-running worker the runner owns.
type AuthorMetadataStageWorker interface {
	// Name is a fixed label for logs (stage and index), never data.
	Name() string
	// Run works until ctx is canceled and then returns. A worker that
	// returns early stops only itself.
	Run(ctx context.Context) error
}

// AuthorMetadataRunner starts and stops a fixed set of workers.
type AuthorMetadataRunner struct {
	workers []AuthorMetadataStageWorker

	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewAuthorMetadataRunner takes the workers it will own.
func NewAuthorMetadataRunner(workers ...AuthorMetadataStageWorker) *AuthorMetadataRunner {
	return &AuthorMetadataRunner{workers: workers}
}

// Start runs ready first — the database check — and starts every worker only
// once it passed. A runner starts once.
func (r *AuthorMetadataRunner) Start(ctx context.Context, ready func(context.Context) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return ErrAuthorMetadataRunnerStarted
	}
	if err := ready(ctx); err != nil {
		return fmt.Errorf("author metadata workers not started: %w", err)
	}
	// The workers outlive the caller's context: only Shutdown stops them.
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	r.started, r.cancel, r.done = true, cancel, make(chan struct{})

	var wg sync.WaitGroup
	for _, w := range r.workers {
		wg.Add(1)
		go func(w AuthorMetadataStageWorker) {
			defer wg.Done()
			if err := w.Run(runCtx); err != nil && runCtx.Err() == nil {
				// The error text is not logged: it may carry query details.
				logging.Errorf("author metadata worker %s stopped unexpectedly", w.Name())
			}
		}(w)
	}
	go func() {
		wg.Wait()
		close(r.done)
	}()
	logging.Infof("author metadata workers started: %d", len(r.workers))
	return nil
}

// Shutdown cancels every worker — their claims and database calls run under
// the canceled context — and waits until all returned or ctx ends, whichever
// is first. Only after it returned may the database pool close. It is a no-op
// for a runner that never started and safe to call again.
func (r *AuthorMetadataRunner) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return nil
	}
	r.cancel()
	done := r.done
	r.mu.Unlock()

	select {
	case <-done:
		logging.Info("author metadata workers stopped")
		return nil
	case <-ctx.Done():
		return ErrAuthorMetadataRunnerShutdownTimeout
	}
}

// AuthorMetadataLocalLoop drives one local normalization worker: a batch
// after a batch while there is work, a poll interval of rest when there is
// none or a batch failed.
type AuthorMetadataLocalLoop struct {
	name   string
	worker *AuthorMetadataLocalWorker
	poll   time.Duration
}

// NewAuthorMetadataLocalLoop wraps one worker.
func NewAuthorMetadataLocalLoop(name string, w *AuthorMetadataLocalWorker, poll time.Duration) *AuthorMetadataLocalLoop {
	return &AuthorMetadataLocalLoop{name: name, worker: w, poll: poll}
}

// Name is the loop's log label.
func (l *AuthorMetadataLocalLoop) Name() string { return l.name }

// RunOnce runs one batch of the underlying worker.
func (l *AuthorMetadataLocalLoop) RunOnce(ctx context.Context) (LocalBatchReport, error) {
	return l.worker.RunOnce(ctx)
}

// Run loops until ctx is canceled. A canceled batch leaves its claimed jobs
// leased; the leases expire and the jobs are claimed again.
func (l *AuthorMetadataLocalLoop) Run(ctx context.Context) error {
	timer := time.NewTimer(l.poll)
	defer timer.Stop()
	for {
		report, err := l.worker.RunOnce(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			logging.Warnf("author metadata worker %s: batch failed", l.name)
		} else if report.Claimed > 0 {
			continue
		}
		timer.Reset(l.poll)
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
	}
}

// AuthorMetadataLocalWorkerConfigFrom maps the configuration of the local
// stream onto the worker: claim size, lease and attempts from the config, the
// shipped normalizer, policy version and retry delays from the defaults.
func AuthorMetadataLocalWorkerConfigFrom(c *config.AuthorMetadataConfig) AuthorMetadataLocalWorkerConfig {
	cfg := DefaultAuthorMetadataLocalWorkerConfig()
	cfg.ClaimLimit = c.LocalNormalization.ClaimSize
	cfg.Lease = c.LocalNormalization.Lease
	cfg.Retry.MaxAttempts = c.LocalNormalization.MaxAttempts
	return cfg
}

// NewAuthorMetadataLocalLoops builds the configured number of local loops,
// each with a worker of its own (and so its own lease owner).
func NewAuthorMetadataLocalLoops(db *pg.DB, c *config.AuthorMetadataConfig) ([]AuthorMetadataStageWorker, error) {
	cfg := AuthorMetadataLocalWorkerConfigFrom(c)
	loops := make([]AuthorMetadataStageWorker, 0, c.LocalNormalization.Concurrency)
	for i := 0; i < c.LocalNormalization.Concurrency; i++ {
		w, err := NewAuthorMetadataLocalWorker(db, &cfg)
		if err != nil {
			return nil, err
		}
		loops = append(loops, NewAuthorMetadataLocalLoop(
			fmt.Sprintf("%s-%d", AuthorMetadataStageLocalNormalization, i), w, c.PollInterval))
	}
	return loops, nil
}
