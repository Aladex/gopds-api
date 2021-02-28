package opdsutils

import (
	"encoding/xml"
	"fmt"
	"time"
)

const header = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:dc="http://purl.org/dc/terms/" xmlns:os="http://a9.com/-/spec/opensearch/1.1/" xmlns:opds="http://opds-spec.org/2010/catalog">`
const ns = "http://www.w3.org/2005/Atom"

// creates an Atom representation of this feed
func (f *Feed) ToAtom() (string, error) {
	a := &Atom{f}
	return ToXML(a)
}

// returns the first non-zero time formatted as a string or ""
func anyTimeFormat(format string, times ...time.Time) string {
	for _, t := range times {
		if !t.IsZero() {
			return t.Format(format)
		}
	}
	return ""
}

// create a new AtomFeed with a generic Feed struct's data
func (a *Atom) AtomFeed() *AtomFeed {
	updated := anyTimeFormat(time.RFC3339, a.Updated, a.Created)
	links := []AtomLink{}
	for _, l := range a.Links {
		links = append(links, AtomLink{
			Href:  l.Href,
			Rel:   l.Rel,
			Type:  l.Type,
			Title: l.Title,
		})
	}

	feed := &AtomFeed{
		Xmlns:    ns,
		Title:    a.Title,
		Links:    links,
		Subtitle: a.Description,
		Id:       a.Id,

		Updated: updated,
	}
	for _, e := range a.Items {
		feed.Entries = append(feed.Entries, newAtomEntry(e))
	}
	return feed
}

func ToXML(feed XmlFeed) (string, error) {
	x := feed.FeedXml()
	data, err := xml.MarshalIndent(x, "", "  ")
	if err != nil {
		return "", err
	}
	// strip empty line from default xml header
	s := header + string(data)
	return s, nil
}

func newAtomEntry(i *Item) *AtomEntry {
	id := i.Id
	// assume the description is html
	s := &AtomSummary{Content: i.Description, Type: "html"}

	atomAuthors := []AtomAuthor{}
	for _, a := range i.Authors {
		atomAuthors = append(atomAuthors, AtomAuthor{
			AtomPerson: AtomPerson{
				Name: a.Name,
				Uri:  fmt.Sprintf("/opds/author/%d", a.ID),
			},
		})
	}

	atomLinks := []AtomLink{}
	for _, l := range i.Link {
		atomLinks = append(atomLinks, AtomLink{
			Href:  l.Href,
			Rel:   l.Rel,
			Type:  l.Type,
			Title: l.Title,
		})
	}
	x := &AtomEntry{
		Title:    i.Title,
		Links:    atomLinks,
		Id:       id,
		Updated:  anyTimeFormat(time.RFC3339, i.Updated, i.Created),
		Summary:  s,
		Language: i.Language,
		Issued:   i.Issued,
	}

	// if there's a content, assume it's html
	if len(i.Content) > 0 {
		x.Content = &AtomContent{Content: i.Content, Type: "text"}
	}

	if len(atomAuthors) > 0 {
		x.Authors = atomAuthors
	}
	return x
}
