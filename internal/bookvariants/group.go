package bookvariants

import (
	"cmp"
	"crypto/md5" // #nosec G501 -- a key with an SQL twin (md5()), not a security use
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

// Input is what the rule reads of one visible book.
type Input struct {
	BookID int64
	Lang   string
	Title  string
	// EntityIDs are the entities of the book's author line; Unlinked says a
	// name of the line links to none. EntityNames are their display names,
	// in any order, for IsPlaceholderAuthor.
	EntityIDs   []string
	Unlinked    bool
	EntityNames []string
	// Series maps a series to the book's number in it; unnumbered entries
	// (number 0) are left out by the caller or ignored here.
	Series map[int64]int64
	// DocumentID and DocVersion are the FB2 document's id and version from the
	// current metadata snapshot; DocDate is the catalog's document date.
	DocumentID   string
	DocVersion   string
	DocDate      string
	RegisterDate time.Time
	// What makes a file complete: a cover, an annotation, a publisher, a year.
	Cover, Annotation, Publisher, Year bool
}

// Kind is what a card holds.
type Kind string

// The kinds of a card.
const (
	KindSingle    Kind = "single"
	KindVariants  Kind = "variants"
	KindRevisions Kind = "revisions"
)

// Mark is what the feed says of a card whose newest file came later than the
// rest.
type Mark string

// The feed marks.
const (
	MarkNone        Mark = ""
	MarkNewRevision Mark = "new_revision"
	MarkNewVariant  Mark = "new_variant"
)

// Member is one file's place in its card.
type Member struct {
	BookID int64
	// Lineage is the file's FB2 document, lowercased, or "book:<id>" for a
	// file without one.
	Lineage string
	// Latest marks the newest revision of its lineage in the card.
	Latest bool
	// VariantRank orders the card's files; 1 is the default variant.
	VariantRank int
	// Tag is the title's stripped trailing tags, joined with ", ".
	Tag string
}

// Group is one card.
type Group struct {
	Key  uuid.UUID
	Base string
	// Members are ordered by VariantRank.
	Members []Member
	Kind    Kind
	// Count is what the card says it holds: documents for variants, files
	// for revisions, 1 for a single file.
	Count        int
	NewestBookID int64
	Mark         Mark
}

const (
	baseSeparator  = "\x1f"
	splitSeparator = "\x1e"
)

// BaseKey is the language, the entity set and the title key of a book, or ""
// when the book is a card of its own: a junk title, no entity, a name that
// links to none, or a placeholder among its authors.
func BaseKey(in *Input) string {
	key, _, groupable := NormalizeTitle(in.Title)
	if !groupable || len(in.EntityIDs) == 0 || in.Unlinked {
		return ""
	}
	for _, name := range in.EntityNames {
		if IsPlaceholderAuthor(name) {
			return ""
		}
	}
	ids := slices.Clone(in.EntityIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	return strings.ToLower(strings.TrimSpace(in.Lang)) + baseSeparator + strings.Join(ids, ",") + baseSeparator + key
}

// SoloKey is the card key of a book that is a card of its own: the UUID of
// md5("book:<id>"), which SQL computes as md5('book:' || id)::uuid.
func SoloKey(bookID int64) uuid.UUID {
	return uuid.UUID(md5.Sum([]byte("book:" + strconv.FormatInt(bookID, 10)))) // #nosec G401 -- see the import
}

// GroupKey is the card key of a base split by series: the UUID of the md5 of
// both.
func GroupKey(base, split string) uuid.UUID {
	return uuid.UUID(md5.Sum([]byte(base + splitSeparator + split))) // #nosec G401 -- see the import
}

// Build makes the cards of books that share one base key. With an empty base
// every book is a card of its own.
//
// Two books numbered differently in one series are different volumes, so a
// series in which the books carry two or more numbers splits the base: a book
// numbered in it joins the card of its number (the lowest such series decides
// for a book numbered in several), and a book numbered in none of them is a
// card of its own rather than a guess at its volume.
func Build(base string, books []Input) []Group {
	if base == "" {
		out := make([]Group, len(books))
		for i := range books {
			out[i] = buildGroup(SoloKey(books[i].BookID), base, books[i:i+1])
		}
		return out
	}
	conflicts := seriesConflicts(books)
	splits := map[string][]Input{}
	var order []string
	for i := range books {
		split := splitOf(&books[i], conflicts)
		if _, seen := splits[split]; !seen {
			order = append(order, split)
		}
		splits[split] = append(splits[split], books[i])
	}
	slices.Sort(order)
	out := make([]Group, 0, len(order))
	for _, split := range order {
		out = append(out, buildGroup(GroupKey(base, split), base, splits[split]))
	}
	return out
}

// seriesConflicts are the series in which the books carry two or more
// numbers, ascending.
func seriesConflicts(books []Input) []int64 {
	numbers := map[int64]map[int64]bool{}
	for i := range books {
		for series, no := range books[i].Series {
			if no <= 0 {
				continue
			}
			if numbers[series] == nil {
				numbers[series] = map[int64]bool{}
			}
			numbers[series][no] = true
		}
	}
	var out []int64
	for series, nos := range numbers {
		if len(nos) > 1 {
			out = append(out, series)
		}
	}
	slices.Sort(out)
	return out
}

func splitOf(b *Input, conflicts []int64) string {
	if len(conflicts) == 0 {
		return ""
	}
	for _, series := range conflicts {
		if no := b.Series[series]; no > 0 {
			return fmt.Sprintf("%d:%d", series, no)
		}
	}
	return fmt.Sprintf("own:%d", b.BookID)
}

// file is a book with what ranking reads of it.
type file struct {
	in      Input
	lineage string
	docDate time.Time
	tag     string
}

func newFile(in *Input) file {
	f := file{in: *in, lineage: strings.ToLower(strings.TrimSpace(in.DocumentID))}
	if f.lineage == "" {
		f.lineage = "book:" + strconv.FormatInt(in.BookID, 10)
	}
	f.docDate, _ = ParseDocDate(in.DocDate)
	_, tags, _ := NormalizeTitle(in.Title)
	f.tag = strings.Join(tags, ", ")
	return f
}

func (f *file) completeness() int {
	n := 0
	for _, has := range []bool{f.in.Cover, f.in.Annotation, f.in.Publisher, f.in.Year} {
		if has {
			n++
		}
	}
	return n
}

// newerRevision orders one lineage's files newest first: the higher
// version, then the later document date, then the higher ID.
func newerRevision(a, b *file) int {
	return cmp.Or(
		-CompareDocVersion(a.in.DocVersion, b.in.DocVersion),
		b.docDate.Compare(a.docDate),
		cmp.Compare(b.in.BookID, a.in.BookID),
	)
}

// betterDefault orders the latest revisions of a card: the most complete,
// then the later document date, the higher version, the later added, the
// higher ID.
func betterDefault(a, b *file) int {
	return cmp.Or(
		cmp.Compare(b.completeness(), a.completeness()),
		b.docDate.Compare(a.docDate),
		-CompareDocVersion(a.in.DocVersion, b.in.DocVersion),
		b.in.RegisterDate.Compare(a.in.RegisterDate),
		cmp.Compare(b.in.BookID, a.in.BookID),
	)
}

// buildGroup ranks one card's files: the latest revision of each lineage
// first, best default first, then each lineage's older revisions under it, in
// the order the latest ones have.
func buildGroup(key uuid.UUID, base string, books []Input) Group {
	lineages := map[string][]*file{}
	var newest *file
	for i := range books {
		f := newFile(&books[i])
		lineages[f.lineage] = append(lineages[f.lineage], &f)
		if newest == nil || f.in.BookID > newest.in.BookID {
			newest = &f
		}
	}
	heads := make([]*file, 0, len(lineages))
	for _, files := range lineages {
		slices.SortFunc(files, newerRevision)
		heads = append(heads, files[0])
	}
	slices.SortFunc(heads, betterDefault)

	g := Group{Key: key, Base: base, Members: make([]Member, 0, len(books)), NewestBookID: newest.in.BookID}
	add := func(f *file, latest bool) {
		g.Members = append(g.Members, Member{
			BookID: f.in.BookID, Lineage: f.lineage, Latest: latest, VariantRank: len(g.Members) + 1, Tag: f.tag,
		})
	}
	for _, h := range heads {
		add(h, true)
	}
	for _, h := range heads {
		for _, f := range lineages[h.lineage][1:] {
			add(f, false)
		}
	}

	switch {
	case len(books) == 1:
		g.Kind, g.Count = KindSingle, 1
	case len(lineages) == 1:
		g.Kind, g.Count = KindRevisions, len(books)
	default:
		g.Kind, g.Count = KindVariants, len(lineages)
	}
	g.Mark = feedMark(newest, lineages)
	return g
}

// feedMark is set when the newest file was added on a later day than every
// other file of the card: a new revision when its document is already in the
// card, a new variant when it is not.
func feedMark(newest *file, lineages map[string][]*file) Mark {
	newestDay := dayOf(newest.in.RegisterDate)
	others := 0
	for _, files := range lineages {
		for _, f := range files {
			if f == newest {
				continue
			}
			others++
			if !dayOf(f.in.RegisterDate).Before(newestDay) {
				return MarkNone
			}
		}
	}
	switch {
	case others == 0:
		return MarkNone
	case len(lineages[newest.lineage]) > 1:
		return MarkNewRevision
	default:
		return MarkNewVariant
	}
}

func dayOf(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// CompareDocVersion compares two FB2 document versions number by number, so
// "1.10" is after "1.9" and "1.0" equals "1". A version with no number is
// before any version with one.
func CompareDocVersion(a, b string) int {
	x, y := versionNumbers(a), versionNumbers(b)
	if len(x) == 0 || len(y) == 0 {
		return cmp.Compare(len(x), len(y))
	}
	for i := 0; i < max(len(x), len(y)); i++ {
		var p, q uint64
		if i < len(x) {
			p = x[i]
		}
		if i < len(y) {
			q = y[i]
		}
		if c := cmp.Compare(p, q); c != 0 {
			return c
		}
	}
	return 0
}

// versionNumbers are the runs of digits in a version, as numbers; a run too
// long for one saturates.
func versionNumbers(v string) []uint64 {
	var out []uint64
	for _, run := range strings.FieldsFunc(v, func(r rune) bool { return !unicode.IsDigit(r) }) {
		n, err := strconv.ParseUint(run, 10, 64)
		if err != nil {
			n = ^uint64(0)
		}
		out = append(out, n)
	}
	return out
}

// docDateLayouts are the document dates the catalog holds in any number.
// The layouts of a document date.
const (
	layoutISODay    = "2006-01-02"
	layoutDottedDay = "02.01.2006"
	layoutYear      = "2006"
)

var docDateLayouts = []string{layoutISODay, layoutDottedDay, layoutYear}

// ParseDocDate reads a catalog document date: a day as 2006-01-02 or
// 02.01.2006, or a year alone, read as its first day.
func ParseDocDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range docDateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
