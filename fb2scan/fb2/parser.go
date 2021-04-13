// Package fb2 represent .fb2 format parser
package fb2scan

import (
	"bytes"
	"encoding/xml"
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
	if val, ok := charMapEncondig[c]; ok {
		decoder := val.NewDecoder()
		r = decoder.Reader(i)
	}
	return
}

// Unmarshal parse data to FB2 type
func (p *Parser) Unmarshal() (result FB2, err error) {
	reader := bytes.NewReader(p.book)
	decoder := xml.NewDecoder(reader)
	decoder.CharsetReader = p.CharsetReader

	if err = decoder.Decode(&result); err != nil {
		return
	}
	result.UnmarshalCoverpage(p.book)
	return
}
