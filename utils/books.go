package utils

import (
	"archive/zip"
	"bytes"
	"errors"
	uuid "github.com/satori/go.uuid"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
)

func DeleteTmpFile(filename string, format string) error {
	err := os.Remove(filename + ".fb2")
	if err != nil {
		return err
	}
	if format == ".mobi" {
		err = os.RemoveAll(filename + ".sdr")
		if err != nil {
			return err
		}
	}
	err = os.Remove(filename + format)
	if err != nil {
		return err
	}
	return nil
}

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

func EpubBook(filename string, path string) (io.ReadCloser, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	for _, f := range r.File {
		if f.Name == filename {
			tmpFilename := uuid.NewV4().String()
			defer DeleteTmpFile(tmpFilename, ".epub")
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}

			tmpFile, err := os.Create(tmpFilename + ".fb2")
			defer tmpFile.Close()

			if err != nil {
				return nil, err
			}

			_, err = io.Copy(tmpFile, rc)

			cmd := exec.Command("external_fb2mobi/fb2mobi", tmpFilename+".fb2", "-f", "epub")
			err = cmd.Run()
			if err != nil {
				return nil, err
			}

			convertedMobi, err := os.Open(tmpFilename + ".epub")
			if err != nil {
				return nil, err
			}

			return convertedMobi, nil
		}
	}
	return nil, errors.New("book is not found")
}

func MobiBook(filename string, path string) (io.ReadCloser, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	for _, f := range r.File {
		if f.Name == filename {
			tmpFilename := uuid.NewV4().String()
			defer DeleteTmpFile(tmpFilename, ".mobi")
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}

			tmpFile, err := os.Create(tmpFilename + ".fb2")
			defer tmpFile.Close()

			if err != nil {
				return nil, err
			}

			_, err = io.Copy(tmpFile, rc)

			cmd := exec.Command("external_fb2mobi/fb2mobi", tmpFilename+".fb2", "-f", "mobi")
			err = cmd.Run()
			if err != nil {
				return nil, err
			}

			convertedMobi, err := os.Open(tmpFilename + ".mobi")
			if err != nil {
				return nil, err
			}

			return convertedMobi, nil
		}
	}
	return nil, errors.New("book is not found")
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
