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
//
// A side may name fallback credits too (E4z): an alias spelling the pass
// keeps as written («Леонид Каганов (LLeo)»), whose credits are no member
// of a key. Their fingerprints anchor the side, count for the veto, and the
// fallback step puts their credits into the entity of the pin's keys — or,
// when no side has a key, into one entity of their own. A list's segments
// and a pair share one fingerprint across several people, and a
// placeholder names no one: a pin never moves them, and they anchor
// nothing.

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
	// PinSplit: the pin names fallback credits alone, and they join two
	// entities by name. A pin of fallback credits moves those credits into
	// one entity; it never joins two entities of keys, so its sides stay
	// apart.
	PinSplit = "split"
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
	fb := b.newPinFallback()
	m.rep.Pins = map[string]int{}
	joined := false
	pins := slices.Clone(b.in.Pins)
	slices.SortFunc(pins, func(x, y MergePin) int { return int(x.ID - y.ID) })
	for _, pin := range pins {
		a, c := pinKeys(byFP, pin.A), pinKeys(byFP, pin.B)
		fa, fc := fb.nodes(pin.A), fb.nodes(pin.B)
		keys, classes := fb.reach(slices.Concat(a, c), slices.Concat(fa, fc))
		state := PinApplied
		switch {
		case len(a)+len(fa) == 0 || len(c)+len(fc) == 0:
			state = PinUnanchored
		case b.pinVetoed(keys, fb.masks(classes)):
			state = PinVetoed
			b.vetoRefused(VetoRefusedPin)
		default:
			if b.joinPin(pin.ID, keys) {
				joined = true
			}
			fb.join(pin.ID, keys, classes)
		}
		m.pinStates = append(m.pinStates, PinState{PinID: pin.ID, State: state})
		m.rep.Pins[state]++
	}
	b.pinPlaces, b.pinsOfClass = fb.places(), fb.classPins()
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

// pinVetoed reports a live veto between any two components the pin would
// join — across its sides or within one, a side whose fingerprints a veto
// split since the pin recorded them included — the components of its keys
// and the veto sides of its fallback credits (extra) alike.
func (b *builder) pinVetoed(keys []int, extra []vetoMask) bool {
	if len(b.vetoOf) == 0 {
		return false
	}
	var masks []vetoMask
	for _, r := range b.rootsOf(keys) {
		masks = append(masks, b.vetoSidesOfKeys(b.membersOf(r)))
	}
	masks = append(masks, extra...)
	for i, x := range masks {
		for _, y := range masks[i+1:] {
			if x.apart(y) {
				return true
			}
		}
	}
	return false
}

// rootsOf are the distinct components of keys, in key order.
func (b *builder) rootsOf(keys []int) []int {
	var roots []int
	for _, k := range keys {
		if r := b.find(k); !slices.Contains(roots, r) {
			roots = append(roots, r)
		}
	}
	return roots
}

