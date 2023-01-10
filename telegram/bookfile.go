package telegram

import (
	"bytes"
	"fmt"
	"gopds-api/config"
	"gopds-api/models"
	"gopds-api/utils"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
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
	switch fileFormat {
	case "fb2":
		rc, err := utils.FB2Book(book.FileName, zipPath)
		if err != nil {
			return err
		}
		defer func() {
			err := rc.Close()
			if err != nil {
				return
			}
		}()
		err = SendFile(url, user.TelegramID, &rc, book.DownloadName()+".fb2")
	case "epub":
		rc, err := utils.EpubBook(book.FileName, zipPath)
		if err != nil {
			return err
		}
		defer func() {
			err := rc.Close()
			if err != nil {
				return
			}
		}()
		err = SendFile(url, user.TelegramID, &rc, book.DownloadName()+".epub")
	}

	return nil
}
