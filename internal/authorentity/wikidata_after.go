package authorentity

import (
	"slices"
	"strings"
	"unicode/utf8"

	"gopds-api/internal/authornorm"
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
//     (rule 3), not every entity of an item recording one, and not when it
//     is the name the item goes by: its main label, the item having one
//     main label per script («George Orwell» / «Джордж Оруэлл», «Danielle
//     Steel» / «Даниэла Стил»), so the pen name and its spelling in the
//     other script are one person's (E4m). A pen name beside a second main
//     label of its script («Agatha Mallowan» beside «Agatha Christie»)
//     keeps apart as before;
//   - an initial keeps apart only an entity whose name with it the item
//     does not carry alone: «J. R. R. Tolkien», «John R. R. Tolkien» and
//     «George R. R. Martin» are names of their items and of no other, so
//     the initials are the item's, not a hub of several people (E4m);
//
// and two more holds, for a union the decisions may have answered already:
//   - two entities the production pair answered other than same on are
//     never joined (not_same);
//   - two full Cyrillic given names or patronymics of one family that are
//     not one name are never joined (given_name, as for a decision).
//
// Everything else holds as at level 0: one key per script, no short name's bucket (the books left in it), no namesake tag, no shared
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

// WikidataOtherName is a merge by another language's name of the item
// (E4m: «Joanne K. Rowlingová»).
const WikidataOtherName = "other_name"

// mergeWikidataAfterDecisions joins the entities after the decisions that
// one item names (see above) and reports whether it joined any.
func (m *modelStep) mergeWikidataAfterDecisions() bool {
	b := m.b
	if b.in.Reference == nil {
		return false
	}
	w := &WikidataReport{Extract: b.in.Reference.Extract, Excluded: map[string]int{}, Merges: map[string]int{}}
	m.rep.Wikidata = w
	byItem, others := b.namedAfterDecisions(w)
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
	for _, qid := range sortedKeys(others) {
		b.mergeOthersInto(w, qid, byItem[qid], others[qid], apart)
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
	c.display = b.matchName(c.credits).DisplayName
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

// namedAfterDecisions are the person entities after the decisions by the
// item main labels name, and by the item another language's name names.
func (b *builder) namedAfterDecisions(w *WikidataReport) (byItem, others map[string][]*component) {
	byItem, others = map[string][]*component{}, map[string][]*component{}
	for _, c := range b.componentsAfterDecisions() {
		qid := b.namedBy(c)
		if qid == "" {
			// No main label names it: a name the item alone carries in
			// another language («Joanne K. Rowlingová», a Czech alias)
			// joins it to an item main labels name (E4m).
			if q := b.otherNameOf(c); q != "" && !wikidataAfterDenied[q] && b.excludedAfter(c, q) == "" {
				others[q] = append(others[q], c)
			}
			continue
		}
		if wikidataAfterDenied[qid] {
			continue
		}
		w.Named++
		if reason := b.excludedAfter(c, qid); reason != "" {
			w.Excluded[reason]++
			continue
		}
		byItem[qid] = append(byItem[qid], c)
	}
	return byItem, others
}

// mergeOthersInto joins the components another language's name names to
// the item's component of most books, after the item's own merge: one at a
// time, none credited on a book of it nor kept apart from it. They stay out
// of the one key per script rule, which reads the item's main labels.
func (b *builder) mergeOthersInto(w *WikidataReport, qid string, named, others []*component,
	apart func(x, y *component) string) {
	if len(named) == 0 {
		return
	}
	anchor := named[0]
	for _, c := range named[1:] {
		if len(c.books) > len(anchor.books) {
			anchor = c
		}
	}
	rule := WikidataRule(qid, b.in.Reference.Extract)
	for _, o := range others {
		if sharesBook(o.books, anchor.books) {
			w.SharedBook++
			continue
		}
		if reason := apart(o, anchor); reason != "" {
			w.Excluded[reason]++
			continue
		}
		if b.vetoApart(b.find(o.root), b.find(anchor.root)) {
			b.vetoRefused(VetoRefusedWikidata)
			continue
		}
		b.union(o.root, anchor.root)
		for _, k := range o.keys {
			b.wdRule[k] = rule
		}
		w.Merges[WikidataOtherName]++
		b.wdDecisions = append(b.wdDecisions, WikidataDecision{Kind: WikidataOtherName, QID: qid,
			Member: b.creditIDs(o.credits), Anchor: b.creditIDs(anchor.credits)})
	}
}

// otherNameOf is the item every name of a component that Wikidata knows
// names alone — as a label of any language, an alias or a native name,
// never a pseudonym — and that spells a main label's family name, inflected
// or not («Rowlingová» of «Rowling»; never «Roman Burevoj», another
// language's label of a pen name); empty when none does, or two items do. It sets the
// component's key and script to that name.
func (b *builder) otherNameOf(c *component) string {
	ref := b.in.Reference
	qid, key := "", ""
	for _, name := range c.names {
		items := ref.KeyItems[name]
		if len(items) == 0 {
			continue
		}
		q := items[0].QID
		for _, it := range items {
			if it.QID != q || it.Kind == NamePseudonym {
				return ""
			}
		}
		if qid != "" && q != qid {
			return ""
		}
		if !familyOfItem(name, ref.Items[q]) {
			return ""
		}
		qid, key = q, name
	}
	if qid != "" && key != c.key {
		c.key = key
		c.script = scriptOf(key)
	}
	return qid
}

// familyOfItem reports a name that spells one of the item's main labels in
// another language: its last word starts, in the sound skeleton, as the
// label's last word does (an inflected family: «Rowlingová»), and its first
// word is the label's first word in that skeleton or the initial of it
// («Joanne» of «J.»). «Beth Smith» beside a main label «Anna Smith» is
// another person.
func familyOfItem(name string, item *Item) bool {
	if item == nil {
		return false
	}
	own := strings.Fields(name)
	if len(own) < minGroup {
		return false
	}
	return slices.ContainsFunc(item.MainLabelKeys, func(k string) bool {
		label := strings.Fields(k)
		if len(label) < minGroup {
			return false
		}
		f := soundSkeleton(label[len(label)-1])
		if utf8.RuneCountInString(f) < minTranslitSkeleton || !strings.HasPrefix(soundSkeleton(own[len(own)-1]), f) {
			return false
		}
		first, given := soundSkeleton(own[0]), soundSkeleton(label[0])
		if utf8.RuneCountInString(label[0]) == 1 {
			return strings.HasPrefix(first, given)
		}
		return first == given
	})
}

// excludedAfter is why an entity named by an item stays out of the step's
// merges: a name of it the item's pseudonym (rule 3), an initial (rule 4),
// a short name's bucket (rule 5), a namesake tag.
func (b *builder) excludedAfter(c *component, qid string) string {
	item := b.in.Reference.Items[qid]
	goesBy := onePerScript(item.MainLabelKeys)
	pen := func(n string) bool {
		return slices.Contains(item.PseudonymKeys, n) && (!goesBy || !slices.Contains(item.MainLabelKeys, n))
	}
	switch {
	case slices.ContainsFunc(c.names, pen):
		return WikidataExcludedPseudonym
	case b.initialsIn(c) && !b.initialsOfItem(c, qid):
		return WikidataExcludedInitials
	case slices.ContainsFunc(c.keys, b.shortBesideFull):
		return WikidataExcludedShortName
	case slices.ContainsFunc(c.keys, func(k int) bool { return b.keys[k].key.tagged != "" }):
		return WikidataExcludedNamesake
	}
	return ""
}

// onePerScript reports main labels of one key per script.
func onePerScript(keys []string) bool {
	byScript := map[string]string{}
	for _, k := range keys {
		s := scriptOf(k)
		if have, ok := byScript[s]; ok && have != k {
			return false
		}
		byScript[s] = k
	}
	return true
}

// initialsOfItem reports every credit name of a component with an initial
// the item's: a name the item carries alone (a label, an alias or a native
// name of it, of no other item), or the initials of a full name of the
// same family in the component that the item carries alone («Джон Р. Р.
// Толкин» beside «Джон Рональд Руэл Толкин», the decisions having joined
// them already). A name carries under the spelling fold too (E4n):
// «Филипп К. Дик» and «Филипп Киндред Дик» are «Филип К. Дик» and «Филип
// Киндред Дик», the item's.
func (b *builder) initialsOfItem(c *component, qid string) bool {
	ref := b.in.Reference
	alone := func(cr *Credit) bool {
		key := ref.NameKey[cr.DisplayName]
		for _, items := range [][]ItemName{ref.KeyItems[key], b.foldedKeyItems()[variantFold(key)]} {
			if len(items) > 0 && !slices.ContainsFunc(items, func(n ItemName) bool { return n.QID != qid }) {
				return true
			}
		}
		return false
	}
	var full []*Credit
	for _, ci := range c.credits {
		if cr := &b.in.Credits[ci]; !creditInitials(cr) && alone(cr) {
			full = append(full, cr)
		}
	}
	for _, ci := range c.credits {
		cr := &b.in.Credits[ci]
		if !creditInitials(cr) || alone(cr) {
			continue
		}
		if !slices.ContainsFunc(full, func(f *Credit) bool { return initialsOfName(cr, f) }) {
			return false
		}
	}
	return true
}

// foldedKeyItems are the items carrying each key under the spelling fold
// (variantFold), built once.
func (b *builder) foldedKeyItems() map[string][]ItemName {
	if b.keyItemsFolded == nil {
		b.keyItemsFolded = map[string][]ItemName{}
		for key, items := range b.in.Reference.KeyItems {
			f := variantFold(key)
			b.keyItemsFolded[f] = append(b.keyItemsFolded[f], items...)
		}
	}
	return b.keyItemsFolded
}

// creditInitials reports a credit name with an initial.
func creditInitials(cr *Credit) bool {
	return hasInitial(cr.DisplayName) || hasInitial(cr.Given) || slices.ContainsFunc(cr.Additional, hasInitial)
}

// initialsOfName reports a credit name of initials that stand for a full
// one of the same family: each word an initial of the full name's word in
// its place, or that word.
func initialsOfName(initials, full *Credit) bool {
	if authornorm.SearchKey(initials.Family) != authornorm.SearchKey(full.Family) || full.Family == "" {
		return false
	}
	words := func(cr *Credit) []string {
		return strings.Fields(authornorm.SearchKey(cr.Given + " " + strings.Join(cr.Additional, " ")))
	}
	return initialsFit(words(initials), words(full))
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
