package authorentity

import (
	"slices"
	"strings"

	"github.com/google/uuid"
)

// groups are the entities to be, in a fixed order: the roots in key order,
// each with the credits that stay and the ones D3 moved in. A root every
// credit moved out of has no group.
func (b *builder) groups() []*group {
	byRoot := map[int]*group{}
	get := func(root int) *group {
		g := byRoot[root]
		if g == nil {
			g = &group{root: root, books: map[int64]bool{}}
			byRoot[root] = g
		}
		return g
	}
	for _, kg := range b.keys {
		for _, ci := range kg.credits {
			root := b.find(kg.idx)
			if to, moved := b.movedTo[ci]; moved {
				root = to
			}
			g := get(root)
			g.credits = append(g.credits, ci)
			g.books[b.in.Credits[ci].BookID] = true
		}
	}
	for _, kg := range b.keys {
		// A bucket every book moved out of disappears (§2.0); one merged
		// into another component is no bucket of its own any more.
		if kg.key.short() && kg.key.evidenceEligible() && len(kg.credits) > 0 && byRoot[kg.idx] == nil &&
			b.find(kg.idx) == kg.idx {
			b.rep.Buckets.Emptied++
		}
	}
	out := make([]*group, 0, len(byRoot))
	for _, root := range sortedKeys(byRoot) {
		g := byRoot[root]
		slices.Sort(g.credits)
		k := b.keys[root].key
		g.bucket = k.short() && k.evidenceEligible()
		out = append(out, g)
	}
	return out
}

// oldMembership is which live stored entity each current credit belonged
// to — its credit row, else its fingerprint row — by credit index, and the
// books of every such entity.
func (b *builder) oldMembership() (oldOf map[int]uuid.UUID, books map[uuid.UUID]map[int64]bool) {
	oldOf = map[int]uuid.UUID{}
	books = map[uuid.UUID]map[int64]bool{}
	for i := range b.in.Credits {
		c := &b.in.Credits[i]
		var id uuid.UUID
		if row, ok := b.old.Credits[CreditKey{CreditID: c.ID}]; ok {
			if row.EntityID == nil {
				continue
			}
			id = *row.EntityID
		} else if row, ok := b.old.Fingerprints[fpKeyOf(c)]; ok {
			id = row.EntityID
		} else {
			continue
		}
		if e, ok := b.old.Entities[id]; !ok || e.MergedInto != nil {
			continue
		}
		oldOf[i] = id
		if books[id] == nil {
			books[id] = map[int64]bool{}
		}
		books[id][c.BookID] = true
	}
	return oldOf, books
}

// anchored are the live entities a manual row points at: never merged away.
func (b *builder) anchored() map[uuid.UUID]bool {
	out := map[uuid.UUID]bool{}
	for _, r := range b.old.Credits {
		if r.Method == MethodManual && r.EntityID != nil {
			out[*r.EntityID] = true
		}
	}
	for _, r := range b.old.Fingerprints {
		if r.Method == MethodManual {
			out[r.EntityID] = true
		}
	}
	return out
}

// sharedBook is a book of a group through a stored entity.
type sharedBook struct {
	entity uuid.UUID
	book   int64
}

// overlap is the books one new group shares with one stored entity.
type overlap struct {
	g      *group
	order  int
	old    uuid.UUID
	shared int
	// rule is the Wikidata rule that brought the stored entity's credits
	// into the group, if one did.
	rule string
}

