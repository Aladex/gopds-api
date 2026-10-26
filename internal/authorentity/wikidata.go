package authorentity

import (
	"slices"
	"strings"
	"unicode"

	"gopds-api/internal/authornorm"
)

// Wikidata as evidence (design E, phase E4w), cut to a rule set safe by
// construction (coordinator's decision of 2026-10-24, round 7): a wrong
// merge is not accepted, a lost one is. Two entities merge on an item Q
// only when
//  1. each display name, keyed (public.search_normalize, the entity
//     SearchKey of contract 3.5), is a main label of Q — Russian, English
//     or "mul";
//  2. no other item of the extract carries that key in any way: as a
//     label, an alias, a native name or a pseudonym;
//  3. Q records no pseudonym (P742) at all;
//  4. neither display name has an initial;
//  5. neither entity is a short name's bucket beside a fuller variant of
//     the same family and given name in the catalog;
//  6. no manual row decides them (such credits never reach the automatic
//     groups);
//
// and, beyond the six, conservative still:
//   - neither entity is a namesake tagged "(N)" in its source;
//   - two names of Q in one script merge only when they are the same key:
//     when the entities on Q go by two keys in one script (two main labels
//     of one script), only those of one key merge;
//   - entities credited on one book are two people: such a set stays apart.
//
// Only display names count: nicknames, other labels and aliases are never
// read. An entity takes Q into author_entity_reference when its display name
// meets 1–3, merged or not. Everything else — spellings, aliases, pen names,
// initials, notable works, bucket books — is left to E4.

// Reference is the Wikidata extract as the build reads it: the key of every
// display name of the credits, the items those keys reach, and what the
// rules read of the items.
type Reference struct {
	// Extract is the extract's content ID (wikidata.Manifest.ContentID).
	Extract string
	// NameKey is the key (public.search_normalize) of every display name
	// of the credits.
	NameKey map[string]string
	// KeyItems are the items carrying each key, by kind of name.
	KeyItems map[string][]ItemName
	// Items are the items KeyItems reaches.
	Items map[string]*Item
}

// ItemName is one item carrying a key.
type ItemName struct {
	QID  string
	Kind string
}

// Item is what the rules read of an item: its labels' keys, in every
// language read, and a label to show.
type Item struct {
	Label     string
	LabelKeys []string
	// MainLabelKeys are the keys of its Russian, English and "mul"
	// labels: the name the item goes by. PseudonymKeys are its P742
	// pseudonyms'.
	MainLabelKeys []string
	PseudonymKeys []string
}

// The kinds of an item's name.
const (
	NameLabel     = "label"
	NameAlias     = "alias"
	NamePseudonym = "pseudonym"
	NameNative    = "native"
)

// The kinds of a Wikidata merge: one display name, or one item's main
// labels in two scripts.
const (
	WikidataSameName    = "same_name"
	WikidataCrossScript = "cross_script"
)

// The reasons an entity named by an item stays out of its merges.
const (
	WikidataExcludedPseudonym = "pseudonym"
	WikidataExcludedInitials  = "initials"
	WikidataExcludedShortName = "short_name"
	WikidataExcludedNamesake  = "namesake"
)

// ruleWikidata prefixes the rule of a Wikidata merge: "wikidata:Q42@wd-…".
const ruleWikidata = "wikidata:"

// WikidataRule is the rule of the rows a merge on item qid of extract
// writes.
func WikidataRule(qid, extract string) string { return ruleWikidata + qid + "@" + extract }

// IsWikidataRule reports a rule of a Wikidata merge.
func IsWikidataRule(rule string) bool { return strings.HasPrefix(rule, ruleWikidata) }

