package authorentity

import (
	"cmp"
	"slices"
	"strings"

	"gopds-api/internal/authornorm"

	"github.com/google/uuid"
)

// Reasons a credit gets no entity.
const (
	NoEntityPlaceholder = "placeholder"
	NoEntityNoLetters   = "no_letters"
	// NoEntityManual is an administrator's "no entity" for the credit.
	NoEntityManual = "manual"
	// NoEntityItemFailed is a credit whose handling panicked: it is left
	// out, the pass goes on (§1.5).
	NoEntityItemFailed = "item_failed"
)

// Plan is one pass: the units to write and the report. Build makes it.
type Plan struct {
	// Units are the units with something to write; the last ones, with a
	// nil EntityID, remove the automatic rows no entity carries any more,
	// one per old entity of those rows.
	Units  []Unit
	Report Report

	components [][]int
	decisions  []WikidataDecision
	refs       map[uuid.UUID]string
	entityOf   map[int64]uuid.UUID
	ruleOf     map[int64]string
	entities   map[uuid.UUID]EntityRow
}

// EntityOf is the entity a credit belongs to after the pass, uuid.Nil when
// none.
func (p *Plan) EntityOf(creditID int64) uuid.UUID { return p.entityOf[creditID] }

// RuleOf is the rule that put a credit in its entity.
func (p *Plan) RuleOf(creditID int64) string { return p.ruleOf[creditID] }

// WikidataDecisions are the merges and moves the Wikidata evidence made,
// for the hand check.
func (p *Plan) WikidataDecisions() []WikidataDecision { return p.decisions }

// WikidataOf is the Wikidata item a live entity of the pass has after it:
// the pass's, or a pinned one; empty when none.
func (p *Plan) WikidataOf(id uuid.UUID) string { return p.refs[id] }

// Entity is a live entity of the pass, nil when there is none.
func (p *Plan) Entity(id uuid.UUID) *EntityRow {
	e, ok := p.entities[id]
	if !ok {
		return nil
	}
	return &e
}

// Components are the units grouped by dependence (see components): each
// group is written or refused as a whole, groups independently of each
// other.
func (p *Plan) Components() [][]Unit {
	out := make([][]Unit, len(p.components))
	for i, idx := range p.components {
		out[i] = make([]Unit, len(idx))
		for j, k := range idx {
			out[i][j] = p.Units[k]
		}
	}
	return out
}

// Changes counts the rows the plan writes.
func (p *Plan) Changes() int {
	n := 0
	for i := range p.Units {
		n += p.Units[i].Changes()
	}
	return n
}

// Apply writes every unit into an in-memory store.
func (p *Plan) Apply(s *State) {
	for i := range p.Units {
		p.Units[i].Apply(s)
	}
}

// keyGroup is the credits of one name key: D1 and D2 together.
type keyGroup struct {
	key     nameKey
	idx     int
	credits []int // automatic credits, by index into Input.Credits
	// fps and books are distinct and sorted once the credits are read.
	fps   []FPKey
	books []int64
}

// builder carries one pass.
type builder struct {
	in  *Input
	old *State
	rep Report

	fixed  map[int]*uuid.UUID // manual: credit index → entity (nil = none)
	keys   []*keyGroup
	byKey  map[string]*keyGroup
	parent []int
	// members are the keys of every component of more than one key, by
	// root, kept by union; a root without an entry is its own only key.
	members map[int][]int
	// Evidence indexes over the automatic credits: the keys on each book,
	// each book's title key, the books of each key per title, and per key
	// its series and the keys it shares a book with (nil when none).
	bookKeys  map[int64][]int
	titleOf   map[int64]string
	titleKeys map[string][]titleHit
	keySeries []map[int64]bool
	coauthors []map[int]bool

	movedTo   map[int]int // credit index → root of the full name it moved to
	movedRule map[int]string

	// d2bRoot is each key's root after D2b; d2bJoined reports a key of a
	// component D2b joined, d2bRefused the keys of every word-sequence
	// component it left apart.
	d2bRoot    []int
	d2bJoined  []bool
	d2bRefused [][]int
	// typoRoot is each key's root before the Wikidata merge; wdRule the
	// rule of every key a Wikidata merge put into another component;
	// fullBases the bases of the catalog's full names, once read.
	typoRoot    []int
	wdRule      map[int]string
	wdDecisions []WikidataDecision
	fullBases   map[string]bool
}

