package authorentity

import (
	"bytes"
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// Outcomes of a merge decision.
const (
	DecisionSame    = "same"
	DecisionRevoked = "revoked"
)

// MergeDecision is a live merge decision (E4b): the candidate of Digest is
// one person (same), or an administrator revoked that (revoked).
type MergeDecision struct {
	ID                     int64
	Digest                 [32]byte
	Kind, Script, ItemKind string
	Outcome                string
	// MembersSHA256, MembersA, MembersB and Credit are the candidate's
	// when it was decided: they tell why a decision lapsed.
	MembersSHA256      [32]byte
	MembersA, MembersB []FPKey
	Credit             *CreditKey
	// ReportID is the eval report the decision cites; 0 when none.
	ReportID int64
	// Withheld is why a model decision does not apply whatever its
	// candidate (E4d, §4.7): its kind is not registered for its
	// configuration (unregistered), the registration is withdrawn, or the
	// decision's script is blocked (script_blocked). Empty when it may.
	Withheld string
	// RegistrationID is the registration a model decision applies under —
	// the live one of its configuration and kind, else the one it was
	// inserted under (0 for none); WasApplied is true when the previous
	// pass applied the decision. They order the decisions a conflict holds.
	RegistrationID int64
	WasApplied     bool
}

// States of a decision in a pass.
const (
	DecisionApplied  = "applied"
	DecisionLapsed   = "lapsed"
	DecisionConflict = "conflict"
)

// Why a decision lapsed (besides the exclusion reasons of its candidate's
// members) or is in conflict.
const (
	// LapsedInputChanged: the pass has the candidate's members with another
	// card.
	LapsedInputChanged = "input_changed"
	// LapsedMembersChanged: no item of the pass has the candidate's members.
	LapsedMembersChanged = "members_changed"
	// LapsedExtractorMoved: the members are there under another extractor
	// version (§4.2 case 7).
	LapsedExtractorMoved = "extractor_moved"
	// LapsedCardFailed: the candidate's card panicked in the seal.
	LapsedCardFailed = "card_failed"
	// ConflictRevoked: the union would join a pair an administrator revoked.
	ConflictRevoked = "revoked"
	// ConflictD3Disagrees: D3 moved the book to another full name.
	ConflictD3Disagrees = "d3_disagrees"
	// ConflictTwoTargets: decisions send one book to two full names.
	ConflictTwoTargets = "two_targets"
	// ConflictVetoed: the union crosses a live veto (a decision older than
	// the veto, E4b-3).
	ConflictVetoed = "vetoed"
	// ConflictNotSame: the union would join the two sides of a pair the
	// production pair answered other than same (E4d, §4.2).
	ConflictNotSame = "not_same"
	// ConflictGivenName: the union would join two full Cyrillic given
	// names (or patronymics) of one family that are not one name (E4c).
	ConflictGivenName = "given_name"
)

// DecisionState is what a pass did with one decision.
type DecisionState struct {
	DecisionID    int64
	State, Reason string
}

// ModelMerge is the owning generation of an entity a model union absorbed:
// its newest merge-log row, the decision path and what it held then.
type ModelMerge struct {
	LogID        int64
	Entity, Into uuid.UUID
	DecisionIDs  []int64
	Members      []FPKey
	Credits      []CreditKey
}

// Outcomes of a revival (§5.2) and their reasons.
const (
	RevivalExact   = "exact"
	RevivalPartial = "partial"
	RevivalLapsed  = "lapsed"

	RevivalManualMember   = "manual_member"
	RevivalMemberGone     = "member_gone"
	RevivalExtractorMoved = "extractor_moved"
	RevivalMemberSplit    = "member_split"
	RevivalExtraMembers   = "extra_members"
	RevivalNoLiveMember   = "no_live_member"
	RevivalStillMerged    = "still_merged"
)

// Revival is what a pass did with one revivable entity: the keys its
// absorption recorded, those a current credit has, those its inheriting
// group holds.
type Revival struct {
	Entity                   uuid.UUID
	LogID                    int64
	Outcome, Reason          string
	Recorded, Live, Restored int
}

// ModelReport is the decisions' part of a pass (E4b).
type ModelReport struct {
	Decisions int            `json:"decisions"`
	Applied   int            `json:"applied"`
	Lapsed    map[string]int `json:"lapsed"`
	Conflict  map[string]int `json:"conflict"`
	// Unions joined level-0 components; BookMoves moved a bucket book.
	Unions    int           `json:"unions"`
	BookMoves int           `json:"book_moves"`
	Revivals  RevivalReport `json:"revivals"`
}

// RevivalReport counts the revivals by outcome and reason.
type RevivalReport struct {
	Exact   int            `json:"exact"`
	Partial map[string]int `json:"partial"`
	Lapsed  map[string]int `json:"lapsed"`
}

// modelStepHook runs as a pass enters its decision step (a test seam).
var modelStepHook func()

// NeedsMergeCensus reports a pass whose plan depends on its sealed
// candidates: it has decisions, or entities a model union absorbed.
func (p *Plan) NeedsMergeCensus() bool {
	return p.b != nil && (len(p.b.in.Decisions) > 0 || len(p.b.old.ModelMerges) > 0)
}

// DecisionStates are what the pass did with each decision, by ID.
func (p *Plan) DecisionStates() []DecisionState { return p.states }

// Revivals are the pass's revivals, by entity.
func (p *Plan) Revivals() []Revival { return p.revivals }

// ApplyMergeDecisions is the decision step of a sealed pass (§5): the same
// decisions whose digest the pass generated join their level-0 components,
// D3 runs again over the joined ones, the book decisions move their books,
// and the IDs are given out again with the absorbed entities of decisions
// no longer applied revived. A pass with no decision and no model merge is
// returned as it is; one that needs its census and has none is refused.
func (p *Plan) ApplyMergeDecisions() (*Plan, error) {
	if !p.NeedsMergeCensus() {
		return p, nil
	}
	if !p.sealed {
		return p, ErrNoCandidates
	}
	if modelStepHook != nil {
		modelStepHook()
	}
	m := newModelStep(p)
	m.evaluate()
	return m.replan(), nil
}

// modelStep carries the decision step of one pass.
type modelStep struct {
	b *builder
	p *Plan
	// The items of the pass by digest (candidates), by members hash, and
	// by members without extractor versions.
	byDigest    map[[32]byte]*sealedItem
	candMembers map[[32]byte]bool
	exclMembers map[[32]byte]string
	looseKeys   map[string]bool
	states      map[int64]*DecisionState
	entityDecs  []decided
	bookDecs    []decided
	applied     map[int64]bool
	rep         ModelReport
}

// decided is a decision with the current candidate of its digest.
type decided struct {
	d  *MergeDecision
	si *sealedItem
}

func newModelStep(p *Plan) *modelStep {
	m := &modelStep{b: p.b, p: p, byDigest: map[[32]byte]*sealedItem{}, candMembers: map[[32]byte]bool{},
		exclMembers: map[[32]byte]string{}, looseKeys: map[string]bool{}, states: map[int64]*DecisionState{},
		applied: map[int64]bool{}, rep: ModelReport{Lapsed: map[string]int{}, Conflict: map[string]int{},
			Revivals: RevivalReport{Partial: map[string]int{}, Lapsed: map[string]int{}}}}
	for _, si := range p.sealedAll {
		if si.it.reason == "" {
			m.byDigest[si.digest] = si
			m.candMembers[si.members] = true
		} else if _, ok := m.exclMembers[si.members]; !ok {
			m.exclMembers[si.members] = si.it.reason
		}
		m.looseKeys[looseMembersKey(si.a, si.b)] = true
	}
	for members := range p.cardFailed {
		m.exclMembers[members] = LapsedCardFailed
	}
	return m
}

// looseMembersKey names a pair's members whatever their extractor version
// and side order.
func looseMembersKey(a, b []FPKey) string {
	enc := func(side []FPKey) string {
		keys := make([]FPKey, len(side))
		for i, k := range side {
			keys[i] = FPKey{Fingerprint: k.Fingerprint, Ordinal: k.Ordinal}
		}
		slices.SortFunc(keys, compareFPKeys)
		return string(memberBytes(slices.Compact(keys)))
	}
	x, y := enc(a), enc(b)
	if y < x {
		x, y = y, x
	}
	return x + "\x1e" + y
}

// evaluate matches every same decision with the pass's candidates and
// settles the entity unions: a decision applies only on its exact digest;
// one that would bridge a not-same pair or two given names is held on its
// own (joinInOrder), and a set of unions that would join a revoked pair
// applies none of them.
func (m *modelStep) evaluate() {
	var same []*MergeDecision
	revoked := map[[32]byte]bool{}
	for i := range m.b.in.Decisions {
		d := &m.b.in.Decisions[i]
		if d.Outcome == DecisionRevoked {
			revoked[d.Digest] = true
			continue
		}
		same = append(same, d)
	}
	slices.SortFunc(same, func(x, y *MergeDecision) int { return bytes.Compare(x.Digest[:], y.Digest[:]) })
	for _, d := range same {
		m.rep.Decisions++
		si := m.byDigest[d.Digest]
		switch reason := ""; {
		case d.Withheld != "":
			// The registration gate comes first: a withdrawn kind or a
			// blocked script never applies, whatever the candidate.
			m.set(d.ID, DecisionLapsed, d.Withheld)
		case si == nil:
			// A candidate a live veto excludes is the decision's own pair
			// across the veto: a conflict, not a lapse (§4.11 step 4).
			if reason = m.lapseReason(d); reason == ExcludedRevokedPair {
				m.set(d.ID, DecisionConflict, ConflictVetoed)
				m.b.vetoRefused(VetoRefusedModel)
				continue
			}
			m.set(d.ID, DecisionLapsed, reason)
		case si.credit != nil:
			m.bookDecs = append(m.bookDecs, decided{d, si})
		default:
			m.entityDecs = append(m.entityDecs, decided{d, si})
		}
	}
	uf := m.joinInOrder()
	held := map[int]string{}
	for _, digest := range sortedDigests(revoked) {
		if si := m.byDigest[digest]; si != nil && si.credit == nil &&
			uf.find(si.sideA.root) == uf.find(si.sideB.root) {
			if root := uf.find(si.sideA.root); held[root] == "" {
				held[root] = ConflictRevoked
			}
		}
	}
	vetoed := m.vetoedUnions(uf)
	refused := map[int]bool{}
	var applied []decided
	for _, e := range m.entityDecs {
		switch root := uf.find(e.si.sideA.root); {
		case held[root] != "":
			m.set(e.d.ID, DecisionConflict, held[root])
		case vetoed[root]:
			m.set(e.d.ID, DecisionConflict, ConflictVetoed)
			// One refused union, however many decisions proposed it.
			if !refused[root] {
				refused[root] = true
				m.b.vetoRefused(VetoRefusedModel)
			}
		default:
			applied = append(applied, e)
		}
	}
	m.entityDecs = applied
}

// joinInOrder joins the entity decisions one by one in a stable order —
// the ones the previous pass applied first, then by registration and id —
// and holds each that would bridge a conflict on its own: a union joining
// the two sides of a pair the production pair answered different
// (not_same), or two full given names of one family that are not one name
// (given_name; never a decision the previous pass applied: a live merge
// keeps its state). Only that decision is held, never the component; m.entityDecs
// keeps the joined ones, and the union of them is returned.
func (m *modelStep) joinInOrder() intUnion {
	slices.SortStableFunc(m.entityDecs, func(x, y decided) int {
		if x.d.WasApplied != y.d.WasApplied {
			if x.d.WasApplied {
				return -1
			}
			return 1
		}
		return cmp.Or(cmp.Compare(x.d.RegistrationID, y.d.RegistrationID), cmp.Compare(x.d.ID, y.d.ID))
	})
	partners := map[int][]int{}
	for _, digest := range sortedDigests(m.b.in.NotSame) {
		if si := m.byDigest[digest]; si != nil && si.credit == nil {
			a, b := si.sideA.root, si.sideB.root
			partners[a] = append(partners[a], b)
			partners[b] = append(partners[b], a)
		}
	}
	uf := newIntUnion()
	roots := map[int][]int{}
	names := map[int]nameClasses{}
	component := func(r int) ([]int, nameClasses) {
		if _, ok := roots[r]; !ok {
			roots[r], names[r] = []int{r}, m.b.nameClassesOf(r)
		}
		return roots[r], names[r]
	}
	var joined []decided
	for _, e := range m.entityDecs {
		x, y := uf.find(e.si.sideA.root), uf.find(e.si.sideB.root)
		if x == y {
			joined = append(joined, e)
			continue
		}
		rx, nx := component(x)
		ry, ny := component(y)
		switch {
		case bridgesNotSame(uf, partners, rx, y):
			m.set(e.d.ID, DecisionConflict, ConflictNotSame)
			continue
		case !e.d.WasApplied && nx.joins(ny):
			m.set(e.d.ID, DecisionConflict, ConflictGivenName)
			continue
		}
		uf.union(x, y)
		r := uf.find(x)
		roots[r], names[r] = append(slices.Clone(rx), ry...), nx.merged(ny)
		joined = append(joined, e)
	}
	m.entityDecs = joined
	return uf
}

// bridgesNotSame reports a level-0 root of one component (rx) whose
// not-same partner is in the other (root y).
func bridgesNotSame(uf intUnion, partners map[int][]int, rx []int, y int) bool {
	for _, r := range rx {
		for _, q := range partners[r] {
			if uf.find(q) == y {
				return true
			}
		}
	}
	return false
}

// sortedDigests are a set's digests in byte order.
func sortedDigests(set map[[32]byte]bool) [][32]byte {
	out := make([][32]byte, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	slices.SortFunc(out, func(x, y [32]byte) int { return bytes.Compare(x[:], y[:]) })
	return out
}

// vetoedUnions are the proposed unions (by root of uf) that would join two
// level-0 components a live veto keeps apart: none of their decisions
// applies.
func (m *modelStep) vetoedUnions(uf intUnion) map[int]bool {
	b := m.b
	if len(b.vetoOf) == 0 {
		return nil
	}
	byUnion := map[int][]int{}
	for _, e := range m.entityDecs {
		for _, r := range []int{e.si.sideA.root, e.si.sideB.root} {
			u := uf.find(r)
			if !slices.Contains(byUnion[u], r) {
				byUnion[u] = append(byUnion[u], r)
			}
		}
	}
	out := map[int]bool{}
	for u, roots := range byUnion {
		acc := vetoMask{}
		for _, r := range roots {
			mr := b.vetoSidesOfKeys(b.membersOf(r))
			if acc.apart(mr) {
				out[u] = true
				break
			}
			for v, s := range mr {
				acc[v] |= s
			}
		}
	}
	return out
}

// lapseReason is why a decision's digest is not among the candidates.
func (m *modelStep) lapseReason(d *MergeDecision) string {
	if m.candMembers[d.MembersSHA256] {
		return LapsedInputChanged
	}
	if r, ok := m.exclMembers[d.MembersSHA256]; ok {
		return r
	}
	if m.looseKeys[looseMembersKey(d.MembersA, d.MembersB)] {
		return LapsedExtractorMoved
	}
	return LapsedMembersChanged
}

func (m *modelStep) set(id int64, state, reason string) {
	m.states[id] = &DecisionState{DecisionID: id, State: state, Reason: reason}
	switch state {
	case DecisionApplied:
		m.applied[id] = true
		m.rep.Applied++
	case DecisionLapsed:
		m.rep.Lapsed[reason]++
	case DecisionConflict:
		m.rep.Conflict[reason]++
	}
}

// replan plans the pass again with the decisions applied.
func (m *modelStep) replan() *Plan {
	b, p := m.b, m.p
	b.modelStep = true
	b.root0 = make([]int, len(b.keys))
	for i := range b.keys {
		b.root0[i] = b.find(i)
	}
	b.forest, b.modelRule = map[int][]forestEdge{}, map[int]string{}
	ruleEdges := map[int][]forestEdge{}
	for _, e := range m.entityDecs {
		m.set(e.d.ID, DecisionApplied, "")
		x, y := e.si.sideA.root, e.si.sideB.root
		if b.find(x) == b.find(y) {
			continue
		}
		edge := forestEdge{decision: e.d.ID, kind: e.d.Kind, report: e.d.ReportID}
		addEdge(b.forest, x, y, edge)
		addEdge(ruleEdges, x, y, edge)
		b.union(x, y)
		m.rep.Unions++
	}
	b.modelKeyRules(ruleEdges)
	b.movedTo, b.movedRule, b.movedMethod = map[int]int{}, map[int]string{}, map[int]string{}
	b.resetPlanReport()
	b.moveShortNameBooks()
	m.moveDecidedBooks()
	b.prepareRevival(func(mm *ModelMerge) bool {
		for _, id := range mm.DecisionIDs {
			if !m.applied[id] {
				return true
			}
		}
		return false
	})
	np := b.plan()
	np.cand, np.cardBooks, np.candidates, np.inputs, np.sample, np.sealed, np.sealedAll =
		p.cand, p.cardBooks, p.candidates, p.inputs, p.sample, p.sealed, p.sealedAll
	for _, id := range sortedKeys(m.states) {
		np.states = append(np.states, *m.states[id])
	}
	for _, r := range np.revivals {
		switch r.Outcome {
		case RevivalExact:
			m.rep.Revivals.Exact++
		case RevivalPartial:
			m.rep.Revivals.Partial[r.Reason]++
		default:
			m.rep.Revivals.Lapsed[r.Reason]++
		}
	}
	rep := m.rep
	np.Report.Model = &rep
	return np
}

// moveDecidedBooks applies the book decisions after D3: a book still in its
// bucket moves to the decision's full name; one D3 moved there already
// keeps D3's row; one D3 moved elsewhere, or that two decisions send to two
// full names, stays where it is.
func (m *modelStep) moveDecidedBooks() {
	b := m.b
	byCredit := map[int][]decided{}
	for _, e := range m.bookDecs {
		byCredit[e.si.it.credit] = append(byCredit[e.si.it.credit], e)
	}
	for _, ci := range sortedKeys(byCredit) {
		decs := byCredit[ci]
		targets := map[int]bool{}
		for _, e := range decs {
			targets[b.find(e.si.sideB.root)] = true
		}
		if len(targets) > 1 {
			for _, e := range decs {
				m.set(e.d.ID, DecisionConflict, ConflictTwoTargets)
			}
			continue
		}
		e := decs[0]
		target := b.find(e.si.sideB.root)
		if b.creditVetoedFrom(ci, b.membersOf(target)) {
			for _, e := range decs {
				m.set(e.d.ID, DecisionConflict, ConflictVetoed)
			}
			b.vetoRefused(VetoRefusedModel)
			continue
		}
		if to, moved := b.movedTo[ci]; moved {
			for _, e := range decs {
				if to == target {
					m.set(e.d.ID, DecisionApplied, "")
				} else {
					m.set(e.d.ID, DecisionConflict, ConflictD3Disagrees)
				}
			}
			continue
		}
		b.movedTo[ci], b.movedRule[ci], b.movedMethod[ci] = target, modelRuleOf(e.d.Kind, e.d.ReportID), MethodModel
		addEdge(b.forest, e.si.sideA.root, e.si.sideB.root,
			forestEdge{decision: e.d.ID, kind: e.d.Kind, report: e.d.ReportID})
		for _, e := range decs {
			m.set(e.d.ID, DecisionApplied, "")
		}
		m.rep.BookMoves++
	}
}

// ruleModel prefixes the rule of a row a model decision put in its entity:
// "model:swap@12", the kind and the eval report.
const ruleModel = "model:"

func modelRuleOf(kind string, report int64) string {
	if report == 0 {
		return ruleModel + kind
	}
	return ruleModel + kind + "@" + strconv.FormatInt(report, 10)
}

// IsModelRule reports a rule of a model decision.
func IsModelRule(rule string) bool { return strings.HasPrefix(rule, ruleModel) }

// forestEdge is one applied decision between two level-0 components.
type forestEdge struct {
	to       int
	decision int64
	kind     string
	report   int64
}

func addEdge(g map[int][]forestEdge, x, y int, e forestEdge) {
	ex, ey := e, e
	ex.to, ey.to = y, x
	g[x] = append(g[x], ex)
	g[y] = append(g[y], ey)
}

// modelKeyRules gives every key of a level-0 component a model union put
// into another the rule of the decision that brought it, walking each
// joined component from its root.
func (b *builder) modelKeyRules(edges map[int][]forestEdge) {
	ruleOf := map[int]string{}
	for _, start := range sortedKeys(edges) {
		if b.find(start) != start {
			continue
		}
		seen := map[int]bool{start: true}
		queue := []int{start}
		for len(queue) > 0 {
			c := queue[0]
			queue = queue[1:]
			for _, e := range edges[c] {
				if seen[e.to] {
					continue
				}
				seen[e.to] = true
				ruleOf[e.to] = modelRuleOf(e.kind, e.report)
				queue = append(queue, e.to)
			}
		}
	}
	for k := range b.keys {
		if r, ok := ruleOf[b.root0[k]]; ok {
			b.modelRule[k] = r
		}
	}
}

// modelPath is the decision path between the level-0 components of two
// entities of group g (§5.1): the decisions on the forest path, in order;
// nil when they are in one component or none joins them.
func (b *builder) modelPath(from, to uuid.UUID, g *group) []forestEdge {
	if len(b.forest) == 0 {
		return nil
	}
	x, y := b.componentOf(from, g), b.componentOf(to, g)
	if x == y {
		return nil
	}
	prev := map[int]forestEdge{}
	seen := map[int]bool{y: true}
	queue := []int{y}
	for len(queue) > 0 && !seen[x] {
		c := queue[0]
		queue = queue[1:]
		for _, e := range b.forest[c] {
			if seen[e.to] {
				continue
			}
			seen[e.to] = true
			back := e
			back.to = c
			prev[e.to] = back
			queue = append(queue, e.to)
		}
	}
	if !seen[x] {
		return nil
	}
	var path []forestEdge
	for c := x; c != y; c = prev[c].to {
		path = append(path, prev[c])
	}
	return path
}

// componentOf is the level-0 component holding the most of an entity's
// credits in g, the lowest on a tie; g's root when it holds none.
func (b *builder) componentOf(id uuid.UUID, g *group) int {
	count := map[int]int{}
	for _, ci := range g.credits {
		if b.oldOf[ci] == id {
			count[b.root0[b.creditKey[ci]]]++
		}
	}
	best, n := g.root, 0
	for _, c := range sortedKeys(count) {
		if count[c] > n {
			best, n = c, count[c]
		}
	}
	return best
}

// heldBy is what a stored entity holds in the pass: the fingerprints of its
// credits and the credit rows that put the others in it.
func (b *builder) heldBy(id uuid.UUID) ([]FPKey, []CreditKey) {
	var fps []FPKey
	var credits []CreditKey
	for _, ci := range sortedKeys(b.oldOf) {
		if b.oldOf[ci] != id {
			continue
		}
		c := &b.in.Credits[ci]
		if b.oldVia[ci] {
			credits = append(credits, CreditKey{CreditID: c.ID})
		} else if fp := fpKeyOf(c); !slices.Contains(fps, fp) {
			fps = append(fps, fp)
		}
	}
	slices.SortFunc(fps, compareFPKeys)
	return fps, credits
}

// revivedEntity is a revivable entity's live members in a pass.
type revivedEntity struct {
	mm      *ModelMerge
	credits map[int]bool
	// Keys recorded: with a current credit, without one, with a credit
	// fixed by hand; loose when they matched only without the extractor
	// version.
	recorded, live, gone, manual int
	loose                        bool
}

// prepareRevival gives every revivable stored model merge the current
// credits of the keys it recorded (§5.2 step 3): their synthetic
// membership, which replaces the stored one in oldMembership. A credit two
// revivable entities recorded goes to the innermost: the smaller record,
// then the earlier merge.
func (b *builder) prepareRevival(revivable func(*ModelMerge) bool) {
	b.revive, b.revived = map[int]uuid.UUID{}, map[uuid.UUID]*revivedEntity{}
	if len(b.old.ModelMerges) == 0 {
		return
	}
	byFP, byLoose, byCredit := map[FPKey][]int{}, map[FPKey][]int{}, map[int64][]int{}
	for i := range b.in.Credits {
		c := &b.in.Credits[i]
		fp := fpKeyOf(c)
		byFP[fp] = append(byFP[fp], i)
		byLoose[FPKey{Fingerprint: fp.Fingerprint, Ordinal: fp.Ordinal}] =
			append(byLoose[FPKey{Fingerprint: fp.Fingerprint, Ordinal: fp.Ordinal}], i)
		byCredit[c.ID] = append(byCredit[c.ID], i)
	}
	ids := make([]uuid.UUID, 0, len(b.old.ModelMerges))
	for id := range b.old.ModelMerges {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(x, y uuid.UUID) int { return strings.Compare(x.String(), y.String()) })
	for _, id := range ids {
		mm := b.old.ModelMerges[id]
		if e, ok := b.old.Entities[id]; !ok || e.MergedInto == nil || !revivable(&mm) {
			continue
		}
		r := b.liveMembers(&mm, byFP, byCredit, false)
		if r.live == 0 {
			if loose := b.liveMembers(&mm, byLoose, byCredit, true); loose.live > 0 {
				r = loose
			}
		}
		b.revived[id] = r
	}
	order := make([]uuid.UUID, 0, len(b.revived))
	for id := range b.revived {
		order = append(order, id)
	}
	slices.SortFunc(order, func(x, y uuid.UUID) int {
		rx, ry := b.revived[x], b.revived[y]
		return cmp.Or(cmp.Compare(rx.recorded, ry.recorded), cmp.Compare(rx.mm.LogID, ry.mm.LogID),
			strings.Compare(x.String(), y.String()))
	})
	for _, id := range order {
		for _, ci := range sortedKeys(b.revived[id].credits) {
			if _, taken := b.revive[ci]; !taken {
				b.revive[ci] = id
			}
		}
	}
}

// liveMembers are the current credits of an absorption's recorded keys,
// those a manual row fixes left out; loose matches the fingerprints
// without their extractor version.
func (b *builder) liveMembers(mm *ModelMerge, byFP map[FPKey][]int, byCredit map[int64][]int, loose bool) *revivedEntity {
	r := &revivedEntity{mm: mm, credits: map[int]bool{}, loose: loose,
		recorded: len(mm.Members) + len(mm.Credits)}
	key := func(cis []int) {
		if len(cis) == 0 {
			r.gone++
			return
		}
		live, fixed := false, false
		for _, ci := range cis {
			if _, byHand := b.fixed[ci]; byHand {
				fixed = true
				continue
			}
			r.credits[ci], live = true, true
		}
		if live {
			r.live++
		}
		if fixed {
			r.manual++
		}
	}
	for _, k := range mm.Members {
		if loose {
			k = FPKey{Fingerprint: k.Fingerprint, Ordinal: k.Ordinal}
		}
		key(byFP[k])
	}
	for _, k := range mm.Credits {
		key(byCredit[k.CreditID])
	}
	return r
}

// revivalOutcomes are what the pass did with each revivable entity (§5.2
// step 6): a group inherited it, whole or in part, or none did.
func (b *builder) revivalOutcomes(groups []*group) []Revival {
	groupOf := map[int]uuid.UUID{}
	byID := map[uuid.UUID]*group{}
	for _, g := range groups {
		byID[g.id] = g
		for _, ci := range g.credits {
			groupOf[ci] = g.id
		}
	}
	var out []Revival
	for _, id := range sortedUUIDs(b.revived) {
		r := b.revived[id]
		rv := Revival{Entity: id, LogID: r.mm.LogID, Recorded: r.recorded, Live: r.live}
		g, inherited := byID[id]
		if !inherited {
			rv.Outcome, rv.Reason = RevivalLapsed, RevivalStillMerged
			if r.live == 0 {
				rv.Reason = RevivalNoLiveMember
			}
			out = append(out, rv)
			continue
		}
		split, extra := false, false
		for ci := range r.credits {
			if groupOf[ci] != id {
				split = true
			}
		}
		for _, ci := range g.credits {
			if !r.credits[ci] {
				extra = true
			}
		}
		rv.Restored = b.restoredKeys(r, g)
		rv.Outcome, rv.Reason = RevivalPartial, r.partialReason(split, extra)
		if rv.Reason == "" {
			rv.Outcome = RevivalExact
		}
		out = append(out, rv)
	}
	return out
}

// partialReason is why an inherited revival is partial, the first that
// holds; empty when it is exact.
func (r *revivedEntity) partialReason(split, extra bool) string {
	switch {
	case r.manual > 0:
		return RevivalManualMember
	case r.gone > 0:
		return RevivalMemberGone
	case r.loose:
		return RevivalExtractorMoved
	case split:
		return RevivalMemberSplit
	case extra:
		return RevivalExtraMembers
	}
	return ""
}

// restoredKeys counts the recorded keys with a credit in the inheriting
// group.
func (b *builder) restoredKeys(r *revivedEntity, g *group) int {
	in := map[int]bool{}
	for _, ci := range g.credits {
		in[ci] = true
	}
	n := 0
	has := func(match func(c *Credit) bool) bool {
		for ci := range r.credits {
			if in[ci] && match(&b.in.Credits[ci]) {
				return true
			}
		}
		return false
	}
	for _, k := range r.mm.Members {
		if has(func(c *Credit) bool {
			fp := fpKeyOf(c)
			return fp.Fingerprint == k.Fingerprint && fp.Ordinal == k.Ordinal &&
				(r.loose || fp.ExtractorVersion == k.ExtractorVersion)
		}) {
			n++
		}
	}
	for _, k := range r.mm.Credits {
		if has(func(c *Credit) bool { return c.ID == k.CreditID }) {
			n++
		}
	}
	return n
}

func sortedUUIDs[V any](m map[uuid.UUID]V) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	slices.SortFunc(out, func(x, y uuid.UUID) int { return strings.Compare(x.String(), y.String()) })
	return out
}

// resetPlanReport clears what planning counts, before a pass plans again.
func (b *builder) resetPlanReport() {
	r := &b.rep
	r.Entities, r.InitialsEntities, r.Changes = 0, 0, 0
	r.EntitiesByKind, r.Rules = map[string]int{}, map[string]int{}
	r.Sizes, r.Top = nil, nil
	r.IDs = IDReport{}
	r.Buckets = BucketReport{MovedBy: map[string]int{}}
	r.Wikidata.References, r.Wikidata.ReferencesWithheld = 0, 0
	if r.Veto != nil {
		r.Veto.KeptInBucket = 0
	}
}

// intUnion is a small union-find over ints.
type intUnion map[int]int

func newIntUnion() intUnion { return intUnion{} }

func (u intUnion) find(x int) int {
	for {
		p, ok := u[x]
		if !ok || p == x {
			return x
		}
		if gp, ok := u[p]; ok && gp != p {
			u[x] = gp
		}
		x = p
	}
}

func (u intUnion) union(x, y int) {
	rx, ry := u.find(x), u.find(y)
	if rx != ry {
		u[max(rx, ry)] = min(rx, ry)
	}
}
