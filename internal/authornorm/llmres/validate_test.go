package llmres

import (
	"errors"
	"testing"
)

// Helpers build inputs and replies without touching JSON so each test aims at
// exactly one validator.

func inputOf(t *testing.T, fields ...FieldValue) Input {
	t.Helper()
	in, err := NewInput(fields)
	if err != nil {
		t.Fatalf("NewInput(%v): %v", fields, err)
	}
	return in
}

func personReply(roles ...RoleAssignment) Reply {
	return Reply{Form: FormPerson, Order: OrderGivenFirst, Roles: roles, CaseFix: []CaseFix{}}
}

func role(i int, r Role) RoleAssignment { return RoleAssignment{I: i, Role: r} }

// checkViolation asserts err is a Violation of the named validator.
func checkViolation(t *testing.T, err error, validator string) {
	t.Helper()
	if err == nil {
		t.Fatalf("no violation, want %s", validator)
	}
	var v *Violation
	if !errors.As(err, &v) || v.Validator != validator {
		t.Fatalf("violation = %v, want validator %s", err, validator)
	}
}

// --- V0: the response model id must be one of the expected ids ---

func TestV0ModelID(t *testing.T) {
	if err := ValidateModelID("deepseek-v4-pro-0813", []string{"deepseek-v4-pro-0813"}); err != nil {
		t.Fatalf("expected id rejected: %v", err)
	}
	// The requested name alone is not proof: the resolver answers with a
	// snapshot id, so the bare requested name must not match.
	checkViolation(t, ValidateModelID("deepseek-v4-pro", []string{"deepseek-v4-pro-0813"}), "V0")
	// The gateway's short alias is admitted only because the configuration
	// lists it explicitly.
	if err := ValidateModelID("k3", []string{"kimi-k3", "k3"}); err != nil {
		t.Fatalf("listed alias rejected: %v", err)
	}
	checkViolation(t, ValidateModelID("k3", []string{"kimi-k3"}), "V0")
	checkViolation(t, ValidateModelID("", []string{"k3"}), "V0")
	checkViolation(t, ValidateModelID("gpt-6-luna", nil), "V0")
}

// --- V2: roles cover every input index exactly once ---

func TestV2RoleCoverage(t *testing.T) {
	in := inputOf(t, FieldValue{FieldFirst, "Thilliez,"}, FieldValue{FieldLast, "Franck"})

	ok := personReply(role(0, RoleFamily), role(1, RoleSeparator), role(2, RoleGiven))
	if err := ok.checkV2(&in); err != nil {
		t.Fatalf("complete coverage rejected: %v", err)
	}

	missing := personReply(role(0, RoleFamily), role(2, RoleGiven))
	checkViolation(t, missing.checkV2(&in), "V2")

	duplicate := personReply(role(0, RoleFamily), role(0, RoleGiven), role(1, RoleSeparator), role(2, RoleGiven))
	checkViolation(t, duplicate.checkV2(&in), "V2")

	outOfRange := personReply(role(0, RoleFamily), role(1, RoleSeparator), role(3, RoleGiven))
	checkViolation(t, outOfRange.checkV2(&in), "V2")

	swappedCount := personReply(role(0, RoleFamily), role(1, RoleSeparator))
	checkViolation(t, swappedCount.checkV2(&in), "V2")
}

// --- V3: filler only for placeholder-dictionary tokens, separator only for
// tokens without letters ---

