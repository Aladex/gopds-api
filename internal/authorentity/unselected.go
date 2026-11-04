package authorentity

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopds-api/internal/authornorm"

	"github.com/google/uuid"
)

// The credits under review (E4k). Their result is a proposal the acceptance
// policy never selects, so the build would leave them out. Two rules read
// them, and neither trusts the proposal alone:
//   - the same-name join: a credit whose name is exactly the name of one
//     entity of the pass — a name selected credits carry — joins it. It only
//     gets the entity's assignment: it is no member of a key, so it moves no
//     evidence, changes no candidate and never names the entity, and it
//     never starts an entity;
//   - the particle rule: a credit whose only doubt is a name particle
//     ("Роберт | ван Гулик") is read as selected when no entity of its name
//     exists, and is keyed like any other credit.
//
// A credit of several people, a collective or a placeholder is left out.

// sameNameExcluded are the classes of a credit that is no one person.
var sameNameExcluded = map[string]bool{
	string(authornorm.ClassCollectiveCandidate): true, string(authornorm.ClassPlaceholderFragment): true,
	string(authornorm.ClassDuplicateComponent): true,
}

// particleCalmFlags are the flags a selected structured name carries too:
// beside the particle they are no doubt of their own.
var particleCalmFlags = map[string]bool{
	string(authornorm.FlagParticle): true, string(authornorm.FlagHyphen): true,
	string(authornorm.FlagApostrophe): true, string(authornorm.FlagWhitespaceCollapsed): true,
	string(authornorm.FlagPlaceholderNickname): true,
}

// nameTitles are the honorifics and clerical titles dropped from a name the
// join compares: the normalizer's affixes and the titles the catalog writes
// in a name field ("протоиерей Александр", "Свт. Григорий").
var nameTitles = func() map[string]bool {
	set := map[string]bool{}
	for _, w := range strings.Fields(`jr sr junior senior младший старший мл св свт прп святой святитель
		преподобный блаженный протоиерей прот священник свящ иерей архимандрит игумен игумения иеромонах монах
		монахиня епископ архиепископ митрополит патриарх dr prof sir dame lord lady rev st mr mrs ms проф академик
		д-р профессор протопресвитер протодиакон диакон схиархимандрит схиигумен иеросхимонах старец авва митр еп
		архиеп`) {
		set[w] = true
	}
	return set
}()

// latinLookalikes are the Latin letters that look like Cyrillic ones;
// cyrillicLookalikes the other way round.
var latinLookalikes = map[rune]rune{'A': 'А', 'B': 'В', 'C': 'С', 'E': 'Е', 'H': 'Н', 'K': 'К', 'M': 'М',
	'O': 'О', 'P': 'Р', 'T': 'Т', 'X': 'Х', 'Y': 'У', 'a': 'а', 'c': 'с', 'e': 'е', 'o': 'о', 'p': 'р', 'x': 'х',
	'y': 'у'}

var cyrillicLookalikes = func() map[rune]rune {
	m := make(map[rune]rune, len(latinLookalikes))
	for l, c := range latinLookalikes {
		m[c] = l
	}
	return m
}()

// sameName is the names an unselected credit is compared by: the build's
// key, the word sequence and, without an initial, the words in any order.
type sameName struct {
	key, sequence, anyOrder string
}

// nameWords are the name words of an unselected credit by field — given,
// additional, family: the proposal's parts, else the source fields; the
// nickname is never a name word. ok is false for a name the join leaves.
func (u *Unselected) nameWords() (fields [3][]string, ok bool) {
	given, additional, family := u.Given, strings.Join(u.Additional, " "), u.Family
	if strings.TrimSpace(given+additional+family) == "" {
		given, additional, family = u.SourceGiven, u.SourceAdditional, u.SourceFamily
	}
	all := 0
	for i, part := range []string{given, additional, family} {
		for _, w := range strings.Fields(part) {
			if nameTitles[strings.TrimSuffix(strings.ToLower(w), ".")] {
				continue
			}
			fields[i] = append(fields[i], w)
			all++
		}
	}
	return fields, all > 0
}

