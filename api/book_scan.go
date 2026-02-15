package api

import (
	"archive/zip"
	"errors"
	"fmt"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/llm"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/services"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

type bookScanState struct {
	mu                sync.Mutex
	running           bool
	sessionID         string
	startedAt         time.Time
	finishedAt        *time.Time
	totalArchives     int
	archivesProcessed int
	currentArchive    string
	totalBooks        int
	totalErrors       int
	lastError         string
	errors            []ScanErrorResponse
}

var scanState bookScanState

type StartScanResponse struct {
	SessionID string    `json:"session_id"`
	StartedAt time.Time `json:"started_at"`
	Message   string    `json:"message"`
}

type ScanStatusResponse struct {
	IsRunning         bool       `json:"is_running"`
	SessionID         string     `json:"session_id,omitempty"`
	TotalArchives     int        `json:"total_archives"`
	ArchivesProcessed int        `json:"archives_processed"`
	CurrentArchive    string     `json:"current_archive,omitempty"`
	TotalBooks        int        `json:"total_books"`
	TotalErrors       int        `json:"total_errors"`
	ProgressPercent   int        `json:"progress_percent"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	ElapsedSeconds    int64      `json:"elapsed_seconds"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
	LastError         string     `json:"last_error,omitempty"`
}

type UnscannedArchiveInfo struct {
	Name        string    `json:"name"`
	SizeMB      float64   `json:"size_mb"`
	FileCount   int       `json:"file_count"`
	CreatedDate time.Time `json:"created_date"`
}

type UnscannedArchivesResponse struct {
	UnscannedArchives []UnscannedArchiveInfo `json:"unscanned_archives"`
	TotalCount        int                    `json:"total_count"`
}

type ScanErrorsResponse struct {
	Errors []ScanErrorResponse `json:"errors"`
}

type ScanErrorResponse struct {
	FileName    string    `json:"file_name"`
	ArchiveName string    `json:"archive_name"`
	Error       string    `json:"error"`
	Timestamp   time.Time `json:"timestamp"`
}

type ArchiveReportResponse struct {
	ArchiveName    string              `json:"archive_name"`
	BooksProcessed int                 `json:"books_processed"`
	BooksSkipped   int                 `json:"books_skipped"`
	Errors         []ScanErrorResponse `json:"errors"`
	DurationMS     int64               `json:"duration_ms"`
}

type ScannedArchiveInfo struct {
	Name        string     `json:"name"`
	BooksCount  int        `json:"books_count"`
	ErrorsCount int        `json:"errors_count"`
	ScannedAt   *time.Time `json:"scanned_at"`
}

type ScannedArchivesResponse struct {
	ScannedArchives []ScannedArchiveInfo `json:"scanned_archives"`
	TotalCount      int                  `json:"total_count"`
}

type RescanArchiveRequest struct {
	Name string `json:"name" binding:"required"`
}

func getArchivesDir() string {
	archivesDir := viper.GetString("app.files_path")
	if archivesDir == "" {
		return "./files/"
	}
	return archivesDir
}

func getCoversDir() string {
	coversDir := viper.GetString("app.posters_path")
	if coversDir == "" {
		return "./posters/"
	}
	return coversDir
}

func newBookScanService() *services.BookScanService {
	archivesDir := getArchivesDir()
	coversDir := getCoversDir()

	enableDetection, enableOpenAI, openaiTimeout := getLanguageDetectionSettings()
	var languageDetector *services.LanguageDetector
	if enableDetection {
		languageDetector = services.NewLanguageDetector(enableOpenAI, openaiTimeout)
	}

	skipDuplicates := getScanSkipDuplicates()

	llmSvc := llm.NewLLMService()
	scanner := services.NewBookScanService(archivesDir, coversDir, languageDetector, skipDuplicates, llmSvc)
	if publisher := newScanEventPublisher(); publisher != nil {
		scanner.SetScanEventPublisher(publisher)
	}
	return scanner
}

func newScanEventPublisher() *services.ScanEventPublisher {
	if wsManager == nil {
		return nil
	}
	wsConn := services.NewAdminWSConnection(wsManager)
	return services.NewScanEventPublisher(wsConn)
}

func getScanSkipDuplicates() bool {
	if viper.IsSet("SCAN_SKIP_DUPLICATES") {
		return viper.GetBool("SCAN_SKIP_DUPLICATES")
	}
	return viper.GetBool("scanning.skip_duplicates")
}

func getLanguageDetectionSettings() (bool, bool, time.Duration) {
	enableDetection := viper.GetBool("scanning.enable_language_detection")
	if viper.IsSet("SCAN_ENABLE_LANGUAGE_DETECTION") {
		enableDetection = viper.GetBool("SCAN_ENABLE_LANGUAGE_DETECTION")
	}

	enableOpenAI := viper.GetBool("scanning.enable_openai_lang_detection")
	if viper.IsSet("ENABLE_OPENAI_LANG_DETECTION") {
		enableOpenAI = viper.GetBool("ENABLE_OPENAI_LANG_DETECTION")
	}

	openaiTimeout := viper.GetDuration("OPENAI_LANG_DETECTION_TIMEOUT")
	if openaiTimeout == 0 {
		openaiTimeout = viper.GetDuration("scanning.openai_lang_detection_timeout")
	}
	if openaiTimeout == 0 {
		openaiTimeout = 5 * time.Second
	}

	return enableDetection, enableOpenAI, openaiTimeout
}

// StartScan godoc
// @Summary Start full archive scan
// @Description Start scanning all unscanned archives (async)
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Produce json
// @Success 200 {object} StartScanResponse
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 409 {object} httputil.HTTPError "Scan already running"
// @Failure 500 {object} httputil.HTTPError
// @Router /api/admin/scan [post]
func StartScan(c *gin.Context) {
	// Mutual exclusion with fix scan
	if fixState.isRunning() {
		httputil.NewError(c, http.StatusConflict, errors.New("fix scan already running"))
		return
	}

	sessionID := fmt.Sprintf("scan_%d", time.Now().UnixNano())
	startedAt := time.Now()
	if !scanState.tryStart(sessionID, startedAt) {
		httputil.NewError(c, http.StatusConflict, errors.New("scan already running"))
		return
	}

	go runFullScan(sessionID)

	c.JSON(http.StatusOK, StartScanResponse{
		SessionID: sessionID,
		StartedAt: startedAt,
		Message:   "scan started",
	})
}

// GetScanStatus godoc
// @Summary Get scan status
// @Description Returns current scan progress and stats
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Produce json
// @Success 200 {object} ScanStatusResponse
// @Failure 403 {object} httputil.HTTPError
// @Router /api/admin/scan/status [get]
func GetScanStatus(c *gin.Context) {
	c.JSON(http.StatusOK, scanState.snapshot())
}

// GetScanErrors godoc
// @Summary Get scan errors
// @Description Returns recent scan errors for failed files
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Produce json
// @Success 200 {object} ScanErrorsResponse
// @Failure 403 {object} httputil.HTTPError
// @Router /api/admin/scan/errors [get]
func GetScanErrors(c *gin.Context) {
	c.JSON(http.StatusOK, ScanErrorsResponse{
		Errors: scanState.getErrors(),
	})
}

// GetScanErrorFile godoc
// @Summary Download a failed scan file
// @Description Returns the original file from the archive for inspection
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param archive query string true "Archive name"
// @Param file query string true "File path inside archive"
// @Produce application/octet-stream
// @Success 200 {file} file
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 404 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /api/admin/scan/errors/file [get]
func GetScanErrorFile(c *gin.Context) {
	archiveName := strings.TrimSpace(c.Query("archive"))
	fileName := strings.TrimSpace(c.Query("file"))
	if archiveName == "" || fileName == "" {
		httputil.NewError(c, http.StatusBadRequest, errors.New("archive and file are required"))
		return
	}

	archivePath, err := resolveArchivePath(archiveName)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}
	defer func() {
		if closeErr := reader.Close(); closeErr != nil {
			logging.Warnf("Failed to close archive %s: %v", archiveName, closeErr)
		}
	}()

	var target *zip.File
	for _, file := range reader.File {
		if file.Name == fileName {
			target = file
			break
		}
	}

	if target == nil {
		httputil.NewError(c, http.StatusNotFound, errors.New("file not found in archive"))
		return
	}

	fileReader, err := target.Open()
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}
	defer func() {
		if closeErr := fileReader.Close(); closeErr != nil {
			logging.Warnf("Failed to close file %s from %s: %v", fileName, archiveName, closeErr)
		}
	}()

	content, err := io.ReadAll(fileReader)
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	downloadName := filepath.Base(fileName)
	if downloadName == "." || downloadName == string(os.PathSeparator) {
		downloadName = "scan_error_file"
	}
	contentType := http.DetectContentType(content)
	if strings.HasSuffix(strings.ToLower(downloadName), ".fb2") {
		contentType = "application/xml"
	}

	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", downloadName))
	c.Data(http.StatusOK, contentType, content)
}

