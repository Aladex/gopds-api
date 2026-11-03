package authorentity

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopds-api/internal/authornorm"

	"github.com/google/uuid"
)

// The fallback entities (E4p): every author credit names an entity. The
// rules above leave some credits without one — a proposal under review no
// rule reads, a credit the model resolve left unresolved, a malformed one, a
// placeholder or a collective stub. Once the pass has its groups, each such
// credit
//   - joins the one entity of exactly its name, as the same-name join does
//     (same_name) — its parsed name, else its display name read given name
//     first, family last, without a part in parentheses or a stray mark:
//     «Игнатий Брянчанинов», a proposal that lost its given name, «Фредерик
//     Дар (Сан-Антонио)», a collective class of a person's or an
//     organization's exact name («НКО СССР») — or, a name of one word too,
//     the one entity a keyed credit of its display name (under the search
//     key and markFold) stands in: «О`Санчес.» beside «О'Санчес»;
//   - else is grouped with the credits of its name into an entity of its
//     own: a person's name as written (as_written) — a handle is the
//     author's identity («lucifer712217», «Shin_Stark») — a collective, a
//     publisher, a magazine or a placeholder by its name, as a collective
//     (as_named);
//   - and a credit with no letter or digit at all names the placeholder
//     entity «Автор не указан» (placeholder).
//
// A name an organization writes — a quoted title, after a word such as
// «Журнал» or «Издательство», or of abbreviations alone («МИД РФ») — is
// read as a collective, never as a person. A pair «Анна и Сергей
// Литвиновы» whose two people are entities of the pass names both, a
// fingerprint row per segment (pair); otherwise it is a collective.
//
// The credits come into the plan only: they are no member of a key, so no
// level-0 component, candidate, card or decision changes, and an entity of
// selected credits gets nothing but the same-name joins.

// The rules of the fallback rows.
const (
	RuleAsWritten   = "as_written"
	RuleAsNamed     = "as_named"
	RulePlaceholder = "placeholder"
	RulePair        = "pair"
)

// organizationWords are the first words of a name an organization writes.
var organizationWords = func() map[string]bool {
	set := map[string]bool{}
	for _, w := range strings.Fields(`журнал журналы газета издательство издание министерство редакция институт
		альманах ассоциация комитет академия университет центр общество ооо оао зао žurnāls magazine press
		publishing`) {
		set[w] = true
	}
	return set
}()

// quotedName is a name that is a title in quotes from end to end
// («Подвиг», "На суше и на море"); a quoted nickname inside a person's name
// («Алексей "фон Раух" Смирнов») is no such name.
var quotedName = regexp.MustCompile(`^\s*[«"„“][^«»"„“]+[»"“”]\s*$`)

// organizationName reports a name an organization writes (see above): its
// first word an organization word — whole, or as one hyphen part of a
// compound («Интернет-журнал Виноградова №4, 2011») — or a comma part that
// begins with a lower-case word no name uses (see commaRole).
func organizationName(display string) bool {
	if quotedName.MatchString(display) {
		return true
	}
	words := strings.Fields(display)
	if len(words) == 0 {
		return false
	}
	first := strings.ToLower(strings.Trim(words[0], ".,:;"))
	for _, part := range strings.Split(first, "-") {
		if organizationWords[part] {
			return true
		}
	}
	if commaRole(display) {
		return true
	}
	if len(words) <= 1 {
		return false
	}
	for _, w := range words {
		w = strings.Trim(w, ".,")
		if utf8.RuneCountInString(w) < 2 || !abbreviation(w) || !strings.ContainsFunc(w, unicode.IsLetter) {
			return false
		}
	}
	return true
}

// commaTitles are the rank and dignity words a person's name carries after
// a comma («Николай II, император», «Пьер де Бурдей, сьер де Брантом»): a
// name, not a role the credit names a publication by.
var commaTitles = func() map[string]bool {
	set := map[string]bool{}
	for _, w := range strings.Fields(`сьер сир папа император императрица царь царица царевич царевна
		король королева князь княгиня княжна граф графиня барон баронесса маркиз маркиза герцог герцогиня
		эрцгерцог эрцгерцогиня кардинал эмир султан шах хан ханша раджа махараджа дофин регент кот`) {
		set[w] = true
	}
	return set
}()