// foldLookalikes writes every letter of the minority script — Latin in a
// mostly Cyrillic name, Cyrillic in a mostly Latin one — as its look-alike.
// ok is false when one has none ("Стогоff"): such a name is not compared.
func foldLookalikes(fields [3][]string) (out [3][]string, ok bool) {
	cyr, lat := 0, 0
	for _, f := range fields {
		for _, w := range f {
			for _, r := range w {
				switch {
				case unicode.Is(unicode.Cyrillic, r):
					cyr++
				case unicode.Is(unicode.Latin, r):
					lat++
				}
			}
		}
	}
	if cyr == 0 || lat == 0 {
		return fields, true
	}
	table, minority := latinLookalikes, unicode.Latin
	if lat > cyr {
		table, minority = cyrillicLookalikes, unicode.Cyrillic
	}
	for i, f := range fields {
		for _, w := range f {
			var b strings.Builder
			for _, r := range w {
				if unicode.Is(minority, r) {
					to, has := table[r]
					if !has {
						return out, false
					}
					r = to
				}
				b.WriteRune(r)
			}
			out[i] = append(out[i], b.String())
		}
	}
	return out, true
}

// sameNameOf is the names an unselected credit joins by. ok is false for a
// credit the join leaves: no one person, a namesake tag, a placeholder, a
// name of fewer than two words or with a letter that has no look-alike.
func (u *Unselected) sameNameOf() (n sameName, ok bool) {
	if u.Kind != KindPerson || sameNameExcluded[u.Class] || u.Tag > 0 {
		return n, false
	}
	words, ok := u.nameWords()
	if !ok {
		return n, false
	}
	if words, ok = foldLookalikes(words); !ok {
		return n, false
	}
	// A part in parentheses the parse left out of the name («Фредерик Дар
	// (Сан-Антонио)» read as «Фредерик | Дар») may be what tells a
	// namesake apart: the name it leaves is no evidence of the person.
	if !parsedQualifier(u.DisplayName, words) {
		return n, false
	}
	res := &Result{Kind: KindPerson, Given: strings.Join(words[0], " "), Family: strings.Join(words[2], " "),
		Suffix: u.Suffix, Nickname: u.Nickname, DisplayName: u.DisplayName}
	if len(words[1]) > 0 {
		res.Additional = []string{strings.Join(words[1], " ")}
	}
	res.Script = string(authornorm.DetectScript(res.Given + " " + strings.Join(res.Additional, " ") + " " +
		res.Family))
	probe := &Credit{Result: res}
	if isPlaceholder(probe) || isPlaceholder(&Credit{Result: u.Result}) {
		return n, false
	}
	key := keyOf(probe)
	n, ok = namesOf(&key)
	return n, ok
}

// parsedQualifier reports a display name whose every word in parentheses,
// a role aside, is a word of the parsed name fields.
func parsedQualifier(display string, fields [3][]string) bool {
	qualifier := qualifierWords(display)
	if len(qualifier) == 0 {
		return true
	}
	have := map[string]bool{}
	for _, f := range fields {
		for _, w := range strings.Fields(authornorm.SearchKey(strings.Join(f, " "))) {
			have[w] = true
		}
	}
	return !slices.ContainsFunc(qualifier, func(w string) bool { return !have[w] })
}

// namesOf is a person key's names, untagged and without a veto part; ok is
// false for a key of fewer than two words.
func namesOf(k *nameKey) (n sameName, ok bool) {
	base := *k
	base.tagged, base.veto = "", ""
	words := strings.Fields(authornorm.SearchKey(base.given + " " + base.additional + " " + base.family))
	if base.kind != KindPerson || len(words) < 2 {
		return n, false
	}
	prefix := base.script + "|" + base.suffix + "|"
	n.key, n.sequence = base.String(), prefix+strings.Join(words, " ")
	if !slices.ContainsFunc(words, func(w string) bool { return utf8.RuneCountInString(w) == 1 }) {
		sorted := slices.Clone(words)
		slices.Sort(sorted)
		n.anyOrder = prefix + strings.Join(sorted, " ")
	}
	return n, true
}

