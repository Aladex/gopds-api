package authorentity_test

import (
	"fmt"
	"math/rand"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode"

	"gopds-api/internal/authorentity"

	"github.com/google/uuid"
)

// A property test of the Wikidata rule: random writers in two scripts with
// spellings, short and full names, initials, tags, nicknames, aliases,
// labels in other languages, pseudonyms and keys shared between items, and
// a random catalog of them with co-authored books and manual rows. An oracle
// of its own — no helper of the package — recomputes the rule from the
// generated data, and the pass must merge exactly what it says:
//
//	two entities of the pass before the Wikidata merge end in one entity iff
//	the oracle's merge sets join them, a set being the entities whose display
//	names are each a main label of one item alone (1, 2), the item recording
//	no pseudonym (3), neither name with an initial (4), no short name beside
//	its full one (5), no manual row (6), no namesake tag, and, when the
//	item's entities go by two keys in one script, only those of one key; a
//	set with two of them on one book merges nothing;
//
// and every entity has the item its display name names by 1–3, unless that
// name is a short name's beside a full one. The pass before the Wikidata merge
// is the same input built without a reference. The evidence moves no book of
// its own: the books D3 moves are the same with the reference and without it,
// each into the entity its full name ends in.

const propertyCases = 3000

// persona is a writer of the generator: a name in Latin and in Cyrillic,
// with a spelling variant of each, a middle name and an initial.
type persona struct {
	latin, cyr [2]pname
}

// pname is one name of the generator: its parts as the extractor gives
// them and the display name.
type pname struct {
	given, additional, family, script, display string
}

var (
	propGiven = [][4]string{{"Anna", "Ann", "Анна", "Анне"}, {"Mary", "Marie", "Мария", "Мэри"},
		{"Stephen", "Steven", "Стивен", "Стефан"}, {"Ivan", "Iwan", "Иван", "Ивaн"}}
	propFamily = [][4]string{{"Smith", "Smyth", "Смит", "Смитт"}, {"King", "Kingg", "Кинг", "Кингг"},
		{"Jones", "Johns", "Джонс", "Джоунс"}, {"O'Dell", "ODell", "О'Делл", "Оделл"}}
	propMiddle = [2]string{"Lee", "Ли"}
)

func latinName(given, additional, family string) pname {
	return pname{given: given, additional: additional, family: family, script: "Latn",
		display: strings.Join(strings.Fields(given+" "+additional+" "+family), " ")}
}

func cyrName(given, additional, family string) pname {
	n := latinName(given, additional, family)
	n.script = "Cyrl"
	return n
}

// oracleKey is the generator's key of a name: lower case, ё as е, every run
// of other than letters and digits one space.
func oracleKey(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(strings.ReplaceAll(s, "ё", "е")) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		} else {
			space = true
		}
	}
	return b.String()
}

// itemName is one name of an item as the generator recorded it.
type itemName struct {
	qid, kind, key string
	main           bool
}

// propFP is a fingerprint of the catalog.
type propFP struct {
	fp                 string
	n                  pname
	tag                int
	manual, collective bool
	books              []int64
}

type propCase struct {
	in      authorentity.Input
	names   []itemName
	pens    map[string]bool // items recording a pseudonym
	fps     []*propFP
	byFP    map[string]*propFP
	fixedID map[string]uuid.UUID
}

func (pc *propCase) addName(qid, kind, display string, main bool) {
	pc.names = append(pc.names, itemName{qid: qid, kind: kind, key: oracleKey(display), main: main})
	if kind == authorentity.NamePseudonym {
		pc.pens[qid] = true
	}
}

