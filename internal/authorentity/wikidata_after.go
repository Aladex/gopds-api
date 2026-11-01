package authorentity

import (
	"slices"
)

// The Wikidata step after the decisions (E4f, Wikidata labels (2)). Level
// 0 merges on Wikidata before the merge candidates are drawn, so a merge
// there changes candidates and lapses the decisions on them; a short name's
// main label sits on its bucket there, which rule 5 keeps out. This step
// runs once the decisions are applied and the bucket books moved, over the
// entities as they then stand — a full name with the books moved into it
// goes by their names too («Гарри Гаррисон» in «Гарри Максвелл Гаррисон») —
// and joins the entities one item names. The candidates are sealed by then:
// no candidate, digest or card changes, and no decision lapses.
//
// The rule is level 0's with two changes:
//   - an entity is named by any name of its credits that is a main label of
//     one item alone, every such name naming the same item (rules 1 and 2);
//   - a pseudonym keeps apart only an entity one of whose names it is
//     (rule 3), not every entity of an item recording one;
//
// and two more holds, for a union the decisions may have answered already:
//   - two entities the production pair answered other than same on are
//     never joined (not_same);
//   - two full Cyrillic given names or patronymics of one family that are
//     not one name are never joined (given_name, as for a decision).
//
// Everything else holds as at level 0: one key per script, no initial, no
// short name's bucket (the books left in it), no namesake tag, no shared
// book, no veto. A bucket book whose two targets the step joins moves, its
// two_targets decisions applied.

// wikidataAfterDenied are the items the step joins nothing on: the way back
// from a wrong merge the hand check finds (an item conflating two people),
// shipped as code like the rest of the rule. Empty.
var wikidataAfterDenied = map[string]bool{}

// The reasons the step keeps a set apart beyond level 0's.
const (
	WikidataExcludedNotSame   = "not_same"
	WikidataExcludedGivenName = "given_name"
)

// mergeWikidataAfterDecisions joins the entities after the decisions that
// one item names (see above) and reports whether it joined any.
func (m *modelStep) mergeWikidataAfterDecisions() bool {
	b := m.b
	if b.in.Reference == nil {
		return false
	}
	w := &WikidataReport{Extract: b.in.Reference.Extract, Excluded: map[string]int{}, Merges: map[string]int{}}
	m.rep.Wikidata = w
	byItem := map[string][]*component{}
	for _, c := range b.componentsAfterDecisions() {
		qid := b.namedBy(c)
		if qid == "" || wikidataAfterDenied[qid] {
			continue
		}
		w.Named++
		if reason := b.excludedAfter(c, qid); reason != "" {
			w.Excluded[reason]++
			continue
		}
		byItem[qid] = append(byItem[qid], c)
	}
	partners := m.notSamePartners()
	names := map[int]nameClasses{}
	nameOf := func(root int) nameClasses {
		if n, ok := names[root]; ok {
			return n
		}
		names[root] = b.nameClassesOf(root)
		return names[root]
	}
	apart := func(x, y *component) string {
		if b.notSameAcross(partners, x.root, y.root) {
			return WikidataExcludedNotSame
		}
		if nameOf(x.root).joins(nameOf(y.root)) {
			return WikidataExcludedGivenName
		}
		return ""
	}
	before := len(b.wdDecisions)
	for _, qid := range sortedKeys(byItem) {
		b.mergeItemInto(w, qid, byItem[qid], apart)
	}
	if len(b.wdDecisions) == before {
		return false
	}
	// A moved book follows its target into the entity it joined: the plan
	// reads the target by its root.
	for ci, to := range b.movedTo {
		b.movedTo[ci] = b.find(to)
	}
	return true
}

// componentsAfterDecisions are the person entities after the decisions, by
// root: the credits of their keys that stayed, and the credits moved into
// them; a root left with no credit is none.
func (b *builder) componentsAfterDecisions() []*component {
	byRoot := map[int]*component{}
	get := func(root int) *component {
		c := byRoot[root]
		if c == nil {
			c = &component{root: root, books: map[int64]bool{}}
			byRoot[root] = c
		}
		return c
	}
	for _, g := range b.keys {
		root := b.find(g.idx)
		c := get(root)
		c.keys = append(c.keys, g.idx)
		for _, ci := range g.credits {
			if to, moved := b.movedTo[ci]; moved {
				t := get(b.find(to))
				t.credits = append(t.credits, ci)
				continue
			}
			c.credits = append(c.credits, ci)
		}
	}
	out := make([]*component, 0, len(byRoot))
	for _, root := range sortedKeys(byRoot) {
		c := byRoot[root]
		if b.keys[root].key.kind == KindCollective || len(c.credits) == 0 {
			continue
		}
		slices.Sort(c.credits)
		for _, ci := range c.credits {
			c.books[b.in.Credits[ci].BookID] = true
		}
		b.nameComponent(c)
		out = append(out, c)
	}
	return out
}

