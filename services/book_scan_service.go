package services

import (
	"archive/zip"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopds-api/database"
	"gopds-api/internal/parser"
	"gopds-api/internal/posters"
	"gopds-api/logging"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// BookScanService handles scanning new books from archives
type BookScanService struct {
	archivesDir      string
	coversDir        string
	languageDetector *LanguageDetector
	skipDuplicates   bool
}

// ScanReport contains results of a scan operation
type ScanReport struct {
	TotalArchives  int             `json:"total_archives"`
	ProcessedBooks int             `json:"processed_books"`
	SkippedBooks   int             `json:"skipped_books"`
	Errors         []ScanError     `json:"errors"`
	Duration       time.Duration   `json:"duration"`
	ArchiveReports []ArchiveReport `json:"archive_reports"`
}

// ArchiveReport contains results for a single archive
type ArchiveReport struct {
	ArchiveName    string        `json:"archive_name"`
	BooksProcessed int           `json:"books_processed"`
	BooksSkipped   int           `json:"books_skipped"`
	Errors         []ScanError   `json:"errors"`
	Duration       time.Duration `json:"duration"`
}

// ScanError represents an error during scanning
type ScanError struct {
	FileName    string    `json:"file_name"`
	ArchiveName string    `json:"archive_name"`
	Error       string    `json:"error"`
	Timestamp   time.Time `json:"timestamp"`
}

// NewBookScanService creates a new BookScanService
func NewBookScanService(archivesDir, coversDir string, languageDetector *LanguageDetector, skipDuplicates bool) *BookScanService {
	return &BookScanService{
		archivesDir:      archivesDir,
		coversDir:        coversDir,
		languageDetector: languageDetector,
		skipDuplicates:   skipDuplicates,
	}
}

// GetUnscannedArchives returns list of unscanned archive file paths
func (s *BookScanService) GetUnscannedArchives() ([]string, error) {
	// 1. Get list of all ZIP files in archives directory
	var archiveFiles []string
	err := filepath.Walk(s.archivesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(strings.ToLower(info.Name()), ".zip") {
			archiveFiles = append(archiveFiles, path)
		}
		return nil
	})
	if err != nil {
		logging.Errorf("Failed to walk archives directory: %v", err)
		return nil, err
	}

	logging.Infof("Found %d ZIP archives in %s", len(archiveFiles), s.archivesDir)

	// 2. Get unscanned catalogs from database
	unscannedCatalogs, err := database.GetUnscannedCatalogs()
	if err != nil {
		return nil, err
	}

	// Create a map for faster lookup
	unscannedMap := make(map[string]bool)
	for _, catName := range unscannedCatalogs {
		unscannedMap[catName] = true
	}

	// 3. Filter archive files by unscanned status
	var unscannedArchives []string
	for _, archivePath := range archiveFiles {
		// Get relative path from archives directory
		relPath, err := filepath.Rel(s.archivesDir, archivePath)
		if err != nil {
			logging.Warnf("Failed to get relative path for %s: %v", archivePath, err)
			continue
		}

		// Check if this archive is in unscanned list
		if unscannedMap[relPath] {
			unscannedArchives = append(unscannedArchives, archivePath)
		}
	}

	logging.Infof("Found %d unscanned archives", len(unscannedArchives))
	return unscannedArchives, nil
}

