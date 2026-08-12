//go:build e2ereal

// End-to-end check of the cold build over real books from the catalogue,
// against a real Redis. Fixtures prove the shape of the pipeline; this proves
// it survives contact with books nobody wrote for it.
//
// Run: go test -tags e2ereal -run TestRealBooks ./services/ -v
//
//	REAL_BOOKS_DIR — directory with .fb2 files
//	PREVIEW_REDIS  — host:port of a scratch Redis (default 127.0.0.1:6380)
package services

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/go-redis/redis"
	"gopds-api/models"
)

type realRepo struct{ book *models.Book }

func (r realRepo) GetBook(int64) (*models.Book, error) { return r.book, nil }

type fileLoader struct{ path string }

func (l fileLoader) Load(_ context.Context, _, _ string, _ int64) ([]byte, error) {
	return os.ReadFile(l.path)
}

var imgSrc = regexp.MustCompile(`<img[^>]+src="([^"]+)"`)

func TestRealBooksEndToEnd(t *testing.T) {
	dir := os.Getenv("REAL_BOOKS_DIR")
	if dir == "" {
		t.Skip("REAL_BOOKS_DIR is not set")
	}
	addr := os.Getenv("PREVIEW_REDIS")
	if addr == "" {
		addr = "127.0.0.1:6380"
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	if err := client.Ping().Err(); err != nil {
		t.Skipf("no Redis at %s: %v", addr, err)
	}
	defer client.Close()

	files, err := filepath.Glob(filepath.Join(dir, "*.fb2"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no books in %s (%v)", dir, err)
	}

	cache := NewRedisPreviewCache(client)
	for i, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			book := &models.Book{
				ID:       int64(i + 1),
				MD5:      fmt.Sprintf("e2e%032d", i+1),
				Format:   "fb2",
				Path:     "archive.zip",
				FileName: filepath.Base(path),
				Approved: true,
			}
			svc := NewPreviewService(realRepo{book}, fileLoader{path}, cache, 2, PreviewLimits{}, 0, 0)

			raw, err := svc.Load(context.Background(), book.ID, false)
			if err != nil {
				t.Fatalf("cold build refused the book: %v", err)
			}

			var m PreviewManifest
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatalf("the entry point is not a manifest: %v", err)
			}
			if m.ChunkCount == 0 {
				t.Fatal("manifest declares no portions")
			}

			key := buildCacheKey(book.ID, book.MD5, m.Revision)
			ordinals := map[int]bool{}
			for _, ref := range m.Images {
				ordinals[ref.Ordinal] = true
			}

			// Every portion the manifest promises must exist, must be HTML,
			// and every image it points at must already be in the cache
			// under this revision — that is the invariant the manifest
			// stands for.
			totalRefs, htmlBytes := 0, 0
			for idx := 0; idx < m.ChunkCount; idx++ {
				chunk, err := cache.GetChunk(context.Background(), key, idx)
				if err != nil {
					t.Fatalf("portion %d promised by the manifest is missing: %v", idx, err)
				}
				htmlBytes += len(chunk)
				html := string(chunk)
				if strings.Contains(html, "<?xml") || strings.Contains(html, "FictionBook") {
					t.Fatalf("portion %d is the raw book, not a render", idx)
				}
				for _, mm := range imgSrc.FindAllStringSubmatch(html, -1) {
					totalRefs++
					src := mm[1]
					parts := strings.Split(strings.Trim(src, "/"), "/")
					n, cerr := strconv.Atoi(parts[len(parts)-1])
					if cerr != nil {
						t.Fatalf("portion %d references %q, which carries no ordinal", idx, src)
					}
					if !strings.Contains(src, m.Revision) {
						t.Fatalf("portion %d references %q, not revision %s", idx, src, m.Revision)
					}
					if !ordinals[n] {
						t.Fatalf("portion %d references image %d, absent from the manifest", idx, n)
					}
					payload, mime, gerr := cache.GetImage(context.Background(), key, n)
					if gerr != nil {
						t.Fatalf("portion %d references image %d, absent from the cache: %v", idx, n, gerr)
					}
					if len(payload) == 0 || mime == "" {
						t.Fatalf("image %d is cached empty (%d bytes, mime %q)", n, len(payload), mime)
					}
				}
			}
			t.Logf("portions=%d html=%.1f KiB images=%d refs=%d revision=%s",
				m.ChunkCount, float64(htmlBytes)/1024, len(m.Images), totalRefs, m.Revision)
		})
	}
}
