// Package thumbs makes small covers on demand.
//
// A thumbnail is a JPEG 240 pixels wide, made from the cover the first time it
// is asked for and kept on local disk from then on. The whole catalog is never
// walked in advance. The source is read through the cover store, so a
// thumbnail can be made whether or not the original is on this machine; the
// thumbnail itself always stays local.
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
	"time"

	"gopds-api/internal/coverstore"
	"gopds-api/internal/objectstore"
	"gopds-api/internal/safeio"
	"gopds-api/internal/safepath"

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
	covers    func() *coverstore.Store
	slots     chan struct{}
	group     singleflight.Group
}

// New builds a service. sourceDir is where the originals are on disk, dir where
// thumbnails go.
func New(sourceDir, dir string, covers func() *coverstore.Store) *Service {
	return &Service{
		sourceDir: sourceDir,
		dir:       dir,
		covers:    covers,
		slots:     make(chan struct{}, generationSlots()),
	}
}

// generationSlots leaves most of the machine to the requests: the first
// opening of a collection asks for dozens of thumbnails at once.
func generationSlots() int {
	return max(1, min(runtime.NumCPU()/shareOfCores, maxSlots))
}

// Path returns the file of the thumbnail at rel, making it if it is missing or
// older than a cover on disk. An original that exists only in object storage
// does not change, so a thumbnail of one needs no request to stay valid.
func (s *Service) Path(ctx context.Context, rel string) (string, error) {
	target, err := safepath.Resolve(s.dir, rel)
	if err != nil {
		return "", err
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
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return fmt.Errorf("%w: %w", ErrUnavailable, ctx.Err())
	}
	data, modified, err := s.readSource(ctx, rel)
	if err != nil {
		return err
	}
	encoded, err := Scale(data)
	if err != nil {
		return err
	}
	return write(target, encoded, modified)
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
func write(target string, data []byte, sourceModified time.Time) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, safeio.DirMode); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".thumb-*")
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