// ScanArchive scans a single archive and processes all FB2 files in it
func (s *BookScanService) ScanArchive(archivePath string) (*ArchiveReport, error) {
	startTime := time.Now()

	// Get archive name (relative path from archives directory)
	archiveName, err := filepath.Rel(s.archivesDir, archivePath)
	if err != nil {
		archiveName = filepath.Base(archivePath)
	}

	logging.Infof("Starting scan of archive: %s", archiveName)

	report := &ArchiveReport{
		ArchiveName: archiveName,
		Errors:      []ScanError{},
	}

	// Ensure catalog entry exists
	_, err = database.GetOrCreateCatalog(archiveName)
	if err != nil {
		logging.Errorf("Failed to get/create catalog for %s: %v", archiveName, err)
		return nil, err
	}

	// Open ZIP archive
	zipReader, err := zip.OpenReader(archivePath)
	if err != nil {
		logging.Errorf("Failed to open archive %s: %v", archiveName, err)
		report.Errors = append(report.Errors, ScanError{
			FileName:    archiveName,
			ArchiveName: archiveName,
			Error:       fmt.Sprintf("failed to open archive: %v", err),
			Timestamp:   time.Now(),
		})
		return report, err
	}
	defer func() {
		if closeErr := zipReader.Close(); closeErr != nil {
			logging.Warnf("Failed to close archive reader: %v", closeErr)
		}
	}()

	// Process each FB2 file in the archive
	for _, file := range zipReader.File {
		if file.FileInfo().IsDir() {
			continue
		}

		fileName := file.Name
		if !strings.HasSuffix(strings.ToLower(fileName), ".fb2") {
			continue
		}

		// Process the book
		bookID, err := s.ProcessBook(file, archiveName)
		if err != nil {
			if err.Error() == "duplicate" {
				report.BooksSkipped++
				logging.Debugf("Skipped duplicate book: %s in %s", fileName, archiveName)
			} else {
				report.Errors = append(report.Errors, ScanError{
					FileName:    fileName,
					ArchiveName: archiveName,
					Error:       err.Error(),
					Timestamp:   time.Now(),
				})
				logging.Warnf("Failed to process book %s in %s: %v", fileName, archiveName, err)
			}
			continue
		}

		report.BooksProcessed++
		logging.Debugf("Successfully processed book ID %d: %s", bookID, fileName)

		// Update progress in database periodically
		if report.BooksProcessed%10 == 0 {
			_ = database.UpdateScanProgress(archiveName, report.BooksProcessed, len(report.Errors))
		}
	}

	// Mark archive as scanned
	err = database.MarkArchiveAsScanned(archiveName, report.BooksProcessed, len(report.Errors))
	if err != nil {
		logging.Errorf("Failed to mark archive %s as scanned: %v", archiveName, err)
	}

	report.Duration = time.Since(startTime)
	logging.Infof("Completed scan of %s: %d books processed, %d skipped, %d errors in %v",
		archiveName, report.BooksProcessed, report.BooksSkipped, len(report.Errors), report.Duration)

	return report, nil
}

