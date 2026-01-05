package parser

// BookFile holds parsed FB2 metadata used during rescans.
type BookFile struct {
	Title      string
	Authors    []Author
	Tags       []string
	Series     *Series
	Language   string
	DocDate    string
	Annotation string
	Cover      []byte
	BodySample string
	Issues     []string
	Filename   string
	Mimetype   string
}

// Author represents a parsed author name and sort key.
type Author struct {
	Name    string
	Sortkey string
}

// Series represents a parsed series name and index.
type Series struct {
	Title string
	Index string
}
