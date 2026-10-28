package authorentity

import (
	"bytes"
	"crypto/sha256"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopds-api/internal/authornorm"

	"github.com/google/uuid"
)

// The merge eval's constructed items (E4c, plan §3.1, §3.2, §3.5), read from
// a sealed plan; nothing here is written, both are eval input only:
//
//   - the adversarial suite: real namesakes — two level-0 people of one
//     script, family and given name told apart by one-word patronymics far
//     from each other — rendered in a stratum's shape, so the only way to
//     answer "same" is the names alone;
//   - the known-same anchors: two key groups the build already holds as one
//     person (a D2 group, a typo merge) shown as two cards, measured as
//     recall and never gated.

// SuiteVersion versions the suite's selection and rendering; it is the
// extractor version of a suite item. AnchorVersion likewise for anchors.
const (
	SuiteVersion  = "e4-suite-v1"
	AnchorVersion = "e4-anchor-v1"
)

// MergeCandidateVersion is a candidate's version, the extractor version of
// its eval item and its job.
const MergeCandidateVersion = mergeCandidateVersion

// The scripts a suite pair or an anchor can be of.
const (
	ScriptCyrl = string(authornorm.ScriptCyrillic)
	ScriptLatn = string(authornorm.ScriptLatin)
)

// suiteMinDistance is how far apart two patronymics must be (edit distance
// above it), so a typo is never taken for a second person (A.13).
const suiteMinDistance = 2

// MergeSuitePair is one namesake pair: two sides of one script, family and
// given name with different patronymics.
type MergeSuitePair struct {
	Script           string
	EntityA, EntityB uuid.UUID
	a, b             *candSide
}

// MergeAnchorPair is one known-same anchor: two key groups of one side.
type MergeAnchorPair struct {
	Kind, Script string
	Entity       uuid.UUID
	ka, kb       int
}

// suiteSide is a side a namesake pair may use, with its root key.
type suiteSide struct {
	s   *candSide
	key *nameKey
}

// MergeSuitePairs are the plan's namesake pairs, in a fixed hash order,
// each side in at most one pair. A side the exclude set names (every
// candidate side, so the suite never shares a card with a holdout), a
// bucket, a tagged or manual side, a side of another script, of initials or
// of a patronymic of several words is never used. A pair that shares a
// book, a series or a normalized title is left out: its label is unsure
// (A.14), as is a pair of patronymics of the two genders.
func (p *Plan) MergeSuitePairs(exclude map[uuid.UUID]bool) []MergeSuitePair {
	if p.cand == nil {
		return nil
	}
	b := p.cand.b
	groups := p.suiteGroups(exclude)
	var all []MergeSuitePair
	for _, g := range sortedKeys(groups) {
		list := groups[g]
		for i, x := range list {
			for _, y := range list[i+1:] {
				if levenshtein(x.key.additional, y.key.additional) <= suiteMinDistance ||
					otherGender(x.key.additional, y.key.additional) || b.sharesEvidence(x.s, y.s) {
					continue
				}
				all = append(all, MergeSuitePair{Script: x.s.script, EntityA: x.s.entity, EntityB: y.s.entity, a: x.s, b: y.s})
			}
		}
	}
	slices.SortStableFunc(all, func(x, y MergeSuitePair) int {
		return bytes.Compare(suiteOrder(x.EntityA, x.EntityB), suiteOrder(y.EntityA, y.EntityB))
	})
	used := map[int]bool{}
	out := all[:0]
	for i := range all {
		pair := &all[i]
		if used[pair.a.root] || used[pair.b.root] {
			continue
		}
		used[pair.a.root], used[pair.b.root] = true, true
		out = append(out, *pair)
	}
	return out
}

// suiteGroups are the sides a namesake pair may use, by script, family and
// given name.
func (p *Plan) suiteGroups(exclude map[uuid.UUID]bool) map[string][]suiteSide {
	b := p.cand.b
	groups := map[string][]suiteSide{}
	for _, root := range sortedKeys(p.cand.sides) {
		s := p.cand.sides[root]
		if !suiteEligible(s, exclude) || len(b.keys[s.root].credits) == 0 {
			continue
		}
		k := &b.keys[s.root].key
		if !suiteKey(k) {
			continue
		}
		g := k.script + "|" + k.family + "|" + k.given
		groups[g] = append(groups[g], suiteSide{s: s, key: k})
	}
	return groups
}

