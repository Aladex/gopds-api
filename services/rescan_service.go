package services

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"gopds-api/database"
	"gopds-api/internal/parser"
	"gopds-api/internal/posters"
	"gopds-api/logging"
	"gopds-api/models"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// RescanService handles book rescanning with approval workflow
type RescanService struct {
	archivesDir string
	coversDir   string
}

// NewRescanService creates a new RescanService
func NewRescanService(archivesDir, coversDir string) *RescanService {
	return &RescanService{
		archivesDir: archivesDir,
		coversDir:   coversDir,
	}
}

// RescanBookPreview parses FB2 file and returns preview of changes
func (s *RescanService) RescanBookPreview(bookID int64, userID int64) (*models.RescanPreview, error) {
	// 1. Get existing book from DB
	book := &models.Book{}
	err := database.GetDB().Model(book).
		Relation("Authors").
		Relation("Series").
		Where("id = ?", bookID).
		Select(book)
	if err != nil {
		logging.Error("Failed to get book: %v", err)
		return nil, errors.New("book not found")
	}

	// 2. Build file path: archivesDir/Path/FileName
	archivePath := filepath.Join(s.archivesDir, book.Path)
	if !strings.HasSuffix(book.Path, ".zip") {
		archivePath = filepath.Join(s.archivesDir, book.Path+".zip")
	}

	// 3. Open archive and extract FB2 file
	fbzContent, err := s.extractFB2FromArchive(archivePath, book.FileName)
	if err != nil {
		logging.Error("Failed to extract FB2 from archive: %v", err)
		return nil, fmt.Errorf("failed to extract FB2 file: %w", err)
	}

	// 4. Parse FB2 metadata
	fbzParser := parser.NewFB2Parser(true) // readCover=true
	parsedBook, err := fbzParser.Parse(bytes.NewReader(fbzContent))
	if err != nil {
		logging.Error("Failed to parse FB2: %v", err)
		return nil, fmt.Errorf("failed to parse FB2 file: %w", err)
	}

	// 5. Get series number from OrderToSeries table if series exists
	var serNo int64 = 0
	if len(book.Series) > 0 && book.Series[0] != nil {
		orderToSeries := &models.OrderToSeries{}
		err := database.GetDB().Model(orderToSeries).
			Where("book_id = ? AND ser_id = ?", bookID, book.Series[0].ID).
			Select(orderToSeries)
		if err == nil {
			serNo = orderToSeries.SerNo
		}
		// If error, just continue with serNo = 0 (no number assigned)
	}

	// 6. Get old values
	oldValues := s.bookToRescanValues(book, serNo)

	// 7. Get new values
	newValues := s.parsedToRescanValues(parsedBook)

	// 8. Calculate diff
	diff := s.calculateDiff(oldValues, newValues)

	// 9. Save to pending table
	pending := &models.BookRescanPending{
		BookID:          bookID,
		Title:           newValues.Title,
		Annotation:      newValues.Annotation,
		Lang:            newValues.Lang,
		DocDate:         newValues.DocDate,
		CoverUpdated:    newValues.HasCover,
		CreatedByUserID: userID,
	}

	// Store parsed data as JSON
	_ = pending.SetAuthors(newValues.Authors)
	_ = pending.SetSeries(newValues.Series)
	_ = pending.SetTags(newValues.Tags)

	// Save cover data if extracted
	if parsedBook.Cover != nil {
		pending.CoverData = parsedBook.Cover
	}

	// Debug: Log what we're about to save
	logging.Info("Saving rescan pending: BookID=%d, AuthorsJSON=%s, SeriesJSON=%s, TagsJSON=%s",
		pending.BookID, string(pending.AuthorsJSON), string(pending.SeriesJSON), string(pending.TagsJSON))

	err = database.SaveRescanPending(pending)
	if err != nil {
		logging.Error("Failed to save pending rescan: %v", err)
		return nil, err
	}

	// 10. Build and return preview
	preview := &models.RescanPreview{
		BookID:          bookID,
		Old:             oldValues,
		New:             newValues,
		Diff:            diff,
		PendingRescanID: pending.ID,
	}

	return preview, nil
}

