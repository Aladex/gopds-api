package opdsutils

import "encoding/xml"

// Multiple links with different rel can coexist
type AtomLink struct {
	XMLName xml.Name `xml:"link"`
	Href    string   `xml:"href,attr"`
	Rel     string   `xml:"rel,attr,omitempty"`
	Type    string   `xml:"type,attr,omitempty"`
	Length  string   `xml:"length,attr,omitempty"`
	Title   string   `xml:"title,attr,omitempty"`
}

type AtomAuthor struct {
	XMLName xml.Name `xml:"author"`
	AtomPerson
}

type AtomPerson struct {
	Name string `xml:"name,omitempty"`
	Uri  string `xml:"uri,omitempty"`
}

type AtomContributor struct {
	XMLName xml.Name `xml:"contributor"`
	AtomPerson
}

type AtomSummary struct {
	XMLName xml.Name `xml:"summary"`
	Content string   `xml:",chardata"`
}

type AtomContent struct {
	XMLName xml.Name `xml:"content"`
	Content string   `xml:",chardata"`
	Type    string   `xml:"type,attr"`
}

type AtomEntry struct {
	XMLName     xml.Name `xml:"entry"`
	Xmlns       string   `xml:"xmlns,attr,omitempty"`
	Title       string   `xml:"title"`   // required
	Updated     string   `xml:"updated"` // required
	Id          string   `xml:"id"`      // required
	Category    string   `xml:"category,omitempty"`
	Content     *AtomContent
	Language    string `xml:"dc:language,omitempty"`
	Issued      string `xml:"dc:issued,omitempty"`
	Rights      string `xml:"rights,omitempty"`
	Source      string `xml:"source,omitempty"`
	Published   string `xml:"published,omitempty"`
	Contributor *AtomContributor
	Summary     *AtomSummary // required if content has src or content is base64
	Links       []AtomLink   // required if no child 'content' elements
	Authors     []AtomAuthor // required if feed lacks an author
}

type AtomFeed struct {
	XMLName   xml.Name `xml:"feed"`
	Xmlns     string   `xml:"xmlns,attr"`
	XmlnsDc   string   `xml:"xmlns:dc,attr"`
	XmlnsOs   string   `xml:"xmlns:os,attr"`
	XmlnsOpds string   `xml:"xmlns:opds,attr,omitempty"`
	Title     string   `xml:"title"` // required
	Icon      string   `xml:"icon,omitempty"`
	Updated   string   `xml:"updated"` // required
	Links     []AtomLink
	Entries   []*AtomEntry `xml:"entry"`
}
