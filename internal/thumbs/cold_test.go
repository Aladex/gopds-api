package thumbs

import (
	"bytes"
	"context"
	"errors"
	"image"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"testing"
	"time"

	"gopds-api/internal/coverstore"
	"gopds-api/internal/objectstore"
	"gopds-api/internal/objectstore/s3test"
)

const (
	// pageCovers is a cold page of a collection.
	pageCovers = 24
	// storeCapacity is max_active_requests in these tests, past every machine's
	// generation slots.
	storeCapacity = 16
	// roundTrip is a store that is healthy and far away.
	roundTrip = 30 * time.Millisecond
)

// serviceOverStore keeps thumbnails in a fake bucket reached with the given
// max_active_requests. Its timeout outlasts any test, so a stall lasts until the
// test ends it.
func serviceOverStore(t *testing.T, server *s3test.Server, maxActive int) *Service {
	t.Helper()
	client, err := objectstore.New(&objectstore.Config{
		Endpoint:        server.URL,
		Region:          s3test.Region,
		Bucket:          s3test.Bucket,
		AccessKeyID:     s3test.AccessKey,
		SecretAccessKey: s3test.SecretKey,
		PathStyle:       true,
		RequestTimeout:  time.Minute,
		Retries:         -1,
		MaxActive:       maxActive,
		WritePrefix:     "thumbs/",
	})
	if err != nil {
		t.Fatal(err)
	}
	covers := coverstore.New(client, "covers/").WithThumbs("thumbs/")
	return New(t.TempDir(), t.TempDir(), func() *coverstore.Store { return covers })
}

// putPage fills the bucket with a page of covers that are nowhere else, and
// their thumbnails. The covers go first: a thumbnail must not be older.
func putPage(t *testing.T, server *s3test.Server, thumbnails bool) {
	t.Helper()
	cover, thumb := encodeJPEG(t, 480, 640), encodeJPEG(t, Width, 320)
	for i := range pageCovers {
		server.Put("covers"+coverName(i), cover)
	}
	for i := range pageCovers {
		if thumbnails {
			server.Put("thumbs"+coverName(i), thumb)
		}
	}
}

// generatePage starts one generation per cover of the page.
func generatePage(ctx context.Context, service *Service) <-chan error {
	results := make(chan error, pageCovers)
	for i := range pageCovers {
		go func() {
			results <- service.generate(ctx, coverName(i), filepath.Join(service.dir, coverName(i)))
		}()
	}
	return results
}

// With the covers only in the bucket this is the main path, and nothing about it
// is decoded: it goes at the speed the store was configured for.
func TestColdPageOfBucketThumbnailsUsesTheStoresCapacity(t *testing.T) {
	server := s3test.New(t)
	service := serviceOverStore(t, server, storeCapacity)
	putPage(t, server, true)
	server.SetDelay(roundTrip)

	results := make(chan error, pageCovers)
	for i := range pageCovers {
		go func() {
			_, err := service.Path(context.Background(), coverName(i))
			results <- err
		}()
	}
	for range pageCovers {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}

	if peak := server.Peak(); peak <= maxSlots || peak > storeCapacity {
		t.Fatalf("%d requests at once, want more than %d and at most %d", peak, maxSlots, storeCapacity)
	}
	if got, want := server.Requests(), 2*pageCovers; got != want {
		t.Fatalf("%d requests, want a GET and a HEAD per cover, %d", got, want)
	}
	if writes := server.Writes(); len(writes) != 0 {
		t.Fatalf("wrote %v", writes)
	}
	for i := range pageCovers {
		if got := decodeSize(t, filepath.Join(service.dir, coverName(i))); got != image.Pt(Width, 320) {
			t.Fatalf("cover %d: size = %v", i, got)
		}
	}
}

// However long the queue, what the fetches hold is capacity times the limit on
// one object. Each is in memory twice for a moment while it is copied.
func TestColdPageMemoryFollowsTheCapacity(t *testing.T) {
	const (
		covers = 96
		bound  = 3 * storeCapacity * coverstore.MaxThumbnail
	)
	defer debug.SetGCPercent(debug.SetGCPercent(5))
	server := s3test.New(t)
	service := serviceOverStore(t, server, storeCapacity)
	cover := encodeJPEG(t, Width, 320)
	thumb := append(bytes.Clone(cover), make([]byte, coverstore.MaxThumbnail-len(cover))...)
	for i := range covers {
		server.Put("covers"+coverName(i), cover)
	}
	for i := range covers {
		server.Put("thumbs"+coverName(i), thumb)
	}
	server.SetDelay(roundTrip)
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	done := make(chan error, covers)
	for i := range covers {
		go func() {
			done <- service.generate(context.Background(), coverName(i), filepath.Join(service.dir, coverName(i)))
		}()
	}
	var peak uint64
	for finished := 0; finished < covers; {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			finished++
		case <-time.After(time.Millisecond):
		}
		var now runtime.MemStats
		runtime.ReadMemStats(&now)
		peak = max(peak, now.HeapAlloc)
	}
	if grown := peak - min(peak, before.HeapAlloc); grown > bound {
		t.Fatalf("heap grew by %d bytes, bound %d", grown, bound)
	}
}

