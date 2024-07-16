package utils

import (
	"archive/zip"
	"bytes"
	"errors"
	"github.com/google/uuid"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
)

type BookProcessor struct {
	filename string
	path     string
}

func DeleteTmpFile(filename, format string) error {
	err := os.Remove(filename + format)
	if err != nil {
		return err
	}
	return nil
}

func NewBookProcessor(filename, path string) *BookProcessor {
	return &BookProcessor{
		filename: filename,
		path:     path,
	}
}

func (bp *BookProcessor) process(format string, cmdArgs []string, convert bool) (io.ReadCloser, error) {
	r, err := zip.OpenReader(bp.path)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name == bp.filename {
			tmpFilename := uuid.New().String()
			defer func() {
				if err := DeleteTmpFile(tmpFilename, format); err != nil {
					log.Printf("failed to delete tmp file: %v", err)
				}
			}()

			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()

			if convert {
				tmpFile, err := os.Create(tmpFilename + ".fb2")
				if err != nil {
					return nil, err
				}
				defer func() {
					if err := tmpFile.Close(); err != nil {
						log.Printf("failed to close tmp file: %v", err)
					}
				}()

				if _, err = io.Copy(tmpFile, rc); err != nil {
					return nil, err
				}

				cmdArgs = append(cmdArgs, tmpFilename+".fb2", ".")

				cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
				if err := cmd.Run(); err != nil {
					return nil, err
				}

				convertedBook, err := os.Open(tmpFilename + format)
				if err != nil {
					return nil, err
				}

				return convertedBook, nil
			} else {
				return rc, nil
			}
		}
	}
	return nil, errors.New("book not found")
}

func (bp *BookProcessor) Epub() (io.ReadCloser, error) {
	return bp.process(".epub", []string{"external_fb2mobi/fb2c", "convert", "--to", "epub"}, true)
}

func (bp *BookProcessor) Mobi() (io.ReadCloser, error) {
	return bp.process(".mobi", []string{"external_fb2mobi/fb2c", "convert", "--to", "mobi"}, true)
}

func (bp *BookProcessor) FB2() (io.ReadCloser, error) {
	return bp.process("", nil, false)
}

func (bp *BookProcessor) Zip(df string) (io.ReadCloser, error) {
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)
	r, err := zip.OpenReader(bp.path)
	if err != nil {
		return nil, err
	}
	for _, f := range r.File {
		if f.Name == bp.filename {
			zf, err := w.Create(df + ".fb2")
			if err != nil {
				return nil, err
			}
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
