package services

import (
	"archive/zip"
	"errors"
	"fmt"
	"gopds-api/database"
	"gopds-api/internal/parser"
	"gopds-api/logging"
	"gopds-api/models"
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
	parsedBook, err := fbzParser.Parse(fbzContent)
	if err != nil {
		logging.Error("Failed to parse FB2: %v", err)
		return nil, fmt.Errorf("failed to parse FB2 file: %w", err)
	}

	// 5. Get old values
	oldValues := s.bookToRescanValues(book)

	// 6. Get new values
	newValues := s.parsedToRescanValues(parsedBook)

	// 7. Calculate diff
	diff := s.calculateDiff(oldValues, newValues)

	// 8. Save to pending table
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

	err = database.SaveRescanPending(pending)
	if err != nil {
		logging.Error("Failed to save pending rescan: %v", err)
		return nil, err
	}

	// 9. Build and return preview
	preview := &models.RescanPreview{
		BookID:          bookID,
		Old:             oldValues,
		New:             newValues,
		Diff:            diff,
		PendingRescanID: pending.ID,
	}

	return preview, nil
}

// ApproveRescan applies pending rescan changes to main book table
func (s *RescanService) ApproveRescan(bookID int64) (*models.RescanApprovalResponse, error) {
	// 1. Check if pending rescan exists
	pending, err := database.GetRescanPendingByBookID(bookID)
	if err != nil {
		logging.Error("Failed to get pending rescan: %v", err)
		return nil, err
	}
	if pending == nil {
		return nil, errors.New("no pending rescan found for this book")
	}

	// 2. Save cover image if needed
	if pending.CoverUpdated && len(pending.CoverData) > 0 {
		// Get book to build cover path
		book := &models.Book{}
		err := database.GetDB().Model(book).Where("id = ?", bookID).Select(book)
		if err != nil {
			logging.Error("Failed to get book for cover save: %v", err)
			// Don't fail the whole operation if cover save fails
		} else {
			coverDir := filepath.Join(s.coversDir, strings.ReplaceAll(book.Path, ".", "-"))
			_ = os.MkdirAll(coverDir, 0755)

			coverFile := filepath.Join(coverDir,
				strings.ReplaceAll(book.FileName, ".", "-")+".jpg")
			_ = os.WriteFile(coverFile, pending.CoverData, 0644)
		}
	}

	// 3. Apply changes via database transaction
	err = database.ApplyRescanChanges(bookID)
	if err != nil {
		logging.Error("Failed to apply rescan changes: %v", err)
		return nil, fmt.Errorf("failed to apply changes: %w", err)
	}

	// 4. Return success response
	newValues := s.pendingToRescanValues(pending)

	response := &models.RescanApprovalResponse{
		Success: true,
		Message: "Book rescan approved and applied",
		BookID:  bookID,
		Action:  "approve",
		Updated: newValues,
	}

	return response, nil
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
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	// Find the file in the archive
	for _, file := range zr.File {
		if file.Name == fileName {
			rc, err := file.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()

			// Read file content
			buf := make([]byte, file.UncompressedSize)
			n, err := rc.Read(buf)
			if err != nil && err.Error() != "EOF" {
				return nil, err
			}

			return buf[:n], nil
		}
	}

	return nil, fmt.Errorf("file not found in archive: %s", fileName)
}

// bookToRescanValues converts existing Book model to rescan old values
func (s *RescanService) bookToRescanValues(book *models.Book) *models.BookRescanOldValues {
	// Get authors
	authors := make([]models.RescanAuthor, len(book.Authors))
	for i, author := range book.Authors {
		authors[i] = models.RescanAuthor{
			ID:   author.ID,
			Name: author.FullName,
		}
	}

	// Get series
	var series *models.RescanSeries
	if len(book.Series) > 0 && book.Series[0] != nil {
		series = &models.RescanSeries{
			ID:    book.Series[0].ID,
			Title: book.Series[0].Ser,
		}
	}

	return &models.BookRescanOldValues{
		Title:      book.Title,
		Annotation: book.Annotation,
		Lang:       book.Lang,
		DocDate:    book.DocDate,
		Authors:    authors,
		Series:     series,
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

	return &models.BookRescanNewValues{
		Title:      book.Title,
		Annotation: book.Annotation,
		Lang:       book.Language,
		DocDate:    book.DocDate,
		Authors:    authors,
		Series:     series,
		Tags:       book.Tags,
		HasCover:   book.Cover != nil,
	}
}

// pendingToRescanValues converts pending rescan to new values for response
func (s *RescanService) pendingToRescanValues(pending *models.BookRescanPending) *models.BookRescanNewValues {
	return &models.BookRescanNewValues{
		Title:      pending.Title,
		Annotation: pending.Annotation,
		Lang:       pending.Lang,
		DocDate:    pending.DocDate,
		Authors:    pending.GetAuthors(),
		Series:     pending.GetSeries(),
		Tags:       pending.GetTags(),
		HasCover:   pending.CoverUpdated,
	}
}

// calculateDiff returns list of fields that changed
func (s *RescanService) calculateDiff(old, new *models.BookRescanOldValues, newValues *models.BookRescanNewValues) []string {
	var diff []string

	if old.Title != newValues.Title {
		diff = append(diff, "title")
	}
	if old.Annotation != newValues.Annotation {
		diff = append(diff, "annotation")
	}
	if old.Lang != newValues.Lang {
		diff = append(diff, "lang")
	}
	if old.DocDate != newValues.DocDate {
		diff = append(diff, "docdate")
	}
	if !authorsEqual(old.Authors, newValues.Authors) {
		diff = append(diff, "authors")
	}
	if !seriesEqual(old.Series, newValues.Series) {
		diff = append(diff, "series")
	}
	if old.HasCover != newValues.HasCover {
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
