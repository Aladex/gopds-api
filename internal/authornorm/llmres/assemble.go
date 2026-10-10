package llmres

import (
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopds-api/internal/authornorm"
)

// ErrRefusedReply marks assembly of a cannot_tell reply: a refusal produces
// no result at all — it can only reduce what gets accepted.
var ErrRefusedReply = errors.New("llmres: cannot_tell is a refusal, nothing to assemble")

// Tier is the evidence class of an answer (design doc section 3): how far the
// agreed reply moves from the FB2 field layout.
type Tier string

// The tiers, in the precedence order assemble computes them: reclassification
// outranks restructuring, which outranks a pure case fix.
const (
	TierConfirm     Tier = "confirm"
	TierRestructure Tier = "restructure"
	TierReclassify  Tier = "reclassify"
	TierCase        Tier = "case"
)

// Outcome is everything the LLM layer derives from one agreed reply.
type Outcome struct {
	Result authornorm.Result
	Tier   Tier
	// HomoglyphRepaired is the homoglyph_repaired flag: the deterministic
	// rule changed at least one letter to a same-shape letter of the other
	// script.
	HomoglyphRepaired bool
}

// Assemble builds the result of one validated reply from the source tokens
// and their roles (design doc section 2, "Сборка результата"). The display
// keeps the source token order except the two admitted moves — nickname
// extraction and the comma-form inversion — filler tokens are dropped, case
// fixes are applied, and the homoglyph rule runs first. Every emitted token
// is checked against the V7 script invariant.
func Assemble(in *Input, rep *Reply, extractorVersion, configVersion string, class authornorm.DecisionClass) (Outcome, error) {
	if rep.Refused() {
		return Outcome{}, ErrRefusedReply
	}
	if err := rep.Validate(in); err != nil {
		return Outcome{}, err
	}

	emitted, repairedAny, err := emittedTokens(in, rep)
	if err != nil {
		return Outcome{}, err
	}

	roleBy := rep.roleByIndex()
	commaIdx := commaBoundary(in, rep, roleBy)

	display := buildDisplay(rep, roleBy, commaIdx, emitted)

	var kind authornorm.Kind
	switch rep.Form {
	case FormPerson:
		kind = authornorm.KindPerson
	case FormCollective, FormMultiplePersons:
		kind = authornorm.KindCollective
	case FormPlaceholder, FormNotAName, FormCannotTell:
		// cannot_tell never reaches assembly — it is a refusal; placeholder
		// and not_a_name carry no kind.
		kind = authornorm.KindUnknown
	}

	r := authornorm.Result{
		DisplayName: display,
		SearchKey:   authornorm.SearchKey(display),
		Script:      authornorm.DetectScript(display),
		Kind:        kind,
		Status:      authornorm.StatusNormalized,
		Method:      authornorm.MethodLLM,

		DecisionClass:     class,
		SourceFingerprint: authornorm.SourceFingerprint(in.source),
		ExtractorVersion:  extractorVersion,
		NormalizerVersion: configVersion,
		SchemaVersion:     authornorm.ResultSchemaVersion,
	}
	key, err := authornorm.NormalizationKey(r.SourceFingerprint, extractorVersion, configVersion)
	if err != nil {
		return Outcome{}, err
	}
	r.NormalizationKey = key

	if rep.Form == FormPerson {
		r.GivenName = joinRole(RoleGiven, roleBy, emitted)
		r.AdditionalNames = joinRole(RoleAdditional, roleBy, emitted)
		r.FamilyName = joinRole(RoleFamily, roleBy, emitted)
		r.Prefix = joinRole(RolePrefix, roleBy, emitted)
		r.Suffix = joinRole(RoleSuffix, roleBy, emitted)
		r.Nickname = joinRole(RoleNickname, roleBy, emitted)
		r.SortName = buildSortName(in, rep, roleBy, emitted, commaIdx)
	}

	return Outcome{Result: r, Tier: computeTier(in, rep), HomoglyphRepaired: repairedAny}, nil
}

