package authorentity

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"gopds-api/internal/authornorm"

	"github.com/google/uuid"
)

// Merge candidates (E4a, plan §2.1–§2.3, §4.2): the pairs a model may later
// be asked "same person?", generated deterministically on the level-0
// components — the build's components after D1, D2, D2b, the typo and the
// Wikidata merge, before any model decision — with the books D3 moved. A
// candidate is observation only: nothing applies it.

// The kinds of a candidate, highest precedence first.
const (
	CandidateBucket   = "bucket"
	CandidateSwap     = "swap"
	CandidateInitials = "initials"
	CandidateTranslit = "translit"
)

// candidateKinds are the kinds in precedence order.
var candidateKinds = []string{CandidateBucket, CandidateSwap, CandidateInitials, CandidateTranslit}

// The item kinds: a pair of entities, or one bucket book against a full
// name's entity.
const (
	ItemEntity = "entity"
	ItemBook   = "book"
)

// ScriptCyrlLatn is the script of a translit candidate; scriptMixed that of
// a side of keys of several scripts, or of a pair of two.
const (
	ScriptCyrlLatn = "Cyrl-Latn"
	scriptMixed    = string(authornorm.ScriptMixed)
)

// The closed set of reasons a pair is no candidate (§2.3).
const (
	ExcludedBucketSide       = "bucket_side"
	ExcludedCoCredited       = "co_credited"
	ExcludedNamesakeTag      = "namesake_tag"
	ExcludedPseudonymKey     = "pseudonym_key"
	ExcludedManualSide       = "manual_side"
	ExcludedRevokedPair      = "revoked_pair"
	ExcludedPartnerCap       = "partner_cap"
	ExcludedInitialsSideGT10 = "initials_side_gt10"
	ExcludedMixedScript      = "mixed_script"
	ExcludedNoFamilyOrGiven  = "no_family_or_given"
	ExcludedLooseScriptOnly  = "loose_script_only"
	ExcludedPrecedence       = "precedence"
)

// ExclusionReasons are the reasons, each counted per kind and script.
var ExclusionReasons = []string{ExcludedBucketSide, ExcludedCoCredited, ExcludedNamesakeTag, ExcludedPseudonymKey,
	ExcludedManualSide, ExcludedRevokedPair, ExcludedPartnerCap, ExcludedInitialsSideGT10, ExcludedMixedScript,
	ExcludedNoFamilyOrGiven, ExcludedLooseScriptOnly, ExcludedPrecedence}

// SampleCandidate is the reason of a sample row of a candidate.
const SampleCandidate = "candidate"

// mergeCandidateVersion prefixes every digest; it is also the eval item's and
// the job's extractor version.
const mergeCandidateVersion = "e4-cand-v2"

// MergeCandidate is one candidate of a pass, sealed: its digest names it.
type MergeCandidate struct {
	Kind, Script, ItemKind string
	// SideA and SideB are the entities after the pass in the canonical side
	// order (side A has the smaller member list; for a book item, side A is
	// the bucket the book stands in).
	SideA, SideB uuid.UUID
	// CreditID is a book item's bucket credit (ordinal CreditOrdinal).
	CreditID      int64
	CreditOrdinal int
	MembersA      []FPKey
	MembersB      []FPKey
	MembersSHA256 [32]byte
	InputSHA256   [32]byte
	Digest        [32]byte
}

// MergeCandidateSample is one explainable row of a (kind, script, reason):
// the sides' entities and Go keys, so the census SQL can be reconciled
// against what the build decided.
type MergeCandidateSample struct {
	Kind, Script, Reason string
	EntityA, EntityB     uuid.UUID
	CreditID             int64
	KeysA, KeysB         []string
	MembersSHA256        [32]byte
	// Digest is the candidate's; zero for an exclusion.
	Digest [32]byte
}

// CandidateReport is what candidate generation found, by kind and script.
type CandidateReport struct {
	ByKind map[string]map[string]*CandidateCounts `json:"by_kind"`
	// Candidates are all of them; Excluded every pair kept out.
	Candidates int `json:"candidates"`
	// Error is the class of a generation that failed; empty when none.
	Error    string `json:"error,omitempty"`
	Excluded int    `json:"excluded"`
	// CardBooks are the sample books the cards read; ItemsFailed the sides
	// whose handling panicked, left out (§1.5).
	CardBooks   int `json:"card_books"`
	ItemsFailed int `json:"items_failed"`
}