// particleOnly reports a proposal whose only doubt is a name particle, read
// as it is parsed: a particle class with no flag a selected structured name
// would not carry, a given name that does not start with a particle — the
// order swapped ("ван Иттерсум | Надежда"), two people ("Ван Сюаньцзе |
// Джон Моффет") or a particle alone ("Ла | Мэр | Де") — and a family that
// does not end in one ("Камп | Лион | Де").
func (u *Unselected) particleOnly() bool {
	if u.Kind != KindPerson || u.Class != string(authornorm.ClassParticle) || u.Given == "" || u.Family == "" {
		return false
	}
	for _, f := range u.Flags {
		if !particleCalmFlags[f] {
			return false
		}
	}
	count := len(strings.Fields(u.DisplayName))
	bare := func(w string) bool { return authornorm.BareParticleWord(w, count) }
	given, family := strings.Fields(u.Given), strings.Fields(u.Family)
	// A one-word given name that is a particle is a name of its own (Бен
	// Бова, Ал Коруд) when the family neither starts nor ends with one.
	ledByParticle := bare(given[0]) && (len(given) > 1 || bare(family[0]))
	return !ledByParticle && !bare(family[len(family)-1])
}

// admitUnselected sorts the unselected credits before the pass keys the
// selected ones: a particle name with no entity of its name to join is read
// as selected (it joins b.in, a copy); the others wait for the same-name
// join, which the plan makes once the entities are known.
func (b *builder) admitUnselected() {
	if len(b.in.Unselected) == 0 {
		return
	}
	b.rep.Unselected = &UnselectedReport{Rules: map[string]int{}}
	sel := b.selectedNames()
	b.givenKnown = sel.given
	in := *b.in
	in.Credits = slices.Clip(in.Credits)
	for i := range b.in.Unselected {
		u := &b.in.Unselected[i]
		b.rep.Unselected.Read++
		n, ok := u.sameNameOf()
		if ok && (sel.have[n.key] || sel.have[n.sequence] || (n.anyOrder != "" && sel.have[n.anyOrder])) {
			b.sameName = append(b.sameName, pendingJoin{u: u, n: n})
			continue
		}
		if u.particleOnly() {
			in.Credits = append(in.Credits, u.Credit)
			b.rep.Unselected.Particles++
			continue
		}
		if c, read := b.read(u, sel); read {
			in.Credits = append(in.Credits, c)
			continue
		}
		b.leftUnselected = append(b.leftUnselected, u)
	}
	b.in = &in
}

// selectedKeys is what the unselected credits are sorted against: the
// names, the bases of the keys and the given names of the selected credits.
type selectedKeys struct {
	have, bases map[string]bool
	given       givenNames
}

func (b *builder) selectedNames() selectedKeys {
	sel := selectedKeys{have: map[string]bool{}, bases: map[string]bool{}, given: givenNames{}}
	cache := map[int64]keyed{}
	for i := range b.in.Credits {
		c := &b.in.Credits[i]
		sel.given.add(c)
		k, reason := b.keyOfCredit(c, cache)
		if reason != "" {
			continue
		}
		kb := k.base()
		sel.bases[kb.String()] = true
		if n, ok := namesOf(&k); ok {
			sel.have[n.key], sel.have[n.sequence] = true, true
			if n.anyOrder != "" {
				sel.have[n.anyOrder] = true
			}
		}
	}
	return sel
}

