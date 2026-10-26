package authorentity

import (
	"slices"
	"strings"

	"github.com/google/uuid"
)

// Report is what one pass found: counts and IDs, never a name (§4.1).
type Report struct {
	// Credits, Books and Fingerprints are the selected author credits read.
	Credits      int `json:"credits"`
	Books        int `json:"books"`
	Fingerprints int `json:"fingerprints"`
	// Entities are the live entities after the pass, by kind.
	Entities       int            `json:"entities"`
	EntitiesByKind map[string]int `json:"entities_by_kind"`
	// InitialsEntities are entities of a name of initials, left to E4.
	InitialsEntities int `json:"initials_entities"`
	// Sizes is the distribution of entities by books; Top the largest.
	Sizes []SizeBucket   `json:"sizes"`
	Top   []TopEntity    `json:"top"`
	Rules map[string]int `json:"fingerprints_by_rule"`
	// D2b is the field-split merge's components.
	D2b D2bReport `json:"d2b"`
	// TypoMerges counts the patronymic typos merged on evidence.
	TypoMerges int            `json:"typo_merges"`
	NoEntity   map[string]int `json:"no_entity"`
	Buckets    BucketReport   `json:"buckets"`
	IDs        IDReport       `json:"ids"`
	Legacy     LegacyReport   `json:"legacy"`
	Wikidata   WikidataReport `json:"wikidata"`
	// Changes counts the rows the pass writes.
	Changes int `json:"changes"`
}

// D2bReport is the field-split merge (E4·D2b): the word-sequence components
// by size, with their pair edges and unions — a component of n keys has
// n(n-1)/2 edges and, joined, n-1 unions, refused none — and the components
// left apart.
type D2bReport struct {
	Sizes []D2bSize `json:"sizes"`
	// EdgesByClass are the pair edges by the shape of their two keys.
	EdgesByClass map[string]int `json:"pair_edges_by_class"`
	// Refused are the components left apart by reason; RefusedEntities the
	// entities of the first ones after the pass.
	Refused         map[string]int `json:"refused"`
	RefusedEntities [][]uuid.UUID  `json:"refused_entities"`
}

// D2bSize is one row of the D2b components by their number of keys.
type D2bSize struct {
	Label      string `json:"label"`
	Components int    `json:"components"`
	Groups     int    `json:"groups"`
	PairEdges  int    `json:"pair_edges"`
	Unions     int    `json:"unions"`
	Refused    int    `json:"refused"`
}

// The classes of a D2b pair edge: either key of initials, else either key
// without a family or a given name, else plain.
const (
	D2bClassPlain     = "plain"
	D2bClassInitials  = "initials"
	D2bClassEmptyPart = "empty_part"
)

// D2bRefusedCoCredited is a component two of whose keys share a book.
const D2bRefusedCoCredited = "co_credited"

// d2bSizes are the rows of the D2b report by component size.
var d2bSizes = []struct {
	label    string
	min, max int
}{{"2", 2, 2}, {"3", 3, 3}, {"4+", 4, 1 << 31}}

func newD2bReport() D2bReport {
	r := D2bReport{Sizes: make([]D2bSize, len(d2bSizes)), RefusedEntities: [][]uuid.UUID{},
		EdgesByClass: map[string]int{D2bClassPlain: 0, D2bClassInitials: 0, D2bClassEmptyPart: 0},
		Refused:      map[string]int{D2bRefusedCoCredited: 0}}
	for i, s := range d2bSizes {
		r.Sizes[i].Label = s.label
	}
	return r
}

// BucketReport is the short names' buckets (§2.0). A bucket book is one
// credit of a short name: a book carries a name once.
type BucketReport struct {
	Buckets int `json:"buckets"`
	Books   int `json:"books"`
	// WithFullNames are the buckets with at least one full name of the same
	// family and given name — the ones D3 has work in — and their books.
	WithFullNames      int `json:"with_full_names"`
	WithFullNamesBooks int `json:"with_full_names_books"`
	// Moved are the books D3 moved to a full name, MovedBy by each kind of
	// evidence they had (a book may have several), MovedTitleOnly by the
	// title alone.
	Moved          int            `json:"moved"`
	MovedBy        map[string]int `json:"moved_by"`
	MovedTitleOnly int            `json:"moved_title_only"`
	// Conflicting books had evidence for two or more full names and stayed.
	Conflicting int `json:"conflicting"`
	// SamizdatBooks are the bucket books with "[СИ]" in the title;
	// SamizdatKept the ones that had evidence and stayed (decision 3b).
	SamizdatBooks int `json:"samizdat_books"`
	SamizdatKept  int `json:"samizdat_kept"`
	Remaining     int `json:"remaining"`
	// Emptied buckets lost every book; Entities are the buckets that stay,
	// Sizes their distribution by books.
	Emptied  int          `json:"emptied"`
	Entities int          `json:"entities"`
	Sizes    []SizeBucket `json:"sizes"`
}

