package api

import (
	"fmt"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/logging"
	"gopds-api/utils"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// ConvertedFile holds an in-memory converted file ready for download.
type ConvertedFile struct {
	Data        []byte
	Filename    string
	ContentType string
}

// epubStore is an in-memory store for converted EPUB files keyed by bookID.
var epubStore sync.Map // key: int64 (bookID), value: *ConvertedFile

func ConvertBookToMobi(bookID int64) error {
	book, err := database.GetBook(bookID) // Retrieve the book details from the database
	if err != nil {
		return err
	}
	zipPath := viper.GetString("app.files_path") + book.Path // Construct the path to the book file.
	mobiConversionDir := viper.GetString("app.mobi_conversion_dir")

	if !utils.FileExists(zipPath) {
		return fmt.Errorf("book file not found: %s", zipPath)
	}

	bp := utils.NewBookProcessor(book.FileName, zipPath) // Create a new BookProcessor for the book file.
	var rc io.ReadCloser
	rc, err = bp.Mobi()
	if err != nil {
		return err
	}
	defer rc.Close()

	filePath := filepath.Join(mobiConversionDir, fmt.Sprintf("%d.mobi", bookID))
	logging.Info("Creating mobi file:", filePath)
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err = io.Copy(file, rc); err != nil {
		return err
	}
	// Send the file to the client
	done := make(chan struct{})
	readyChannels.Store(bookID, done)

	// Delete the file after it has been sent
	logging.Infof("Book %d converted and stored at %s", bookID, filePath)
	return nil
}

func ConvertBookToEpub(bookID int64) error {
	book, err := database.GetBook(bookID)
	if err != nil {
		return err
	}
	zipPath := viper.GetString("app.files_path") + book.Path

	if !utils.FileExists(zipPath) {
		return fmt.Errorf("book file not found: %s", zipPath)
	}

	bp := utils.NewBookProcessor(book.FileName, zipPath)
	rc, err := bp.Epub()
	if err != nil {
		return err
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return err
	}

	epubStore.Store(bookID, &ConvertedFile{
		Data:        data,
		Filename:    book.DownloadName() + ".epub",
		ContentType: "application/epub+zip",
	})

	logging.Infof("Book %d converted to EPUB and stored in memory (%d bytes)", bookID, len(data))
	return nil
}

// deleteFile deletes the file at the specified path
func deleteFile(filePath string) error {
	err := os.Remove(filePath)
	if err != nil {
		logging.Errorf("Failed to delete file %s: %v", filePath, err)
	}
	return err
}

func DownloadConvertedBook(c *gin.Context) {
	bookID := c.Param("id")
	mobiConversionDir := viper.GetString("app.mobi_conversion_dir")
	filePath := filepath.Join(mobiConversionDir, fmt.Sprintf("%s.mobi", bookID)) // Construct the path to the mobi file
	contentDisp := "attachment; filename=%s.%s"

	// Check if the file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "File not found"})
		return
	}
	// Convert book ID to int64
	bookIDInt, err := strconv.ParseInt(bookID, 10, 64)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, fmt.Errorf("invalid book ID: %v", err))
		return
	}
	book, err := database.GetBook(bookIDInt) // Retrieve the book details from the database.
	if err != nil {
		httputil.NewError(c, http.StatusNotFound, err) // Send a 404 Not Found if the book is not in the database.
		return
	}

	c.Header("Content-Disposition", fmt.Sprintf(contentDisp, book.DownloadName(), "mobi")) // Set the Content-Disposition header.
	c.Header("Content-Type", "application/x-mobipocket-ebook")                             // Set the Content-Type header to the mobi format.

	// Send the file to the client
	c.File(filePath)

	// Delete the file after it has been sent
	go func() {
		err := deleteFile(filePath)
		if err != nil {
			logging.Errorf("Failed to delete mobi file: %v", err)
		}
	}()
}

// HeadConvertedBook handles HEAD requests for converted MOBI files.
func HeadConvertedBook(c *gin.Context) {
	bookID := c.Param("id")
	mobiConversionDir := viper.GetString("app.mobi_conversion_dir")
	filePath := filepath.Join(mobiConversionDir, fmt.Sprintf("%s.mobi", bookID))

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "File not found"})
		return
	}

	bookIDInt, err := strconv.ParseInt(bookID, 10, 64)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, fmt.Errorf("invalid book ID: %v", err))
		return
	}
	if _, err := database.GetBook(bookIDInt); err != nil {
		httputil.NewError(c, http.StatusNotFound, err)
		return
	}

	c.Header("Content-Type", "application/x-mobipocket-ebook")
	c.Status(http.StatusOK)
}
