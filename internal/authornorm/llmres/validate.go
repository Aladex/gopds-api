package llmres

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/text/cases"

	"gopds-api/internal/authornorm"
)

// Violation is a validator failure: the reply (or the assembled result) broke
// one of the deterministic rules V0-V7 and is invalid. The detail never
// carries token text, only positions — violations may reach the logs.
type Violation struct {
	Validator string
	Detail    string
}

func (e *Violation) Error() string {
	return "llmres: validator " + e.Validator + ": " + e.Detail
}

// The validator ids V0-V7, shared by every Violation the checks raise.
const (
	validatorV0 = "V0"
	validatorV1 = "V1"
	validatorV2 = "V2"
	validatorV3 = "V3"
	validatorV4 = "V4"
	validatorV5 = "V5"
	validatorV6 = "V6"
	validatorV7 = "V7"
)

// ValidateModelID is validator V0: the model id in the response must be one
// of the expected ids of this configuration participant (a snapshot id such
// as deepseek-v4-pro-0813, or a listed alias such as k3). Anything else is a
// model_mismatch: the answer may come from a silently substituted model.
func ValidateModelID(model string, expectedIDs []string) error {
	if model == "" || !slices.Contains(expectedIDs, model) {
		return &Violation{Validator: validatorV0, Detail: "response model id is not in the expected id list"}
	}
	return nil
}

// Input is the validated request side: the tokenized source display name plus
// the per-token case defects the local normalizer observed, computed here so
// the validators never trust the wire.
type Input struct {
	Tokens []Token

	source  authornorm.SourceValue
	defects map[int]authornorm.QualityFlag
}

// NewInput builds the request side from the source fields in document order:
// it tokenizes, and for every token that is the letter-carrying core of a
// white-space word it records the case defect (all_caps / lowercase) the
// local normalizer contract observes on that word.
func NewInput(fields []FieldValue) (Input, error) {
	for _, f := range fields {
		if !f.Field.valid() {
			return Input{}, fmt.Errorf("llmres: unknown field %q", string(f.Field))
		}
	}
	kinds := map[Field]authornorm.ComponentKind{
		FieldFirst:    authornorm.ComponentFirst,
		FieldMiddle:   authornorm.ComponentMiddle,
		FieldLast:     authornorm.ComponentLast,
		FieldNickname: authornorm.ComponentNickname,
	}
	var comps []authornorm.SourceComponent
	for _, f := range fields {
		comps = append(comps, authornorm.SourceComponent{Kind: kinds[f.Field], Value: f.Text})
	}
	v, err := authornorm.NewSourceValue(comps)
	if err != nil {
		return Input{}, err
	}

	displayWordCount := 0
	for _, f := range fields {
		displayWordCount += len(strings.Fields(f.Text))
	}

	var tokens []Token
	defects := map[int]authornorm.QualityFlag{}
	for _, f := range fields {
		wordDefects := authornorm.WordCaseDefects(f.Text, displayWordCount)
		for wi, word := range strings.Fields(f.Text) {
			parts := splitWord(word)
			if flag, ok := wordDefects[wi]; ok {
				// The defect belongs to the word's letter-carrying core token.
				for pi, part := range parts {
					if strings.IndexFunc(part, unicode.IsLetter) >= 0 {
						defects[len(tokens)+pi] = flag
						break
					}
				}
			}
			for _, part := range parts {
				tokens = append(tokens, Token{I: len(tokens), Text: part, Field: f.Field})
			}
		}
	}
	return Input{Tokens: tokens, source: v, defects: defects}, nil
}

// Source returns the canonical source value the input was built from.
func (in *Input) Source() authornorm.SourceValue { return in.source }

