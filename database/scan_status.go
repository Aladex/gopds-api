package database

import (
	"fmt"
	"time"

	"gopds-api/logging"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// GetUnscannedCatalogs returns a list of catalog names that have not been scanned yet
func GetUnscannedCatalogs() ([]string, error) {
	var catalogs []models.Catalog
	err := db.Model(&catalogs).
		Where("is_scanned = ?", false).
		Select()

	if err != nil {
		logging.Error(fmt.Sprintf("Failed to get unscanned catalogs: %v", err))
		return nil, err
	}

	catalogNames := make([]string, len(catalogs))
	for i, catalog := range catalogs {
		catalogNames[i] = catalog.CatName
	}

	logging.Debug(fmt.Sprintf("Found %d unscanned catalogs", len(catalogNames)))
	return catalogNames, nil
}

// GetAllCatalogs returns all catalogs from the database
func GetAllCatalogs() ([]models.Catalog, error) {
	var catalogs []models.Catalog
	err := db.Model(&catalogs).
		Order("cat_name ASC").
		Select()

	if err != nil {
		logging.Error(fmt.Sprintf("Failed to get all catalogs: %v", err))
		return nil, err
	}

	return catalogs, nil
}

// GetScanStatus returns the scan status for a specific archive by name
func GetScanStatus(archiveName string) (*models.Catalog, error) {
	catalog := &models.Catalog{}
	err := db.Model(catalog).
		Where("cat_name = ?", archiveName).
		Select()

	if err != nil {
		if err == pg.ErrNoRows {
			logging.Debug(fmt.Sprintf("Catalog not found: %s", archiveName))
			return nil, nil
		}
		logging.Error(fmt.Sprintf("Failed to get scan status for %s: %v", archiveName, err))
		return nil, err
	}

	return catalog, nil
}

// GetOrCreateCatalog gets an existing catalog or creates a new one if it doesn't exist
func GetOrCreateCatalog(archiveName string) (*models.Catalog, error) {
	catalog, err := GetScanStatus(archiveName)
	if err != nil {
		return nil, err
	}

	if catalog != nil {
		return catalog, nil
	}

	// Create new catalog entry
	newCatalog := &models.Catalog{
		CatName:     archiveName,
		IsScanned:   false,
		BooksCount:  0,
		ErrorsCount: 0,
	}

	_, err = db.Model(newCatalog).Insert()
	if err != nil {
		logging.Error(fmt.Sprintf("Failed to create catalog %s: %v", archiveName, err))
		return nil, err
	}

	logging.Info(fmt.Sprintf("Created new catalog entry: %s", archiveName))
	return newCatalog, nil
}

// MarkArchiveAsScanned marks an archive as scanned with the given book count and error count
func MarkArchiveAsScanned(archiveName string, booksCount, errorsCount int) error {
	now := time.Now()

	result, err := db.Model((*models.Catalog)(nil)).
		Set("is_scanned = ?", true).
		Set("scanned_at = ?", now).
		Set("books_count = ?", booksCount).
		Set("errors_count = ?", errorsCount).
		Where("cat_name = ?", archiveName).
		Update()

	if err != nil {
		logging.Error(fmt.Sprintf("Failed to mark archive %s as scanned: %v", archiveName, err))
		return err
	}

	if result.RowsAffected() == 0 {
		logging.Warn(fmt.Sprintf("No catalog found to update: %s", archiveName))
		return fmt.Errorf("catalog not found: %s", archiveName)
	}

	logging.Info(fmt.Sprintf("Marked archive %s as scanned (books: %d, errors: %d)",
		archiveName, booksCount, errorsCount))
	return nil
}

// MarkArchiveAsUnscanned marks an archive as unscanned (for re-scanning)
func MarkArchiveAsUnscanned(archiveName string) error {
	result, err := db.Model((*models.Catalog)(nil)).
		Set("is_scanned = ?", false).
		Set("scanned_at = ?", nil).
		Set("books_count = ?", 0).
		Set("errors_count = ?", 0).
		Where("cat_name = ?", archiveName).
		Update()

	if err != nil {
		logging.Error(fmt.Sprintf("Failed to mark archive %s as unscanned: %v", archiveName, err))
		return err
	}

	if result.RowsAffected() == 0 {
		logging.Warn(fmt.Sprintf("No catalog found to update: %s", archiveName))
		return fmt.Errorf("catalog not found: %s", archiveName)
	}

	logging.Info(fmt.Sprintf("Marked archive %s as unscanned for re-scan", archiveName))
	return nil
}

// UpdateScanProgress updates the book count and error count for an archive during scanning
func UpdateScanProgress(archiveName string, booksCount, errorsCount int) error {
	result, err := db.Model((*models.Catalog)(nil)).
		Set("books_count = ?", booksCount).
		Set("errors_count = ?", errorsCount).
		Where("cat_name = ?", archiveName).
		Update()

	if err != nil {
		logging.Error(fmt.Sprintf("Failed to update scan progress for %s: %v", archiveName, err))
		return err
	}

	if result.RowsAffected() == 0 {
		logging.Warn(fmt.Sprintf("No catalog found to update: %s", archiveName))
		return fmt.Errorf("catalog not found: %s", archiveName)
	}

	logging.Debug(fmt.Sprintf("Updated scan progress for %s (books: %d, errors: %d)",
		archiveName, booksCount, errorsCount))
	return nil
}

// DeleteBooksByArchive deletes books and related data for a specific archive.
func DeleteBooksByArchive(archiveName string) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		logging.Error(fmt.Sprintf("Failed to start delete transaction for %s: %v", archiveName, err))
		return 0, err
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && rollbackErr != pg.ErrTxDone {
			logging.Warn(fmt.Sprintf("Failed to rollback delete transaction: %v", rollbackErr))
		}
	}()

	deleteRelated := []string{
		"DELETE FROM favorite_books WHERE book_id IN (SELECT id FROM opds_catalog_book WHERE path = ?)",
		"DELETE FROM book_collection_books WHERE book_id IN (SELECT id FROM opds_catalog_book WHERE path = ?)",
		"DELETE FROM covers WHERE book_id IN (SELECT id FROM opds_catalog_book WHERE path = ?)",
		"DELETE FROM opds_catalog_bauthor WHERE book_id IN (SELECT id FROM opds_catalog_book WHERE path = ?)",
		"DELETE FROM opds_catalog_bseries WHERE book_id IN (SELECT id FROM opds_catalog_book WHERE path = ?)",
	}

	for _, query := range deleteRelated {
		if _, err := tx.Exec(query, archiveName); err != nil {
			logging.Error(fmt.Sprintf("Failed to delete related rows for %s: %v", archiveName, err))
			return 0, err
		}
	}

	result, err := tx.Exec("DELETE FROM opds_catalog_book WHERE path = ?", archiveName)
	if err != nil {
		logging.Error(fmt.Sprintf("Failed to delete books for %s: %v", archiveName, err))
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		logging.Error(fmt.Sprintf("Failed to commit delete transaction for %s: %v", archiveName, err))
		return 0, err
	}

	deleted := result.RowsAffected()
	logging.Info(fmt.Sprintf("Deleted %d books for archive %s", deleted, archiveName))
	return int(deleted), nil
}

