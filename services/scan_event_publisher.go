package services

import "time"

const (
	ScanStarted        = "scan_started"
	ArchiveStarted     = "archive_started"
	BookProcessed      = "book_processed"
	ArchiveCompleted   = "archive_completed"
	ScanCompleted      = "scan_completed"
	ScanErrorEventType = "scan_error"
	ScanProgress       = "scan_progress"

	FixScanStartedType   = "fix_scan_started"
	FixScanProgressType  = "fix_scan_progress"
	FixScanCompletedType = "fix_scan_completed"
	FixScanErrorType     = "fix_scan_error"

	GenreTitleGenStartedType   = "genre_title_gen_started"
	GenreTitleGenProgressType  = "genre_title_gen_progress"
	GenreTitleGenCompletedType = "genre_title_gen_completed"
)

type ScanEventPublisher struct {
	wsConn WebSocketConnection
}

type ScanStartedEvent struct {
	TotalArchives int       `json:"total_archives"`
	Timestamp     time.Time `json:"timestamp"`
}

type ArchiveStartedEvent struct {
	ArchiveName string    `json:"archive_name"`
	Timestamp   time.Time `json:"timestamp"`
}

type BookProcessedEvent struct {
	ArchiveName string    `json:"archive_name"`
	BookTitle   string    `json:"book_title"`
	BookID      int64     `json:"book_id"`
	Timestamp   time.Time `json:"timestamp"`
}

type ArchiveCompletedEvent struct {
	ArchiveName string      `json:"archive_name"`
	BooksCount  int         `json:"books_count"`
	ErrorsCount int         `json:"errors_count"`
	DurationMS  int64       `json:"duration_ms"`
	Timestamp   time.Time   `json:"timestamp"`
	Errors      []ScanError `json:"errors,omitempty"`
}

type ScanCompletedEvent struct {
	TotalArchives  int             `json:"total_archives"`
	TotalBooks     int             `json:"total_books"`
	TotalErrors    int             `json:"total_errors"`
	DurationMS     int64           `json:"duration_ms"`
	Timestamp      time.Time       `json:"timestamp"`
	ArchiveReports []ArchiveReport `json:"archive_reports,omitempty"`
}

