package converter

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"gopds-api/epub"
	"io"
	"io/ioutil"
	"log"
)

// FB2Converter - main object for converting fb2 to epub
type FB2Converter struct {
	Fb2ReaderFunc   func() (io.ReadCloser, error)
	Translit        bool
	SectionsPerPage int

	Chapter int
}

func (c *FB2Converter) links() (map[string]string, error) {
	decoder, err := c.decoder()
	if err != nil {
		return nil, err
	}

	defer decoder.close()

	var (
		links          = map[string]string{}
		currentBody    string
		currentSection string
		chapter        int
		sectionDepth   int
		sectionsNum    int

		updatePage = func() {
			sectionDepth = 0
			sectionsNum = 0
			chapter++
			currentSection = fmt.Sprintf("%s%d", currentBody, chapter)
		}
	)

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		switch t := token.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "section":
				if sectionDepth == 0 {
					sectionsNum++
					if sectionsNum >= c.SectionsPerPage {
						updatePage()
					}
				}
				sectionDepth++

			case "body":
				currentBody = "ch"
				for _, a := range t.Attr {
					if a.Name.Local == "name" && len(a.Value) > 0 {
						currentBody = a.Value
					}
				}
				updatePage()
			}

			for _, a := range t.Attr {
				if a.Name.Local == "id" && len(a.Value) > 0 {
					links[`#`+a.Value] = currentSection
					break
				}
			}

		case xml.EndElement:
			if t.Name.Local == "section" {
				sectionDepth--
			}

		}
	}

	return links, nil
}

func (c *FB2Converter) makeEpub(links map[string]string, translit bool) (io.ReadCloser, error) {
	decoder, err := c.decoder()
	if err != nil {
		return nil, err
	}
	defer decoder.close()
	buf := new(bytes.Buffer)
	outEpub, err := epub.New(buf, translit)
	if err != nil {
		return nil, err
	}

	defer outEpub.Close()

	c.Chapter = 0

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		if t, ok := token.(xml.StartElement); ok {
			switch t.Name.Local {
			case "description":
				if err = c.fillDescription(decoder, outEpub); err != nil {
					return nil, err
				}

			case "body":
				bodyName := "ch"
				for _, a := range t.Attr {
					if a.Name.Local == "name" && len(a.Value) > 0 {
						bodyName = a.Value
					}
				}

				if err = c.addPage(decoder, outEpub, bodyName, links); err != nil {
					return nil, err
				}

			case "binary":
				content, err := decoder.getText()
				if err != nil {
					return nil, err
				}

				var contentType, id string
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "content-type":
						contentType = a.Value
					case "id":
						id = a.Value
					}
				}
				if err = outEpub.AddBinary(id, contentType, content); err != nil {
					log.Println(err)
				}
			}
		}
	}
	outEpub.Close()

	zipAnswer := ioutil.NopCloser(bytes.NewReader(buf.Bytes()))
	return zipAnswer, nil
}

// Convert fb2 to epub
func (c *FB2Converter) Convert(translit bool) (io.ReadCloser, error) {
	links, err := c.links()
	if err != nil {
		return nil, err
	}
	ior, err := c.makeEpub(links, translit)
	return ior, err
}
