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

func (p *ScanEventPublisher) PublishScanProgress(currentArchive string, archivesProcessed, totalArchives, booksProcessed, totalBooks int) {
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
		Timestamp:         time.Now(),
	})
}
