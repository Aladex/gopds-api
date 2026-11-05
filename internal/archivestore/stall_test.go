package archivestore

import (
	"bytes"
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
	if !bytes.Equal(got, wantBook(t, data, bookName(1))) {
		t.Error("wrong book")
	}
	fx.server.SetStallBodies(false)
	fx.server.SetSilentErrors(false)
}

func TestColdBookReadIsBoundedOverall(t *testing.T) {
	data := buildArchive(t, 5, 64<<10, 23)
	fx := newFixture(t, &Config{ReadBudget: testBudget}, data)
	if err := fx.store.Check(context.Background(), fx.path); err != nil {
		t.Fatal(err)
	}
	// Every read succeeds, so nothing short of an overall limit ends this.
	fx.server.SetDrip(5 * time.Millisecond)

	err := within(t, settleLimit, func() error {
		_, err := tryBook(context.Background(), fx.store, fx.path, bookName(1))
		return err
	})

	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	waitForIdle(t, fx.server, settleLimit)
}

func TestReadStartedEarlierHasOnlyWhatIsLeft(t *testing.T) {
	data := buildArchive(t, 5, 1000, 24)
	fx := newFixture(t, &Config{ReadBudget: time.Hour}, data)
	if err := fx.store.Check(context.Background(), fx.path); err != nil {
		t.Fatal(err)
	}
	fx.store.now = func() time.Time { return time.Now().Add(-time.Hour) }
	ctx := fx.store.StartRead(context.Background())
	fx.store.now = time.Now
	fx.server.Reset()

	_, err := tryBook(ctx, fx.store, fx.path, bookName(1))

	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a read past its budget got %v", err)
	}
	if n := fx.server.Requests(); n != 0 {
		t.Fatalf("%d requests past the budget", n)
	}
}

// The budget is for the store. A conversion that follows the read keeps all the
// time it ever had, which a real deadline on the context would take from it.
func TestStartedReadPutsNoDeadlineOnTheContext(t *testing.T) {
	fx := newFixture(t, &Config{ReadBudget: time.Millisecond}, buildArchive(t, 1, 10, 25))
	if _, has := fx.store.StartRead(context.Background()).Deadline(); has {
		t.Fatal("the context carries a deadline")
	}
}

func TestLocalArchiveHasNoBudget(t *testing.T) {
	data := buildArchive(t, 5, 1000, 26)
	fx := newFixture(t, &Config{ReadBudget: time.Hour}, data)
	if err := os.WriteFile(fx.path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	fx.store.now = func() time.Time { return time.Now().Add(-time.Hour) }
	ctx := fx.store.StartRead(context.Background())
	fx.store.now = time.Now

	got, err := tryBook(ctx, fx.store, fx.path, bookName(1))

	if err != nil || len(got) == 0 {
		t.Fatalf("a book on disk failed on the store's budget: %d bytes, %v", len(got), err)
	}
}

func TestDiskOnlyStoreLeavesTheContextAlone(t *testing.T) {
	ctx := context.Background()
	if got := (&Store{}).StartRead(ctx); got != ctx {
		t.Fatal("a store without object storage touched the context")
	}
}

func TestEntryCloseReleasesItsBudget(t *testing.T) {
	data := buildArchive(t, 5, 1000, 27)
	fx := newFixture(t, &Config{}, data)
	entry, err := fx.store.Find(context.Background(), fx.path, bookName(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, has := entry.ctx.Deadline(); !has {
		t.Fatal("a remote entry has no deadline")
	}
	_ = entry.Close()
	if entry.ctx.Err() == nil {
		t.Fatal("closing the entry left its context running")
	}
}