// WikidataReport is what the Wikidata evidence did in a pass.
type WikidataReport struct {
	// Extract is the extract read; empty when there was none.
	Extract string `json:"extract"`
	// Named are the entities whose display name is a main label of one
	// item alone (rules 1 and 2).
	Named int `json:"named"`
	// Excluded are the named entities kept out of the merges, by reason:
	// the item records a pseudonym, an initial, a short name beside a full
	// one, a namesake tag.
	Excluded map[string]int `json:"excluded"`
	// Groups are the items two or more entities may merge on.
	Groups int `json:"groups"`
	// Merges are the entities merged into another, by kind.
	Merges map[string]int `json:"merges"`
	// ScriptConflicts are the items whose entities go by two keys in one
	// script: only the entities of one key merged.
	ScriptConflicts int `json:"script_conflicts"`
	// SharedBook are the sets left apart: two of them on one book.
	SharedBook int `json:"shared_book"`
	// References are the entities given their item (author_entity_reference);
	// ReferencesWithheld those named by an item whose display name is a short
	// name's beside a full one: no item.
	References         int `json:"references"`
	ReferencesWithheld int `json:"references_withheld"`
}

func newWikidataReport() WikidataReport {
	return WikidataReport{Excluded: map[string]int{}, Merges: map[string]int{}}
}

// WikidataDecision is one merge of the evidence, for the hand check: an
// entity merged into the anchor of an item.
type WikidataDecision struct {
	Kind string
	QID  string
	// Member and Anchor are the credits of the two sides.
	Member, Anchor []int64
}

// component is a key component after the typo merge.
type component struct {
	root    int
	keys    []int
	credits []int
	books   map[int64]bool
	display string
	// key is the display name's key, script its script.
	key, script string
}

// mergeWikidata merges the components the reference links (see the rules
// above). It runs after the typo merge.
func (b *builder) mergeWikidata() {
	b.typoRoot = make([]int, len(b.keys))
	for i := range b.keys {
		b.typoRoot[i] = b.find(i)
	}
	ref := b.in.Reference
	if ref == nil {
		return
	}
	w := &b.rep.Wikidata
	w.Extract = ref.Extract
	byItem := map[string][]*component{}
	for _, c := range b.wikidataComponents() {
		qid := b.mainLabelOf(c.key)
		if qid == "" {
			continue
		}
		w.Named++
		if reason := b.excluded(c, qid); reason != "" {
			w.Excluded[reason]++
			continue
		}
		byItem[qid] = append(byItem[qid], c)
	}
	for _, qid := range sortedKeys(byItem) {
		b.mergeItem(qid, byItem[qid])
	}
}

// wikidataComponents are the person components of automatic key groups, in
// root order, with their credits, books and display names.
func (b *builder) wikidataComponents() []*component {
	byRoot := map[int]*component{}
	for _, g := range b.keys {
		root := b.find(g.idx)
		c := byRoot[root]
		if c == nil {
			c = &component{root: root, books: map[int64]bool{}}
			byRoot[root] = c
		}
		c.keys = append(c.keys, g.idx)
		for _, ci := range g.credits {
			c.credits = append(c.credits, ci)
			c.books[b.in.Credits[ci].BookID] = true
		}
	}
	out := make([]*component, 0, len(byRoot))
	for _, root := range sortedKeys(byRoot) {
		c := byRoot[root]
		// The items are people: a collective is never one of them.
		if b.keys[root].key.kind == KindCollective {
			continue
		}
		slices.Sort(c.credits)
		c.display = b.display(c.credits).DisplayName
		c.key = b.in.Reference.NameKey[c.display]
		c.script = scriptOf(c.display)
		out = append(out, c)
	}
	return out
}

// mainLabelOf is the item whose main label key is, when no other item of
// the extract carries key in any way (rules 1 and 2); empty otherwise.
func (b *builder) mainLabelOf(key string) string {
	if key == "" {
		return ""
	}
	ref := b.in.Reference
	qid := ""
	for _, n := range ref.KeyItems[key] {
		if qid != "" && n.QID != qid {
			return ""
		}
		qid = n.QID
	}
	if it := ref.Items[qid]; it == nil || !slices.Contains(it.MainLabelKeys, key) {
		return ""
	}
	return qid
}

// hasPseudonyms reports an item recording a pseudonym (P742): out of this
// phase (rule 3).
func (b *builder) hasPseudonyms(qid string) bool {
	return len(b.in.Reference.Items[qid].PseudonymKeys) > 0
}

