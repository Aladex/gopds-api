package thumbs

import (
	"bytes"
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopds-api/internal/coverstore"
	"gopds-api/internal/objectstore/s3test"
)

const (
	bucketTimeout = 200 * time.Millisecond
	coverKey      = "covers" + rel
	thumbKey      = "thumbs" + rel
	waitLimit     = 3 * time.Second
)

// bucketService keeps thumbnails in a fake bucket. source and dir are its two
// directories on disk.
func bucketService(t *testing.T) (service *Service, server *s3test.Server, source, dir string) {
	t.Helper()
	server = s3test.New(t)
	covers := coverstore.New(server.WriterClient(bucketTimeout, "thumbs/"), "covers/").WithThumbs("thumbs/")
	source, dir = t.TempDir(), t.TempDir()
	return New(source, dir, func() *coverstore.Store { return covers }), server, source, dir
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitLimit)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("no %s within %s", what, waitLimit)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func readsOf(server *s3test.Server, prefix string) int {
	count := 0
	for _, r := range server.Reads() {
		if strings.HasPrefix(r.Key, prefix) {
			count++
		}
	}
	return count
}

func TestBucketThumbnailIsSavedAndServed(t *testing.T) {
	service, server, _, dir := bucketService(t)
	stored := encodeJPEG(t, 240, 320)
	// The cover goes first: dated after the thumbnail, it would get it remade.
	server.Put(coverKey, encodeJPEG(t, 480, 640))
	server.Put(thumbKey, stored)

	name, err := service.Path(context.Background(), rel)
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(name)
	if err != nil || !bytes.Equal(got, stored) || name != filepath.Join(dir, rel) {
		t.Fatalf("name %s holds %d bytes: %v", name, len(got), err)
	}
	for _, r := range server.Reads() {
		if r.Key == coverKey && r.Method == "GET" {
			t.Fatal("read the cover although the bucket had the thumbnail")
		}
	}
	if writes := server.Writes(); len(writes) != 0 {
		t.Fatalf("wrote %v", writes)
	}

	server.Reset()
	if _, err := service.Path(context.Background(), rel); err != nil {
		t.Fatal(err)
	}
	if n := server.Requests(); n != 0 {
		t.Fatalf("a thumbnail on disk made %d requests", n)
	}
}

func TestLocalThumbnailComesBeforeTheBucket(t *testing.T) {
	service, server, _, dir := bucketService(t)
	server.Put(thumbKey, encodeJPEG(t, 240, 320))
	local := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, encodeJPEG(t, 100, 100), 0o600); err != nil {
		t.Fatal(err)
	}
	server.Reset()

	if _, err := service.Path(context.Background(), rel); err != nil {
		t.Fatal(err)
	}
	if n := server.Requests(); n != 0 {
		t.Fatalf("%d requests for a thumbnail on disk", n)
	}
}

func TestGeneratedThumbnailIsUploaded(t *testing.T) {
	service, server, _, _ := bucketService(t)
	server.Put(coverKey, encodeJPEG(t, 480, 640))

	name, err := service.Path(context.Background(), rel)
	if err != nil {
		t.Fatal(err)
	}

	local, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "upload", func() bool { _, _, ok := server.Object(thumbKey); return ok })
	stored, kind, _ := server.Object(thumbKey)
	if !bytes.Equal(stored, local) || kind != "image/jpeg" {
		t.Fatalf("stored %d bytes as %q, local %d", len(stored), kind, len(local))
	}
}

func TestClearedLocalThumbnailComesBackFromTheBucket(t *testing.T) {
	service, server, _, dir := bucketService(t)
	server.Put(coverKey, encodeJPEG(t, 480, 640))
	if _, err := service.Path(context.Background(), rel); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "upload", func() bool { _, _, ok := server.Object(thumbKey); return ok })

	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	server.Reset()
	if _, err := service.Path(context.Background(), rel); err != nil {
		t.Fatal(err)
	}

	for _, r := range server.Reads() {
		if r.Key == coverKey && r.Method == "GET" {
			t.Fatal("made the thumbnail again instead of reading it back")
		}
	}
	if n := len(server.Writes()); n != 0 {
		t.Fatalf("%d uploads of a thumbnail already there", n)
	}
}