// CandidateCounts are the candidates of one kind and script and the pairs
// kept out of it, by reason.
type CandidateCounts struct {
	Candidates int            `json:"candidates"`
	Excluded   map[string]int `json:"excluded"`
}

// candidateSideHook runs on every credit of a side, candidateGenerateHook
// as generation starts, candidateSealHook as the seal does,
// candidateCardHook on every card book; a test makes them panic.
var (
	candidateSideHook     func(c *Credit)
	candidateGenerateHook func()
	candidateSealHook     func()
	candidateCardHook     func(book int64)
)

// CandidateErrorGenerate is the census error class of a generation that
// panicked: the plan stands, with no candidate.
const CandidateErrorGenerate = "generate_failed"

// ErrNoCandidates is the seal of a plan whose generation failed.
var ErrNoCandidates = errors.New("the pass generated no merge candidates")

// MergeCandidatesSealed reports a census sealed whole: only such a pass may
// write its candidates.
func (p *Plan) MergeCandidatesSealed() bool { return p.sealed }

// The bounds of §2.1: partners per side and kind, books of an initials side.
const (
	maxCandidatePartners = 20
	maxInitialsSideBooks = 10
	// minTranslitSkeleton is how short a family skeleton translit pairs.
	minTranslitSkeleton = 3
	// translitGivenPrefix is how many consonants of the given name's
	// skeleton the tight rule compares.
	translitGivenPrefix = 2
)

// candSide is one level-0 component as a side: the automatic keys of one
// root and the entity the pass gives it, with the books D3 moved in.
type candSide struct {
	root    int
	entity  uuid.UUID
	keys    []int
	members []FPKey
	// books are the entity's books after D3, sorted.
	books   []int64
	script  string
	display string
	// tokens is the display key's words, sorted: the swap shape.
	tokens string
	names  []string
	// variants are the members' nicknames.
	variants []string
	bucket   bool
	tagged   bool
	manual   bool
	noName   bool
}

// candItem is one pair or book item of a pass, with the reason it is no
// candidate (empty for a candidate).
type candItem struct {
	kind, script, reason string
	a, b                 *candSide
	// credit is a book item's bucket credit, by index into Input.Credits;
	// -1 for a pair.
	credit int
}

// candidates is what generation keeps for the seal.
type candidates struct {
	b     *builder
	items []*candItem
}

// collectCandidates generates the pass's candidates and exclusions on the
// level-0 components (§2.1) and counts them; the seal adds the cards.
func (b *builder) collectCandidates(p *Plan, groups []*group) {
	if candidateGenerateHook != nil {
		candidateGenerateHook()
	}
	sides := b.candidateSides(p, groups)
	g := &candGen{b: b, sides: sides, pairs: map[candPairKey]*candItem{}}
	g.swaps()
	g.initials()
	g.translits()
	g.cap()
	g.precedence()
	for _, it := range g.ordered() {
		if it.reason == "" {
			it.reason = b.pairReason(it)
		}
	}
	items := g.ordered()
	items = append(items, b.bucketItems(sides)...)
	r := &b.rep.Candidates
	for _, it := range items {
		byScript := r.ByKind[it.kind]
		if byScript == nil {
			byScript = map[string]*CandidateCounts{}
			r.ByKind[it.kind] = byScript
		}
		counts := byScript[it.script]
		if counts == nil {
			counts = &CandidateCounts{Excluded: map[string]int{}}
			byScript[it.script] = counts
		}
		if it.reason == "" {
			counts.Candidates++
			r.Candidates++
		} else {
			counts.Excluded[it.reason]++
			r.Excluded++
		}
	}
	p.cand = &candidates{b: b, items: items}
	p.cardBooks = b.cardBookIDs(items)
	r.CardBooks = len(p.cardBooks)
}

// candidateSides are the person groups of the pass as sides, by root; a
// side whose handling panicked is left out and counted.
func (b *builder) candidateSides(p *Plan, groups []*group) map[int]*candSide {
	keysOf := map[int][]int{}
	for _, kg := range b.keys {
		root := b.find(kg.idx)
		keysOf[root] = append(keysOf[root], kg.idx)
	}
	manualFP := b.manualFingerprints()
	anchored := b.anchored()
	out := map[int]*candSide{}
	for _, g := range groups {
		if b.keys[g.root].key.kind != KindPerson {
			continue
		}
		var s *candSide
		if failed := isolate(func() { s = b.candidateSide(p, g, keysOf[g.root], manualFP, anchored) }); failed {
			b.rep.Candidates.ItemsFailed++
			continue
		}
		out[g.root] = s
	}
	return out
}