// ApproveRescan applies pending rescan changes to main book table with selective field updates
func (s *RescanService) ApproveRescan(bookID int64, selectedFields *models.RescanApprovalRequest) (*models.RescanApprovalResponse, error) {
	// 1. Check if pending rescan exists
	pending, err := database.GetRescanPendingByBookID(bookID)
	if err != nil {
		logging.Error("Failed to get pending rescan: %v", err)
		return nil, err
	}
	if pending == nil {
		return nil, errors.New("no pending rescan found for this book")
	}

	// 2. Get book to build cover path
	book := &models.Book{}
	err = database.GetDB().Model(book).Where("id = ?", bookID).Select(book)
	if err != nil {
		logging.Error("Failed to get book for cover save: %v", err)
		return nil, errors.New("book not found")
	}

	// 3. Handle cover image based on selection
	coverFile := posters.FilePath(s.coversDir, book.Path, book.FileName)
	shouldUpdateCover := models.ShouldUpdate(getSelectedFieldFlag(selectedFields, "cover"))

	if shouldUpdateCover {
		if pending.CoverUpdated {
			if len(pending.CoverData) == 0 {
				return nil, errors.New("pending rescan has no cover data")
			}
			if err := os.MkdirAll(filepath.Dir(coverFile), 0755); err != nil {
				logging.Error("Failed to create cover directory: %v", err)
				return nil, fmt.Errorf("failed to create cover directory: %w", err)
			}
			if err := os.WriteFile(coverFile, pending.CoverData, 0644); err != nil {
				logging.Error("Failed to write cover file: %v", err)
				return nil, fmt.Errorf("failed to write cover file: %w", err)
			}
		} else {
			if err := os.Remove(coverFile); err != nil && !os.IsNotExist(err) {
				logging.Error("Failed to remove cover file: %v", err)
				return nil, fmt.Errorf("failed to remove cover file: %w", err)
			}
		}
	}

	// 4. Apply changes via database transaction with selective fields
	updatedFields, skippedFields, err := database.ApplySelectiveRescanChanges(bookID, selectedFields)
	if err != nil {
		logging.Error("Failed to apply rescan changes: %v", err)
		return nil, fmt.Errorf("failed to apply changes: %w", err)
	}

	// 5. Return success response
	newValues := s.pendingToRescanValues(pending)

	response := &models.RescanApprovalResponse{
		Success:       true,
		Message:       "Book rescan approved and applied",
		BookID:        bookID,
		Action:        "approve",
		Updated:       newValues,
		UpdatedFields: updatedFields,
		SkippedFields: skippedFields,
	}

	return response, nil
}

// getSelectedFieldFlag returns the field flag from selectedFields
func getSelectedFieldFlag(selectedFields *models.RescanApprovalRequest, field string) *bool {
	if selectedFields == nil {
		return nil // Default: update all
	}
	switch field {
	case "cover":
		return selectedFields.UpdateCover
	default:
		return nil
	}
}

// RejectRescan removes pending rescan without applying changes
func (s *RescanService) RejectRescan(bookID int64) (*models.RescanApprovalResponse, error) {
	err := database.DeleteRescanPending(bookID)
	if err != nil {
		logging.Error("Failed to delete pending rescan: %v", err)
		return nil, err
	}

	response := &models.RescanApprovalResponse{
		Success: true,
		Message: "Book rescan rejected",
		BookID:  bookID,
		Action:  "reject",
	}

	return response, nil
}

// Helper methods

func (s *RescanService) extractFB2FromArchive(archivePath, fileName string) ([]byte, error) {
	// First, try to open as ZIP archive
	zr, err := zip.OpenReader(archivePath)
	if err == nil {
		// Successfully opened as ZIP, try to find the file inside
		defer zr.Close()

		for _, file := range zr.File {
			if file.Name == fileName {
				rc, err := file.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()

				// Read file content
				content, err := io.ReadAll(rc)
				if err != nil {
					return nil, err
				}

				return content, nil
			}
		}
		return nil, fmt.Errorf("file not found in archive: %s", fileName)
	}

	// If not a valid ZIP, try to read as direct FB2 file
	// This handles cases where the path points directly to an FB2 file
	content, err := os.ReadFile(archivePath)
	if err == nil {
		return content, nil
	}

	return nil, fmt.Errorf("could not open as ZIP or read as file: %w", err)
}