// reference is the input's Wikidata side, made of the recorded names.
func (pc *propCase) reference() *authorentity.Reference {
	ref := &authorentity.Reference{Extract: "wd-0000000000000000", NameKey: map[string]string{},
		KeyItems: map[string][]authorentity.ItemName{}, Items: map[string]*authorentity.Item{}}
	for _, n := range pc.names {
		ref.KeyItems[n.key] = append(ref.KeyItems[n.key], authorentity.ItemName{QID: n.qid, Kind: n.kind})
		it := ref.Items[n.qid]
		if it == nil {
			it = &authorentity.Item{Label: n.key}
			ref.Items[n.qid] = it
		}
		switch n.kind {
		case authorentity.NamePseudonym:
			it.PseudonymKeys = append(it.PseudonymKeys, n.key)
		case authorentity.NameLabel:
			it.LabelKeys = append(it.LabelKeys, n.key)
			if n.main {
				it.MainLabelKeys = append(it.MainLabelKeys, n.key)
			}
		}
	}
	for i := range pc.in.Credits {
		d := pc.in.Credits[i].DisplayName
		ref.NameKey[d] = oracleKey(d)
	}
	return ref
}

func randomPersona(r *rand.Rand) persona {
	g, f := propGiven[r.Intn(len(propGiven))], propFamily[r.Intn(len(propFamily))]
	return persona{
		latin: [2]pname{latinName(g[0], "", f[0]), latinName(g[1], "", f[1])},
		cyr:   [2]pname{cyrName(g[2], "", f[2]), cyrName(g[3], "", f[3])},
	}
}

// variant is a name of the persona: a script, a spelling, sometimes a middle
// name, an initial or an initial glued to the surname.
func variant(r *rand.Rand, p *persona) pname {
	n := p.latin[0]
	middle := propMiddle[0]
	if r.Intn(2) == 0 {
		n, middle = p.cyr[0], propMiddle[1]
	}
	if r.Intn(4) == 0 {
		if n.script == "Latn" {
			n = p.latin[1]
		} else {
			n = p.cyr[1]
		}
	}
	first := string([]rune(n.given)[:1])
	switch r.Intn(18) {
	case 0, 1:
		n.additional = middle
		n.display = strings.Join([]string{n.given, middle, n.family}, " ")
	case 2:
		n.given = string([]rune(n.given)[:1]) + "."
		n.display = n.given + " " + n.family
	case 3:
		n.given = string([]rune(n.given)[:1]) + "."
		n.display = n.given + n.family
	case 4:
		n.given = first
		n.display = n.given + " " + n.family
	case 5:
		n.given = "(" + first + ")"
		n.display = n.given + " " + n.family
	case 6:
		n.given = first
		n.display = first + ", " + n.family
	case 7:
		n.given = first + "．"
		n.display = n.given + " " + n.family
	case 8:
		// The display name written out, an initial in the parts.
		n.given = first + "."
	case 9:
		n.additional = string([]rune(middle)[:1])
		n.display = strings.Join([]string{n.given, middle, n.family}, " ")
	case 10:
		// An initial in the display name alone, glued by a fullwidth dot.
		n.display = first + "．" + n.family
	case 11:
		// An initial in the display name alone, glued by brackets.
		n.display = "(" + first + ")" + n.family
	}
	return n
}

var nameKinds = []string{authorentity.NameLabel, authorentity.NameAlias, authorentity.NameNative,
	authorentity.NamePseudonym}

