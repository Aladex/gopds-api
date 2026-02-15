package services

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopds-api/database"
	"gopds-api/internal/parser"
	"gopds-api/internal/posters"
	"gopds-api/llm"
	"gopds-api/logging"
	"gopds-api/models"
)

// FixScanService handles bulk metadata refresh for existing FB2 books
type FixScanService struct {
	archivesDir      string
	coversDir        string
	languageDetector *LanguageDetector
	llmService       *llm.LLMService
	publisher        *ScanEventPublisher

	// Atomic counters for progress reporting from the handler's ticker
	ProgressCount  int64
	UpdatedCount   int64
	ErrorCount     int64
	TotalBooks     int64
	TotalArchives  int64
	CurrentArchive atomic.Value // stores string
}

// FixScanReport contains results of a fix scan operation
type FixScanReport struct {
	TotalBooks    int            `json:"total_books"`
	UpdatedBooks  int            `json:"updated_books"`
	TotalArchives int            `json:"total_archives"`
	ErrorCount    int            `json:"error_count"`
	Errors        []FixScanError `json:"errors,omitempty"`
	Duration      time.Duration  `json:"duration"`
}

// FixScanError represents a single error during fix scan
type FixScanError struct {
	BookID      int64  `json:"book_id"`
	FileName    string `json:"file_name"`
	ArchivePath string `json:"archive_path"`
	Error       string `json:"error"`
}

// archiveGroup groups books by their archive path
type archiveGroup struct {
	archivePath string // absolute path: filepath.Join(archivesDir, relPath)
	relPath     string // book.Path
	books       []models.Book
}

// NewFixScanService creates a new FixScanService
func NewFixScanService(archivesDir, coversDir string, ld *LanguageDetector, llmSvc *llm.LLMService) *FixScanService {
	s := &FixScanService{
		archivesDir:      archivesDir,
		coversDir:        coversDir,
		languageDetector: ld,
		llmService:       llmSvc,
	}
	s.CurrentArchive.Store("")
	return s
}

// SetScanEventPublisher sets the publisher for WebSocket events
func (s *FixScanService) SetScanEventPublisher(pub *ScanEventPublisher) {
	s.publisher = pub
}

// RunFixScan re-parses every FB2 book in the database and updates metadata
func (s *FixScanService) RunFixScan(ctx context.Context, workers int) (*FixScanReport, error) {
	startTime := time.Now()

	// 1. Query all FB2 books
	db := database.GetDB()
	var books []models.Book
	err := db.Model(&books).
		Column("id", "path", "filename", "title", "cover", "format").
		Where("format = ?", "fb2").
		Order("path ASC", "id ASC").
		Select()
	if err != nil {
		return nil, fmt.Errorf("failed to query books: %w", err)
	}

	if len(books) == 0 {
		return &FixScanReport{Duration: time.Since(startTime)}, nil
	}

	// 2. Group by Path (archive)
	groups := s.groupByArchive(books)

	// Set totals for external progress reading
	atomic.StoreInt64(&s.TotalBooks, int64(len(books)))
	atomic.StoreInt64(&s.TotalArchives, int64(len(groups)))

	// Publish start event
	s.publisher.PublishFixScanStarted(len(books), len(groups))

	// 3. Worker pool
	var (
		errMu     sync.Mutex
		scanErrs  []FixScanError
		maxErrors = 500
	)

	jobs := make(chan archiveGroup, workers*2)
	var wg sync.WaitGroup

	addError := func(e FixScanError) {
		atomic.AddInt64(&s.ErrorCount, 1)
		errMu.Lock()
		if len(scanErrs) < maxErrors {
			scanErrs = append(scanErrs, e)
		}
		errMu.Unlock()
	}

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
				s.CurrentArchive.Store(group.relPath)
				s.processArchiveGroup(ctx, group, addError)
			}
		}
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go worker()
	}

	// Feed jobs
	for _, group := range groups {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return s.buildReport(books, groups, scanErrs, startTime), ctx.Err()
		case jobs <- group:
		}
	}
	close(jobs)
	wg.Wait()

	report := s.buildReport(books, groups, scanErrs, startTime)
	s.publisher.PublishFixScanCompleted(report)
	return report, nil
}

