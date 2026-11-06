package authorentity

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// The spelling kinds (D2): the twins no other kind asks, each pair put to the
// model with the cards of both sides.
//
//   - typo: two full names of one script, suffix and every part but one, the
//     family or the given name, that differ by a small edit (typoVerdict,
//     whose word counts are frozen in typo_words.go).
//     Patronymics are the variant kind's and two different ones are two
//     people, so they never pair here. A pair of gender endings («Иванов»,
//     «Иванова») is asked only when the sides share a title or a series;
//   - name_form: a pair of swap, variant or initials the plain kinds keep out
//     as bucket_side (for initials only when it is the one target), and a
//     given name cut short («Дж. Олдридж», «Джеймс Олдридж») with one target.
//     One target is what a new question needs. A pair already applied keeps
//     its candidate when a second target appears (keepNameForm).
//
// Like the name kinds they take no part in the generation, the cap or the
// precedence of the others, so what a registered kind covers never changes,
// and a pair any other kind asks stays there. Unlike them a side of two
// scripts may pair through the keys that match: the item takes their script.
// A same joins as an orphan's does, after every orphan (joinOrphans), so a
// decision of these kinds never holds an orphan's.

// The spelling kinds.
const (
	CandidateTypo     = "typo"
	CandidateNameForm = "name_form"
)

// SpellingKinds are the spelling kinds.
var SpellingKinds = []string{CandidateTypo, CandidateNameForm}

// SpellingKind reports a spelling kind.
func SpellingKind(kind string) bool { return kind == CandidateTypo || kind == CandidateNameForm }

// The bounds of the typo rule, over the frozen sides of typo_words.go. A word
// is common on more than typoRareSides sides: two common words of other
// consonants are two names. A first letter that changes is a typo only of a
// word on at most typoRareFirst sides whose partner is on typoFirstRatio times
// as many.
const (
	typoLongWord   = 7
	typoEditsLong  = 2
	typoRareSides  = 8
	typoRareFirst  = 2
	typoFirstRatio = 5
	// maxAbbreviation is the longest given name read as cut short.
	maxAbbreviation = 3
)

// The outcomes of typoVerdict.
const (
	typoNo = iota
	typoYes
	typoGender
)

// typoRef is one key of a side in one slot: rest names every other part.
type typoRef struct {
	side   *candSide
	script string
	rest   string
}

// typoKey reports a key the typo rule reads.
func typoKey(k *nameKey) bool { return variantKey(k) && k.tagged == "" }

// typos pairs the sides of a typo (see above).
func (n *nameKindPairs) typos() {
	family, given := map[string][]typoRef{}, map[string][]typoRef{}
	for _, s := range n.g.sortedSides() {
		for _, k := range s.keys {
			key := &n.g.b.keys[k].key
			if !typoKey(key) {
				continue
			}
			common := key.script + "|" + key.suffix + "|" + key.additional + "|"
			family[key.family] = append(family[key.family], typoRef{side: s, script: key.script, rest: common + key.given})
			given[key.given] = append(given[key.given], typoRef{side: s, script: key.script, rest: common + key.family})
		}
	}
	n.typoSlot(family, false)
	n.typoSlot(given, true)
}

// typoSlot pairs the near words of one part of the name; a patronymic in the
// given name's place never pairs.
func (n *nameKindPairs) typoSlot(refs map[string][]typoRef, givenPart bool) {
	for _, pair := range nearWords(sortedKeys(refs)) {
		x, y := pair[0], pair[1]
		if givenPart && (patronymic(x) || patronymic(y)) {
			continue
		}
		if verdict := typoVerdict(x, y); verdict != typoNo {
			n.typoPairs(refs[x], refs[y], verdict == typoGender)
		}
	}
}

// typoPairs pairs the keys of two near words that agree on the rest.
func (n *nameKindPairs) typoPairs(xs, ys []typoRef, gender bool) {
	byRest := map[string][]typoRef{}
	for _, y := range ys {
		byRest[y.rest] = append(byRest[y.rest], y)
	}
	for _, x := range xs {
		for _, y := range byRest[x.rest] {
			n.addSpelling(CandidateTypo, x.script, x.side, y.side, gender)
		}
	}
}

// addSpelling records a pair of a spelling kind, side a the side of fewer
// books, unless it is one side or asked, and returns it, nil when it is not
// recorded.
func (n *nameKindPairs) addSpelling(kind, script string, x, y *candSide, gender bool) *candItem {
	if x == y || (n.asked[sidePair(x, y)] && !n.out[sidePair(x, y)]) {
		return nil
	}
	n.asked[sidePair(x, y)], n.out[sidePair(x, y)] = true, false
	if len(y.books) < len(x.books) || (len(y.books) == len(x.books) && y.root < x.root) {
		x, y = y, x
	}
	it := &candItem{kind: kind, script: script, a: x, b: y, credit: -1}
	it.reason = n.g.b.spellingReason(x, y, gender)
	n.items = append(n.items, it)
	return it
}