// IDReport is what the pass did to the IDs (§3).
type IDReport struct {
	Inherited int `json:"inherited"`
	New       int `json:"new"`
	// Merged stored entities went into another (merged_into); Orphaned are
	// stored live entities no current credit belongs to any more.
	Merged   int `json:"merged"`
	Orphaned int `json:"orphaned"`
}

// LegacyReport compares the entities with the legacy authors (§4.1).
type LegacyReport struct {
	// Authors are the legacy authors with matched credits; EntitiesPerAuthor
	// is into how many entities each one's credits split.
	Authors           int          `json:"authors"`
	EntitiesPerAuthor []SizeBucket `json:"entities_per_author"`
	TopSplit          []TopLegacy  `json:"top_split"`
	// SharedEntities gather the credits of two or more legacy authors;
	// AuthorsPerEntity is the distribution over entities with legacy credits.
	SharedEntities   int          `json:"shared_entities"`
	AuthorsPerEntity []SizeBucket `json:"authors_per_entity"`
	TopShared        []TopEntity  `json:"top_shared"`
}

// SizeBucket is one row of a distribution.
type SizeBucket struct {
	Label    string `json:"label"`
	Entities int    `json:"entities"`
	Books    int    `json:"books"`
}

// TopEntity is an entity by ID with its books (and its legacy authors in the
// comparison).
type TopEntity struct {
	ID      uuid.UUID `json:"id"`
	Books   int       `json:"books"`
	Authors int       `json:"legacy_authors,omitempty"`
}

// TopLegacy is a legacy author by ID with the entities its credits split
// into.
type TopLegacy struct {
	ID       int64 `json:"id"`
	Entities int   `json:"entities"`
	Books    int   `json:"books"`
}

// topN bounds the top lists.
const topN = 20

func newReport() Report {
	return Report{EntitiesByKind: map[string]int{}, Rules: map[string]int{}, NoEntity: map[string]int{},
		Buckets: BucketReport{MovedBy: map[string]int{}}, Wikidata: newWikidataReport(), D2b: newD2bReport()}
}

// bookSizes are the distribution rows by books (P8 of the design).
var bookSizes = []struct {
	label    string
	min, max int
}{{"1", 1, 1}, {"2-5", 2, 5}, {"6-20", 6, 20}, {"21-100", 21, 100}, {"100+", 101, 1 << 31}}

// countSizes are the distribution rows by a small count (entities per legacy
// author, legacy authors per entity).
var countSizes = []struct {
	label    string
	min, max int
}{{"1", 1, 1}, {"2", 2, 2}, {"3+", 3, 1 << 31}}

func distribution(sizes []struct {
	label    string
	min, max int
}, values []int, books []int) []SizeBucket {
	out := make([]SizeBucket, len(sizes))
	for i, s := range sizes {
		out[i].Label = s.label
	}
	for i, v := range values {
		for j, s := range sizes {
			if v >= s.min && v <= s.max {
				out[j].Entities++
				out[j].Books += books[i]
			}
		}
	}
	return out
}

// report fills the counts of the pass.
func (b *builder) report(p *Plan, groups []*group, members map[uuid.UUID][]int, wantFP map[FPKey]AssignmentRow) {
	r := &b.rep
	for _, row := range wantFP {
		rule := row.Rule
		if IsWikidataRule(rule) {
			// One rule per item: counted together.
			rule = strings.TrimSuffix(ruleWikidata, ":")
		}
		r.Rules[rule]++
	}
	bucketOf := map[uuid.UUID]bool{}
	initialsOf := map[uuid.UUID]bool{}
	for _, g := range groups {
		bucketOf[g.id] = g.bucket
		initialsOf[g.id] = b.keys[g.root].key.initials()
	}
	entityBooks := map[uuid.UUID]int{}
	var all, allBooks, bucketSizes, bucketBooks []int
	var top []TopEntity
	for id, e := range p.entities {
		books := map[int64]bool{}
		for _, ci := range members[id] {
			books[b.in.Credits[ci].BookID] = true
		}
		n := len(books)
		entityBooks[id] = n
		r.Entities++
		r.EntitiesByKind[e.Kind]++
		if initialsOf[id] {
			r.InitialsEntities++
		}
		all, allBooks = append(all, n), append(allBooks, n)
		top = append(top, TopEntity{ID: id, Books: n})
		if bucketOf[id] {
			r.Buckets.Entities++
			bucketSizes, bucketBooks = append(bucketSizes, n), append(bucketBooks, n)
		}
	}
	r.Sizes = distribution(bookSizes, all, allBooks)
	r.Buckets.Sizes = distribution(bookSizes, bucketSizes, bucketBooks)
	r.Top = topEntities(top)

	r.Legacy = b.legacyReport(p, entityBooks)
	r.D2b.RefusedEntities = b.d2bRefusedEntities(p)
}

