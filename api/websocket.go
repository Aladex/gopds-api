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

	c.Header("Content-Disposition", fmt.Sprintf(contentDisp, book.DownloadName(), "mobi")) // Set the Content-Disposition header.
	c.Header("Content-Type", "application/x-mobipocket-ebook")                             // Set the Content-Type header to the mobi format.

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

var notificationChannel = make(chan string)

func notifyClientBookReady(bookID int64) {
	logrus.Infof("Notifying client that book %d is ready", bookID)
	if ch, ok := readyChannels.Load(bookID); ok {
		// Close the channel to notify the client that the book is ready
		close(ch.(chan struct{}))
		readyChannels.Delete(bookID)
		notificationChannel <- fmt.Sprintf("%d", bookID) // Send the book ID to the notification channel
		logrus.Infof("Book %d notification sent to notificationChannel", bookID)
	} else {
		logrus.Warnf("Notification channel for book %d not found in readyChannels", bookID)
	}
}

func WebsocketHandler(c *gin.Context) {
	conn, _, _, err := ws.UpgradeHTTP(c.Request, c.Writer)
	if err != nil {
		logrus.Error("Failed to upgrade to WebSocket:", err)
		return
	}
	defer conn.Close()

	quit := make(chan struct{})

	go func() {
		// Read from the WebSocket connection to keep it alive
		for {
			if _, _, err := wsutil.ReadClientData(conn); err != nil {
				logrus.Warn("WebSocket connection closed by client.")
				close(quit)
				return
			}
		}
	}()

	for {
		select {
		case bookID := <-notificationChannel: // Wait for a book to be ready
			logrus.Infof("Notifying client about ready book: %s", bookID)
			if err := wsutil.WriteServerMessage(conn, ws.OpText, []byte(bookID)); err != nil {
				logrus.Warn("Error writing to WebSocket:", err)
				close(quit)
				return
			}
		case <-quit:
			logrus.Info("Connection closed by client request.")
			return
		}
	}
}
