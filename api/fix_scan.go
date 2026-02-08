package api

import (
	"context"
	"errors"
	"fmt"
	"gopds-api/httputil"
	"gopds-api/logging"
	"gopds-api/services"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

type fixScanState struct {
	mu                sync.Mutex
	running           bool
	sessionID         string
	startedAt         time.Time
	finishedAt        *time.Time
	totalBooks        int
	booksProcessed    int
	booksUpdated      int
	totalArchives     int
	archivesProcessed int
	currentArchive    string
	errorCount        int
	lastError         string
	cancelFunc        context.CancelFunc
}

var fixState fixScanState

type StartFixScanRequest struct {
	Workers int `json:"workers"`
}

type FixScanStatusResponse struct {
	IsRunning       bool       `json:"is_running"`
	SessionID       string     `json:"session_id,omitempty"`
	TotalBooks      int        `json:"total_books"`
	BooksProcessed  int        `json:"books_processed"`
	BooksUpdated    int        `json:"books_updated"`
	TotalArchives   int        `json:"total_archives"`
	CurrentArchive  string     `json:"current_archive,omitempty"`
	ErrorCount      int        `json:"error_count"`
	ProgressPercent int        `json:"progress_percent"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	ElapsedSeconds  int64      `json:"elapsed_seconds"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
}

func (s *fixScanState) tryStart(sessionID string, startedAt time.Time, cancel context.CancelFunc) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return false
	}
	s.running = true
	s.sessionID = sessionID
	s.startedAt = startedAt
	s.finishedAt = nil
	s.totalBooks = 0
	s.booksProcessed = 0
	s.booksUpdated = 0
	s.totalArchives = 0
	s.archivesProcessed = 0
	s.currentArchive = ""
	s.errorCount = 0
	s.lastError = ""
	s.cancelFunc = cancel
	return true
}

func (s *fixScanState) isRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *fixScanState) snapshot() FixScanStatusResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	resp := FixScanStatusResponse{
		IsRunning:      s.running,
		SessionID:      s.sessionID,
		TotalBooks:     s.totalBooks,
		BooksProcessed: s.booksProcessed,
		BooksUpdated:   s.booksUpdated,
		TotalArchives:  s.totalArchives,
		CurrentArchive: s.currentArchive,
		ErrorCount:     s.errorCount,
		LastError:      s.lastError,
	}

	if !s.startedAt.IsZero() {
		startedAt := s.startedAt
		resp.StartedAt = &startedAt
	}
	if s.finishedAt != nil {
		finishedAt := *s.finishedAt
		resp.FinishedAt = &finishedAt
	}

	var elapsed time.Duration
	if s.running && !s.startedAt.IsZero() {
		elapsed = time.Since(s.startedAt)
	} else if s.finishedAt != nil && !s.startedAt.IsZero() {
		elapsed = s.finishedAt.Sub(s.startedAt)
	}
	resp.ElapsedSeconds = int64(elapsed.Seconds())

	if s.totalBooks > 0 {
		resp.ProgressPercent = (s.booksProcessed * 100) / s.totalBooks
		if !s.running && s.booksProcessed >= s.totalBooks {
			resp.ProgressPercent = 100
		}
	}

	return resp
}

func (s *fixScanState) finish(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionID != sessionID {
		return
	}
	s.running = false
	s.currentArchive = ""
	now := time.Now()
	s.finishedAt = &now
	s.cancelFunc = nil
}

func (s *fixScanState) fail(sessionID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionID != sessionID {
		return
	}
	s.running = false
	now := time.Now()
	s.finishedAt = &now
	s.lastError = err.Error()
	s.errorCount++
	s.cancelFunc = nil
}

func (s *fixScanState) cancel() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running || s.cancelFunc == nil {
		return false
	}
	s.cancelFunc()
	return true
}

func (s *fixScanState) updateProgress(booksProcessed, booksUpdated, totalBooks, totalArchives, errorCount int, currentArchive string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.booksProcessed = booksProcessed
	s.booksUpdated = booksUpdated
	s.totalBooks = totalBooks
	s.totalArchives = totalArchives
	s.errorCount = errorCount
	s.currentArchive = currentArchive
}

