package telegram

import (
	"bytes"
	"errors"
	"fmt"
	"gopds-api/config"
	"gopds-api/models"
	"gopds-api/utils"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
)

func SendFile(url string, chatID int, file *io.ReadCloser, fileName string) error {
	// Send file to telegram by POST request from read.Closer file
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	fw, err := w.CreateFormFile("document", fileName)
	if err != nil {
		return err
	}
	_, err = io.Copy(fw, *file)
	if err != nil {
		return err
	}
	err = w.Close()
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", url, &b)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("chat_id", strconv.Itoa(chatID))
	client := &http.Client{}
	_, err = client.Do(req)
	if err != nil {
		return err
	}
	return nil
}

// SendBookFile sends book file to telegram user
func SendBookFile(fileFormat string, user models.User, book models.Book) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendDocument", user.BotToken)
	zipPath := config.AppConfig.GetString("app.files_path") + book.Path

	bp := utils.NewBookProcessor(book.FileName, zipPath)
	var rc io.ReadCloser
	var err error

	switch strings.ToLower(fileFormat) {
	case "fb2":
		rc, err = bp.FB2()
	case "epub":
		rc, err = bp.Epub()
	case "mobi":
		rc, err = bp.Mobi()
	case "zip":
		rc, err = bp.Zip(book.FileName)
	default:
		return errors.New("unsupported file format")
	}

	if err != nil {
		return err
	}
	defer func() {
		if cerr := rc.Close(); cerr != nil {
			log.Printf("failed to close file: %v", cerr)
		}
	}()

	err = SendFile(url, user.TelegramID, &rc, fmt.Sprintf("%s.%s", book.DownloadName(), fileFormat))
	if err != nil {
		return err
	}

	return nil
}
