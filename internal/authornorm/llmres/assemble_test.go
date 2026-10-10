package llmres

import (
	"errors"
	"testing"

	"gopds-api/internal/authornorm"
)

const (
	testExtractorVersion = "extractor-test-v1"
	testConfigVersion    = "authornorm-llm-test-v1"
)

// assembleOne runs the full pipeline on a single-field-per-component source.
func assembleOne(t *testing.T, fields []FieldValue, rep *Reply) Outcome {
	t.Helper()
	in := inputOf(t, fields...)
	if err := rep.Validate(&in); err != nil {
		t.Fatalf("reply must be valid for assembly: %v", err)
	}
	out, err := Assemble(&in, rep, testExtractorVersion, testConfigVersion, authornorm.DecisionClass("llm.test"))
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	return out
}

// The worked examples of design doc section 3, end to end.

func TestAssembleInitialsConfirm(t *testing.T) {
	// Л. | А. | Калугина — the models confirm the FB2 field layout.
	out := assembleOne(t,
		[]FieldValue{{FieldFirst, "Л."}, {FieldMiddle, "А."}, {FieldLast, "Калугина"}},
		&Reply{Form: FormPerson, Order: OrderGivenFirst, CaseFix: []CaseFix{}, Roles: []RoleAssignment{
			role(0, RoleGiven), role(1, RoleAdditional), role(2, RoleFamily)}})

	if out.Tier != TierConfirm {
		t.Errorf("tier = %s, want confirm", out.Tier)
	}
	r := out.Result
	if r.GivenName != "Л." || r.AdditionalNames != "А." || r.FamilyName != "Калугина" {
		t.Errorf("fields = %q/%q/%q", r.GivenName, r.AdditionalNames, r.FamilyName)
	}
	if r.DisplayName != "Л. А. Калугина" {
		t.Errorf("display = %q", r.DisplayName)
	}
	if r.SortName != "Калугина, Л. А." {
		t.Errorf("sort = %q", r.SortName)
	}
	if r.SearchKey != "л а калугина" {
		t.Errorf("search key = %q", r.SearchKey)
	}
	if r.Kind != authornorm.KindPerson || r.Method != authornorm.MethodLLM || r.Status != authornorm.StatusNormalized {
		t.Errorf("kind/method/status = %q/%q/%q", r.Kind, r.Method, r.Status)
	}
	if out.HomoglyphRepaired {
		t.Error("no homoglyph repair expected")
	}
}

func TestAssembleCommaInversion(t *testing.T) {
	// Thilliez, | Franck — the comma form unfolds to "Имя Фамилия".
	out := assembleOne(t,
		[]FieldValue{{FieldFirst, "Thilliez,"}, {FieldLast, "Franck"}},
		&Reply{Form: FormPerson, Order: OrderFamilyFirst, CaseFix: []CaseFix{}, Roles: []RoleAssignment{
			role(0, RoleFamily), role(1, RoleSeparator), role(2, RoleGiven)}})

	if out.Tier != TierRestructure {
		t.Errorf("tier = %s, want restructure", out.Tier)
	}
	r := out.Result
	if r.DisplayName != "Franck Thilliez" {
		t.Errorf("display = %q, want inverted comma form", r.DisplayName)
	}
	if r.SortName != "Thilliez, Franck" {
		t.Errorf("sort = %q", r.SortName)
	}
	if r.GivenName != "Franck" || r.FamilyName != "Thilliez" {
		t.Errorf("fields = %q/%q", r.GivenName, r.FamilyName)
	}
	if r.SearchKey != "franck thilliez" {
		t.Errorf("search key = %q", r.SearchKey)
	}
}

