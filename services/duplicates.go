package services

import (
	"archive/zip"
	"context"
	// #nosec G501 -- MD5 is used below as a content fingerprint for finding
	// duplicate books, never to protect anything. Collision resistance is
	// not a property this depends on.
	"crypto/md5"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"gopds-api/internal/archivestore"
	"gopds-api/internal/safeio"
	"gopds-api/logging"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// WebSocketConnection interface for WebSocket communication
type WebSocketConnection interface {
	SendMessage(messageType string, data interface{}) error
}

// DuplicateScanProgressType is the WebSocket event type of duplicate scan
// progress frames; the manager routes them to the duplicates topic.
const DuplicateScanProgressType = "duplicate_scan_progress"

// DuplicateScanProgress represents the progress of a duplicate scan
type DuplicateScanProgress struct {
	JobID           int64  `json:"job_id"`
	Status          string `json:"status"`
	ProcessedBooks  int    `json:"processed_books"`
	TotalBooks      int    `json:"total_books"`
	DuplicatesFound int    `json:"duplicates_found"`
	Error           string `json:"error,omitempty"`
}

// DuplicateGroup represents a group of duplicate books
type DuplicateGroup struct {
	MD5Hash       string   `json:"md5_hash"`
	Count         int      `json:"count"`
	BookIDs       []int64  `json:"book_ids"`
	ExampleTitles []string `json:"example_titles"`
}

// HideResult represents the result of hiding duplicates
type HideResult struct {
	HiddenCount  int `json:"hidden_count"`
	SkippedEmpty int `json:"skipped_empty"`
}

const (
	batchSize      = 50        // Send WebSocket updates every N books
	jobsPerWorker  = 2         // Archives queued ahead of each worker
	hashBufferSize = 64 * 1024 // 64KB buffer for hashing
)

var (
	// Mutex to ensure only one scan runs at a time
	scanMutex sync.Mutex
	cancelMu  sync.Mutex
	cancelMap = make(map[int64]context.CancelFunc)
)

// ScanDuplicates scans all books and computes MD5 hashes for duplicate detection
func ScanDuplicates(ctx context.Context, db *pg.DB, jobID int64, wsConn WebSocketConnection, filesPath string, workers int) error {
	logging.Infof("Starting duplicate scan job %d", jobID)

	// Update job status to running
	now := time.Now()
	_, err := db.Model(&models.AdminScanJob{}).
		Set("status = ?", "running").
		Set("started_at = ?", now).
		Set("updated_at = ?", now).
		Where("id = ?", jobID).
		Update()
	if err != nil {
		logging.Errorf("Failed to update job status to running: %v", err)
		return err
	}

	// Send initial WebSocket message
	if wsConn != nil {
		_ = wsConn.SendMessage(DuplicateScanProgressType, DuplicateScanProgress{
			JobID:           jobID,
			Status:          "running",
			ProcessedBooks:  0,
			TotalBooks:      0,
			DuplicatesFound: 0,
		})
	}

	// Get all books (both approved and unapproved)
	var books []models.Book
	err = db.Model(&books).
		Column("id", "path", "filename", "md5", "title").
		Order("id ASC").
		Select()
	if err != nil {
		logging.Errorf("Failed to fetch books: %v", err)
		return updateJobError(db, jobID, err.Error())
	}

	totalBooks := len(books)
	logging.Infof("Found %d books to scan", totalBooks)

	// Update total books count
	_, err = db.Model(&models.AdminScanJob{}).
		Set("total_books = ?", totalBooks).
		Set("updated_at = ?", time.Now()).
		Where("id = ?", jobID).
		Update()
	if err != nil {
		logging.Warnf("Failed to update total_books: %v", err)
	}

	var errorCount int64 = 0

	totalBooksInt64 := int64(totalBooks)
	progressCount := int64(0)
	jobs := make(chan archiveBooks, workers*jobsPerWorker)
	var wg sync.WaitGroup

	hashBook := func(archive *archivestore.Archive, book *models.Book, filePath string) {
		hash, hashErr := bookMD5(archive, book.FileName)
		if hashErr != nil {
			logging.Warnf("Failed to compute MD5 for book ID %d (%s/%s): %v", book.ID, filePath, book.FileName, hashErr)
			atomic.AddInt64(&errorCount, 1)
			return
		}
		_, updateErr := db.Model(&models.Book{}).
			Set("md5 = ?", hash).
			Set("registerdate = registerdate"). // Preserve registerdate
			Where("id = ?", book.ID).
			Update()
		if updateErr != nil {
			logging.Errorf("Failed to update MD5 for book ID %d: %v", book.ID, updateErr)
			atomic.AddInt64(&errorCount, 1)
		}
	}

	countBook := func() {
		current := atomic.AddInt64(&progressCount, 1)
		if current%batchSize != 0 && current != totalBooksInt64 {
			return
		}
		processed := int(current)
		if progressErr := updateJobProgress(db, jobID, processed, 0); progressErr != nil {
			logging.Warnf("Failed to update job progress: %v", progressErr)
		}
		if wsConn != nil {
			_ = wsConn.SendMessage(DuplicateScanProgressType, DuplicateScanProgress{
				JobID:           jobID,
				Status:          "running",
				ProcessedBooks:  processed,
				TotalBooks:      totalBooks,
				DuplicatesFound: 0,
			})
		}
	}

	// An archive is opened once for its books, so that one only in object
	// storage is fetched once. A failed open is that archive's books failing,
	// each counted and logged, and the job goes on.
	worker := func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case group, ok := <-jobs:
				if !ok {
					return
				}
				filePath := filepath.Join(filesPath, group.path)
				archive, openErr := group.open(ctx, filePath)
				for i := range group.books {
					book := &group.books[i]
					if ctx.Err() != nil {
						break
					}
					switch {
					case book.MD5 != "":
					case openErr != nil:
						logging.Warnf("Failed to compute MD5 for book ID %d (%s/%s): %v", book.ID, filePath, book.FileName, openErr)
						atomic.AddInt64(&errorCount, 1)
					default:
						hashBook(archive, book, filePath)
					}
					countBook()
				}
				if archive != nil {
					if closeErr := archive.Close(); closeErr != nil {
						logging.Warnf("Failed to close archive %s: %v", filePath, closeErr)
					}
				}
			}
		}
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go worker()
	}

	for _, group := range groupBooksByArchive(books) {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			logging.Warn("Scan cancelled by context")
			return updateJobError(db, jobID, "scan_cancelled")
		case jobs <- group:
		}
	}
	close(jobs)
	wg.Wait()
	processedBooks := int(atomic.LoadInt64(&progressCount))

	// Calculate duplicates count after scanning is complete
	duplicatesFound := 0
	groups, err := GetDuplicateGroups(ctx, db)
	if err == nil {
		for _, group := range groups {
			if group.Count > 1 {
				duplicatesFound += group.Count - 1 // All except one are duplicates
			}
		}
		logging.Infof("Calculated %d total duplicates across %d groups", duplicatesFound, len(groups))
	} else {
		logging.Warnf("Failed to calculate duplicates after scan: %v", err)
	}

	// Mark job as completed
	finishedAt := time.Now()
	_, err = db.Model(&models.AdminScanJob{}).
		Set("status = ?", "completed").
		Set("processed_books = ?", processedBooks).
		Set("duplicates_found = ?", duplicatesFound).
		Set("finished_at = ?", finishedAt).
		Set("updated_at = ?", finishedAt).
		Where("id = ?", jobID).
		Update()
	if err != nil {
		logging.Errorf("Failed to mark job as completed: %v", err)
	}

	// Send final WebSocket message
	if wsConn != nil {
		_ = wsConn.SendMessage(DuplicateScanProgressType, DuplicateScanProgress{
			JobID:           jobID,
			Status:          "completed",
			ProcessedBooks:  processedBooks,
			TotalBooks:      totalBooks,
			DuplicatesFound: duplicatesFound,
		})
	}

	logging.Infof("Duplicate scan completed. Processed: %d, Duplicates: %d, Errors: %d", processedBooks, duplicatesFound, atomic.LoadInt64(&errorCount))
	return nil
}

