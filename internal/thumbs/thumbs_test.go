package thumbs

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gopds-api/internal/coverstore"
	"gopds-api/internal/objectstore/s3test"
)

const rel = "/lib/book-fb2.jpg"

func encodeJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 90, A: 255})
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, nil); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func writeCover(t *testing.T, dir string, data []byte) string {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return full
}

func decodeSize(t *testing.T, name string) image.Point {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if format != "jpeg" {
		t.Fatalf("format %s", format)
	}
	return image.Pt(config.Width, config.Height)
}

func diskService(t *testing.T) (service *Service, source, dir string) {
	t.Helper()
	source, dir = t.TempDir(), t.TempDir()
	return New(source, dir, coverstore.Default), source, dir
}

func TestLargeCoverIsScaledDownToWidth(t *testing.T) {
	service, source, _ := diskService(t)
	writeCover(t, source, encodeJPEG(t, 1400, 2240))

	name, err := service.Path(context.Background(), rel)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeSize(t, name); got != image.Pt(Width, 384) {
		t.Fatalf("size = %v", got)
	}
}

func TestSmallCoverIsNotEnlarged(t *testing.T) {
	service, source, _ := diskService(t)
	writeCover(t, source, encodeJPEG(t, 100, 160))

	name, err := service.Path(context.Background(), rel)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeSize(t, name); got != image.Pt(100, 160) {
		t.Fatalf("size = %v", got)
	}
}

func TestTransparentCoverGetsAWhiteBackground(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 400, 400))
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	thumb, err := Scale(encoded.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(thumb))
	if err != nil {
		t.Fatal(err)
	}
	if r, _, _, _ := decoded.At(10, 10).RGBA(); r < 0xf000 {
		t.Fatalf("background red = %x", r)
	}
}

func TestSecondRequestDoesNotRegenerate(t *testing.T) {
	service, source, dir := diskService(t)
	writeCover(t, source, encodeJPEG(t, 400, 600))
	name, err := service.Path(context.Background(), rel)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	past := before.ModTime().Add(time.Hour)
	if timeErr := os.Chtimes(name, past, past); timeErr != nil {
		t.Fatal(timeErr)
	}

	if _, pathErr := service.Path(context.Background(), rel); pathErr != nil {
		t.Fatal(pathErr)
	}
	after, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(past) {
		t.Fatal("a fresh thumbnail was made again")
	}
	if entries, _ := filepath.Glob(filepath.Join(dir, "lib", ".thumb-*")); len(entries) != 0 {
		t.Fatalf("left temporary files: %v", entries)
	}
}

func TestReplacedCoverRemakesTheThumbnail(t *testing.T) {
	service, source, _ := diskService(t)
	full := writeCover(t, source, encodeJPEG(t, 400, 600))
	name, err := service.Path(context.Background(), rel)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeSize(t, name); got != image.Pt(240, 360) {
		t.Fatalf("size = %v", got)
	}

	writeCover(t, source, encodeJPEG(t, 400, 400))
	later := time.Now().Add(time.Minute)
	if timeErr := os.Chtimes(full, later, later); timeErr != nil {
		t.Fatal(timeErr)
	}

	name, err = service.Path(context.Background(), rel)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeSize(t, name); got != image.Pt(240, 240) {
		t.Fatalf("size = %v: the old thumbnail survived", got)
	}
	// Dated with the source, or it would be remade on every request.
	thumb, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	if later.After(thumb.ModTime()) {
		t.Fatal("a thumbnail older than its source will be remade forever")
	}
}

func TestMissingSource(t *testing.T) {
	service, _, _ := diskService(t)
	if _, err := service.Path(context.Background(), rel); !errors.Is(err, ErrNoSource) {
		t.Fatalf("err = %v", err)
	}
}

func TestBrokenSource(t *testing.T) {
	service, source, dir := diskService(t)
	writeCover(t, source, []byte("not an image at all"))
	if _, err := service.Path(context.Background(), rel); !errors.Is(err, ErrBroken) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, rel)); !os.IsNotExist(err) {
		t.Fatal("a broken source left a thumbnail")
	}
}