func TestServingDoesNotWaitForTheUpload(t *testing.T) {
	service, server, _, _ := bucketService(t)
	server.Put(coverKey, encodeJPEG(t, 480, 640))
	server.SetHangWrites(true)

	started := time.Now()
	if _, err := service.Path(context.Background(), rel); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= bucketTimeout {
		t.Fatalf("took %s behind a hanging upload", elapsed)
	}
	waitFor(t, "upload attempt", func() bool { return len(server.Writes()) > 0 })
	server.SetHangWrites(false)
}

func TestUploadFailureStaysOutOfTheReaderWay(t *testing.T) {
	service, server, source, _ := bucketService(t)
	server.SetRefuseWrites(true)
	writeCover(t, source, encodeJPEG(t, 480, 640))
	if _, err := service.Path(context.Background(), rel); err != nil {
		t.Fatal(err)
	}
	for i := range 8 {
		name := "/lib/" + strconv.Itoa(i) + ".jpg"
		server.Put("covers"+name, encodeJPEG(t, 300, 300))
		if _, err := service.Path(context.Background(), name); err != nil {
			t.Fatalf("a refused upload reached a reader: %v", err)
		}
	}
	waitFor(t, "every upload attempt", func() bool { return len(server.Writes()) == 9 })

	server.Put("covers/lib/after.jpg", encodeJPEG(t, 300, 300))
	if _, err := service.Path(context.Background(), "/lib/after.jpg"); err != nil {
		t.Fatalf("refused uploads rested the reads: %v", err)
	}
}

func TestFullQueueDropsAndNeverWaits(t *testing.T) {
	service, server, source, _ := bucketService(t)
	server.SetHangWrites(true)
	writeCover(t, source, encodeJPEG(t, 480, 640))
	if _, err := service.Path(context.Background(), rel); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "upload attempt", func() bool { return len(server.Writes()) == 1 })

	started := time.Now()
	for range 2 * cap(service.uploads) {
		service.queueUpload(rel)
	}
	if elapsed := time.Since(started); elapsed >= bucketTimeout {
		t.Fatalf("queueing took %s", elapsed)
	}
	if n := len(service.uploads); n != cap(service.uploads) {
		t.Fatalf("%d waiting, the queue holds %d", n, cap(service.uploads))
	}
	server.SetHangWrites(false)
	for len(service.uploads) > 0 {
		<-service.uploads
	}
}

func TestNewerLocalCoverBeatsTheBucketThumbnail(t *testing.T) {
	service, server, source, _ := bucketService(t)
	cover := writeCover(t, source, encodeJPEG(t, 480, 640))
	now := time.Now()
	if err := os.Chtimes(cover, now, now); err != nil {
		t.Fatal(err)
	}
	stale := encodeJPEG(t, 240, 100)
	server.PutAt(thumbKey, stale, now.Add(-time.Hour))

	name, err := service.Path(context.Background(), rel)
	if err != nil {
		t.Fatal(err)
	}

	if got := decodeSize(t, name); got != image.Pt(240, 320) {
		t.Fatalf("served the stale thumbnail: %v", got)
	}
	waitFor(t, "replacement", func() bool {
		stored, _, _ := server.Object(thumbKey)
		return !bytes.Equal(stored, stale)
	})
}

func TestReplacedLocalCoverReplacesBothCopies(t *testing.T) {
	service, server, source, _ := bucketService(t)
	cover := writeCover(t, source, encodeJPEG(t, 480, 640))
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(cover, past, past); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Path(context.Background(), rel); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "upload", func() bool { _, _, ok := server.Object(thumbKey); return ok })
	first, _, _ := server.Object(thumbKey)
	server.PutAt(thumbKey, first, past)

	writeCover(t, source, encodeJPEG(t, 480, 960))

	name, err := service.Path(context.Background(), rel)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeSize(t, name); got != image.Pt(240, 480) {
		t.Fatalf("size = %v", got)
	}
	waitFor(t, "replacement", func() bool {
		stored, _, _ := server.Object(thumbKey)
		return !bytes.Equal(stored, first)
	})
}

func TestOlderLocalCoverTakesTheBucketThumbnail(t *testing.T) {
	service, server, source, _ := bucketService(t)
	cover := writeCover(t, source, encodeJPEG(t, 480, 640))
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(cover, past, past); err != nil {
		t.Fatal(err)
	}
	stored := encodeJPEG(t, 240, 100)
	server.PutAt(thumbKey, stored, past.Add(time.Hour))

	name, err := service.Path(context.Background(), rel)
	if err != nil {
		t.Fatal(err)
	}

	if got := decodeSize(t, name); got != image.Pt(240, 100) {
		t.Fatalf("made a new one: %v", got)
	}
	if n := len(server.Writes()); n != 0 {
		t.Fatalf("%d uploads", n)
	}
}