// assignIDs gives every group an ID (§3, ID stability): each inherits the
// stored entity it shares the most books with — one group per entity, so a
// split leaves the ID with the larger piece and a merge with the entity of
// more books; a group sharing none gets a new ID. A stored entity no group
// inherited goes into the group it shares the most books with
// (merged_into); one sharing none is left alone.
func (b *builder) assignIDs(groups []*group) map[uuid.UUID][]MergeRow {
	pairs := b.overlaps(groups)
	taken := map[uuid.UUID]bool{}
	for _, p := range pairs {
		if p.g.id != uuid.Nil || taken[p.old] {
			continue
		}
		p.g.id, taken[p.old] = p.old, true
		b.rep.IDs.Inherited++
	}
	newID := b.in.NewID
	if newID == nil {
		newID = func() uuid.UUID { return uuid.Must(uuid.NewV7()) }
	}
	for _, g := range groups {
		if g.id == uuid.Nil {
			g.id = newID()
			b.rep.IDs.New++
		}
	}
	return b.absorbUnmatched(pairs, taken)
}

// overlaps are the books every group shares with every stored entity
// through its credits, the largest first; ties go to the stored entity with more books, then to the
// larger group, then by ID and group order.
func (b *builder) overlaps(groups []*group) []overlap {
	oldOf, oldBooks := b.oldMembership()
	wdRule := b.wikidataCreditRules()
	var pairs []overlap
	for order, g := range groups {
		// Shared books count only through the group's own credits that
		// belonged to the stored entity: a co-author on the same book is
		// no member of it.
		shared := map[uuid.UUID]int{}
		rule := map[uuid.UUID]string{}
		seen := map[sharedBook]bool{}
		for _, ci := range g.credits {
			id, ok := oldOf[ci]
			if !ok {
				continue
			}
			if r := wdRule[ci]; r != "" && rule[id] == "" {
				rule[id] = r
			}
			if sb := (sharedBook{id, b.in.Credits[ci].BookID}); !seen[sb] {
				seen[sb] = true
				shared[id]++
			}
		}
		for id, n := range shared {
			pairs = append(pairs, overlap{g: g, order: order, old: id, shared: n, rule: rule[id]})
		}
	}
	slices.SortFunc(pairs, func(x, y overlap) int {
		switch {
		case x.shared != y.shared:
			return y.shared - x.shared
		case len(oldBooks[x.old]) != len(oldBooks[y.old]):
			return len(oldBooks[y.old]) - len(oldBooks[x.old])
		case len(x.g.books) != len(y.g.books):
			return len(y.g.books) - len(x.g.books)
		case x.old != y.old:
			return strings.Compare(x.old.String(), y.old.String())
		}
		return x.order - y.order
	})
	return pairs
}

// absorbUnmatched sends every stored entity no group inherited into the
// group it shares the most books with — the first of its pairs — unless a
// manual row anchors it.
func (b *builder) absorbUnmatched(pairs []overlap, taken map[uuid.UUID]bool) map[uuid.UUID][]MergeRow {
	anchored := b.anchored()
	absorb := map[uuid.UUID][]MergeRow{}
	for _, p := range pairs {
		if taken[p.old] || anchored[p.old] {
			continue
		}
		taken[p.old] = true
		m := MergeRow{From: p.old, To: p.g.id, Method: MethodDeterministic, SharedBooks: p.shared}
		if p.rule != "" {
			// The stored entity's credits came in on Wikidata evidence.
			m.Method, m.Rule = MethodEvidence, p.rule
		}
		absorb[p.g.id] = append(absorb[p.g.id], m)
		b.rep.IDs.Merged++
	}
	for id, e := range b.old.Entities {
		if e.MergedInto == nil && !taken[id] && !anchored[id] {
			b.rep.IDs.Orphaned++
		}
	}
	return absorb
}

// wikidataCreditRules are the Wikidata rules credits are in their group by,
// by credit index: those of a key merged on the evidence and not moved by
// D3.
func (b *builder) wikidataCreditRules() map[int]string {
	out := map[int]string{}
	for _, kg := range b.keys {
		for _, ci := range kg.credits {
			if _, moved := b.movedTo[ci]; !moved && b.wdRule[kg.idx] != "" {
				out[ci] = b.wdRule[kg.idx]
			}
		}
	}
	return out
}