// Build computes one pass over the input.
func Build(in *Input) *Plan {
	b := &builder{in: in, old: in.Old, fixed: map[int]*uuid.UUID{}, byKey: map[string]*keyGroup{},
		members: map[int][]int{}, bookKeys: map[int64][]int{}, titleOf: map[int64]string{},
		titleKeys: map[string][]titleHit{}, movedTo: map[int]int{}, movedRule: map[int]string{}, wdRule: map[int]string{}}
	if b.old == nil {
		b.old = NewState()
	}
	b.rep = newReport()
	b.classify()
	b.indexEvidence()
	b.mergeFieldSplits()
	b.mergeTypos()
	b.mergeWikidata()
	b.moveShortNameBooks()
	return b.plan()
}

// classify sorts the credits: fixed by a manual row, without an entity, or
// keyed for the automatic groups. One credit's failure is its own (§1.5).
func (b *builder) classify() {
	cs := b.in.Credits
	books := map[int64]bool{}
	fps := map[FPKey]bool{}
	cache := map[int64]keyed{}
	for i := range cs {
		books[cs[i].BookID] = true
		fps[fpKeyOf(&cs[i])] = true
		if b.fixedByHand(i) {
			continue
		}
		key, reason := b.keyOfCredit(&cs[i], cache)
		if reason != "" {
			b.rep.NoEntity[reason]++
			continue
		}
		b.addToGroup(&key, i)
	}
	b.rep.Credits, b.rep.Books, b.rep.Fingerprints = len(cs), len(books), len(fps)
	b.orderKeys()
}

// fixedByHand records a credit a manual row decides — its own row first,
// then its fingerprint's (§1.2) — and reports whether there was one.
func (b *builder) fixedByHand(i int) bool {
	c := &b.in.Credits[i]
	if row, ok := b.old.Credits[CreditKey{CreditID: c.ID}]; ok && row.Method == MethodManual {
		b.fixed[i] = row.EntityID
		if row.EntityID == nil {
			b.rep.NoEntity[NoEntityManual]++
		}
		return true
	}
	if row, ok := b.old.Fingerprints[fpKeyOf(c)]; ok && row.Method == MethodManual {
		id := row.EntityID
		b.fixed[i] = &id
		return true
	}
	return false
}

// addToGroup adds credit i to the group of its key.
func (b *builder) addToGroup(key *nameKey, i int) {
	c := &b.in.Credits[i]
	g := b.byKey[key.String()]
	if g == nil {
		g = &keyGroup{key: *key}
		b.byKey[key.String()] = g
	}
	g.credits = append(g.credits, i)
	if fp := fpKeyOf(c); !slices.Contains(g.fps, fp) {
		g.fps = append(g.fps, fp)
	}
	g.books = append(g.books, c.BookID)
}

// orderKeys numbers the groups in key order, so nothing downstream depends
// on map order.
func (b *builder) orderKeys() {
	names := make([]string, 0, len(b.byKey))
	for k := range b.byKey {
		names = append(names, k)
	}
	slices.Sort(names)
	for i, k := range names {
		g := b.byKey[k]
		g.idx = i
		slices.Sort(g.books)
		g.books = slices.Compact(g.books)
		slices.SortFunc(g.fps, compareFPKeys)
		b.keys = append(b.keys, g)
	}
	b.parent = make([]int, len(b.keys))
	for i := range b.parent {
		b.parent[i] = i
	}
}

func fpKeyOf(c *Credit) FPKey {
	return FPKey{Fingerprint: c.Fingerprint, ExtractorVersion: c.ExtractorVersion}
}

// keyFunc is keyOf; a test swaps it to make one credit panic.
var keyFunc = keyOf

