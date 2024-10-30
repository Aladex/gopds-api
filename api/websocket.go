package api

import (
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"gopds-api/database"
	"gopds-api/httputil"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
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

func WebsocketHandler(c *gin.Context) {
	conn, _, _, err := ws.UpgradeHTTP(c.Request, c.Writer)
	if err != nil {
		logrus.Error("Failed to upgrade to WebSocket:", err)
		return
	}
	defer conn.Close()

	quit := make(chan struct{})

	go func() {
		// Read messages from the client
		for {
			msg, op, err := wsutil.ReadClientData(conn)
			if err != nil {
				logrus.Warn("WebSocket connection closed by client.")
				close(quit)
				return
			}

			// Log the message received from the client
			if op == ws.OpText {
				logrus.Infof("Received message from client: %s", string(msg))

				// Parse the message from the client
				var request struct {
					BookID int64  `json:"bookID"`
					Format string `json:"format"`
				}
				if err := json.Unmarshal(msg, &request); err != nil {
					logrus.Error("Failed to parse message from client:", err)
					continue
				}

				// Check if file exists and notify the client
				go checkFileAndNotify(conn, request.BookID)
			} else {
				logrus.Infof("Received non-text message from client with opcode: %v", op)
			}
		}
	}()

	for {
		select {
		case bookID := <-notificationChannel: // Ожидаем, пока книга будет готова
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

// checkFileAndNotify checks if the file for the specified book ID exists and notifies the client
func checkFileAndNotify(conn net.Conn, bookID int64) {
	mobiConversionDir := viper.GetString("app.mobi_conversion_dir")
	filePath := filepath.Join(mobiConversionDir, fmt.Sprintf("%d.mobi", bookID))

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	attempts := 0
	maxAttempts := 10

	for range ticker.C {
		attempts++
		// Check if the file exists
		if _, err := os.Stat(filePath); err == nil {
			// File exists
			logrus.Infof("Book %d is ready on disk, notifying client", bookID)
			if err := wsutil.WriteServerMessage(conn, ws.OpText, []byte(fmt.Sprintf("%d", bookID))); err != nil {
				logrus.Warn("Error writing to WebSocket:", err)
			}
			return
		} else if !os.IsNotExist(err) {
			logrus.Errorf("Error checking file %s: %v", filePath, err)
			return
		}

		if attempts >= maxAttempts {
			logrus.Errorf("File %s not found after %d attempts", filePath, maxAttempts)
			return
		}
	}
}
