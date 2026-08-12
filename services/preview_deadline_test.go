package services

// preview_deadline_test.go proves the property the phase-3 review flagged as
// unproven: a cache operation that STARTED before the build deadline is
// bounded by that deadline, not by the go-redis client's own timeouts.
//
// go-redis v6's WithContext stores the context but never checks it in the
// command path, so an already-running command used to block until the
// library's read timeout (3 s by default) regardless of the build deadline.
// A PutManifest released that way publishes after the deadline — a direct
// violation of the phase-3 invariant.
//
// The tests need a backend whose commands hang longer than the deadline.
// miniredis cannot stall a command mid-flight, and the mock PreviewCache
// ignores context by construction, so this file carries a minimal fake RESP
// server: it speaks just enough of the Redis protocol for the client's
// commands, stores values in memory (so assertions inspect the CACHE
// CONTENT, not returned errors), and can hold a chosen command until the
// test releases it.

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis"

	"gopds-api/internal/converter"
)

// fakeRedis is a test double for the Redis server: a TCP listener speaking
// enough RESP for the go-redis v6 client, backed by in-memory maps.
//
// The hang hook is the point of the double: before replying to a command it
// consults hangFn, and if the hook returns a channel the handler blocks on
// it — a command in flight the test can hold past any deadline and release
// when it chooses. Blocking happens BEFORE the store is touched and without
// the store mutex held, so other connections (the abandoned operation's late
// completion arrives on its own connection) keep working while one command
// hangs.
type fakeRedis struct {
	ln net.Listener

	mu      sync.Mutex
	strings map[string]string
	hashes  map[string]map[string]string
	hangFn  func(cmd string, args []string) <-chan struct{}
}

// newFakeRedis starts the double on a loopback port and registers cleanup.
func newFakeRedis(t *testing.T) *fakeRedis {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("fake redis: listen: %v", err)
	}
	f := &fakeRedis{
		ln:      ln,
		strings: map[string]string{},
		hashes:  map[string]map[string]string{},
	}
	go f.serve()
	t.Cleanup(func() { f.ln.Close() })
	return f
}

// client wires a go-redis client against the double. Default client timeouts
// are kept on purpose: the tests assert the caller is freed at the BUILD
// deadline (hundreds of milliseconds), far below the library's own read
// timeout (3 s) — that gap is what proves the deadline, not the client,
// interrupted the operation.
func (f *fakeRedis) client() *redis.Client {
	return redis.NewClient(&redis.Options{Addr: f.ln.Addr().String(), DB: 0})
}

// setHang installs the hang hook. Install it before the operation under
// test starts; the hook itself signals "the hung command has arrived"
// through the test's own channels.
func (f *fakeRedis) setHang(fn func(cmd string, args []string) <-chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hangFn = fn
}

// hasString reports whether the store holds a string value under key.
func (f *fakeRedis) hasString(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.strings[key]
	return ok
}

func (f *fakeRedis) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return // listener closed by cleanup
		}
		go f.handle(conn)
	}
}

func (f *fakeRedis) handle(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	for {
		args, err := readRESPCommand(r)
		if err != nil {
			return // client went away or a write timeout killed the conn
		}
		if len(args) == 0 {
			continue
		}

		// Consult the hang hook before touching the store, and never while
		// holding the store mutex: a hung SET must not block the retraction
		// EVAL arriving on another connection.
		f.mu.Lock()
		hangFn := f.hangFn
		f.mu.Unlock()
		if hangFn != nil {
			if gate := hangFn(strings.ToUpper(args[0]), args[1:]); gate != nil {
				<-gate
			}
		}

		reply := f.execute(args)
		if _, err := w.WriteString(reply); err != nil {
			return
		}
		if err := w.Flush(); err != nil {
			return
		}
	}
}

