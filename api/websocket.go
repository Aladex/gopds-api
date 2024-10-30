package api

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"gopds-api/database"
	"gopds-api/httputil"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

// deleteFile deletes the file at the specified path
func deleteFile(filePath string) error {
	err := os.Remove(filePath)
	if err != nil {
		logrus.Errorf("Failed to delete file %s: %v", filePath, err)
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

	c.Header("Content-Disposition", fmt.Sprintf(contentDisp, book.DownloadName(), c.Param("format"))) // Set the Content-Disposition header.
	c.Header("Content-Type", "application/x-mobipocket-ebook")                                        // Set the Content-Type header to the mobi format.

	// Send the file to the client
	c.File(filePath)

	// Delete the file after it has been sent
	go func() {
		err := deleteFile(filePath)
		if err != nil {
			logrus.Errorf("Failed to delete mobi file: %v", err)
		}
	}()
}

func notifyClientBookReady(bookID int64) {
	// Get the channel for the bookID
	if ch, ok := readyChannels.Load(bookID); ok {
		close(ch.(chan struct{}))
		readyChannels.Delete(bookID) // Remove the channel from the map
	}
}

func readyChannel(bookID string) chan struct{} {
	// Try to get the channel from the map
	if ch, ok := readyChannels.Load(bookID); ok {
		return ch.(chan struct{})
	}

	// Create a new channel and store it in the map
	ch := make(chan struct{})
	readyChannels.Store(bookID, ch)
	return ch
}

func WebsocketHandler(c *gin.Context) {
	conn, _, _, err := ws.UpgradeHTTP(c.Request, c.Writer)
	if err != nil {
		logrus.Error("Failed to upgrade to WebSocket:", err)
		return
	}
	defer conn.Close()

	bookID := c.Query("bookID") // Get the book ID from the query parameters

	for {
		select {
		case <-readyChannel(bookID): // Wait for the book to be ready
			err = wsutil.WriteServerMessage(conn, ws.OpText, []byte(bookID))
			if err != nil {
				logrus.Error("Failed to write WebSocket message:", err)
				// Delete book file if the message could not be sent
				go func() {
					err := deleteFile(filepath.Join(viper.GetString("app.mobi_conversion_dir"), fmt.Sprintf("%s.mobi", bookID)))
					if err != nil {
						logrus.Errorf("Failed to delete mobi file: %v", err)
					}
				}()
				return
			}
		}
	}
}