func (b *builder) candidateSide(p *Plan, g *group, keys []int, manualFP map[FPKey]bool,
	anchored map[uuid.UUID]bool) *candSide {
	s := &candSide{root: g.root, entity: g.id, keys: keys, manual: anchored[g.id]}
	scripts := map[string]bool{}
	s.noName = true
	for _, k := range keys {
		kg := b.keys[k]
		scripts[kg.key.script] = true
		s.tagged = s.tagged || kg.key.tagged != ""
		s.noName = s.noName && kg.key.family == "" && kg.key.given == ""
		for _, fp := range kg.fps {
			s.members = append(s.members, fp)
			s.manual = s.manual || manualFP[fp]
		}
		for _, ci := range kg.credits {
			c := &b.in.Credits[ci]
			if candidateSideHook != nil {
				candidateSideHook(c)
			}
			s.names = append(s.names, c.DisplayName)
			s.variants = append(s.variants, c.Nickname)
		}
	}
	s.script = scriptMixed
	if len(scripts) == 1 {
		for sc := range scripts {
			s.script = sc
		}
	}
	slices.SortFunc(s.members, compareFPKeys)
	s.books = sortedKeys(g.books)
	e := p.entities[g.id]
	s.display = e.DisplayName
	words := strings.Fields(e.SearchKey)
	slices.Sort(words)
	s.tokens = strings.Join(words, " ")
	root := &b.keys[g.root].key
	s.bucket = b.isBucketKey(g.root) && b.bucketKey(root)
	return s
}

// manualFingerprints are the fingerprints a manual row fixes, itself or a
// credit of it.
func (b *builder) manualFingerprints() map[FPKey]bool {
	out := map[FPKey]bool{}
	for fp, row := range b.old.Fingerprints {
		if row.Method == MethodManual {
			out[fp] = true
		}
	}
	for ci := range b.fixed {
		out[fpKeyOf(&b.in.Credits[ci])] = true
	}
	return out
}

// candPairKey names an unordered pair of sides in one kind.
type candPairKey struct {
	kind   string
	lo, hi int
}

// candGen generates the entity pairs of a pass.
type candGen struct {
	b     *builder
	sides map[int]*candSide
	pairs map[candPairKey]*candItem
}

// add records the pair of sides x and y of kind; loose marks a translit
// that only the loose rule matches, which a tight match replaces.
func (g *candGen) add(kind, script string, x, y *candSide, loose bool) {
	if x == y {
		return
	}
	key := candPairKey{kind: kind, lo: min(x.root, y.root), hi: max(x.root, y.root)}
	if old, ok := g.pairs[key]; ok {
		if old.reason == ExcludedLooseScriptOnly && !loose {
			old.reason = ""
		}
		return
	}
	it := &candItem{kind: kind, script: script, a: x, b: y, credit: -1}
	if loose {
		it.reason = ExcludedLooseScriptOnly
	}
	g.pairs[key] = it
}

// sortedSides are the sides in root order.
func (g *candGen) sortedSides() []*candSide {
	out := make([]*candSide, 0, len(g.sides))
	for _, root := range sortedKeys(g.sides) {
		out = append(out, g.sides[root])
	}
	return out
}

// swaps pairs sides whose display names are the same words: another order,
// or the same display key on two components.
func (g *candGen) swaps() {
	byTokens := map[string][]*candSide{}
	for _, s := range g.sortedSides() {
		if s.tokens != "" {
			byTokens[s.tokens] = append(byTokens[s.tokens], s)
		}
	}
	for _, tokens := range sortedKeys(byTokens) {
		group := byTokens[tokens]
		for i, x := range group {
			for _, y := range group[i+1:] {
				script := x.script
				if y.script != x.script {
					script = scriptMixed
				}
				g.add(CandidateSwap, script, x, y, false)
			}
		}
	}
}

// keyRef is one key of a side.
type keyRef struct {
	side *candSide
	key  *nameKey
}

// initials pairs a key of initials with a key of full words of the same
// script, family and suffix whose words its initials stand for, in order.
func (g *candGen) initials() {
	var short []keyRef
	full := map[string][]keyRef{}
	for _, s := range g.sortedSides() {
		for _, k := range s.keys {
			key := &g.b.keys[k].key
			if key.kind != KindPerson || key.family == "" {
				continue
			}
			base := key.script + "|" + key.suffix + "|" + key.family
			switch {
			case key.initials():
				short = append(short, keyRef{side: s, key: key})
			case key.given != "":
				full[base] = append(full[base], keyRef{side: s, key: key})
			}
		}
	}
	for _, x := range short {
		wx := strings.Fields(x.key.given + " " + x.key.additional)
		for _, y := range full[x.key.script+"|"+x.key.suffix+"|"+x.key.family] {
			if initialsFit(wx, strings.Fields(y.key.given+" "+y.key.additional)) {
				g.add(CandidateInitials, x.key.script, x.side, y.side, false)
			}
		}
	}
}