// archiveBooks is the books of one archive.
type archiveBooks struct {
	path  string
	books []models.Book
}

// groupBooksByArchive splits books by archive, keeping the order of first
// appearance.
func groupBooksByArchive(books []models.Book) []archiveBooks {
	index := make(map[string]int)
	var groups []archiveBooks
	for b := range books {
		path := books[b].Path
		i, seen := index[path]
		if !seen {
			i = len(groups)
			index[path] = i
			groups = append(groups, archiveBooks{path: path})
		}
		groups[i].books = append(groups[i].books, books[b])
	}
	return groups
}

// open opens the archive for the books that still need hashing; nil when none
// does, which spares a scan that has nothing to do here from touching it.
func (g archiveBooks) open(ctx context.Context, filePath string) (*archivestore.Archive, error) {
	missing := 0
	for i := range g.books {
		if g.books[i].MD5 == "" {
			missing++
		}
	}
	if missing == 0 {
		return nil, nil
	}
	return archivestore.Default().OpenBatch(ctx, filePath, missing)
}

// computeBookMD5 computes MD5 hash of a book file stored inside a zip archive.
func computeBookMD5(zipPath string, filename string) (string, error) {
	if filename == "" {
		return "", errors.New("missing filename")
	}
	archive, err := archivestore.Default().OpenArchive(context.Background(), zipPath)
	if err != nil {
		return "", err
	}
	defer func() {
		if closeErr := archive.Close(); closeErr != nil {
			logging.Warnf("Failed to close zip %s: %v", zipPath, closeErr)
		}
	}()
	return bookMD5(archive, filename)
}

