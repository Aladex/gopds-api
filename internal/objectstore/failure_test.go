package objectstore_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/internal/objectstore"
	"gopds-api/internal/objectstore/s3test"
)

// settled is how long an answer that must not hang may take on a slow machine.
const settled = timeout + 2*time.Second

// within runs call and fails the test if it does not return: a regression here
// is a hang, and a hang must read as a failure of this test, not of the run.
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

func readAll(client *objectstore.Client, ctx context.Context, key string) error {
	body, err := client.Get(ctx, key, 0, 10, "")
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	_, err = io.ReadAll(body)
	return err
}

// silent answers with headers that promise a body, and then says nothing until
// the client gives up.
func silent(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if status == http.StatusPartialContent {
			w.Header().Set("Content-Range", "bytes 0-9/10")
		}
		w.Header().Set("Content-Length", "10")
		w.WriteHeader(status)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}
}

func TestSilentErrorBodyFailsWithinTheTimeout(t *testing.T) {
	server := httptest.NewServer(silent(http.StatusServiceUnavailable))
	defer server.Close()
	client := clientOf(t, server.URL)

	err := within(t, settled, func() error {
		_, err := client.Get(context.Background(), "a", 0, 10, "")
		return err
	})

	if !errors.Is(err, objectstore.ErrUnavailable) || !strings.Contains(err.Error(), "503") {
		t.Fatalf("err = %v", err)
	}
}

func TestSilentErrorBodiesRestTheStore(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		silent(http.StatusServiceUnavailable)(w, r)
	}))
	defer server.Close()
	client := clientOf(t, server.URL)

	for range 7 {
		_ = within(t, settled, func() error { return readAll(client, context.Background(), "a") })
	}

	if n := calls.Load(); n != 5 {
		t.Fatalf("%d calls reached a store with a threshold of 5", n)
	}
}

func TestStalledBodiesRestTheStore(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		silent(http.StatusPartialContent)(w, r)
	}))
	defer server.Close()
	client := clientOf(t, server.URL)

	for range 7 {
		err := within(t, settled, func() error { return readAll(client, context.Background(), "a") })
		if !errors.Is(err, objectstore.ErrUnavailable) {
			t.Fatalf("err = %v", err)
		}
	}

	if n := calls.Load(); n != 5 {
		t.Fatalf("%d calls reached a store with a threshold of 5", n)
	}
}

func TestTruncatedBodiesRestTheStore(t *testing.T) {
	server := s3test.New(t)
	server.Put("a", []byte("0123456789"))
	server.DropAfter(2)
	client := server.Client(timeout, -1)

	for range 7 {
		if err := readAll(client, context.Background(), "a"); !errors.Is(err, objectstore.ErrUnavailable) {
			t.Fatalf("err = %v", err)
		}
	}

	if n := server.Requests(); n != 5 {
		t.Fatalf("%d calls reached a store with a threshold of 5", n)
	}
}

// A HEAD has no body to fail on, so it says nothing about a store whose bodies
// stall, and must not wipe the failures those bodies counted.
func TestHeadersAloneDoNotClearTheCount(t *testing.T) {
	var gets atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead:
			w.Header().Set("Content-Length", "10")
		case r.URL.Path == "/"+s3test.Bucket+"/missing":
			w.WriteHeader(http.StatusNotFound)
		default:
			gets.Add(1)
			silent(http.StatusPartialContent)(w, r)
		}
	}))
	defer server.Close()
	client := clientOf(t, server.URL)
	ctx := context.Background()

	for range 7 {
		if _, err := client.Head(ctx, "a"); err != nil && !errors.Is(err, objectstore.ErrUnavailable) {
			t.Fatal(err)
		}
		_, _ = client.Head(ctx, "missing")
		_ = within(t, settled, func() error { return readAll(client, ctx, "a") })
	}

	if n := gets.Load(); n != 5 {
		t.Fatalf("%d stalled reads with a threshold of 5", n)
	}
}

func TestCompletedReadClearsTheCount(t *testing.T) {
	server := s3test.New(t)
	server.Put("a", []byte("0123456789"))
	client := server.Client(timeout, -1)
	ctx := context.Background()

	failFour := func() {
		server.DropAfter(2)
		for range 4 {
			_ = readAll(client, ctx, "a")
		}
		server.DropAfter(0)
	}
	failFour()
	if err := readAll(client, ctx, "a"); err != nil {
		t.Fatal(err)
	}
	failFour()

	if err := readAll(client, ctx, "a"); err != nil {
		t.Fatalf("a completed read did not clear the count: %v", err)
	}
}

func TestReadAbandonedByItsCallerDoesNotCount(t *testing.T) {
	server := httptest.NewServer(silent(http.StatusPartialContent))
	defer server.Close()
	client := clientOf(t, server.URL)

	for range 7 {
		ctx, cancel := context.WithCancel(context.Background())
		body, err := client.Get(ctx, "a", 0, 10, "")
		if err != nil {
			cancel()
			t.Fatalf("a caller leaving rested the store: %v", err)
		}
		cancel()
		_, _ = io.ReadAll(body)
		_ = body.Close()
	}
}

func TestBadListingCountsAgainstTheStore(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte("<ListBucketResult><Contents>"))
	}))
	defer server.Close()
	client := clientOf(t, server.URL)

	for range 7 {
		if _, err := client.List(context.Background(), ""); !errors.Is(err, objectstore.ErrUnavailable) {
			t.Fatalf("err = %v", err)
		}
	}

	if n := calls.Load(); n != 5 {
		t.Fatalf("%d calls reached a store with a threshold of 5", n)
	}
}