func (s *FixScanService) buildReport(books []models.Book, groups []archiveGroup, errs []FixScanError, startTime time.Time) *FixScanReport {
	return &FixScanReport{
		TotalBooks:    len(books),
		UpdatedBooks:  int(atomic.LoadInt64(&s.UpdatedCount)),
		TotalArchives: len(groups),
		ErrorCount:    int(atomic.LoadInt64(&s.ErrorCount)),
		Errors:        errs,
		Duration:      time.Since(startTime),
	}
}

func (s *FixScanService) groupByArchive(books []models.Book) []archiveGroup {
	groupMap := make(map[string]*archiveGroup)
	var order []string

	for _, book := range books {
		if g, ok := groupMap[book.Path]; ok {
			g.books = append(g.books, book)
		} else {
			archivePath := filepath.Join(s.archivesDir, book.Path)
			if !strings.HasSuffix(book.Path, ".zip") {
				archivePath = filepath.Join(s.archivesDir, book.Path+".zip")
			}
			groupMap[book.Path] = &archiveGroup{
				archivePath: archivePath,
				relPath:     book.Path,
				books:       []models.Book{book},
			}
			order = append(order, book.Path)
		}
	}

	result := make([]archiveGroup, 0, len(order))
	for _, key := range order {
		result = append(result, *groupMap[key])
	}
	return result
}

func (s *FixScanService) processArchiveGroup(ctx context.Context, group archiveGroup, addError func(FixScanError)) {
	// Open ZIP once for all books in this archive
	zr, err := zip.OpenReader(group.archivePath)
	if err != nil {
		// Log error for all books in this group
		for _, book := range group.books {
			addError(FixScanError{
				BookID:      book.ID,
				FileName:    book.FileName,
				ArchivePath: group.relPath,
				Error:       fmt.Sprintf("failed to open archive: %v", err),
			})
			atomic.AddInt64(&s.ProgressCount, 1)
		}
		return
	}
	defer func() {
		if closeErr := zr.Close(); closeErr != nil {
			logging.Warnf("Failed to close archive %s: %v", group.archivePath, closeErr)
		}
	}()

	// Build index: filename -> *zip.File
	fileIndex := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		fileIndex[f.Name] = f
	}

	for _, book := range group.books {
		select {
		case <-ctx.Done():
			return
		default:
		}

		s.processBook(book, group.relPath, fileIndex, addError)
		atomic.AddInt64(&s.ProgressCount, 1)
	}
}