func generate(seed int64) *propCase {
	r := rand.New(rand.NewSource(seed))
	pc := &propCase{pens: map[string]bool{}, byFP: map[string]*propFP{}, fixedID: map[string]uuid.UUID{}}
	var personas []persona
	for i := range 1 + r.Intn(3) {
		qid := fmt.Sprintf("Q%d", i+1)
		p := randomPersona(r)
		personas = append(personas, p)
		// Main labels: mostly one per script, sometimes a second spelling.
		pc.addName(qid, authorentity.NameLabel, p.cyr[0].display, true)
		if r.Intn(5) > 0 {
			pc.addName(qid, authorentity.NameLabel, p.latin[0].display, true)
		}
		if r.Intn(6) == 0 {
			pc.addName(qid, authorentity.NameLabel, p.cyr[1].display, true)
		}
		if r.Intn(3) == 0 {
			// Any variant as a main label: initials, a middle name, a spelling.
			pc.addName(qid, authorentity.NameLabel, variant(r, &p).display, true)
		}
		for range r.Intn(3) {
			kind := nameKinds[r.Intn(len(nameKinds))]
			pc.addName(qid, kind, variant(r, &p).display, false)
		}
		if r.Intn(4) == 0 {
			pc.addName(qid, authorentity.NamePseudonym, randomPersona(r).latin[0].display, false)
		}
	}
	// Another item carrying a name of a writer above, in any way.
	if r.Intn(3) == 0 {
		p := personas[r.Intn(len(personas))]
		pc.addName("Q99", nameKinds[r.Intn(len(nameKinds))], variant(r, &p).display, r.Intn(2) == 0)
	}

	pc.in = authorentity.Input{Books: map[int64]authorentity.Book{}}
	next := uint64(0)
	pc.in.NewID = func() uuid.UUID {
		next++
		var u uuid.UUID
		for i := range 8 {
			u[15-i] = byte(next >> (8 * i))
		}
		return u
	}
	pc.in.Old = authorentity.NewState()
	book, credit := int64(0), int64(0)
	for i := range 3 + r.Intn(6) {
		f := &propFP{fp: fmt.Sprintf("fp%d", i)}
		p := personas[r.Intn(len(personas))]
		if r.Intn(6) == 0 {
			p = randomPersona(r)
		}
		f.n = variant(r, &p)
		if len(pc.fps) > 0 && r.Intn(8) == 0 {
			// Another reading of a name already in the catalog: the same
			// display name, the parts the other way round — another key.
			o := pc.fps[r.Intn(len(pc.fps))].n
			f.n = pname{given: o.family, family: o.given, additional: o.additional, script: o.script, display: o.display}
		}
		if r.Intn(10) == 0 {
			f.tag = 2
		}
		f.manual = r.Intn(15) == 0
		f.collective = r.Intn(12) == 0
		res := &authorentity.Result{ResultID: int64(i + 1), Kind: authorentity.KindPerson, Script: f.n.script,
			Family: f.n.family, Given: f.n.given, DisplayName: f.n.display, SortName: f.n.display}
		if f.n.additional != "" {
			res.Additional = []string{f.n.additional}
		}
		if f.collective {
			res = &authorentity.Result{ResultID: int64(i + 1), Kind: authorentity.KindCollective, Script: f.n.script,
				DisplayName: f.n.display, SortName: f.n.display}
		}
		if r.Intn(5) == 0 {
			res.Nickname = variant(r, &personas[r.Intn(len(personas))]).display
		}
		for range 1 + r.Intn(3) {
			credit++
			bk := book + 1
			if book > 0 && r.Intn(7) == 0 {
				// A book of another fingerprint: co-authors.
				bk = 1 + r.Int63n(book)
			} else {
				book++
				bk := authorentity.Book{Title: fmt.Sprintf("Book %d", book)}
				if r.Intn(3) == 0 {
					// A series D3 may read as evidence for a short name.
					bk.Series = []int64{1 + r.Int63n(2)}
				}
				pc.in.Books[book] = bk
			}
			if !slices.Contains(f.books, bk) {
				f.books = append(f.books, bk)
			}
			pc.in.Credits = append(pc.in.Credits, authorentity.Credit{ID: credit, BookID: bk, Fingerprint: f.fp,
				ExtractorVersion: "v1", Tag: f.tag, Result: res})
		}
		if f.manual {
			id := uuid.UUID{0xee, byte(i)}
			pc.fixedID[f.fp] = id
			pc.in.Old.Entities[id] = authorentity.EntityRow{ID: id, Kind: "person", DisplayName: f.n.display,
				SortName: f.n.display, SearchKey: oracleKey(f.n.display)}
			k := authorentity.FPKey{Fingerprint: f.fp, ExtractorVersion: "v1"}
			pc.in.Old.Fingerprints[k] = authorentity.AssignmentRow{Key: k, EntityID: id,
				Method: authorentity.MethodManual, Rule: "manual"}
		}
		pc.fps = append(pc.fps, f)
		pc.byFP[f.fp] = f
	}
	pc.in.Reference = pc.reference()
	return pc
}