// spellingReason is why a spelling pair is no candidate, empty when it is
// one: a vetoed pair, a shared book, a namesake tag, a manual side, and for a
// pair of gender endings no book evidence.
func (b *builder) spellingReason(x, y *candSide, gender bool) string {
	switch {
	case len(b.vetoOf) > 0 && b.vetoSidesOfKeys(x.keys).apart(b.vetoSidesOfKeys(y.keys)):
		return ExcludedRevokedPair
	case sharesBooks(x.books, y.books):
		return ExcludedCoCredited
	case x.tagged || y.tagged:
		return ExcludedNamesakeTag
	case x.manual || y.manual:
		return ExcludedManualSide
	case gender && !b.sharesEvidence(x, y):
		return ExcludedTypoGender
	}
	return ""
}

// nearWords are the pairs of words within the typo distance, each pair once,
// in sorted order: the words sharing a form of them with letters deleted.
func nearWords(words []string) [][2]string {
	byForm := map[string][]int{}
	for i, w := range words {
		r := []rune(w)
		if len(r) < typoMinLetters {
			continue
		}
		for _, form := range deletedForms(r, typoEdits(len(r))) {
			byForm[form] = append(byForm[form], i)
		}
	}
	seen := map[[2]int]bool{}
	for _, list := range byForm {
		for i, x := range list {
			for _, y := range list[i+1:] {
				seen[[2]int{min(x, y), max(x, y)}] = true
			}
		}
	}
	pairs := make([][2]int, 0, len(seen))
	for p := range seen {
		pairs = append(pairs, p)
	}
	slices.SortFunc(pairs, func(x, y [2]int) int {
		if x[0] != y[0] {
			return x[0] - y[0]
		}
		return x[1] - y[1]
	})
	out := make([][2]string, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, [2]string{words[p[0]], words[p[1]]})
	}
	return out
}

// deletedForms are a word with up to edits letters deleted, each once.
func deletedForms(r []rune, edits int) []string {
	seen := map[string]bool{string(r): true}
	level := [][]rune{r}
	for range edits {
		var next [][]rune
		for _, w := range level {
			for i := range w {
				form := slices.Delete(slices.Clone(w), i, i+1)
				if s := string(form); !seen[s] {
					seen[s] = true
					next = append(next, form)
				}
			}
		}
		level = next
	}
	return sortedKeys(seen)
}

// typoEdits is how many edits a word of the length may hold: one, two in a
// long word.
func typoEdits(letters int) int {
	if letters >= typoLongWord {
		return typoEditsLong
	}
	return 1
}

// typoVerdict reads two near words: one word misspelled, or two words of one
// stem told apart only by a gender ending, or neither. It reads the names and
// the frozen table, never the catalog.
func typoVerdict(x, y string) int {
	return typoVerdictOfSides(x, y, typoWordSides(x), typoWordSides(y))
}

// typoVerdictOfSides is typoVerdict of words on nx and ny sides.
func typoVerdictOfSides(x, y string, nx, ny int) int {
	rx, ry := []rune(x), []rune(y)
	letters := min(len(rx), len(ry))
	if letters < typoMinLetters {
		return typoNo
	}
	if d := typoDistance(rx, ry); d == 0 || d > typoEdits(letters) {
		return typoNo
	}
	lo, hi := min(nx, ny), max(nx, ny)
	sound := rx[0] == ry[0] || sameSound(rx[0], ry[0])
	if !sound && (lo > typoRareFirst || hi < typoFirstRatio*lo) {
		return typoNo
	}
	if genderEndings(rx, ry) {
		return typoGender
	}
	sx, sy := consonants(x), consonants(y)
	if sx == sy || (rx[0] != ry[0] && consonants(string(rx[1:])) == consonants(string(ry[1:]))) {
		return typoYes
	}
	if lo > typoRareSides && !cutConsonant(sx, sy, x, y) {
		return typoNo
	}
	return typoYes
}

// typoDistance is the edit distance of two words with two neighboring
// letters traded as one edit.
func typoDistance(a, b []rune) int {
	rows := make([][]int, len(a)+1)
	for i := range rows {
		rows[i] = make([]int, len(b)+1)
		rows[i][0] = i
	}
	for j := range rows[0] {
		rows[0][j] = j
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			rows[i][j] = min(rows[i-1][j]+1, rows[i][j-1]+1, rows[i-1][j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				rows[i][j] = min(rows[i][j], rows[i-2][j-2]+1)
			}
		}
	}
	return rows[len(a)][len(b)]
}

