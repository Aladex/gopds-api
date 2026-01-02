package services

import (
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"gopds-api/logging"
	"gopds-api/models"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-pg/pg/v10"
)

// WebSocketConnection interface for WebSocket communication
type WebSocketConnection interface {
	SendMessage(messageType string, data interface{}) error
}

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
	hashBufferSize = 64 * 1024 // 64KB buffer for hashing
)

var (
	// Mutex to ensure only one scan runs at a time
	scanMutex sync.Mutex
)

// ScanDuplicates scans all books and computes MD5 hashes for duplicate detection
func ScanDuplicates(ctx context.Context, db *pg.DB, jobID int64, wsConn WebSocketConnection, filesPath string) error {
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
		_ = wsConn.SendMessage("duplicate_scan_progress", DuplicateScanProgress{
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
		Column("id", "path", "md5", "title").
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

	processedBooks := 0
	errorCount := 0

	for i, book := range books {
		// Check context cancellation
		select {
		case <-ctx.Done():
			logging.Warn("Scan cancelled by context")
			return updateJobError(db, jobID, "scan_cancelled")
		default:
		}

		// Skip if MD5 already exists
		if book.MD5 != "" {
			processedBooks++
			continue
		}

		// Compute MD5 hash
		filePath := filepath.Join(filesPath, book.Path)
		hash, err := computeMD5(filePath)
		if err != nil {
			logging.Warnf("Failed to compute MD5 for book ID %d (%s): %v", book.ID, filePath, err)
			errorCount++
			// Continue processing even if file is missing
			processedBooks++
			continue
		}

		// Update book with MD5 hash
		_, err = db.Model(&models.Book{}).
			Set("md5 = ?", hash).
			Set("registerdate = registerdate"). // Preserve registerdate
			Where("id = ?", book.ID).
			Update()
		if err != nil {
			logging.Errorf("Failed to update MD5 for book ID %d: %v", book.ID, err)
			errorCount++
		}

		processedBooks++

		// Send progress update every batchSize books (without duplicatesFound during scan)
		if (i+1)%batchSize == 0 || i == totalBooks-1 {
			err = updateJobProgress(db, jobID, processedBooks, 0)
			if err != nil {
				logging.Warnf("Failed to update job progress: %v", err)
			}

			if wsConn != nil {
				_ = wsConn.SendMessage("duplicate_scan_progress", DuplicateScanProgress{
					JobID:           jobID,
					Status:          "running",
					ProcessedBooks:  processedBooks,
					TotalBooks:      totalBooks,
					DuplicatesFound: 0,
				})
			}
		}
	}

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
		_ = wsConn.SendMessage("duplicate_scan_progress", DuplicateScanProgress{
			JobID:           jobID,
			Status:          "completed",
			ProcessedBooks:  processedBooks,
			TotalBooks:      totalBooks,
			DuplicatesFound: duplicatesFound,
		})
	}

	logging.Infof("Duplicate scan completed. Processed: %d, Duplicates: %d, Errors: %d", processedBooks, duplicatesFound, errorCount)
	return nil
}

// computeMD5 computes MD5 hash of a file
func computeMD5(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			logging.Warnf("Failed to close file %s: %v", filePath, closeErr)
		}
	}()

	hash := md5.New()
	buffer := make([]byte, hashBufferSize)

	_, err = io.CopyBuffer(hash, file, buffer)
	if err != nil {
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

// GetDuplicateGroups retrieves all groups of duplicate books
func GetDuplicateGroups(_ context.Context, db *pg.DB) ([]DuplicateGroup, error) {
	logging.Info("Fetching duplicate groups")

	// Query to find duplicate groups
	type result struct {
		MD5Hash string
		Count   int
	}

	var results []result
	err := db.Model(&models.Book{}).
		Column("md5").
		ColumnExpr("COUNT(*) as count").
		Where("md5 IS NOT NULL AND md5 != ''").
		Group("md5").
		Having("COUNT(*) > 1").
		Order("count DESC").
		Select(&results)
	if err != nil {
		logging.Errorf("Failed to fetch duplicate groups: %v", err)
		return nil, err
	}

	groups := make([]DuplicateGroup, 0, len(results))

	// For each duplicate group, get book IDs and example titles
	for _, r := range results {
		var books []models.Book
		err := db.Model(&books).
			Column("id", "title").
			Where("md5 = ?", r.MD5Hash).
			Order("id ASC").
			Limit(5). // Get up to 5 example titles
			Select()
		if err != nil {
			logging.Warnf("Failed to fetch books for hash %s: %v", r.MD5Hash, err)
			continue
		}

		bookIDs := make([]int64, len(books))
		titles := make([]string, 0, len(books))
		for i, book := range books {
			bookIDs[i] = book.ID
			if len(titles) < 3 { // Only include 3 example titles
				titles = append(titles, book.Title)
			}
		}

		groups = append(groups, DuplicateGroup{
			MD5Hash:       r.MD5Hash,
			Count:         r.Count,
			BookIDs:       bookIDs,
			ExampleTitles: titles,
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
		MD5Hash string
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
			Where("md5 = ?", h.MD5Hash).
			Order("id DESC"). // Newest first
			Select()
		if err != nil {
			logging.Warnf("Failed to fetch books for hash %s: %v", h.MD5Hash, err)
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