// nameComponent sets a component's display name, its key and script and
// the keys of its credits' names.
func (b *builder) nameComponent(c *component) {
	ref := b.in.Reference
	c.display = b.display(c.credits).DisplayName
	c.key = ref.NameKey[c.display]
	c.script = scriptOf(c.display)
	c.names = nil
	for _, ci := range c.credits {
		if k := ref.NameKey[b.in.Credits[ci].DisplayName]; k != "" {
			c.names = append(c.names, k)
		}
	}
	slices.Sort(c.names)
	c.names = slices.Compact(c.names)
}

// namedBy is the item a component's names name (rules 1 and 2): every name
// that is a main label of one item alone is one of the same item. It sets
// the component's key and script to the name that names it — its display
// name's when that one does, else the first that does. Empty when no name
// does, or two name two items.
func (b *builder) namedBy(c *component) string {
	ref := b.in.Reference
	qid, key := "", ""
	for _, name := range c.names {
		q := b.mainLabelOf(name)
		switch {
		case q == "":
			continue
		case qid != "" && q != qid:
			return ""
		case qid == "" || name == ref.NameKey[c.display]:
			qid, key = q, name
		}
	}
	if qid == "" {
		return ""
	}
	if key != c.key {
		c.key = key
		for _, ci := range c.credits {
			if n := b.in.Credits[ci].DisplayName; ref.NameKey[n] == key {
				c.script = scriptOf(n)
				break
			}
		}
	}
	return qid
}

// excludedAfter is why an entity named by an item stays out of the step's
// merges: a name of it the item's pseudonym (rule 3), an initial (rule 4),
// a short name's bucket (rule 5), a namesake tag.
func (b *builder) excludedAfter(c *component, qid string) string {
	pens := b.in.Reference.Items[qid].PseudonymKeys
	switch {
	case slices.ContainsFunc(c.names, func(n string) bool { return slices.Contains(pens, n) }):
		return WikidataExcludedPseudonym
	case b.initialsIn(c):
		return WikidataExcludedInitials
	case slices.ContainsFunc(c.keys, b.shortBesideFull):
		return WikidataExcludedShortName
	case slices.ContainsFunc(c.keys, func(k int) bool { return b.keys[k].key.tagged != "" }):
		return WikidataExcludedNamesake
	}
	return ""
}

// notSamePartners are the level-0 components the production pair answered
// other than same on, pair by pair.
func (m *modelStep) notSamePartners() map[int][]int {
	partners := map[int][]int{}
	for _, digest := range sortedDigests(m.b.in.NotSame) {
		if si := m.byDigest[digest]; si != nil && si.credit == nil {
			x, y := si.sideA.root, si.sideB.root
			partners[x] = append(partners[x], y)
			partners[y] = append(partners[y], x)
		}
	}
	return partners
}

// notSameAcross reports a level-0 component of one entity whose not-same
// partner is in the other.
func (b *builder) notSameAcross(partners map[int][]int, rx, ry int) bool {
	for _, k := range b.membersOf(rx) {
		for _, q := range partners[b.root0[k]] {
			if b.find(q) == ry {
				return true
			}
		}
	}
	return false
}

// retryTwoTargets moves the bucket books two_targets held whose targets
// the Wikidata step joined: their decisions now send them to one entity.
func (m *modelStep) retryTwoTargets() {
	byCredit := map[int][]decided{}
	for _, e := range m.bookDecs {
		byCredit[e.si.it.credit] = append(byCredit[e.si.it.credit], e)
	}
	for _, ci := range sortedKeys(byCredit) {
		decs := byCredit[ci]
		if !slices.ContainsFunc(decs, func(e decided) bool {
			s := m.states[e.d.ID]
			return s != nil && s.Reason == ConflictTwoTargets
		}) {
			continue
		}
		moves := m.rep.BookMoves
		m.moveBook(ci, decs)
		if m.rep.BookMoves > moves {
			m.rep.TwoTargetsJoined++
		}
	}
}
