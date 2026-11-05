package archivestore

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"gopds-api/internal/objectstore/s3test"
)

const (
	testBudget = 500 * time.Millisecond
	// settleLimit is the budget and a slow machine's worth of room.
	settleLimit = testBudget + 2*time.Second
)

// within fails the test if call does not return: what these tests guard
// against is a hang.
func within(t *testing.T, limit time.Duration, call func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- call() }()
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		t.Fatalf("no answer within %s", limit)
		return nil
	}
}

func waitForIdle(t *testing.T, server *s3test.Server, limit time.Duration) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for server.Active() > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d requests still open after %s", server.Active(), limit)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A caller that leaves does not end a directory load, which other callers may
// be waiting on. Something else has to.
func TestDirectoryLoadOutlivesNoOneForever(t *testing.T) {
	for name, stall := range map[string]func(*s3test.Server){
		"flushed error headers": func(s *s3test.Server) { s.SetSilentErrors(true) },
		"a body that drips":     func(s *s3test.Server) { s.SetDrip(20 * time.Millisecond) },
	} {
		t.Run(name, func(t *testing.T) {
			fx := newFixture(t, &Config{ReadBudget: testBudget}, buildArchive(t, 5, 1000, 21))
			stall(fx.server)

			first, leave := context.WithCancel(context.Background())
			left := make(chan error, 1)
			go func() { left <- fx.store.Check(first, fx.path) }()
			for fx.server.Active() == 0 {
				time.Sleep(time.Millisecond)
			}
			leave()
			if err := within(t, settleLimit, func() error { return <-left }); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("the caller that left got %v", err)
			}

			err := within(t, settleLimit, func() error { return fx.store.Check(context.Background(), fx.path) })
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("the next caller got %v", err)
			}
			waitForIdle(t, fx.server, settleLimit)
		})
	}
}

func TestLocalArchiveServesDuringAStall(t *testing.T) {
	data := buildArchive(t, 5, 1000, 22)
	fx := newFixture(t, &Config{}, data)
	fx.server.SetStallBodies(true)
	fx.server.SetSilentErrors(true)

	// The object store is stuck on a book that exists only there.
	for range 8 {
		go func() { _ = fx.store.Check(context.Background(), fx.path) }()
	}
	for fx.server.Active() == 0 {
		time.Sleep(time.Millisecond)
	}

	local := fx.root + "local.zip"
	if err := os.WriteFile(local, data, 0o600); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	got := readBook(t, fx.store, local, bookName(1))
	if elapsed := time.Since(started); elapsed > testTimeout/2 {
		t.Errorf("a local book waited %s behind a stalled store", elapsed)
	}
	if want := wantBook(t, data, bookName(1)); string(got) != string(want) {
		t.Error("wrong book")
	}
	fx.server.SetStallBodies(false)
	fx.server.SetSilentErrors(false)
}