// sameSounds are the first letters two spellings of one name trade: «Гарри»,
// «Харри»; «Вильям», «Уильям»; «Олександр», «Александр».
var sameSounds = [][2]rune{{'г', 'х'}, {'в', 'у'}, {'о', 'а'}, {'е', 'э'}, {'и', 'й'}, {'g', 'h'}, {'v', 'w'}, {'c', 'k'}}

// sameSound reports two letters of sameSounds.
func sameSound(a, b rune) bool {
	return slices.ContainsFunc(sameSounds, func(p [2]rune) bool {
		return (p[0] == a && p[1] == b) || (p[0] == b && p[1] == a)
	})
}

// genderEndingPairs are the ends a masculine and a feminine form of one stem
// differ by.
var genderEndingPairs = [][2]string{{"", "а"}, {"", "я"}, {"", "и"}, {"й", "я"}, {"ий", feminineEnd}, {"ой", feminineEnd},
	{"ый", feminineEnd}, {"", "a"}, {"", "e"}}

// feminineEnd is the feminine adjective ending of a family name.
const feminineEnd = "ая"

// genderEndings reports two words of one stem and a pair of gender ends.
func genderEndings(x, y []rune) bool {
	i := 0
	for i < len(x) && i < len(y) && x[i] == y[i] {
		i++
	}
	ex, ey := string(x[i:]), string(y[i:])
	return slices.ContainsFunc(genderEndingPairs, func(p [2]string) bool {
		return (p[0] == ex && p[1] == ey) || (p[0] == ey && p[1] == ex)
	})
}

// vowelLetters are the letters consonants leaves out.
const vowelLetters = "аеёиоуыэюяйьъіїєўaeiouy-"

// consonantSounds are the consonants spellings of one name trade.
var consonantSounds = strings.NewReplacer("х", "г", "ц", "с", "з", "с", "щ", "ш", "h", "g", "z", "s", "c", "k")

// consonants is a word without its vowels, signs and doubled letters, the
// consonants of one sound as one.
func consonants(word string) string {
	var out []rune
	for _, r := range consonantSounds.Replace(word) {
		if !strings.ContainsRune(vowelLetters, r) && (len(out) == 0 || out[len(out)-1] != r) {
			out = append(out, r)
		}
	}
	return string(out)
}

// cutConsonant reports two words that end in a consonant, one's consonants
// the start of the other's («Майк», «Майкл»; «Бернар», «Бернард»). Two that
// end in a vowel are two names («Мария», «Марина»).
func cutConsonant(sx, sy, x, y string) bool {
	if !strings.HasPrefix(sx, sy) && !strings.HasPrefix(sy, sx) {
		return false
	}
	last := func(w string) rune {
		r, _ := utf8.DecodeLastRuneInString(w)
		return r
	}
	return !strings.ContainsRune(vowelLetters, last(x)) && !strings.ContainsRune(vowelLetters, last(y))
}

// nameForms pairs the sides of a name_form (see above): existing are the
// items of the plain kinds.
func (n *nameKindPairs) nameForms(existing []*candItem) {
	targets := n.initialsTargets()
	for _, it := range existing {
		if it.reason != ExcludedBucketSide || it.credit >= 0 || (it.script != ScriptCyrl && it.script != ScriptLatn) {
			continue
		}
		switch it.kind {
		case CandidateSwap, CandidateVariant:
			n.addSpelling(CandidateNameForm, it.script, it.a, it.b, false)
		case CandidateInitials:
			if n.oneInitialsTarget(it, targets) {
				n.addSpelling(CandidateNameForm, it.script, it.a, it.b, false)
			} else {
				n.keepNameForm(it.script, it.a, it.b)
			}
		}
	}
	n.abbreviations()
}

// keepNameForm records a pair of several targets a pass applied (D5). A second
// full name makes the short one ambiguous for a new question, not for the pair
// the models accepted on these two cards, so the pair stays a candidate while
// its decision's members and card hold: the seal drops it on any other digest
// (keptDigest), and it counts against no partner cap. Every other hold of a
// candidate and a decision still applies to it.
func (n *nameKindPairs) keepNameForm(script string, x, y *candSide) {
	members := sealItem(n.g.b, &candItem{a: x, b: y, credit: -1}).members
	if !n.g.b.keptNameForms().members[members] {
		return
	}
	if it := n.addSpelling(CandidateNameForm, script, x, y, false); it != nil {
		it.kept = true
	}
}