// GetUnscannedArchives godoc
// @Summary List unscanned archives
// @Description Returns archive list with size and file count metadata
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Produce json
// @Success 200 {object} UnscannedArchivesResponse
// @Failure 403 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /api/admin/scan/unscanned [get]
func GetUnscannedArchives(c *gin.Context) {
	scanner := newBookScanService()
	archives, err := scanner.GetUnscannedArchives()
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	archivesDir := getArchivesDir()
	response := UnscannedArchivesResponse{
		UnscannedArchives: make([]UnscannedArchiveInfo, 0, len(archives)),
		TotalCount:        len(archives),
	}

	for _, archivePath := range archives {
		info, err := os.Stat(archivePath)
		if err != nil {
			logging.Warnf("Failed to stat archive %s: %v", archivePath, err)
			continue
		}

		name, err := filepath.Rel(archivesDir, archivePath)
		if err != nil {
			name = filepath.Base(archivePath)
		}

		fileCount := countArchiveFiles(archivePath)
		sizeMB := math.Round((float64(info.Size())/(1024*1024))*100) / 100

		response.UnscannedArchives = append(response.UnscannedArchives, UnscannedArchiveInfo{
			Name:        name,
			SizeMB:      sizeMB,
			FileCount:   fileCount,
			CreatedDate: info.ModTime(),
		})
	}

	response.TotalCount = len(response.UnscannedArchives)
	c.JSON(http.StatusOK, response)
}

