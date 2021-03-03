package fb2scan_test

import (
	fb2scan "gopds-api/fb2scan/fb2"
	"io/ioutil"
	"os"
	"testing"
)

func TestParser(t *testing.T) {
	var (
		file     *os.File
		data     []byte
		result   fb2scan.FB2
		err      error
		filename = "test_books/win-enc.fb2"
	)

	if file, err = os.OpenFile(filename, os.O_RDONLY, 0666); err != nil {
		t.Fatal(err)
	}

	defer file.Close()

	if data, err = ioutil.ReadAll(file); err != nil {
		t.Fatal(err)
	}

	p := fb2scan.New(data)

	if result, err = p.Unmarshal(); err != nil {
		t.Fatal(err)
	}

	t.Logf("%+v\n", result)
}