// readRESPCommand parses one command in RESP array-of-bulk-strings form —
// the only form go-redis v6 produces.
func readRESPCommand(r *bufio.Reader) ([]string, error) {
	header, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(header, "*") {
		return nil, fmt.Errorf("fake redis: expected array header, got %q", header)
	}
	n, err := strconv.Atoi(strings.TrimSpace(header[1:]))
	if err != nil {
		return nil, fmt.Errorf("fake redis: bad array length: %v", err)
	}
	args := make([]string, 0, n)
	for i := 0; i < n; i++ {
		bulk, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(bulk, "$") {
			return nil, fmt.Errorf("fake redis: expected bulk header, got %q", bulk)
		}
		l, err := strconv.Atoi(strings.TrimSpace(bulk[1:]))
		if err != nil {
			return nil, fmt.Errorf("fake redis: bad bulk length: %v", err)
		}
		buf := make([]byte, l+2) // payload + CRLF
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:l]))
	}
	return args, nil
}

// bulk formats one bulk-string reply element.
func bulk(s string) string {
	return fmt.Sprintf("$%d\r\n%s\r\n", len(s), s)
}

// execute applies one command to the store and formats its RESP reply.
// Unknown commands get +OK: the client sends nothing exotic on DB 0 without
// a password, and anything new the client adds is not what these tests
// assert on.
func (f *fakeRedis) execute(args []string) string {
	f.mu.Lock()
	defer f.mu.Unlock()

	switch strings.ToUpper(args[0]) {
	case "PING":
		return "+PONG\r\n"
	case "SET": // SET key value [EX seconds]
		f.strings[args[1]] = args[2]
		return "+OK\r\n"
	case "GET":
		if v, ok := f.strings[args[1]]; ok {
			return bulk(v)
		}
		return "$-1\r\n"
	case "HMSET": // HMSET key field value [field value ...]
		h := f.hashes[args[1]]
		if h == nil {
			h = map[string]string{}
			f.hashes[args[1]] = h
		}
		for i := 2; i+1 < len(args); i += 2 {
			h[args[i]] = args[i+1]
		}
		return "+OK\r\n"
	case "HGETALL":
		h := f.hashes[args[1]]
		var b strings.Builder
		fmt.Fprintf(&b, "*%d\r\n", len(h)*2)
		for field, v := range h {
			b.WriteString(bulk(field))
			b.WriteString(bulk(v))
		}
		return b.String()
	case "EXPIRE":
		return ":1\r\n"
	case "DEL":
		deleted := 0
		for _, key := range args[1:] {
			if _, ok := f.strings[key]; ok {
				delete(f.strings, key)
				deleted++
			}
		}
		return fmt.Sprintf(":%d\r\n", deleted)
	case "EVAL": // EVAL script numkeys key [arg] — only the retraction script
		// shape is supported: compare KEYS[1] against ARGV[1], delete on match.
		numKeys, _ := strconv.Atoi(args[2])
		key := args[3]
		argv := args[3+numKeys:]
		if len(argv) > 0 {
			if v, ok := f.strings[key]; ok && v == argv[0] {
				delete(f.strings, key)
				return ":1\r\n"
			}
		}
		return ":0\r\n"
	}
	return "+OK\r\n"
}