// commaOccupations are the plain occupation and degree words a source
// writes after a person's name («Питер Браун художник, детский писатель»,
// «Священник Даниил Сысоев кандидат богословия»): a name, not a
// publication's role mark.
var commaOccupations = func() map[string]bool {
	set := map[string]bool{}
	for _, w := range strings.Fields(`поэт поэтесса писатель писательница прозаик драматург критик литературовед
		филолог историк философ ученый учёный журналист сценарист режиссер режиссёр актер актёр композитор
		музыкант певец певица скульптор архитектор художник художница переводчик переводчица иллюстратор
		лингвист востоковед синолог японовед германист англист славист социолог психолог экономист
		математик физик химик биолог географ геолог геодезист инженер врач юрист правовед богослов теолог
		священник протоиерей иеромонах монах диакон преподаватель педагог учитель лектор редактор
		составитель библиограф краевед адвокат судья дипломат политик философ кандидат доктор профессор
		академик член-корр`) {
		set[w] = true
	}
	return set
}()

// commaRole reports a display whose part after a comma begins with a
// lower-case word a person's name never begins with: a role the credit
// names its holder by («Горкин А. П., гл. редактор»), a phrase («Автор,
// пиши еще!»), a list of generic words («Конспекты, шпаргалки, учебники
// «ЭКСМО»»). A particle («Арман Жан, де Ришелье»), a generational suffix
// («Д М Маршалл, мл.»), a title (nameTitles, commaTitles), a plain
// occupation (commaOccupations) and a lone handle of a digit or an
// underscore («… Тай Алтай-87, altai87») belong to a name.
func commaRole(display string) bool {
	for _, part := range commaSeparator.Split(display, -1)[1:] {
		words := strings.Fields(part)
		if len(words) == 0 {
			continue
		}
		w := strings.Trim(words[0], ".,")
		if w == "" {
			continue
		}
		if len(words) == 1 && strings.ContainsAny(w, "0123456789_") {
			continue
		}
		first, _ := utf8.DecodeRuneInString(w)
		if unicode.IsUpper(first) || unicode.IsDigit(first) {
			continue
		}
		lower := strings.ToLower(strings.TrimSuffix(w, "."))
		if nameParticles[lower] || nameTitles[lower] || commaTitles[lower] || commaOccupations[lower] ||
			listSuffix.MatchString(w) {
			continue
		}
		// An occupation may follow its modifier («детский писатель»); an
		// abbreviated role mark («гл. редактор») names a publication.
		if !strings.HasSuffix(words[0], ".") && len(words) > 1 {
			if second := strings.ToLower(strings.TrimSuffix(strings.Trim(words[1], ".,"), ".")); commaOccupations[second] {
				continue
			}
		}
		return true
	}
	return false
}

// pairWords is how many words a pair has: two given names, «и», a family.
const pairWords = 4

// pairNames are the two people of a pair «Анна и Сергей Литвиновы»: each
// given name with the family in the forms a plural stands for (Литвиновы:
// Литвинов, Литвинова; Бузиновские: Бузиновский, Бузиновская); ok is false
// for any other shape.
func pairNames(c *Credit) (first, second []sameName, ok bool) {
	w := strings.Fields(c.DisplayName)
	if len(w) != pairWords || !strings.EqualFold(w[1], "и") || !fullName(w[0]) || !fullName(w[2]) {
		return nil, nil, false
	}
	family := w[3]
	forms := []string{family}
	switch {
	case strings.HasSuffix(family, "ие"):
		base := strings.TrimSuffix(family, "ие")
		forms = append(forms, base+"ий", base+"ая")
	case strings.HasSuffix(family, "ы"):
		base := strings.TrimSuffix(family, "ы")
		forms = append(forms, base, base+"а")
	}
	names := func(given string) []sameName {
		var out []sameName
		for _, f := range forms {
			k := keyOf(&Credit{Result: &Result{Kind: KindPerson, Script: c.Script, Given: given, Family: f}})
			if n, ok := namesOf(&k); ok {
				out = append(out, n)
			}
		}
		return out
	}
	return names(w[0]), names(w[2]), true
}