// planner turns the groups of one pass into its plan.
type planner struct {
	b          *builder
	p          *Plan
	members    map[uuid.UUID][]int
	order      []uuid.UUID
	rootEntity map[int]uuid.UUID
	wantFP     map[FPKey]AssignmentRow
	wantCredit map[CreditKey]CreditRow
	units      map[uuid.UUID]*Unit
	cleanup    Unit
}

// plan turns the groups into the stored rows and the units that write the
// difference from what is stored.
func (b *builder) plan() *Plan {
	groups := b.groups()
	absorb := b.assignIDs(groups)
	pl := &planner{b: b, p: &Plan{entityOf: map[int64]uuid.UUID{}, ruleOf: map[int64]string{},
		entities: map[uuid.UUID]EntityRow{}, refs: map[uuid.UUID]string{}},
		members: map[uuid.UUID][]int{}, rootEntity: map[int]uuid.UUID{}, units: map[uuid.UUID]*Unit{}}
	pl.collectMembers(groups)
	pl.wantRows()
	pl.entityUnits(absorb)
	pl.diffReferences(groups, absorb)
	pl.diffFingerprints()
	pl.diffCredits()
	p := pl.p
	for _, id := range pl.order {
		if u := pl.units[id]; u.Changes() > 0 {
			p.Units = append(p.Units, *u)
		}
	}
	p.Units = append(p.Units, pl.cleanupUnits()...)
	p.components = b.components(p.Units)
	p.decisions = b.wdDecisions
	b.report(p, groups, pl.members, pl.wantFP)
	p.Report = b.rep
	p.Report.Changes = p.Changes()
	return p
}

// collectMembers are each entity's credits: its group's plus the credits
// fixed to it by hand, in the order the units are written.
func (pl *planner) collectMembers(groups []*group) {
	b := pl.b
	for _, g := range groups {
		pl.members[g.id] = g.credits
		pl.order = append(pl.order, g.id)
		pl.rootEntity[g.root] = g.id
	}
	for _, ci := range sortedKeys(b.fixed) {
		id := b.fixed[ci]
		c := &b.in.Credits[ci]
		if id == nil {
			pl.p.entityOf[c.ID], pl.p.ruleOf[c.ID] = uuid.Nil, MethodManual
			continue
		}
		if e, ok := b.old.Entities[*id]; !ok || e.MergedInto != nil {
			continue
		}
		if _, ok := pl.members[*id]; !ok {
			pl.order = append(pl.order, *id)
		}
		pl.members[*id] = append(pl.members[*id], ci)
		pl.p.entityOf[c.ID], pl.p.ruleOf[c.ID] = *id, MethodManual
	}
}

// wantRows are the automatic rows the pass stores: a fingerprint row for
// every fingerprint of a key whose root has an entity, and a credit row for
// every book D3 moved.
func (pl *planner) wantRows() {
	b := pl.b
	// A typo merge is told by the components before the Wikidata merge.
	keysPerRoot := map[int]int{}
	for _, kg := range b.keys {
		keysPerRoot[b.typoRoot[kg.idx]]++
	}
	pl.wantFP = map[FPKey]AssignmentRow{}
	for _, kg := range b.keys {
		root := b.find(kg.idx)
		id, ok := pl.rootEntity[root]
		if !ok {
			continue
		}
		rule, method := RuleD1, MethodDeterministic
		switch {
		case b.wdRule[kg.idx] != "":
			rule, method = b.wdRule[kg.idx], MethodEvidence
		case keysPerRoot[b.typoRoot[kg.idx]] > 1:
			rule = RuleTypo
		case len(kg.fps) > 1:
			rule = RuleD2
		}
		for _, fp := range kg.fps {
			pl.wantFP[fp] = AssignmentRow{Key: fp, EntityID: id, Method: method, Rule: rule}
		}
		for _, ci := range kg.credits {
			c := &b.in.Credits[ci]
			pl.p.entityOf[c.ID], pl.p.ruleOf[c.ID] = id, rule
		}
	}
	pl.wantCredit = map[CreditKey]CreditRow{}
	for ci, root := range b.movedTo {
		c := &b.in.Credits[ci]
		id := pl.rootEntity[root]
		key := CreditKey{CreditID: c.ID}
		pl.wantCredit[key] = CreditRow{Key: key, EntityID: &id, Method: MethodEvidence, Rule: b.movedRule[ci]}
		pl.p.entityOf[c.ID], pl.p.ruleOf[c.ID] = id, b.movedRule[ci]
	}
}