type ScanErrorEvent struct {
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

type ScanProgressEvent struct {
	CurrentArchive    string    `json:"current_archive"`
	ArchivesProcessed int       `json:"archives_processed"`
	TotalArchives     int       `json:"total_archives"`
	BooksProcessed    int       `json:"books_processed"`
	TotalBooks        int       `json:"total_books"`
	ProgressPercent   int       `json:"progress_percent"`
	ElapsedSeconds    int64     `json:"elapsed_seconds"`
	Timestamp         time.Time `json:"timestamp"`
}

func NewScanEventPublisher(wsConn WebSocketConnection) *ScanEventPublisher {
	return &ScanEventPublisher{wsConn: wsConn}
}

func (p *ScanEventPublisher) PublishScanStarted(totalArchives int) {
	if p == nil || p.wsConn == nil {
		return
	}
	_ = p.wsConn.SendMessage(ScanStarted, ScanStartedEvent{
		TotalArchives: totalArchives,
		Timestamp:     time.Now(),
	})
}

func (p *ScanEventPublisher) PublishArchiveStarted(archiveName string) {
	if p == nil || p.wsConn == nil {
		return
	}
	_ = p.wsConn.SendMessage(ArchiveStarted, ArchiveStartedEvent{
		ArchiveName: archiveName,
		Timestamp:   time.Now(),
	})
}

func (p *ScanEventPublisher) PublishBookProcessed(archiveName, title string, bookID int64) {
	if p == nil || p.wsConn == nil {
		return
	}
	_ = p.wsConn.SendMessage(BookProcessed, BookProcessedEvent{
		ArchiveName: archiveName,
		BookTitle:   title,
		BookID:      bookID,
		Timestamp:   time.Now(),
	})
}

func (p *ScanEventPublisher) PublishArchiveCompleted(report *ArchiveReport) {
	if p == nil || p.wsConn == nil || report == nil {
		return
	}
	_ = p.wsConn.SendMessage(ArchiveCompleted, ArchiveCompletedEvent{
		ArchiveName: report.ArchiveName,
		BooksCount:  report.BooksProcessed,
		ErrorsCount: len(report.Errors),
		DurationMS:  report.Duration.Milliseconds(),
		Timestamp:   time.Now(),
		Errors:      report.Errors,
	})
}

func (p *ScanEventPublisher) PublishScanCompleted(report *ScanReport) {
	if p == nil || p.wsConn == nil || report == nil {
		return
	}
	_ = p.wsConn.SendMessage(ScanCompleted, ScanCompletedEvent{
		TotalArchives:  report.TotalArchives,
		TotalBooks:     report.ProcessedBooks,
		TotalErrors:    len(report.Errors),
		DurationMS:     report.Duration.Milliseconds(),
		Timestamp:      time.Now(),
		ArchiveReports: report.ArchiveReports,
	})
}

func (p *ScanEventPublisher) PublishScanError(err error) {
	if p == nil || p.wsConn == nil || err == nil {
		return
	}
	_ = p.wsConn.SendMessage(ScanErrorEventType, ScanErrorEvent{
		Message:   err.Error(),
		Timestamp: time.Now(),
	})
}

func (p *ScanEventPublisher) PublishScanProgress(currentArchive string, archivesProcessed, totalArchives, booksProcessed, totalBooks int, elapsedSeconds int64) {
	if p == nil || p.wsConn == nil {
		return
	}

	progressPercent := 0
	if totalBooks > 0 {
		progressPercent = (booksProcessed * 100) / totalBooks
	} else if totalArchives > 0 {
		progressPercent = (archivesProcessed * 100) / totalArchives
	}

	_ = p.wsConn.SendMessage(ScanProgress, ScanProgressEvent{
		CurrentArchive:    currentArchive,
		ArchivesProcessed: archivesProcessed,
		TotalArchives:     totalArchives,
		BooksProcessed:    booksProcessed,
		TotalBooks:        totalBooks,
		ProgressPercent:   progressPercent,
		ElapsedSeconds:    elapsedSeconds,
		Timestamp:         time.Now(),
	})
}

// Fix Scan event types and publish methods

type FixScanStartedEvent struct {
	ScanType      string    `json:"scan_type"`
	TotalBooks    int       `json:"total_books"`
	TotalArchives int       `json:"total_archives"`
	Timestamp     time.Time `json:"timestamp"`
}

type FixScanProgressEvent struct {
	ScanType        string    `json:"scan_type"`
	CurrentArchive  string    `json:"current_archive"`
	BooksProcessed  int       `json:"books_processed"`
	TotalBooks      int       `json:"total_books"`
	BooksUpdated    int       `json:"books_updated"`
	ErrorCount      int       `json:"error_count"`
	ProgressPercent int       `json:"progress_percent"`
	ElapsedSeconds  int64     `json:"elapsed_seconds"`
	Timestamp       time.Time `json:"timestamp"`
}

type FixScanCompletedEvent struct {
	ScanType      string    `json:"scan_type"`
	TotalBooks    int       `json:"total_books"`
	UpdatedBooks  int       `json:"updated_books"`
	TotalArchives int       `json:"total_archives"`
	ErrorCount    int       `json:"error_count"`
	DurationMS    int64     `json:"duration_ms"`
	Timestamp     time.Time `json:"timestamp"`
}

type FixScanErrorEvent struct {
	ScanType  string    `json:"scan_type"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

func (p *ScanEventPublisher) PublishFixScanStarted(totalBooks, totalArchives int) {
	if p == nil || p.wsConn == nil {
		return
	}
	_ = p.wsConn.SendMessage(FixScanStartedType, FixScanStartedEvent{
		ScanType:      "fix",
		TotalBooks:    totalBooks,
		TotalArchives: totalArchives,
		Timestamp:     time.Now(),
	})
}

func (p *ScanEventPublisher) PublishFixScanProgress(currentArchive string, booksProcessed, totalBooks, booksUpdated, errorCount int, elapsedSeconds int64) {
	if p == nil || p.wsConn == nil {
		return
	}

	progressPercent := 0
	if totalBooks > 0 {
		progressPercent = (booksProcessed * 100) / totalBooks
	}

	_ = p.wsConn.SendMessage(FixScanProgressType, FixScanProgressEvent{
		ScanType:        "fix",
		CurrentArchive:  currentArchive,
		BooksProcessed:  booksProcessed,
		TotalBooks:      totalBooks,
		BooksUpdated:    booksUpdated,
		ErrorCount:      errorCount,
		ProgressPercent: progressPercent,
		ElapsedSeconds:  elapsedSeconds,
		Timestamp:       time.Now(),
	})
}

func (p *ScanEventPublisher) PublishFixScanCompleted(report *FixScanReport) {
	if p == nil || p.wsConn == nil || report == nil {
		return
	}
	_ = p.wsConn.SendMessage(FixScanCompletedType, FixScanCompletedEvent{
		ScanType:      "fix",
		TotalBooks:    report.TotalBooks,
		UpdatedBooks:  report.UpdatedBooks,
		TotalArchives: report.TotalArchives,
		ErrorCount:    report.ErrorCount,
		DurationMS:    report.Duration.Milliseconds(),
		Timestamp:     time.Now(),
	})
}

func (p *ScanEventPublisher) PublishFixScanError(err error) {
	if p == nil || p.wsConn == nil || err == nil {
		return
	}
	_ = p.wsConn.SendMessage(FixScanErrorType, FixScanErrorEvent{
		ScanType:  "fix",
		Message:   err.Error(),
		Timestamp: time.Now(),
	})
}

// Genre title generation events

type GenreTitleGenStartedEvent struct {
	Total     int       `json:"total"`
	Timestamp time.Time `json:"timestamp"`
}

type GenreTitleGenProgressEvent struct {
	Total           int       `json:"total"`
	Processed       int       `json:"processed"`
	CurrentGenre    string    `json:"current_genre"`
	ProgressPercent int       `json:"progress_percent"`
	Timestamp       time.Time `json:"timestamp"`
}

type GenreTitleGenCompletedEvent struct {
	Total      int       `json:"total"`
	Updated    int       `json:"updated"`
	DurationMS int64     `json:"duration_ms"`
	Timestamp  time.Time `json:"timestamp"`
}

func (p *ScanEventPublisher) PublishGenreTitleGenStarted(total int) {
	if p == nil || p.wsConn == nil {
		return
	}
	_ = p.wsConn.SendMessage(GenreTitleGenStartedType, GenreTitleGenStartedEvent{
		Total:     total,
		Timestamp: time.Now(),
	})
}

func (p *ScanEventPublisher) PublishGenreTitleGenProgress(total, processed int, currentGenre string) {
	if p == nil || p.wsConn == nil {
		return
	}
	progressPercent := 0
	if total > 0 {
		progressPercent = (processed * 100) / total
	}
	_ = p.wsConn.SendMessage(GenreTitleGenProgressType, GenreTitleGenProgressEvent{
		Total:           total,
		Processed:       processed,
		CurrentGenre:    currentGenre,
		ProgressPercent: progressPercent,
		Timestamp:       time.Now(),
	})
}

func (p *ScanEventPublisher) PublishGenreTitleGenCompleted(total, updated int, durationMS int64) {
	if p == nil || p.wsConn == nil {
		return
	}
	_ = p.wsConn.SendMessage(GenreTitleGenCompletedType, GenreTitleGenCompletedEvent{
		Total:      total,
		Updated:    updated,
		DurationMS: durationMS,
		Timestamp:  time.Now(),
	})
}
