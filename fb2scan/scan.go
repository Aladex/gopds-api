package fb2scan

import (
	"archive/zip"
	"fmt"
	fb2scan "gopds-api/fb2scan/fb2"
	"gopds-api/models"
	"io/ioutil"
	"strconv"
	"strings"
	"time"
)

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

	}
}