// ProcessBook processes a single FB2 file from an archive
func (s *BookScanService) ProcessBook(zipFile *zip.File, archiveName string) (int64, error) {
	fileName := zipFile.Name

	// 1. Extract and read FB2 content
	fileReader, err := zipFile.Open()
	if err != nil {
		return 0, fmt.Errorf("failed to open file in archive: %w", err)
	}
	defer func() {
		if closeErr := fileReader.Close(); closeErr != nil {
			logging.Warnf("Failed to close file reader: %v", closeErr)
		}
	}()

	fb2Content, err := io.ReadAll(fileReader)
	if err != nil {
		return 0, fmt.Errorf("failed to read file content: %w", err)
	}

	// 2. Parse FB2 file
	fb2Parser := parser.NewFB2Parser(true) // readCover=true
	parsedBook, err := fb2Parser.Parse(bytes.NewReader(fb2Content))
	if err != nil {
		return 0, fmt.Errorf("failed to parse FB2: %w", err)
	}

	// 3. Detect language
	detectedLang := parsedBook.Language
	if s.languageDetector != nil {
		langResult := s.languageDetector.DetectLanguage(parsedBook.TextSample, parsedBook.Language)
		detectedLang = langResult.Language
		logging.Debugf("Language detected for %s: %s (confidence: %.2f, method: %s)",
			fileName, detectedLang, langResult.Confidence, langResult.Method)
	}

	// 4. Check for duplicates if enabled
	if s.skipDuplicates {
		isDuplicate, err := s.checkDuplicate(fb2Content, parsedBook.Title, parsedBook.Authors)
		if err != nil {
			logging.Warnf("Duplicate check failed for %s, proceeding anyway: %v", fileName, err)
		} else if isDuplicate {
			return 0, fmt.Errorf("duplicate")
		}
	}

	// 5. Create book record in database
	book := &models.Book{
		Path:         archiveName,
		Format:       "fb2",
		FileName:     fileName,
		RegisterDate: time.Now(),
		DocDate:      parsedBook.DocDate,
		Lang:         detectedLang,
		Title:        parsedBook.Title,
		Cover:        len(parsedBook.Cover) > 0,
		Annotation:   parsedBook.Annotation,
		Approved:     true, // Auto-approve scanned books
	}

	// Compute MD5 hash for duplicate detection
	hash := md5.Sum(fb2Content)
	book.MD5 = hex.EncodeToString(hash[:])

	// Start transaction
	tx, err := database.GetDB().Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && rollbackErr != pg.ErrTxDone {
			logging.Warnf("Failed to rollback transaction: %v", rollbackErr)
		}
	}()

	// Insert book
	_, err = tx.Model(book).Insert()
	if err != nil {
		return 0, fmt.Errorf("failed to insert book: %w", err)
	}

	// 6. Process cover if present
	if len(parsedBook.Cover) > 0 {
		err = s.ProcessCover(book, parsedBook.Cover)
		if err != nil {
			logging.Warnf("Failed to save cover for %s: %v", fileName, err)
			// Don't fail the entire operation if cover save fails
		}
	}

	// 7. Process authors
	err = s.ProcessAuthors(tx, book.ID, parsedBook.Authors)
	if err != nil {
		return 0, fmt.Errorf("failed to process authors: %w", err)
	}

	// 8. Process series if present
	if parsedBook.Series != nil {
		err = s.ProcessSeries(tx, book.ID, parsedBook.Series)
		if err != nil {
			return 0, fmt.Errorf("failed to process series: %w", err)
		}
	}

	// Commit transaction
	err = tx.Commit()
	if err != nil {
		return 0, fmt.Errorf("failed to commit transaction: %w", err)
	}

	logging.Infof("Successfully added book ID %d: %s", book.ID, parsedBook.Title)
	return book.ID, nil
}

// checkDuplicate checks if a book is a duplicate based on MD5 hash and fuzzy title/author matching
// Note: title and authors parameters are reserved for future fuzzy matching implementation
func (s *BookScanService) checkDuplicate(content []byte, title string, authors []parser.Author) (bool, error) {
	// TODO: Implement fuzzy matching using title and authors parameters
	_ = title   // Reserved for future use
	_ = authors // Reserved for future use

	// Check by MD5 hash first (exact duplicate)
	hash := md5.Sum(content)
	md5Hash := hex.EncodeToString(hash[:])

	var count int
	count, err := database.GetDB().Model((*models.Book)(nil)).
		Where("md5 = ?", md5Hash).
		Count()
	if err != nil {
		return false, err
	}
	if count > 0 {
		logging.Debugf("Found exact duplicate by MD5: %s", md5Hash)
		return true, nil
	}

	// TODO: Implement fuzzy matching by title + authors
	// For now, only check exact MD5 matches

	return false, nil
}

// ProcessCover saves the cover image to disk
func (s *BookScanService) ProcessCover(book *models.Book, coverData []byte) error {
	// Compute cover file path using the same logic as Python
	coverPath := posters.FilePath(s.coversDir, book.Path, book.FileName)

	// Create parent directories if needed
	err := os.MkdirAll(filepath.Dir(coverPath), 0755)
	if err != nil {
		return fmt.Errorf("failed to create cover directory: %w", err)
	}

	// Write cover image to file
	err = os.WriteFile(coverPath, coverData, 0644)
	if err != nil {
		return fmt.Errorf("failed to write cover file: %w", err)
	}

	logging.Debugf("Saved cover to: %s", coverPath)
	return nil
}

