package fb2scan

import (
	"golang.org/x/text/encoding/charmap"
	fb2scan "gopds-api/fb2scan/protoype"
)

var charMapEncondig = map[string]*charmap.Charmap{
	"windows-1251": charmap.Windows1251,
	"windows-1252": charmap.Windows1252,
	"windows-1255": charmap.Windows1255,
	"koi8-r":       charmap.KOI8R,
	"iso-8859-1":   charmap.ISO8859_1,
	"iso-8859-5":   charmap.ISO8859_5,
}

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

func NewPFB2() fb2scan.PFB2 {
	var result fb2scan.PFB2
	result.Description = new(fb2scan.Description)
	result.Description.TitleInfo = new(fb2scan.TitleInfo)

	return result
}