// GetCatalogStats returns aggregated statistics across all catalogs
func GetCatalogStats() (map[string]interface{}, error) {
	var totalCatalogs, scannedCatalogs, totalBooks, totalErrors int

	// Count total catalogs
	totalCatalogs, err := db.Model((*models.Catalog)(nil)).Count()
	if err != nil {
		logging.Error(fmt.Sprintf("Failed to count total catalogs: %v", err))
		return nil, err
	}

	// Count scanned catalogs
	scannedCatalogs, err = db.Model((*models.Catalog)(nil)).
		Where("is_scanned = ?", true).
		Count()
	if err != nil {
		logging.Error(fmt.Sprintf("Failed to count scanned catalogs: %v", err))
		return nil, err
	}

	// Sum total books and errors
	type aggregateResult struct {
		TotalBooks  int `pg:"total_books"`
		TotalErrors int `pg:"total_errors"`
	}

	var result aggregateResult
	err = db.Model((*models.Catalog)(nil)).
		ColumnExpr("COALESCE(SUM(books_count), 0) AS total_books").
		ColumnExpr("COALESCE(SUM(errors_count), 0) AS total_errors").
		Select(&result)
	if err != nil {
		logging.Error(fmt.Sprintf("Failed to sum books and errors: %v", err))
		return nil, err
	}

	totalBooks = result.TotalBooks
	totalErrors = result.TotalErrors

	stats := map[string]interface{}{
		"total_catalogs":     totalCatalogs,
		"scanned_catalogs":   scannedCatalogs,
		"unscanned_catalogs": totalCatalogs - scannedCatalogs,
		"total_books":        totalBooks,
		"total_errors":       totalErrors,
	}

	return stats, nil
}