// The oracle.

// namedBy is the item whose main label key is, carried by no other item in
// any way; empty otherwise.
func (pc *propCase) namedBy(key string) string {
	qid, main := "", false
	for _, n := range pc.names {
		if n.key != key {
			continue
		}
		if qid != "" && n.qid != qid {
			return ""
		}
		qid = n.qid
		main = main || n.kind == authorentity.NameLabel && n.main
	}
	if !main {
		return ""
	}
	return qid
}

// oracleInitial reports a one-letter word: every punctuation mark and
// space is a break but an apostrophe and a hyphen, which glue a name.
func oracleInitial(s string) bool {
	spaced := strings.Map(func(r rune) rune {
		if strings.ContainsRune("'’`ʼ‘-‐‑‒–—", r) {
			return r
		}
		if unicode.IsPunct(r) || unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, s)
	for _, w := range strings.Fields(spaced) {
		if len(oracleLetter.FindAllString(w, -1)) == 1 {
			return true
		}
	}
	return false
}

var oracleLetter = regexp.MustCompile(`\p{L}`)

// initialsOf reports an initial in a component's display name or in the
// given name or the additional part of any of its fingerprints.
func (pc *propCase) initialsOf(c *propComp) bool {
	if oracleInitial(c.n.display) {
		return true
	}
	for fp := range c.fps {
		n := pc.byFP[fp].n
		if oracleInitial(n.given) || oracleInitial(n.additional) {
			return true
		}
	}
	return false
}

// shortBesideFull reports a fingerprint without a middle name whose family
// and given name a person's fingerprint with one has — automatic or manual,
// tagged or not.
func (pc *propCase) shortBesideFull(f *propFP) bool {
	if f.n.additional != "" {
		return false
	}
	for _, o := range pc.fps {
		if !o.collective && o.n.additional != "" &&
			o.n.script == f.n.script && oracleKey(o.n.family) == oracleKey(f.n.family) &&
			oracleKey(o.n.given) == oracleKey(f.n.given) {
			return true
		}
	}
	return false
}

// oracleDisplay is the display name of credits: the fingerprint's of the
// most books, then of the most words, then the first.
func (pc *propCase) oracleDisplay(credits []authorentity.Credit) pname {
	books := map[int64]map[int64]bool{}
	for _, c := range credits {
		if books[c.ResultID] == nil {
			books[c.ResultID] = map[int64]bool{}
		}
		books[c.ResultID][c.BookID] = true
	}
	var best *propFP
	bestID := int64(0)
	for _, c := range credits {
		f := pc.byFP[c.Fingerprint]
		if best == nil || len(books[c.ResultID]) > len(books[bestID]) ||
			len(books[c.ResultID]) == len(books[bestID]) &&
				(len(strings.Fields(f.n.display)) > len(strings.Fields(best.n.display)) ||
					len(strings.Fields(f.n.display)) == len(strings.Fields(best.n.display)) && c.ResultID < bestID) {
			best, bestID = f, c.ResultID
		}
	}
	return best.n
}

// propComp is a component of the pass before the Wikidata merge.
type propComp struct {
	credits    []authorentity.Credit
	books      map[int64]bool
	fps        map[string]bool
	n          pname
	key        string
	collective bool
}

// expect is the oracle's partition of the automatic credits: the
// components of the pass without a reference, joined by the merge sets.
func (pc *propCase) expect(before map[uuid.UUID]*propComp) (merged map[*propComp]*propComp, stats map[string]int) {
	stats = map[string]int{}
	parent := map[*propComp]*propComp{}
	var find func(c *propComp) *propComp
	find = func(c *propComp) *propComp {
		if parent[c] == nil || parent[c] == c {
			return c
		}
		return find(parent[c])
	}
	byItem := map[string][]*propComp{}
	ids := make([]uuid.UUID, 0, len(before))
	for id := range before {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	for _, id := range ids {
		c := before[id]
		qid := pc.namedBy(c.key)
		if qid == "" {
			continue
		}
		if c.collective {
			stats["collective"]++
			continue
		}
		stats["named"]++
		switch {
		case pc.pens[qid]:
			stats["pseudonym"]++
		case pc.initialsOf(c):
			stats["initials"]++
		case slices.ContainsFunc(sortedFPs(c.fps), func(fp string) bool { return pc.shortBesideFull(pc.byFP[fp]) }):
			stats["short"]++
		case slices.ContainsFunc(sortedFPs(c.fps), func(fp string) bool { return pc.byFP[fp].tag != 0 }):
			stats["namesake"]++
		default:
			byItem[qid] = append(byItem[qid], c)
		}
	}
	for _, cs := range byItem {
		if len(cs) < 2 {
			continue
		}
		keys := map[string]map[string]bool{}
		for _, c := range cs {
			if keys[c.n.script] == nil {
				keys[c.n.script] = map[string]bool{}
			}
			keys[c.n.script][c.key] = true
		}
		sets := [][]*propComp{cs}
		if len(keys["Latn"]) > 1 || len(keys["Cyrl"]) > 1 {
			stats["script_conflict"]++
			byKey := map[string][]*propComp{}
			for _, c := range cs {
				byKey[c.key] = append(byKey[c.key], c)
			}
			sets = nil
			for _, s := range byKey {
				sets = append(sets, s)
			}
		}
	sets:
		for _, set := range sets {
			if len(set) < 2 {
				continue
			}
			for i, x := range set {
				for _, y := range set[i+1:] {
					for bk := range x.books {
						if y.books[bk] {
							stats["shared_book"]++
							continue sets
						}
					}
				}
			}
			for _, c := range set[1:] {
				parent[find(c)] = find(set[0])
				stats["merges"]++
				if c.key == set[0].key {
					stats["same_name"]++
				}
			}
		}
	}
	merged = map[*propComp]*propComp{}
	for _, c := range before {
		merged[c] = find(c)
	}
	return merged, stats
}

func sortedFPs(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// movedCredits are the credits D3 moved in a pass.
func movedCredits(p *authorentity.Plan) map[int64]bool {
	out := map[int64]bool{}
	for i := range p.Units {
		for _, c := range p.Units[i].Credits {
			out[c.Key.CreditID] = true
		}
	}
	return out
}

func (pc *propCase) check(t *testing.T, seed int64) (stats map[string]int, bad []string) {
	t.Helper()
	plain := pc.in
	plain.Reference = nil
	before := authorentity.Build(&plain)
	after := authorentity.Build(&pc.in)
	// The Wikidata evidence moves no book of its own: the books D3 moves are
	// the same with it and without it, each into the entity its full name
	// ends in.
	moved, movedAfter := movedCredits(before), movedCredits(after)
	for id := range movedAfter {
		if !moved[id] {
			bad = append(bad, fmt.Sprintf("seed %d: credit %d moved only with the reference", seed, id))
		}
	}
	for id := range moved {
		if !movedAfter[id] {
			bad = append(bad, fmt.Sprintf("seed %d: credit %d moved only without the reference", seed, id))
			continue
		}
		for _, c := range pc.in.Credits {
			if !moved[c.ID] && before.EntityOf(c.ID) == before.EntityOf(id) && after.EntityOf(c.ID) != after.EntityOf(id) {
				bad = append(bad, fmt.Sprintf("seed %d: moved credit %d left its full name's entity", seed, id))
				break
			}
		}
	}
	comps := map[uuid.UUID]*propComp{}
	for _, c := range pc.in.Credits {
		f := pc.byFP[c.Fingerprint]
		if f.manual {
			if got := after.EntityOf(c.ID); got != pc.fixedID[f.fp] {
				bad = append(bad, fmt.Sprintf("manual credit %d moved to %v", c.ID, got))
			}
			continue
		}
		if moved[c.ID] {
			// Checked above: in its full name's entity.
			continue
		}
		id := before.EntityOf(c.ID)
		comp := comps[id]
		if comp == nil {
			comp = &propComp{books: map[int64]bool{}, fps: map[string]bool{}}
			comps[id] = comp
		}
		comp.credits = append(comp.credits, c)
		comp.books[c.BookID] = true
		comp.fps[c.Fingerprint] = true
		comp.collective = comp.collective || f.collective
	}
	for _, comp := range comps {
		comp.n = pc.oracleDisplay(comp.credits)
		comp.key = oracleKey(comp.n.display)
	}
	merged, stats := pc.expect(comps)
	for _, x := range comps {
		for _, y := range comps {
			if x == y {
				continue
			}
			want := merged[x] == merged[y]
			got := after.EntityOf(x.credits[0].ID) == after.EntityOf(y.credits[0].ID)
			if want != got {
				bad = append(bad, fmt.Sprintf("seed %d: %q and %q: want together %v, got %v",
					seed, x.n.display, y.n.display, want, got))
			}
		}
		for _, c := range x.credits[1:] {
			if after.EntityOf(c.ID) != after.EntityOf(x.credits[0].ID) {
				bad = append(bad, fmt.Sprintf("seed %d: %q split", seed, x.n.display))
			}
		}
	}
	// Every entity's item: by its display name, 1–3.
	byEntity := map[uuid.UUID][]authorentity.Credit{}
	for _, c := range pc.in.Credits {
		if !pc.byFP[c.Fingerprint].manual {
			byEntity[after.EntityOf(c.ID)] = append(byEntity[after.EntityOf(c.ID)], c)
		}
	}
	for id, cs := range byEntity {
		display := pc.oracleDisplay(cs).display
		want := pc.namedBy(oracleKey(display))
		switch {
		case pc.pens[want] || slices.ContainsFunc(cs, func(c authorentity.Credit) bool {
			return pc.byFP[c.Fingerprint].collective
		}):
			want = ""
		case want != "" && slices.ContainsFunc(cs, func(c authorentity.Credit) bool {
			// A short name beside a full one shown as the entity's name.
			f := pc.byFP[c.Fingerprint]
			return f.n.display == display && pc.shortBesideFull(f)
		}):
			stats["withheld"]++
			want = ""
		}
		if got := after.WikidataOf(id); got != want {
			bad = append(bad, fmt.Sprintf("seed %d: entity %q: item %q, want %q", seed, pc.oracleDisplay(cs).display,
				got, want))
		}
		if want != "" {
			stats["references"]++
		}
	}
	if len(moved) > 0 {
		stats["d3_cases"]++
	}
	return stats, bad
}

func TestWikidataMergesExactlyWhatTheRuleSays(t *testing.T) {
	total := map[string]int{}
	failing := 0
	for seed := int64(1); seed <= propertyCases; seed++ {
		pc := generate(seed)
		stats, bad := pc.check(t, seed)
		for k, v := range stats {
			total[k] += v
		}
		if len(bad) > 0 {
			failing++
			if failing <= 10 {
				t.Error(strings.Join(bad, "; "))
			}
		}
	}
	t.Logf("%d cases, %d failing; %v", propertyCases, failing, total)
	// Every condition must have been met, or the oracle proves nothing.
	for _, k := range []string{"named", "merges", "same_name", "pseudonym", "initials", "short", "namesake",
		"script_conflict", "shared_book", "collective", "references", "withheld", "d3_cases"} {
		if total[k] == 0 {
			t.Errorf("the generator never exercised %q", k)
		}
	}
}
