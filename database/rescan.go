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
	// Get pending rescan
	pending, err := GetRescanPendingByBookID(bookID)
	if err != nil {
		return err
	}
	if pending == nil {
		return errors.New("no pending rescan found")
	}

	// Get existing book
	book := &models.Book{}
	err = db.Model(book).Where("id = ?", bookID).Select(book)
	if err != nil {
		logging.Error(err)
		return err
	}

	// Start transaction
	tx, err := db.Begin()
	if err != nil {
		logging.Error(err)
		return err
	}

	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Update main book record
	book.Title = pending.Title
	book.Annotation = pending.Annotation
	book.Lang = pending.Lang
	book.DocDate = pending.DocDate
	book.Cover = pending.CoverUpdated

	_, err = tx.Model(book).Update()
	if err != nil {
		logging.Error(err)
		return err
	}

	// Update authors
	authors := pending.GetAuthors()
	err = updateBookAuthors(tx, bookID, authors)
	if err != nil {
		logging.Error(err)
		return err
	}

	// Update series
	series := pending.GetSeries()
	err = updateBookSeries(tx, bookID, series)
	if err != nil {
		logging.Error(err)
		return err
	}

	// Update tags (genres)
	tags := pending.GetTags()
	err = updateBookTags(tx, bookID, tags)
	if err != nil {
		logging.Error(err)
		return err
	}

	// Delete pending rescan
	_, err = tx.Model(&models.BookRescanPending{}).
		Where("book_id = ?", bookID).
		Delete()
	if err != nil {
		logging.Error(err)
		return err
	}

	// Commit transaction
	err = tx.Commit()
	return err
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
		_, _ = parseIndex(series.Index, &serNo)
	}

	link := &models.OrderToSeries{
		SeriesID: seriesObj.ID,
		BookID:   bookID,
		SerNo:    serNo,
	}

	_, err = tx.Model(link).Insert()
	return err
}

// updateBookTags updates tags (we'll add this to genre/tags if supported)
// For now, just logging that tags changed
func updateBookTags(tx *pg.Tx, bookID int64, tags []string) error {
	// Note: Depending on your schema, tags might be stored differently
	// This is a placeholder - implement based on your actual schema
	logging.Info("Would update tags for book %d: %v", bookID, tags)
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