func TestV3FillerAndSeparator(t *testing.T) {
	in := inputOf(t, FieldValue{FieldFirst, "Аноним Мир"})
	fillerOnDictionary := personReply(role(0, RoleFiller), role(1, RoleGiven))
	if err := fillerOnDictionary.checkV3(&in); err != nil {
		t.Fatalf("dictionary placeholder as filler rejected: %v", err)
	}
	fillerOnName := personReply(role(0, RoleGiven), role(1, RoleFiller))
	checkViolation(t, fillerOnName.checkV3(&in), "V3")

	inLatin := inputOf(t, FieldValue{FieldFirst, "Anonymous"}, FieldValue{FieldLast, "Smith"})
	latinFiller := personReply(role(0, RoleFiller), role(1, RoleFamily))
	if err := latinFiller.checkV3(&inLatin); err != nil {
		t.Fatalf("latin placeholder as filler rejected: %v", err)
	}
	latinFillerOnName := personReply(role(0, RoleGiven), role(1, RoleFiller))
	checkViolation(t, latinFillerOnName.checkV3(&inLatin), "V3")

	inPunct := inputOf(t, FieldValue{FieldFirst, "Thilliez,"})
	commaSeparator := personReply(role(0, RoleFamily), role(1, RoleSeparator))
	if err := commaSeparator.checkV3(&inPunct); err != nil {
		t.Fatalf("comma as separator rejected: %v", err)
	}
	nameAsSeparator := personReply(role(0, RoleSeparator), role(1, RoleSeparator))
	checkViolation(t, nameAsSeparator.checkV3(&inPunct), "V3")

	// An initial carries a letter, so it cannot be a separator.
	inInitial := inputOf(t, FieldValue{FieldFirst, "Л."}, FieldValue{FieldLast, "Калугина"})
	initialAsSeparator := personReply(role(0, RoleSeparator), role(1, RoleFamily))
	checkViolation(t, initialAsSeparator.checkV3(&inInitial), "V3")
}

// --- V4: case_fix only where the local normalizer saw all_caps/lowercase,
// and the fix folds back to the source token ---

func TestV4CaseFix(t *testing.T) {
	in := inputOf(t, FieldValue{FieldFirst, "ИВАН"}, FieldValue{FieldLast, "ПЕТРОВ"})
	fix := []CaseFix{{I: 0, To: CaseFixCapitalize}, {I: 1, To: CaseFixCapitalize}}
	ok := personReply(role(0, RoleGiven), role(1, RoleFamily))
	ok.CaseFix = fix
	if err := ok.checkV4(&in); err != nil {
		t.Fatalf("case_fix on all-caps tokens rejected: %v", err)
	}

	// A well-cased token carries no defect: no case_fix is admitted.
	inClean := inputOf(t, FieldValue{FieldFirst, "Иван"}, FieldValue{FieldLast, "Петров"})
	bad := personReply(role(0, RoleGiven), role(1, RoleFamily))
	bad.CaseFix = []CaseFix{{I: 0, To: CaseFixCapitalize}}
	checkViolation(t, bad.checkV4(&inClean), "V4")

	// Lowercase defect on the first name word of a component.
	inLower := inputOf(t, FieldValue{FieldFirst, "иван"}, FieldValue{FieldLast, "Петров"})
	lowerFix := personReply(role(0, RoleGiven), role(1, RoleFamily))
	lowerFix.CaseFix = []CaseFix{{I: 0, To: CaseFixCapitalize}}
	if err := lowerFix.checkV4(&inLower); err != nil {
		t.Fatalf("case_fix on lowercase defect rejected: %v", err)
	}

	// Duplicate index.
	dup := personReply(role(0, RoleGiven), role(1, RoleFamily))
	dup.CaseFix = []CaseFix{{I: 0, To: CaseFixCapitalize}, {I: 0, To: CaseFixCapitalizeParts}}
	checkViolation(t, dup.checkV4(&in), "V4")

	// Index out of range.
	oob := personReply(role(0, RoleGiven), role(1, RoleFamily))
	oob.CaseFix = []CaseFix{{I: 5, To: CaseFixCapitalize}}
	checkViolation(t, oob.checkV4(&in), "V4")
}

func TestApplyCaseFix(t *testing.T) {
	tests := []struct{ in, to, want string }{
		{"ИВАН", string(CaseFixCapitalize), "Иван"},
		{"иван", string(CaseFixCapitalize), "Иван"},
		{"PETROV", string(CaseFixCapitalize), "Petrov"},
		{"АННА-МАРИЯ", string(CaseFixCapitalizeParts), "Анна-Мария"},
		{"о'брайен", string(CaseFixCapitalizeParts), "О'Брайен"},
		{"О'БРАЙЕН", string(CaseFixCapitalizeParts), "О'Брайен"},
		{"MCDONALD", string(CaseFixCapitalize), "Mcdonald"},
	}
	for _, tt := range tests {
		if got := applyCaseFix(tt.in, CaseFixKind(tt.to)); got != tt.want {
			t.Errorf("applyCaseFix(%q, %s) = %q, want %q", tt.in, tt.to, got, tt.want)
		}
	}
}

