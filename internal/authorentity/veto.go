package authorentity

import (
	"encoding/hex"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// The durable veto of a revoke (§4.11, E4b-3): a revoke records, per side,
// every (book, raw fingerprint) of the side's member credits, and while the
// veto is live no union at any level joins its two sides. No name is
// recorded: a name is shared by namesakes and proves nothing about lineage.

// MergeVeto is a live (not retired) veto.
type MergeVeto struct {
	ID int64
	// ItemKind is the revoked candidate's: an entity pair, or a book item
	// whose side a is BookID with its credit's fingerprint.
	ItemKind string
	BookID   int64
	Members  []VetoMember
}

// Sides of a veto.
const (
	VetoSideA = "a"
	VetoSideB = "b"
)

// VetoMember is one recorded (book, fingerprint) of a side.
type VetoMember struct {
	Side        string
	BookID      int64
	Fingerprint string
}

// States of a veto in a pass.
const (
	// VetoAnchored: every recorded book is held or gone.
	VetoAnchored = "anchored"
	// VetoUnanchored: a recorded book is lost — it has current credits and
	// none carries the recorded fingerprint. The side is widened.
	VetoUnanchored = "unanchored"
	// VetoGone: nothing lost, and one side's fingerprints are credited
	// nowhere: the person left the catalog.
	VetoGone = "gone"
)

// VetoState is what a pass found of one veto.
type VetoState struct {
	VetoID                          int64
	State                           string
	HeldBooks, LostBooks, GoneBooks int
	// WidenedFingerprints are the fingerprints a lost book added to a side.
	WidenedFingerprints int
}

// VetoOverlap is a veto whose two sides share fingerprints, or one of
// whose sides is empty: no pass can keep them apart, and the pass is
// refused (§4.11, round 5).
type VetoOverlap struct {
	VetoID int64 `json:"veto_id"`
	Shared int   `json:"shared_fingerprints"`
	// EmptySide is a veto with no recorded book on a side: it could keep
	// nothing apart, and is refused like an overlap (fail closed).
	EmptySide bool `json:"empty_side,omitempty"`
}

// VetoReport is what the vetoes did in a pass; absent without a veto.
type VetoReport struct {
	Vetoes int            `json:"vetoes"`
	States map[string]int `json:"states"`
	// SplitD2 counts the key groups split because they held both sides of
	// a veto; SplitWidened those of them split through a widened side.
	SplitD2      int                `json:"veto_split_d2"`
	SplitWidened int                `json:"veto_split_widened"`
	Widened      []VetoWidenedSplit `json:"widened_splits,omitempty"`
	// D2bVetoed counts the word-sequence components left apart; Refused
	// the unions refused per pass (typo, wikidata, model).
	D2bVetoed int            `json:"d2b_vetoed"`
	Refused   map[string]int `json:"veto_refused"`
	// KeptInBucket counts the vetoed books D3 left in their bucket.
	KeptInBucket int           `json:"veto_kept_in_bucket"`
	Overlaps     []VetoOverlap `json:"overlaps,omitempty"`
}

// VetoWidenedSplit is a key group a widened side split (round 5, P2): the
// piece that kept the group's ID, the new one, and the fingerprints on the
// side only through the widening (hex).
type VetoWidenedSplit struct {
	VetoID       int64     `json:"veto_id"`
	Kept         uuid.UUID `json:"kept_entity"`
	New          uuid.UUID `json:"new_entity"`
	Fingerprints []string  `json:"widened_fingerprints"`
}

// Passes a veto refuses a union in.
const (
	VetoRefusedTypo     = "typo"
	VetoRefusedWikidata = "wikidata"
	VetoRefusedModel    = "model"
)

// vetoSide is one side of one live veto, by the veto's index.
type vetoSide struct {
	veto int
	side int // 0 is a, 1 is b
}

// liveVeto is a veto with its effective sides for the pass.
type liveVeto struct {
	MergeVeto
	// sides are the effective fingerprints, recorded the recorded ones.
	sides, recorded [2]map[string]bool
	state           VetoState
}

// widenedPiece is a credit a widened side split off its key group, with a
// credit of the group's other piece.
type widenedPiece struct {
	veto      int64
	split     int // a credit of the widened piece
	other     int // a credit of the rest, -1 when none
	widenedFP []string
}

// prepareVetoes reads the live vetoes (§4.11): each recorded book held,
// gone or lost by its current credits, the state, the widened sides, the
// fingerprint index, and the overlaps that refuse the pass.
func (b *builder) prepareVetoes() {
	if len(b.in.Vetoes) == 0 {
		return
	}
	credited := map[string]bool{}
	for i := range b.in.Credits {
		credited[b.in.Credits[i].Fingerprint] = true
	}
	b.vetoOf = map[string][]vetoSide{}
	rep := &VetoReport{States: map[string]int{}, Refused: map[string]int{}}
	for i := range b.in.Vetoes {
		v := &liveVeto{MergeVeto: b.in.Vetoes[i]}
		v.state.VetoID = v.ID
		b.vetoContinuity(v, credited)
		b.vetoes = append(b.vetoes, v)
		vi := len(b.vetoes) - 1
		for side := range 2 {
			for _, fp := range sortedKeys(v.sides[side]) {
				b.vetoOf[fp] = append(b.vetoOf[fp], vetoSide{veto: vi, side: side})
			}
		}
		shared := 0
		for fp := range v.sides[0] {
			if v.sides[1][fp] {
				shared++
			}
		}
		empty := len(v.recorded[0]) == 0 || len(v.recorded[1]) == 0
		if shared > 0 || empty {
			rep.Overlaps = append(rep.Overlaps, VetoOverlap{VetoID: v.ID, Shared: shared, EmptySide: empty})
		}
		rep.States[v.state.State]++
	}
	rep.Vetoes = len(b.vetoes)
	b.rep.Veto = rep
}

// vetoContinuity settles a veto's books, state and effective sides.
func (b *builder) vetoContinuity(v *liveVeto, credited map[string]bool) {
	type sideBook struct {
		side int
		book int64
	}
	recorded := map[sideBook][]string{}
	for side := range 2 {
		v.sides[side], v.recorded[side] = map[string]bool{}, map[string]bool{}
	}
	for _, m := range v.Members {
		side := 0
		if m.Side == VetoSideB {
			side = 1
		}
		v.recorded[side][m.Fingerprint] = true
		v.sides[side][m.Fingerprint] = true
		k := sideBook{side, m.BookID}
		recorded[k] = append(recorded[k], m.Fingerprint)
	}
	keys := make([]sideBook, 0, len(recorded))
	for k := range recorded {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(x, y sideBook) int {
		if x.side != y.side {
			return x.side - y.side
		}
		return int(x.book - y.book)
	})
	st := &v.state
	for _, k := range keys {
		current := b.in.VetoBooks[k.book]
		switch {
		case len(current) == 0:
			st.GoneBooks++
		case slices.ContainsFunc(recorded[k], func(fp string) bool { return slices.Contains(current, fp) }):
			st.HeldBooks++
		default:
			st.LostBooks++
			for _, fp := range current {
				if !v.sides[k.side][fp] {
					v.sides[k.side][fp] = true
					st.WidenedFingerprints++
				}
			}
		}
	}
	switch {
	case st.LostBooks > 0:
		st.State = VetoUnanchored
	case !anyCredited(v.recorded[0], credited) || !anyCredited(v.recorded[1], credited):
		st.State = VetoGone
	default:
		st.State = VetoAnchored
	}
}

func anyCredited(fps, credited map[string]bool) bool {
	for fp := range fps {
		if credited[fp] {
			return true
		}
	}
	return false
}

// VetoStates are the live vetoes as the pass found them, by veto.
func (p *Plan) VetoStates() []VetoState {
	if p.b == nil {
		return nil
	}
	out := make([]VetoState, len(p.b.vetoes))
	for i, v := range p.b.vetoes {
		out[i] = v.state
	}
	return out
}

// VetoOverlaps are the vetoes whose sides share fingerprints: a pass with
// any is refused whole.
func (p *Plan) VetoOverlaps() []VetoOverlap {
	if p.Report.Veto == nil {
		return nil
	}
	return p.Report.Veto.Overlaps
}

// vetoMask is a set of (veto, side) bits: side a is 1, side b is 2.
type vetoMask map[int]uint8

// vetoBothSides is a mask entry holding both sides of its veto.
const vetoBothSides uint8 = 1<<0 | 1<<1

func (m vetoMask) add(sides []vetoSide) {
	for _, s := range sides {
		m[s.veto] |= 1 << s.side
	}
}

// vetoSidesOfKeys are the veto sides the fingerprints of keys belong to.
func (b *builder) vetoSidesOfKeys(keys []int) vetoMask {
	m := vetoMask{}
	for _, k := range keys {
		for _, fp := range b.keys[k].fps {
			m.add(b.vetoOf[fp.Fingerprint])
		}
	}
	return m
}

// apart reports some veto with side a in x and side b in y, or the other
// way round.
func (x vetoMask) apart(y vetoMask) bool {
	for v, mx := range x {
		my := y[v]
		if (mx&1 != 0 && my&2 != 0) || (mx&2 != 0 && my&1 != 0) {
			return true
		}
	}
	return false
}

// both reports a veto with both of its sides in m.
func (m vetoMask) both() bool {
	for _, s := range m {
		if s == vetoBothSides {
			return true
		}
	}
	return false
}

// vetoApart is the one predicate every union consults (§4.11): some live
// veto has a fingerprint of one side in component x and one of the other in
// component y, over the components' full member sets.
func (b *builder) vetoApart(rx, ry int) bool {
	if len(b.vetoOf) == 0 {
		return false
	}
	return b.vetoSidesOfKeys(b.membersOf(rx)).apart(b.vetoSidesOfKeys(b.membersOf(ry)))
}

// vetoRefused counts a union a veto refused in pass.
func (b *builder) vetoRefused(pass string) {
	b.rep.Veto.Refused[pass]++
}

// splitVetoedGroups is D2 under the vetoes (§4.11 step 1): a key group
// whose credits carry fingerprints of both sides of a live veto is split.
// Each credit gets a veto part in its key, the sorted (veto, side) its
// fingerprint belongs to among the vetoes the group holds both sides of;
// credits of neither side keep the plain key.
func (b *builder) splitVetoedGroups() {
	if len(b.vetoOf) == 0 {
		return
	}
	for _, name := range sortedKeys(b.byKey) {
		g := b.byKey[name]
		m := vetoMask{}
		for _, ci := range g.credits {
			m.add(b.vetoOf[b.in.Credits[ci].Fingerprint])
		}
		var held []int
		for _, v := range sortedKeys(m) {
			if m[v] == vetoBothSides {
				held = append(held, v)
			}
		}
		if len(held) == 0 {
			continue
		}
		delete(b.byKey, name)
		b.rep.Veto.SplitD2++
		pieces := map[string][]int{}
		for _, ci := range g.credits {
			key := g.key
			key.veto = b.vetoPart(b.in.Credits[ci].Fingerprint, held)
			b.addToGroup(&key, ci)
			pieces[key.veto] = append(pieces[key.veto], ci)
		}
		b.noteWidenedSplit(pieces, held)
	}
}

// vetoPart is a fingerprint's veto part among the held vetoes: "id:side"
// for each, sorted; empty for a fingerprint of neither side.
func (b *builder) vetoPart(fp string, held []int) string {
	var parts []string
	for _, s := range b.vetoOf[fp] {
		if slices.Contains(held, s.veto) {
			parts = append(parts, strconv.FormatInt(b.vetoes[s.veto].ID, 10)+":"+string("ab"[s.side]))
		}
	}
	slices.Sort(parts)
	return strings.Join(parts, ",")
}

// noteWidenedSplit records the pieces of a split group that are on a side
// only through its widening, for the report.
func (b *builder) noteWidenedSplit(pieces map[string][]int, held []int) {
	counted := false
	for _, part := range sortedKeys(pieces) {
		credits := pieces[part]
		for _, vi := range held {
			v := b.vetoes[vi]
			var widened []string
			for _, ci := range credits {
				fp := b.in.Credits[ci].Fingerprint
				for side := range 2 {
					if v.sides[side][fp] && !v.recorded[side][fp] && !slices.Contains(widened, hex.EncodeToString([]byte(fp))) {
						widened = append(widened, hex.EncodeToString([]byte(fp)))
					}
				}
			}
			if len(widened) == 0 {
				continue
			}
			if !counted {
				b.rep.Veto.SplitWidened++
				counted = true
			}
			slices.Sort(widened)
			other := -1
			for _, p := range sortedKeys(pieces) {
				if p != part {
					other = pieces[p][0]
					break
				}
			}
			b.widenedPieces = append(b.widenedPieces, widenedPiece{veto: v.ID, split: credits[0], other: other,
				widenedFP: widened})
		}
	}
}

// reportWidenedSplits names the entities of the pieces once the pass gave
// out its IDs: the piece of an entity the store had kept the ID.
func (b *builder) reportWidenedSplits(p *Plan) {
	if b.rep.Veto == nil {
		return
	}
	b.rep.Veto.Widened = nil
	for _, w := range b.widenedPieces {
		split := p.entityOf[b.in.Credits[w.split].ID]
		var other uuid.UUID
		if w.other >= 0 {
			other = p.entityOf[b.in.Credits[w.other].ID]
		}
		kept, made := other, split
		if _, stored := b.old.Entities[split]; stored {
			if _, otherStored := b.old.Entities[other]; !otherStored {
				kept, made = split, other
			}
		}
		b.rep.Veto.Widened = append(b.rep.Veto.Widened, VetoWidenedSplit{VetoID: w.veto, Kept: kept, New: made,
			Fingerprints: w.widenedFP})
	}
}

// bookVetoedTo reports a book veto that keeps credit ci out of component
// root (§4.11 step 5): the credit is the veto's book with a fingerprint of
// side a, and root holds side b.
func (b *builder) bookVetoedTo(ci, root int) bool {
	if len(b.vetoOf) == 0 {
		return false
	}
	c := &b.in.Credits[ci]
	target := b.vetoSidesOfKeys(b.membersOf(root))
	for _, s := range b.vetoOf[c.Fingerprint] {
		v := b.vetoes[s.veto]
		if v.ItemKind == ItemBook && v.BookID == c.BookID && s.side == 0 && target[s.veto]&2 != 0 {
			return true
		}
	}
	return false
}

// creditVetoedFrom reports a veto with credit ci's fingerprint on one side
// and the keys of the other: a book item across a veto.
func (b *builder) creditVetoedFrom(ci int, keys []int) bool {
	if len(b.vetoOf) == 0 {
		return false
	}
	m := vetoMask{}
	m.add(b.vetoOf[b.in.Credits[ci].Fingerprint])
	return m.apart(b.vetoSidesOfKeys(keys))
}
