// Package fb2 represent .fb2 format parser
package fb2scan

import (
	"bytes"
	"encoding/xml"
	"golang.org/x/net/html/charset"
	"io"
	"io/ioutil"
	"regexp"
	"strings"
)

// Parser struct
type Parser struct {
	book   []byte
	reader io.Reader
}

// New creates new Parser
func New(data []byte) *Parser {
	return &Parser{
		book: data,
	}
}

// NewReader creates new Parser from reader
func NewReader(data io.Reader) *Parser {
	return &Parser{
		reader: data,
	}
}

// CharsetReader required for change encodings
func (p *Parser) CharsetReader(c string, i io.Reader) (r io.Reader, e error) {
	c = strings.ToLower(c)
	if val, ok := charMapEncondig[c]; ok {
		decoder := val.NewDecoder()
		r = decoder.Reader(i)
	}
	return
}

func (p *Parser) RemoveBody() []byte {
	bodyRegExp := regexp.MustCompile(`(?s)(.*)<body>.*</body>(.*)`)
	return bodyRegExp.ReplaceAll(p.book, []byte(`$1$2`))
}

// Unmarshal parse data to FB2 type
func (p *Parser) Unmarshal() (result FB2, err error) {
	_, name, _ := charset.DetermineEncoding(p.book, "")
	byteReader := bytes.NewReader(p.book)
	if strings.Contains(name, "utf-16") {
		reader, _ := charset.NewReaderLabel(name, byteReader)
		p.book, err = ioutil.ReadAll(reader)
	}
	bookData := p.RemoveBody()
	byteReader = bytes.NewReader(bookData)
	decoder := *xml.NewDecoder(byteReader)

	decoder.CharsetReader = charset.NewReaderLabel

	if err = decoder.Decode(&result); err != nil {
		return
	}
	result.UnmarshalCoverpage(p.book)
	return
}