// --- V5: role grammar for person ---

func TestV5PersonGrammar(t *testing.T) {
	in := inputOf(t, FieldValue{FieldFirst, "Иван"}, FieldValue{FieldLast, "Иванов"})

	// A person needs at least one given/family/single_name role.
	noName := personReply(role(0, RoleSeparator), role(1, RoleSeparator))
	checkViolation(t, noName.checkV5(&in), "V5")

	// order=given_first requires the first name token to be given.
	orderLie := personReply(role(0, RoleFamily), role(1, RoleGiven))
	checkViolation(t, orderLie.checkV5(&in), "V5")

	// order=family_first requires the first name token to be family.
	familyFirst := Reply{Form: FormPerson, Order: OrderFamilyFirst,
		Roles: []RoleAssignment{role(0, RoleFamily), role(1, RoleGiven)}, CaseFix: []CaseFix{}}
	if err := familyFirst.checkV5(&in); err != nil {
		t.Fatalf("consistent family_first rejected: %v", err)
	}
	givenLie := Reply{Form: FormPerson, Order: OrderGivenFirst,
		Roles: []RoleAssignment{role(0, RoleFamily), role(1, RoleGiven)}, CaseFix: []CaseFix{}}
	checkViolation(t, givenLie.checkV5(&in), "V5")

	// not_applicable is not a person's order.
	na := Reply{Form: FormPerson, Order: OrderNotApplicable,
		Roles: []RoleAssignment{role(0, RoleGiven), role(1, RoleFamily)}, CaseFix: []CaseFix{}}
	checkViolation(t, na.checkV5(&in), "V5")

	// Family tokens form one contiguous segment, particles aside.
	inSplit := inputOf(t, FieldValue{FieldFirst, "Гарсиа Лорка де Пабло"})
	splitFamily := Reply{Form: FormPerson, Order: OrderFamilyFirst, CaseFix: []CaseFix{}, Roles: []RoleAssignment{
		role(0, RoleFamily), role(1, RoleGiven), role(2, RoleFamily), role(3, RoleParticle), role(4, RoleFamily)}}
	checkViolation(t, splitFamily.checkV5(&inSplit), "V5")

	// Particles inside the family segment are fine.
	inParticles := inputOf(t, FieldValue{FieldFirst, "Сервантес де Сааведра, Мигель"})
	particlesInside := Reply{Form: FormPerson, Order: OrderFamilyFirst, Roles: []RoleAssignment{
		role(0, RoleFamily), role(1, RoleParticle), role(2, RoleFamily),
		role(3, RoleSeparator), role(4, RoleGiven)}, CaseFix: []CaseFix{}}
	if err := particlesInside.checkV5(&inParticles); err != nil {
		t.Fatalf("family segment with inner particle rejected: %v", err)
	}

	// Prefix only at the beginning, suffix only at the end.
	inPrefix := inputOf(t, FieldValue{FieldFirst, "Иван святитель"})
	prefixLate := personReply(role(0, RoleGiven), role(1, RolePrefix))
	checkViolation(t, prefixLate.checkV5(&inPrefix), "V5")
	prefixEarly := personReply(role(0, RolePrefix), role(1, RoleGiven))
	if err := prefixEarly.checkV5(&inPrefix); err != nil {
		t.Fatalf("leading prefix rejected: %v", err)
	}

	inSuffix := inputOf(t, FieldValue{FieldFirst, "Иван младший"})
	suffixEarly := personReply(role(0, RoleSuffix), role(1, RoleGiven))
	checkViolation(t, suffixEarly.checkV5(&inSuffix), "V5")

	// single_name excludes given/additional/family and fixes the order.
	inMono := inputOf(t, FieldValue{FieldFirst, "Мадонна"})
	mono := Reply{Form: FormPerson, Order: OrderSingleName,
		Roles: []RoleAssignment{role(0, RoleSingleName)}, CaseFix: []CaseFix{}}
	if err := mono.checkV5(&inMono); err != nil {
		t.Fatalf("mononym rejected: %v", err)
	}
	monoMixed := Reply{Form: FormPerson, Order: OrderSingleName,
		Roles: []RoleAssignment{role(0, RoleSingleName), role(1, RoleFamily)}, CaseFix: []CaseFix{}}
	inMono2 := inputOf(t, FieldValue{FieldFirst, "Мадонна Чикконе"})
	checkViolation(t, monoMixed.checkV5(&inMono2), "V5")
}

