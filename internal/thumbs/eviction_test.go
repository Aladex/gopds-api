package thumbs

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopds-api/internal/coverstore"
	"gopds-api/internal/objectstore/s3test"
)

const keptCover = "/lib/kept.jpg"

// patientService reads a fake bucket with a timeout longer than any test, so a
// stall lasts until the test ends it and nothing gets out of it by timing out.
func patientService(t *testing.T) (*Service, *s3test.Server, string) {
	t.Helper()
	server := s3test.New(t)
	covers := coverstore.New(server.Client(time.Minute, -1), "covers/")
	dir := t.TempDir()
	return New(t.TempDir(), dir, func() *coverstore.Store { return covers }), server, dir
}

// A cover removed while its generation waits for the processor is now a cover
// in the bucket. That read may stall, and it must not do so in a processor
// slot: the covers still on disk need those.
func TestEvictedCoverLeavesTheProcessorToCoversStillOnDisk(t *testing.T) {
	service, server, dir := patientService(t)
	cover := encodeJPEG(t, 32, 48)
	limit := cap(service.slots)
	for i := range limit {
		writeCoverAt(t, service.sourceDir, coverName(i), cover)
	}
	writeCoverAt(t, service.sourceDir, keptCover, cover)
	free := occupy(service.slots)

	stop, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan error, limit)
	for i := range limit {
		go func() {
			results <- service.generate(stop, coverName(i), filepath.Join(dir, coverName(i)))
		}()
	}
	waitFor(t, "generations waiting", func() bool { return asleep() == limit })
	for i := range limit {
		if err := os.Remove(filepath.Join(service.sourceDir, coverName(i))); err != nil {
			t.Fatal(err)
		}
	}
	server.SetHang(true)
	defer server.SetHang(false)
	free()
	waitFor(t, "reads of the bucket", func() bool { return server.Active() == limit })

	held := len(service.slots)
	ctx, stopKept := context.WithTimeout(context.Background(), waitLimit)
	defer stopKept()
	_, err := service.Path(ctx, keptCover)
	cancel()
	for range limit {
		<-results
	}
	if held != 0 {
		t.Errorf("%d processor slots held by stalled reads of the bucket", held)
	}
	if err != nil {
		t.Fatalf("a cover on disk waited behind the bucket: %v", err)
	}
}

// The eviction does not lose the thumbnail: the generation goes on from the
// bucket, or reports a cover that is nowhere.
func TestCoverEvictedWhileWaiting(t *testing.T) {
	for _, tt := range []struct {
		name     string
		inBucket bool
		want     error
	}{
		{name: "comes from the bucket", inBucket: true},
		{name: "is nowhere", want: ErrNoSource},
	} {
		t.Run(tt.name, func(t *testing.T) {
			service, server, dir := remoteService(t)
			writeCoverAt(t, service.sourceDir, rel, encodeJPEG(t, 400, 600))
			if tt.inBucket {
				server.Put(coverKey, encodeJPEG(t, 100, 200))
			}
			free := occupy(service.slots)

			result := make(chan error, 1)
			go func() { result <- service.generate(context.Background(), rel, filepath.Join(dir, rel)) }()
			waitFor(t, "the generation waiting", func() bool { return asleep() == 1 })
			if err := os.Remove(filepath.Join(service.sourceDir, rel)); err != nil {
				t.Fatal(err)
			}
			free()

			if err := <-result; !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if tt.inBucket {
				if got := decodeSize(t, filepath.Join(dir, rel)); got != image.Pt(100, 200) {
					t.Fatalf("size = %v", got)
				}
			}
		})
	}
}
