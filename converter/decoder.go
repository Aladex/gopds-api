package converter

import (
	"encoding/xml"
	"io"
)

type fb2decoder struct {
	*xml.Decoder
	f io.ReadCloser
}

func (d *fb2decoder) close() error {
	return d.f.Close()
}

func (d *fb2decoder) getText() (string, error) {
	token, err := d.Token()
	if err != nil {
		return "", err
	}

	if t, ok := token.(xml.CharData); ok {
		return string(t), nil
	}

	return "", nil
}

func (c *FB2Converter) decoder() (*fb2decoder, error) {
	var (
		f   io.ReadCloser
		err error
	)

	if f, err = c.Fb2ReaderFunc(); err != nil {
		return nil, err
	}

	return &fb2decoder{
		Decoder: xml.NewDecoder(f),
		f:       f,
	}, nil
}