// read is an unselected credit a deterministic read reads as selected. A
// read gives no selected name a new full name or spelling: it would change
// the entities D3 and the merges already settled, so such a credit is held.
func (b *builder) read(u *Unselected, sel selectedKeys) (Credit, bool) {
	res, rule, ok := b.readAsSelected(u, sel.given)
	if !ok {
		return Credit{}, false
	}
	c := u.Credit
	res.ResultID = u.ResultID
	c.Result = res
	k := keyOf(&c)
	if kb := k.base(); sel.bases[kb.String()] {
		b.rep.Unselected.Rules[rule+"_held"]++
		return Credit{}, false
	}
	b.rep.Unselected.Rules[rule]++
	return c, true
}

// pendingJoin is an unselected credit waiting for the same-name join.
type pendingJoin struct {
	u *Unselected
	n sameName
}

// wantSameName joins every waiting credit to the one entity of its name —
// its key, else its word sequence, else its words in any order — with a
// fingerprint row; several entities of that name join it to none.
func (pl *planner) wantSameName() {
	b := pl.b
	if len(b.sameName) == 0 {
		return
	}
	// The plan is made again after the decisions: its joins are counted anew.
	b.rep.Unselected.SameName, b.rep.Unselected.Ambiguous = 0, 0
	byName := pl.entitiesByName()
	for _, w := range b.sameName {
		if b.pinTook[w.u.ID] {
			// A pin puts it elsewhere (fallbackGroups).
			continue
		}
		var hit map[uuid.UUID]bool
		for _, s := range []string{w.n.key, w.n.sequence, w.n.anyOrder} {
			if s != "" && len(byName[s]) > 0 {
				hit = byName[s]
				break
			}
		}
		switch {
		case len(hit) > 1:
			b.rep.Unselected.Ambiguous++
		case len(hit) == 1:
			for id := range hit {
				pl.join(w.u, id)
			}
		}
	}
}

// entitiesByName are the entities of the pass by each name of their keys.
// A name no entity's key goes by goes by the one entity the books of its
// emptied keys moved to (E4n): a bucket the majority emptied is still its
// short name's.
func (pl *planner) entitiesByName() map[string]map[uuid.UUID]bool {
	b := pl.b
	byName := map[string]map[uuid.UUID]bool{}
	movedByName := map[string]map[uuid.UUID]bool{}
	for _, kg := range b.keys {
		into := byName
		id, ok := pl.rootEntity[b.find(kg.idx)]
		if !ok {
			into = movedByName
			id, ok = pl.movedEntity(kg)
		}
		if !ok {
			continue
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
				into[s] = map[uuid.UUID]bool{}
			}
			into[s][id] = true
		}
	}
	for s, ids := range movedByName {
		if byName[s] == nil {
			byName[s] = ids
		}
	}
	return byName
}

// movedEntity is the one entity a key's moved credits went to; ok is false
// when they went to none or to several.
func (pl *planner) movedEntity(kg *keyGroup) (id uuid.UUID, ok bool) {
	b := pl.b
	for _, ci := range kg.credits {
		to, moved := b.movedTo[ci]
		if !moved {
			continue
		}
		e, has := pl.rootEntity[b.find(to)]
		if !has || ok && e != id {
			return uuid.UUID{}, false
		}
		id, ok = e, true
	}
	return id, ok
}

// join gives an unselected credit's fingerprint the entity's row, unless a
// selected credit's row or the administrator's holds it; a credit of a
// fingerprint another credit joined shares its row.
func (pl *planner) join(u *Unselected, id uuid.UUID) {
	b := pl.b
	fp := fpKeyOf(&u.Credit)
	if row, taken := pl.wantFP[fp]; taken && (row.Rule != RuleSameName || row.EntityID != id) {
		return
	}
	if old, ok := b.old.Fingerprints[fp]; ok && old.Method == MethodManual {
		return
	}
	pl.wantFP[fp] = AssignmentRow{Key: fp, EntityID: id, Method: MethodDeterministic, Rule: RuleSameName}
	pl.p.entityOf[u.ID], pl.p.ruleOf[u.ID] = id, RuleSameName
	b.rep.Unselected.SameName++
}