// keyed is a credit's key, or the reason it has no entity.
type keyed struct {
	key    nameKey
	reason string
}

// keyOfCredit is a credit's key or the reason it gets none. Every credit of
// a result has the same parts, so the key is computed once per result; a
// tagged credit's key is its own.
func (b *builder) keyOfCredit(c *Credit, cache map[int64]keyed) (key nameKey, reason string) {
	if k, ok := cache[c.ResultID]; ok && c.Tag == 0 {
		return k.key, k.reason
	}
	var k keyed
	if failed := isolate(func() {
		k.key = keyFunc(c)
		switch {
		case !k.key.hasLetterOrDigit():
			k.reason = NoEntityNoLetters
		case isPlaceholder(c):
			k.reason = NoEntityPlaceholder
		}
	}); failed {
		k = keyed{reason: NoEntityItemFailed}
	}
	if c.Tag == 0 && k.reason != NoEntityItemFailed {
		cache[c.ResultID] = k
	}
	return k.key, k.reason
}

// isolate runs fn and reports whether it panicked.
func isolate(fn func()) (failed bool) {
	defer func() {
		if recover() != nil {
			failed = true
		}
	}()
	fn()
	return false
}

func (b *builder) find(i int) int {
	for b.parent[i] != i {
		b.parent[i] = b.parent[b.parent[i]]
		i = b.parent[i]
	}
	return i
}

// union joins two key groups; the lower index is the root, so the result
// does not depend on the order of the unions.
func (b *builder) union(x, y int) {
	rx, ry := b.find(x), b.find(y)
	if rx == ry {
		return
	}
	if ry < rx {
		rx, ry = ry, rx
	}
	b.parent[ry] = rx
	b.members[rx] = slices.Concat(b.membersOf(rx), b.membersOf(ry))
	delete(b.members, ry)
}

// titleHit is one book of a key under a title.
type titleHit struct {
	key  int
	book int64
}

// indexEvidence builds the indexes the evidence reads.
func (b *builder) indexEvidence() {
	b.keySeries = make([]map[int64]bool, len(b.keys))
	b.coauthors = make([]map[int]bool, len(b.keys))
	for _, g := range b.keys {
		for _, book := range g.books {
			b.bookKeys[book] = append(b.bookKeys[book], g.idx)
			bk := b.in.Books[book]
			for _, sr := range bk.Series {
				if b.keySeries[g.idx] == nil {
					b.keySeries[g.idx] = map[int64]bool{}
				}
				b.keySeries[g.idx][sr] = true
			}
			t, ok := b.titleOf[book]
			if !ok {
				t = titleKey(bk.Title)
				b.titleOf[book] = t
			}
			if t != "" {
				b.titleKeys[t] = append(b.titleKeys[t], titleHit{key: g.idx, book: book})
			}
		}
	}
	for _, keys := range b.bookKeys {
		for _, k := range keys {
			for _, o := range keys {
				if o == k {
					continue
				}
				if b.coauthors[k] == nil {
					b.coauthors[k] = map[int]bool{}
				}
				b.coauthors[k][o] = true
			}
		}
	}
}

// underTitle reports a book of key under title t other than except.
func (b *builder) underTitle(t string, key int, except int64) bool {
	if t == "" {
		return false
	}
	for _, h := range b.titleKeys[t] {
		if h.key == key && h.book != except {
			return true
		}
	}
	return false
}

// fullNamesByBase are the full, evidence-eligible keys of each base.
func (b *builder) fullNamesByBase() map[nameKey][]int {
	out := map[nameKey][]int{}
	for _, g := range b.keys {
		if !g.key.short() && g.key.evidenceEligible() {
			out[g.key.base()] = append(out[g.key.base()], g.idx)
		}
	}
	return out
}

// sharesABook reports two keys with a credit on one book: two people.
func (b *builder) sharesABook(x, y int) bool {
	xs, ys := b.keys[x].books, b.keys[y].books
	for i, j := 0, 0; i < len(xs) && j < len(ys); {
		switch {
		case xs[i] == ys[j]:
			return true
		case xs[i] < ys[j]:
			i++
		default:
			j++
		}
	}
	return false
}