func (s *FixScanService) processBook(book models.Book, archivePath string, fileIndex map[string]*zip.File, addError func(FixScanError)) {
	zipFile, ok := fileIndex[book.FileName]
	if !ok {
		addError(FixScanError{
			BookID:      book.ID,
			FileName:    book.FileName,
			ArchivePath: archivePath,
			Error:       "file not found in archive",
		})
		return
	}

	// Read FB2 content
	rc, err := zipFile.Open()
	if err != nil {
		addError(FixScanError{
			BookID:      book.ID,
			FileName:    book.FileName,
			ArchivePath: archivePath,
			Error:       fmt.Sprintf("failed to open file in archive: %v", err),
		})
		return
	}

	content, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		addError(FixScanError{
			BookID:      book.ID,
			FileName:    book.FileName,
			ArchivePath: archivePath,
			Error:       fmt.Sprintf("failed to read file: %v", err),
		})
		return
	}

	// Parse FB2
	fb2Parser := parser.NewFB2Parser(true)
	parsed, err := fb2Parser.Parse(bytes.NewReader(content))
	if err != nil {
		addError(FixScanError{
			BookID:      book.ID,
			FileName:    book.FileName,
			ArchivePath: archivePath,
			Error:       fmt.Sprintf("failed to parse FB2: %v", err),
		})
		return
	}

	// Cover logic:
	// cover=true + no cover in FB2 -> keep cover=true (never downgrade)
	// cover=false + cover in FB2 -> extract, save, set cover=true
	// cover=true + cover in FB2 -> re-extract and overwrite
	// cover=false + no cover in FB2 -> no change
	hasCoverInFB2 := parsed.Cover != nil && len(parsed.Cover) > 0
	newCoverFlag := book.Cover // start with current value

	if hasCoverInFB2 {
		// Save/overwrite cover file
		if err := s.saveCover(book, parsed.Cover); err != nil {
			logging.Warnf("Failed to save cover for book %d: %v", book.ID, err)
			// Don't fail the whole book for a cover error
		} else {
			newCoverFlag = true
		}
	}
	// If book.Cover is true and no cover in FB2, keep cover=true (never downgrade)

	// Detect language
	detectedLang := parsed.Language
	if s.languageDetector != nil {
		result := s.languageDetector.DetectLanguage(parsed.Language, parsed.BodySample)
		detectedLang = result.Language
	}

	// Convert authors
	authors := make([]models.RescanAuthor, len(parsed.Authors))
	for i, a := range parsed.Authors {
		authors[i] = models.RescanAuthor{Name: a.Name}
	}

	// Convert series
	var series *models.RescanSeries
	if parsed.Series != nil {
		series = &models.RescanSeries{
			Title: parsed.Series.Title,
			Index: parsed.Series.Index,
		}
	}

	// Update DB in a transaction
	db := database.GetDB()
	tx, err := db.Begin()
	if err != nil {
		addError(FixScanError{
			BookID:      book.ID,
			FileName:    book.FileName,
			ArchivePath: archivePath,
			Error:       fmt.Sprintf("failed to begin transaction: %v", err),
		})
		return
	}

	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Update book metadata
	_, err = tx.Model(&models.Book{}).
		Set("title = ?", parsed.Title).
		Set("annotation = ?", parsed.Annotation).
		Set("lang = ?", detectedLang).
		Set("docdate = ?", parsed.DocDate).
		Set("cover = ?", newCoverFlag).
		Set("registerdate = registerdate"). // preserve registerdate
		Where("id = ?", book.ID).
		Update()
	if err != nil {
		addError(FixScanError{
			BookID:      book.ID,
			FileName:    book.FileName,
			ArchivePath: archivePath,
			Error:       fmt.Sprintf("failed to update book: %v", err),
		})
		return
	}

	// Update authors
	err = database.UpdateBookAuthors(tx, book.ID, authors)
	if err != nil {
		addError(FixScanError{
			BookID:      book.ID,
			FileName:    book.FileName,
			ArchivePath: archivePath,
			Error:       fmt.Sprintf("failed to update authors: %v", err),
		})
		return
	}

	// Update series
	err = database.UpdateBookSeries(tx, book.ID, series)
	if err != nil {
		addError(FixScanError{
			BookID:      book.ID,
			FileName:    book.FileName,
			ArchivePath: archivePath,
			Error:       fmt.Sprintf("failed to update series: %v", err),
		})
		return
	}

	// Update genres/tags
	err = database.UpdateBookTags(tx, book.ID, parsed.Tags, s.llmService)
	if err != nil {
		addError(FixScanError{
			BookID:      book.ID,
			FileName:    book.FileName,
			ArchivePath: archivePath,
			Error:       fmt.Sprintf("failed to update tags: %v", err),
		})
		return
	}

	err = tx.Commit()
	if err != nil {
		addError(FixScanError{
			BookID:      book.ID,
			FileName:    book.FileName,
			ArchivePath: archivePath,
			Error:       fmt.Sprintf("failed to commit: %v", err),
		})
		return
	}

	atomic.AddInt64(&s.UpdatedCount, 1)
}

func (s *FixScanService) saveCover(book models.Book, coverData []byte) error {
	coverPath := posters.FilePath(s.coversDir, book.Path, book.FileName)
	if err := os.MkdirAll(filepath.Dir(coverPath), 0755); err != nil {
		return fmt.Errorf("failed to create cover directory: %w", err)
	}
	return os.WriteFile(coverPath, coverData, 0644)
}