// initialsFit reports words of initials that stand for the full words in
// the same places: each one letter equal to the first of its word, or the
// word itself.
func initialsFit(initials, words []string) bool {
	if len(initials) == 0 || len(initials) > len(words) {
		return false
	}
	for i, w := range initials {
		r, _ := utf8.DecodeRuneInString(w)
		f, _ := utf8.DecodeRuneInString(words[i])
		if r != f || (utf8.RuneCountInString(w) > 1 && w != words[i]) {
			return false
		}
	}
	return true
}

// translits pairs a Cyrillic key with a Latin one of the same family
// skeleton and the same first consonants of the given name's skeleton (the
// tight rule); a pair only the given name's first letter joins is counted
// loose_script_only.
func (g *candGen) translits() {
	type skel struct {
		ref           keyRef
		family, given string
		initial       rune
	}
	var cyrl []skel
	latn := map[string][]skel{}
	for _, s := range g.sortedSides() {
		for _, k := range s.keys {
			key := &g.b.keys[k].key
			if key.kind != KindPerson || key.family == "" || key.given == "" ||
				(key.script != string(authornorm.ScriptCyrillic) && key.script != string(authornorm.ScriptLatin)) {
				continue
			}
			sk := skel{ref: keyRef{side: s, key: key}, family: familySkeleton(key.family),
				given: givenSkeleton(key.given), initial: givenInitial(key.given)}
			if utf8.RuneCountInString(sk.family) < minTranslitSkeleton {
				continue
			}
			if key.script == string(authornorm.ScriptCyrillic) {
				cyrl = append(cyrl, sk)
			} else {
				latn[sk.family] = append(latn[sk.family], sk)
			}
		}
	}
	for _, c := range cyrl {
		for _, l := range latn[c.family] {
			tight := c.given != "" && prefixRunes(c.given, translitGivenPrefix) == prefixRunes(l.given, translitGivenPrefix)
			if tight || c.initial == l.initial {
				g.add(CandidateTranslit, ScriptCyrlLatn, c.ref.side, l.ref.side, !tight)
			}
		}
	}
}

// cap keeps out every pair of a side with more partners of the kind than
// §2.1 allows; loose translits neither count nor are capped.
func (g *candGen) cap() {
	partners := map[string]map[int]int{}
	for key, it := range g.pairs {
		if it.reason == ExcludedLooseScriptOnly {
			continue
		}
		if partners[key.kind] == nil {
			partners[key.kind] = map[int]int{}
		}
		partners[key.kind][key.lo]++
		partners[key.kind][key.hi]++
	}
	for key, it := range g.pairs {
		if it.reason == "" && (partners[key.kind][key.lo] > maxCandidatePartners ||
			partners[key.kind][key.hi] > maxCandidatePartners) {
			it.reason = ExcludedPartnerCap
		}
	}
}

// precedence keeps a pair of two kinds in the higher one only.
func (g *candGen) precedence() {
	for key, it := range g.pairs {
		if it.reason != "" {
			continue
		}
		for _, kind := range candidateKinds {
			if kind == key.kind {
				break
			}
			if higher, ok := g.pairs[candPairKey{kind: kind, lo: key.lo, hi: key.hi}]; ok && higher.reason != ExcludedPartnerCap &&
				higher.reason != ExcludedLooseScriptOnly && higher.reason != ExcludedPrecedence {
				it.reason = ExcludedPrecedence
				break
			}
		}
	}
}

// ordered are the pairs in kind and root order.
func (g *candGen) ordered() []*candItem {
	keys := make([]candPairKey, 0, len(g.pairs))
	for k := range g.pairs {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(x, y candPairKey) int {
		if c := strings.Compare(x.kind, y.kind); c != 0 {
			return c
		}
		if x.lo != y.lo {
			return x.lo - y.lo
		}
		return x.hi - y.hi
	})
	out := make([]*candItem, len(keys))
	for i, k := range keys {
		out[i] = g.pairs[k]
	}
	return out
}

