// Package fb2 represent .fb2 format parser
package fb2scan

import (
	"bytes"
	"encoding/xml"
	"golang.org/x/net/html/charset"
	"io"
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
	switch c {
	case "windows-1251":
		r = decodeWin1251(i)
	case "windows-1252":
		r = decodeWin1252(i)
	case "koi8-r":
		r = decodeKoi8r(i)
	case "iso-8859-1":
		r = decodeISO8859_1(i)
	case "iso-8859-5":
		r = decodeISO8859_5(i)
	case "utf-16":
		r = decodeUTF16(i)
	}
	return
}

// Unmarshal parse data to FB2 type
func (p *Parser) Unmarshal() (result FB2, err error) {
	if p.reader != nil {
		decoder := xml.NewDecoder(p.reader)
		decoder.CharsetReader = p.CharsetReader
		if err = decoder.Decode(&result); err != nil {
			return
		}
		result.UnmarshalCoverpage(p.book)
		return
	}
	_, name, _ := charset.DetermineEncoding(p.book, "")

	reader := bytes.NewReader(p.book)
	decoder := xml.NewDecoder(reader)
	if strings.Contains(name, "utf") {
		byteReader := bytes.NewReader(p.book)
		reader, _ := charset.NewReaderLabel(name, byteReader)
		decoder = xml.NewDecoder(reader)
	} else {
		decoder.CharsetReader = p.CharsetReader
	}

	if err = decoder.Decode(&result); err != nil {
		return
	}
	result.UnmarshalCoverpage(p.book)
	return
}