// bookMD5 hashes one book of an open archive.
func bookMD5(archive *archivestore.Archive, filename string) (string, error) {
	if filename == "" {
		return "", errors.New("missing filename")
	}
	var target *zip.File
	for _, f := range archive.File {
		if f.Name == filename {
			target = f
			break
		}
	}
	if target == nil {
		return "", errors.New("book not found in archive")
	}

	rc, err := archive.OpenFile(target)
	if err != nil {
		return "", err
	}
	defer func() {
		if closeErr := rc.Close(); closeErr != nil {
			logging.Warnf("Failed to close book stream %s: %v", filename, closeErr)
		}
	}()

	// #nosec G401 -- a fingerprint for duplicate detection, see the import.
	hash := md5.New()
	buffer := make([]byte, hashBufferSize)

	// Bounded like every other read out of an archive: hashing a bomb costs
	// time rather than disk, but it costs it on the scan worker.
	if _, err := safeio.CopyBuffer(hash, rc, buffer, safeio.MaxBookBytes); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// updateJobProgress updates the progress of a scan job
func updateJobProgress(db *pg.DB, jobID int64, processedBooks, duplicatesFound int) error {
	_, err := db.Model(&models.AdminScanJob{}).
		Set("processed_books = ?", processedBooks).
		Set("duplicates_found = ?", duplicatesFound).
		Set("updated_at = ?", time.Now()).
		Where("id = ?", jobID).
		Update()
	return err
}

// updateJobError marks a job as failed with an error message
func updateJobError(db *pg.DB, jobID int64, errorMsg string) error {
	now := time.Now()
	_, err := db.Model(&models.AdminScanJob{}).
		Set("status = ?", "failed").
		Set("error = ?", errorMsg).
		Set("finished_at = ?", now).
		Set("updated_at = ?", now).
		Where("id = ?", jobID).
		Update()
	if err != nil {
		logging.Errorf("Failed to update job error: %v", err)
	}
	return fmt.Errorf("%s", errorMsg)
}

// GetDuplicateGroups retrieves all groups of duplicate books using a single optimized query
func GetDuplicateGroups(_ context.Context, db *pg.DB) ([]DuplicateGroup, error) {
	logging.Info("Fetching duplicate groups")

	// Single optimized query using PostgreSQL array aggregation
	type groupResult struct {
		MD5           string   `pg:"md5"`
		Count         int      `pg:"count"`
		BookIDs       []int64  `pg:"book_ids,array"`
		ExampleTitles []string `pg:"example_titles,array"`
	}

	var results []groupResult
	err := db.Model(&models.Book{}).
		Column("md5").
		ColumnExpr("COUNT(*) as count").
		ColumnExpr("ARRAY_AGG(id ORDER BY id) as book_ids").
		ColumnExpr("ARRAY_AGG(DISTINCT title ORDER BY title) as example_titles").
		Where("md5 IS NOT NULL AND md5 != ''").
		Group("md5").
		Having("COUNT(*) > 1").
		Order("count DESC").
		Select(&results)
	if err != nil {
		logging.Errorf("Failed to fetch duplicate groups: %v", err)
		return nil, err
	}

	// Convert results to DuplicateGroup objects
	groups := make([]DuplicateGroup, 0, len(results))
	for _, r := range results {
		exampleTitles := r.ExampleTitles
		if len(exampleTitles) > 3 {
			exampleTitles = exampleTitles[:3]
		}
		groups = append(groups, DuplicateGroup{
			MD5Hash:       r.MD5,
			Count:         r.Count,
			BookIDs:       r.BookIDs,
			ExampleTitles: exampleTitles,
		})
	}

	logging.Infof("Found %d duplicate groups", len(groups))
	return groups, nil
}

// HideDuplicates hides duplicate books based on the "Newest ID" rule
func HideDuplicates(_ context.Context, db *pg.DB) (*HideResult, error) {
	logging.Info("Starting to hide duplicates")

	// Get all duplicate groups
	type duplicateHash struct {
		MD5 string `pg:"md5"`
	}

	var hashes []duplicateHash
	err := db.Model(&models.Book{}).
		ColumnExpr("DISTINCT md5").
		Where("md5 IS NOT NULL AND md5 != ''").
		Group("md5").
		Having("COUNT(*) > 1").
		Select(&hashes)
	if err != nil {
		logging.Errorf("Failed to fetch duplicate hashes: %v", err)
		return nil, err
	}

	hiddenCount := 0
	skippedEmpty := 0

	// Process each duplicate group
	for _, h := range hashes {
		// Get all books with this hash, ordered by ID DESC
		var books []models.Book
		err := db.Model(&books).
			Column("id").
			Where("md5 = ?", h.MD5).
			Order("id DESC"). // Newest first
			Select()
		if err != nil {
			logging.Warnf("Failed to fetch books for hash %s: %v", h.MD5, err)
			continue
		}

		if len(books) <= 1 {
			skippedEmpty++
			continue
		}

		// First book (highest ID) is the winner - keep it visible
		winnerID := books[0].ID

		// Hide all other books
		for i := 1; i < len(books); i++ {
			_, err := db.Model(&models.Book{}).
				Set("duplicate_hidden = ?", true).
				Set("duplicate_of_id = ?", winnerID).
				Set("registerdate = registerdate"). // Preserve registerdate
				Where("id = ?", books[i].ID).
				Update()
			if err != nil {
				logging.Errorf("Failed to hide book ID %d: %v", books[i].ID, err)
				continue
			}
			hiddenCount++
		}
	}

	logging.Infof("Hidden %d duplicate books, skipped %d empty groups", hiddenCount, skippedEmpty)
	return &HideResult{
		HiddenCount:  hiddenCount,
		SkippedEmpty: skippedEmpty,
	}, nil
}

// EnsureOneScanRunning checks if a scan is already running or pending
func EnsureOneScanRunning(_ context.Context, db *pg.DB) error {
	scanMutex.Lock()
	defer scanMutex.Unlock()

	var activeJob models.AdminScanJob
	err := db.Model(&activeJob).
		WhereIn("status IN (?)", []string{"pending", "running"}).
		First()
	if err == nil {
		return fmt.Errorf("scan job %d is already %s", activeJob.ID, activeJob.Status)
	}

	// pg.ErrNoRows means no active job, which is what we want
	if errors.Is(err, pg.ErrNoRows) {
		return nil
	}

	return err
}

func RegisterScanCancel(jobID int64, cancel context.CancelFunc) {
	cancelMu.Lock()
	defer cancelMu.Unlock()
	cancelMap[jobID] = cancel
}

func UnregisterScanCancel(jobID int64) {
	cancelMu.Lock()
	defer cancelMu.Unlock()
	delete(cancelMap, jobID)
}

func CancelScan(jobID int64) bool {
	cancelMu.Lock()
	defer cancelMu.Unlock()
	cancel, ok := cancelMap[jobID]
	if !ok {
		return false
	}
	cancel()
	delete(cancelMap, jobID)
	return true
}
