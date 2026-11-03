package authorentity

import (
	"strings"

	"gopds-api/internal/authornorm"
)

// The name kinds (E4q): the pairs the other kinds keep out by
// construction, asked as kinds of their own so the model tells namesakes
// from one person.
//
//   - orphan: two sides of one written name (script, family, given name and
//     suffix): each side with the group's main, its side of most books, and
//     a side of the short name with each side of a full name of it (a
//     bucket of several full names asks each). Two full names of other
//     patronymics are asked too: the card shows both. The bucket kind asks
//     a short name's books one by one, an anthology story on a card of its
//     own; the orphan asks the two entities with their cards' books, and a
//     same joins them after the book moves (joinOrphans), every book the
//     orphan's side still holds, a per-book answer other than same
//     included. The production rule asks more of it (mergeApplyAccepted);
//   - pen_name: a side and the side of its pen name — a name an item records
//     as a pseudonym (P742) of the item another side's main label names,
//     two sides each named by a name one item alone carries, or a side
//     whose nickname is another side's full name. The kinds keep these out
//     (pseudonym_key). Two sides of one written name are never a pen name:
//     that is the orphan's question.
//
// Like the mixed kinds they take no part in the generation, the cap or the
// precedence of the others, so what a registered kind covers never
// changes; a pair another kind asks stays there. A side pairs with at most
// maxCandidatePartners sides per kind (partner_cap).

// The name kinds.
const (
	CandidateOrphan  = "orphan"
	CandidatePenName = "pen_name"
)

// NameKinds are the name kinds.
var NameKinds = []string{CandidateOrphan, CandidatePenName}

// NameKind reports a name kind.
func NameKind(kind string) bool { return kind == CandidateOrphan || kind == CandidatePenName }

// nameKindPairs collects the pairs of the name kinds.
type nameKindPairs struct {
	g     *candGen
	asked map[[2]int]bool
	items []*candItem
	// bases are each side's written names (script, family, given, suffix).
	bases map[int]map[string]bool
}

// nameItems are the items of the name kinds of a pass whose other pair
// items are existing: a pair any of them asks is no name-kind pair.
func (g *candGen) nameItems(existing []*candItem) []*candItem {
	n := &nameKindPairs{g: g, asked: map[[2]int]bool{}, bases: map[int]map[string]bool{}}
	for _, it := range existing {
		if it.reason == "" && it.credit < 0 {
			n.asked[sidePair(it.a, it.b)] = true
		}
	}
	n.orphans()
	n.penNames()
	n.capPartners()
	return n.items
}

// sidePair names an unordered pair of sides.
func sidePair(x, y *candSide) [2]int { return [2]int{min(x.root, y.root), max(x.root, y.root)} }

// add records the pair of x and y of kind, unless it is one side or asked
// (a pen name of one written name is the orphan's); short is an orphan of
// a short side x.
func (n *nameKindPairs) add(kind string, x, y *candSide, short bool) {
	if x == y || n.asked[sidePair(x, y)] || (kind == CandidatePenName && n.sameBase(x, y)) {
		return
	}
	n.asked[sidePair(x, y)] = true
	it := &candItem{kind: kind, script: pairScript(x, y), a: x, b: y, credit: -1, short: short}
	it.reason = n.g.b.nameKindReason(it)
	n.items = append(n.items, it)
}

// sameBase reports two sides of one written name.
func (n *nameKindPairs) sameBase(x, y *candSide) bool {
	for base := range n.bases[x.root] {
		if n.bases[y.root][base] {
			return true
		}
	}
	return false
}

// pairScript is a pair's script: the sides' one script, or Cyrillic and
// Latin.
func pairScript(x, y *candSide) string {
	if x.script == y.script {
		return x.script
	}
	return ScriptCyrlLatn
}

// nameKindReason is why a name-kind pair is no candidate, empty when it is
// one: a vetoed pair, a shared book, a namesake tag, a manual side, a side
// of mixed scripts, and for an orphan a side of no name.
func (b *builder) nameKindReason(it *candItem) string {
	x, y := it.a, it.b
	switch {
	case len(b.vetoOf) > 0 && b.vetoSidesOfKeys(x.keys).apart(b.vetoSidesOfKeys(y.keys)):
		return ExcludedRevokedPair
	case sharesBooks(x.books, y.books):
		return ExcludedCoCredited
	case x.tagged || y.tagged:
		return ExcludedNamesakeTag
	case x.manual || y.manual:
		return ExcludedManualSide
	}
	return nameKindNameReason(it)
}