// pairRoots are the roots of a pair's two people, each the one root one
// form of its name names; ok is false when either has none or several.
func pairRoots(byName map[string]map[int]bool, c *Credit) (roots [2]int, ok bool) {
	first, second, ok := pairNames(c)
	if !ok {
		return roots, false
	}
	for i, names := range [][]sameName{first, second} {
		found := map[int]bool{}
		for _, n := range names {
			if r, hit := uniqueRoot(byName, n); hit {
				found[r] = true
			}
		}
		if len(found) != 1 {
			return roots, false
		}
		for r := range found {
			roots[i] = r
		}
	}
	return roots, roots[0] != roots[1]
}

// fallbackRoot is the highest root of a fallback group: every group of keys
// has a root of zero or more, and an entity of manual credits alone -1.
const fallbackRoot = -2

// placeholderName is the entity of the credits that name no one.
const placeholderName = "Автор не указан"

// fallbackNonPerson are the classes of a proposal that is no one person.
var fallbackNonPerson = map[string]bool{
	string(authornorm.ClassCollectiveCandidate): true, string(authornorm.ClassPlaceholderFragment): true,
	string(authornorm.ClassDuplicateComponent): true,
}

// FallbackReport counts the fallback entities (E4p).
type FallbackReport struct {
	// Credits are the credits the step read; Joined those that joined an
	// entity of their name; Entities the fallback entities, by rule.
	Credits  int            `json:"credits"`
	Joined   int            `json:"joined"`
	Pairs    int            `json:"pairs"`
	Entities map[string]int `json:"entities"`
	Rows     map[string]int `json:"rows"`
}

// fallbackCredit is a credit of the step: its index in b.in.Credits, the
// group it goes by and its rule.
type fallbackCredit struct {
	ci          int
	group, rule string
	name, shown sameName
	person      bool
	// pending is a credit the same-name join reads: it joins there when
	// one entity has its name, and comes here when none or several do.
	pending bool
	// ordinal is the segment of a list's credit (lists.go); 0 otherwise.
	ordinal int
	// written is the credit's display name under the search key and
	// markFold, empty for a credit the joins never read.
	written string
}

// collectFallback appends the credits the pass leaves without an entity to
// b.in (a copy) once, after the keys: credits past b.selected never reach a
// card.
func (b *builder) collectFallback() {
	if b.fallback != nil {
		return
	}
	b.fallback = []fallbackCredit{}
	in := *b.in
	in.Credits = slices.Clone(in.Credits)
	for _, ci := range b.noKey {
		b.fallback = append(b.fallback, fallbackOf(ci, &in.Credits[ci], false))
	}
	pending := map[*Unselected]bool{}
	var us []*Unselected
	for _, w := range b.sameName {
		pending[w.u] = true
		us = append(us, w.u)
	}
	for _, u := range slices.Concat(us, b.leftUnselected, ptrs(b.in.Fallback)) {
		b.collectOne(&in, u, pending[u])
	}
	for len(b.creditKey) < len(in.Credits) {
		b.creditKey = append(b.creditKey, -1)
	}
	b.in = &in
}

// collectOne appends credit u to in as the step reads it: a list's
// segments, else the one credit with the names its joins read; pending is
// a credit the same-name join reads.
func (b *builder) collectOne(in *Input, u *Unselected, pending bool) {
	c := u.Credit
	res := *c.Result
	c.Result = &res
	one, segments := b.readListed(&c)
	if len(segments) > 1 {
		b.collectSegments(in, c, segments, u.Tag == 0)
		return
	}
	ci := len(in.Credits)
	nonPerson := (fallbackNonPerson[u.Class] || c.Kind != KindPerson) && !one
	in.Credits = append(in.Credits, c)
	f := fallbackOf(ci, &in.Credits[ci], nonPerson)
	if f.person && !one {
		f.name, _ = u.sameNameOf()
		f.pending = pending
	}
	if u.Tag == 0 && (one || !sameNameExcluded[u.Class]) && f.rule != RulePlaceholder {
		f.shown = shownName(&in.Credits[ci])
	}
	// The written name is the whole name, so a class of several people or
	// a collective joins only an entity of exactly it.
	if u.Tag == 0 && f.rule != RulePlaceholder {
		f.written = markFold(in.Credits[ci].DisplayName)
	}
	b.fallback = append(b.fallback, f)
}

