package utils

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// CollectionManager struct for managing collections
type CollectionManager struct {
	CollectionID int64
	BasePath     string
}

// AddBookToCollection adds a book to the collection
func (cm *CollectionManager) AddBookToCollection(bookID, position int, reader io.Reader) error {
	collectionPath := filepath.Join(cm.BasePath, fmt.Sprintf("collection_%d", cm.CollectionID))
	if err := os.MkdirAll(collectionPath, 0755); err != nil {
		return fmt.Errorf("failed to create collection directory: %w", err)
	}

	formats := []string{"fb2", "epub", "mobi"}
	for _, format := range formats {
		fileName := fmt.Sprintf("%02d_%d.%s", position, bookID, format)
		filePath := filepath.Join(collectionPath, fileName)

		file, err := os.Create(filePath)
		if err != nil {
			return fmt.Errorf("failed to create file: %w", err)
		}
		defer file.Close()

		if _, err := io.Copy(file, reader); err != nil {
			return fmt.Errorf("failed to write file: %w", err)
		}
	}

	return nil
}

// RemoveBookFromCollection removes a book from the collection
func (cm *CollectionManager) RemoveBookFromCollection(bookID int, position int) error {
	collectionPath := filepath.Join(cm.BasePath, fmt.Sprintf("collection_%d", cm.CollectionID))
	formats := []string{"fb2", "epub", "mobi"}

	for _, format := range formats {
		fileName := fmt.Sprintf("%02d_%d.%s", position, bookID, format)
		filePath := filepath.Join(collectionPath, fileName)

		if err := os.Remove(filePath); err != nil {
			if os.IsNotExist(err) {
				continue // ignore if file does not exist
			}
			return fmt.Errorf("failed to remove file: %w", err)
		}
	}

	return nil
}

// RenumberBooksInCollection updates the positions of books in the collection
func (cm *CollectionManager) RenumberBooksInCollection(newPositions map[int]int) error {
	collectionPath := filepath.Join(cm.BasePath, fmt.Sprintf("collection_%d", cm.CollectionID))
	formats := []string{"fb2", "epub", "mobi"}

	for oldPosition, newPosition := range newPositions {
		for _, format := range formats {
			oldFileName := fmt.Sprintf("%02d_", oldPosition)
			newFileName := fmt.Sprintf("%02d_", newPosition)

			err := filepath.Walk(collectionPath, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}

				// If the file name starts with the old position, rename it to the new position
				if filepath.Base(path)[:len(oldFileName)] == oldFileName && filepath.Ext(path) == "."+format {
					newPath := filepath.Join(collectionPath, newFileName+filepath.Base(path)[len(oldFileName):])
					if err := os.Rename(path, newPath); err != nil {
						return fmt.Errorf("failed to rename file %s to %s: %w", path, newPath, err)
					}
				}

				return nil
			})

			if err != nil {
				return err
			}
		}
	}

	return nil
}

// DeleteCollection removes the collection directory
func (cm *CollectionManager) DeleteCollection() error {
	collectionPath := filepath.Join(cm.BasePath, fmt.Sprintf("collection_%d", cm.CollectionID))
	if err := os.RemoveAll(collectionPath); err != nil {
		return fmt.Errorf("failed to delete collection: %w", err)
	}
	return nil
}
