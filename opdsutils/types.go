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
	Title       string
	Link        []Link
	Description string
	Author      *Author
	Updated     time.Time
	Created     time.Time
	Id          string
	Subtitle    string
	Items       []*Item
	Copyright   string
	Image       *Image
}

type Link struct {
	Href, Rel, Type, Length, Title string
}

type Author struct {
	Name, Email string
}

type Item struct {
	Title       string
	Link        []Link
	Source      *Link
	Author      *Author
	Description string // used as description in rss, summary in atom
	Id          string // used as guid in rss, id in atom
	Updated     time.Time
	Created     time.Time
	Enclosure   *Enclosure
	Content     string
}

type Enclosure struct {
	Url, Length, Type string
}

type Image struct {
	Url, Title, Link string
	Width, Height    int
}
