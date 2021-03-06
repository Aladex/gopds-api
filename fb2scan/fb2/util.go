package fb2scan

import (
	fb2scan "gopds-api/fb2scan/protoype"
	"io"

	"golang.org/x/text/encoding/charmap"
)

// get xlink from enclosed tag image
func parseImage(data []byte) string {
	result := ""
	quoteOpened := false
_loop:
	for _, v := range data {
		if quoteOpened {
			if v == '"' {
				break _loop
			}
			result += string(v)
		} else {
			if v == '"' {
				quoteOpened = true
			}
		}
	}
	return result
}

// decode windows-1251
func decodeWin1251(i io.Reader) (r io.Reader) {
	decoder := charmap.Windows1251.NewDecoder()
	r = decoder.Reader(i)

	return
}

func decodeWin1252(i io.Reader) (r io.Reader) {
	decoder := charmap.Windows1252.NewDecoder()
	r = decoder.Reader(i)

	return
}

func NewPFB2() fb2scan.PFB2 {
	var result fb2scan.PFB2
	result.Description = new(fb2scan.Description)
	result.Description.TitleInfo = new(fb2scan.TitleInfo)

	return result
}
