package objectstore_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/internal/objectstore"
	"gopds-api/internal/objectstore/s3test"
)

const timeout = 200 * time.Millisecond

func TestHeadAndGet(t *testing.T) {
	server := s3test.New(t)
	server.Put("archives/a.zip", []byte("0123456789"))
	client := server.Client(timeout, 1)
	ctx := context.Background()

	info, err := client.Head(ctx, "archives/a.zip")
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != 10 || info.ETag == "" || strings.Contains(info.ETag, `"`) || info.LastModified.IsZero() {
		t.Fatalf("info = %+v", info)
	}

	body, err := client.Get(ctx, "archives/a.zip", 2, 3, info.ETag)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "234" || body.Size != 10 {
		t.Fatalf("got %q, size %d", got, body.Size)
	}
}

func TestKeysThatNeedEncodingAreSignedAndFound(t *testing.T) {
	server := s3test.New(t)
	client := server.Client(timeout, 1)
	for _, key := range []string{"archives/a b.zip", "covers/Толстой/книга+1.jpg", "a&b=c/d~e.zip"} {
		server.Put(key, []byte("x"))
		if _, err := client.Head(context.Background(), key); err != nil {
			t.Errorf("%q: %v", key, err)
		}
	}
}

func TestNotFound(t *testing.T) {
	client := s3test.New(t).Client(timeout, 1)
	if _, err := client.Head(context.Background(), "absent"); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestNotFoundIsNotRetried(t *testing.T) {
	server := s3test.New(t)
	_, _ = server.Client(timeout, 3).Head(context.Background(), "absent")
	if n := server.Requests(); n != 1 {
		t.Fatalf("%d requests for an honest 404", n)
	}
}

func TestChangedVersionIsRefused(t *testing.T) {
	server := s3test.New(t)
	server.Put("a", []byte("0123456789"))
	_, err := server.Client(timeout, 1).Get(context.Background(), "a", 0, 4, "older-version")
	if !errors.Is(err, objectstore.ErrChanged) {
		t.Fatalf("err = %v", err)
	}
}

func TestServerErrorIsRetriedAndBounded(t *testing.T) {
	server := s3test.New(t)
	server.Put("a", []byte("x"))
	client := server.Client(timeout, 2)

	server.FailNext(2)
	if _, err := client.Head(context.Background(), "a"); err != nil {
		t.Fatalf("two failures within three attempts: %v", err)
	}
	server.Reset()
	server.SetDown(true)
	if _, err := client.Head(context.Background(), "a"); !errors.Is(err, objectstore.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if n := server.Requests(); n != 3 {
		t.Fatalf("%d attempts, want 3", n)
	}
}

func TestForbiddenIsUnavailableAndNotRetried(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<Error><Code>InvalidAccessKeyId</Code></Error>"))
	}))
	defer server.Close()
	client := clientOf(t, server.URL)
	_, err := client.Get(context.Background(), "a", 0, 1, "")
	if !errors.Is(err, objectstore.ErrUnavailable) || !strings.Contains(err.Error(), "InvalidAccessKeyId") {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("%d calls", calls.Load())
	}
}

func clientOf(t *testing.T, endpoint string) *objectstore.Client {
	t.Helper()
	client, err := objectstore.New(&objectstore.Config{
		Endpoint: endpoint, Region: s3test.Region, Bucket: s3test.Bucket,
		AccessKeyID: s3test.AccessKey, SecretAccessKey: s3test.SecretKey,
		PathStyle: true, RequestTimeout: timeout, Retries: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestHangingServerFailsWithinTheTimeout(t *testing.T) {
	server := s3test.New(t)
	server.SetHang(true)
	client := server.Client(timeout, -1)

	started := time.Now()
	_, err := client.Head(context.Background(), "a")

	if !errors.Is(err, objectstore.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if elapsed := time.Since(started); elapsed > timeout+time.Second {
		t.Fatalf("took %s", elapsed)
	}
}

func TestBodyThatGoesSilentFailsItsRead(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-9/10")
		w.Header().Set("Content-Length", "10")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("01"))
		w.(http.Flusher).Flush()
		<-release
	}))
	defer server.Close()
	defer close(release)

	body, err := clientOf(t, server.URL).Get(context.Background(), "a", 0, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()

	started := time.Now()
	_, err = io.ReadAll(body)

	if !errors.Is(err, objectstore.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if elapsed := time.Since(started); elapsed > timeout+time.Second {
		t.Fatalf("took %s", elapsed)
	}
}

func TestRepeatedFailureRestsTheStore(t *testing.T) {
	server := s3test.New(t)
	server.SetDown(true)
	client := server.Client(timeout, -1)
	ctx := context.Background()
	for range 5 {
		_, _ = client.Head(ctx, "a")
	}
	server.Reset()

	started := time.Now()
	_, err := client.Head(ctx, "a")

	if !errors.Is(err, objectstore.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if n := server.Requests(); n != 0 {
		t.Fatalf("%d requests while resting", n)
	}
	if elapsed := time.Since(started); elapsed > 50*time.Millisecond {
		t.Fatalf("resting took %s", elapsed)
	}
}

func TestCanceledCallerDoesNotCountAgainstTheStore(t *testing.T) {
	server := s3test.New(t)
	server.Put("a", []byte("x"))
	client := server.Client(timeout, -1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range 10 {
		_, _ = client.Head(ctx, "a")
	}
	if _, err := client.Head(context.Background(), "a"); err != nil {
		t.Fatalf("callers leaving rested the store: %v", err)
	}
}

func TestListFollowsPages(t *testing.T) {
	server := s3test.New(t)
	for i := range 7 {
		server.Put(fmt.Sprintf("archives/%d.zip", i), []byte("x"))
	}
	server.Put("covers/a.jpg", []byte("x"))

	objects, err := server.Client(timeout, 1).List(context.Background(), "archives/")
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 7 {
		t.Fatalf("%d objects", len(objects))
	}
	if objects[0].Key != "archives/0.zip" || objects[0].Size != 1 || objects[0].ETag == "" {
		t.Fatalf("first = %+v", objects[0])
	}
}

func TestConfigIsChecked(t *testing.T) {
	for name, cfg := range map[string]objectstore.Config{
		"no scheme": {Endpoint: "s3.example.com", Region: "r", Bucket: "b"},
		"no bucket": {Endpoint: "https://s3.example.com", Region: "r"},
		"no region": {Endpoint: "https://s3.example.com", Bucket: "b"},
	} {
		if _, err := objectstore.New(&cfg); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestCredentialsNeverPrint(t *testing.T) {
	const secret = "very-secret-value"
	cfg := objectstore.Config{Endpoint: "https://s3.example.com", AccessKeyID: secret, SecretAccessKey: secret}
	var printed bytes.Buffer
	fmt.Fprintf(&printed, "%v %+v %#v %s", cfg, cfg, cfg, cfg.SecretAccessKey)
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	printed.Write(encoded)
	if strings.Contains(printed.String(), secret) {
		t.Fatalf("a credential printed: %s", printed.String())
	}
}

func TestCredentialsStayOutOfErrors(t *testing.T) {
	server := s3test.New(t)
	server.SetDown(true)
	_, err := server.Client(timeout, -1).Head(context.Background(), "a")
	if err == nil {
		t.Fatal("no error")
	}
	if strings.Contains(err.Error(), s3test.SecretKey) || strings.Contains(err.Error(), s3test.AccessKey) {
		t.Fatalf("a credential in an error: %v", err)
	}
}
