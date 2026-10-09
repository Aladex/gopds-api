package main

// Phase 15 fix round 1: the shutdown boundaries the review found open — a
// shutdown deadline that passes while a worker still owns the pool, and the
// container stop signal.

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// slowCleanupWorker, once canceled, holds until released and then makes one
// more database call: a final operation longer than the shutdown budget.
type slowCleanupWorker struct {
	db               *pg.DB
	started, release chan struct{}
	result           chan error
}

func (w *slowCleanupWorker) Name() string { return "slow-cleanup" }

func (w *slowCleanupWorker) Run(ctx context.Context) error {
	close(w.started)
	<-ctx.Done()
	<-w.release
	_, err := w.db.Exec(`SELECT 1`)
	w.result <- err
	return nil
}

// B1: when the deadline passes with a worker still running, the pool stays
// open for it; the shutdown reports the failure instead of closing.
func TestAuthorMetadataShutdownTimeoutKeepsLiveWorkersPool(t *testing.T) {
	db, _ := configuredDB(t)
	t.Cleanup(func() { _ = db.Close() })
	w := &slowCleanupWorker{db: db, started: make(chan struct{}), release: make(chan struct{}), result: make(chan error, 1)}
	runner, err := startAuthorMetadataRunner(context.Background(), db, []services.AuthorMetadataStageWorker{w})
	require.NoError(t, err)
	select {
	case <-w.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the runner never started the worker")
	}

	err = shutdownAuthorMetadataThenDatabase(runner, db, 30*time.Millisecond)
	assert.ErrorIs(t, err, services.ErrAuthorMetadataRunnerShutdownTimeout, "a missed deadline is reported")

	close(w.release)
	assert.NoError(t, <-w.result, "a worker alive after the deadline must find its pool open")
	join, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, runner.Shutdown(join))
}

// B2 helper process: the application's own signal registration, the real
// runner start and the real shutdown helper; it writes the marker only after
// the runner stopped and the pool closed.
func TestAuthorMetadataSignalChild(t *testing.T) {
	marker := os.Getenv("GOPDS_SIGNAL_TEST_MARKER")
	if marker == "" {
		t.Skip("helper process for TestShutdownSignalsReachRunnerAndDatabase")
	}
	db, _ := configuredDB(t)
	w := &lastWordWorker{db: db, started: make(chan struct{})}
	runner, err := startAuthorMetadataRunner(context.Background(), db, []services.AuthorMetadataStageWorker{w})
	require.NoError(t, err)
	<-w.started
	quit := notifyShutdown()
	fmt.Println("SIGNAL_CHILD_READY")
	<-quit
	require.NoError(t, shutdownAuthorMetadataThenDatabase(runner, db, 5*time.Second))
	if _, closedErr := db.Exec(`SELECT 1`); closedErr == nil || !strings.Contains(closedErr.Error(), closedPool) {
		t.Fatalf("pool not closed after shutdown: %v", closedErr)
	}
	require.NoError(t, os.WriteFile(marker, []byte(fmt.Sprint(w.lastErr.Load())), 0o600))
}

// B2: SIGINT and SIGTERM (the container stop signal) both run the runner
// shutdown and the database close.
func TestShutdownSignalsReachRunnerAndDatabase(t *testing.T) {
	configuredDB(t) // skips without an integration database
	for _, tc := range []struct {
		name string
		sig  os.Signal
	}{{"SIGINT", os.Interrupt}, {"SIGTERM", syscall.SIGTERM}} {
		t.Run(tc.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "shutdown.marker")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAuthorMetadataSignalChild$", "-test.v")
			cmd.Env = append(os.Environ(), "GOPDS_SIGNAL_TEST_MARKER="+marker)
			stdout, err := cmd.StdoutPipe()
			require.NoError(t, err)
			require.NoError(t, cmd.Start())
			ready := false
			lines := bufio.NewScanner(stdout)
			for lines.Scan() {
				if strings.Contains(lines.Text(), "SIGNAL_CHILD_READY") {
					ready = true
					break
				}
			}
			require.True(t, ready, "the helper never reached its signal wait")
			go func() {
				for lines.Scan() { // drain the rest so the child never blocks on stdout
				}
			}()

			require.NoError(t, cmd.Process.Signal(tc.sig))
			assert.NoError(t, cmd.Wait(), "the process must end through the graceful shutdown")
			got, err := os.ReadFile(marker)
			require.NoError(t, err, "the runner shutdown and the database close were not reached")
			assert.Equal(t, "<nil>", string(got), "the worker's last database call preceded the close")
		})
	}
}