// d2bRefusedEntities are the entities after the pass of the first
// components D2b left apart, for the hand check.
func (b *builder) d2bRefusedEntities(p *Plan) [][]uuid.UUID {
	out := [][]uuid.UUID{}
	for _, keys := range b.d2bRefused[:min(topN, len(b.d2bRefused))] {
		seen := map[uuid.UUID]bool{}
		for _, k := range keys {
			for _, ci := range b.keys[k].credits {
				if id := p.entityOf[b.in.Credits[ci].ID]; id != uuid.Nil {
					seen[id] = true
				}
			}
		}
		ids := make([]uuid.UUID, 0, len(seen))
		for id := range seen {
			ids = append(ids, id)
		}
		slices.SortFunc(ids, func(x, y uuid.UUID) int { return strings.Compare(x.String(), y.String()) })
		out = append(out, ids)
	}
	return out
}

// legacyReport compares the entities with the legacy authors (§4.1): per
// legacy author, into how many entities its credits split; per entity, how
// many legacy authors' credits it gathers.
func (b *builder) legacyReport(p *Plan, entityBooks map[uuid.UUID]int) LegacyReport {
	l := LegacyReport{EntitiesPerAuthor: distribution(countSizes, nil, nil),
		AuthorsPerEntity: distribution(countSizes, nil, nil), TopSplit: []TopLegacy{}, TopShared: []TopEntity{}}
	if len(b.in.Legacy) == 0 {
		return l
	}
	authorEntities, authorBooks, entityAuthors := b.legacyMembership(p)
	var perAuthor, perAuthorBooks []int
	var split []TopLegacy
	for _, legacy := range sortedKeys(authorEntities) {
		n, books := len(authorEntities[legacy]), len(authorBooks[legacy])
		perAuthor, perAuthorBooks = append(perAuthor, n), append(perAuthorBooks, books)
		split = append(split, TopLegacy{ID: legacy, Entities: n, Books: books})
	}
	sortSplit(split)
	var perEntity, perEntityBooks []int
	var shared []TopEntity
	for id, authors := range entityAuthors {
		perEntity, perEntityBooks = append(perEntity, len(authors)), append(perEntityBooks, entityBooks[id])
		if len(authors) > 1 {
			l.SharedEntities++
			shared = append(shared, TopEntity{ID: id, Books: entityBooks[id], Authors: len(authors)})
		}
	}
	sortShared(shared)
	l.Authors = len(authorEntities)
	l.EntitiesPerAuthor = distribution(countSizes, perAuthor, perAuthorBooks)
	l.AuthorsPerEntity = distribution(countSizes, perEntity, perEntityBooks)
	l.TopSplit = split[:min(topN, len(split))]
	l.TopShared = shared[:min(topN, len(shared))]
	if l.TopShared == nil {
		l.TopShared = []TopEntity{}
	}
	return l
}

// legacyMembership is, per legacy author, its credits' entities and books,
// and per entity its credits' legacy authors.
func (b *builder) legacyMembership(p *Plan) (
	authorEntities map[int64]map[uuid.UUID]bool, authorBooks map[int64]map[int64]bool,
	entityAuthors map[uuid.UUID]map[int64]bool,
) {
	authorEntities, authorBooks = map[int64]map[uuid.UUID]bool{}, map[int64]map[int64]bool{}
	entityAuthors = map[uuid.UUID]map[int64]bool{}
	for i := range b.in.Credits {
		c := &b.in.Credits[i]
		legacy, ok := b.in.Legacy[c.ID]
		id := p.entityOf[c.ID]
		if !ok || id == uuid.Nil {
			continue
		}
		if authorEntities[legacy] == nil {
			authorEntities[legacy], authorBooks[legacy] = map[uuid.UUID]bool{}, map[int64]bool{}
		}
		authorEntities[legacy][id] = true
		authorBooks[legacy][c.BookID] = true
		if entityAuthors[id] == nil {
			entityAuthors[id] = map[int64]bool{}
		}
		entityAuthors[id][legacy] = true
	}
	return authorEntities, authorBooks, entityAuthors
}

// sortSplit orders legacy authors by the entities they split into, then by
// books; the input is in ID order and the sort is stable.
func sortSplit(split []TopLegacy) {
	slices.SortStableFunc(split, func(x, y TopLegacy) int {
		if x.Entities != y.Entities {
			return y.Entities - x.Entities
		}
		return y.Books - x.Books
	})
}

// sortShared orders entities by the legacy authors they gather, then by
// books, then by ID.
func sortShared(shared []TopEntity) {
	slices.SortFunc(shared, func(x, y TopEntity) int {
		switch {
		case x.Authors != y.Authors:
			return y.Authors - x.Authors
		case x.Books != y.Books:
			return y.Books - x.Books
		}
		return strings.Compare(x.ID.String(), y.ID.String())
	})
}

// topEntities are the largest entities by books, then by ID.
func topEntities(all []TopEntity) []TopEntity {
	slices.SortFunc(all, func(x, y TopEntity) int {
		if x.Books != y.Books {
			return y.Books - x.Books
		}
		return strings.Compare(x.ID.String(), y.ID.String())
	})
	out := all[:min(topN, len(all))]
	if out == nil {
		out = []TopEntity{}
	}
	return out
}