// Validate runs the reply validators in order: V2 (coverage), V3 (filler and
// separator constraints), V4 (case fixes), V5 (person grammar), V6
// (non-person forms). V0 is a separate transport-level check and V1 is the
// parsing itself; V7 is the assembly invariant.
func (r *Reply) Validate(in *Input) error {
	for _, check := range []func(*Input) error{r.checkV2, r.checkV3, r.checkV4, r.checkV5, r.checkV6} {
		if err := check(in); err != nil {
			return err
		}
	}
	return nil
}

// roleByIndex maps token index to its assigned role.
func (r *Reply) roleByIndex() map[int]Role {
	out := make(map[int]Role, len(r.Roles))
	for _, ra := range r.Roles {
		out[ra.I] = ra.Role
	}
	return out
}

// checkV2: roles cover every input index exactly once.
func (r *Reply) checkV2(in *Input) error {
	seen := make([]bool, len(in.Tokens))
	for _, ra := range r.Roles {
		if ra.I < 0 || ra.I >= len(in.Tokens) {
			return &Violation{Validator: validatorV2, Detail: "role index out of range"}
		}
		if seen[ra.I] {
			return &Violation{Validator: validatorV2, Detail: "token index covered twice"}
		}
		seen[ra.I] = true
	}
	for i, ok := range seen {
		if !ok {
			return &Violation{Validator: validatorV2, Detail: fmt.Sprintf("token index %d not covered", i)}
		}
	}
	return nil
}

// placeholderVocab caches the local normalizer's closed placeholder
// dictionary: keys for whole-token matches and fragment prefixes.
var placeholderVocab = sync.OnceValues(authornorm.PlaceholderVocabulary)