// keyEvidence is the evidence linking the books of two keys that share no
// book: a title of one on a book of the other, a shared series, a shared
// co-author.
func (b *builder) keyEvidence(x, y int) []string {
	var kinds []string
	for _, book := range b.keys[x].books {
		if b.underTitle(b.titleOf[book], y, 0) {
			kinds = append(kinds, EvidenceTitle)
			break
		}
	}
	for s := range b.keySeries[x] {
		if b.keySeries[y][s] {
			kinds = append(kinds, EvidenceSeries)
			break
		}
	}
	for o := range b.coauthors[x] {
		if o != y && b.coauthors[y][o] {
			kinds = append(kinds, EvidenceCoauthor)
			break
		}
	}
	return kinds
}

// minD2bWords is how few words a name D2b joins has: one word is no name
// whose fields the source could have split. minD2bKeys is how few keys a
// word-sequence component has: one key alone is no field split.
const (
	minD2bWords = 2
	minD2bKeys  = 2
)

// mergeFieldSplits is D2b (§2.0): untagged person keys of one script and
// suffix whose given, additional and family words read the same in that
// order — the source put a field boundary elsewhere — are one name. Such a
// component is joined whole or not at all: two of its keys credited on one
// book are two people the catalog itself names, and the whole component
// stays apart, whatever the order of its keys.
func (b *builder) mergeFieldSplits() {
	bySeq := map[string][]int{}
	for _, g := range b.keys {
		if seq, ok := g.key.wordSequence(); ok {
			bySeq[seq] = append(bySeq[seq], g.idx)
		}
	}
	r := &b.rep.D2b
	b.d2bJoined = make([]bool, len(b.keys))
	for _, seq := range sortedKeys(bySeq) {
		keys := bySeq[seq]
		n := len(keys)
		if n < minD2bKeys {
			continue
		}
		row := &r.Sizes[d2bSizeRow(n)]
		row.Components++
		row.Groups += n
		row.PairEdges += b.countD2bEdges(keys)
		if b.anyShareABook(keys) {
			row.Refused++
			r.Refused[D2bRefusedCoCredited]++
			b.d2bRefused = append(b.d2bRefused, keys)
			continue
		}
		for _, k := range keys[1:] {
			b.union(keys[0], k)
		}
		for _, k := range keys {
			b.d2bJoined[k] = true
		}
		row.Unions += n - 1
	}
	b.d2bRoot = make([]int, len(b.keys))
	for i := range b.keys {
		b.d2bRoot[i] = b.find(i)
	}
}

// d2bSizeRow is the report row of a component of n keys.
func d2bSizeRow(n int) int {
	for i, s := range d2bSizes {
		if n >= s.min && n <= s.max {
			return i
		}
	}
	return len(d2bSizes) - 1
}

// countD2bEdges counts every pair of a component by its class and returns
// how many pairs there are.
func (b *builder) countD2bEdges(keys []int) (edges int) {
	for i, x := range keys {
		for _, y := range keys[i+1:] {
			kx, ky := &b.keys[x].key, &b.keys[y].key
			class := D2bClassPlain
			switch {
			case kx.initials() || ky.initials():
				class = D2bClassInitials
			case kx.family == "" || kx.given == "" || ky.family == "" || ky.given == "":
				class = D2bClassEmptyPart
			}
			b.rep.D2b.EdgesByClass[class]++
			edges++
		}
	}
	return edges
}

// anyShareABook reports two of the keys credited on one book.
func (b *builder) anyShareABook(keys []int) bool {
	for i, x := range keys {
		for _, y := range keys[i+1:] {
			if b.sharesABook(x, y) {
				return true
			}
		}
	}
	return false
}

// minTypoNames is how many full names of one name a typo needs: itself and
// the one it misspells.
const minTypoNames = 2