// excluded is why a component named by an item stays out of its merges,
// empty when it may merge: the item's pseudonyms (rule 3), an initial in the
// display name (rule 4), a short name's bucket beside a full name (rule 5),
// a namesake tag. A tagged key is a component alone — the typo merge joins
// full, untagged names only, D2b untagged ones — so its root's key tells; a
// short name's key may be in a D2b component, which stays out with it.
func (b *builder) excluded(c *component, qid string) string {
	switch {
	case b.hasPseudonyms(qid):
		return WikidataExcludedPseudonym
	case b.initialsIn(c):
		return WikidataExcludedInitials
	case slices.ContainsFunc(c.keys, b.shortBesideFull):
		return WikidataExcludedShortName
	case b.keys[c.root].key.tagged != "":
		return WikidataExcludedNamesake
	}
	return ""
}

// shortBesideFull reports a key of a short name's bucket with a fuller
// variant of the same family and given name in the catalog.
func (b *builder) shortBesideFull(k int) bool { return b.bucketKey(&b.keys[k].key) }

// bucketKey reports a short name's key with a fuller variant of the same
// family and given name in the catalog.
func (b *builder) bucketKey(key *nameKey) bool {
	if !key.short() || key.family == "" || key.given == "" {
		return false
	}
	if b.fullBases == nil {
		b.fullBases = b.fullNameBases()
	}
	return b.fullBases[personBase(key)]
}

// fullNameBases are the family and given names of every person's full name
// in the catalog: automatic or put in an entity by hand, tagged or not, of
// initials or not — any of them makes the short name a bucket.
func (b *builder) fullNameBases() map[string]bool {
	out := map[string]bool{}
	add := func(k *nameKey) {
		if k.kind == KindPerson && !k.short() && k.family != "" && k.given != "" {
			out[personBase(k)] = true
		}
	}
	for _, g := range b.keys {
		add(&g.key)
	}
	cache := map[int64]keyed{}
	for _, ci := range sortedKeys(b.fixed) {
		if k, reason := b.keyOfCredit(&b.in.Credits[ci], cache); reason == "" {
			add(&k)
		}
	}
	return out
}

// personBase is a person's name without its additional part and its tag.
func personBase(k *nameKey) string {
	return k.family + "|" + k.given + "|" + k.suffix
}

// minGroup is how many components an item needs for a merge.
const minGroup = 2

// mergeItem merges the components an item names: all of them when they go
// by one key per script, else those of each key.
func (b *builder) mergeItem(qid string, cs []*component) {
	if len(cs) < minGroup {
		return
	}
	w := &b.rep.Wikidata
	w.Groups++
	byKey := map[string][]*component{}
	keyOfScript := map[string]string{}
	conflict := false
	for _, c := range cs {
		byKey[c.key] = append(byKey[c.key], c)
		if k, ok := keyOfScript[c.script]; ok && k != c.key {
			conflict = true
		}
		keyOfScript[c.script] = c.key
	}
	sets := [][]*component{cs}
	if conflict {
		w.ScriptConflicts++
		sets = nil
		for _, k := range sortedKeys(byKey) {
			sets = append(sets, byKey[k])
		}
	}
	rule := WikidataRule(qid, b.in.Reference.Extract)
	for _, set := range sets {
		b.mergeSet(qid, rule, set)
	}
}

// mergeSet merges a set of components into the one of most books, unless
// two of them are credited on one book.
func (b *builder) mergeSet(qid, rule string, set []*component) {
	if len(set) < minGroup {
		return
	}
	w := &b.rep.Wikidata
	for i, x := range set {
		for _, y := range set[i+1:] {
			if sharesBook(x.books, y.books) {
				w.SharedBook++
				return
			}
		}
	}
	set = slices.Clone(set)
	slices.SortStableFunc(set, func(x, y *component) int { return len(y.books) - len(x.books) })
	anchor := set[0]
	for _, m := range set[1:] {
		b.union(m.root, anchor.root)
		for _, k := range m.keys {
			b.wdRule[k] = rule
		}
		kind := WikidataCrossScript
		if m.key == anchor.key {
			kind = WikidataSameName
		}
		w.Merges[kind]++
		b.wdDecisions = append(b.wdDecisions, WikidataDecision{Kind: kind, QID: qid,
			Member: b.creditIDs(m.credits), Anchor: b.creditIDs(anchor.credits)})
	}
}

