// Package thumbs makes small covers on demand.
//
// A thumbnail is a JPEG 240 pixels wide, made from the cover the first time it
// is asked for. The whole catalog is never walked in advance. The source is
// read through the cover store, so a thumbnail can be made whether or not the
// original is on this machine.
//
// On local disk a thumbnail is a cache that may be cleared at any time. Without
// object storage that disk is the only home it has. With object storage it is
// looked for on disk, then in the bucket, and only then made, and one that was
// made is uploaded in the background so that it need not be made again. An
// upload that fails or is dropped costs nothing but that.
//
// A thumbnail in the bucket is good for as long as its object is not older than
// the cover it was made from, by the date of the cover on disk or, if there is
// none on disk, in the bucket. An older one is ignored, remade and replaced.
//
// Memory for covers follows the slots, not the queue: a generation holds no
// bytes until it has a slot, and keeps the slot until its thumbnail is written.
// Reads of the bucket have slots of their own, so a store that stalls holds up
// those and not the covers on disk, which do not wait for them at all: with
// every bucket read taken, a thumbnail is made from its cover on disk even if
// the bucket has one.
package thumbs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // decodes covers extracted as GIF
	"image/jpeg"
	_ "image/png" // decodes covers extracted as PNG
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"gopds-api/internal/coverstore"
	"gopds-api/internal/objectstore"
	"gopds-api/internal/safeio"
	"gopds-api/internal/safepath"
	"gopds-api/logging"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decodes covers extracted as WebP
	"golang.org/x/sync/singleflight"
)

const (
	// Width is the width of a thumbnail in pixels. A book card shows a cover
	// at most 108 CSS pixels wide, so this is sharp at 2x with room to spare.
	Width = 240
	// quality matches the measurement on real covers: about 18 KB each.
	quality = 78

	// Covers past these are refused from their header, before any decoding:
	// 40 megapixels already take 160 MB to decode.
	maxSide   = 16_000
	maxPixels = 40_000_000
	// maxSourceBytes bounds the file read to make a thumbnail.
	maxSourceBytes = 64 << 20

	// A generation is one core's work: at most half of them, and never more
	// than maxSlots however large the machine.
	shareOfCores = 2
	maxSlots     = 4

	// generationTimeout bounds one generation, source read included.
	generationTimeout = 30 * time.Second

	// tempDir is under the thumbnail directory and holds files being written.
	// One place for them, so that a start can clear what a crash left without
	// walking hundreds of thousands of thumbnails.
	tempDir     = ".tmp"
	tempPattern = ".thumb-*"

	// uploadQueue bounds the thumbnails waiting to go to the bucket. A name is
	// all that waits, so this costs next to nothing, and what does not fit is
	// dropped: it stays local until someone clears it.
	uploadQueue = 1024
)

// Errors of Path. ErrNoSource and ErrBroken are both "no thumbnail", and the
// route answers 404 to both so that the page can show its own tile;
// ErrUnavailable is the store being down, which says nothing about the cover.
var (
	ErrNoSource    = errors.New("thumbnail source not found")
	ErrBroken      = errors.New("thumbnail source is not a usable image")
	ErrUnavailable = objectstore.ErrUnavailable
)

// Service makes and finds thumbnails.
type Service struct {
	sourceDir string
	dir       string
	// insideSource is dir relative to sourceDir, empty if it is elsewhere.
	insideSource string
	covers       func() *coverstore.Store
	// slots bounds the work of the processor, and with it the covers read from
	// disk: one is taken before the read. remote bounds what is read from
	// object storage, thumbnails and covers alike, and is held for as long as
	// those bytes are in memory. It can be held for as long as the store
	// stalls, which is why covers on disk do not queue for it.
	slots  chan struct{}
	remote chan struct{}
	group  singleflight.Group

	uploads     chan string
	startUpload sync.Once
}

// New builds a service. sourceDir is where the originals are on disk, dir where
// thumbnails go.
func New(sourceDir, dir string, covers func() *coverstore.Store) *Service {
	s := &Service{
		sourceDir:    sourceDir,
		dir:          dir,
		insideSource: nestedIn(sourceDir, dir),
		covers:       covers,
		slots:        make(chan struct{}, generationSlots()),
		remote:       make(chan struct{}, generationSlots()),
		uploads:      make(chan string, uploadQueue),
	}
	s.clearTemporaries()
	return s
}

// nestedIn is dir relative to parent, empty when dir is not inside it.
func nestedIn(parent, dir string) string {
	rel, err := filepath.Rel(parent, dir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return rel
}

// clearTemporaries removes what a crash left mid-write.
func (s *Service) clearTemporaries() {
	leftovers, err := filepath.Glob(filepath.Join(s.dir, tempDir, tempPattern))
	if err != nil {
		logging.Warnf("thumbs: looking for leftovers: %v", err)
		return
	}
	for _, name := range leftovers {
		if err := os.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			logging.Warnf("thumbs: cannot remove %s: %v", name, err)
		}
	}
}