// ProcessAuthors creates or links authors to the book
func (s *BookScanService) ProcessAuthors(tx *pg.Tx, bookID int64, authors []parser.Author) error {
	for _, parsedAuthor := range authors {
		// Get or create author
		author := &models.Author{}
		err := tx.Model(author).
			Where("full_name = ?", parsedAuthor.Name).
			Select()

		if err != nil {
			if err == pg.ErrNoRows {
				// Create new author
				author.FullName = parsedAuthor.Name
				_, err = tx.Model(author).Insert()
				if err != nil {
					return fmt.Errorf("failed to create author %s: %w", parsedAuthor.Name, err)
				}
				logging.Debugf("Created new author: %s (ID: %d)", author.FullName, author.ID)
			} else {
				return fmt.Errorf("failed to query author: %w", err)
			}
		}

		// Link author to book via junction table
		orderToAuthor := &models.OrderToAuthor{
			BookID:   bookID,
			AuthorID: author.ID,
		}
		_, err = tx.Model(orderToAuthor).Insert()
		if err != nil {
			return fmt.Errorf("failed to link author to book: %w", err)
		}
		logging.Debugf("Linked author %s to book %d", author.FullName, bookID)
	}

	return nil
}

// ProcessSeries creates or links series to the book
func (s *BookScanService) ProcessSeries(tx *pg.Tx, bookID int64, series *parser.Series) error {
	// Get or create series
	seriesModel := &models.Series{}
	err := tx.Model(seriesModel).
		Where("ser = ?", series.Title).
		Select()

	if err != nil {
		if err == pg.ErrNoRows {
			// Create new series
			seriesModel.Ser = series.Title
			_, err = tx.Model(seriesModel).Insert()
			if err != nil {
				return fmt.Errorf("failed to create series %s: %w", series.Title, err)
			}
			logging.Debugf("Created new series: %s (ID: %d)", seriesModel.Ser, seriesModel.ID)
		} else {
			return fmt.Errorf("failed to query series: %w", err)
		}
	}

	// Parse series number
	var serNo int64 = 0
	if series.Index != "" {
		_, err := fmt.Sscanf(series.Index, "%d", &serNo)
		if err != nil {
			logging.Debugf("Failed to parse series index '%s', using 0: %v", series.Index, err)
			serNo = 0
		}
	}

	// Link series to book via junction table
	orderToSeries := &models.OrderToSeries{
		BookID:   bookID,
		SeriesID: seriesModel.ID,
		SerNo:    serNo,
	}
	_, err = tx.Model(orderToSeries).Insert()
	if err != nil {
		return fmt.Errorf("failed to link series to book: %w", err)
	}
	logging.Debugf("Linked series %s (number %d) to book %d", seriesModel.Ser, serNo, bookID)

	return nil
}

// ScanAll scans all unscanned archives
func (s *BookScanService) ScanAll() (*ScanReport, error) {
	startTime := time.Now()

	logging.Info("Starting full archive scan")

	report := &ScanReport{
		Errors:         []ScanError{},
		ArchiveReports: []ArchiveReport{},
	}

	// Get unscanned archives
	archives, err := s.GetUnscannedArchives()
	if err != nil {
		return nil, fmt.Errorf("failed to get unscanned archives: %w", err)
	}

	report.TotalArchives = len(archives)
	if len(archives) == 0 {
		logging.Info("No unscanned archives found")
		report.Duration = time.Since(startTime)
		return report, nil
	}

	// Process each archive
	for _, archivePath := range archives {
		archiveReport, err := s.ScanArchive(archivePath)
		if err != nil {
			// Log error but continue with other archives
			logging.Errorf("Failed to scan archive %s: %v", archivePath, err)
			if archiveReport != nil {
				report.ArchiveReports = append(report.ArchiveReports, *archiveReport)
			}
			continue
		}

		report.ArchiveReports = append(report.ArchiveReports, *archiveReport)
		report.ProcessedBooks += archiveReport.BooksProcessed
		report.SkippedBooks += archiveReport.BooksSkipped
		report.Errors = append(report.Errors, archiveReport.Errors...)
	}

	report.Duration = time.Since(startTime)

	logging.Infof("Completed full scan: %d archives, %d books processed, %d skipped, %d errors in %v",
		report.TotalArchives, report.ProcessedBooks, report.SkippedBooks, len(report.Errors), report.Duration)

	return report, nil
}
