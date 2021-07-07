package fb2scan

import (
	"archive/zip"
	"encoding/base64"
	"fmt"
	"gopds-api/config"
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
	go updateCover(coverChan)
}

var bookChan = make(chan models.Book)
var coverChan = make(chan models.Book)

func isArcScanned(file string, catalogs []string) bool {
	for _, c := range catalogs {
		if c == file {
			return true
		}
	}
	return false
}

func GetUnscannedFiles(catalogs, files []string) []string {
	var unscannedFiles []string
	for _, f := range files {
		if !isArcScanned(f, catalogs) {
			unscannedFiles = append(unscannedFiles, f)
		}
	}
	return unscannedFiles
}

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

func updateCover(c chan models.Book) {
	for {
		err := database.UpdateBookCover(<-c)
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
			*files = append(*files, strings.ReplaceAll(path, config.AppConfig.GetString("app.files_path"), ""))
		}
		return nil
	}
}

func GetArchivesList() {
	var files []string
	path := config.AppConfig.GetString("app.files_path")
	err := filepath.Walk(path, visit(&files))
	if err != nil {
		logging.CustomLog.Println(err)
	}
	scannedCatalogs, err := database.GetCatalogs(true)
	if err != nil {
		logging.CustomLog.Println(err)
		return
	}
	unscannedFiles := GetUnscannedFiles(scannedCatalogs, files)
	for _, f := range unscannedFiles {
		ScanNewArchive(path + f)
		err := database.AddCatalog(models.Catalog{
			CatName:   f,
			IsScanned: true,
		})
		if err != nil {
			logging.CustomLog.Println(err)
		}
	}
}

func ExtractCover(cover string) (jpegVal string, err error) {
	jpegVal = strings.TrimSpace(strings.ReplaceAll(cover, "\n", ""))
	_, err = base64.StdEncoding.DecodeString(jpegVal)
	if err != nil {
		logging.CustomLog.Println("decode cover error:", err)
		return
	}
	return
}

func scanBookCover(f *zip.File, path string) (models.Book, error) {
	newBook := models.Book{
		FileName: f.Name,
		Path:     path,
	}

	rc, err := f.Open()
	if err != nil {
		logging.CustomLog.Println(err)
		return models.Book{}, err
	}
	data, err := ioutil.ReadAll(rc)
	if err != nil {
		logging.CustomLog.Println(err)
		return models.Book{}, err
	}
	p := fb2scan.New(data)
	result, err := p.Unmarshal()
	if err != nil {
		logging.CustomLog.Println(err, fmt.Sprintf("book_path: %s, filename: %s", path, f.Name))
		return models.Book{}, err
	}

	coverTag := strings.ReplaceAll(result.Description.TitleInfo.Coverpage.Image.Href, "#", "")
	for _, c := range result.Binary {
		if c.ID == coverTag {
			cover, err := ExtractCover(c.Value)
			if err != nil {
				continue
			}
			if cover != "" {
				newBook.Covers = append(newBook.Covers, &models.Cover{
					Cover:       cover,
					ContentType: c.ContentType,
				})
			}
		}
	}
	return newBook, nil
}

func UpdateCovers() {
	scannedCatalogs, err := database.GetCatalogs(true)
	if err != nil {
		logging.CustomLog.Println(err)
		return
	}
	for _, a := range scannedCatalogs {
		r, err := zip.OpenReader(config.AppConfig.GetString("app.files_path") + a)
		if err != nil {
			logging.CustomLog.Println(err)
			return
		}
		for _, f := range r.File {
			r, err := scanBookCover(f, a)
			if err != nil {
				logging.CustomLog.Println(err)
			} else {
				coverChan <- r
			}
		}
	}
}

func ScanFb2File(data []byte, path string, filename string) (models.Book, error) {
	p := fb2scan.New(data)
	result, err := p.Unmarshal()
	if err != nil {
		logging.CustomLog.Println(err)
		return models.Book{}, err
	}
	newBook := models.Book{
		Path:         strings.ReplaceAll(path, config.AppConfig.GetString("app.files_path"), ""),
		Format:       "fb2",
		FileName:     filename,
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
		newBook.Authors = append(newBook.Authors, models.Author{
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
	coverTag := strings.ReplaceAll(result.Description.TitleInfo.Coverpage.Image.Href, "#", "")
	for _, c := range result.Binary {
		if c.ID == coverTag {
			cover, err := ExtractCover(c.Value)
			if err != nil {
				continue
			}
			if cover != "" {
				newBook.Covers = append(newBook.Covers, &models.Cover{
					Cover:       cover,
					ContentType: c.ContentType,
				})
			}
		}
	}
	return newBook, nil
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
			logging.CustomLog.Println(err)
			continue
		}
		data, err := ioutil.ReadAll(rc)
		if err != nil {
			logging.CustomLog.Println(err)
			continue
		}
		newBook, err := ScanFb2File(data, path, f.Name)

		if err != nil {
			logging.CustomLog.Println(err)
			continue
		}

		bookChan <- newBook
	}
}

func SaveBook(bookData []byte, filename string) {
	path := config.AppConfig.GetString("app.users_path")

	userBook, err := ScanFb2File(bookData, path, filename)
	if err != nil {
		logging.CustomLog.Println(err)
	}
	archiveFileName := fmt.Sprintf("%s/book.zip", path)

	bookZip, err := os.Create(archiveFileName)
	if err != nil {
		logging.CustomLog.Println(err)
	}

	zipWriter := zip.NewWriter(bookZip)
	defer func() {
		err := zipWriter.Close()
		if err != nil {
			logging.CustomLog.Println(err)
		}
	}()

	zf, err := zipWriter.Create("book.fb2")
	_, err = zf.Write(bookData)
	if err != nil {
		logging.CustomLog.Println(err)
	}
	fmt.Println(userBook)
}