func TestThumbnailMadeInTheSameSecondIsNotStale(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	if newerThan(now.Add(900*time.Millisecond), now) {
		t.Fatal("a Last-Modified cut to the second made its own cover look newer")
	}
	if !newerThan(now.Add(time.Second), now) {
		t.Fatal("a cover a second newer did not count")
	}
}

func TestNewerBucketCoverBeatsTheBucketThumbnail(t *testing.T) {
	service, server, _, _ := bucketService(t)
	now := time.Now()
	server.PutAt(coverKey, encodeJPEG(t, 480, 640), now)
	stale := encodeJPEG(t, 240, 100)
	server.PutAt(thumbKey, stale, now.Add(-time.Hour))

	name, err := service.Path(context.Background(), rel)
	if err != nil {
		t.Fatal(err)
	}

	if got := decodeSize(t, name); got != image.Pt(240, 320) {
		t.Fatalf("served the stale thumbnail: %v", got)
	}
	waitFor(t, "replacement", func() bool {
		stored, _, _ := server.Object(thumbKey)
		return !bytes.Equal(stored, stale)
	})
}

func TestBucketThumbnailOfACoverGoneEverywhereStillServes(t *testing.T) {
	service, server, _, _ := bucketService(t)
	server.Put(thumbKey, encodeJPEG(t, 240, 320))
	if _, err := service.Path(context.Background(), rel); err != nil {
		t.Fatal(err)
	}
}

func TestBrokenBucketThumbnailIsReplaced(t *testing.T) {
	service, server, _, _ := bucketService(t)
	server.Put(coverKey, encodeJPEG(t, 480, 640))
	server.Put(thumbKey, []byte("not a picture"))

	name, err := service.Path(context.Background(), rel)
	if err != nil {
		t.Fatal(err)
	}

	if got := decodeSize(t, name); got != image.Pt(240, 320) {
		t.Fatalf("size = %v", got)
	}
	waitFor(t, "replacement", func() bool {
		stored, _, _ := server.Object(thumbKey)
		return string(stored) != "not a picture"
	})
}

func TestBucketDownStillMakesOneFromALocalCover(t *testing.T) {
	service, server, source, _ := bucketService(t)
	writeCover(t, source, encodeJPEG(t, 480, 640))
	server.SetDown(true)

	if _, err := service.Path(context.Background(), rel); err != nil {
		t.Fatalf("an outage stopped a cover that is on disk: %v", err)
	}
}

func TestBucketStallStillMakesOneFromALocalCover(t *testing.T) {
	service, server, source, _ := bucketService(t)
	writeCover(t, source, encodeJPEG(t, 480, 640))
	server.SetHang(true)
	defer server.SetHang(false)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := service.Path(ctx, rel); err != nil {
		t.Fatalf("a stall stopped a cover that is on disk: %v", err)
	}
}

func TestBucketDownAndNoLocalCoverIsUnavailable(t *testing.T) {
	service, server, _, _ := bucketService(t)
	server.SetDown(true)
	_, err := service.Path(context.Background(), rel)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestMissingCoverUploadsNothing(t *testing.T) {
	service, server, _, _ := bucketService(t)
	if _, err := service.Path(context.Background(), rel); !errors.Is(err, ErrNoSource) {
		t.Fatalf("err = %v", err)
	}
	if n := len(server.Writes()); n != 0 {
		t.Fatalf("%d uploads", n)
	}
}

func TestUploadSwitchedOffKeepsThumbnailsLocal(t *testing.T) {
	service, server, _ := remoteService(t)
	server.Put(coverKey, encodeJPEG(t, 480, 640))
	server.Put(thumbKey, encodeJPEG(t, 240, 100))

	name, err := service.Path(context.Background(), rel)
	if err != nil {
		t.Fatal(err)
	}

	if got := decodeSize(t, name); got != image.Pt(240, 320) {
		t.Fatalf("read the bucket's thumbnail with uploads off: %v", got)
	}
	if n := readsOf(server, "thumbs/"); n != 0 {
		t.Fatalf("%d requests under the thumbnail prefix", n)
	}
	time.Sleep(20 * time.Millisecond)
	if n := len(server.Writes()); n != 0 {
		t.Fatalf("%d uploads", n)
	}
}