// bookToRescanValues converts existing Book model to rescan old values
func (s *RescanService) bookToRescanValues(book *models.Book, serNo int64) *models.BookRescanOldValues {
	// Get authors
	authors := make([]models.RescanAuthor, len(book.Authors))
	for i, author := range book.Authors {
		authors[i] = models.RescanAuthor{
			ID:   author.ID,
			Name: author.FullName,
		}
	}

	// Get series with index
	var series *models.RescanSeries
	if len(book.Series) > 0 && book.Series[0] != nil {
		series = &models.RescanSeries{
			ID:    book.Series[0].ID,
			Title: book.Series[0].Ser,
		}
		// Set Index from database if series number exists
		if serNo > 0 {
			series.Index = fmt.Sprintf("%d", serNo)
		}
	}

	return &models.BookRescanOldValues{
		Title:      book.Title,
		Annotation: book.Annotation,
		Lang:       book.Lang,
		DocDate:    book.DocDate,
		Authors:    authors,
		Series:     series,
		Tags:       []string{}, // Empty tags for old values since books don't store tags yet
		HasCover:   book.Cover,
	}
}

// parsedToRescanValues converts parsed FB2 book to new values
func (s *RescanService) parsedToRescanValues(book *parser.BookFile) *models.BookRescanNewValues {
	// Convert authors
	authors := make([]models.RescanAuthor, len(book.Authors))
	for i, author := range book.Authors {
		authors[i] = models.RescanAuthor{
			ID:   0, // New authors don't have ID yet
			Name: author.Name,
		}
	}

	// Convert series
	var series *models.RescanSeries
	if book.Series != nil {
		series = &models.RescanSeries{
			ID:    0, // New series don't have ID yet
			Title: book.Series.Title,
			Index: book.Series.Index,
		}
	}

	// Ensure tags is never nil
	tags := book.Tags
	if tags == nil {
		tags = []string{}
	}

	return &models.BookRescanNewValues{
		Title:      book.Title,
		Annotation: book.Annotation,
		Lang:       book.Language,
		DocDate:    book.DocDate,
		Authors:    authors,
		Series:     series,
		Tags:       tags,
		HasCover:   book.Cover != nil,
	}
}

// pendingToRescanValues converts pending rescan to new values for response
func (s *RescanService) pendingToRescanValues(pending *models.BookRescanPending) *models.BookRescanNewValues {
	// Ensure tags is never nil
	tags := pending.GetTags()
	if tags == nil {
		tags = []string{}
	}

	return &models.BookRescanNewValues{
		Title:      pending.Title,
		Annotation: pending.Annotation,
		Lang:       pending.Lang,
		DocDate:    pending.DocDate,
		Authors:    pending.GetAuthors(),
		Series:     pending.GetSeries(),
		Tags:       tags,
		HasCover:   pending.CoverUpdated,
	}
}

// calculateDiff returns list of fields that changed
func (s *RescanService) calculateDiff(old *models.BookRescanOldValues, new *models.BookRescanNewValues) []string {
	diff := make([]string, 0) // Initialize as empty slice instead of nil

	if old.Title != new.Title {
		diff = append(diff, "title")
	}
	if old.Annotation != new.Annotation {
		diff = append(diff, "annotation")
	}
	if old.Lang != new.Lang {
		diff = append(diff, "lang")
	}
	if old.DocDate != new.DocDate {
		diff = append(diff, "docdate")
	}
	if !authorsEqual(old.Authors, new.Authors) {
		diff = append(diff, "authors")
	}
	if !seriesEqual(old.Series, new.Series) {
		diff = append(diff, "series")
	}
	if old.HasCover != new.HasCover {
		diff = append(diff, "cover")
	}

	return diff
}

func authorsEqual(a, b []models.RescanAuthor) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name {
			return false
		}
	}
	return true
}

func seriesEqual(a, b *models.RescanSeries) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Title == b.Title && a.Index == b.Index
}