// entityUnits start a unit per entity: its row when it differs from the
// stored one, and the entities it absorbs.
func (pl *planner) entityUnits(absorb map[uuid.UUID][]MergeRow) {
	b := pl.b
	for _, id := range pl.order {
		u := &Unit{EntityID: id, Absorb: absorb[id]}
		row := b.display(pl.members[id])
		row.ID = id
		old, stored := b.old.Entities[id]
		if stored && old.NamePinned {
			row.Kind, row.DisplayName, row.SortName, row.SearchKey, row.NamePinned =
				old.Kind, old.DisplayName, old.SortName, old.SearchKey, true
		}
		if !stored || !old.sameAs(&row) {
			r := row
			u.Entity = &r
		}
		pl.p.entities[id] = row
		pl.units[id] = u
	}
}

// diffReferences gives every entity its Wikidata item (entityQID) and drops
// the items of the entities it absorbs: the survivor of a merge has its
// group's item, each piece of a split its own. A pinned row is an
// administrator's and stays as it is.
func (pl *planner) diffReferences(groups []*group, absorb map[uuid.UUID][]MergeRow) {
	b := pl.b
	rootOf := map[uuid.UUID]int{}
	for _, g := range groups {
		rootOf[g.id] = g.root
	}
	extract := ""
	if b.in.Reference != nil {
		extract = b.in.Reference.Extract
	}
	for _, id := range pl.order {
		u := pl.units[id]
		key := RefKey{EntityID: id, Source: SourceWikidata}
		old, stored := b.old.References[key]
		root, ok := rootOf[id]
		if !ok {
			// An entity of manual credits alone.
			root = -1
		}
		// A pinned item is the administrator's; the absorbed entities'
		// unpinned items go all the same.
		if stored && old.Pinned {
			pl.p.refs[id] = old.Ref
		} else {
			want := b.entityQID(root, pl.members[id])
			switch {
			case want != "":
				pl.p.refs[id] = want
				if !stored || old.Ref != want || old.Extract != extract {
					u.References = append(u.References, ReferenceRow{Key: key, Ref: want, Extract: extract})
				}
			case stored:
				u.DeleteReferences = append(u.DeleteReferences, key)
			}
		}
		for _, m := range absorb[id] {
			gone := RefKey{EntityID: m.From, Source: SourceWikidata}
			if r, ok := b.old.References[gone]; ok && !r.Pinned {
				u.DeleteReferences = append(u.DeleteReferences, gone)
			}
		}
	}
}

// diffFingerprints adds the fingerprint rows that differ from the stored
// ones to their entity's unit; the cleanup removes the automatic rows no
// entity carries any more. Manual rows are never touched.
func (pl *planner) diffFingerprints() {
	old := pl.b.old.Fingerprints
	for _, fp := range sortedFPKeys(pl.wantFP) {
		want := pl.wantFP[fp]
		if have, ok := old[fp]; ok && (have.Method == MethodManual || have == want) {
			continue
		}
		u := pl.units[want.EntityID]
		u.Fingerprints = append(u.Fingerprints, want)
	}
	for _, fp := range sortedFPKeys(old) {
		if _, ok := pl.wantFP[fp]; !ok && old[fp].Method != MethodManual {
			pl.cleanup.DeleteFingerprints = append(pl.cleanup.DeleteFingerprints, fp)
		}
	}
}