// StartFixScan godoc
// @Summary Start fix scan (bulk metadata refresh)
// @Description Re-parses every FB2 book in the database and updates metadata
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept json
// @Produce json
// @Param body body StartFixScanRequest false "Fix scan parameters"
// @Success 200 {object} StartScanResponse
// @Failure 409 {object} httputil.HTTPError "Scan already running"
// @Failure 500 {object} httputil.HTTPError
// @Router /api/admin/scan/fix [post]
func StartFixScan(c *gin.Context) {
	var req StartFixScanRequest
	_ = c.ShouldBindJSON(&req)

	workers := req.Workers
	if workers <= 0 {
		workers = 4
	}
	if workers > 8 {
		workers = 8
	}

	// Mutual exclusion with archive scan
	if scanState.isRunning() {
		httputil.NewError(c, http.StatusConflict, errors.New("archive scan already running"))
		return
	}

	sessionID := fmt.Sprintf("fix_scan_%d", time.Now().UnixNano())
	startedAt := time.Now()
	ctx, cancel := context.WithCancel(context.Background())

	if !fixState.tryStart(sessionID, startedAt, cancel) {
		cancel()
		httputil.NewError(c, http.StatusConflict, errors.New("fix scan already running"))
		return
	}

	go runFixScan(ctx, sessionID, workers)

	c.JSON(http.StatusOK, StartScanResponse{
		SessionID: sessionID,
		StartedAt: startedAt,
		Message:   "fix scan started",
	})
}

// GetFixScanStatus godoc
// @Summary Get fix scan status
// @Description Returns current fix scan progress and stats
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Produce json
// @Success 200 {object} FixScanStatusResponse
// @Failure 403 {object} httputil.HTTPError
// @Router /api/admin/scan/fix/status [get]
func GetFixScanStatus(c *gin.Context) {
	c.JSON(http.StatusOK, fixState.snapshot())
}

// CancelFixScan godoc
// @Summary Cancel fix scan
// @Description Cancels the currently running fix scan
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Produce json
// @Success 200 {object} map[string]string
// @Failure 404 {object} httputil.HTTPError "No fix scan running"
// @Router /api/admin/scan/fix/cancel [post]
func CancelFixScan(c *gin.Context) {
	if fixState.cancel() {
		c.JSON(http.StatusOK, gin.H{"message": "fix scan cancellation requested"})
	} else {
		httputil.NewError(c, http.StatusNotFound, errors.New("no fix scan running"))
	}
}

func newFixScanService() *services.FixScanService {
	archivesDir := getArchivesDir()
	coversDir := getCoversDir()

	enableDetection, enableOpenAI, openaiTimeout := getLanguageDetectionSettings()
	var ld *services.LanguageDetector
	if enableDetection {
		ld = services.NewLanguageDetector(enableOpenAI, openaiTimeout)
	}

	return services.NewFixScanService(archivesDir, coversDir, ld)
}

func runFixScan(ctx context.Context, sessionID string, workers int) {
	defer func() {
		if r := recover(); r != nil {
			logging.Error(fmt.Sprintf("Panic in fix scan: %v", r))
			fixState.fail(sessionID, fmt.Errorf("panic: %v", r))
		}
	}()

	fixService := newFixScanService()
	publisher := newScanEventPublisher()
	if publisher != nil {
		fixService.SetScanEventPublisher(publisher)
	}

	// Start progress ticker
	progressTicker := time.NewTicker(500 * time.Millisecond)
	progressDone := make(chan struct{})

	go func() {
		for {
			select {
			case <-progressTicker.C:
				booksProcessed := int(atomic.LoadInt64(&fixService.ProgressCount))
				booksUpdated := int(atomic.LoadInt64(&fixService.UpdatedCount))
				errorCount := int(atomic.LoadInt64(&fixService.ErrorCount))
				totalBooks := int(atomic.LoadInt64(&fixService.TotalBooks))
				totalArchives := int(atomic.LoadInt64(&fixService.TotalArchives))
				currentArchive, _ := fixService.CurrentArchive.Load().(string)

				fixState.updateProgress(booksProcessed, booksUpdated, totalBooks, totalArchives, errorCount, currentArchive)

				if publisher != nil && totalBooks > 0 {
					var elapsedSeconds int64
					fixState.mu.Lock()
					if !fixState.startedAt.IsZero() {
						elapsedSeconds = int64(time.Since(fixState.startedAt).Seconds())
					}
					fixState.mu.Unlock()

					publisher.PublishFixScanProgress(
						currentArchive,
						booksProcessed,
						totalBooks,
						booksUpdated,
						errorCount,
						elapsedSeconds,
					)
				}
			case <-progressDone:
				return
			}
		}
	}()

	report, err := fixService.RunFixScan(ctx, workers)

	// Stop progress ticker
	close(progressDone)
	progressTicker.Stop()

	if err != nil {
		fixState.fail(sessionID, err)
		if publisher != nil {
			publisher.PublishFixScanError(err)
		}
		return
	}

	// Final state update
	if report != nil {
		fixState.updateProgress(
			report.TotalBooks,
			report.UpdatedBooks,
			report.TotalBooks,
			report.TotalArchives,
			report.ErrorCount,
			"",
		)
	}

	fixState.finish(sessionID)
}