// joinPin joins the components of a pin's keys into the one of most
// credits and reports whether any two were apart; the keys it brought in go
// by the pin's rule.
func (b *builder) joinPin(id int64, keys []int) bool {
	roots := b.rootsOf(keys)
	if len(roots) <= 1 {
		return false
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

// pinPlace is where the fallback step puts a credit of a pinned
// fingerprint: with key (into the entity of its component), or, -1, into
// the one entity of its class; rule is the pin that placed it first, mask
// the veto sides of its class.
type pinPlace struct {
	key   int
	class int
	rule  string
	mask  vetoMask
}

// pinFallback joins the pinnable fingerprints of fallback credits across
// the pins of a pass: a union-find over them, each class with a key of the
// component it joined (-1 when none), its veto sides, the pins that joined
// it and, by node, the rule of the first pin that named it.
type pinFallback struct {
	b     *builder
	node  map[string]int
	fps   []string
	uf    intUnion
	key   map[int]int
	mask  map[int]vetoMask
	pins  map[int][]int64
	rule  map[int]string
	named map[int]bool
}

func (b *builder) newPinFallback() *pinFallback {
	fb := &pinFallback{b: b, node: map[string]int{}, uf: newIntUnion(), key: map[int]int{}, mask: map[int]vetoMask{},
		pins: map[int][]int64{}, rule: map[int]string{}, named: map[int]bool{}}
	for i := range b.fallback {
		f := &b.fallback[i]
		fp := b.in.Credits[f.ci].Fingerprint
		if _, ok := fb.node[fp]; ok || !b.pinnable(f) {
			continue
		}
		n := len(fb.fps)
		fb.node[fp], fb.fps = n, append(fb.fps, fp)
		fb.key[n] = -1
		m := vetoMask{}
		m.add(b.vetoOf[fp])
		fb.mask[n] = m
	}
	return fb
}

// pinnable reports a fallback credit a pin may move: one person or one
// collective, neither a list's segment, nor a pair, nor a placeholder.
func (b *builder) pinnable(f *fallbackCredit) bool {
	switch f.rule {
	case RuleListed, RulePlaceholder:
		return false
	case RuleAsNamed:
		_, _, pair := pairNames(&b.in.Credits[f.ci])
		return !pair
	}
	return f.ordinal == 0
}

// nodes are the pinnable fallback fingerprints of a side.
func (fb *pinFallback) nodes(fps []string) []int {
	var out []int
	for _, fp := range fps {
		if n, ok := fb.node[fp]; ok {
			out = append(out, n)
		}
	}
	return out
}

// reach are the keys a pin joins — its own and those an earlier pin joined
// its fallback fingerprints to — and the classes of those fingerprints.
func (fb *pinFallback) reach(keys, nodes []int) (all, classes []int) {
	all = slices.Clone(keys)
	for _, n := range nodes {
		c := fb.uf.find(n)
		if slices.Contains(classes, c) {
			continue
		}
		classes = append(classes, c)
		if k := fb.key[c]; k >= 0 {
			all = append(all, k)
		}
	}
	return all, classes
}

// masks are the veto sides of classes.
func (fb *pinFallback) masks(classes []int) []vetoMask {
	out := make([]vetoMask, len(classes))
	for i, c := range classes {
		out[i] = fb.mask[c]
	}
	return out
}

// join puts the classes of an applied pin into one, with a key of the
// component it joined; a fingerprint keeps the rule of the first pin that
// named it.
func (fb *pinFallback) join(id int64, keys, classes []int) {
	if len(classes) == 0 {
		return
	}
	rule := rulePin + strconv.FormatInt(id, 10)
	key, m := -1, vetoMask{}
	if len(keys) > 0 {
		key = keys[0]
	}
	pins := []int64{id}
	for _, c := range classes {
		for v, s := range fb.mask[c] {
			m[v] |= s
		}
		pins = append(pins, fb.pins[c]...)
		fb.uf.union(c, classes[0])
	}
	root := fb.uf.find(classes[0])
	slices.Sort(pins)
	fb.key[root], fb.mask[root], fb.pins[root] = key, m, slices.Compact(pins)
	// A fingerprint no pin named yet is a class of its own.
	for _, c := range classes {
		if !fb.named[c] {
			fb.named[c], fb.rule[c] = true, rule
		}
	}
}

// places are the fingerprints the applied pins named, by raw fingerprint.
func (fb *pinFallback) places() map[string]pinPlace {
	out := map[string]pinPlace{}
	for n, fp := range fb.fps {
		if !fb.named[n] {
			continue
		}
		c := fb.uf.find(n)
		out[fp] = pinPlace{key: fb.key[c], class: c, rule: fb.rule[n], mask: fb.mask[c]}
	}
	return out
}

// classPins are the pins that joined each class, by class.
func (fb *pinFallback) classPins() map[int][]int64 {
	out := map[int][]int64{}
	for n := range fb.fps {
		if c := fb.uf.find(n); fb.named[n] && out[c] == nil {
			out[c] = fb.pins[c]
		}
	}
	return out
}

// refusePins records, in the pass's pin states and report, what the
// fallback step found a class of pins of fallback credits alone in when it
// placed none of them (pinClassRoots): an applied pin of such a class is
// vetoed or split. These pins joined no key, so nothing else undoes.
func (m *modelStep) refusePins() {
	if len(m.b.pinRefused) == 0 {
		return
	}
	refused := map[int64]string{}
	for class, state := range m.b.pinRefused {
		for _, id := range m.b.pinsOfClass[class] {
			refused[id] = state
		}
	}
	for i := range m.pinStates {
		s := &m.pinStates[i]
		if state, ok := refused[s.PinID]; ok && s.State == PinApplied {
			m.rep.Pins[PinApplied]--
			if m.rep.Pins[PinApplied] == 0 {
				delete(m.rep.Pins, PinApplied)
			}
			s.State = state
			m.rep.Pins[state]++
		}
	}
}
