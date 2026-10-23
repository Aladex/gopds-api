package llmres

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
	validatorV8 = "V8"
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

	source authornorm.SourceValue
	// words is the white-space word of the source display each token was
	// cut from, so a form other than person rebuilds the source as written.
	words   []int
	defects map[int]authornorm.QualityFlag
	// particles marks the letter-carrying tokens of the words the local
	// normalizer classifies as name particles.
	particles map[int]bool
}

// SourceFingerprint is the fingerprint of the source the request's fields
// rebuild. It need not be the fingerprint of the credit the fields were taken
// from: an empty FB2 element is left out of the fields, and the credit's
// display keeps the FB2 element order.
func (in *Input) SourceFingerprint() [32]byte {
	return authornorm.SourceFingerprint(in.source)
}

// NewInput builds the request side from the source fields in document order:
// it tokenizes, and for every token that is the letter-carrying core of a
// white-space word it records the case defect a case fix may repair: all_caps
// on the words the local normalizer flags, lowercase on every name word that
// starts lowercase (authornorm.NameWordCaseDefects).
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
	var words []int
	defects := map[int]authornorm.QualityFlag{}
	particles := map[int]bool{}
	wordNo := 0
	for _, f := range fields {
		wordDefects := authornorm.NameWordCaseDefects(f.Text, displayWordCount)
		for wi, word := range strings.Fields(f.Text) {
			parts := splitWord(word)
			if authornorm.ParticleWord(word, displayWordCount) {
				for pi, part := range parts {
					if strings.IndexFunc(part, unicode.IsLetter) >= 0 {
						particles[len(tokens)+pi] = true
					}
				}
			}
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
				words = append(words, wordNo)
			}
			wordNo++
		}
	}
	return Input{Tokens: tokens, source: v, words: words, defects: defects, particles: particles}, nil
}

// Source returns the canonical source value the input was built from.
func (in *Input) Source() authornorm.SourceValue { return in.source }

