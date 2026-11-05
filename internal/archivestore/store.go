// Package archivestore opens the library's zip archives wherever they are.
//
// A file on disk always wins and is opened exactly as it always was. Only an
// archive missing from disk is read from object storage, and only when object
// storage is configured: an installation without it never reaches any code
// here beyond zip.OpenReader. A half-migrated library is therefore an ordinary
// state, and removing the configuration returns to the disk alone.
//
// An archive in object storage is read in place, through ranges. archive/zip
// needs only an io.ReaderAt and a size, and one book needs only the end of the
// archive plus its own few hundred kilobytes, so a request for one book never
// downloads the seven gigabytes around it. The workers that do read every
// entry get a temporary copy instead, one at a time, through OpenWhole.
//
// Nothing here removes an archive. The only files this package deletes are
// its own cache entries and temporary copies.
package archivestore

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopds-api/internal/objectstore"
	"gopds-api/logging"

	"golang.org/x/sync/singleflight"
)

// Errors of this package. They wrap what callers already test for, so that a
// missing archive still satisfies errors.Is(err, fs.ErrNotExist).
var (
	// ErrNotFound is an archive neither on disk nor in object storage.
	ErrNotFound = fmt.Errorf("archive not found: %w", fs.ErrNotExist)
	// ErrEntryNotFound is an archive that opened and has no such entry.
	ErrEntryNotFound = errors.New("entry not found in archive")
	// ErrUnavailable is an archive object storage could not deliver right now.
	ErrUnavailable = objectstore.ErrUnavailable
	// ErrTooLarge is an archive that does not fit the temporary disk budget.
	ErrTooLarge = errors.New("archive is larger than the temporary disk budget")
)

const (
	defaultDirectoryCache = 64 << 20
	defaultTempBudget     = 20 << 30

	// directoryRevalidate is how long a cached directory is trusted without
	// asking. Every ranged read checks the object's version on its own, so
	// this only bounds how long a replaced archive can look unchanged to a
	// request that is answered from the book cache.
	directoryRevalidate = 15 * time.Minute

	// defaultReadBudget stays under the example server's write timeout of ten
	// seconds, so a store too slow to deliver is an answer, not a dropped
	// connection.
	defaultReadBudget = 9 * time.Second

	// wholeFetchMinEntries is how many entries of one archive make a
	// temporary copy cheaper than reading them one range at a time.
	wholeFetchMinEntries = 32
)

// Config is how a Store reaches object storage. The zero value of everything
// but Root and Client takes a default.
type Config struct {
	// Root is app.files_path: what a local path is relative to.
	Root string
	// Client is the bucket. Nil means the disk alone.
	Client *objectstore.Client
	// Prefix goes in front of an archive's relative path to make its key.
	Prefix string
	// CacheDir holds extracted books and temporary copies.
	CacheDir string
	// BookCacheBytes bounds the extracted books. Zero disables that cache.
	BookCacheBytes int64
	// DirectoryCacheBytes bounds the central directories kept in memory.
	DirectoryCacheBytes int64
	// TempBudgetBytes is the largest temporary copy a worker may make.
	TempBudgetBytes int64
	// ReadBudget bounds a load of a directory, and one book's read start to
	// finish. Zero means the default.
	ReadBudget time.Duration
}

// Store opens archives. The zero value and New without a client serve from
// disk alone.
type Store struct {
	root       string
	rootAbs    string
	client     *objectstore.Client
	prefix     string
	tempDir    string
	tempBudget int64
	readBudget time.Duration

	directories *directoryCache
	books       *bookCache
	loads       singleflight.Group
	wholeSlot   chan struct{}
	now         func() time.Time
}

var defaultStore atomic.Pointer[Store]

// Default is the application's store: the disk alone until SetDefault.
func Default() *Store {
	if s := defaultStore.Load(); s != nil {
		return s
	}
	return &Store{}
}

// SetDefault installs the application's store. Called once at startup; tests
// restore the previous one.
func SetDefault(s *Store) { defaultStore.Store(s) }