// diffCredits does the same for the credit rows: a stored automatic row the
// pass no longer wants goes with the unit of the credit's entity, or with
// the cleanup when the credit has none.
func (pl *planner) diffCredits() {
	old := pl.b.old.Credits
	for _, key := range sortedCreditKeys(pl.wantCredit) {
		want := pl.wantCredit[key]
		if have, ok := old[key]; ok && (have.Method == MethodManual || sameCreditRow(&have, &want)) {
			continue
		}
		u := pl.units[*want.EntityID]
		u.Credits = append(u.Credits, want)
	}
	for _, key := range sortedCreditKeys(old) {
		if _, ok := pl.wantCredit[key]; ok || old[key].Method == MethodManual {
			continue
		}
		if id := pl.p.entityOf[key.CreditID]; id != uuid.Nil && pl.units[id] != nil {
			pl.units[id].DeleteCredits = append(pl.units[id].DeleteCredits, key)
		} else {
			pl.cleanup.DeleteCredits = append(pl.cleanup.DeleteCredits, key)
		}
	}
}

// cleanupUnits split the cleanup by the old entity of each removed row, in
// entity order: removing the rows of one old entity depends on what the
// pass does with that entity, not on the other entities' units.
func (pl *planner) cleanupUnits() []Unit {
	old := pl.b.old
	byEntity := map[uuid.UUID]*Unit{}
	unitOf := func(id uuid.UUID) *Unit {
		if byEntity[id] == nil {
			byEntity[id] = &Unit{}
		}
		return byEntity[id]
	}
	for _, fp := range pl.cleanup.DeleteFingerprints {
		u := unitOf(old.Fingerprints[fp].EntityID)
		u.DeleteFingerprints = append(u.DeleteFingerprints, fp)
	}
	for _, key := range pl.cleanup.DeleteCredits {
		var id uuid.UUID
		if e := old.Credits[key].EntityID; e != nil {
			id = *e
		}
		u := unitOf(id)
		u.DeleteCredits = append(u.DeleteCredits, key)
	}
	ids := make([]uuid.UUID, 0, len(byEntity))
	for id := range byEntity {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(x, y uuid.UUID) int { return strings.Compare(x.String(), y.String()) })
	out := make([]Unit, 0, len(ids))
	for _, id := range ids {
		out = append(out, *byEntity[id])
	}
	return out
}

// components group the units that depend on each other, by index into
// units: units that touch one entity — as their own, as one they absorb, or
// as the stored owner of a fingerprint or credit row they rewrite, or of
// the fingerprint row a credit they override is linked through — or one
// fingerprint (a credit's override depends on its fingerprint's row even
// when nothing was stored yet). A split or a merge is several units: the
// database writes or refuses them together, never a part (a bucket's
// fingerprint moved to another entity while the unit that takes its books
// over, and the merge, are refused, would lose the merge and link the
// books wrongly). Components are in the order of their first unit, units
// in plan order within each.
func (b *builder) components(units []Unit) [][]int {
	d := b.newDeps(len(units))
	for i := range units {
		d.unit(i, &units[i])
	}
	var out [][]int
	at := map[int]int{}
	for i := range units {
		r := d.find(i)
		if _, ok := at[r]; !ok {
			at[r] = len(out)
			out = append(out, nil)
		}
		out[at[r]] = append(out[at[r]], i)
	}
	return out
}

// deps joins units into components (union-find) by the keys they touch:
// entities and fingerprints.
type deps struct {
	parent []int
	// holder is the first unit to touch each key; a later one joins it.
	holder map[string]int
	// creditFP is each input credit's fingerprint; fpOwners and
	// creditOwners are the stored owners of every fingerprint (whatever the
	// ordinal) and of every credit.
	creditFP     map[int64]FPKey
	fpOwners     map[FPKey][]uuid.UUID
	creditOwners map[int64][]uuid.UUID
}