// emittedTokens renders the per-token output texts: the homoglyph rule runs
// first, then the admitted case fixes, and every emitted token is checked
// against the V7 script invariant. It also reports whether the homoglyph rule
// changed any token.
func emittedTokens(in *Input, rep *Reply) (emitted []string, repairedAny bool, err error) {
	repaired, repairedAny := RepairHomoglyphs(in.Tokens)
	emitted = make([]string, len(repaired))
	for i, t := range repaired {
		emitted[i] = t.Text
	}
	for _, cf := range rep.CaseFix {
		emitted[cf.I] = applyCaseFix(emitted[cf.I], cf.To)
	}
	for i := range in.Tokens {
		if err := CheckTokenInvariant(in.Tokens[i].Text, emitted[i]); err != nil {
			return nil, false, err
		}
	}
	return emitted, repairedAny, nil
}

// commaBoundary finds the comma separator that splits a family-first comma
// form ("Фамилия, Имя"): a comma-bearing separator token after every family
// token and before every given/additional token. It returns -1 when the
// source has no such comma.
func commaBoundary(in *Input, rep *Reply, roleBy map[int]Role) int {
	if rep.Form != FormPerson || rep.Order != OrderFamilyFirst {
		return -1
	}
	familyIdx := roleIndexes(RoleFamily, roleBy)
	givenIdx := givenIndexes(roleBy)
	if len(familyIdx) == 0 || len(givenIdx) == 0 {
		return -1
	}
	lastFamily, firstGiven := familyIdx[len(familyIdx)-1], givenIdx[0]
	for i, t := range in.Tokens {
		if roleBy[i] == RoleSeparator && strings.ContainsRune(t.Text, ',') && i > lastFamily && i < firstGiven {
			return i
		}
	}
	return -1
}

// roleIndexes returns the sorted indexes of tokens carrying one role.
func roleIndexes(target Role, roleBy map[int]Role) []int {
	var out []int
	for i, role := range roleBy {
		if role == target {
			out = append(out, i)
		}
	}
	slices.Sort(out)
	return out
}

// givenIndexes returns the sorted indexes of given and additional tokens.
func givenIndexes(roleBy map[int]Role) []int {
	out := append(roleIndexes(RoleGiven, roleBy), roleIndexes(RoleAdditional, roleBy)...)
	slices.Sort(out)
	return out
}

// buildDisplay renders the display name: the source tokens without filler
// (and without the nickname for a person — it moves to its own field), with
// the comma form inverted to "Имя Фамилия" when the source wrote
// "Фамилия, Имя".
func buildDisplay(rep *Reply, roleBy map[int]Role, commaIdx int, emitted []string) string {
	keep := func(i int) bool {
		if i == commaIdx {
			return false
		}
		switch roleBy[i] {
		case RoleFiller:
			return false
		case RoleNickname:
			return rep.Form != FormPerson
		case RoleGiven, RoleAdditional, RoleFamily, RoleParticle, RolePrefix,
			RoleSuffix, RoleSingleName, RoleSeparator:
			return true
		}
		return true
	}
	var parts []string
	if commaIdx >= 0 {
		for i := commaIdx + 1; i < len(emitted); i++ {
			if keep(i) {
				parts = append(parts, emitted[i])
			}
		}
	}
	for i := 0; i < len(emitted) && (commaIdx < 0 || i < commaIdx); i++ {
		if keep(i) {
			parts = append(parts, emitted[i])
		}
	}
	return joinTokens(parts)
}

// joinTokens renders token texts back to a string: single spaces between
// words, and a punctuation-only token attaches to its neighbor — a closing
// token to the previous word, an opening bracket to the next one.
func joinTokens(parts []string) string {
	var b strings.Builder
	openingPrev := false
	for i, p := range parts {
		if i > 0 && !openingPrev && !closingToken(p) {
			b.WriteByte(' ')
		}
		b.WriteString(p)
		openingPrev = openingToken(p)
	}
	return b.String()
}

func punctOnly(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool { return !unicode.IsPunct(r) }) < 0
}

func closingToken(s string) bool {
	if !punctOnly(s) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s)
	return !unicode.Is(unicode.Ps, r) && !unicode.Is(unicode.Pi, r)
}