// ScanSpecificArchive godoc
// @Summary Start archive rescan (async)
// @Description Start rescanning a single archive (async). Use GET /api/admin/scan/status to check progress.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept json
// @Param body body RescanArchiveRequest true "Archive name to rescan"
// @Produce json
// @Success 202 {object} StartScanResponse
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 404 {object} httputil.HTTPError
// @Failure 409 {object} httputil.HTTPError "Scan already running"
// @Failure 500 {object} httputil.HTTPError
// @Router /api/admin/scan/archive [post]
func ScanSpecificArchive(c *gin.Context) {
	var req RescanArchiveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("archive name required"))
		return
	}

	if scanState.isRunning() {
		httputil.NewError(c, http.StatusConflict, errors.New("scan already running"))
		return
	}

	archivePath, err := resolveArchivePath(req.Name)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	if _, err := os.Stat(archivePath); err != nil {
		if os.IsNotExist(err) {
			httputil.NewError(c, http.StatusNotFound, errors.New("archive not found"))
			return
		}
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	sessionID := fmt.Sprintf("rescan_%d", time.Now().UnixNano())
	startedAt := time.Now()

	if !scanState.tryStart(sessionID, startedAt) {
		httputil.NewError(c, http.StatusConflict, errors.New("scan already running"))
		return
	}

	go runSingleArchiveScan(sessionID, archivePath)

	c.JSON(http.StatusAccepted, StartScanResponse{
		SessionID: sessionID,
		StartedAt: startedAt,
		Message:   "rescan started",
	})
}

// ResetArchiveScanStatus godoc
// @Summary Reset archive scan status
// @Description Mark archive as unscanned, optionally delete associated books
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param name path string true "Archive name"
// @Param confirm query bool true "Confirmation flag"
// @Param delete_books query bool false "Delete associated books"
// @Produce json
// @Success 200 {object} models.Result
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 404 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /api/admin/scan/reset/{name} [delete]
func ResetArchiveScanStatus(c *gin.Context) {
	if c.Query("confirm") != "true" {
		httputil.NewError(c, http.StatusBadRequest, errors.New("confirm=true is required"))
		return
	}

	name := c.Param("name")
	if name == "" {
		httputil.NewError(c, http.StatusBadRequest, errors.New("archive name required"))
		return
	}

	if c.Query("delete_books") == "true" {
		deleted, err := database.DeleteBooksByArchive(name)
		if err != nil {
			httputil.NewError(c, http.StatusInternalServerError, err)
			return
		}
		logging.Infof("Deleted %d books for archive %s", deleted, name)
	}

	if err := database.MarkArchiveAsUnscanned(name); err != nil {
		if strings.Contains(err.Error(), "catalog not found") {
			httputil.NewError(c, http.StatusNotFound, err)
			return
		}
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, models.Result{
		Result: "reset_ok",
		Error:  nil,
	})
}