// isThumbnail reports a path that names one of this service's own files
// rather than a cover: covers are not looked for among them.
func (s *Service) isThumbnail(rel string) bool {
	clean := strings.TrimLeft(filepath.Clean(string(filepath.Separator)+rel), string(filepath.Separator))
	under := func(dir string) bool {
		return clean == dir || strings.HasPrefix(clean, dir+string(filepath.Separator))
	}
	return under(tempDir) || (s.insideSource != "" && under(s.insideSource))
}

// generationSlots leaves most of the machine to the requests: the first
// opening of a collection asks for dozens of thumbnails at once.
func generationSlots() int {
	return max(1, min(runtime.NumCPU()/shareOfCores, maxSlots))
}

// Path returns the file of the thumbnail at rel: the one on disk unless it is
// missing or older than a cover on disk, else the one in the bucket, else a new
// one. A thumbnail on disk of a cover that is not on disk costs no request and
// is not checked against the bucket.
func (s *Service) Path(ctx context.Context, rel string) (string, error) {
	target, err := safepath.Resolve(s.dir, rel)
	if err != nil {
		return "", err
	}
	if s.isThumbnail(rel) {
		return "", ErrNoSource
	}
	if fresh(target, s.sourceDir, rel) {
		return target, nil
	}
	// One generation per thumbnail, however many requests wait for it. It
	// runs detached from them: the page that asked first may leave while the
	// next one is waiting.
	done := s.group.DoChan(rel, func() (any, error) {
		generation, cancel := context.WithTimeout(context.WithoutCancel(ctx), generationTimeout)
		defer cancel()
		return nil, s.generate(generation, rel, target)
	})
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case result := <-done:
		if result.Err != nil {
			return "", result.Err
		}
		return target, nil
	}
}

// fresh reports a thumbnail that can be served as it is.
func fresh(target, sourceDir, rel string) bool {
	thumb, err := os.Stat(target)
	if err != nil || !thumb.Mode().IsRegular() {
		return false
	}
	source, onDisk := coverstore.ModTimeOnDisk(sourceDir, rel)
	return !onDisk || !source.After(thumb.ModTime())
}

func (s *Service) generate(ctx context.Context, rel, target string) error {
	store := s.covers()
	original, onDisk := coverstore.ModTimeOnDisk(s.sourceDir, rel)
	if onDisk {
		return s.generateFromDisk(ctx, store, rel, target, original)
	}
	return s.generateFromStore(ctx, store, rel, target)
}

// generateFromDisk makes a thumbnail of a cover on disk, unless the bucket has
// one and can hand it over right now.
func (s *Service) generateFromDisk(
	ctx context.Context, store *coverstore.Store, rel, target string, original time.Time,
) error {
	if store.ThumbsInBucket() && s.fromBucketIfFree(ctx, store, rel, target, original) {
		return nil
	}
	leave, err := enter(ctx, s.slots)
	if err != nil {
		return err
	}
	defer leave()
	data, modified, err := s.readSource(ctx, rel)
	if err != nil {
		return err
	}
	return s.finish(store, rel, target, data, modified)
}

// generateFromStore gets a thumbnail of a cover that is not on disk. It takes
// the processor's slot only for the last step: a stalled read must not hold one
// of those, and bytes waiting for one are still counted in remote.
func (s *Service) generateFromStore(ctx context.Context, store *coverstore.Store, rel, target string) error {
	leaveRemote, err := enter(ctx, s.remote)
	if err != nil {
		return err
	}
	defer leaveRemote()
	if store.ThumbsInBucket() && s.fromBucket(ctx, store, rel, target, time.Time{}, false) {
		return nil
	}
	data, modified, err := s.readSource(ctx, rel)
	if err != nil {
		return err
	}
	leave, err := enter(ctx, s.slots)
	if err != nil {
		return err
	}
	defer leave()
	return s.finish(store, rel, target, data, modified)
}

// enter takes a slot, waiting for one for as long as ctx allows. The function
// it returns gives the slot back.
func enter(ctx context.Context, slots chan struct{}) (leave func(), err error) {
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, ctx.Err())
	}
}

// finish turns a cover's bytes into a thumbnail on disk.
func (s *Service) finish(store *coverstore.Store, rel, target string, data []byte, modified time.Time) error {
	encoded, err := Scale(data)
	if err != nil {
		return err
	}
	if err := s.write(target, encoded, modified); err != nil {
		return err
	}
	if store.ThumbsInBucket() {
		s.queueUpload(rel)
	}
	return nil
}

// fromBucketIfFree is fromBucket for a cover on disk, which has something
// better to do than wait for a read of the bucket.
func (s *Service) fromBucketIfFree(
	ctx context.Context, store *coverstore.Store, rel, target string, original time.Time,
) bool {
	select {
	case s.remote <- struct{}{}:
		defer func() { <-s.remote }()
		return s.fromBucket(ctx, store, rel, target, original, true)
	default:
		return false
	}
}