// mergeTypos merges a full name with the nearest patronymic of the same
// name when the two differ by a typo and strong evidence links their books
// (§2.0). A name with two candidates at its nearest distance is ambiguous:
// no union reaches it from either side. Two names credited on one book are
// two people, and no chain of unions may put them in one entity.
func (b *builder) mergeTypos() {
	var edges []typoEdge
	for _, full := range b.fullNamesByBase() {
		if len(full) < minTypoNames {
			continue
		}
		nearest := map[int]typoEdge{}
		ambiguous := map[int]bool{}
		for _, x := range full {
			if e, ties := b.nearestTypo(x, full); ties == 1 {
				nearest[x] = e
			} else if ties > 1 {
				ambiguous[x] = true
			}
		}
		for x, e := range nearest {
			if !ambiguous[e.y] {
				edges = append(edges, typoEdge{d: e.d, x: min(x, e.y), y: max(x, e.y)})
			}
		}
	}
	slices.SortFunc(edges, func(p, q typoEdge) int {
		if p.d != q.d {
			return p.d - q.d
		}
		if p.x != q.x {
			return p.x - q.x
		}
		return p.y - q.y
	})
	for _, e := range edges {
		rx, ry := b.find(e.x), b.find(e.y)
		if rx == ry || b.componentsShareABook(rx, ry) {
			continue
		}
		b.union(rx, ry)
		b.rep.TypoMerges++
	}
}

// typoEdge is a typo merge of keys x and y at edit distance d.
type typoEdge struct{ d, x, y int }

// nearestTypo is x's nearest typo candidate with evidence among full, and
// how many candidates share that distance.
func (b *builder) nearestTypo(x int, full []int) (best typoEdge, ties int) {
	best.y = -1
	for _, y := range full {
		if x == y || b.sharesABook(x, y) {
			continue
		}
		d, ok := typoCandidates(b.keys[x].key.additional, b.keys[y].key.additional)
		if !ok || len(b.keyEvidence(x, y)) == 0 {
			continue
		}
		switch {
		case best.y < 0 || d < best.d:
			best, ties = typoEdge{d: d, x: x, y: y}, 1
		case d == best.d:
			ties++
		}
	}
	return best, ties
}

// membersOf are the keys of a component; a key alone is its own.
func (b *builder) membersOf(root int) []int {
	if m, ok := b.members[root]; ok {
		return m
	}
	return []int{root}
}

// componentsShareABook reports two components with keys credited on one
// book: the D2b unions and the typo merges before it count.
func (b *builder) componentsShareABook(rx, ry int) bool {
	for _, x := range b.membersOf(rx) {
		for _, y := range b.membersOf(ry) {
			if b.sharesABook(x, y) {
				return true
			}
		}
	}
	return false
}

// moveShortNameBooks is D3: each book of a short name moves to the one full
// name its evidence points to (§2.0); a samizdat book stays (decision 3b).
func (b *builder) moveShortNameBooks() {
	byBase := b.fullNamesByBase()
	for _, g := range b.keys {
		if !b.isBucketKey(g.idx) {
			continue
		}
		b.rep.Buckets.Buckets++
		full := byBase[g.key.base()]
		if len(full) > 0 {
			b.rep.Buckets.WithFullNames++
			b.rep.Buckets.WithFullNamesBooks += len(g.credits)
		}
		for _, ci := range g.credits {
			b.moveShortNameBook(ci, g.idx, full)
		}
	}
	b.rep.Buckets.Remaining = b.rep.Buckets.Books - b.rep.Buckets.Moved
}

// isBucketKey reports a short name's bucket key: short, evidence-eligible,
// and not in a component D2b joined — such a key is a full name the source
// split into other fields, and D3 neither moves nor counts its books.
func (b *builder) isBucketKey(k int) bool {
	key := &b.keys[k].key
	return key.short() && key.evidenceEligible() && !b.d2bJoined[k]
}