// --- V6: non-person forms carry no field structure ---

func TestV6NonPerson(t *testing.T) {
	in := inputOf(t, FieldValue{FieldFirst, "Chuck Hogan, Guillermo del Toro"})
	collective := Reply{Form: FormMultiplePersons, Order: OrderNotApplicable, Roles: []RoleAssignment{
		role(0, RoleGiven), role(1, RoleFamily), role(2, RoleSeparator),
		role(3, RoleGiven), role(4, RoleParticle), role(5, RoleFamily)}, CaseFix: []CaseFix{}}
	if err := collective.checkV6(&in); err != nil {
		t.Fatalf("collective with not_applicable order rejected: %v", err)
	}
	badOrder := collective
	badOrder.Order = OrderGivenFirst
	checkViolation(t, badOrder.checkV6(&in), "V6")
}

// --- V7: the emitted token keeps the source token's letters ---

func TestV7TokenInvariant(t *testing.T) {
	for _, ok := range [][2]string{
		{"Иван", "Иван"},         // identical
		{"ИВАН", "Иван"},         // case fix
		{"Hиколай", "Николай"},   // homoglyph repair Latin H -> Cyrillic Н
		{"Iван", "Иван"},         // homoglyph repair Latin I -> Cyrillic И
		{"Thilliez", "Thilliez"}, // unchanged latin
		{"О'БРАЙЕН", "О'Брайен"}, // parts case fix
	} {
		if err := CheckTokenInvariant(ok[0], ok[1]); err != nil {
			t.Errorf("CheckTokenInvariant(%q, %q) = %v, want nil", ok[0], ok[1], err)
		}
	}
	// "иВАН" from "Иван" folds back to the same string: a case-only change is
	// reachable by a (pointless) case fix, so the invariant admits it.
	if err := CheckTokenInvariant("Иван", "иВАН"); err != nil {
		t.Errorf("case-only change must pass: %v", err)
	}
	for _, pair := range [][2]string{
		{"Иван", "Iван"},       // transliteration is a letter change
		{"Иван", "Иваныч"},     // length change
		{"Иван", "Ивaн"},       // sneaked-in Latin a
		{"Hиколай", "Hиколаи"}, // homoglyph must not corrupt other letters
		{"Иван", "Ивай"},
	} {
		err := CheckTokenInvariant(pair[0], pair[1])
		checkViolation(t, err, "V7")
	}
}

// --- Validate runs V2..V6 in order over the whole reply ---

func TestValidateRunsAllValidators(t *testing.T) {
	in := inputOf(t, FieldValue{FieldFirst, "Thilliez,"}, FieldValue{FieldLast, "Franck"})
	valid := Reply{Form: FormPerson, Order: OrderFamilyFirst, Roles: []RoleAssignment{
		role(0, RoleFamily), role(1, RoleSeparator), role(2, RoleGiven)}, CaseFix: []CaseFix{}}
	if err := valid.Validate(&in); err != nil {
		t.Fatalf("valid reply rejected: %v", err)
	}

	// A coverage hole surfaces before a grammar problem.
	coverage := valid
	coverage.Roles = valid.Roles[:2]
	checkViolation(t, coverage.Validate(&in), "V2")

	// A filler violation surfaces before a grammar problem: here the reply
	// also has the order lying about the first name token.
	both := valid
	both.Roles = []RoleAssignment{role(0, RoleFiller), role(1, RoleSeparator), role(2, RoleGiven)}
	checkViolation(t, both.Validate(&in), "V3")
}
