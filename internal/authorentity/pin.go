package authorentity

import (
	"slices"
	"strconv"
)

// The hand join (E4o): an administrator pins two entities as one person —
// the raw fingerprints each side's credits carried when the pin was
// registered — and every pass joins the components those fingerprints are
// in, after the decisions, the Wikidata step and the other-language
// spelling. No candidate, digest or card changes, so no decision lapses.
// A fingerprint stays a credit's across rebuilds and archives, so a pin
// keeps joining what it named; a new credit of a pinned fingerprint joins
// with it. A pin never overrides a veto: a live veto between its sides
// leaves them apart (vetoed). A side none of whose fingerprints the pass
// holds joins nothing (unanchored). Only an administrator releases a pin, by
// revoking it.

// MergePin is a live (not revoked) hand join: the raw fingerprints of each
// side.
type MergePin struct {
	ID   int64
	A, B []string
}

// States of a pin in a pass.
const (
	// PinApplied: both sides are one entity.
	PinApplied = "applied"
	// PinVetoed: a live veto keeps the sides apart.
	PinVetoed = "vetoed"
	// PinUnanchored: a side's fingerprints are credited nowhere.
	PinUnanchored = "unanchored"
)

// PinState is what a pass did with one pin.
type PinState struct {
	PinID int64
	State string
}

// rulePin prefixes the rule of a key a pin joined: "pin:12".
const rulePin = "pin:"

// PinStates are what the pass did with each pin, by ID.
func (p *Plan) PinStates() []PinState { return p.pinStates }

// applyPins joins each live pin's sides (see above), records its state and
// reports whether it joined any.
func (m *modelStep) applyPins() bool {
	b := m.b
	if len(b.in.Pins) == 0 {
		return false
	}
	byFP := map[string][]int{}
	for _, kg := range b.keys {
		for _, fp := range kg.fps {
			byFP[fp.Fingerprint] = append(byFP[fp.Fingerprint], kg.idx)
		}
	}
	m.rep.Pins = map[string]int{}
	joined := false
	pins := slices.Clone(b.in.Pins)
	slices.SortFunc(pins, func(x, y MergePin) int { return int(x.ID - y.ID) })
	for _, pin := range pins {
		a, c := pinKeys(byFP, pin.A), pinKeys(byFP, pin.B)
		state := PinApplied
		switch {
		case len(a) == 0 || len(c) == 0:
			state = PinUnanchored
		case b.pinVetoed(a, c):
			state = PinVetoed
			b.vetoRefused(VetoRefusedPin)
		default:
			if b.joinPin(pin.ID, a, c) {
				joined = true
			}
		}
		m.pinStates = append(m.pinStates, PinState{PinID: pin.ID, State: state})
		m.rep.Pins[state]++
	}
	if joined {
		// A moved book follows its target into the entity it joined, as
		// after the Wikidata step.
		for ci, to := range b.movedTo {
			b.movedTo[ci] = b.find(to)
		}
	}
	return joined
}

// pinKeys are the keys holding a side's fingerprints.
func pinKeys(byFP map[string][]int, fps []string) []int {
	var out []int
	for _, fp := range fps {
		out = append(out, byFP[fp]...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// pinVetoed reports a live veto between the components of a pin's sides.
func (b *builder) pinVetoed(a, c []int) bool {
	for _, x := range a {
		for _, y := range c {
			if rx, ry := b.find(x), b.find(y); rx != ry && b.vetoApart(rx, ry) {
				return true
			}
		}
	}
	return false
}

// joinPin joins the components of a pin's keys into the one of most
// credits and reports whether any two were apart; the keys it brought in go
// by the pin's rule.
func (b *builder) joinPin(id int64, a, c []int) bool {
	var roots []int
	for _, k := range slices.Concat(a, c) {
		if r := b.find(k); !slices.Contains(roots, r) {
			roots = append(roots, r)
		}
	}
	credits := func(r int) int {
		n := 0
		for _, k := range b.membersOf(r) {
			n += len(b.keys[k].credits)
		}
		return n
	}
	slices.SortStableFunc(roots, func(x, y int) int { return credits(y) - credits(x) })
	for _, r := range roots[1:] {
		for _, k := range b.membersOf(r) {
			if b.wdRule[k] == "" && b.modelRule[k] == "" {
				b.wdRule[k] = rulePin + strconv.FormatInt(id, 10)
			}
		}
		b.union(r, roots[0])
	}
	return len(roots) > 1
}