func (b *builder) creditIDs(credits []int) []int64 {
	out := make([]int64, len(credits))
	for i, ci := range credits {
		out[i] = b.in.Credits[ci].ID
	}
	slices.Sort(out)
	return out
}

func sharesBook(x, y map[int64]bool) bool {
	if len(y) < len(x) {
		x, y = y, x
	}
	for bk := range x {
		if y[bk] {
			return true
		}
	}
	return false
}

// hasInitial reports a name with an initial: a word of one letter once the
// name is cut at white space and at every punctuation mark but those that
// glue a name — an apostrophe, a hyphen (O'Dell, Жан-Поль) — whatever the
// mark: "A.", "(A)", "A,", "A．" (a fullwidth dot), "A.Smith", "J.R.R.",
// "J.-P.".
func hasInitial(name string) bool {
	words := strings.FieldsFunc(name, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.Is(unicode.Z, r) || unicode.IsPunct(r) && !gluesAName(r)
	})
	for _, w := range words {
		letters := 0
		for _, r := range w {
			if unicode.IsLetter(r) {
				letters++
			}
		}
		if letters == 1 {
			return true
		}
	}
	return false
}

// gluesAName reports a mark inside one word of a name: an apostrophe or a
// hyphen of any kind.
func gluesAName(r rune) bool {
	return unicode.Is(unicode.Pd, r) || strings.ContainsRune("'’`ʼ‘", r)
}

// initialsIn reports a component with an initial in its display name or in
// the given name or the additional part of any of its credits (rule 4).
func (b *builder) initialsIn(c *component) bool {
	if hasInitial(c.display) {
		return true
	}
	for _, ci := range c.credits {
		cr := &b.in.Credits[ci]
		if hasInitial(cr.Given) || slices.ContainsFunc(cr.Additional, hasInitial) {
			return true
		}
	}
	return false
}

// scriptOf is the script most of a name's letters are in: Cyrillic or Latin.
func scriptOf(s string) string {
	cyr, lat := 0, 0
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Cyrillic, r):
			cyr++
		case unicode.Is(unicode.Latin, r):
			lat++
		}
	}
	if cyr >= lat {
		return string(authornorm.ScriptCyrillic)
	}
	return string(authornorm.ScriptLatin)
}

// entityQID is the item an entity is: the one whose main label its display
// name is, carried by no other item, with no pseudonym recorded (rules 1–3),
// merged or not; none for a collective, and none when the display name is a
// short name's beside a full one in the catalog — a bucket of possibly
// several people (Алексей Иванов beside Алексей Викторович Иванов).
func (b *builder) entityQID(root int, credits []int) string {
	if b.in.Reference == nil || len(credits) == 0 {
		return ""
	}
	if root >= 0 && b.keys[root].key.kind == KindCollective {
		return ""
	}
	row := b.display(credits)
	if row.Kind == KindCollective {
		return ""
	}
	qid := b.mainLabelOf(b.in.Reference.NameKey[row.DisplayName])
	if qid == "" || b.hasPseudonyms(qid) {
		return ""
	}
	if b.displayIsABucket(row.DisplayName, credits) {
		b.rep.Wikidata.ReferencesWithheld++
		return ""
	}
	b.rep.Wikidata.References++
	return qid
}

// displayIsABucket reports a credit of the display name whose key is a short
// name's beside a full one.
func (b *builder) displayIsABucket(display string, credits []int) bool {
	cache := map[int64]keyed{}
	for _, ci := range credits {
		c := &b.in.Credits[ci]
		if c.DisplayName != display {
			continue
		}
		if k, reason := b.keyOfCredit(c, cache); reason == "" && b.bucketKey(&k) {
			return true
		}
	}
	return false
}