// eventually polls cond until it holds or the budget runs out. Used where
// the assertion depends on an abandoned goroutine finishing (the late SET
// and its retraction race the test by construction), so a fixed sleep would
// guess what a poll can prove.
func eventually(t *testing.T, budget time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// A PutManifest that starts before the deadline and hangs past it must be
// interrupted BY THE DEADLINE: the caller is freed at the context's timeout,
// not at the client's own read timeout (3 s, kept as the default here on
// purpose). After the fake releases the hung SET, the write completes late —
// and the manifest must NOT stay in the cache: a manifest published past the
// deadline is the phase-3 promise broken.
//
// Both assertions are on observable state, not on the returned error: the
// elapsed time distinguishes "freed at the deadline" from "waited for the
// client timeout", and the cache CONTENT distinguishes "the late write was
// retracted" from "the manifest was published after the deadline". Mutation
// "check ctx only before the operation" fails the elapsed assertion (the
// call blocks the full client read timeout); mutation "no retraction" fails
// the content assertion (the manifest survives the release).
func TestRedisPreviewCache_DeadlineInterruptsHungPutManifest(t *testing.T) {
	fake := newFakeRedis(t)
	client := fake.client()
	defer client.Close()
	cache := NewRedisPreviewCache(client)

	hangEntered := make(chan struct{}, 1)
	hold := make(chan struct{}, 8) // buffered: a release never blocks the test
	defer close(hold)              // release every hung command the test itself did not
	var cmdMu sync.Mutex
	var cmdLog []string
	fake.setHang(func(cmd string, args []string) <-chan struct{} {
		cmdMu.Lock()
		cmdLog = append(cmdLog, cmd+" "+strings.Join(args, " "))
		cmdMu.Unlock()
		if cmd == "SET" && len(args) > 0 && strings.HasSuffix(args[0], ":manifest") {
			select {
			case hangEntered <- struct{}{}:
			default:
			}
			return hold
		}
		return nil
	})

	key := buildCacheKey(42, "deadline-md5", renderVersionPrefix)
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := cache.PutManifest(ctx, key, []byte(`{"revision":"r"}`), time.Minute)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("PutManifest returned nil for a cache write that outlived the deadline")
	}
	if !errors.Is(err, ErrCacheUnavailable) {
		t.Errorf("err = %v, want it to match ErrCacheUnavailable (miss and outage must stay distinct)", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded in the chain — the deadline interrupted the write", err)
	}
	if elapsed >= 2*time.Second {
		t.Errorf("PutManifest took %v — the caller waited for the client's read timeout, not the 250 ms deadline; "+
			"a pre-operation ctx check alone does not interrupt a command in flight", elapsed)
	}
	if fake.hasString(manifestKey(key)) {
		t.Error("the manifest is in the cache while its SET is still hung — it must not be published")
	}

	// Let the hung SET complete. The write lands late — past the deadline —
	// so the cache must retract it. Watching for the retraction EVAL first is
	// what makes this a real detector: polling for the manifest's absence
	// alone would pass instantly (the SET has not landed yet at that point),
	// and a mutation that drops the retraction would survive. With the EVAL
	// barrier the mutation fails here — no retraction is ever sent — and the
	// final content assertion pins the outcome.
	hold <- struct{}{} // release the one hung SET
	eventually(t, 3*time.Second, "the retraction EVAL for the late manifest write", func() bool {
		cmdMu.Lock()
		defer cmdMu.Unlock()
		for _, c := range cmdLog {
			if strings.HasPrefix(c, "EVAL ") && strings.Contains(c, ":manifest") {
				return true
			}
		}
		return false
	})
	eventually(t, 3*time.Second, "the late manifest write to be retracted", func() bool {
		return !fake.hasString(manifestKey(key))
	})
}

// chunkBlockCache wraps mockPreviewCache and blocks the PutChunk call for
// blockIndex until release is closed, answering success regardless of the
// build context — the mock ignores context by construction. This mirrors
// imageBlockCache (preview_build_test.go) for the chunk write path, so the
// "canceled after the writes, before the manifest" scenario does not depend
// on the image pipeline: it gives the test a deterministic window between
// "some chunks are cached" and "the manifest is about to be published" to
// cancel the build context.
type chunkBlockCache struct {
	*mockPreviewCache
	blockIndex int
	entered    chan struct{}
	release    chan struct{}
}

func (c *chunkBlockCache) PutChunk(ctx context.Context, key string, index int, data []byte, ttl time.Duration) error {
	if index != c.blockIndex {
		return c.mockPreviewCache.PutChunk(ctx, key, index, data, ttl)
	}
	select {
	case c.entered <- struct{}{}:
	default:
	}
	<-c.release
	return c.mockPreviewCache.PutChunk(context.Background(), key, index, data, ttl)
}

// A build whose context is canceled after some chunks are cached must NOT
// publish the manifest — the pre-manifest ctx check is the only gate left
// once the blocked write returns success, and this test is its detector for
// the chunk path. The fixture produces EXACTLY two chunks: with more, the
// next chunk write's own pre-write ctx check would refuse first, and the
// test would no longer isolate the pre-manifest gate. The assertion is on
// the cache (manifest absent), not on the returned error: a mutation that
// removes the pre-manifest ctx check would still return through PutManifest,
// publishing a promise for work the cancellation was supposed to stop.
func TestPreviewBuild_CanceledAfterChunkWritesPublishesNoManifest(t *testing.T) {
	// 6 paragraphs at a 512-byte portion ceiling render into exactly 2
	// chunks, so the blocked chunk 1 is the LAST write before the manifest.
	paras := ""
	for i := 0; i < 6; i++ {
		paras += `<p>АБЗАЦ НОМЕР ` + strings.Repeat("длинный ", 6) + `</p>`
	}
	fb2 := `<?xml version="1.0"?>` +
		`<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">` +
		`<body><section><title><p>ГЛАВА</p></title>` + paras + `</section></body></FictionBook>`

	repo := buildBookRepo()
	loader := &fakeArchiveLoader{data: []byte(fb2)}
	cache := &chunkBlockCache{
		mockPreviewCache: newMockCache(),
		blockIndex:       1,
		entered:          make(chan struct{}, 1),
		release:          make(chan struct{}),
	}
	svc := NewPreviewService(repo, loader, cache, 4, defaultPreviewLimits(), 0, 0)
	svc.chunkPolicy = converter.PreviewPolicy{MaxChunkBytes: 512}

	done := make(chan loadResult, 1)
	go func() {
		data, err := svc.Load(context.Background(), 1, false)
		done <- loadResult{data, err}
	}()

	// Barrier: the build rendered, cached chunk 0, and is now blocked inside
	// the chunk 1 write — past at least one write, before the manifest.
	waitSignal(t, cache.entered, "the build to reach the chunk 1 write")

	// Cancel the service context: this cancels the build context (a child of
	// svcCtx), so the pre-manifest ctx check must refuse before PutManifest.
	svc.Shutdown()

	// Release the blocked write so the build goroutine can proceed to the
	// manifest check and exit. The write succeeds — the mock ignores context.
	close(cache.release)

	res := awaitLoadResult(t, done, "the canceled build to return")
	if res.err == nil {
		t.Fatal("the build succeeded despite cancellation")
	}

	key := buildCacheKey(1, "abc", svc.revision(repo.books[1]))
	if _, err := cache.GetManifest(context.Background(), key); !errors.Is(err, ErrCacheMiss) {
		t.Errorf("manifest present after cancellation (err = %v) — "+
			"the build published a promise for work the cancellation was supposed to stop", err)
	}
}

// The service-level half of the proof: the deadline fires while the build is
// blocked inside a cache write (the second chunk's SET), and the build is
// freed by the deadline — before the write completes — without publishing
// the manifest. The chunk written before the hang proves the build really
// was mid-flight in the write sequence, not refused before it started.
//
// The fixture hangs a chunk write, not an image write: the property under
// test is "a cache operation in flight when the deadline fires", and chunks
// reach Redis as plain SETs without depending on the image pipeline.
//
// The manifest assertion is on the cache content and stays green after the
// hung write is released: the freed build abandoned the chunk write, so the
// manifest is never attempted. A build that waited out the hung write and
// then published would fail this test even though every pre-write ctx check
// is in place — that is the bug the review found.
func TestPreviewBuild_DeadlineDuringCacheWriteFreesBuildAndPublishesNoManifest(t *testing.T) {
	fake := newFakeRedis(t)
	client := fake.client()
	defer client.Close()
	cache := NewRedisPreviewCache(client)

	hangEntered := make(chan struct{}, 1)
	hold := make(chan struct{}, 8) // buffered: a release never blocks the test
	release := sync.OnceFunc(func() { close(hold) })
	defer release()
	fake.setHang(func(cmd string, args []string) <-chan struct{} {
		if cmd == "SET" && len(args) > 0 && strings.HasSuffix(args[0], ":chunk:1") {
			select {
			case hangEntered <- struct{}{}:
			default:
			}
			return hold
		}
		return nil
	})

	// A tight chunk ceiling and a repetitive body force several portions, so
	// the hang on chunk 1 provably happens mid-sequence: chunk 0 is already
	// cached, more writes are still owed.
	paras := ""
	for i := 0; i < 40; i++ {
		paras += `<p>АБЗАЦ НОМЕР ` + strings.Repeat("длинный ", 6) + `</p>`
	}
	fb2 := `<?xml version="1.0"?>` +
		`<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">` +
		`<body><section><title><p>ГЛАВА</p></title>` + paras + `</section></body></FictionBook>`

	repo := buildBookRepo()
	loader := &fakeArchiveLoader{data: []byte(fb2)}
	svc := NewPreviewService(repo, loader, cache, 4, defaultPreviewLimits(), 300*time.Millisecond, 0)
	svc.chunkPolicy = converter.PreviewPolicy{MaxChunkBytes: 512}

	done := make(chan loadResult, 1)
	go func() {
		data, err := svc.Load(context.Background(), 1, false)
		done <- loadResult{data, err}
	}()

	// Barrier: the build rendered, cached chunk 0, and is blocked inside the
	// chunk 1 write — the deadline will fire mid-command.
	waitSignal(t, hangEntered, "the build to hang inside the chunk 1 write")

	start := time.Now()
	res := awaitLoadResult(t, done, "the deadline to free the build")
	elapsed := time.Since(start)

	if res.err == nil {
		t.Fatal("the build succeeded although a chunk write outlived the deadline")
	}
	if !errors.Is(res.err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded in the chain", res.err)
	}
	if elapsed >= 2*time.Second {
		t.Errorf("Load returned %v after the chunk write hung — the build waited for the client's read timeout "+
			"instead of being freed at the 300 ms build deadline", elapsed)
	}

	key := buildCacheKey(1, "abc", svc.revision(repo.books[1]))
	if fake.hasString(manifestKey(key)) {
		t.Error("the manifest is in the cache although a chunk write it indexes outlived the deadline")
	}
	if !fake.hasString(chunkKey(key, 0)) {
		t.Error("chunk 0 is not cached — the build never reached the write sequence; the test proves nothing")
	}

	// Release the hung write and give the abandoned operation room to land
	// late: the manifest must STILL be absent, because the build was already
	// refused at the deadline and never attempted it.
	release()
	eventually(t, 2*time.Second, "the abandoned chunk write to complete late", func() bool {
		return fake.hasString(chunkKey(key, 1))
	})
	if fake.hasString(manifestKey(key)) {
		t.Error("the manifest appeared after the deadline — the build waited out the hung write and published")
	}
}

// The happy path over the fake server: without a deadline in the way, every
// operation round-trips. This guards the interruption plumbing against "the
// wrapper broke the normal path" without needing the live Redis on 6380.
func TestRedisPreviewCache_FakeServerRoundTrip(t *testing.T) {
	fake := newFakeRedis(t)
	client := fake.client()
	defer client.Close()
	cache := NewRedisPreviewCache(client)
	ctx := context.Background()

	key := buildCacheKey(7, "roundtrip-md5", renderVersionPrefix)

	if err := cache.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if _, err := cache.GetManifest(ctx, key); !errors.Is(err, ErrCacheMiss) {
		t.Errorf("GetManifest before any write: err = %v, want ErrCacheMiss", err)
	}

	chunkData := []byte("<p>portion</p>")
	if err := cache.PutChunk(ctx, key, 0, chunkData, time.Minute); err != nil {
		t.Fatalf("PutChunk: %v", err)
	}
	gotChunk, gerr := cache.GetChunk(ctx, key, 0)
	if gerr != nil {
		t.Fatalf("GetChunk: %v", gerr)
	}
	if !bytes.Equal(gotChunk, chunkData) {
		t.Errorf("chunk roundtrip: got %q, want %q", gotChunk, chunkData)
	}

	imageData := []byte{0x89, 'P', 'N', 'G'}
	if err := cache.PutImage(ctx, key, 1, imageData, "image/png", time.Minute); err != nil {
		t.Fatalf("PutImage: %v", err)
	}
	payload, mime, ierr := cache.GetImage(ctx, key, 1)
	if ierr != nil {
		t.Fatalf("GetImage: %v", ierr)
	}
	if !bytes.Equal(payload, imageData) || mime != "image/png" {
		t.Errorf("image roundtrip: got (%q, %q), want (%q, image/png)", payload, mime, imageData)
	}

	manifestData := []byte(`{"revision":"r","chunk_count":1}`)
	if err := cache.PutManifest(ctx, key, manifestData, time.Minute); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	gotManifest, merr := cache.GetManifest(ctx, key)
	if merr != nil {
		t.Fatalf("GetManifest: %v", merr)
	}
	if !bytes.Equal(gotManifest, manifestData) {
		t.Errorf("manifest roundtrip: got %q, want %q", gotManifest, manifestData)
	}
}