// GetScannedArchives godoc
// @Summary List scanned archives
// @Description Returns scanned archive list with scan statistics
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Produce json
// @Success 200 {object} ScannedArchivesResponse
// @Failure 403 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /api/admin/scan/scanned [get]
func GetScannedArchives(c *gin.Context) {
	catalogs, err := database.GetAllCatalogs()
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	response := ScannedArchivesResponse{
		ScannedArchives: make([]ScannedArchiveInfo, 0),
		TotalCount:      0,
	}

	for _, catalog := range catalogs {
		if !catalog.IsScanned {
			continue
		}
		response.ScannedArchives = append(response.ScannedArchives, ScannedArchiveInfo{
			Name:        catalog.CatName,
			BooksCount:  catalog.BooksCount,
			ErrorsCount: catalog.ErrorsCount,
			ScannedAt:   catalog.ScannedAt,
		})
	}

	response.TotalCount = len(response.ScannedArchives)
	c.JSON(http.StatusOK, response)
}

func runFullScan(sessionID string) {
	scanner := newBookScanService()
	publisher := newScanEventPublisher()
	archives, err := scanner.GetUnscannedArchives()
	if err != nil {
		scanState.fail(sessionID, fmt.Errorf("failed to get unscanned archives: %w", err))
		if publisher != nil {
			publisher.PublishScanError(err)
		}
		return
	}

	scanState.setTotalArchives(sessionID, len(archives))
	if len(archives) == 0 {
		scanState.finish(sessionID)
		if publisher != nil {
			publisher.PublishScanCompleted(&services.ScanReport{
				TotalArchives: 0,
				Duration:      0,
			})
		}
		return
	}

	if publisher != nil {
		publisher.PublishScanStarted(len(archives))
	}

	// Start progress monitoring goroutine
	progressTicker := time.NewTicker(500 * time.Millisecond)
	defer progressTicker.Stop()

	progressDone := make(chan struct{})
	go func() {
		for {
			select {
			case <-progressTicker.C:
				processed, total := scanner.GetScanProgress()
				if total > 0 {
					scanState.mu.Lock()
					scanState.totalBooks = scanState.totalBooks - scanState.totalBooks%1000 + processed
					archivesProcessed := scanState.archivesProcessed
					totalArchives := scanState.totalArchives
					currentArchive := scanState.currentArchive
					totalBooks := scanState.totalBooks
					startedAt := scanState.startedAt
					scanState.mu.Unlock()

					// Calculate elapsed seconds
					var elapsedSeconds int64
					if !startedAt.IsZero() {
						elapsedSeconds = int64(time.Since(startedAt).Seconds())
					}

					// Send WebSocket progress update
					scanner.PublishProgress(currentArchive, archivesProcessed, totalArchives, totalBooks, totalBooks, elapsedSeconds)
				}
			case <-progressDone:
				return
			}
		}
	}()

	archivesDir := getArchivesDir()
	scanReport := &services.ScanReport{
		TotalArchives:  len(archives),
		ArchiveReports: []services.ArchiveReport{},
		Errors:         []services.ScanError{},
	}
	scanStart := time.Now()
	for _, archivePath := range archives {
		archiveName := archiveNameFromPath(archivesDir, archivePath)
		scanState.setCurrentArchive(sessionID, archiveName)

		report, scanErr := scanner.ScanArchive(archivePath)
		scanState.addErrors(sessionID, report)
		if report != nil {
			scanReport.ArchiveReports = append(scanReport.ArchiveReports, *report)
			scanReport.ProcessedBooks += report.BooksProcessed
			scanReport.SkippedBooks += report.BooksSkipped
			scanReport.Errors = append(scanReport.Errors, report.Errors...)
		}
		if scanErr != nil && publisher != nil {
			publisher.PublishScanError(scanErr)
		}
		scanState.applyArchiveResult(sessionID, report, scanErr)
	}

	// Stop progress monitoring
	close(progressDone)
	progressTicker.Stop()

	if publisher != nil {
		scanReport.Duration = time.Since(scanStart)
		publisher.PublishScanCompleted(scanReport)
	}

	scanState.finish(sessionID)
}