// suiteEligible reports a side the suite may use: no bucket, tag, manual
// row, nameless side or candidate side, of one of the two scripts.
func suiteEligible(s *candSide, exclude map[uuid.UUID]bool) bool {
	return !s.bucket && !s.tagged && !s.manual && !s.noName && !exclude[s.entity] &&
		(s.script == ScriptCyrl || s.script == ScriptLatn)
}

// suiteKey reports a full person name with a one-word patronymic.
func suiteKey(k *nameKey) bool {
	return k.kind == KindPerson && k.family != "" && k.given != "" && k.additional != "" && k.suffix == "" &&
		!strings.Contains(k.additional, " ") && !k.initials()
}

// suiteOrder is a pair's place in the suite's order.
func suiteOrder(a, b uuid.UUID) []byte {
	h := sha256.New()
	h.Write([]byte(SuiteVersion))
	h.Write(a[:])
	h.Write(b[:])
	return h.Sum(nil)
}

// otherGender reports two patronymics of the two genders («-ич», «-на»).
func otherGender(a, b string) bool {
	return gender(a) != "" && gender(b) != "" && gender(a) != gender(b)
}

// sharesEvidence reports two sides with a book, a series or a normalized
// title in common.
func (b *builder) sharesEvidence(x, y *candSide) bool {
	books, series, titles := map[int64]bool{}, map[int64]bool{}, map[string]bool{}
	for _, id := range x.books {
		books[id] = true
		bk := b.in.Books[id]
		for _, s := range bk.Series {
			series[s] = true
		}
		if t := authornorm.SearchKey(bk.Title); t != "" {
			titles[t] = true
		}
	}
	for _, id := range y.books {
		bk := b.in.Books[id]
		if books[id] || titles[authornorm.SearchKey(bk.Title)] {
			return true
		}
		for _, s := range bk.Series {
			if series[s] {
				return true
			}
		}
	}
	return false
}

// MergeSuiteBookIDs are the sample books the pairs' cards show, sorted.
func (p *Plan) MergeSuiteBookIDs(pairs []MergeSuitePair) []int64 {
	set := map[int64]bool{}
	for i := range pairs {
		for _, s := range []*candSide{pairs[i].a, pairs[i].b} {
			for _, id := range s.sampleBooks() {
				set[id] = true
			}
		}
	}
	return sortedKeys(set)
}

