package utils

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"io/ioutil"
)

func ZipBook(df, filename string, path string) (io.ReadCloser, error) {
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	for _, f := range r.File {
		if f.Name == filename {
			// Создаем новый архив
			zf, err := w.Create(df + ".fb2")
			if err != nil {
				return nil, err
			}
			// Открываем файл
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			_, err = io.Copy(zf, rc)
			if err != nil {
				return nil, err
			}
			err = w.Close()
			if err != nil {
				return nil, err
			}
			zipAnswer := ioutil.NopCloser(bytes.NewReader(buf.Bytes()))

			return zipAnswer, nil
		}
	}

	return nil, errors.New("book is not found")

}

func EpubBook() {

}

func MobiBook() {

}

func FB2Book(filename string, path string) (io.ReadCloser, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	for _, f := range r.File {
		if f.Name == filename {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			return rc, nil
		}
	}
	return nil, errors.New("book is not found")
}