// moveShortNameBook decides one book of a short name: it moves when its
// evidence points to exactly one full name and it is not samizdat.
func (b *builder) moveShortNameBook(ci, short int, full []int) {
	b.rep.Buckets.Books++
	c := &b.in.Credits[ci]
	samizdat := isSamizdat(b.in.Books[c.BookID].Title)
	if samizdat {
		b.rep.Buckets.SamizdatBooks++
	}
	targets := map[int][]string{}
	for _, f := range full {
		root := b.find(f)
		if b.rootOnBook(root, c.BookID) {
			// A name of that entity stands on this very book: another person.
			continue
		}
		if kinds := b.bookEvidence(c.BookID, short, f); len(kinds) > 0 {
			targets[root] = mergeKinds(targets[root], kinds)
		}
	}
	switch {
	case len(targets) == 0:
	case samizdat:
		b.rep.Buckets.SamizdatKept++
	case len(targets) > 1:
		b.rep.Buckets.Conflicting++
	default:
		for root, kinds := range targets {
			b.movedTo[ci] = root
			b.movedRule[ci] = ruleD3 + strings.Join(kinds, "+")
			b.rep.Buckets.Moved++
			for _, k := range kinds {
				b.rep.Buckets.MovedBy[k]++
			}
			if len(kinds) == 1 && kinds[0] == EvidenceTitle {
				b.rep.Buckets.MovedTitleOnly++
			}
		}
	}
}

// rootOnBook reports a key of the component root credited on book.
func (b *builder) rootOnBook(root int, book int64) bool {
	for _, k := range b.bookKeys[book] {
		if b.find(k) == root {
			return true
		}
	}
	return false
}

// bookEvidence is the evidence that a short name's book is the full name's:
// its title on another book of the full name, a series of it, a co-author
// of it.
func (b *builder) bookEvidence(book int64, short, full int) []string {
	var kinds []string
	bk := b.in.Books[book]
	if b.underTitle(b.titleOf[book], full, book) {
		kinds = append(kinds, EvidenceTitle)
	}
	for _, s := range bk.Series {
		if b.keySeries[full][s] {
			kinds = append(kinds, EvidenceSeries)
			break
		}
	}
	for _, k := range b.bookKeys[book] {
		if k != short && k != full && b.coauthors[full][k] {
			kinds = append(kinds, EvidenceCoauthor)
			break
		}
	}
	return kinds
}

// evidenceOrder is the order kinds are written in a rule.
var evidenceOrder = []string{EvidenceTitle, EvidenceSeries, EvidenceCoauthor}

func mergeKinds(have, add []string) []string {
	var out []string
	for _, k := range evidenceOrder {
		if slices.Contains(have, k) || slices.Contains(add, k) {
			out = append(out, k)
		}
	}
	return out
}

// group is one entity to be: a root of key groups with the credits that
// stay and the ones moved in.
type group struct {
	root    int
	credits []int
	books   map[int64]bool
	id      uuid.UUID
	// bucket reports a short name's bucket.
	bucket bool
}

// sortedKeys returns a map's keys in order.
func sortedKeys[K cmp.Ordered, V any](m map[K]V) []K {
	out := make([]K, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// display is the name an entity takes from its credits (§1.1): the display
// name of the result with the most books, then the fullest, then the lowest
// result id. Never new letters.
func (b *builder) display(credits []int) EntityRow {
	type candidate struct {
		c     *Credit
		books map[int64]bool
	}
	byResult := map[int64]*candidate{}
	for _, ci := range credits {
		c := &b.in.Credits[ci]
		cand := byResult[c.ResultID]
		if cand == nil {
			cand = &candidate{c: c, books: map[int64]bool{}}
			byResult[c.ResultID] = cand
		}
		cand.books[c.BookID] = true
	}
	var best *candidate
	for _, rid := range sortedKeys(byResult) {
		cand := byResult[rid]
		switch {
		case best == nil, len(cand.books) > len(best.books):
			best = cand
		case len(cand.books) == len(best.books) &&
			len(strings.Fields(cand.c.DisplayName)) > len(strings.Fields(best.c.DisplayName)):
			best = cand
		}
	}
	kind := best.c.Kind
	if kind != KindCollective {
		kind = KindPerson
	}
	return EntityRow{Kind: kind, DisplayName: best.c.DisplayName, SortName: best.c.SortName,
		SearchKey: authornorm.SearchKey(best.c.DisplayName)}
}