// pairReason is why a pair that passed the cap and the precedence is no
// candidate, empty when it is one: the first reason of §2.1 that holds.
func (b *builder) pairReason(it *candItem) string {
	if reason := b.sidesReason(it.a, it.b); reason != "" {
		return reason
	}
	x, y := it.a, it.b
	switch {
	case it.script == scriptMixed || !candidateScript(it.kind, x, y):
		return ExcludedMixedScript
	case x.noName || y.noName:
		return ExcludedNoFamilyOrGiven
	case it.kind == CandidateInitials && b.initialsSideBooks(it) > maxInitialsSideBooks:
		return ExcludedInitialsSideGT10
	}
	return ""
}

// sidesReason is why two sides are never one question, whatever the kind:
// a bucket, a shared book, a namesake tag, a manual row, a pen name.
func (b *builder) sidesReason(x, y *candSide) string {
	switch {
	case len(b.vetoOf) > 0 && b.vetoSidesOfKeys(x.keys).apart(b.vetoSidesOfKeys(y.keys)):
		return ExcludedRevokedPair
	case x.bucket || y.bucket:
		return ExcludedBucketSide
	case sharesBooks(x.books, y.books):
		return ExcludedCoCredited
	case x.tagged || y.tagged:
		return ExcludedNamesakeTag
	case x.manual || y.manual:
		return ExcludedManualSide
	case b.pseudonymOf(x, y) || b.pseudonymOf(y, x):
		return ExcludedPseudonymKey
	}
	return ""
}

// candidateScript reports sides of the scripts a kind asks: one script of
// the two, Cyrillic and Latin for a translit.
func candidateScript(kind string, x, y *candSide) bool {
	cyrl, latn := string(authornorm.ScriptCyrillic), string(authornorm.ScriptLatin)
	if kind == CandidateTranslit {
		return (x.script == cyrl && y.script == latn) || (x.script == latn && y.script == cyrl)
	}
	return x.script == y.script && (x.script == cyrl || x.script == latn)
}

// initialsSideBooks are the books of a pair's side of initials.
func (b *builder) initialsSideBooks(it *candItem) int {
	n := 0
	for _, s := range []*candSide{it.a, it.b} {
		for _, k := range s.keys {
			if b.keys[k].key.initials() {
				n = max(n, len(s.books))
			}
		}
	}
	return n
}

// pseudonymOf reports y's display key a pseudonym (P742) of x's item.
func (b *builder) pseudonymOf(x, y *candSide) bool {
	ref := b.in.Reference
	if ref == nil {
		return false
	}
	qid := b.mainLabelOf(ref.NameKey[x.display])
	if qid == "" {
		return false
	}
	return slices.Contains(ref.Items[qid].PseudonymKeys, ref.NameKey[y.display])
}

// sharesBooks reports two sorted book lists with a book in common.
func sharesBooks(xs, ys []int64) bool {
	for i, j := 0, 0; i < len(xs) && j < len(ys); {
		switch {
		case xs[i] == ys[j]:
			return true
		case xs[i] < ys[j]:
			i++
		default:
			j++
		}
	}
	return false
}

// bucketItems are the book items: every book left in a short name's bucket
// after D3 against each entity holding a full name of its family and given
// name (§2.1), at most maxCandidatePartners of them.
func (b *builder) bucketItems(sides map[int]*candSide) []*candItem {
	byBase := b.fullNamesByBase()
	var out []*candItem
	for _, root := range sortedKeys(sides) {
		s := sides[root]
		if !s.bucket {
			continue
		}
		var fulls []*candSide
		seen := map[int]bool{}
		for _, f := range byBase[b.keys[root].key.base()] {
			r := b.find(f)
			if full, ok := sides[r]; ok && !seen[r] && r != root {
				seen[r] = true
				fulls = append(fulls, full)
			}
		}
		slices.SortFunc(fulls, func(x, y *candSide) int { return x.root - y.root })
		for _, ci := range b.keys[root].credits {
			if _, moved := b.movedTo[ci]; moved {
				continue
			}
			book := b.in.Credits[ci].BookID
			for _, full := range fulls {
				it := &candItem{kind: CandidateBucket, script: s.script, a: s, b: full, credit: ci}
				switch {
				case b.creditVetoedFrom(ci, full.keys):
					it.reason = ExcludedRevokedPair
				case len(fulls) > maxCandidatePartners:
					it.reason = ExcludedPartnerCap
				case slices.Contains(full.books, book):
					it.reason = ExcludedCoCredited
				case full.manual:
					it.reason = ExcludedManualSide
				case !candidateScript(CandidateBucket, s, full):
					it.reason = ExcludedMixedScript
				}
				out = append(out, it)
			}
		}
	}
	return out
}

