package api

import (
	"archive/zip"
	"errors"
	"fmt"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/services"
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

	scanner := services.NewBookScanService(archivesDir, coversDir, languageDetector, skipDuplicates)
	if wsManager != nil {
		wsConn := services.NewAdminWSConnection(wsManager)
		scanner.SetScanEventPublisher(services.NewScanEventPublisher(wsConn))
	}
	return scanner
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

	enableOpenAI := false
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
// @Summary Scan a specific archive
// @Description Scan one archive by name (blocking)
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param name path string true "Archive name"
// @Produce json
// @Success 200 {object} ArchiveReportResponse
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 404 {object} httputil.HTTPError
// @Failure 409 {object} httputil.HTTPError "Scan already running"
// @Failure 500 {object} httputil.HTTPError
// @Router /api/admin/scan/archive/{name} [post]
func ScanSpecificArchive(c *gin.Context) {
	if scanState.isRunning() {
		httputil.NewError(c, http.StatusConflict, errors.New("scan already running"))
		return
	}

	name := c.Param("name")
	archivePath, err := resolveArchivePath(name)
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

	scanner := newBookScanService()
	report, err := scanner.ScanArchive(archivePath)
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, archiveReportToResponse(report))
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

func runFullScan(sessionID string) {
	scanner := newBookScanService()
	archives, err := scanner.GetUnscannedArchives()
	if err != nil {
		scanState.fail(sessionID, fmt.Errorf("failed to get unscanned archives: %w", err))
		return
	}

	scanState.setTotalArchives(sessionID, len(archives))
	if len(archives) == 0 {
		scanState.finish(sessionID)
		return
	}

	archivesDir := getArchivesDir()
	for _, archivePath := range archives {
		archiveName := archiveNameFromPath(archivesDir, archivePath)
		scanState.setCurrentArchive(sessionID, archiveName)

		report, scanErr := scanner.ScanArchive(archivePath)
		scanState.applyArchiveResult(sessionID, report, scanErr)
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
