package authorentity_test

import (
	"strings"
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/google/uuid"
)

// catalog builds a build input the way the catalog holds it: books with a
// title and series, and the selected author credits on them. Every
// fingerprint carries one result, as on production (one selected result per
// fingerprint).
type catalog struct {
	t       *testing.T
	in      authorentity.Input
	results map[string]*authorentity.Result
	next    int64
}

func newCatalog(t *testing.T) *catalog {
	t.Helper()
	return &catalog{t: t, in: authorentity.Input{Books: map[int64]authorentity.Book{}}, results: map[string]*authorentity.Result{}}
}

// book adds a book with a title and the series it belongs to.
func (c *catalog) book(id int64, title string, series ...int64) *catalog {
	c.in.Books[id] = authorentity.Book{Title: title, Series: series}
	return c
}

// name is one parsed person name of a fingerprint.
type name struct {
	family, given string
	additional    []string
	suffix        string
	nickname      string
	tag           int
	script        string
}

func person(family, given string, additional ...string) *name {
	return &name{family: family, given: given, additional: additional, script: "Cyrl"}
}

func (n *name) withNickname(nick string) *name { n.nickname = nick; return n }
func (n *name) withSuffix(s string) *name      { n.suffix = s; return n }
func (n *name) withTag(tag int) *name          { n.tag = tag; return n }
func (n *name) latin() *name                   { n.script = "Latn"; return n }

// nickOnly is a person known by a nickname alone: no name parts.
func nickOnly(nick string) *name { return &name{nickname: nick, script: "Cyrl"} }

func (n *name) display() string {
	if n.family == "" && n.given == "" && len(n.additional) == 0 {
		return n.nickname
	}
	parts := []string{n.given}
	parts = append(parts, n.additional...)
	parts = append(parts, n.family)
	if n.suffix != "" {
		parts = append(parts, n.suffix)
	}
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}

// credit adds the credit id on book with fingerprint fp, whose result is n.
func (c *catalog) credit(id, book int64, fp string, n *name) *catalog {
	c.t.Helper()
	if _, ok := c.in.Books[book]; !ok {
		c.t.Fatalf("credit %d on unknown book %d", id, book)
	}
	r, ok := c.results[fp]
	if !ok {
		c.next++
		r = &authorentity.Result{ResultID: c.next, Kind: "person", Script: n.script, Family: n.family,
			Given: n.given, Additional: n.additional, Suffix: n.suffix, Nickname: n.nickname,
			DisplayName: n.display(), SortName: n.display()}
		c.results[fp] = r
	}
	c.in.Credits = append(c.in.Credits, authorentity.Credit{
		ID: id, BookID: book, Fingerprint: fp, ExtractorVersion: "v1", Tag: n.tag, Result: r,
	})
	return c
}

// collective adds a credit whose result is a collective: a name and no
// person parts.
func (c *catalog) collective(id, book int64, fp, display string) *catalog {
	c.t.Helper()
	r, ok := c.results[fp]
	if !ok {
		c.next++
		r = &authorentity.Result{ResultID: c.next, Kind: authorentity.KindCollective, Script: "Cyrl",
			DisplayName: display, SortName: display}
		c.results[fp] = r
	}
	c.in.Credits = append(c.in.Credits, authorentity.Credit{ID: id, BookID: book, Fingerprint: fp,
		ExtractorVersion: "v1", Result: r})
	return c
}

func (c *catalog) build() *authorentity.Plan {
	c.t.Helper()
	return authorentity.Build(&c.in)
}

func same(t *testing.T, p *authorentity.Plan, a, b int64) {
	t.Helper()
	ea, eb := p.EntityOf(a), p.EntityOf(b)
	if ea == uuid.Nil || eb == uuid.Nil || ea != eb {
		t.Errorf("credits %d and %d: want one entity, got %v and %v", a, b, ea, eb)
	}
}

func apart(t *testing.T, p *authorentity.Plan, a, b int64) {
	t.Helper()
	ea, eb := p.EntityOf(a), p.EntityOf(b)
	if ea == uuid.Nil || eb == uuid.Nil || ea == eb {
		t.Errorf("credits %d and %d: want two entities, got %v and %v", a, b, ea, eb)
	}
}

func none(t *testing.T, p *authorentity.Plan, a int64) {
	t.Helper()
	if e := p.EntityOf(a); e != uuid.Nil {
		t.Errorf("credit %d: want no entity, got %v", a, e)
	}
}