// New builds a store. It creates its cache directories, and clears temporary
// copies a crash left behind, but does not touch the network.
func New(cfg *Config) (*Store, error) {
	s := &Store{root: cfg.Root, client: cfg.Client, now: time.Now}
	if cfg.Root != "" {
		abs, err := filepath.Abs(cfg.Root)
		if err != nil {
			return nil, fmt.Errorf("archivestore: root: %w", err)
		}
		s.rootAbs = abs
	}
	if cfg.Client == nil {
		return s, nil
	}
	if cfg.CacheDir == "" {
		return nil, errors.New("archivestore: a cache directory is required with object storage")
	}
	s.prefix = cfg.Prefix
	s.readBudget = cfg.ReadBudget
	if s.readBudget <= 0 {
		s.readBudget = defaultReadBudget
	}
	s.tempDir = filepath.Join(cfg.CacheDir, "tmp")
	s.tempBudget = cfg.TempBudgetBytes
	if s.tempBudget <= 0 {
		s.tempBudget = defaultTempBudget
	}
	s.wholeSlot = make(chan struct{}, 1)
	directoryBytes := cfg.DirectoryCacheBytes
	if directoryBytes <= 0 {
		directoryBytes = defaultDirectoryCache
	}
	s.directories = newDirectoryCache(directoryBytes)
	if err := s.prepareTempDir(); err != nil {
		return nil, err
	}
	if cfg.BookCacheBytes > 0 {
		books, err := newBookCache(filepath.Join(cfg.CacheDir, "books"), cfg.BookCacheBytes)
		if err != nil {
			return nil, err
		}
		s.books = books
	}
	return s, nil
}

// readDeadline is the context key of the moment a started read runs out.
type readDeadline struct{}

// StartRead starts the clock on reading one book for one request. What comes
// back carries the moment the budget runs out, and every call into the store
// made with it shares that one budget, however many of them a request needs: a
// check, a directory and the book itself are one wait for the person asking.
//
// It is a value and not a context deadline on purpose. A conversion runs on the
// same context after the read, and the store's budget is none of its business.
func (s *Store) StartRead(ctx context.Context) context.Context {
	if s.client == nil {
		return ctx
	}
	return context.WithValue(ctx, readDeadline{}, s.now().Add(s.readBudget))
}

// bound limits one operation to the budget of the read it belongs to, or to a
// budget of its own. Only a remote archive can feel it: a file on disk is read
// without looking at the context, as it always was.
func (s *Store) bound(ctx context.Context) (context.Context, context.CancelFunc) {
	if s.client == nil {
		return ctx, func() {}
	}
	deadline, started := ctx.Value(readDeadline{}).(time.Time)
	if !started {
		deadline = s.now().Add(s.readBudget)
	}
	return context.WithDeadline(ctx, deadline)
}

// Remote reports whether object storage is configured.
func (s *Store) Remote() bool { return s.client != nil }

// key maps a local path to its object key. An archive outside the root has
// none, and stays a matter for the disk.
func (s *Store) key(localPath string) (string, bool) {
	if s.client == nil {
		return "", false
	}
	rel, ok := s.relative(localPath)
	if !ok {
		return "", false
	}
	return s.prefix + rel, true
}

