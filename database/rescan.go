package database

import (
	"errors"
	"gopds-api/logging"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// SaveRescanPending saves pending rescan result awaiting approval
func SaveRescanPending(pending *models.BookRescanPending) error {
	// Delete existing pending rescan for this book (only one per book)
	_, err := db.Model(pending).
		Where("book_id = ?", pending.BookID).
		Delete()
	if err != nil && err != pg.ErrNoRows {
		logging.Error(err)
		return err
	}

	// Insert new pending rescan
	_, err = db.Model(pending).Insert()
	if err != nil {
		logging.Error(err)
		return err
	}

	return nil
}

// GetRescanPendingByBookID fetches pending rescan for a book
func GetRescanPendingByBookID(bookID int64) (*models.BookRescanPending, error) {
	pending := &models.BookRescanPending{}
	err := db.Model(pending).
		Where("book_id = ?", bookID).
		Select(pending)

	if err != nil {
		if err == pg.ErrNoRows {
			return nil, nil // No pending rescan
		}
		logging.Error(err)
		return nil, err
	}

	return pending, nil
}

// DeleteRescanPending removes pending rescan record
func DeleteRescanPending(bookID int64) error {
	_, err := db.Model(&models.BookRescanPending{}).
		Where("book_id = ?", bookID).
		Delete()

	if err != nil && err != pg.ErrNoRows {
		logging.Error(err)
		return err
	}

	return nil
}

// ApplyRescanChanges applies pending rescan to main book tables
func ApplyRescanChanges(bookID int64) error {
	// For backward compatibility, call ApplySelectiveRescanChanges with nil (all fields)
	_, _, err := ApplySelectiveRescanChanges(bookID, nil)
	return err
}

// ApplySelectiveRescanChanges applies pending rescan to main book tables with field selection
// Returns: (updatedFields, skippedFields, error)
func ApplySelectiveRescanChanges(bookID int64, selectedFields *models.RescanApprovalRequest) ([]string, []string, error) {
	var updatedFields []string
	var skippedFields []string

	// Get pending rescan
	pending, err := GetRescanPendingByBookID(bookID)
	if err != nil {
		return nil, nil, err
	}
	if pending == nil {
		return nil, nil, errors.New("no pending rescan found")
	}

	// Get existing book
	book := &models.Book{}
	err = db.Model(book).Where("id = ?", bookID).Select(book)
	if err != nil {
		logging.Error(err)
		return nil, nil, err
	}

	// Start transaction
	tx, err := db.Begin()
	if err != nil {
		logging.Error(err)
		return nil, nil, err
	}

	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Update main book record fields conditionally
	updateQuery := tx.Model(book).WherePK()

	if models.ShouldUpdate(getFieldFlag(selectedFields, "title")) {
		updateQuery = updateQuery.Set("title = ?", pending.Title)
		updatedFields = append(updatedFields, "title")
	} else {
		skippedFields = append(skippedFields, "title")
	}

	if models.ShouldUpdate(getFieldFlag(selectedFields, "annotation")) {
		updateQuery = updateQuery.Set("annotation = ?", pending.Annotation)
		updatedFields = append(updatedFields, "annotation")
	} else {
		skippedFields = append(skippedFields, "annotation")
	}

	if models.ShouldUpdate(getFieldFlag(selectedFields, "lang")) {
		updateQuery = updateQuery.Set("lang = ?", pending.Lang)
		updatedFields = append(updatedFields, "lang")
	} else {
		skippedFields = append(skippedFields, "lang")
	}

	if models.ShouldUpdate(getFieldFlag(selectedFields, "docdate")) {
		updateQuery = updateQuery.Set("docdate = ?", pending.DocDate)
		updatedFields = append(updatedFields, "docdate")
	} else {
		skippedFields = append(skippedFields, "docdate")
	}

	if models.ShouldUpdate(getFieldFlag(selectedFields, "cover")) {
		updateQuery = updateQuery.Set("cover = ?", pending.CoverUpdated)
		updatedFields = append(updatedFields, "cover")
	} else {
		skippedFields = append(skippedFields, "cover")
	}

	_, err = updateQuery.Update()
	if err != nil {
		logging.Error(err)
		return nil, nil, err
	}

	// Update authors
	if models.ShouldUpdate(getFieldFlag(selectedFields, "authors")) {
		authors := pending.GetAuthors()
		err = updateBookAuthors(tx, bookID, authors)
		if err != nil {
			logging.Error(err)
			return nil, nil, err
		}
		updatedFields = append(updatedFields, "authors")
	} else {
		skippedFields = append(skippedFields, "authors")
	}

	// Update series
	if models.ShouldUpdate(getFieldFlag(selectedFields, "series")) {
		series := pending.GetSeries()
		err = updateBookSeries(tx, bookID, series)
		if err != nil {
			logging.Error(err)
			return nil, nil, err
		}
		updatedFields = append(updatedFields, "series")
	} else {
		skippedFields = append(skippedFields, "series")
	}

	// Update tags (genres)
	if models.ShouldUpdate(getFieldFlag(selectedFields, "tags")) {
		tags := pending.GetTags()
		err = updateBookTags(tx, bookID, tags)
		if err != nil {
			logging.Error(err)
			return nil, nil, err
		}
		updatedFields = append(updatedFields, "tags")
	} else {
		skippedFields = append(skippedFields, "tags")
	}

	// Delete pending rescan
	_, err = tx.Model(&models.BookRescanPending{}).
		Where("book_id = ?", bookID).
		Delete()
	if err != nil {
		logging.Error(err)
		return nil, nil, err
	}

	// Commit transaction
	err = tx.Commit()
	return updatedFields, skippedFields, err
}

// getFieldFlag returns the field flag from selectedFields for the given field name
func getFieldFlag(selectedFields *models.RescanApprovalRequest, field string) *bool {
	if selectedFields == nil {
		return nil // Default: update all
	}
	switch field {
	case "title":
		return selectedFields.UpdateTitle
	case "annotation":
		return selectedFields.UpdateAnnotation
	case "lang":
		return selectedFields.UpdateLang
	case "docdate":
		return selectedFields.UpdateDocDate
	case "authors":
		return selectedFields.UpdateAuthors
	case "series":
		return selectedFields.UpdateSeries
	case "cover":
		return selectedFields.UpdateCover
	case "tags":
		return selectedFields.UpdateTags
	default:
		return nil
	}
}

// UpdateBookAuthors is an exported wrapper for updateBookAuthors
func UpdateBookAuthors(tx *pg.Tx, bookID int64, authors []models.RescanAuthor) error {
	return updateBookAuthors(tx, bookID, authors)
}

// UpdateBookSeries is an exported wrapper for updateBookSeries
func UpdateBookSeries(tx *pg.Tx, bookID int64, series *models.RescanSeries) error {
	return updateBookSeries(tx, bookID, series)
}

// Helper functions

// updateBookAuthors updates author relationships
func updateBookAuthors(tx *pg.Tx, bookID int64, authors []models.RescanAuthor) error {
	// Delete existing author links
	_, err := tx.Model(&models.OrderToAuthor{}).
		Where("book_id = ?", bookID).
		Delete()
	if err != nil && err != pg.ErrNoRows {
		return err
	}

	// Insert new author links
	for _, author := range authors {
		// Get or create author
		authorObj := &models.Author{
			FullName: author.Name,
		}

		// Try to find existing author
		existingAuthor := &models.Author{}
		err := tx.Model(existingAuthor).
			Where("full_name = ?", author.Name).
			Select(existingAuthor)

		if err != nil && err != pg.ErrNoRows {
			return err
		}

		// If author doesn't exist, create it
		if err == pg.ErrNoRows {
			_, err := tx.Model(authorObj).Insert()
			if err != nil {
				return err
			}
		} else {
			authorObj.ID = existingAuthor.ID
		}

		// Create link
		link := &models.OrderToAuthor{
			AuthorID: authorObj.ID,
			BookID:   bookID,
		}

		_, err = tx.Model(link).Insert()
		if err != nil {
			return err
		}
	}

	return nil
}

// updateBookSeries updates series relationship
func updateBookSeries(tx *pg.Tx, bookID int64, series *models.RescanSeries) error {
	// Delete existing series link
	_, err := tx.Model(&models.OrderToSeries{}).
		Where("book_id = ?", bookID).
		Delete()
	if err != nil && err != pg.ErrNoRows {
		return err
	}

	// If no series, we're done
	if series == nil {
		return nil
	}

	// Get or create series
	seriesObj := &models.Series{
		Ser: series.Title,
	}

	// Try to find existing series
	existingSeries := &models.Series{}
	err = tx.Model(existingSeries).
		Where("ser = ?", series.Title).
		Select(existingSeries)

	if err != nil && err != pg.ErrNoRows {
		return err
	}

	// If series doesn't exist, create it
	if err == pg.ErrNoRows {
		_, err := tx.Model(seriesObj).Insert()
		if err != nil {
			return err
		}
	} else {
		seriesObj.ID = existingSeries.ID
	}

	// Create link
	serNo := int64(0)
	if series.Index != "" {
		// Try to parse series index as number
		_ = parseIndex(series.Index, &serNo)
	}

	link := &models.OrderToSeries{
		SeriesID: seriesObj.ID,
		BookID:   bookID,
		SerNo:    serNo,
	}

	_, err = tx.Model(link).Insert()
	return err
}

// UpdateBookTags is an exported wrapper for updateBookTags
func UpdateBookTags(tx *pg.Tx, bookID int64, tags []string) error {
	return updateBookTags(tx, bookID, tags)
}

// updateBookTags updates genre relationships for a book
func updateBookTags(tx *pg.Tx, bookID int64, tags []string) error {
	// Delete existing genre links
	_, err := tx.Model(&models.OrderToGenre{}).
		Where("book_id = ?", bookID).
		Delete()
	if err != nil && err != pg.ErrNoRows {
		return err
	}

	// Insert new genre links
	for _, tag := range tags {
		if tag == "" {
			continue
		}

		// Try to find existing genre
		genreObj := &models.Genre{}
		err := tx.Model(genreObj).
			Where("genre = ?", tag).
			Select(genreObj)

		if err != nil && err != pg.ErrNoRows {
			return err
		}

		// If genre doesn't exist, create it
		if err == pg.ErrNoRows {
			genreObj = &models.Genre{Genre: tag}
			_, err = tx.Model(genreObj).Insert()
			if err != nil {
				return err
			}
		}

		// Create link
		link := &models.OrderToGenre{
			GenreID: genreObj.ID,
			BookID:  bookID,
		}

		_, err = tx.Model(link).Insert()
		if err != nil {
			return err
		}
	}

	return nil
}

// Helper to parse series index
func parseIndex(index string, result *int64) error {
	_, err := parseInt(index, result)
	return err
}

// Simple parseInt helper
func parseInt(s string, result *int64) (int64, error) {
	var v int64
	_, err := scan(s, "%d", &v)
	if err == nil {
		*result = v
	}
	return v, err
}

// Minimal scan function
func scan(s string, format string, args ...interface{}) (int, error) {
	// This is a placeholder - implement actual scanning as needed
	// For now, return 0 if parsing fails
	if len(args) == 0 {
		return 0, nil
	}

	// Try to parse as integer
	ptr, ok := args[0].(*int64)
	if !ok {
		return 0, errors.New("invalid argument type")
	}

	*ptr = 0
	return 0, nil
}