func TestAssembleMultiplePersons(t *testing.T) {
	// Chuck Hogan, Guillermo del Toro in one field: the credit is not split.
	out := assembleOne(t,
		[]FieldValue{{FieldFirst, "Chuck Hogan, Guillermo del Toro"}},
		&Reply{Form: FormMultiplePersons, Order: OrderNotApplicable, CaseFix: []CaseFix{}, Roles: []RoleAssignment{
			role(0, RoleGiven), role(1, RoleFamily), role(2, RoleSeparator),
			role(3, RoleGiven), role(4, RoleParticle), role(5, RoleFamily)}})

	if out.Tier != TierReclassify {
		t.Errorf("tier = %s, want reclassify", out.Tier)
	}
	r := out.Result
	if r.Kind != authornorm.KindCollective {
		t.Errorf("kind = %q, want collective", r.Kind)
	}
	// Non-person roles are not used for fields; the display keeps source order.
	if r.DisplayName != "Chuck Hogan, Guillermo del Toro" {
		t.Errorf("display = %q", r.DisplayName)
	}
	if r.GivenName != "" || r.FamilyName != "" || r.SortName != "" {
		t.Errorf("no fields for a collective: %q/%q/%q", r.GivenName, r.FamilyName, r.SortName)
	}
}

func TestAssembleHomoglyphRepair(t *testing.T) {
	// Hиколай | Семенович | Лесков — the Latin H is repaired deterministically.
	out := assembleOne(t,
		[]FieldValue{{FieldFirst, "Hиколай"}, {FieldMiddle, "Семенович"}, {FieldLast, "Лесков"}},
		&Reply{Form: FormPerson, Order: OrderGivenFirst, CaseFix: []CaseFix{}, Roles: []RoleAssignment{
			role(0, RoleGiven), role(1, RoleAdditional), role(2, RoleFamily)}})

	if !out.HomoglyphRepaired {
		t.Error("homoglyph_repaired flag must be set")
	}
	r := out.Result
	if r.DisplayName != "Николай Семенович Лесков" {
		t.Errorf("display = %q", r.DisplayName)
	}
	if r.GivenName != "Николай" {
		t.Errorf("given = %q", r.GivenName)
	}
	if r.SearchKey != "николай семенович лесков" {
		t.Errorf("search key = %q", r.SearchKey)
	}
}

func TestAssembleNicknameExtraction(t *testing.T) {
	// Александр Алексеевич Кондаков | журналист — the nickname leaves display.
	out := assembleOne(t,
		[]FieldValue{{FieldFirst, "Александр"}, {FieldMiddle, "Алексеевич"}, {FieldLast, "Кондаков"}, {FieldNickname, "журналист"}},
		&Reply{Form: FormPerson, Order: OrderGivenFirst, CaseFix: []CaseFix{}, Roles: []RoleAssignment{
			role(0, RoleGiven), role(1, RoleAdditional), role(2, RoleFamily), role(3, RoleNickname)}})

	if out.Tier != TierConfirm {
		t.Errorf("tier = %s, want confirm", out.Tier)
	}
	r := out.Result
	if r.Nickname != "журналист" {
		t.Errorf("nickname = %q", r.Nickname)
	}
	if r.DisplayName != "Александр Алексеевич Кондаков" {
		t.Errorf("display = %q", r.DisplayName)
	}
	if r.SortName != "Кондаков, Александр Алексеевич" {
		t.Errorf("sort = %q", r.SortName)
	}
}

func TestAssembleFalseCollective(t *testing.T) {
	// А | И | Деникин — "И" is an initial, not a conjunction.
	out := assembleOne(t,
		[]FieldValue{{FieldFirst, "А"}, {FieldMiddle, "И"}, {FieldLast, "Деникин"}},
		&Reply{Form: FormPerson, Order: OrderGivenFirst, CaseFix: []CaseFix{}, Roles: []RoleAssignment{
			role(0, RoleGiven), role(1, RoleAdditional), role(2, RoleFamily)}})
	if out.Result.Kind != authornorm.KindPerson {
		t.Errorf("kind = %q", out.Result.Kind)
	}
	if out.Result.SortName != "Деникин, А И" {
		t.Errorf("sort = %q", out.Result.SortName)
	}
}

func TestAssembleEasternOrder(t *testing.T) {
	// Лу Синь in one field: family first, sort without a comma.
	out := assembleOne(t,
		[]FieldValue{{FieldFirst, "Лу Синь"}},
		&Reply{Form: FormPerson, Order: OrderFamilyFirst, CaseFix: []CaseFix{}, Roles: []RoleAssignment{
			role(0, RoleFamily), role(1, RoleGiven)}})

	if out.Tier != TierRestructure {
		t.Errorf("tier = %s, want restructure", out.Tier)
	}
	r := out.Result
	if r.DisplayName != "Лу Синь" {
		t.Errorf("display = %q", r.DisplayName)
	}
	if r.SortName != "Лу Синь" {
		t.Errorf("sort = %q, want source order without comma", r.SortName)
	}
	if r.FamilyName != "Лу" || r.GivenName != "Синь" {
		t.Errorf("fields = %q/%q", r.FamilyName, r.GivenName)
	}
}