// relative is localPath under the root, slash-separated. Most callers build
// paths by concatenating the configured root and the catalog's path, which is
// checked first because it holds whatever the root looks like.
func (s *Store) relative(localPath string) (string, bool) {
	rel, found := strings.CutPrefix(localPath, s.root)
	if !found || s.root == "" {
		abs, err := filepath.Abs(localPath)
		if err != nil {
			return "", false
		}
		rel, err = filepath.Rel(s.rootAbs, abs)
		if err != nil {
			return "", false
		}
	}
	rel = path.Clean(filepath.ToSlash(strings.TrimLeft(rel, `/\`)))
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}

// onDisk reports whether the local file decides this request: it is there, or
// it failed in a way that is not its absence.
func onDisk(localPath string) bool {
	_, err := os.Stat(localPath)
	return err == nil || !errors.Is(err, fs.ErrNotExist)
}

// Check says whether an archive can be read: nil, ErrNotFound or
// ErrUnavailable. Without object storage it only asks the disk, and only about
// the file's absence: an unreadable file is left for the open to report.
func (s *Store) Check(ctx context.Context, localPath string) error {
	if onDisk(localPath) {
		return nil
	}
	key, ok := s.key(localPath)
	if !ok {
		return ErrNotFound
	}
	ctx, cancel := s.bound(ctx)
	defer cancel()
	_, err := s.directory(ctx, key)
	return mapError(err)
}

// Stat is an archive's size and date, wherever it is.
func (s *Store) Stat(ctx context.Context, localPath string) (int64, time.Time, error) {
	info, err := os.Stat(localPath)
	if err == nil {
		return info.Size(), info.ModTime(), nil
	}
	key, ok := s.key(localPath)
	if !ok || !errors.Is(err, fs.ErrNotExist) {
		return 0, time.Time{}, err
	}
	remote, err := s.client.Head(ctx, key)
	if err != nil {
		return 0, time.Time{}, mapError(err)
	}
	return remote.Size, remote.LastModified, nil
}

// List names the archives in object storage, as paths relative to the root.
// Nil without object storage.
func (s *Store) List(ctx context.Context) ([]string, error) {
	if s.client == nil {
		return nil, nil
	}
	objects, err := s.client.List(ctx, s.prefix)
	if err != nil {
		return nil, mapError(err)
	}
	names := make([]string, 0, len(objects))
	for _, o := range objects {
		if strings.HasSuffix(strings.ToLower(o.Key), ".zip") {
			names = append(names, strings.TrimPrefix(o.Key, s.prefix))
		}
	}
	return names, nil
}

// Archive is one opened archive. Close releases whatever it holds.
type Archive struct {
	*zip.Reader
	// ranged is the reader of a remote archive, nil for a local file.
	ranged *rangedReader
	close  func() error
	once   sync.Once
	err    error
}

// Close closes the archive once.
func (a *Archive) Close() error {
	a.once.Do(func() {
		if a.close != nil {
			a.err = a.close()
		}
	})
	return a.err
}

// OpenFile opens an entry. On a remote archive it sizes the read to the entry,
// so that the entry costs one request rather than a run of growing ones.
func (a *Archive) OpenFile(f *zip.File) (io.ReadCloser, error) {
	if a.ranged != nil {
		a.ranged.expect(entrySpan(f))
	}
	return f.Open()
}

func localArchive(localPath string) (*Archive, error) {
	r, err := zip.OpenReader(localPath)
	if err != nil {
		return nil, err
	}
	return &Archive{Reader: &r.Reader, close: r.Close}, nil
}

// OpenArchive opens an archive for a few reads. The disk answers first; a
// remote archive is read by ranges, its directory from the cache. It costs
// nothing to open twice, so a worker may call it per batch.
func (s *Store) OpenArchive(ctx context.Context, localPath string) (*Archive, error) {
	key, ok := s.key(localPath)
	if !ok || onDisk(localPath) {
		return localArchive(localPath)
	}
	dir, err := s.directory(ctx, key)
	if err != nil {
		return nil, mapError(err)
	}
	return s.remoteArchive(ctx, key, dir)
}

func (s *Store) remoteArchive(ctx context.Context, key string, dir *directory) (*Archive, error) {
	reader := newRangedReader(ctx, s.client, key, dir)
	zr, err := zip.NewReader(reader, dir.info.Size)
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return nil, err
	}
	return &Archive{Reader: zr, ranged: reader, close: reader.Close}, nil
}

// OpenBatch opens an archive to read entries of it. A worker that wants a
// handful of books reads them by ranges; one that wants many gets OpenWhole.
func (s *Store) OpenBatch(ctx context.Context, localPath string, entries int) (*Archive, error) {
	if entries < wholeFetchMinEntries {
		return s.OpenArchive(ctx, localPath)
	}
	return s.OpenWhole(ctx, localPath)
}

// Entry is one book found in its archive.
type Entry struct {
	store   *Store
	ctx     context.Context
	cancel  context.CancelFunc
	archive *Archive
	file    *zip.File
}

// Find opens the archive and finds an entry in it, without reading the entry.
// The Entry must be closed. A remote entry has the budget of one read, from
// here until it is closed.
func (s *Store) Find(ctx context.Context, localPath, name string) (*Entry, error) {
	ctx, cancel := s.bound(ctx)
	archive, err := s.OpenArchive(ctx, localPath)
	if err != nil {
		cancel()
		return nil, err
	}
	file := findFile(archive.Reader, name)
	if file == nil && archive.ranged != nil {
		// A cached directory may predate this book, and no ranged read will
		// ever catch that, because there is nothing to read.
		archive, file, err = s.findAfterRevalidating(ctx, archive, name)
	}
	if err != nil {
		_ = archive.Close()
		cancel()
		return nil, err
	}
	if file == nil {
		_ = archive.Close()
		cancel()
		return nil, ErrEntryNotFound
	}
	return &Entry{store: s, ctx: ctx, cancel: cancel, archive: archive, file: file}, nil
}

// findAfterRevalidating asks the store whether a directory is still current
// and looks again in the one it gets. No file and no error is a book the store
// itself has said is absent. A store that could not say is an error: the
// archive may have changed, and the directory known here cannot tell.
func (s *Store) findAfterRevalidating(ctx context.Context, archive *Archive, name string) (*Archive, *zip.File, error) {
	key := archive.ranged.key
	s.directories.expire(key)
	dir, err := s.directory(ctx, key)
	if err != nil {
		return archive, nil, mapError(err)
	}
	if dir.unverified != nil {
		return archive, nil, dir.unverified
	}
	if dir.info.ETag == archive.ranged.dir.info.ETag {
		return archive, nil, nil
	}
	fresh, err := s.remoteArchive(ctx, key, dir)
	if err != nil {
		return archive, nil, err
	}
	_ = archive.Close()
	return fresh, findFile(fresh.Reader, name), nil
}

func findFile(r *zip.Reader, name string) *zip.File {
	for _, f := range r.File {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// Name is the entry's name.
func (e *Entry) Name() string { return e.file.Name }

// Size is the uncompressed size the archive declares. The number is the
// archive's word, not a measurement: use it to refuse, never to admit.
func (e *Entry) Size() uint64 { return e.file.UncompressedSize64 }

// Close releases the archive and what is left of the budget.
func (e *Entry) Close() error {
	defer e.cancel()
	return e.archive.Close()
}

// Open reads the entry: from the disk, from the book cache, or from object
// storage. The stream is valid until the Entry is closed.
func (e *Entry) Open() (io.ReadCloser, error) {
	if e.archive.ranged == nil {
		return e.file.Open()
	}
	if cached := e.store.books.open(e.cacheKey()); cached != nil {
		return cached, nil
	}
	rc, err := e.archive.OpenFile(e.file)
	if errors.Is(err, objectstore.ErrChanged) {
		// Reading the entry is the first time this version is checked against
		// the store. A replaced archive is a cache miss, not a failure.
		if reopenErr := e.reopen(); reopenErr != nil {
			return nil, reopenErr
		}
		if cached := e.store.books.open(e.cacheKey()); cached != nil {
			return cached, nil
		}
		rc, err = e.archive.OpenFile(e.file)
	}
	if err != nil {
		return nil, mapError(err)
	}
	return e.store.books.tee(e.cacheKey(), e.file.UncompressedSize64, rc), nil
}

// reopen drops what is known about the archive and finds the entry again.
func (e *Entry) reopen() error {
	key := e.archive.ranged.key
	e.store.directories.remove(key)
	_ = e.archive.Close()
	dir, err := e.store.directory(e.ctx, key)
	if err != nil {
		return mapError(err)
	}
	archive, err := e.store.remoteArchive(e.ctx, key, dir)
	if err != nil {
		return err
	}
	e.archive = archive
	if e.file = findFile(archive.Reader, e.file.Name); e.file == nil {
		return ErrEntryNotFound
	}
	return nil
}

func (e *Entry) cacheKey() string {
	ranged := e.archive.ranged
	return bookCacheKey(ranged.key, ranged.dir.info.ETag, e.file.Name)
}

// mapError turns a storage error into this package's vocabulary.
func mapError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, objectstore.ErrNotFound):
		return ErrNotFound
	default:
		return err
	}
}

// removeFile deletes a file of this package's own, saying so when it cannot.
func removeFile(name string) {
	if err := os.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		logging.Warnf("archivestore: cannot remove %s: %v", name, err)
	}
}
