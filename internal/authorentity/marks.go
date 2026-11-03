package authorentity

import (
	"strings"

	"gopds-api/internal/authornorm"
)

// The marks of a name (E4p). The search key reads an apostrophe as a
// separator but a backtick, an acute accent or a modifier apostrophe as a
// letter: «Джон О'Хара» and «Джон О`Хара», «Наталья О'Шей» and «Наталья
// О`Шей» are two keys of one written name, which D1 joins in no other
// spelling. After the decisions, the step joins the entities of two keys
// that are one under markFold — every field, the suffix and the script the
// same — as D1 would have; the holds of the Wikidata step apply (not_same,
// the given-name guard, a shared book, a veto).

// RuleMarks is the rule of a key the marks step joined.
const RuleMarks = "marks"

// apostropheMarks are the marks the sources write an apostrophe with.
var apostropheMarks = strings.NewReplacer("`", "'", "´", "'", "ʼ", "'", "‘", "'", "’", "'")

// markFold is a key part with every apostrophe mark an apostrophe, keyed
// again.
func markFold(part string) string {
	return authornorm.SearchKey(apostropheMarks.Replace(part))
}

// markedKey is a person key under markFold; ok is false for a key the step
// leaves: not a person's, tagged, or one markFold does not change.
func markedKey(k *nameKey) (folded string, ok bool) {
	if k.kind != KindPerson || k.tagged != "" {
		return "", false
	}
	f := *k
	f.family, f.given, f.additional, f.name = markFold(k.family), markFold(k.given), markFold(k.additional),
		markFold(k.name)
	f.veto = ""
	return f.String(), f.family != k.family || f.given != k.given || f.additional != k.additional ||
		f.name != k.name
}

// mergeMarks joins the components of keys one under markFold (see above)
// and reports whether it joined any.
func (m *modelStep) mergeMarks() bool {
	b := m.b
	marked := map[string]bool{}
	byFold := map[string][]int{}
	for _, kg := range b.keys {
		f, changed := markedKey(&kg.key)
		if f == "" {
			continue
		}
		if changed {
			marked[f] = true
		}
		byFold[f] = append(byFold[f], kg.idx)
	}
	partners := m.notSamePartners()
	joined := false
	for _, f := range sortedKeys(marked) {
		keys := byFold[f]
		for _, k := range keys[1:] {
			if m.joinMarked(keys[0], k, partners) {
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

// joinMarked joins the components of keys x and y unless they are one or
// the holds keep them apart, and reports whether it did.
func (m *modelStep) joinMarked(x, y int, partners map[int][]int) bool {
	b := m.b
	rx, ry := b.find(x), b.find(y)
	if rx == ry || b.componentsShareABook(rx, ry) || b.notSameAcross(partners, rx, ry) ||
		b.nameClassesOf(rx).joins(b.nameClassesOf(ry)) {
		return false
	}
	if b.vetoApart(rx, ry) {
		b.vetoRefused(VetoRefusedMarks)
		return false
	}
	for _, k := range b.membersOf(ry) {
		if b.wdRule[k] == "" && b.modelRule[k] == "" {
			b.wdRule[k] = RuleMarks
		}
	}
	b.union(rx, ry)
	m.rep.Marks++
	return true
}
