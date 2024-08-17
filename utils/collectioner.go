package utils

import (
	"fmt"
	"github.com/spf13/viper"
	"gopds-api/models"
	"io"
	"os"
	"path/filepath"
)

// CollectionManager struct for managing collections
type CollectionManager struct {
	BasePath string
}

// UpdateBookCollection updates the collection based on the provided list of BookCollectionBook
// UpdateBookCollection updates the collection based on the provided list of BookCollectionBook
func (cm *CollectionManager) UpdateBookCollection(collectionID int64, books []models.Book) error {
	collectionPath := filepath.Join(cm.BasePath, fmt.Sprintf("collection_%d", collectionID))
	if err := os.MkdirAll(collectionPath, 0755); err != nil {
		return fmt.Errorf("failed to create collection directory: %w", err)
	}

	existingFiles := make(map[string]bool)
	for _, book := range books {
		zipPath := viper.GetString("app.files_path") + book.Path // Construct the path to the book file.

		if !FileExists(zipPath) {
			return fmt.Errorf("book file not found for book ID: %d", book.ID)
		}

		bp := NewBookProcessor(book.FileName, zipPath)

		// Process and save the book in different formats
		formats := []string{"fb2", "epub", "mobi"}
		for _, format := range formats {
			var bookContent io.ReadCloser
			var err error

			switch format {
			case "fb2":
				bookContent, err = bp.FB2()
			case "epub":
				bookContent, err = bp.Epub()
			case "mobi":
				bookContent, err = bp.Mobi()
			}

			if err != nil {
				return fmt.Errorf("failed to process book to %s format: %w", format, err)
			}
			defer bookContent.Close()

			fileName := fmt.Sprintf("%02d_%d_%s.%s", book.Position, book.ID, book.DownloadName(), format)
			filePath := filepath.Join(collectionPath, fileName)

			file, err := os.Create(filePath)
			if err != nil {
				return fmt.Errorf("failed to create file: %w", err)
			}
			defer file.Close()

			if _, err := io.Copy(file, bookContent); err != nil {
				return fmt.Errorf("failed to write file: %w", err)
			}

			// Add the file to the list of existing files
			existingFiles[fileName] = true
		}
	}

	// Remove files that are no longer part of the collection
	err := filepath.Walk(collectionPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() {
			fileName := filepath.Base(path)
			if !existingFiles[fileName] {
				if err := os.Remove(path); err != nil {
					return fmt.Errorf("failed to remove file: %w", err)
				}
			}
		}

		return nil
	})

	if err != nil {
		return err
	}

	return nil
}

// DeleteCollection removes the collection directory
func (cm *CollectionManager) DeleteCollection(collectionID int64) error {
	collectionPath := filepath.Join(cm.BasePath, fmt.Sprintf("collection_%d", collectionID))
	if err := os.RemoveAll(collectionPath); err != nil {
		return fmt.Errorf("failed to delete collection: %w", err)
	}
	return nil
}
