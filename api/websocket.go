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
	"gopds-api/utils"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

func ConvertBookToMobi(bookID int64) error {
	book, err := database.GetBook(bookID) // Retrieve the book details from the database
	if err != nil {
		return err
	}
	zipPath := viper.GetString("app.files_path") + book.Path // Construct the path to the book file.
	mobiConversionDir := viper.GetString("app.mobi_conversion_dir")

	if !utils.FileExists(zipPath) {
		return fmt.Errorf("file %s not found", zipPath)
	}

	bp := utils.NewBookProcessor(book.FileName, zipPath) // Create a new BookProcessor for the book file.
	var rc io.ReadCloser
	rc, err = bp.Mobi()
	if err != nil {
		return err
	}
	defer rc.Close()

	filePath := filepath.Join(mobiConversionDir, fmt.Sprintf("%d.mobi", bookID))
	logrus.Info("Creating mobi file:", filePath)
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
	logrus.Infof("Book %d converted and stored at %s", bookID, filePath)
	return nil
}

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

func WebsocketHandler(c *gin.Context) {
	conn, _, _, err := ws.UpgradeHTTP(c.Request, c.Writer)
	if err != nil {
		logrus.Error("Failed to upgrade to WebSocket:", err)
		return
	}
	defer conn.Close()

	// Create a channel for sending ping messages to the client
	clientNotificationChannel := make(chan string)
	quit := make(chan struct{})

	// Create a ticker for sending ping messages every 5 seconds
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Чтение сообщений от клиента
	go func() {
		for {
			msg, op, err := wsutil.ReadClientData(conn)
			if err != nil {
				logrus.Warn("WebSocket connection closed by client.")
				close(quit)
				return
			}

			// Обработка текстовых сообщений от клиента
			if op == ws.OpText {
				logrus.Infof("Received message from client: %s", string(msg))

				// Парсинг сообщения с запросом конвертации
				var request struct {
					BookID int64  `json:"bookID"`
					Format string `json:"format"`
				}
				if err := json.Unmarshal(msg, &request); err != nil {
					logrus.Error("Failed to parse message from client:", err)
					continue
				}

				// Запуск конвертации книги
				if request.Format == "mobi" {
					go func(bookID int64) {
						err := ConvertBookToMobi(bookID)
						if err != nil {
							logrus.Errorf("Failed to convert book to mobi: %v", err)
							return
						}
						clientNotificationChannel <- strconv.FormatInt(bookID, 10) // Отправляем в уникальный канал клиента
					}(request.BookID)
				} else {
					logrus.Warnf("Unsupported format: %s", request.Format)
				}
			} else {
				logrus.Infof("Received non-text message from client with opcode: %v", op)
			}
		}
	}()

	// Чтение уведомлений о готовности книги и отправка их клиенту
	for {
		select {
		case bookID := <-clientNotificationChannel:
			logrus.Infof("Notifying client about ready book: %s", bookID)
			if err := wsutil.WriteServerMessage(conn, ws.OpText, []byte(bookID)); err != nil {
				logrus.Warn("Error writing to WebSocket:", err)
				close(quit)
				return
			}
		case <-ticker.C:
			// Send a ping message to the client every 5 seconds
			if err := wsutil.WriteServerMessage(conn, ws.OpPing, nil); err != nil {
				logrus.Warn("Error sending ping to WebSocket:", err)
				close(quit)
				return
			}
		case <-quit:
			logrus.Info("Connection closed by client request.")
			return
		}
	}
}
