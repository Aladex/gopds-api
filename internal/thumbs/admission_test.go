package thumbs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"gopds-api/internal/coverstore"
	"gopds-api/internal/objectstore/s3test"
)

// waitingCovers is well past any machine's slots, so that some must wait.
const waitingCovers = 12

// occupy fills a set of slots for as long as the returned function is not
// called.
func occupy(slots chan struct{}) (free func()) {
	for range cap(slots) {
		slots <- struct{}{}
	}
	return func() {
		for range cap(slots) {
			<-slots
		}
	}
}

// asleep counts generations parked on a channel with no request under way. A
// read of a file is not parked at all, and one of the store is ruled out by
// its frames, so this counts what is really waiting for a slot, wherever in a
// generation that happens to be.
func asleep() int {
	stacks := make([]byte, 4<<20)
	stacks = stacks[:runtime.Stack(stacks, true)]
	count := 0
	for _, goroutine := range bytes.Split(stacks, []byte("\n\n")) {
		header, _, _ := bytes.Cut(goroutine, []byte("\n"))
		parked := bytes.Contains(header, []byte("[select")) || bytes.Contains(header, []byte("[chan send"))
		inGeneration := bytes.Contains(goroutine, []byte("(*Service).generate("))
		if parked && inGeneration && !bytes.Contains(goroutine, []byte("internal/objectstore.")) {
			count++
		}
	}
	return count
}

func coverName(i int) string { return fmt.Sprintf("/lib/%02d.jpg", i) }

func writeCoverAt(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// generateAll starts one generation per cover and returns where their results
// land.
func generateAll(ctx context.Context, service *Service, dir string) <-chan error {
	results := make(chan error, waitingCovers)
	for i := range waitingCovers {
		go func() {
			results <- service.generate(ctx, coverName(i), filepath.Join(dir, coverName(i)))
		}()
	}
	return results
}

func wantGenerated(t *testing.T, results <-chan error) {
	t.Helper()
	for range waitingCovers {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
}

// Whoever is in the queue has not touched its cover: what is in the file by the
// time its turn comes is what it makes the thumbnail from. This is what keeps
// the memory for covers to the slots rather than the size of the queue.
func TestWaitingGenerationsHaveNotReadTheirCovers(t *testing.T) {
	service, source, dir := diskService(t)
	before, after := encodeJPEG(t, 400, 600), encodeJPEG(t, 100, 200)
	for i := range waitingCovers {
		writeCoverAt(t, source, coverName(i), before)
	}
	free := occupy(service.slots)

	results := generateAll(context.Background(), service, dir)
	waitFor(t, "generations waiting", func() bool { return asleep() == waitingCovers })
	for i := range waitingCovers {
		writeCoverAt(t, source, coverName(i), after)
	}
	free()

	wantGenerated(t, results)
	for i := range waitingCovers {
		if got := decodeSize(t, filepath.Join(dir, coverName(i))); got != image.Pt(100, 200) {
			t.Fatalf("cover %d was read before its turn: thumbnail is %v", i, got)
		}
	}
}

// A thumbnail in the bucket is bytes in memory like a cover is, and stalls like
// any other read of the bucket.
func TestBucketThumbnailFetchesAreBounded(t *testing.T) {
	server := s3test.New(t)
	// Longer than the test: a read that timed out would make room for the next
	// one, and the server would see both for a moment.
	covers := coverstore.New(server.WriterClient(time.Minute, "thumbs/"), "covers/").WithThumbs("thumbs/")
	service := New(t.TempDir(), t.TempDir(), func() *coverstore.Store { return covers })
	server.SetHang(true)
	defer server.SetHang(false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := generateAll(ctx, service, service.dir)
	waitFor(t, "every generation accounted for", func() bool {
		return server.Active()+asleep() == waitingCovers
	})
	peak := server.Peak()
	cancel()
	for range waitingCovers {
		<-results
	}
	if limit := cap(service.remote); peak != limit {
		t.Fatalf("%d reads at once, limit %d", peak, limit)
	}
}

// A cover from the store is bytes in memory until it is written out, so it
// stays counted while it waits for the processor.
func TestCoverFromTheStoreKeepsItsPlaceWhileItWaitsToDecode(t *testing.T) {
	service, server, dir := remoteService(t)
	for i := range waitingCovers {
		server.Put("covers"+coverName(i), encodeJPEG(t, 400, 600))
	}
	free := occupy(service.slots)

	results := generateAll(context.Background(), service, dir)
	waitFor(t, "generations waiting", func() bool { return asleep() == waitingCovers })
	reads := readsOf(server, "covers/")
	free()

	wantGenerated(t, results)
	if limit := cap(service.remote); reads != limit {
		t.Fatalf("%d covers read while the processor was busy, limit %d", reads, limit)
	}
}

// With every bucket read taken, a cover on disk is the quicker way: nothing it
// does may depend on those reads ever finishing.
func TestCoverOnDiskSkipsABusyBucket(t *testing.T) {
	service, server, source, dir := bucketService(t)
	writeCover(t, source, encodeJPEG(t, 480, 640))
	server.Put(thumbKey, encodeJPEG(t, 240, 100))
	defer occupy(service.remote)()

	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	if _, err := service.Path(ctx, rel); err != nil {
		t.Fatal(err)
	}
	if n := server.Requests(); n != 0 {
		t.Fatalf("%d requests with every bucket read taken", n)
	}
	if got := decodeSize(t, filepath.Join(dir, rel)); got != image.Pt(Width, 320) {
		t.Fatalf("size = %v", got)
	}
}

// A lookup that found nothing is done with the bucket: waiting for the
// processor with a bucket read in hand would starve the covers that need it.
func TestWaitingForTheProcessorHoldsNoBucketRead(t *testing.T) {
	service, _, source, dir := bucketService(t)
	for i := range waitingCovers {
		writeCoverAt(t, source, coverName(i), encodeJPEG(t, 400, 600))
	}
	free := occupy(service.slots)

	results := generateAll(context.Background(), service, dir)
	waitFor(t, "generations waiting", func() bool { return asleep() == waitingCovers })
	held := len(service.remote)
	free()

	wantGenerated(t, results)
	if held != 0 {
		t.Fatalf("%d bucket reads held while waiting for the processor", held)
	}
}

func TestWaitForTheStoreEndsWithTheGeneration(t *testing.T) {
	service, server, dir := remoteService(t)
	server.Put(coverKey, encodeJPEG(t, 400, 600))
	defer occupy(service.remote)()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := service.generate(ctx, rel, filepath.Join(dir, rel))
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if n := server.Requests(); n != 0 {
		t.Fatalf("%d requests past the limit", n)
	}
}