// The capacity is max_active_requests and nothing else, and it is a limit: the
// generations past it have not started their read.
func TestBucketThumbnailFetchesFollowMaxActiveRequests(t *testing.T) {
	for _, limit := range []int{6, storeCapacity} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			server := s3test.New(t)
			service := serviceOverStore(t, server, limit)
			putPage(t, server, true)
			server.SetHang(true)
			defer server.SetHang(false)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			results := generatePage(ctx, service)
			waitFor(t, "every generation accounted for", func() bool {
				return server.Active() == limit && asleep() == pageCovers-limit
			})
			cancel()
			for range pageCovers {
				<-results
			}
			if peak := server.Peak(); peak != limit {
				t.Fatalf("%d reads at once, limit %d", peak, limit)
			}
		})
	}
}

// A cover that has to be decoded keeps the small bound however large the store's
// capacity is, and what is waiting for it holds none of the thumbnail fetches.
func TestCoversToDecodeKeepTheirBound(t *testing.T) {
	server := s3test.New(t)
	service := serviceOverStore(t, server, storeCapacity)
	putPage(t, server, false)
	server.SetStallBodies(true)
	defer server.SetStallBodies(false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := generatePage(ctx, service)
	limit := cap(service.remote)
	waitFor(t, "every generation accounted for", func() bool {
		return server.Active() == limit && asleep() == pageCovers-limit
	})
	held := len(service.thumbReads)
	cancel()
	for range pageCovers {
		<-results
	}
	if held != 0 {
		t.Fatalf("%d thumbnail fetches held by covers waiting their turn", held)
	}
}

// Every thumbnail fetch stalled, and a cover on disk is not waiting for any.
func TestStalledThumbnailFetchesLeaveCoversOnDiskAlone(t *testing.T) {
	server := s3test.New(t)
	service := serviceOverStore(t, server, storeCapacity)
	putPage(t, server, true)
	writeCover(t, service.sourceDir, encodeJPEG(t, 480, 640))
	server.SetHang(true)
	defer server.SetHang(false)

	stop, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := generatePage(stop, service)
	waitFor(t, "every fetch stalled", func() bool { return server.Active() == storeCapacity })

	ctx, stopLocal := context.WithTimeout(context.Background(), waitLimit)
	defer stopLocal()
	_, err := service.Path(ctx, rel)
	cancel()
	for range pageCovers {
		<-results
	}
	if err != nil {
		t.Fatalf("a cover on disk waited for the bucket: %v", err)
	}
}

// The capacity of the store is for covers that have nothing else. A stall may
// hold up a few covers on disk that were trying the bucket, and all the others
// must get through while it lasts.
func TestStallHoldsUpOnlyAFewCoversOnDisk(t *testing.T) {
	server := s3test.New(t)
	service := serviceOverStore(t, server, storeCapacity)
	cover := encodeJPEG(t, 480, 640)
	for i := range pageCovers {
		writeCoverAt(t, service.sourceDir, coverName(i), cover)
	}
	server.SetHang(true)
	defer server.SetHang(false)

	stop, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := generatePage(stop, service)
	held := cap(service.remote)
	for range pageCovers - held {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(waitLimit):
			t.Fatalf("%d covers on disk held up by the stall, limit %d", server.Active(), held)
		}
	}
	active := server.Active()
	cancel()
	for range held {
		<-results
	}
	if active != held {
		t.Fatalf("%d reads of the bucket, limit %d", active, held)
	}
}

// What the bucket holds under a thumbnail's name is bounded per object, or the
// capacity above would bound nothing. One past it is remade from the cover and
// replaced.
func TestThumbnailPastItsLimitIsRemade(t *testing.T) {
	service, server, _, dir := bucketService(t)
	oversized := append(encodeJPEG(t, Width, 100), make([]byte, coverstore.MaxThumbnail)...)
	server.Put(coverKey, encodeJPEG(t, 480, 640))
	server.Put(thumbKey, oversized)

	if _, err := service.Path(context.Background(), rel); err != nil {
		t.Fatal(err)
	}
	if got := decodeSize(t, filepath.Join(dir, rel)); got != image.Pt(Width, 320) {
		t.Fatalf("size = %v", got)
	}
	waitFor(t, "the replacement", func() bool {
		stored, _, _ := server.Object(thumbKey)
		return !bytes.Equal(stored, oversized)
	})
}

func TestWaitForAThumbnailFetchEndsWithTheGeneration(t *testing.T) {
	service, server, _, dir := bucketService(t)
	server.Put(thumbKey, encodeJPEG(t, Width, 320))
	defer occupy(service.thumbReads)()

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