// fromBucket saves the bucket's thumbnail to disk if it has a good one. False
// means make a new one, which an outage of the bucket should not get in the way
// of when the cover is on disk.
func (s *Service) fromBucket(
	ctx context.Context, store *coverstore.Store, rel, target string, original time.Time, onDisk bool,
) bool {
	thumb, err := store.OpenThumb(ctx, rel)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			logging.Warnf("thumbs: reading %s from the bucket: %v", rel, err)
		}
		return false
	}
	defer func() { _ = thumb.Close() }()
	data, err := io.ReadAll(thumb)
	if err != nil {
		return false
	}
	if _, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil {
		logging.Warnf("thumbs: %s in the bucket is not an image: %v", rel, err)
		return false
	}
	if !onDisk {
		// A cover that cannot be dated says nothing against the thumbnail.
		remote, err := store.RemoteModTime(ctx, rel)
		if err == nil && newerThan(remote, thumb.ModTime) {
			return false
		}
	} else if newerThan(original, thumb.ModTime) {
		return false
	}
	if err := s.write(target, data, original); err != nil {
		logging.Warnf("thumbs: saving %s: %v", rel, err)
		return false
	}
	return true
}

// newerThan compares at the second, which is all a Last-Modified header holds:
// a thumbnail made right after its cover must not count as older than it.
func newerThan(cover, thumb time.Time) bool {
	return cover.Truncate(time.Second).After(thumb)
}

// queueUpload hands a thumbnail to the background uploader, never waiting.
func (s *Service) queueUpload(rel string) {
	s.startUpload.Do(func() { go s.uploadLoop() })
	select {
	case s.uploads <- rel:
	default:
		logging.Warnf("thumbs: upload queue is full, %s stays local", rel)
	}
}

// uploadLoop runs for the life of the process. One at a time is enough: this
// is a page's worth of thumbnails, and nothing is waiting for them.
func (s *Service) uploadLoop() {
	for rel := range s.uploads {
		s.upload(rel)
	}
}

// upload sends what is on disk by now, which may be newer than what was queued.
func (s *Service) upload(rel string) {
	target, err := safepath.Resolve(s.dir, rel)
	if err != nil {
		return
	}
	// #nosec G304 -- target comes from safepath.Resolve above, which refuses
	// anything outside the thumbnail directory.
	data, err := os.ReadFile(target)
	if err != nil {
		logging.Debugf("thumbs: %s is gone before its upload: %v", rel, err)
		return
	}
	if err := s.covers().PutThumb(context.Background(), rel, data); err != nil {
		logging.Warnf("thumbs: uploading %s: %v", rel, err)
	}
}

func (s *Service) readSource(ctx context.Context, rel string) ([]byte, time.Time, error) {
	cover, err := s.covers().Open(ctx, s.sourceDir, rel)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, time.Time{}, ErrNoSource
	case err != nil:
		return nil, time.Time{}, err
	}
	defer func() { _ = cover.Close() }()
	data, err := io.ReadAll(io.LimitReader(cover, maxSourceBytes+1))
	if err != nil {
		// A directory where a cover should be is not a usable image either.
		if cover.Local {
			return nil, time.Time{}, fmt.Errorf("%w: %w", ErrBroken, err)
		}
		return nil, time.Time{}, err
	}
	if len(data) > maxSourceBytes {
		return nil, time.Time{}, fmt.Errorf("%w: over %d bytes", ErrBroken, maxSourceBytes)
	}
	return data, cover.ModTime, nil
}

// Scale decodes a cover and encodes its thumbnail. Dimensions are checked from
// the header first, so a huge or broken file is refused without being unpacked.
// A cover already narrower than Width keeps its size.
func Scale(data []byte) ([]byte, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBroken, err)
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > maxSide || config.Height > maxSide ||
		config.Width*config.Height > maxPixels {
		return nil, fmt.Errorf("%w: %dx%d", ErrBroken, config.Width, config.Height)
	}
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBroken, err)
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width > Width {
		height = max(1, height*Width/width)
		width = Width
	}
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	// White under transparency: JPEG has no alpha, and black is what a PNG
	// cover with a transparent background would otherwise get.
	draw.Draw(canvas, canvas.Bounds(), image.White, image.Point{}, draw.Src)
	draw.ApproxBiLinear.Scale(canvas, canvas.Bounds(), source, bounds, draw.Over, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, canvas, &jpeg.Options{Quality: quality}); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBroken, err)
	}
	return out.Bytes(), nil
}

// write stores a thumbnail atomically, dated no earlier than its source: a
// cover with a date from the future would otherwise be regenerated forever.
// Directories are made on every write because this tree is a cache: anyone may
// clear it at any time.
func (s *Service) write(target string, data []byte, sourceModified time.Time) error {
	temps := filepath.Join(s.dir, tempDir)
	if err := os.MkdirAll(temps, safeio.DirMode); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), safeio.DirMode); err != nil {
		return err
	}
	temp, err := os.CreateTemp(temps, tempPattern)
	if err != nil {
		return err
	}
	name := temp.Name()
	_, err = temp.Write(data)
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err == nil && sourceModified.After(time.Now()) {
		err = os.Chtimes(name, sourceModified, sourceModified)
	}
	if err == nil {
		err = os.Rename(name, target)
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}