// collectSegments appends a list's segments (lists.go) to in, each a
// person of its own read as written, joining by its shown name when the
// credit has no namesake tag (shown).
func (b *builder) collectSegments(in *Input, c Credit, segments []*Result, shown bool) {
	for i, r := range segments {
		seg := c
		seg.Result = r
		ci := len(in.Credits)
		in.Credits = append(in.Credits, seg)
		k := keyOf(&seg)
		f := fallbackCredit{ci: ci, group: "p|" + k.String(), rule: RuleListed, person: true, ordinal: i}
		if shown {
			f.shown = shownName(&seg)
		}
		b.fallback = append(b.fallback, f)
	}
}

// ptrs are pointers to the elements of us.
func ptrs(us []Unselected) []*Unselected {
	out := make([]*Unselected, len(us))
	for i := range us {
		out[i] = &us[i]
	}
	return out
}

// fallbackOf is how the step reads credit c (index ci): a person's name as
// written, keyed like any other; anything else by its name as a
// collective; a name with no letter or digit as the placeholder. It
// rewrites c's result to what the entity shows.
func fallbackOf(ci int, c *Credit, nonPerson bool) fallbackCredit {
	display := strings.TrimSpace(c.DisplayName)
	if !nonPerson && c.Kind == KindPerson && !isPlaceholder(c) && !organizationName(display) {
		if k := keyOf(c); k.hasLetterOrDigit() {
			return fallbackCredit{ci: ci, group: "p|" + k.String(), rule: RuleAsWritten, person: true}
		}
	}
	key := authornorm.SearchKey(display)
	res := *c.Result
	res.Kind = KindCollective
	if !strings.ContainsFunc(key, isLetterOrDigit) {
		res.DisplayName, res.SortName = placeholderName, placeholderName
		c.Result = &res
		return fallbackCredit{ci: ci, group: "x|", rule: RulePlaceholder}
	}
	res.DisplayName, res.SortName = display, display
	c.Result = &res
	return fallbackCredit{ci: ci, group: "c|" + key, rule: RuleAsNamed}
}