// sample is how many rows of each (kind, script, reason) the sample keeps.
const candidateSampleRows = 50

// sampleBooks are a side's card books: the oldest mergeCardSample.
func (s *candSide) sampleBooks() []int64 { return s.books[:min(mergeCardSample, len(s.books))] }

// cardBookIDs are the books the candidates' cards show.
func (b *builder) cardBookIDs(items []*candItem) []int64 {
	set := map[int64]bool{}
	for _, it := range items {
		if it.reason != "" {
			continue
		}
		if it.credit >= 0 {
			set[b.in.Credits[it.credit].BookID] = true
		} else {
			for _, id := range it.a.sampleBooks() {
				set[id] = true
			}
		}
		for _, id := range it.b.sampleBooks() {
			set[id] = true
		}
	}
	return sortedKeys(set)
}

// MergeCandidates are the pass's sealed candidates, in digest order.
func (p *Plan) MergeCandidates() []MergeCandidate { return p.candidates }

// MergeCandidateSample is the pass's explainable sample, by kind, script,
// reason and member hash.
func (p *Plan) MergeCandidateSample() []MergeCandidateSample { return p.sample }

// CardBookIDs are the sample books whose fields the cards need, sorted.
func (p *Plan) CardBookIDs() []int64 { return p.cardBooks }

// MergeInput is the exact model input of candidate i of MergeCandidates.
func (p *Plan) MergeInput(i int) []byte { return p.inputs[i] }

// sealed is one item with its members and, for a candidate, its input.
type sealedItem struct {
	it        *candItem
	a, b      []FPKey
	credit    *CreditKey
	sideA     *candSide
	sideB     *candSide
	members   [32]byte
	input     []byte
	inputHash [32]byte
	digest    [32]byte
}

// SealMergeCandidates renders every candidate's cards from the loaded
// sample book fields (a book without fields shows its title alone) and
// computes the members and input hashes, the digests and the sample.
func (p *Plan) SealMergeCandidates(books map[int64]CardBookFields) error {
	if p.cand == nil {
		return ErrNoCandidates
	}
	if failed := isolate(func() { p.seal(books) }); failed {
		p.candidates, p.inputs, p.sample, p.sealedAll, p.cardFailed = nil, nil, nil, nil, nil
		return errSealFailed
	}
	p.sealed = true
	return nil
}

// errSealFailed is a seal that panicked: no candidate is kept.
var errSealFailed = errors.New("sealing the merge candidates failed")

// seal is the seal itself.
func (p *Plan) seal(books map[int64]CardBookFields) {
	if candidateSealHook != nil {
		candidateSealHook()
	}
	b := p.cand.b
	r := &cardRenderer{b: b, p: p, fields: books, byBook: b.creditsOnBooks(p.cardBooks), cards: map[int]MergeCard{}}
	var all []*sealedItem
	p.cardFailed = map[[32]byte]bool{}
	for _, it := range p.cand.items {
		si := sealItem(b, it)
		if it.reason == "" {
			if failed := isolate(func() {
				a, b := r.card(si.sideA, si), r.card(si.sideB, nil)
				si.input = MergeInputJSON(&a, &b)
			}); failed {
				// One card that panicked leaves its item out, counted (§1.5);
				// a decision on it lapses card_failed.
				p.dropFailedCard(si)
				continue
			}
			si.inputHash = sha256.Sum256(si.input)
			si.digest = candidateDigest(it.kind, it.script, si.members, si.inputHash)
		}
		all = append(all, si)
	}
	p.candidates, p.inputs = nil, nil
	var cands []*sealedItem
	for _, si := range all {
		if si.it.reason == "" {
			cands = append(cands, si)
		}
	}
	slices.SortFunc(cands, func(x, y *sealedItem) int { return bytes.Compare(x.digest[:], y.digest[:]) })
	for _, si := range cands {
		c := MergeCandidate{Kind: si.it.kind, Script: si.it.script, ItemKind: ItemEntity, SideA: si.sideA.entity,
			SideB: si.sideB.entity, MembersA: si.a, MembersB: si.b, MembersSHA256: si.members,
			InputSHA256: si.inputHash, Digest: si.digest}
		if si.credit != nil {
			c.ItemKind, c.CreditID, c.CreditOrdinal = ItemBook, si.credit.CreditID, si.credit.Ordinal
		}
		p.candidates = append(p.candidates, c)
		p.inputs = append(p.inputs, si.input)
	}
	p.sample = b.candidateSample(all)
	p.sealedAll = all
}

