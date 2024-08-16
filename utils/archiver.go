package utils

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// CollectionArchiver struct for archiving collections
type CollectionArchiver struct {
	CollectionID int64
	BasePath     string
}

// AddBookToArchive adds a book to the collection archive
func (ca *CollectionArchiver) AddBookToArchive(filename, archivePath, format string, position int) error {
	bp := NewBookProcessor(filename, archivePath)

	var reader io.ReadCloser
	var err error

	switch format {
	case "fb2":
		reader, err = bp.FB2()
	case "epub":
		reader, err = bp.Epub()
	case "mobi":
		reader, err = bp.Mobi()
	default:
		return fmt.Errorf("unknown book format: %s", format)
	}

	if err != nil {
		return err
	}
	defer reader.Close()

	return ca.addToArchiveFromReader(reader, format, position)
}

// addToArchiveFromReader adds a book to the collection archive from a reader
func (ca *CollectionArchiver) addToArchiveFromReader(reader io.Reader, format string, position int) error {
	archivePath := filepath.Join(ca.BasePath, fmt.Sprintf("collection_%d.%s.zip", ca.CollectionID, format))

	// Create a temporary buffer to store the new archive
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)

	// Open the existing archive for reading, if it exists
	existingArchive, err := zip.OpenReader(archivePath)
	if err == nil {
		defer existingArchive.Close()

		// Copy existing files from the old archive to the new one
		for _, f := range existingArchive.File {
			fw, err := w.Create(f.Name)
			if err != nil {
				return err
			}
			fr, err := f.Open()
			if err != nil {
				return err
			}
			if _, err = io.Copy(fw, fr); err != nil {
				fr.Close()
				return err
			}
			fr.Close()
		}
	}

	// Add the new book to the archive
	archiveFilename := fmt.Sprintf("%d_book.%s", position, format)
	fw, err := w.Create(archiveFilename)
	if err != nil {
		return err
	}

	if _, err = io.Copy(fw, reader); err != nil {
		return err
	}

	// Close the archive writer
	w.Close()

	// Write the new archive to the file, overwriting the old one
	if err := os.WriteFile(archivePath, buf.Bytes(), 0755); err != nil {
		return err
	}

	return nil
}

// RemoveBookFromArchive removes a book from the collection archive
func (ca *CollectionArchiver) RemoveBookFromArchive(position int, format string) error {
	archivePath := filepath.Join(ca.BasePath, fmt.Sprintf("collection_%d.%s.zip", ca.CollectionID, format))

	// Open the existing archive for reading
	existingArchive, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer existingArchive.Close()

	// Create a temporary buffer to store the new archive
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)

	targetFilename := fmt.Sprintf("%d_", position)
	for _, f := range existingArchive.File {
		// Skip the target file
		if filepath.Base(f.Name) == targetFilename {
			continue
		}

		fw, err := w.Create(f.Name)
		if err != nil {
			return err
		}
		fr, err := f.Open()
		if err != nil {
			return err
		}
		if _, err = io.Copy(fw, fr); err != nil {
			fr.Close()
			return err
		}
		fr.Close()
	}

	// Close the archive writer
	w.Close()

	// Write the new archive to the file, overwriting the old one
	if err := os.WriteFile(archivePath, buf.Bytes(), 0755); err != nil {
		return err
	}

	return nil
}

// RenumberBooksInArchive renumbers the books in the collection archive
func (ca *CollectionArchiver) RenumberBooksInArchive(newPositions map[int]int, format string) error {
	archivePath := filepath.Join(ca.BasePath, fmt.Sprintf("collection_%d.%s.zip", ca.CollectionID, format))

	// Open the existing archive for reading
	existingArchive, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer existingArchive.Close()

	// Create a temporary buffer to store the new archive
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)

	for _, f := range existingArchive.File {
		oldPosition := getPositionFromFilename(f.Name)
		newPosition, exists := newPositions[oldPosition]
		if !exists {
			newPosition = oldPosition
		}

		newFilename := fmt.Sprintf("%d_%s", newPosition, getFilenameWithoutPosition(f.Name))
		fw, err := w.Create(newFilename)
		if err != nil {
			return err
		}
		fr, err := f.Open()
		if err != nil {
			return err
		}
		if _, err = io.Copy(fw, fr); err != nil {
			fr.Close()
			return err
		}
		fr.Close()
	}

	// Close the archive writer
	w.Close()

	// Write the new archive to the file, overwriting the old one
	if err := os.WriteFile(archivePath, buf.Bytes(), 0755); err != nil {
		return err
	}

	return nil
}

// DeleteArchives deletes the collection archives
func (ca *CollectionArchiver) DeleteArchives() error {
	formats := []string{"fb2", "epub", "mobi"}
	for _, format := range formats {
		archivePath := filepath.Join(ca.BasePath, fmt.Sprintf("collection_%d.%s.zip", ca.CollectionID, format))
		if err := os.Remove(archivePath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to delete archive %s: %w", archivePath, err)
		}
	}
	return nil
}

// getPositionFromFilename gets the position from the filename
func getPositionFromFilename(filename string) int {
	var position int
	fmt.Sscanf(filename, "%d_", &position)
	return position
}

// getFilenameWithoutPosition gets the filename without the position
func getFilenameWithoutPosition(filename string) string {
	var position int
	_, name := filepath.Split(filename)
	fmt.Sscanf(name, "%d_%s", &position, &name)
	return name
}