// keptForms are the name_form decisions the last pass applied that nothing
// withholds, by members and by digest.
type keptForms struct {
	members map[[32]byte]bool
	digests map[[32]byte]bool
}

// keptNameForms are the pass's keptForms.
func (b *builder) keptNameForms() *keptForms {
	if b.kept == nil {
		b.kept = &keptForms{members: map[[32]byte]bool{}, digests: map[[32]byte]bool{}}
		for i := range b.in.Decisions {
			d := &b.in.Decisions[i]
			if d.Kind == CandidateNameForm && d.Outcome != DecisionRevoked && d.WasApplied && d.Withheld == "" {
				b.kept.members[d.MembersSHA256], b.kept.digests[d.Digest] = true, true
			}
		}
	}
	return b.kept
}

// keptDigest reports the digest of a decision keepNameForm kept.
func (b *builder) keptDigest(digest [32]byte) bool { return b.keptNameForms().digests[digest] }

// initialsTargets are the initials pairs of each side, of any reason.
func (n *nameKindPairs) initialsTargets() map[int]int {
	out := map[int]int{}
	for key := range n.g.pairs {
		if key.kind == CandidateInitials {
			out[key.lo]++
			out[key.hi]++
		}
	}
	return out
}

// oneInitialsTarget reports a pair whose side of initials has no other
// initials pair: many targets stay apart.
func (n *nameKindPairs) oneInitialsTarget(it *candItem, targets map[int]int) bool {
	for _, s := range []*candSide{it.a, it.b} {
		if n.g.b.sideOfInitials(s) {
			return targets[s.root] == 1
		}
	}
	return false
}

// sideOfInitials reports a side with a key of initials.
func (b *builder) sideOfInitials(s *candSide) bool {
	return slices.ContainsFunc(s.keys, func(k int) bool { return b.keys[k].key.initials() })
}

// abbreviations pairs a given name cut short with the one side of its full
// name.
func (n *nameKindPairs) abbreviations() {
	short, full := n.abbreviationKeys()
	for _, x := range short {
		wx := strings.Fields(x.key.given + " " + x.key.additional)
		if !writtenCut(x.side, wx[0]) {
			continue
		}
		targets := map[int]*candSide{}
		for _, y := range full[fullBase(x.key)] {
			wy := strings.Fields(y.key.given + " " + y.key.additional)
			if y.side != x.side && strings.HasPrefix(wy[0], wx[0]) && (len(wx) == 1 || initialsFit(wx[1:], wy[1:])) {
				targets[y.side.root] = y.side
			}
		}
		for _, root := range sortedKeys(targets) {
			if len(targets) == 1 {
				n.addSpelling(CandidateNameForm, x.key.script, x.side, targets[root], false)
			} else {
				n.keepNameForm(x.key.script, x.side, targets[root])
			}
		}
	}
}

// abbreviationKeys are the keys whose given name may be cut short, and the
// keys of a full given name by fullBase.
func (n *nameKindPairs) abbreviationKeys() (short []keyRef, full map[string][]keyRef) {
	full = map[string][]keyRef{}
	for _, s := range n.g.sortedSides() {
		for _, k := range s.keys {
			key := &n.g.b.keys[k].key
			if !abbreviationKey(key) {
				continue
			}
			first, _, _ := strings.Cut(key.given, " ")
			if letters := utf8.RuneCountInString(first); letters >= 2 && letters <= maxAbbreviation {
				short = append(short, keyRef{side: s, key: key})
			} else if letters >= typoMinLetters {
				full[fullBase(key)] = append(full[fullBase(key)], keyRef{side: s, key: key})
			}
		}
	}
	return short, full
}

// abbreviationKey reports a person's key of a family and a given name, of
// Cyrillic or Latin letters.
func abbreviationKey(k *nameKey) bool {
	return k.kind == KindPerson && k.family != "" && k.given != "" && k.tagged == "" &&
		(k.script == ScriptCyrl || k.script == ScriptLatn)
}

// fullBase names a family of one script and suffix.
func fullBase(k *nameKey) string { return k.script + "|" + k.suffix + "|" + k.family }

// writtenCut reports a word the side writes as cut short: of no vowel, or
// with a period after it. «Дж» and «Вл.» are; «Ли» and «Том», names of their
// own, are not.
func writtenCut(s *candSide, word string) bool {
	if !strings.ContainsAny(word, vowelLetters) {
		return true
	}
	return slices.ContainsFunc(s.names, func(name string) bool {
		return strings.Contains(strings.ToLower(name), word+".")
	})
}