func TestTruncatedSourceIsBroken(t *testing.T) {
	whole := encodeJPEG(t, 400, 600)
	if _, err := Scale(whole[:len(whole)/4]); !errors.Is(err, ErrBroken) {
		t.Fatalf("err = %v", err)
	}
}

// A PNG header is enough to claim any size, so the file below would take 64
// GB to decode. The test passing at all is the proof it is refused unread.
func TestHugeImageIsRefusedFromItsHeader(t *testing.T) {
	var small bytes.Buffer
	if err := png.Encode(&small, image.NewGray(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	huge := small.Bytes()
	// Width and height are the eight bytes after the IHDR tag.
	copy(huge[16:24], []byte{0, 0, 0x30, 0, 0, 0, 0x30, 0})
	if _, err := Scale(huge); !errors.Is(err, ErrBroken) {
		t.Fatalf("err = %v", err)
	}
}

func TestPathOutsideTheDirectoryIsResolvedInside(t *testing.T) {
	service, _, dir := diskService(t)
	_, err := service.Path(context.Background(), "../../etc/passwd")
	if !errors.Is(err, ErrNoSource) {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatal("wrote something")
	}
}

func TestDirectoryIsNotAThumbnail(t *testing.T) {
	service, source, _ := diskService(t)
	writeCover(t, source, encodeJPEG(t, 300, 300))
	if _, err := service.Path(context.Background(), rel); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Path(context.Background(), "/lib"); !errors.Is(err, ErrBroken) {
		t.Fatalf("err = %v", err)
	}
}

func remoteService(t *testing.T) (*Service, *s3test.Server, string) {
	t.Helper()
	server := s3test.New(t)
	covers := coverstore.New(server.Client(200*time.Millisecond, -1), "covers/")
	dir := t.TempDir()
	return New(t.TempDir(), dir, func() *coverstore.Store { return covers }), server, dir
}

func TestThumbnailFromARemoteCoverStaysLocal(t *testing.T) {
	service, server, dir := remoteService(t)
	server.Put("covers"+rel, encodeJPEG(t, 480, 480))

	name, err := service.Path(context.Background(), rel)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeSize(t, name); got != image.Pt(240, 240) {
		t.Fatalf("size = %v", got)
	}
	if name != filepath.Join(dir, rel) {
		t.Fatalf("name = %s", name)
	}

	server.Reset()
	if _, err := service.Path(context.Background(), rel); err != nil {
		t.Fatal(err)
	}
	if n := server.Requests(); n != 0 {
		t.Fatalf("a ready thumbnail made %d requests", n)
	}
}

func TestReadyThumbnailSurvivesAnOutage(t *testing.T) {
	service, server, _ := remoteService(t)
	server.Put("covers"+rel, encodeJPEG(t, 480, 480))
	if _, err := service.Path(context.Background(), rel); err != nil {
		t.Fatal(err)
	}
	server.SetDown(true)
	if _, err := service.Path(context.Background(), rel); err != nil {
		t.Fatalf("a thumbnail on disk failed in an outage: %v", err)
	}
}

func TestOutageIsNotAMissingCover(t *testing.T) {
	service, server, dir := remoteService(t)
	server.SetDown(true)
	_, err := service.Path(context.Background(), rel)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if errors.Is(err, ErrNoSource) {
		t.Fatal("an outage read as a missing cover")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatal("wrote something")
	}
}

func TestIdenticalRequestsCollapse(t *testing.T) {
	service, server, _ := remoteService(t)
	server.Put("covers"+rel, encodeJPEG(t, 480, 480))

	var wg sync.WaitGroup
	failures := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := service.Path(context.Background(), rel); err != nil {
				failures <- err
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	if n := server.Requests(); n != 1 {
		t.Fatalf("%d reads of one source", n)
	}
}

func TestGenerationsAreLimited(t *testing.T) {
	service, source, _ := diskService(t)
	writeCover(t, source, encodeJPEG(t, 480, 640))
	limit := cap(service.slots)
	if limit < 1 || limit > 4 {
		t.Fatalf("%d slots", limit)
	}
	for range limit {
		service.slots <- struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := service.generate(ctx, rel, filepath.Join(t.TempDir(), "x.jpg"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a generation ran past the limit: %v", err)
	}
}

func TestLeftoverTemporaryFilesGoAtStart(t *testing.T) {
	source, dir := t.TempDir(), t.TempDir()
	leftover := filepath.Join(dir, tempDir, ".thumb-123")
	if err := os.MkdirAll(filepath.Dir(leftover), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leftover, []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(dir, "lib", "book.jpg")
	if err := os.MkdirAll(filepath.Dir(kept), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kept, []byte("thumb"), 0o600); err != nil {
		t.Fatal(err)
	}

	New(source, dir, func() *coverstore.Store { return &coverstore.Store{} })

	if _, err := os.Stat(leftover); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the leftover stayed: %v", err)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("a thumbnail went with it: %v", err)
	}
}

func TestStartWithoutADirectoryWritesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-yet")
	New(t.TempDir(), dir, func() *coverstore.Store { return &coverstore.Store{} })
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("start created %s: %v", dir, err)
	}
}

func TestWritingLeavesNoTemporaryFileBeside(t *testing.T) {
	service, source, dir := diskService(t)
	writeCover(t, source, encodeJPEG(t, 480, 640))
	if _, err := service.Path(context.Background(), rel); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, tempDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("%d files left in the temporary directory", len(entries))
	}
	strays, err := filepath.Glob(filepath.Join(filepath.Dir(filepath.Join(dir, rel)), ".thumb-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(strays) != 0 {
		t.Fatalf("strays beside the thumbnail: %v", strays)
	}
}

func TestClearedDirectoryIsRefilled(t *testing.T) {
	service, source, dir := diskService(t)
	writeCover(t, source, encodeJPEG(t, 480, 640))
	if _, err := service.Path(context.Background(), rel); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	name, err := service.Path(context.Background(), rel)
	if err != nil {
		t.Fatalf("a cleared cache failed: %v", err)
	}
	if got := decodeSize(t, name); got.X != Width {
		t.Fatalf("size = %v", got)
	}
}

// With thumbnails kept under the posters directory, every thumbnail is also a
// file under the source directory, and must not become a source of another.
func TestThumbnailIsNeverASource(t *testing.T) {
	source := t.TempDir()
	dir := filepath.Join(source, ".thumbs")
	service := New(source, dir, func() *coverstore.Store { return &coverstore.Store{} })
	writeCover(t, source, encodeJPEG(t, 480, 640))
	if _, err := service.Path(context.Background(), rel); err != nil {
		t.Fatal(err)
	}

	for _, again := range []string{"/.thumbs" + rel, "/.thumbs/.thumbs" + rel, "/x/../.thumbs" + rel, "/" + tempDir + "/.thumb-1"} {
		_, err := service.Path(context.Background(), again)
		if !errors.Is(err, ErrNoSource) {
			t.Errorf("%s: err = %v", again, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".thumbs")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a thumbnail of a thumbnail was made: %v", err)
	}
}

func TestGenerationFromALocalCoverDoesNotQueueBehindARemoteOne(t *testing.T) {
	service, server, dir := remoteService(t)
	server.Put("covers/stuck.jpg", encodeJPEG(t, 480, 480))
	server.SetStallBodies(true)
	writeCover(t, service.sourceDir, encodeJPEG(t, 480, 640))

	for range cap(service.slots) {
		service.remote <- struct{}{}
	}
	defer func() {
		for range cap(service.slots) {
			<-service.remote
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	name, err := service.Path(ctx, rel)
	if err != nil {
		t.Fatalf("a cover on disk waited for the store: %v", err)
	}
	if name != filepath.Join(dir, rel) {
		t.Fatalf("name = %s", name)
	}
}
