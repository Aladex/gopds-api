package opdsutils

import "time"

type Atom struct {
	*Feed
}

// FeedXml returns an XML-Ready object for an Atom object
func (a *Atom) FeedXml() interface{} {
	return a.AtomFeed()
}

type XmlFeed interface {
	FeedXml() interface{}
}

type Feed struct {
	Title   string
	Id      string
	Links   []Link
	Updated time.Time
	Items   []*Item
}

type Link struct {
	Href, Rel, Type, Length, Title string
}

// Author is a name of an entry's author line; ID is the catalog author it
// links to, nil when it links to none.
type Author struct {
	Name string
	ID   *int64
}

type Item struct {
	Title       string
	Link        []Link
	Source      *Link
	Authors     []Author
	Description string // used as description in rss, summary in atom
	Id          string // used as guid in rss, id in atom
	Updated     time.Time
	Language    string // used as guid in rss, id in atom
	Issued      string // used as guid in rss, id in atom
	Content     string
}