// Validate runs the reply validators in order: V2 (coverage), V3 (filler and
// separator constraints), V4 (case fixes), V5 (person grammar), V8 (name
// particles). V0 is a
// separate transport-level check and V1 is the parsing itself; V7 is the
// assembly invariant. V6 — a non-person form claiming a name order — is gone
// with the order: the order is computed, not answered.
func (r *Reply) Validate(in *Input) error {
	for _, check := range []func(*Input) error{r.checkV2, r.checkV3, r.checkV4, r.checkV5, r.checkV8} {
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

// checkV2: a person's roles cover every input index exactly once. A form
// other than person is answered by its form: its roles are not part of the
// answer (its canonical form stores none, and the resolve applies an answer
// from that form), so V2 does not read them.
func (r *Reply) checkV2(in *Input) error {
	if r.Form != FormPerson {
		return nil
	}
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

// labelWords is the closed vocabulary of contributor-role labels written into
// a person's name ("Сергей переводчик Смирнов", "Ю. Томашевский Составитель"),
// compared as search keys. A word that is also a given name ("Пер") is left
// out: dropping it would drop a name.
var labelWords = vocabulary("составитель|составители|сост|автор составитель|авт сост|" +
	"редактор|ред|переводчик|иллюстратор|сценарист|самиздат|псевдоним|" +
	"translator|editor|illustrator|compiler|aka")

// labelPhrases are the label phrases whose words are filler only together:
// "под" or "by" alone is an ordinary word.
var labelPhrases = vocabulary("под редакцией|под ред|гл ред|гл редактор|главный редактор|" +
	"отв ред|ответственный редактор|translated by|edited by|illustrated by|compiled by")

// vocabulary is the set of search keys listed in one '|'-separated string.
func vocabulary(list string) map[string]bool {
	set := map[string]bool{}
	for _, key := range strings.Split(list, "|") {
		set[key] = true
	}
	return set
}

// addressPatterns match an e-mail address or a web address written into a
// name. A domain needs a lower-case top-level part, so a glued initial
// ("A.Smith") is no address.
var addressPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[A-Za-z]{2,}$`),
	regexp.MustCompile(`(?i)^(https?://|www\.)\S+$`),
	regexp.MustCompile(`^[\p{L}\p{N}-]+(\.[\p{L}\p{N}-]+)*\.[a-z]{2,6}/?$`),
}

// isFillerToken reports a token the closed dictionaries admit as filler on
// its own: a placeholder word, a role label or an address.
func isFillerToken(text string) bool {
	if isPlaceholderToken(text) || labelWords[authornorm.SearchKey(text)] {
		return true
	}
	for _, p := range addressPatterns {
		if p.MatchString(text) {
			return true
		}
	}
	return false
}

// maxPhraseTokens bounds the windows a filler phrase is looked for in: the
// longest placeholder key or label phrase has three words, and a separator
// token may sit between two of them.
const maxPhraseTokens = 5

// isFillerPhrase reports whether the tokens from..to read, as a search key,
// as a whole placeholder key ("неизвестный автор") or a label phrase ("под
// редакцией").
func isFillerPhrase(in *Input, from, to int) bool {
	texts := make([]string, 0, to-from+1)
	for i := from; i <= to; i++ {
		texts = append(texts, in.Tokens[i].Text)
	}
	key := authornorm.SearchKey(strings.Join(texts, " "))
	keys, _ := placeholderVocab()
	return labelPhrases[key] || slices.Contains(keys, key)
}

// fillerInPhrase reports whether filler token i lies in a phrase whose every
// letter-carrying token is filler too: the words of "Неизвестный автор" are
// filler together, never one of them alone.
func fillerInPhrase(in *Input, roleBy map[int]Role, i int) bool {
	for from := max(0, i-maxPhraseTokens+1); from <= i; from++ {
		for to := i; to < len(in.Tokens) && to-from < maxPhraseTokens; to++ {
			if from == to || !isFillerPhrase(in, from, to) {
				continue
			}
			whole := true
			for j := from; j <= to; j++ {
				if roleBy[j] != RoleFiller && strings.IndexFunc(in.Tokens[j].Text, unicode.IsLetter) >= 0 {
					whole = false
					break
				}
			}
			if whole {
				return true
			}
		}
	}
	return false
}

// checkV3: in a person, filler is admitted only for what the closed
// dictionaries name — a placeholder word, a role label, an address, or a word
// of a whole placeholder key or label phrase that is filler throughout — and
// separator only for tokens without letters. A form other than person is
// answered by its form: its roles build nothing, so V3 does not read them.
func (r *Reply) checkV3(in *Input) error {
	if r.Form != FormPerson {
		return nil
	}
	roleBy := r.roleByIndex()
	for _, ra := range r.Roles {
		if ra.I < 0 || ra.I >= len(in.Tokens) {
			continue // coverage is V2's call
		}
		text := in.Tokens[ra.I].Text
		switch ra.Role {
		case RoleFiller:
			if !isFillerToken(text) && !fillerInPhrase(in, roleBy, ra.I) {
				return &Violation{Validator: validatorV3, Detail: fmt.Sprintf("filler on token %d outside the closed dictionaries", ra.I)}
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

// checkV4: a case fix is admitted only for a token with a case defect
// (NewInput), it is unique per token, in range, and the fixed text folds back
// to the source token: a fix changes case, never letters. The prompt asks for
// no case fix on the nickname field; V4 does not enforce it, so an answer
// that capitalizes a pen name lands in the case tier, which the gate judges
// on its own, instead of counting against its model's invalid share.
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

// checkV5: the role grammar of a person — at least one name role, or a pen
// name alone (only nickname tokens name the person); the first name token
// given, family or single_name (the order follows from it); family tokens in
// one contiguous segment (particles aside); prefix only before the first name
// token, suffix only after it — a title after or inside the name ("Иоанн
// Святитель Златоуст", "Фалассий авва") — and never inside the family
// segment, which contiguity already forbids.
func (r *Reply) checkV5(in *Input) error {
	if r.Form != FormPerson {
		return nil
	}
	roleBy := r.roleByIndex()
	nameIdx, familyIdx, single := r.nameIndexes()
	if len(nameIdx) == 0 {
		nameIdx = roleIndexes(RoleNickname, roleBy)
	}
	if len(nameIdx) == 0 {
		return &Violation{Validator: validatorV5, Detail: "a person needs a given, family, single_name or nickname role"}
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
	if OrderOf(r.Form, r.Roles) == "" {
		return &Violation{Validator: validatorV5, Detail: "the first name token is additional"}
	}
	return nil
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

// checkSingleNameGrammar: a mononym does not mix with the split name roles.
func (r *Reply) checkSingleNameGrammar() error {
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
// after it.
func (r *Reply) checkAffixPositions(nameIdx []int) error {
	first := nameIdx[0]
	for _, ra := range r.Roles {
		if ra.Role == RolePrefix && ra.I >= first {
			return &Violation{Validator: validatorV5, Detail: "prefix after the first name token"}
		}
		if ra.Role == RoleSuffix && ra.I <= first {
			return &Violation{Validator: validatorV5, Detail: "suffix before the first name token"}
		}
	}
	return nil
}

// CheckParticles is V8 alone: it checks a reply stored before V8 existed
// against the particle rule. Like V8 inside Validate it relies on the role
// coverage V2 checked when the reply was answered.
func (r *Reply) CheckParticles(in *Input) error { return r.checkV8(in) }

// TestHookSkipV8, while true, makes V8 accept every reply: a test seam that
// plays a binary built before V8 (resolve runs 15 and 16), so the real
// pipeline stores the rows that binary stored. Production code never sets it.
var TestHookSkipV8 atomic.Bool

// checkV8: a person keeps every name particle in a name component. A token
// of a word the local normalizer classifies as a particle must take the
// family role or the role of the FB2 field it came from (given for first,
// additional for middle, family for last, nickname for nickname). The
// particle role puts the token in no component at all — "Le" of "Le Guin"
// would vanish from the family name — so it is invalid for such a token, and
// so is any other move. A word that only looks like a particle and is a
// given name ("Ben" in the first field) keeps its own field's role.
func (r *Reply) checkV8(in *Input) error {
	if r.Form != FormPerson || TestHookSkipV8.Load() {
		return nil
	}
	own := map[Field]Role{
		FieldFirst:    RoleGiven,
		FieldMiddle:   RoleAdditional,
		FieldLast:     RoleFamily,
		FieldNickname: RoleNickname,
	}
	for _, ra := range r.Roles {
		if ra.I < 0 || ra.I >= len(in.Tokens) || !in.particles[ra.I] {
			continue
		}
		if ra.Role != RoleFamily && ra.Role != own[in.Tokens[ra.I].Field] {
			return &Violation{Validator: validatorV8, Detail: fmt.Sprintf("particle token %d left out of the family and its own field", ra.I)}
		}
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