func (b *builder) newDeps(n int) *deps {
	d := &deps{parent: make([]int, n), holder: map[string]int{}, creditFP: make(map[int64]FPKey, len(b.in.Credits)),
		fpOwners: map[FPKey][]uuid.UUID{}, creditOwners: map[int64][]uuid.UUID{}}
	for i := range d.parent {
		d.parent[i] = i
	}
	for i := range b.in.Credits {
		c := &b.in.Credits[i]
		d.creditFP[c.ID] = fpKeyOf(c)
	}
	for k, row := range b.old.Fingerprints {
		base := baseFP(k)
		d.fpOwners[base] = append(d.fpOwners[base], row.EntityID)
	}
	for k, row := range b.old.Credits {
		if row.EntityID != nil {
			d.creditOwners[k.CreditID] = append(d.creditOwners[k.CreditID], *row.EntityID)
		}
	}
	return d
}

func baseFP(k FPKey) FPKey {
	return FPKey{Fingerprint: k.Fingerprint, ExtractorVersion: k.ExtractorVersion}
}

func (d *deps) find(i int) int {
	for d.parent[i] != i {
		d.parent[i] = d.parent[d.parent[i]]
		i = d.parent[i]
	}
	return i
}

func (d *deps) touch(i int, key string) {
	j, ok := d.holder[key]
	if !ok {
		d.holder[key] = i
		return
	}
	if ri, rj := d.find(i), d.find(j); ri != rj {
		d.parent[max(ri, rj)] = min(ri, rj)
	}
}

func (d *deps) entity(i int, id uuid.UUID) {
	if id != uuid.Nil {
		d.touch(i, "e"+id.String())
	}
}

func (d *deps) fingerprint(i int, k FPKey) {
	base := baseFP(k)
	d.touch(i, "f"+base.Fingerprint+"\x00"+base.ExtractorVersion)
	for _, id := range d.fpOwners[base] {
		d.entity(i, id)
	}
}

func (d *deps) credit(i int, id int64) {
	for _, owner := range d.creditOwners[id] {
		d.entity(i, owner)
	}
	if fp, ok := d.creditFP[id]; ok {
		d.fingerprint(i, fp)
	}
}

// unit joins unit i with every unit it depends on.
func (d *deps) unit(i int, u *Unit) {
	d.entity(i, u.EntityID)
	for _, m := range u.Absorb {
		d.entity(i, m.From)
		d.entity(i, m.To)
	}
	for _, row := range u.Fingerprints {
		d.fingerprint(i, row.Key)
	}
	for _, k := range u.DeleteFingerprints {
		d.fingerprint(i, k)
	}
	for _, row := range u.Credits {
		d.credit(i, row.Key.CreditID)
	}
	for _, k := range u.DeleteCredits {
		d.credit(i, k.CreditID)
	}
}

func sameCreditRow(a, b *CreditRow) bool {
	return a.Key == b.Key && equalID(a.EntityID, b.EntityID) && a.Method == b.Method && a.Rule == b.Rule
}

func compareFPKeys(x, y FPKey) int {
	if c := strings.Compare(x.Fingerprint, y.Fingerprint); c != 0 {
		return c
	}
	if c := strings.Compare(x.ExtractorVersion, y.ExtractorVersion); c != 0 {
		return c
	}
	return x.Ordinal - y.Ordinal
}

func sortedFPKeys[V any](m map[FPKey]V) []FPKey {
	out := make([]FPKey, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.SortFunc(out, compareFPKeys)
	return out
}

func sortedCreditKeys[V any](m map[CreditKey]V) []CreditKey {
	out := make([]CreditKey, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.SortFunc(out, func(x, y CreditKey) int {
		if x.CreditID != y.CreditID {
			if x.CreditID < y.CreditID {
				return -1
			}
			return 1
		}
		return x.Ordinal - y.Ordinal
	})
	return out
}