// nameKindNameReason is why the names of a name-kind pair keep it out: a
// side of mixed scripts, an orphan across scripts or of a side of no name.
func nameKindNameReason(it *candItem) string {
	x, y := it.a, it.b
	cyrl, latn := string(authornorm.ScriptCyrillic), string(authornorm.ScriptLatin)
	oneScript := func(s *candSide) bool { return s.script == cyrl || s.script == latn }
	switch {
	case !oneScript(x) || !oneScript(y) || (it.kind == CandidateOrphan && x.script != y.script):
		return ExcludedMixedScript
	case it.kind == CandidateOrphan && (x.noName || y.noName):
		return ExcludedNoFamilyOrGiven
	}
	return ""
}

// namedSide is a side of a written name, of its full name or its short one.
type namedSide struct {
	s    *candSide
	full bool
}

// orphans pairs the sides of one written name (see above).
func (n *nameKindPairs) orphans() {
	byBase := map[string][]namedSide{}
	for _, s := range n.g.sortedSides() {
		full := n.writtenBases(s)
		n.bases[s.root] = full
		for _, base := range sortedKeys(full) {
			byBase[base] = append(byBase[base], namedSide{s: s, full: full[base]})
		}
	}
	for _, base := range sortedKeys(byBase) {
		if sides := byBase[base]; len(sides) >= minGroup {
			n.orphanGroup(sides)
		}
	}
}

// writtenBases are the written names of a side's person keys (script,
// family, given, suffix), each true when the side has it as a full name.
func (n *nameKindPairs) writtenBases(s *candSide) map[string]bool {
	full := map[string]bool{}
	for _, k := range s.keys {
		key := &n.g.b.keys[k].key
		if key.kind != KindPerson || key.family == "" || key.given == "" || key.initials() || key.tagged != "" {
			continue
		}
		base := key.script + "|" + personBase(key)
		full[base] = full[base] || key.additional != ""
	}
	return full
}

// orphanGroup pairs the sides of one written name: each short side with
// each full one, every side with the main; a short side is side a.
func (n *nameKindPairs) orphanGroup(sides []namedSide) {
	main := sides[0]
	for _, x := range sides[1:] {
		if len(x.s.books) > len(main.s.books) {
			main = x
		}
	}
	for _, x := range sides {
		for _, f := range sides {
			if !x.full && f.full {
				n.add(CandidateOrphan, x.s, f.s, true)
			}
		}
	}
	for _, x := range sides {
		switch {
		case x == main:
		case !main.full && x.full:
			n.add(CandidateOrphan, main.s, x.s, true)
		default:
			n.add(CandidateOrphan, x.s, main.s, !x.full)
		}
	}
}

// penNames pairs a side with the side of its pen name (see above).
func (n *nameKindPairs) penNames() {
	sides := n.g.sortedSides()
	if ref := n.g.b.in.Reference; ref != nil {
		n.itemPenNames(ref, sides)
	}
	byDisplay := map[string][]*candSide{}
	for _, s := range sides {
		if k := authornorm.SearchKey(s.display); strings.Contains(k, " ") {
			byDisplay[k] = append(byDisplay[k], s)
		}
	}
	for _, s := range sides {
		for _, v := range sortedDistinct(s.variants) {
			for _, t := range byDisplay[authornorm.SearchKey(v)] {
				n.add(CandidatePenName, s, t, false)
			}
		}
	}
}

// itemPenNames are the pen names the reference's items record: a
// pseudonym of the item a side's main label names, and two sides each
// named by a name one item alone carries, with the item's side of most
// books.
func (n *nameKindPairs) itemPenNames(ref *Reference, sides []*candSide) {
	mainSides, aloneSides := n.itemSides(ref, sides)
	penOf := map[string][]string{}
	for _, qid := range sortedKeys(ref.Items) {
		for _, p := range ref.Items[qid].PseudonymKeys {
			penOf[p] = append(penOf[p], qid)
		}
	}
	for _, s := range sides {
		for _, name := range n.sideNameKeys(s) {
			for _, q := range penOf[name] {
				for _, r := range mainSides[q] {
					n.add(CandidatePenName, s, r, false)
				}
			}
		}
	}
	for _, q := range sortedKeys(aloneSides) {
		if group := aloneSides[q]; len(group) >= minGroup {
			main := mostBooks(group)
			for _, x := range group {
				n.add(CandidatePenName, x, main, false)
			}
		}
	}
}

// itemSides are the sides of each item: those its main label names, and
// those a name names that the item alone carries.
func (n *nameKindPairs) itemSides(ref *Reference, sides []*candSide) (mainSides, aloneSides map[string][]*candSide) {
	mainSides, aloneSides = map[string][]*candSide{}, map[string][]*candSide{}
	for _, s := range sides {
		seenMain, seenAlone := map[string]bool{}, map[string]bool{}
		for _, name := range n.sideNameKeys(s) {
			if q := n.g.b.mainLabelOf(name); q != "" && !seenMain[q] {
				seenMain[q] = true
				mainSides[q] = append(mainSides[q], s)
			}
			if q := aloneItem(ref, name); q != "" && !seenAlone[q] {
				seenAlone[q] = true
				aloneSides[q] = append(aloneSides[q], s)
			}
		}
	}
	return mainSides, aloneSides
}