// SuiteItemKey names a rendered suite item: its pair, shape and rendering.
func SuiteItemKey(pair *MergeSuitePair, shape string, romanized bool) [32]byte {
	h := sha256.New()
	h.Write([]byte(SuiteVersion + "|" + shape + "|"))
	if romanized {
		h.Write([]byte("romanized|"))
	}
	h.Write(pair.EntityA[:])
	h.Write(pair.EntityB[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// RenderSuiteItem renders a namesake pair in a stratum's shape (§3.2), each
// person with its own real books: swap — both without the patronymic, one
// side reordered; initials — the side of fewer books as an initial and the
// family name against the other's full name. Romanized, both sides' names
// are in Latin letters by the fixed table, their books unchanged. books are
// the loaded fields of MergeSuiteBookIDs.
func (p *Plan) RenderSuiteItem(pair *MergeSuitePair, shape string, romanized bool,
	books map[int64]CardBookFields) MergeInput {
	b := p.cand.b
	x, y := pair.a, pair.b
	if bytes.Compare(suiteOrder(pair.EntityB, pair.EntityA), suiteOrder(pair.EntityA, pair.EntityB)) < 0 {
		x, y = y, x
	}
	nx, ny := b.nameParts(x), b.nameParts(y)
	var nameX, nameY string
	switch shape {
	case CandidateInitials:
		if len(y.books) < len(x.books) {
			x, y, nx, ny = y, x, ny, nx
		}
		first, _ := utf8.DecodeRuneInString(nx.given)
		nameX = string(first) + ". " + nx.family
		nameY = ny.given + " " + ny.additional + " " + ny.family
	default:
		nameX = nx.given + " " + nx.family
		nameY = ny.family + " " + ny.given
	}
	if romanized {
		nameX, nameY = Romanize(nameX), Romanize(nameY)
	}
	r := p.renderer(books, p.MergeSuiteBookIDs([]MergeSuitePair{*pair}))
	a := r.cardOf([]string{nameX}, nil, x.books, x.entity)
	c := r.cardOf([]string{nameY}, nil, y.books, y.entity)
	return MergeInput{CardVersion: MergeCardVersion, A: a, B: c}
}

// personName is a side's name parts as its credits spell them.
type personName struct{ given, additional, family string }

// nameParts is a side's name: its root key group's first credit's result.
func (b *builder) nameParts(s *candSide) personName {
	c := &b.in.Credits[b.keys[s.root].credits[0]]
	return personName{given: c.Given, additional: strings.Join(c.Additional, " "), family: c.Family}
}

// renderer is a card renderer over loaded book fields.
func (p *Plan) renderer(books map[int64]CardBookFields, ids []int64) *cardRenderer {
	b := p.cand.b
	return &cardRenderer{b: b, p: p, fields: books, byBook: b.creditsOnBooks(ids), cards: map[int]MergeCard{}}
}

// cardOf renders a card of the given names over books of entity: the
// oldest mergeCardSample of them shown.
func (r *cardRenderer) cardOf(names, variants []string, books []int64, entity uuid.UUID) MergeCard {
	var sample []MergeCardBook
	for _, id := range books[:min(mergeCardSample, len(books))] {
		sample = append(sample, r.book(id, entity))
	}
	return MergeCardOf(names, variants, len(books), sample)
}

// MergeAnchorPairs are the plan's known-same anchors, in a fixed hash
// order: per side at most one pair of its key groups — two names the build
// already holds as one person (a typo merge, a D2b union, a Wikidata merge)
// — that are displayed differently and share no book. The kind is initials
// for initials that stand for the other's full name, swap for anything
// else (§3.2: D2 groups and typo merges are swap-shaped). Sides as for the
// suite; the exclude set leaves sides out.
func (p *Plan) MergeAnchorPairs(exclude map[uuid.UUID]bool) []MergeAnchorPair {
	if p.cand == nil {
		return nil
	}
	b := p.cand.b
	var out []MergeAnchorPair
	for _, root := range sortedKeys(p.cand.sides) {
		s := p.cand.sides[root]
		if s.bucket || s.tagged || s.manual || exclude[s.entity] || len(s.keys) < 2 ||
			(s.script != ScriptCyrl && s.script != ScriptLatn) {
			continue
		}
		if pair, ok := b.anchorPair(s); ok {
			out = append(out, pair)
		}
	}
	slices.SortStableFunc(out, func(x, y MergeAnchorPair) int {
		return bytes.Compare(anchorOrder(&x, b), anchorOrder(&y, b))
	})
	return out
}

// anchorPair is a side's first pair of key groups an anchor can show.
func (b *builder) anchorPair(s *candSide) (MergeAnchorPair, bool) {
	keys := slices.Clone(s.keys)
	slices.Sort(keys)
	for i, x := range keys {
		for _, y := range keys[i+1:] {
			kx, ky := b.keys[x], b.keys[y]
			if len(kx.credits) == 0 || len(ky.credits) == 0 || kx.key.kind != KindPerson ||
				ky.key.kind != KindPerson || overlaps(kx.books, ky.books) ||
				slices.Equal(b.displayNames(kx), b.displayNames(ky)) {
				continue
			}
			return MergeAnchorPair{Kind: anchorKind(&kx.key, &ky.key), Script: s.script, Entity: s.entity, ka: x, kb: y},
				true
		}
	}
	return MergeAnchorPair{}, false
}

// displayNames are a key group's credits' display names, distinct and
// sorted.
func (b *builder) displayNames(kg *keyGroup) []string {
	names := make([]string, 0, len(kg.credits))
	for _, ci := range kg.credits {
		names = append(names, b.in.Credits[ci].DisplayName)
	}
	return sortedDistinct(names)
}

// anchorKind is the shape two keys of one person show: initials when one
// is initials standing for the other's full name, else swap.
func anchorKind(x, y *nameKey) string {
	if x.family == "" || x.family != y.family || x.initials() == y.initials() {
		return CandidateSwap
	}
	short, full := x, y
	if y.initials() {
		short, full = y, x
	}
	if full.given != "" &&
		initialsFit(strings.Fields(short.given+" "+short.additional), strings.Fields(full.given+" "+full.additional)) {
		return CandidateInitials
	}
	return CandidateSwap
}

// overlaps reports two sorted book lists with a book in common.
func overlaps(a, b []int64) bool {
	for _, id := range a {
		if _, ok := slices.BinarySearch(b, id); ok {
			return true
		}
	}
	return false
}

// anchorOrder is an anchor's place in the anchors' order.
func anchorOrder(a *MergeAnchorPair, b *builder) []byte {
	h := sha256.New()
	h.Write([]byte(AnchorVersion + "|" + a.Kind + "|" + b.keys[a.ka].key.String() + "|" + b.keys[a.kb].key.String()))
	h.Write(a.Entity[:])
	return h.Sum(nil)
}

// AnchorItemKey names an anchor item.
func (p *Plan) AnchorItemKey(a *MergeAnchorPair) [32]byte {
	var out [32]byte
	copy(out[:], anchorOrder(a, p.cand.b))
	return out
}

// MergeAnchorBookIDs are the sample books the anchors' cards show, sorted.
func (p *Plan) MergeAnchorBookIDs(anchors []MergeAnchorPair) []int64 {
	b := p.cand.b
	set := map[int64]bool{}
	for i := range anchors {
		for _, k := range []int{anchors[i].ka, anchors[i].kb} {
			books := b.keys[k].books
			for _, id := range books[:min(mergeCardSample, len(books))] {
				set[id] = true
			}
		}
	}
	return sortedKeys(set)
}

// RenderAnchor renders an anchor: each key group as a card of its credits'
// names and its own books, in a fixed side order.
func (p *Plan) RenderAnchor(a *MergeAnchorPair, books map[int64]CardBookFields) MergeInput {
	b := p.cand.b
	r := p.renderer(books, p.MergeAnchorBookIDs([]MergeAnchorPair{*a}))
	card := func(k int) MergeCard {
		kg := b.keys[k]
		var names, variants []string
		for _, ci := range kg.credits {
			names = append(names, b.in.Credits[ci].DisplayName)
			variants = append(variants, b.in.Credits[ci].Nickname)
		}
		return r.cardOf(names, variants, kg.books, a.Entity)
	}
	x, y := card(a.ka), card(a.kb)
	key := p.AnchorItemKey(a)
	if key[0]&1 == 1 {
		x, y = y, x
	}
	return MergeInput{CardVersion: MergeCardVersion, A: x, B: y}
}

// romanTable is the fixed ru→latin table of the romanized suite items
// (§3.5): readable, one way.
var romanTable = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e", 'ж': "zh", 'з': "z", 'и': "i",
	'й': "i", 'к': "k", 'л': "l", 'м': "m", 'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t",
	'у': "u", 'ф': "f", 'х': "kh", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "shch", 'ъ': "", 'ы': "y", 'ь': "",
	'э': "e", 'ю': "iu", 'я': "ia",
}

// Romanize writes a Cyrillic name in Latin letters by the fixed table; a
// capital stays a capital, anything outside the table is kept.
func Romanize(s string) string {
	var out strings.Builder
	for _, r := range s {
		lower := unicode.ToLower(r)
		latin, ok := romanTable[lower]
		if !ok {
			out.WriteRune(r)
			continue
		}
		if lower != r && latin != "" {
			first, size := utf8.DecodeRuneInString(latin)
			latin = string(unicode.ToUpper(first)) + latin[size:]
		}
		out.WriteString(latin)
	}
	return out.String()
}
