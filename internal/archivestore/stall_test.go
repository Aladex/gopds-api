package archivestore

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"gopds-api/internal/objectstore/s3test"

	"golang.org/x/sync/singleflight"
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

// A caller that has already left has nobody to load for. Waiting on the key
// is what makes this exact: a load is registered before directory returns, so
// a Do on the key either joins it until it ends or runs after it has ended.
func TestCallerAlreadyGoneStartsNoLoad(t *testing.T) {
	for name, prepare := range map[string]func(*fixture, string) error{
		"first open": func(*fixture, string) error { return nil },
		"stale directory": func(fx *fixture, key string) error {
			if err := fx.store.Check(context.Background(), fx.path); err != nil {
				return err
			}
			fx.store.directories.expire(key)
			return nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			fx := newFixture(t, &Config{ReadBudget: testBudget}, buildArchive(t, 5, 1000, 25))
			key, _ := fx.store.key(fx.path)
			if err := prepare(fx, key); err != nil {
				t.Fatal(err)
			}
			fx.server.Reset()
			gone, leave := context.WithCancel(context.Background())
			leave()

			if err := fx.store.Check(gone, fx.path); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("err = %v", err)
			}

			_, _, _ = fx.store.loads.Do(key, func() (any, error) { return nil, nil })
			if n := fx.server.Requests(); n != 0 {
				t.Fatalf("%d requests for a caller that left", n)
			}
		})
	}
}

// The load is held open through the group itself, so that it is running when
// the caller arrives with nothing to poll for.
func TestCallerAlreadyGoneLeavesARunningLoadAlone(t *testing.T) {
	fx := newFixture(t, &Config{ReadBudget: testBudget}, buildArchive(t, 5, 1000, 26))
	key, _ := fx.store.key(fx.path)
	release := make(chan struct{})
	running := fx.store.loads.DoChan(key, func() (any, error) {
		<-release
		return fx.store.loadDirectory(context.Background(), key, nil)
	})
	gone, leave := context.WithCancel(context.Background())
	leave()

	if err := fx.store.Check(gone, fx.path); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}

	waiting := fx.store.loads.DoChan(key, func() (any, error) {
		return nil, errors.New("the running load was dropped")
	})
	close(release)
	for name, result := range map[string]<-chan singleflight.Result{"the load": running, "its waiter": waiting} {
		if done := <-result; done.Err != nil {
			t.Errorf("%s: %v", name, done.Err)
		}
	}
	if n := fx.server.Requests(); n != 2 {
		t.Errorf("%d requests, want the HEAD and the tail of one load", n)
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

// replacedDuringAnOutage caches the directory of a one-book archive, replaces
// the object with one of three books and takes the store down, which is how a
// book can be in the catalog and in the bucket yet not in what is known here.
func replacedDuringAnOutage(t *testing.T) (fx *fixture, after []byte) {
	t.Helper()
	fx = newFixture(t, &Config{}, buildArchive(t, 1, 100, 41))
	fx.store.client = fx.server.Client(testTimeout, -1)
	readBook(t, fx.store, fx.path, bookName(0))
	after = buildArchive(t, 3, 100, 42)
	fx.server.Put(testPrefix+testArchive, after)
	fx.server.SetDown(true)
	return fx, after
}

func TestNewBookIsNotMissingWhileTheStoreCannotSay(t *testing.T) {
	fx, after := replacedDuringAnOutage(t)

	_, err := fx.store.Find(context.Background(), fx.path, bookName(2))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if errors.Is(err, ErrEntryNotFound) {
		t.Fatalf("err = %v: an outage proved a book absent", err)
	}

	fx.server.SetDown(false)
	got := readBook(t, fx.store, fx.path, bookName(2))
	if !bytes.Equal(got, wantBook(t, after, bookName(2))) {
		t.Fatal("wrong book after the outage")
	}
}

// A stale directory is still good for what it does list.
func TestStaleDirectoryStillFindsWhatItKnows(t *testing.T) {
	fx, _ := replacedDuringAnOutage(t)
	if _, err := fx.store.Find(context.Background(), fx.path, bookName(2)); err == nil {
		t.Fatal("found a book the directory does not list")
	}

	entry, err := fx.store.Find(context.Background(), fx.path, bookName(0))
	if err != nil {
		t.Fatalf("a known book during the outage: %v", err)
	}
	_ = entry.Close()
}

// The same outage over an archive that has not changed: nothing here tells the
// two apart, so neither may be called a missing book.
func TestAbsentEntryNeedsAStoreThatAnswered(t *testing.T) {
	fx := newFixture(t, &Config{}, buildArchive(t, 1, 100, 43))
	fx.store.client = fx.server.Client(testTimeout, -1)
	readBook(t, fx.store, fx.path, bookName(0))

	fx.server.SetDown(true)
	if _, err := fx.store.Find(context.Background(), fx.path, "absent.fb2"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("store down: %v", err)
	}
	fx.server.SetDown(false)
	if _, err := fx.store.Find(context.Background(), fx.path, "absent.fb2"); !errors.Is(err, ErrEntryNotFound) {
		t.Fatalf("store back: %v", err)
	}
}

func TestStallDuringTheRefreshIsNotAMissingBook(t *testing.T) {
	fx, _ := replacedDuringAnOutage(t)
	fx.server.SetDown(false)
	fx.server.SetHang(true)
	defer fx.server.SetHang(false)

	if _, err := fx.store.Find(context.Background(), fx.path, bookName(2)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestArchiveGoneDuringTheRefreshIsAMissingArchive(t *testing.T) {
	fx := newFixture(t, &Config{}, buildArchive(t, 1, 100, 44))
	readBook(t, fx.store, fx.path, bookName(0))
	fx.server.Delete(testPrefix + testArchive)

	_, err := fx.store.Find(context.Background(), fx.path, bookName(2))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
