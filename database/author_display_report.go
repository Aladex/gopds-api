package database

import (
	"context"
	"strings"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The comparison of the legacy authors with the author layer, book by book,
// that the read-path switch is judged by. It carries counts and book IDs
// only: never a name.

// Report categories by the words of a book's two author lists (compared
// without order, case, ё/е or punctuation); every book is in exactly one.
const (
	ReportWordsSame        = "words_same"         // the same words, perhaps reordered
	ReportWordsLayerExtra  = "words_layer_extra"  // the layer has more: mostly a patronymic
	ReportWordsLegacyExtra = "words_legacy_extra" // the catalog has more
	ReportWordsBothDiffer  = "words_both_differ"  // each has words the other lacks
	ReportWordsNoLayer     = "words_no_layer"     // no author credits
	ReportWordsNoLegacy    = "words_no_legacy"    // credits, and no legacy author's word
)

// Report categories of a book's shape; a book may be in several or none.
const (
	// ReportMultiAuthor: two or more author credits; the order becomes the file's.
	ReportMultiAuthor = "multi_author"
	// ReportAuthorCountDiffers: credits and legacy authors both present, in
	// different numbers (the placeholder counts as no legacy author).
	ReportAuthorCountDiffers = "author_count_differs"
	// ReportLegacyPlaceholder: the catalog links the unknown-author placeholder.
	ReportLegacyPlaceholder = "legacy_placeholder"
	// ReportFileNamesTitleCased: a name shown as the file spells it lost its
	// capitals for display.
	ReportFileNamesTitleCased = "file_names_title_cased"
)

// Report categories of the author line the book gets; every book is in
// exactly one of the three display categories.
const (
	ReportDisplayLayer           = "display_layer"
	ReportDisplayLegacyNoCredits = "display_legacy_no_credits"
	// ReportDisplayLegacyUnmatched: kept on the catalog because its legacy
	// authors carry a word no credit has; the edits the line protects.
	ReportDisplayLegacyUnmatched = "display_legacy_unmatched"
	// ReportLayerUnlinkedName: shown from the layer with a name that links
	// nowhere.
	ReportLayerUnlinkedName = "layer_unlinked_name"
	// ReportLayerUnlinkedWithLegacy: the same, while the book has a legacy
	// author — what an edit that removed an author by hand would look like,
	// and the one edit the line cannot tell from the file.
	ReportLayerUnlinkedWithLegacy = "layer_unlinked_with_legacy"
)

// ReportCategories lists every category of the report, in reading order.
var ReportCategories = []string{
	ReportWordsSame, ReportWordsLayerExtra, ReportWordsLegacyExtra, ReportWordsBothDiffer,
	ReportWordsNoLayer, ReportWordsNoLegacy,
	ReportMultiAuthor, ReportAuthorCountDiffers, ReportLegacyPlaceholder, ReportFileNamesTitleCased,
	ReportDisplayLayer, ReportDisplayLegacyNoCredits, ReportDisplayLegacyUnmatched,
	ReportLayerUnlinkedName, ReportLayerUnlinkedWithLegacy,
}

// reportSamples is how many book IDs a category keeps.
const reportSamples = 20

// reportBatch is how many books one query of the report reads.
const reportBatch = 1000

// AuthorDisplayCount is how many books fell into a category and the IDs of
// the first of them, in catalog order.
type AuthorDisplayCount struct {
	Books   int     `json:"books"`
	Samples []int64 `json:"samples"`
}

// AuthorDisplayReport compares the legacy authors with the author layer over
// a window of the catalog: the books with IDs above AfterID, in ID order.
// NextAfterID is where the next window starts, nil once the catalog is
// read to its end.
type AuthorDisplayReport struct {
	AfterID         int64                          `json:"after_id"`
	NextAfterID     *int64                         `json:"next_after_id"`
	Books           int                            `json:"books"`
	Categories      map[string]*AuthorDisplayCount `json:"categories"`
	CreditsLinked   int                            `json:"credits_linked"`
	CreditsUnlinked int                            `json:"credits_unlinked"`
}

func newAuthorDisplayReport(afterID int64) *AuthorDisplayReport {
	r := &AuthorDisplayReport{AfterID: afterID, Categories: make(map[string]*AuthorDisplayCount, len(ReportCategories))}
	for _, c := range ReportCategories {
		r.Categories[c] = &AuthorDisplayCount{Samples: []int64{}}
	}
	return r
}

func (r *AuthorDisplayReport) count(category string, bookID int64) {
	c := r.Categories[category]
	c.Books++
	if len(c.Samples) < reportSamples {
		c.Samples = append(c.Samples, bookID)
	}
}

// add sorts one book into its categories.
func (r *AuthorDisplayReport) add(bookID int64, s *bookAuthorSources) {
	r.Books++
	r.count(wordsCategory(s), bookID)
	legacyAuthors := r.addShape(bookID, s)
	r.addDisplay(bookID, s, legacyAuthors)
}

// addShape counts the book's shape and returns how many legacy authors it
// has that are authors at all.
func (r *AuthorDisplayReport) addShape(bookID int64, s *bookAuthorSources) int {
	counted := 0
	for _, a := range distinctLegacyAuthors(s.legacy) {
		if a.Name == models.UnknownAuthorName {
			r.count(ReportLegacyPlaceholder, bookID)
		}
		if namesAnAuthor(a) {
			counted++
		}
	}
	if len(s.credits) > 1 {
		r.count(ReportMultiAuthor, bookID)
	}
	if len(s.credits) > 0 && counted > 0 && len(s.credits) != counted {
		r.count(ReportAuthorCountDiffers, bookID)
	}
	for _, c := range s.credits {
		if !c.Selected && displayCase(c.Name) != c.Name {
			r.count(ReportFileNamesTitleCased, bookID)
			break
		}
	}
	return counted
}

// addDisplay counts the author line the book gets and the links of a line
// from the layer.
func (r *AuthorDisplayReport) addDisplay(bookID int64, s *bookAuthorSources, legacyAuthors int) {
	line := resolveAuthorDisplay(s)
	switch {
	case line.Source == models.AuthorDisplayLayer:
		r.count(ReportDisplayLayer, bookID)
	case line.Fallback == models.AuthorDisplayNoCredits:
		r.count(ReportDisplayLegacyNoCredits, bookID)
		return
	default:
		r.count(ReportDisplayLegacyUnmatched, bookID)
		return
	}
	unlinked := 0
	for _, a := range line.Authors {
		if a.LegacyAuthorID == nil {
			unlinked++
		}
	}
	r.CreditsLinked += len(line.Authors) - unlinked
	r.CreditsUnlinked += unlinked
	if unlinked > 0 {
		r.count(ReportLayerUnlinkedName, bookID)
		if legacyAuthors > 0 {
			r.count(ReportLayerUnlinkedWithLegacy, bookID)
		}
	}
}

// wordsCategory compares the words of every legacy author with the words of
// every author credit of the book.
func wordsCategory(s *bookAuthorSources) string {
	if len(s.credits) == 0 {
		return ReportWordsNoLayer
	}
	var legacyNames, creditNames []string
	for _, a := range s.legacy {
		legacyNames = append(legacyNames, a.Name)
	}
	for _, c := range s.credits {
		creditNames = append(creditNames, c.Name)
	}
	legacyWords := nameWords(strings.Join(legacyNames, " "))
	creditWords := nameWords(strings.Join(creditNames, " "))
	if len(legacyWords) == 0 {
		return ReportWordsNoLegacy
	}
	legacyWithin, creditsWithin := wordsWithin(legacyWords, creditWords), wordsWithin(creditWords, legacyWords)
	switch {
	case legacyWithin && creditsWithin:
		return ReportWordsSame
	case legacyWithin:
		return ReportWordsLayerExtra
	case creditsWithin:
		return ReportWordsLegacyExtra
	default:
		return ReportWordsBothDiffer
	}
}

// CompareAuthorDisplay reports on up to maxBooks books of the catalog with
// IDs above afterID, in ID order, reading them a batch at a time; maxBooks
// of zero or less reads to the end of the catalog.
func CompareAuthorDisplay(ctx context.Context, db pg.DBI, afterID int64, maxBooks int) (*AuthorDisplayReport, error) {
	r := newAuthorDisplayReport(afterID)
	cursor := afterID
	for maxBooks <= 0 || r.Books < maxBooks {
		limit := reportBatch
		if maxBooks > 0 && maxBooks-r.Books < limit {
			limit = maxBooks - r.Books
		}
		var ids []int64
		if _, err := db.QueryContext(ctx, &ids,
			`SELECT id FROM opds_catalog_book WHERE id > ? ORDER BY id LIMIT ?`, cursor, limit); err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			return r, nil
		}
		sources, err := loadBookAuthorSources(ctx, db, ids)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			r.add(id, sources[id])
		}
		cursor = ids[len(ids)-1]
		if len(ids) < limit {
			return r, nil
		}
	}
	// The window is full; it points onward only if a book lies beyond it, so
	// a window ending on the last book closes the walk itself.
	var more bool
	if _, err := db.QueryOneContext(ctx, pg.Scan(&more),
		`SELECT EXISTS (SELECT 1 FROM opds_catalog_book WHERE id > ?)`, cursor); err != nil {
		return nil, err
	}
	if more {
		next := cursor
		r.NextAfterID = &next
	}
	return r, nil
}