func runSingleArchiveScan(sessionID string, archivePath string) {
	defer func() {
		if r := recover(); r != nil {
			logging.Error(fmt.Sprintf("Panic in archive scan: %v", r))
			scanState.fail(sessionID, fmt.Errorf("panic: %v", r))
		}
	}()

	scanner := newBookScanService()
	publisher := newScanEventPublisher()
	archivesDir := getArchivesDir()
	archiveName := archiveNameFromPath(archivesDir, archivePath)

	scanState.setTotalArchives(sessionID, 1)
	scanState.setCurrentArchive(sessionID, archiveName)
	if publisher != nil {
		publisher.PublishScanStarted(1)
	}

	// Start progress monitoring goroutine
	progressTicker := time.NewTicker(500 * time.Millisecond)
	defer progressTicker.Stop()

	progressDone := make(chan struct{})
	go func() {
		for {
			select {
			case <-progressTicker.C:
				processed, total := scanner.GetScanProgress()
				if total > 0 {
					scanState.mu.Lock()
					scanState.totalBooks = processed
					archivesProcessed := scanState.archivesProcessed
					totalArchives := scanState.totalArchives
					currentArchive := scanState.currentArchive
					startedAt := scanState.startedAt
					scanState.mu.Unlock()

					// Calculate elapsed seconds
					var elapsedSeconds int64
					if !startedAt.IsZero() {
						elapsedSeconds = int64(time.Since(startedAt).Seconds())
					}

					// Send WebSocket progress update
					scanner.PublishProgress(currentArchive, archivesProcessed, totalArchives, processed, total, elapsedSeconds)
				}
			case <-progressDone:
				return
			}
		}
	}()

	// Start the actual scan
	report, scanErr := scanner.ScanArchive(archivePath)
	scanState.addErrors(sessionID, report)
	if scanErr != nil && publisher != nil {
		publisher.PublishScanError(scanErr)
	}

	// Stop progress monitoring
	close(progressDone)
	progressTicker.Stop()

	// Apply final results
	scanState.applyArchiveResult(sessionID, report, scanErr)
	if publisher != nil {
		scanReport := &services.ScanReport{
			TotalArchives:  1,
			ProcessedBooks: 0,
			SkippedBooks:   0,
			Errors:         []services.ScanError{},
			Duration:       0,
		}
		if report != nil {
			scanReport.ArchiveReports = []services.ArchiveReport{*report}
			scanReport.ProcessedBooks = report.BooksProcessed
			scanReport.SkippedBooks = report.BooksSkipped
			scanReport.Errors = append(scanReport.Errors, report.Errors...)
			scanReport.Duration = report.Duration
		}
		publisher.PublishScanCompleted(scanReport)
	}
	scanState.finish(sessionID)
}

func (s *bookScanState) tryStart(sessionID string, startedAt time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return false
	}
	s.running = true
	s.sessionID = sessionID
	s.startedAt = startedAt
	s.finishedAt = nil
	s.totalArchives = 0
	s.archivesProcessed = 0
	s.currentArchive = ""
	s.totalBooks = 0
	s.totalErrors = 0
	s.lastError = ""
	s.errors = nil
	return true
}

func (s *bookScanState) isRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *bookScanState) setTotalArchives(sessionID string, total int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionID != sessionID {
		return
	}
	s.totalArchives = total
}

func (s *bookScanState) setCurrentArchive(sessionID, archive string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionID != sessionID {
		return
	}
	s.currentArchive = archive
}

func (s *bookScanState) applyArchiveResult(sessionID string, report *services.ArchiveReport, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionID != sessionID {
		return
	}

	s.archivesProcessed++
	if report != nil {
		s.totalBooks += report.BooksProcessed
		s.totalErrors += len(report.Errors)
	}
	if err != nil {
		s.totalErrors++
		s.lastError = err.Error()
	}
}

func (s *bookScanState) fail(sessionID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionID != sessionID {
		return
	}
	s.running = false
	now := time.Now()
	s.finishedAt = &now
	s.lastError = err.Error()
	s.totalErrors++
}

func (s *bookScanState) finish(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionID != sessionID {
		return
	}
	s.running = false
	s.currentArchive = ""
	now := time.Now()
	s.finishedAt = &now
}