// dropFailedCard leaves out a candidate whose card panicked: it is counted
// failed instead, in the pass's report and in the builder's, which a
// decision step reports from.
func (p *Plan) dropFailedCard(si *sealedItem) {
	p.cardFailed[si.members] = true
	for _, r := range []*CandidateReport{&p.Report.Candidates, &p.cand.b.rep.Candidates} {
		r.ItemsFailed++
		r.Candidates--
	}
	p.Report.Candidates.ByKind[si.it.kind][si.it.script].Candidates--
}

// sealItem puts an item's sides in canonical order with their members.
func sealItem(b *builder, it *candItem) *sealedItem {
	si := &sealedItem{it: it, sideA: it.a, sideB: it.b, a: it.a.members, b: it.b.members}
	if it.credit >= 0 {
		c := &b.in.Credits[it.credit]
		si.a = []FPKey{fpKeyOf(c)}
		si.credit = &CreditKey{CreditID: c.ID}
	} else if bytes.Compare(memberBytes(si.a), memberBytes(si.b)) > 0 {
		si.sideA, si.sideB, si.a, si.b = si.sideB, si.sideA, si.b, si.a
	}
	si.members = MembersSHA256(si.a, si.b, si.credit)
	return si
}

// memberBytes is the canonical encoding of one side's members.
func memberBytes(members []FPKey) []byte {
	var buf bytes.Buffer
	for _, m := range members {
		fmt.Fprintf(&buf, "%s\x1f%s\x1f%d\n", m.Fingerprint, m.ExtractorVersion, m.Ordinal)
	}
	return buf.Bytes()
}