// isLetterOrDigit reports a rune that names something.
func isLetterOrDigit(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// fallbackGroups are the step's groups of the pass: the credits that join
// the one entity root of their name (joined, by credit index) and a group
// per name of the others, on roots below every key's.
func (b *builder) fallbackGroups(groups []*group) (out []*group, joined map[int]int, pairs map[int][2]int) {
	b.collectFallback()
	if len(b.fallback) == 0 {
		return nil, nil, nil
	}
	byName, byWritten := b.rootsByName(groups), b.rootsByWritten(groups)
	joined, pairs = map[int]int{}, map[int][2]int{}
	byGroup := map[string]*group{}
	var order []string
	for i := range b.fallback {
		f := &b.fallback[i]
		if root, found := f.joinRoot(byName, byWritten); found {
			// A pending credit the same-name join joins itself.
			if root >= 0 {
				joined[f.ci] = root
			}
			continue
		}
		if f.rule == RuleAsNamed {
			if roots, ok := pairRoots(byName, &b.in.Credits[f.ci]); ok {
				pairs[f.ci] = roots
				continue
			}
		}
		g := byGroup[f.group]
		if g == nil {
			g = &group{root: fallbackRoot - len(order), books: map[int64]bool{}}
			byGroup[f.group] = g
			order = append(order, f.group)
		}
		g.credits = append(g.credits, f.ci)
		g.books[b.in.Credits[f.ci].BookID] = true
	}
	for _, k := range order {
		out = append(out, byGroup[k])
	}
	return out, joined, pairs
}

// joinRoot is the one root a credit joins by its names in order — parsed,
// shown, written; found with root -1 for a pending credit the same-name
// join joins.
func (f *fallbackCredit) joinRoot(byName, byWritten map[string]map[int]bool) (root int, found bool) {
	if f.person && f.name.key != "" {
		if r, ok := uniqueRoot(byName, f.name); ok {
			if f.pending {
				return -1, true
			}
			return r, true
		}
	}
	if r, ok := uniqueRoot(byName, f.shown); ok && f.shown.key != "" {
		return r, true
	}
	if roots := byWritten[f.written]; f.written != "" && len(roots) == 1 {
		for r := range roots {
			return r, true
		}
	}
	return 0, false
}

// inParentheses is a part of a display name in parentheses: a pen name, a
// maiden name, a gloss.
var inParentheses = regexp.MustCompile(`\s*\([^()]*\)?`)

// placeWords are the words of a part in parentheses that tells namesakes
// apart by their place («Сергей Смирнов (Москва)», «(Подмосковье)»): such
// a name is no other name without it.
var placeWords = func() map[string]bool {
	set := map[string]bool{}
	for _, w := range strings.Fields(`москва подмосковье спб петербург санкт ленинград киев минск харьков одесса
		екатеринбург новосибирск казань самара нижний новгород ростов воронеж пермь омск томск иркутск красноярск
		владивосток рига таллин вильнюс израиль сша украина беларусь казахстан германия канада россия`) {
		set[w] = true
	}
	return set
}()

// shownName is a credit's display name as a person's: given name first,
// family last, the words between additional, a part in parentheses
// dropped; empty for a display name of fewer than two words, a placeholder,
// or one whose part in parentheses names a place.
func shownName(c *Credit) sameName {
	for _, part := range inParentheses.FindAllString(c.DisplayName, -1) {
		for _, w := range strings.Fields(authornorm.SearchKey(part)) {
			if placeWords[w] {
				return sameName{}
			}
		}
	}
	var words []string
	for _, w := range strings.Fields(inParentheses.ReplaceAllString(c.DisplayName, " ")) {
		// A stray mark at a word's edge («>Юрий», «Сергеев+», a byte-order
		// mark) is no part of the name; an initial keeps its dot.
		if w = strings.TrimFunc(w, func(r rune) bool { return !isLetterOrDigit(r) && r != '.' }); w != "" {
			words = append(words, w)
		}
	}
	if len(words) <= 1 {
		return sameName{}
	}
	res := &Result{Kind: KindPerson, Given: words[0], Family: words[len(words)-1], Suffix: c.Suffix}
	if middle := words[1 : len(words)-1]; len(middle) > 0 {
		res.Additional = []string{strings.Join(middle, " ")}
	}
	res.Script = string(authornorm.DetectScript(strings.Join(words, " ")))
	probe := &Credit{Result: res}
	if isPlaceholder(probe) {
		return sameName{}
	}
	k := keyOf(probe)
	n, _ := namesOf(&k)
	return n
}

// uniqueRoot is the one root a name names: its key, else its word
// sequence, else its words in any order, as the same-name join reads them.
func uniqueRoot(byName map[string]map[int]bool, n sameName) (int, bool) {
	for _, s := range []string{n.key, n.sequence, n.anyOrder} {
		if s == "" || len(byName[s]) == 0 {
			continue
		}
		if len(byName[s]) > 1 {
			return 0, false
		}
		for r := range byName[s] {
			return r, true
		}
	}
	return 0, false
}

// rootsByName are the group roots of the pass by each name of their keys:
// what entitiesByName reads, by root; a key whose books all moved goes by
// the one root they moved to.
func (b *builder) rootsByName(groups []*group) map[string]map[int]bool {
	has := map[int]bool{}
	for _, g := range groups {
		has[g.root] = true
	}
	byName, moved := map[string]map[int]bool{}, map[string]map[int]bool{}
	for _, kg := range b.keys {
		into, root := byName, b.find(kg.idx)
		if !has[root] {
			var ok bool
			if root, ok = b.movedRoot(kg, has); !ok {
				continue
			}
			into = moved
		}
		n, ok := namesOf(&kg.key)
		if !ok {
			continue
		}
		for _, s := range []string{n.key, n.sequence, n.anyOrder} {
			if s == "" {
				continue
			}
			if into[s] == nil {
				into[s] = map[int]bool{}
			}
			into[s][root] = true
		}
	}
	for s, roots := range moved {
		if byName[s] == nil {
			byName[s] = roots
		}
	}
	return byName
}

// rootsByWritten are the group roots by the display names of their keyed
// credits — selected, or read as selected (E4l) — under the search key and
// markFold.
func (b *builder) rootsByWritten(groups []*group) map[string]map[int]bool {
	out := map[string]map[int]bool{}
	for _, g := range groups {
		for _, ci := range g.credits {
			if ci >= len(b.creditKey) || b.creditKey[ci] < 0 {
				continue
			}
			k := markFold(b.in.Credits[ci].DisplayName)
			if k == "" {
				continue
			}
			if out[k] == nil {
				out[k] = map[int]bool{}
			}
			out[k][g.root] = true
		}
	}
	return out
}

// movedRoot is the one group root a key's moved credits went to; ok is
// false when they went to none or to several.
func (b *builder) movedRoot(kg *keyGroup, has map[int]bool) (root int, ok bool) {
	for _, ci := range kg.credits {
		to, moved := b.movedTo[ci]
		if !moved {
			continue
		}
		r := b.find(to)
		if !has[r] || ok && r != root {
			return 0, false
		}
		root, ok = r, true
	}
	return root, ok
}

// wantFallback gives the step's credits their fingerprint rows: a joined
// one its name's entity (same_name), the others their group's, unless a
// row of a selected credit or the administrator's holds the fingerprint.
func (pl *planner) wantFallback(groups []*group, joined map[int]int, pairs map[int][2]int) {
	b := pl.b
	if len(b.fallback) == 0 {
		return
	}
	rep := &FallbackReport{Entities: map[string]int{}, Rows: map[string]int{}}
	b.rep.Fallback = rep
	rule, ordinal := map[int]string{}, map[int]int{}
	for i := range b.fallback {
		f := &b.fallback[i]
		rule[f.ci], ordinal[f.ci] = f.rule, f.ordinal
		rep.Credits++
	}
	for _, ci := range sortedKeys(joined) {
		if id, ok := pl.rootEntity[joined[ci]]; ok && pl.fallbackRow(ci, ordinal[ci], id, RuleSameName) {
			rep.Joined++
		}
	}
	for _, ci := range sortedKeys(pairs) {
		a, okA := pl.rootEntity[pairs[ci][0]]
		c, okC := pl.rootEntity[pairs[ci][1]]
		if okA && okC && pl.fallbackRow(ci, 0, a, RulePair) && pl.fallbackRow(ci, 1, c, RulePair) {
			rep.Pairs++
		}
	}
	for _, g := range groups {
		r := rule[g.credits[0]]
		rep.Entities[r]++
		for _, ci := range g.credits {
			if pl.fallbackRow(ci, ordinal[ci], g.id, rule[ci]) {
				rep.Rows[rule[ci]]++
			}
		}
	}
}

// fallbackRow writes credit ci's fingerprint row of a segment to entity id
// and reports whether it did.
func (pl *planner) fallbackRow(ci, ordinal int, id uuid.UUID, rule string) bool {
	b := pl.b
	c := &b.in.Credits[ci]
	fp := fpKeyOf(c)
	fp.Ordinal = ordinal
	if row, taken := pl.wantFP[fp]; taken && row.EntityID != id {
		return false
	}
	if old, ok := b.old.Fingerprints[fp]; ok && old.Method == MethodManual {
		return false
	}
	pl.wantFP[fp] = AssignmentRow{Key: fp, EntityID: id, Method: MethodDeterministic, Rule: rule}
	if ordinal == 0 {
		pl.p.entityOf[c.ID], pl.p.ruleOf[c.ID] = id, rule
	}
	return true
}