func (s *bookScanState) snapshot() ScanStatusResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	response := ScanStatusResponse{
		IsRunning:         s.running,
		SessionID:         s.sessionID,
		TotalArchives:     s.totalArchives,
		ArchivesProcessed: s.archivesProcessed,
		CurrentArchive:    s.currentArchive,
		TotalBooks:        s.totalBooks,
		TotalErrors:       s.totalErrors,
		LastError:         s.lastError,
	}

	if !s.startedAt.IsZero() {
		startedAt := s.startedAt
		response.StartedAt = &startedAt
	}
	if s.finishedAt != nil {
		finishedAt := *s.finishedAt
		response.FinishedAt = &finishedAt
	}

	var elapsed time.Duration
	if s.running && !s.startedAt.IsZero() {
		elapsed = time.Since(s.startedAt)
	} else if s.finishedAt != nil && !s.startedAt.IsZero() {
		elapsed = s.finishedAt.Sub(s.startedAt)
	}
	response.ElapsedSeconds = int64(elapsed.Seconds())

	if s.totalArchives > 0 {
		progress := (s.archivesProcessed * 100) / s.totalArchives
		if !s.running && s.archivesProcessed >= s.totalArchives {
			progress = 100
		}
		response.ProgressPercent = progress
	}

	return response
}

func (s *bookScanState) addErrors(sessionID string, report *services.ArchiveReport) {
	if report == nil || len(report.Errors) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionID != sessionID {
		return
	}
	const maxErrors = 500
	if s.errors == nil {
		s.errors = make([]ScanErrorResponse, 0, len(report.Errors))
	}
	for _, errItem := range report.Errors {
		if len(s.errors) >= maxErrors {
			break
		}
		s.errors = append(s.errors, ScanErrorResponse{
			FileName:    errItem.FileName,
			ArchiveName: errItem.ArchiveName,
			Error:       errItem.Error,
			Timestamp:   errItem.Timestamp,
		})
	}
}

func (s *bookScanState) getErrors() []ScanErrorResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.errors) == 0 {
		return nil
	}
	out := make([]ScanErrorResponse, len(s.errors))
	copy(out, s.errors)
	return out
}

func resolveArchivePath(name string) (string, error) {
	if name == "" {
		return "", errors.New("archive name required")
	}

	cleanName := filepath.Clean(name)
	archivesDir := getArchivesDir()
	absDir, err := filepath.Abs(archivesDir)
	if err != nil {
		return "", err
	}

	absPath, err := filepath.Abs(filepath.Join(archivesDir, cleanName))
	if err != nil {
		return "", err
	}

	if absPath != absDir && !strings.HasPrefix(absPath, absDir+string(os.PathSeparator)) {
		return "", errors.New("invalid archive path")
	}

	return absPath, nil
}

func archiveNameFromPath(archivesDir, archivePath string) string {
	name, err := filepath.Rel(archivesDir, archivePath)
	if err != nil {
		return filepath.Base(archivePath)
	}
	return name
}

func countArchiveFiles(archivePath string) int {
	zipReader, err := zip.OpenReader(archivePath)
	if err != nil {
		logging.Warnf("Failed to open archive %s for counting: %v", archivePath, err)
		return 0
	}
	defer func() {
		if closeErr := zipReader.Close(); closeErr != nil {
			logging.Warnf("Failed to close archive %s: %v", archivePath, closeErr)
		}
	}()

	count := 0
	for _, file := range zipReader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		if strings.HasSuffix(strings.ToLower(file.Name), ".fb2") {
			count++
		}
	}
	return count
}

func archiveReportToResponse(report *services.ArchiveReport) ArchiveReportResponse {
	response := ArchiveReportResponse{
		ArchiveName:    report.ArchiveName,
		BooksProcessed: report.BooksProcessed,
		BooksSkipped:   report.BooksSkipped,
		DurationMS:     report.Duration.Milliseconds(),
	}
	if len(report.Errors) > 0 {
		response.Errors = make([]ScanErrorResponse, 0, len(report.Errors))
		for _, errItem := range report.Errors {
			response.Errors = append(response.Errors, ScanErrorResponse{
				FileName:    errItem.FileName,
				ArchiveName: errItem.ArchiveName,
				Error:       errItem.Error,
				Timestamp:   errItem.Timestamp,
			})
		}
	}
	return response
}
