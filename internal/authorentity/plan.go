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
		// A bucket every book moved out of disappears (§2.0).
		if kg.key.short() && kg.key.evidenceEligible() && len(kg.credits) > 0 && byRoot[kg.idx] == nil {
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
	var pairs []overlap
	for order, g := range groups {
		// Shared books count only through the group's own credits that
		// belonged to the stored entity: a co-author on the same book is
		// no member of it.
		shared := map[uuid.UUID]int{}
		seen := map[sharedBook]bool{}
		for _, ci := range g.credits {
			id, ok := oldOf[ci]
			if !ok {
				continue
			}
			if sb := (sharedBook{id, b.in.Credits[ci].BookID}); !seen[sb] {
				seen[sb] = true
				shared[id]++
			}
		}
		for id, n := range shared {
			pairs = append(pairs, overlap{g: g, order: order, old: id, shared: n})
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
		absorb[p.g.id] = append(absorb[p.g.id], MergeRow{From: p.old, To: p.g.id, Method: MethodDeterministic, SharedBooks: p.shared})
		b.rep.IDs.Merged++
	}
	for id, e := range b.old.Entities {
		if e.MergedInto == nil && !taken[id] && !anchored[id] {
			b.rep.IDs.Orphaned++
		}
	}
	return absorb
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
		entities: map[uuid.UUID]EntityRow{}, names: map[uuid.UUID][]Name{}},
		members: map[uuid.UUID][]int{}, rootEntity: map[int]uuid.UUID{}, units: map[uuid.UUID]*Unit{}}
	pl.collectMembers(groups)
	pl.wantRows()
	pl.entityUnits(absorb)
	pl.diffFingerprints()
	pl.diffCredits()
	p := pl.p
	for _, id := range pl.order {
		if u := pl.units[id]; u.Changes() > 0 {
			p.Units = append(p.Units, *u)
		}
	}
	if pl.cleanup.Changes() > 0 {
		p.Units = append(p.Units, pl.cleanup)
	}
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
	keysPerRoot := map[int]int{}
	for _, kg := range b.keys {
		keysPerRoot[b.find(kg.idx)]++
	}
	pl.wantFP = map[FPKey]AssignmentRow{}
	for _, kg := range b.keys {
		root := b.find(kg.idx)
		id, ok := pl.rootEntity[root]
		if !ok {
			continue
		}
		rule := RuleD1
		switch {
		case keysPerRoot[root] > 1:
			rule = RuleTypo
		case len(kg.fps) > 1:
			rule = RuleD2
		}
		for _, fp := range kg.fps {
			pl.wantFP[fp] = AssignmentRow{Key: fp, EntityID: id, Method: MethodDeterministic, Rule: rule}
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

// entityUnits start a unit per entity: its row and its names when they
// differ from the stored ones, and the entities it absorbs.
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
		names := b.namesOf(pl.members[id])
		pl.p.names[id] = names
		if !sameNames(b.old.Names[id], names) {
			u.Names, u.NamesChanged = names, true
		}
		pl.units[id] = u
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

// sameNames compares stored name variants with planned ones whatever order
// the store returned them in (the database sorts by its collation).
func sameNames(stored, planned []Name) bool {
	if len(stored) != len(planned) {
		return false
	}
	sorted := slices.Clone(stored)
	sortNames(sorted)
	return slices.Equal(sorted, planned)
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