// MembersSHA256 is the members hash of a candidate: both sides' member keys
// in the candidate's side order and, for a book item, its credit key.
func MembersSHA256(a, b []FPKey, credit *CreditKey) [32]byte {
	h := sha256.New()
	h.Write([]byte(mergeCandidateVersion + "\nside a\n"))
	h.Write(memberBytes(a))
	h.Write([]byte("side b\n"))
	h.Write(memberBytes(b))
	if credit != nil {
		fmt.Fprintf(h, "credit\x1f%d\x1f%d\n", credit.CreditID, credit.Ordinal)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// candidateDigest names a candidate (§4.2).
func candidateDigest(kind, script string, members, input [32]byte) [32]byte {
	h := sha256.New()
	h.Write([]byte(mergeCandidateVersion + "|" + kind + "|" + script + "|"))
	h.Write(members[:])
	h.Write([]byte("|"))
	h.Write(input[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// cardRenderer renders the cards of a seal, each entity side's once.
type cardRenderer struct {
	b      *builder
	p      *Plan
	fields map[int64]CardBookFields
	byBook map[int64][]int
	cards  map[int]MergeCard
}

// card is a side's card; for a book item's bucket side (si set) the card
// of its one book.
func (r *cardRenderer) card(s *candSide, si *sealedItem) MergeCard {
	if si != nil && si.credit != nil {
		c := &r.b.in.Credits[si.it.credit]
		return MergeCardOf([]string{c.DisplayName}, []string{c.Nickname}, 1,
			[]MergeCardBook{r.book(c.BookID, s.entity)})
	}
	if card, ok := r.cards[s.root]; ok {
		return card
	}
	var sample []MergeCardBook
	for _, id := range s.sampleBooks() {
		sample = append(sample, r.book(id, s.entity))
	}
	card := MergeCardOf(s.names, s.variants, len(s.books), sample)
	r.cards[s.root] = card
	return card
}

// book is one sample book of the side of entity: its co-authors are the
// selected names of the book's credits of other entities.
func (r *cardRenderer) book(id int64, entity uuid.UUID) MergeCardBook {
	if candidateCardHook != nil {
		candidateCardHook(id)
	}
	var coauthors []string
	for _, ci := range r.byBook[id] {
		c := &r.b.in.Credits[ci]
		if r.p.entityOf[c.ID] != entity {
			coauthors = append(coauthors, c.DisplayName)
		}
	}
	f := r.fields[id]
	return MergeCardBookOf(r.b.in.Books[id].Title, &f, coauthors)
}

// creditsOnBooks are the credits on each of the books.
func (b *builder) creditsOnBooks(books []int64) map[int64][]int {
	want := map[int64]bool{}
	for _, id := range books {
		want[id] = true
	}
	out := map[int64][]int{}
	for i := range b.in.Credits {
		if id := b.in.Credits[i].BookID; want[id] {
			out[id] = append(out[id], i)
		}
	}
	return out
}

// candidateSample keeps, per (kind, script, reason), the rows of the lowest
// member hash.
func (b *builder) candidateSample(all []*sealedItem) []MergeCandidateSample {
	type group struct{ kind, script, reason string }
	byGroup := map[group][]*sealedItem{}
	for _, si := range all {
		reason := si.it.reason
		if reason == "" {
			reason = SampleCandidate
		}
		g := group{si.it.kind, si.it.script, reason}
		byGroup[g] = append(byGroup[g], si)
	}
	groups := make([]group, 0, len(byGroup))
	for g := range byGroup {
		groups = append(groups, g)
	}
	slices.SortFunc(groups, func(x, y group) int {
		return strings.Compare(x.kind+"|"+x.script+"|"+x.reason, y.kind+"|"+y.script+"|"+y.reason)
	})
	var out []MergeCandidateSample
	for _, g := range groups {
		rows := byGroup[g]
		slices.SortFunc(rows, func(x, y *sealedItem) int { return bytes.Compare(x.members[:], y.members[:]) })
		for _, si := range rows[:min(candidateSampleRows, len(rows))] {
			row := MergeCandidateSample{Kind: g.kind, Script: g.script, Reason: g.reason, EntityA: si.sideA.entity,
				EntityB: si.sideB.entity, KeysA: b.sideKeys(si.sideA), KeysB: b.sideKeys(si.sideB),
				MembersSHA256: si.members, Digest: si.digest}
			if si.credit != nil {
				row.CreditID = si.credit.CreditID
			}
			out = append(out, row)
		}
	}
	return out
}

// sideKeys are a side's Go keys, sorted.
func (b *builder) sideKeys(s *candSide) []string {
	out := make([]string, len(s.keys))
	for i, k := range s.keys {
		out[i] = b.keys[k].key.String()
	}
	slices.Sort(out)
	return out
}

// The ru→latin folding of the translit skeletons (plan A.4): the same
// table for both scripts, so a Latin name passes through its Latin part.
var (
	familySteps   = replaceSteps("щ>shch ш>sh ч>ch ж>zh ю>yu я>ya х>kh ц>ts ph>f th>t ck>k x>ks kh>h")
	familyLetters = letterMap("абвгдезийклмнопрстуфыэьъzcqwj", "abvgdesiiklmnoprstufye--skkvy")
	givenSteps    = replaceSteps("щ>sh ш>sh ч>ch ph>f th>t")
	givenLetters  = letterMap("абвгдезийклмнопрстуфхцыэюяжzcqwj", "abvgdesiiklmnoprstufhcyeyyzskkvy")
	initialLetter = letterMap("абвгдезийклмнопрстуфхцчшщыэюяж", "abvgdeziiklmnoprstufhccssyeyyz")
)

// replaceSteps reads "from>to" steps, applied one after another.
func replaceSteps(spec string) [][2]string {
	var out [][2]string
	for _, step := range strings.Fields(spec) {
		from, to, _ := strings.Cut(step, ">")
		out = append(out, [2]string{from, to})
	}
	return out
}

// letterMap maps each rune of from to the rune of to in its place.
func letterMap(from, to string) map[rune]rune {
	f, t := []rune(from), []rune(to)
	out := make(map[rune]rune, len(f))
	for i, r := range f {
		out[r] = t[i]
	}
	return out
}

// skeleton folds a name part: the replacements one after another, then the
// letters at once, then hyphens, vowels and repeated letters out.
func skeleton(part string, steps [][2]string, letters map[rune]rune) string {
	for _, st := range steps {
		part = strings.ReplaceAll(part, st[0], st[1])
	}
	var out []rune
	for _, r := range part {
		if m, ok := letters[r]; ok {
			r = m
		}
		switch r {
		case '-', 'a', 'e', 'i', 'o', 'u', 'y':
			continue
		}
		if len(out) > 0 && out[len(out)-1] == r {
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

// familySkeleton is the family name's skeleton; givenSkeleton the given
// name's; givenInitial its first letter folded.
func familySkeleton(family string) string { return skeleton(family, familySteps, familyLetters) }

func givenSkeleton(given string) string { return skeleton(given, givenSteps, givenLetters) }

func givenInitial(given string) rune {
	r, _ := utf8.DecodeRuneInString(given)
	if m, ok := initialLetter[r]; ok {
		return m
	}
	return r
}

// prefixRunes is the first n runes of s.
func prefixRunes(s string, n int) string { return cutRunes(s, n) }
