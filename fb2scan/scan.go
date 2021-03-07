package fb2scan

import (
	"archive/zip"
	"encoding/base64"
	"fmt"
	"github.com/spf13/viper"
	"gopds-api/database"
	fb2scan "gopds-api/fb2scan/fb2"
	"gopds-api/logging"
	"gopds-api/models"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func init() {
	go addBook(bookChan)
}

var bookChan = make(chan models.Book)

// AnnotationTagRemove удаление тегов по регулярке из аннотации
func AnnotationTagRemove(annotation string) string {
	tagRegExp := regexp.MustCompile(`<[^>]*>`)
	return tagRegExp.ReplaceAllString(annotation, "")
}

func addBook(b chan models.Book) {
	for {
		err := database.AddBook(<-b)
		if err != nil {
			logging.CustomLog.Println(err)
		}
	}
}

func visit(files *[]string) filepath.WalkFunc {
	return func(path string, info os.FileInfo, err error) error {
		if err != nil {
			logging.CustomLog.Println(err)
		}
		if strings.HasSuffix(path, ".zip") {
			*files = append(*files, path)
		}
		return nil
	}
}

func GetArchivesList() {
	var files []string
	err := filepath.Walk(viper.GetString("app.files_path"), visit(&files))
	if err != nil {
		logging.CustomLog.Println(err)
	}
	for _, f := range files {
		ScanNewArchive(f)
	}
}

func ExtractCover(cover string) string {
	jpegVal := strings.TrimSpace(strings.ReplaceAll(cover, "\n", ""))
	_, err := base64.StdEncoding.DecodeString(jpegVal)
	if err != nil {
		logging.CustomLog.Println("decode error:", err)
		return ""
	}
	return jpegVal
}

// ScanNewArchives функция для сканирования новых архивов после скачивания
func ScanNewArchive(path string) {
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
			DocDate:      result.Description.DocumentInfo.Date.Value,
			Lang:         result.Description.TitleInfo.Lang,
			Title:        result.Description.TitleInfo.BookTitle,
			Annotation:   AnnotationTagRemove(result.Description.TitleInfo.Annotation.Value),
			Series:       nil,
		}
		if newBook.Annotation == "" {
			newBook.Annotation = "Нет описания"
		}
		for _, a := range result.Description.TitleInfo.Author {
			authorName := fmt.Sprintf("%s %s", a.FirstName, a.LastName)
			newBook.Authors = append(newBook.Authors, &models.Author{
				FullName: strings.TrimSpace(authorName),
			})
		}
		for _, s := range result.Description.TitleInfo.Sequence {
			seqNum, err := strconv.ParseInt(s.Number, 10, 64)
			if err != nil {
				seqNum = 0
			}
			newBook.Series = append(newBook.Series, &models.Series{
				SerNo: seqNum,
				Ser:   s.Name,
			})
		}

		for _, c := range result.Binary {
			if c.ContentType == "image/jpeg" && strings.ToLower(c.ID) == "cover.jpg" {
				cover := ExtractCover(c.Value)
				if cover != "" {
					newBook.Covers = append(newBook.Covers, &models.Cover{
						Cover: cover,
					})
				}
			}
		}
		bookChan <- newBook

	}
}