func openingToken(s string) bool {
	if !punctOnly(s) {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(s)
	return unicode.Is(unicode.Ps, r) || unicode.Is(unicode.Pi, r)
}

// joinRole renders the tokens of one role in source order.
func joinRole(target Role, roleBy map[int]Role, emitted []string) string {
	var parts []string
	for i := range emitted {
		if roleBy[i] == target {
			parts = append(parts, emitted[i])
		}
	}
	return joinTokens(parts)
}

// buildSortName renders the sort name of a person (design doc section 2):
// "Фамилия, Имя Второе"; particles written lowercase in the source move to
// the end, capitalized ones stay with the family; an eastern family-first
// order without a comma keeps the source order; a single name sorts as
// itself. Without a family there is no safe sort form and none is invented.
func buildSortName(in *Input, rep *Reply, roleBy map[int]Role, emitted []string, commaIdx int) string {
	if rep.Order == OrderSingleName {
		return joinRole(RoleSingleName, roleBy, emitted)
	}

	// The family part is the family tokens plus the capitalized particles in
	// source order ("Ле Гуин"); lowercase particles go to the tail
	// ("Сервантес Сааведра, Мигель де").
	familyPart, tail := familyParts(in, roleBy, emitted)

	var givenPart []string
	for i := range emitted {
		if roleBy[i] == RoleGiven || roleBy[i] == RoleAdditional {
			givenPart = append(givenPart, emitted[i])
		}
	}

	family := joinTokens(familyPart)
	if family == "" {
		return ""
	}
	given := joinTokens(givenPart)

	familyIdx := roleIndexes(RoleFamily, roleBy)
	givenIdx := givenIndexes(roleBy)
	comma := rep.Order == OrderGivenFirst || commaIdx >= 0 || crossField(in, familyIdx, givenIdx)

	var b strings.Builder
	b.WriteString(family)
	if given != "" {
		if comma {
			b.WriteString(", ")
		} else {
			b.WriteString(" ")
		}
		b.WriteString(given)
	}
	if len(tail) > 0 {
		b.WriteString(" ")
		b.WriteString(joinTokens(tail))
	}
	return b.String()
}

// familyParts splits the emitted tokens into the family part and the particle
// tail: capitalized particles stay with the family, lowercase ones move to
// the tail.
func familyParts(in *Input, roleBy map[int]Role, emitted []string) (familyPart, tail []string) {
	for i := range emitted {
		switch roleBy[i] {
		case RoleFamily:
			familyPart = append(familyPart, emitted[i])
		case RoleParticle:
			if particleCapitalized(in.Tokens[i].Text) {
				familyPart = append(familyPart, emitted[i])
			} else {
				tail = append(tail, emitted[i])
			}
		case RoleGiven, RoleAdditional, RolePrefix, RoleSuffix, RoleNickname,
			RoleSingleName, RoleSeparator, RoleFiller:
			// Neither the family part nor the particle tail.
		}
	}
	return familyPart, tail
}

// particleCapitalized reports whether the source token starts with an
// uppercase letter — the source spelling decides the particle's place in the
// sort name.
func particleCapitalized(sourceText string) bool {
	for _, r := range sourceText {
		if unicode.IsLetter(r) {
			return unicode.IsUpper(r)
		}
	}
	return false
}

// crossField reports whether family and given tokens come from different FB2
// fields — the field structure then vouches for the boundary, and the sort
// name takes the comma form ("Иванов, Сергей") even though the source wrote
// no comma.
func crossField(in *Input, familyIdx, givenIdx []int) bool {
	for _, f := range familyIdx {
		for _, g := range givenIdx {
			if in.Tokens[f].Field != in.Tokens[g].Field {
				return true
			}
		}
	}
	return false
}

// computeTier derives the tier of the answer: reclassify for a non-person
// form, restructure when the roles depart from the FB2 field layout, case
// when only a case fix is applied, confirm when the models confirm the
// layout.
func computeTier(in *Input, rep *Reply) Tier {
	if rep.Form != FormPerson {
		return TierReclassify
	}
	if !rolesMatchFields(in, rep) {
		return TierRestructure
	}
	if len(rep.CaseFix) > 0 {
		return TierCase
	}
	return TierConfirm
}

// rolesMatchFields reports the confirm condition: every token's role is
// exactly what its FB2 field says (given=first, additional=middle,
// family=last, nickname=nickname). A separator or any other departure makes
// the answer a restructure.
func rolesMatchFields(in *Input, rep *Reply) bool {
	expected := map[Field]Role{
		FieldFirst:    RoleGiven,
		FieldMiddle:   RoleAdditional,
		FieldLast:     RoleFamily,
		FieldNickname: RoleNickname,
	}
	roleBy := rep.roleByIndex()
	for i, t := range in.Tokens {
		role, ok := roleBy[i]
		if !ok || role != expected[t.Field] {
			return false
		}
	}
	return true
}
