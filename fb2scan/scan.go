package fb2scan

import (
	"archive/zip"
	"encoding/base64"
	"fmt"
	fb2scan "gopds-api/fb2scan/fb2"
	"gopds-api/logging"
	"gopds-api/models"
	"io/ioutil"
	"strconv"
	"strings"
	"time"
)

func init() {
	go addBook(bookChan)
}

var bookChan chan models.Book

func addBook(b chan models.Book) {
	for {
		fmt.Println(<-b)
	}
}

func ExtractCover(name, cover, path string) bool {
	jpegVal := strings.Join(strings.Split(cover, "\n"), "")
	decoded, err := base64.StdEncoding.DecodeString(jpegVal)
	if err != nil {
		logging.CustomLog.Println("decode error:", err)
		return false
	}
	err = ioutil.WriteFile(fmt.Sprintf("%s/%s", path, name), decoded, 0644)
	if err != nil {
		logging.CustomLog.Println("decode error:", err)
		return false
	}
	return true
}

// ScanNewArchives функция для сканирования новых архивов после скачивания
func ScanNewArchives(path string) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return
	}
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			return
		}
		data, err := ioutil.ReadAll(rc)
		if err != nil {
			return
		}
		p := fb2scan.New(data)
		result, err := p.Unmarshal()
		if err != nil {
			return
		}
		newBook := models.Book{
			Path:         path,
			Format:       "fb2",
			FileName:     f.Name,
			RegisterDate: time.Now(),
			DocDate:      result.Description.DocumentInfo.Date,
			Lang:         result.Description.TitleInfo.Lang,
			Title:        result.Description.TitleInfo.BookTitle,
			Annotation:   result.Description.TitleInfo.Annotation,
			Cover:        false,
			Series:       nil,
		}
		for _, a := range result.Description.TitleInfo.Author {
			authorName := fmt.Sprintf("%s %s", a.FirstName, a.LastName)
			newBook.Authors = append(newBook.Authors, &models.Author{
				FullName: strings.TrimSpace(authorName),
			})
		}
		for _, s := range result.Description.TitleInfo.Sequence {
			seqNum, err := strconv.Atoi(s.Number)
			if err != nil {
				seqNum = 0
			}
			newBook.Series = append(newBook.Series, &models.Series{
				SerNo: seqNum,
				Ser:   s.Name,
			})
		}
		// TODO: здесь надо будет обдумать извлечение обложки книги. Может перетереться значение
		for _, c := range result.Binary {
			if c.ContentType == "image/jpeg" {
				newBook.Cover = ExtractCover(f.Name, c.Value, "")
			}
		}
	}
}