func TestContentRangeIsCheckedWhole(t *testing.T) {
	for name, header := range map[string]string{
		"another start":    "bytes 3-6/100",
		"another end":      "bytes 2-9/100",
		"shorter":          "bytes 2-4/100",
		"longer":           "bytes 2-6/100",
		"end before start": "bytes 2-1/100",
		"end past total":   "bytes 2-5/4",
		"no total":         "bytes 2-5/*",
		"no end":           "bytes 2-/100",
		"not bytes":        "items 2-5/100",
		"garbage":          "bytes a-b/c",
		"absent":           "",
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Range", header)
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write([]byte("2345"))
			}))
			defer server.Close()
			_, err := clientOf(t, server.URL).Get(context.Background(), "a", 2, 4, "")
			if !errors.Is(err, objectstore.ErrUnavailable) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestRangeClippedAtTheEndOfTheObjectIsFine(t *testing.T) {
	server := s3test.New(t)
	server.Put("a", []byte("0123456789"))
	body, err := server.Client(timeout, -1).Get(context.Background(), "a", 8, 5, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	got, err := io.ReadAll(body)
	if err != nil || string(got) != "89" {
		t.Fatalf("got %q, err %v", got, err)
	}
}

func TestContentLengthMustMatchTheRange(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-3/100")
		w.Header().Set("Content-Length", "6")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("012345"))
	}))
	defer server.Close()
	_, err := clientOf(t, server.URL).Get(context.Background(), "a", 0, 4, "")
	if !errors.Is(err, objectstore.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

// An endless chunked 206: the range is what must stop it, not the sender.
func TestOversizedRangeIsCut(t *testing.T) {
	const asked = 1 << 10
	var sent atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", asked-1, 1<<30))
		w.WriteHeader(http.StatusPartialContent)
		chunk := make([]byte, 4<<10)
		for r.Context().Err() == nil {
			n, err := w.Write(chunk)
			sent.Add(int64(n))
			if err != nil {
				return
			}
		}
	}))
	defer server.Close()

	body, err := clientOf(t, server.URL).Get(context.Background(), "a", 0, asked, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	got, err := io.ReadAll(body)

	if !errors.Is(err, objectstore.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if len(got) > asked {
		t.Fatalf("handed over %d bytes of a %d byte range", len(got), asked)
	}
}

func TestShortRangeIsAFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-9/100")
		w.WriteHeader(http.StatusPartialContent)
		w.(http.Flusher).Flush() // chunked, so that only the range can say it is short
		_, _ = w.Write([]byte("0123"))
	}))
	defer server.Close()
	err := readAll(clientOf(t, server.URL), context.Background(), "a")
	if !errors.Is(err, objectstore.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func limitedClient(t *testing.T, endpoint string, active int) *objectstore.Client {
	t.Helper()
	client, err := objectstore.New(&objectstore.Config{
		Endpoint: endpoint, Region: s3test.Region, Bucket: s3test.Bucket,
		AccessKeyID: s3test.AccessKey, SecretAccessKey: s3test.SecretKey,
		PathStyle: true, RequestTimeout: timeout, Retries: -1, MaxActive: active,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestActiveCallsAreBounded(t *testing.T) {
	const limit = 2
	var active, peak, calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		now := active.Add(1)
		defer active.Add(-1)
		for {
			seen := peak.Load()
			if now <= seen || peak.CompareAndSwap(seen, now) {
				break
			}
		}
		silent(http.StatusPartialContent)(w, r)
	}))
	defer server.Close()
	client := limitedClient(t, server.URL, limit)

	var wg sync.WaitGroup
	failures := make([]error, 8)
	for i := range failures {
		wg.Add(1)
		go func() {
			defer wg.Done()
			failures[i] = readAll(client, context.Background(), "a")
		}()
	}
	_ = within(t, 3*settled, func() error { wg.Wait(); return nil })

	for _, err := range failures {
		if !errors.Is(err, objectstore.ErrUnavailable) {
			t.Fatalf("err = %v", err)
		}
	}
	if n := peak.Load(); n != limit {
		t.Fatalf("%d calls at once with a limit of %d", n, limit)
	}
}

func TestABodyHoldsItsSlotUntilClosed(t *testing.T) {
	server := s3test.New(t)
	server.Put("a", []byte("0123456789"))
	client := limitedClient(t, server.URL, 1)
	ctx := context.Background()

	held, err := client.Get(ctx, "a", 0, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	server.Reset()
	if _, err := client.Head(ctx, "a"); !errors.Is(err, objectstore.ErrUnavailable) {
		t.Fatalf("a second call got past a limit of one: %v", err)
	}
	if n := server.Requests(); n != 0 {
		t.Fatalf("%d requests past the limit", n)
	}

	_ = held.Close()
	if _, err := client.Head(ctx, "a"); err != nil {
		t.Fatalf("the slot did not come back: %v", err)
	}
}

func TestFailedCallsGiveTheirSlotBack(t *testing.T) {
	server := s3test.New(t)
	server.Put("a", []byte("x"))
	client := limitedClient(t, server.URL, 1)
	ctx := context.Background()

	server.FailNext(3)
	for range 3 {
		if _, err := client.Head(ctx, "a"); !errors.Is(err, objectstore.ErrUnavailable) {
			t.Fatalf("err = %v", err)
		}
	}
	if _, err := client.Get(ctx, "absent", 0, 1, ""); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if _, err := client.Head(ctx, "a"); err != nil {
		t.Fatalf("a failed call kept its slot: %v", err)
	}
}