func TestAssembleSwapFromStructuredFields(t *testing.T) {
	// Иванов (first) | Сергей (last): display keeps the source form, the sort
	// gains the comma because family and given come from different fields.
	out := assembleOne(t,
		[]FieldValue{{FieldFirst, "Иванов"}, {FieldLast, "Сергей"}},
		&Reply{Form: FormPerson, Order: OrderFamilyFirst, CaseFix: []CaseFix{}, Roles: []RoleAssignment{
			role(0, RoleFamily), role(1, RoleGiven)}})

	r := out.Result
	if r.DisplayName != "Иванов Сергей" {
		t.Errorf("display = %q, want unchanged source order", r.DisplayName)
	}
	if r.SortName != "Иванов, Сергей" {
		t.Errorf("sort = %q", r.SortName)
	}
}

func TestAssembleParticleTailInSort(t *testing.T) {
	// Сервантес Сааведра, Мигель де — a particle written lowercase in the
	// source moves to the end of the sort name.
	out := assembleOne(t,
		[]FieldValue{{FieldFirst, "Сервантес Сааведра, Мигель де"}},
		&Reply{Form: FormPerson, Order: OrderFamilyFirst, CaseFix: []CaseFix{}, Roles: []RoleAssignment{
			role(0, RoleFamily), role(1, RoleFamily), role(2, RoleSeparator),
			role(3, RoleGiven), role(4, RoleParticle)}})

	r := out.Result
	if r.DisplayName != "Мигель де Сервантес Сааведра" {
		t.Errorf("display = %q", r.DisplayName)
	}
	if r.SortName != "Сервантес Сааведра, Мигель де" {
		t.Errorf("sort = %q", r.SortName)
	}
	if r.FamilyName != "Сервантес Сааведра" {
		t.Errorf("family = %q", r.FamilyName)
	}
}

func TestAssembleCapitalizedParticleStaysWithFamily(t *testing.T) {
	// Ле Гуин, Урсула — a capitalized particle stays with the family.
	out := assembleOne(t,
		[]FieldValue{{FieldFirst, "Ле Гуин, Урсула"}},
		&Reply{Form: FormPerson, Order: OrderFamilyFirst, CaseFix: []CaseFix{}, Roles: []RoleAssignment{
			role(0, RoleParticle), role(1, RoleFamily), role(2, RoleSeparator), role(3, RoleGiven)}})

	r := out.Result
	if r.DisplayName != "Урсула Ле Гуин" {
		t.Errorf("display = %q", r.DisplayName)
	}
	if r.SortName != "Ле Гуин, Урсула" {
		t.Errorf("sort = %q", r.SortName)
	}
	if r.FamilyName != "Гуин" {
		t.Errorf("family = %q — particles are their own role", r.FamilyName)
	}
}

func TestAssembleCaseFix(t *testing.T) {
	// ИВАН | ПЕТРОВ — an admitted case fix rewrites display and fields.
	out := assembleOne(t,
		[]FieldValue{{FieldFirst, "ИВАН"}, {FieldLast, "ПЕТРОВ"}},
		&Reply{Form: FormPerson, Order: OrderGivenFirst, CaseFix: []CaseFix{
			{I: 0, To: CaseFixCapitalize}, {I: 1, To: CaseFixCapitalize}}, Roles: []RoleAssignment{
			role(0, RoleGiven), role(1, RoleFamily)}})

	if out.Tier != TierCase {
		t.Errorf("tier = %s, want case", out.Tier)
	}
	r := out.Result
	if r.DisplayName != "Иван Петров" {
		t.Errorf("display = %q", r.DisplayName)
	}
	if r.GivenName != "Иван" || r.FamilyName != "Петров" {
		t.Errorf("fields = %q/%q", r.GivenName, r.FamilyName)
	}
	if r.SortName != "Петров, Иван" {
		t.Errorf("sort = %q", r.SortName)
	}
}