// mostBooks is the side of most books of sides, the first of them on a tie.
func mostBooks(sides []*candSide) *candSide {
	main := sides[0]
	for _, x := range sides[1:] {
		if len(x.books) > len(main.books) {
			main = x
		}
	}
	return main
}

// sideNameKeys are the reference keys of a side's credits' display names,
// distinct and sorted.
func (n *nameKindPairs) sideNameKeys(s *candSide) []string {
	ref := n.g.b.in.Reference
	keys := make([]string, 0, len(s.names))
	for _, name := range s.names {
		keys = append(keys, ref.NameKey[name])
	}
	return sortedDistinct(keys)
}

// aloneItem is the one item that carries a key, by any kind of name; empty
// when none or several do.
func aloneItem(ref *Reference, key string) string {
	qid := ""
	for _, it := range ref.KeyItems[key] {
		if qid != "" && it.QID != qid {
			return ""
		}
		qid = it.QID
	}
	return qid
}

// capPartners keeps out every pair of a side with more partners of a kind
// than maxCandidatePartners (partner_cap).
func (n *nameKindPairs) capPartners() {
	partners := map[string]map[int]int{}
	for _, it := range n.items {
		if partners[it.kind] == nil {
			partners[it.kind] = map[int]int{}
		}
		partners[it.kind][it.a.root]++
		partners[it.kind][it.b.root]++
	}
	for _, it := range n.items {
		p := partners[it.kind]
		if it.reason == "" && (p[it.a.root] > maxCandidatePartners || p[it.b.root] > maxCandidatePartners) {
			it.reason = ExcludedPartnerCap
		}
	}
}

// joinOrphans applies the orphan decisions (E4q) and reports whether one
// joined: a same joins the entities of its two sides, those of two full
// names first, then those of a short side. A side whose sames name two
// entities holds them all, two_targets; the holds of the Wikidata step
// apply (a not_same pair, a veto) and a shared book, shared_book.
func (m *modelStep) joinOrphans() bool {
	b := m.b
	partners := m.notSamePartners()
	joined := false
	for _, short := range []bool{false, true} {
		bySide := map[int][]decided{}
		for _, e := range m.orphanDecs {
			if e.si.it.short == short {
				bySide[e.si.it.a.root] = append(bySide[e.si.it.a.root], e)
			}
		}
		for _, root := range sortedKeys(bySide) {
			if m.joinOrphan(bySide[root], partners) {
				joined = true
			}
		}
	}
	if joined {
		for ci, to := range b.movedTo {
			b.movedTo[ci] = b.find(to)
		}
	}
	return joined
}

// joinOrphan applies the sames decs of one orphan side (joinOrphans) and
// reports whether it joined.
func (m *modelStep) joinOrphan(decs []decided, partners map[int][]int) bool {
	b := m.b
	rx := b.find(decs[0].si.it.a.root)
	targets := map[int]bool{}
	for _, e := range decs {
		if r := b.find(e.si.it.b.root); r != rx {
			targets[r] = true
		}
	}
	state, reason := DecisionApplied, ""
	switch {
	case len(targets) > 1:
		state, reason = DecisionConflict, ConflictTwoTargets
	case len(targets) == 1:
		ry := sortedKeys(targets)[0]
		switch {
		case b.notSameAcross(partners, rx, ry):
			state, reason = DecisionConflict, ConflictNotSame
		case b.vetoApart(rx, ry):
			state, reason = DecisionConflict, ConflictVetoed
			b.vetoRefused(VetoRefusedModel)
		case b.componentsShareABook(rx, ry):
			state, reason = DecisionConflict, ConflictSharedBook
		}
	}
	for _, e := range decs {
		m.set(e.d.ID, state, reason)
	}
	if state != DecisionApplied || len(targets) == 0 {
		return false
	}
	e, ry := decs[0], sortedKeys(targets)[0]
	// The rule goes to the keys of the smaller entity: the orphan joined.
	joined := ry
	if b.rootBooks(rx) < b.rootBooks(ry) {
		joined = rx
	}
	rule := modelRuleOf(e.d.Kind, e.d.ReportID)
	for _, k := range b.membersOf(joined) {
		if b.modelRule[k] == "" && b.wdRule[k] == "" {
			b.modelRule[k] = rule
		}
	}
	addEdge(b.forest, e.si.it.a.root, e.si.it.b.root, forestEdge{decision: e.d.ID, kind: e.d.Kind, report: e.d.ReportID})
	b.union(rx, ry)
	m.rep.Unions++
	return true
}
