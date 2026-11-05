package objectstore

import (
	"net/url"
	"testing"
)

func TestObjectURL(t *testing.T) {
	endpoint, err := url.Parse("https://s3.example.com/base/")
	if err != nil {
		t.Fatal(err)
	}
	pathStyle := &Client{cfg: Config{Bucket: "lib", PathStyle: true}, endpoint: endpoint}
	virtual := &Client{cfg: Config{Bucket: "lib"}, endpoint: endpoint}
	listing := url.Values{prefixParam: {"a b/"}, listTypeParam: {listTypeV2}}

	for name, c := range map[string]struct {
		got, want string
	}{
		"path style":   {pathStyle.objectURL("книга 1.zip", nil), "https://s3.example.com/base/lib/%D0%BA%D0%BD%D0%B8%D0%B3%D0%B0%201.zip"},
		"virtual host": {virtual.objectURL("dir/a+b.zip", nil), "https://lib.s3.example.com/base/dir/a%2Bb.zip"},
		"bucket":       {pathStyle.objectURL("", listing), "https://s3.example.com/base/lib/?list-type=2&prefix=a%20b%2F"},
	} {
		if c.got != c.want {
			t.Errorf("%s: got %s, want %s", name, c.got, c.want)
		}
	}
}