func TestAssemblePlaceholderDropsFiller(t *testing.T) {
	// A placeholder word the dictionary owns is filler and leaves the display.
	out := assembleOne(t,
		[]FieldValue{{FieldFirst, "Аноним"}},
		&Reply{Form: FormPlaceholder, Order: OrderNotApplicable, CaseFix: []CaseFix{}, Roles: []RoleAssignment{
			role(0, RoleFiller)}})
	if out.Result.Kind != authornorm.KindUnknown {
		t.Errorf("kind = %q, want unknown", out.Result.Kind)
	}
	if out.Result.DisplayName != "" {
		t.Errorf("display = %q, want empty after dropping the filler", out.Result.DisplayName)
	}
	if out.Tier != TierReclassify {
		t.Errorf("tier = %s, want reclassify", out.Tier)
	}
}

func TestAssembleCannotTellRefuses(t *testing.T) {
	in := inputOf(t, FieldValue{FieldFirst, "Лу Синь"})
	rep := Reply{Form: FormCannotTell, Order: OrderNotApplicable, CaseFix: []CaseFix{}, Roles: []RoleAssignment{
		role(0, RoleFamily), role(1, RoleGiven)}}
	if !rep.Refused() {
		t.Fatal("cannot_tell is a refusal")
	}
	if err := rep.Validate(&in); err != nil {
		t.Fatalf("a refusal is still a structurally valid reply: %v", err)
	}
	if _, err := Assemble(&in, &rep, testExtractorVersion, testConfigVersion, "llm.test"); !errors.Is(err, ErrRefusedReply) {
		t.Fatalf("Assemble = %v, want ErrRefusedReply", err)
	}
}

func TestAssembleNormalizationKey(t *testing.T) {
	fields := []FieldValue{{FieldFirst, "Л."}, {FieldLast, "Калугина"}}
	in := inputOf(t, fields...)
	rep := Reply{Form: FormPerson, Order: OrderGivenFirst, CaseFix: []CaseFix{}, Roles: []RoleAssignment{
		role(0, RoleGiven), role(1, RoleFamily)}}

	out, err := Assemble(&in, &rep, testExtractorVersion, testConfigVersion, "llm.test")
	if err != nil {
		t.Fatal(err)
	}
	v, err := sourceOf(fields)
	if err != nil {
		t.Fatal(err)
	}
	wantFP := authornorm.SourceFingerprint(v)
	if out.Result.SourceFingerprint != wantFP {
		t.Error("source fingerprint must be the source value's fingerprint")
	}
	wantKey, err := authornorm.NormalizationKey(wantFP, testExtractorVersion, testConfigVersion)
	if err != nil {
		t.Fatal(err)
	}
	if out.Result.NormalizationKey != wantKey {
		t.Error("normalization key must be SHA-256(fingerprint, extractor version, config version)")
	}

	// A different configuration version is a different key.
	out2, err := Assemble(&in, &rep, testExtractorVersion, "authornorm-llm-test-v2", "llm.test")
	if err != nil {
		t.Fatal(err)
	}
	if out2.Result.NormalizationKey == out.Result.NormalizationKey {
		t.Error("the normalization key must change with the configuration version")
	}
	if out2.Result.NormalizerVersion != "authornorm-llm-test-v2" {
		t.Errorf("normalizer version = %q", out2.Result.NormalizerVersion)
	}
}

func TestTierPrecedenceRestructureBeatsCase(t *testing.T) {
	// A reply that both restructures and fixes case is a restructure.
	in := inputOf(t, FieldValue{FieldFirst, "ИВАНОВ"}, FieldValue{FieldLast, "Сергей"})
	rep := Reply{Form: FormPerson, Order: OrderFamilyFirst, CaseFix: []CaseFix{
		{I: 0, To: CaseFixCapitalize}}, Roles: []RoleAssignment{
		role(0, RoleFamily), role(1, RoleGiven)}}
	out, err := Assemble(&in, &rep, testExtractorVersion, testConfigVersion, "llm.test")
	if err != nil {
		t.Fatal(err)
	}
	if out.Tier != TierRestructure {
		t.Errorf("tier = %s, want restructure", out.Tier)
	}
}