// isPlaceholderToken reports whether a token's folded form belongs to the
// closed placeholder dictionary of the local normalizer.
func isPlaceholderToken(text string) bool {
	key := authornorm.SearchKey(text)
	if key == "" {
		return false
	}
	keys, prefixes := placeholderVocab()
	if slices.Contains(keys, key) {
		return true
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// checkV3: filler is admitted only for tokens from the closed placeholder
// dictionary, and separator only for tokens without letters.
func (r *Reply) checkV3(in *Input) error {
	for _, ra := range r.Roles {
		if ra.I < 0 || ra.I >= len(in.Tokens) {
			continue // coverage is V2's call
		}
		text := in.Tokens[ra.I].Text
		switch ra.Role {
		case RoleFiller:
			if !isPlaceholderToken(text) {
				return &Violation{Validator: validatorV3, Detail: fmt.Sprintf("filler on token %d outside the placeholder dictionary", ra.I)}
			}
		case RoleSeparator:
			if strings.IndexFunc(text, unicode.IsLetter) >= 0 {
				return &Violation{Validator: validatorV3, Detail: fmt.Sprintf("separator on token %d that carries letters", ra.I)}
			}
		case RoleGiven, RoleAdditional, RoleFamily, RoleParticle, RolePrefix,
			RoleSuffix, RoleNickname, RoleSingleName:
			// V3 constrains only filler and separator.
		}
	}
	return nil
}

// checkV4: a case fix is admitted only for a token where the local normalizer
// observed all_caps or lowercase, it is unique per token, in range, and the
// fixed text folds back to the source token — a fix changes case, never
// letters.
func (r *Reply) checkV4(in *Input) error {
	seen := make(map[int]bool, len(r.CaseFix))
	fold := cases.Fold()
	for _, cf := range r.CaseFix {
		if cf.I < 0 || cf.I >= len(in.Tokens) {
			return &Violation{Validator: validatorV4, Detail: "case_fix index out of range"}
		}
		if seen[cf.I] {
			return &Violation{Validator: validatorV4, Detail: "duplicate case_fix for one token"}
		}
		seen[cf.I] = true
		if _, ok := in.defects[cf.I]; !ok {
			return &Violation{Validator: validatorV4, Detail: fmt.Sprintf("case_fix on token %d without an all_caps/lowercase defect", cf.I)}
		}
		text := in.Tokens[cf.I].Text
		if fold.String(applyCaseFix(text, cf.To)) != fold.String(text) {
			return &Violation{Validator: validatorV4, Detail: fmt.Sprintf("case_fix on token %d changes letters, not case", cf.I)}
		}
	}
	return nil
}

// isHyphenRune and isApostropheRune mirror the local normalizer's token
// punctuation classes; they decide the parts of capitalize_parts.
func isHyphenRune(r rune) bool { return r == '-' || r == '‐' || r == '‑' }

func isApostropheRune(r rune) bool {
	switch r {
	case '\'', '’', '‘', 'ʼ', 'ʻ', '`':
		return true
	}
	return false
}

// applyCaseFix applies one of the two closed case transformations.
func applyCaseFix(text string, kind CaseFixKind) string {
	if kind == CaseFixCapitalizeParts {
		var b strings.Builder
		var part strings.Builder
		flush := func() {
			if part.Len() > 0 {
				b.WriteString(capitalizePart(part.String()))
				part.Reset()
			}
		}
		for _, r := range text {
			if isHyphenRune(r) || isApostropheRune(r) {
				flush()
				b.WriteRune(r)
			} else {
				part.WriteRune(r)
			}
		}
		flush()
		return b.String()
	}
	return capitalizePart(text)
}

// capitalizePart upper-cases the first letter of a run and lower-cases every
// following letter; non-letters pass through untouched.
func capitalizePart(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	firstLetterSeen := false
	for _, r := range text {
		switch {
		case !unicode.IsLetter(r):
			b.WriteRune(r)
		case !firstLetterSeen:
			b.WriteRune(unicode.ToUpper(r))
			firstLetterSeen = true
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// checkV5: the role grammar of a person — at least one name role; family
// tokens in one contiguous segment (particles aside); prefix only before and
// suffix only after the name tokens; the order must agree with the position
// of the first name token.
func (r *Reply) checkV5(in *Input) error {
	if r.Form != FormPerson {
		return nil
	}
	if r.Order == OrderNotApplicable {
		return &Violation{Validator: validatorV5, Detail: "a person's order is never not_applicable"}
	}
	roleBy := r.roleByIndex()
	nameIdx, familyIdx, single := r.nameIndexes()
	if len(nameIdx) == 0 {
		return &Violation{Validator: validatorV5, Detail: "a person needs at least one given, family or single_name role"}
	}
	slices.Sort(nameIdx)
	if single {
		if err := r.checkSingleNameGrammar(); err != nil {
			return err
		}
	}
	if err := checkFamilyContiguous(familyIdx, roleBy); err != nil {
		return err
	}
	if err := r.checkAffixPositions(nameIdx); err != nil {
		return err
	}
	return r.checkOrderMatchesFirstRole(nameIdx[0], roleBy)
}

// nameIndexes collects the indexes of the name-carrying roles: every name
// token, the family tokens separately, and whether a single_name is present.
func (r *Reply) nameIndexes() (nameIdx, familyIdx []int, single bool) {
	for _, ra := range r.Roles {
		if !ra.Role.nameRole() {
			continue
		}
		nameIdx = append(nameIdx, ra.I)
		if ra.Role == RoleFamily {
			familyIdx = append(familyIdx, ra.I)
		}
		if ra.Role == RoleSingleName {
			single = true
		}
	}
	return nameIdx, familyIdx, single
}

// checkSingleNameGrammar: a mononym fixes the order and does not mix with the
// split name roles.
func (r *Reply) checkSingleNameGrammar() error {
	if r.Order != OrderSingleName {
		return &Violation{Validator: validatorV5, Detail: "single_name role requires order single_name"}
	}
	for _, ra := range r.Roles {
		if ra.Role == RoleGiven || ra.Role == RoleAdditional || ra.Role == RoleFamily {
			return &Violation{Validator: validatorV5, Detail: "single_name does not mix with given/additional/family"}
		}
	}
	return nil
}

// checkFamilyContiguous: family tokens form one contiguous segment, particles
// aside.
func checkFamilyContiguous(familyIdx []int, roleBy map[int]Role) error {
	if len(familyIdx) == 0 {
		return nil
	}
	slices.Sort(familyIdx)
	for i := familyIdx[0] + 1; i < familyIdx[len(familyIdx)-1]; i++ {
		if role, ok := roleBy[i]; ok && role != RoleFamily && role != RoleParticle {
			return &Violation{Validator: validatorV5, Detail: "family tokens do not form one contiguous segment"}
		}
	}
	return nil
}

// checkAffixPositions: prefix only before the first name token, suffix only
// after the last one.
func (r *Reply) checkAffixPositions(nameIdx []int) error {
	first, last := nameIdx[0], nameIdx[len(nameIdx)-1]
	for _, ra := range r.Roles {
		if ra.Role == RolePrefix && ra.I >= first {
			return &Violation{Validator: validatorV5, Detail: "prefix after the first name token"}
		}
		if ra.Role == RoleSuffix && ra.I <= last {
			return &Violation{Validator: validatorV5, Detail: "suffix before the last name token"}
		}
	}
	return nil
}

// checkOrderMatchesFirstRole: the written order must agree with the role of
// the first name token.
func (r *Reply) checkOrderMatchesFirstRole(first int, roleBy map[int]Role) error {
	switch r.Order {
	case OrderGivenFirst:
		if roleBy[first] != RoleGiven {
			return &Violation{Validator: validatorV5, Detail: "given_first but the first name token is not given"}
		}
	case OrderFamilyFirst:
		if roleBy[first] != RoleFamily {
			return &Violation{Validator: validatorV5, Detail: "family_first but the first name token is not family"}
		}
	case OrderSingleName:
		if roleBy[first] != RoleSingleName {
			return &Violation{Validator: validatorV5, Detail: "single_name order but the first name token is not single_name"}
		}
	case OrderNotApplicable:
		// A person's not_applicable order is rejected before this check.
	}
	return nil
}

// checkV6: for a non-person form the roles build no fields — only the display
// is assembled (filler/separator aside) — so the reply must not claim an
// order of name tokens.
func (r *Reply) checkV6(in *Input) error {
	if r.Form == FormPerson {
		return nil
	}
	if r.Order != OrderNotApplicable {
		return &Violation{Validator: validatorV6, Detail: "a non-person form has no name order"}
	}
	return nil
}

// CheckTokenInvariant is validator V7: the emitted form of a token keeps the
// source token's letters. Every rune of the emitted text must be the source
// rune itself, a case variant of it (case folding decides), or its homoglyph
// counterpart from the closed repair table; the rune count never changes.
// When a homoglyph replacement was used, the emitted token must be
// single-script — the repair rule's own condition, re-checked on the output.
func CheckTokenInvariant(sourceToken, emitted string) error {
	src := []rune(sourceToken)
	out := []rune(emitted)
	if len(src) != len(out) {
		return &Violation{Validator: validatorV7, Detail: "emitted token changes the rune count"}
	}
	fold := cases.Fold()
	homoglyphUsed := false
	for i := range src {
		if src[i] == out[i] {
			continue
		}
		if unicode.IsLetter(src[i]) && unicode.IsLetter(out[i]) {
			if fold.String(string(src[i])) == fold.String(string(out[i])) {
				continue
			}
			if homoglyphOther[src[i]] == out[i] {
				homoglyphUsed = true
				continue
			}
		}
		return &Violation{Validator: validatorV7, Detail: "emitted token changes a letter of the source token"}
	}
	if homoglyphUsed {
		script := authornorm.DetectScript(emitted)
		if script == authornorm.ScriptMixed {
			return &Violation{Validator: validatorV7, Detail: "homoglyph repair left the token mixed-script"}
		}
	}
	return nil
}
