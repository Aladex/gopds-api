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

// ToPB converts fb2 to protobuf fb2
func ToPB(target FB2) fb2scan.PFB2 {
	var result = NewPFB2()

	result.Description.TitleInfo.Annotation = target.Description.TitleInfo.Annotation
	for i, v := range target.Description.TitleInfo.Author {
		result.Description.TitleInfo.Author[i].FirstName = v.FirstName
		result.Description.TitleInfo.Author[i].MiddleName = v.MiddleName
		result.Description.TitleInfo.Author[i].LastName = v.LastName
		result.Description.TitleInfo.Author[i].Email = v.Email
		result.Description.TitleInfo.Author[i].HomePage = v.HomePage
		result.Description.TitleInfo.Author[i].Nickname = v.Nickname
	}
	result.Description.TitleInfo.Annotation = target.Description.TitleInfo.Annotation
	result.Description.TitleInfo.Annotation = target.Description.TitleInfo.Annotation
	result.ID = target.ID

	return result
}

func NewPFB2() fb2scan.PFB2 {
	var result fb2scan.PFB2
	result.Description = new(fb2scan.Description)
	result.Description.TitleInfo = new(fb2scan.TitleInfo)

	return result
}
