package llmres

import (
	"testing"

	"gopds-api/internal/authornorm"
)

// The review-backlog shapes of run 14 (report R0, appendix A): answers the
// judges gave that the reply contract had no valid encoding for. Every case
// is a real source shape, named by the prefix of its fingerprint.

// mustAssemble validates and assembles a reply and fails the test on any
// violation.
func mustAssemble(t *testing.T, fields []FieldValue, rep *Reply) Outcome {
	t.Helper()
	in := inputOf(t, fields...)
	out, err := Assemble(&in, rep, testExtractorVersion, testConfigVersion, authornorm.DecisionClass("llm.test"))
	if err != nil {
		t.Fatalf("%v: %v", fields, err)
	}
	return out
}

// mustViolate asserts the reply breaks the named validator.
func mustViolate(t *testing.T, fields []FieldValue, rep *Reply, validator string) {
	t.Helper()
	in := inputOf(t, fields...)
	checkViolation(t, rep.Validate(&in), validator)
}

func formReply(form Form, roles ...RoleAssignment) *Reply {
	return &Reply{Form: form, Roles: roles, CaseFix: []CaseFix{}}
}

func personOf(roles ...RoleAssignment) *Reply {
	rep := formReply(FormPerson, roles...)
	rep.Order = OrderOf(rep.Form, rep.Roles)
	return rep
}

func withFix(rep *Reply, fixes ...CaseFix) *Reply {
	rep.CaseFix = fixes
	return rep
}

// --- A: a form other than person is answered by its form; its roles are
// not part of the answer and constrain nothing ---

func TestBacklogNonPersonFormsOfOrdinaryWordsAreValid(t *testing.T) {
	cases := []struct {
		name    string
		fields  []FieldValue
		reply   *Reply
		kind    authornorm.Kind
		display string
	}{
		{"e7939173 Газета \"Слова и дела\"", []FieldValue{{FieldLast, `Газета "Слова и дела"`}},
			formReply(FormCollective, role(0, RoleFiller), role(1, RoleSeparator), role(2, RoleSingleName),
				role(3, RoleFiller), role(4, RoleFiller), role(5, RoleSeparator)),
			authornorm.KindCollective, `Газета "Слова и дела"`},
		{"286392b8 Международное право", []FieldValue{{FieldFirst, "Международное"}, {FieldLast, "право"}},
			formReply(FormNotAName, role(0, RoleFiller), role(1, RoleFiller)),
			authornorm.KindUnknown, "Международное право"},
		{"800398605 Михал ТЫМОВСКИЙ и др.", []FieldValue{{FieldFirst, "Михал"}, {FieldLast, "ТЫМОВСКИЙ и др."}},
			formReply(FormMultiplePersons, role(0, RoleGiven), role(1, RoleFamily), role(2, RoleFiller),
				role(3, RoleFiller), role(4, RoleSeparator)),
			authornorm.KindCollective, "Михал ТЫМОВСКИЙ и др."},
		{"20e14b4d Department of the Army", []FieldValue{{FieldLast, "Department of the Army"}},
			formReply(FormCollective, role(0, RoleFamily), role(1, RoleSeparator), role(2, RoleSeparator), role(3, RoleFamily)),
			authornorm.KindCollective, "Department of the Army"},
		{"47ff7406 Буддизм Религия Неизвестный автор", []FieldValue{{FieldFirst, "Буддизм"}, {FieldLast, "Религия"},
			{FieldNickname, "Неизвестный автор"}},
			formReply(FormPlaceholder, role(0, RoleFiller), role(1, RoleFiller), role(2, RoleFiller), role(3, RoleFiller)),
			authornorm.KindUnknown, "Буддизм Религия Неизвестный автор"},
		{"5ea54d23 Литературный проект Этногенез", []FieldValue{{FieldFirst, "Литературный"}, {FieldMiddle, "проект"},
			{FieldLast, "Этногенез"}},
			formReply(FormCollective, role(0, RoleFiller), role(1, RoleFiller), role(2, RoleSingleName)),
			authornorm.KindCollective, "Литературный проект Этногенез"},
	}
	for _, c := range cases {
		out := mustAssemble(t, c.fields, c.reply)
		r := out.Result
		if r.Kind != c.kind || r.DisplayName != c.display {
			t.Errorf("%s: kind/display = %q/%q, want %q/%q", c.name, r.Kind, r.DisplayName, c.kind, c.display)
		}
		if r.GivenName != "" || r.FamilyName != "" || r.Nickname != "" || r.SortName != "" {
			t.Errorf("%s: a non-person form fills no name field: %+v", c.name, r)
		}
		if out.Tier != TierReclassify {
			t.Errorf("%s: tier = %s, want reclassify", c.name, out.Tier)
		}
	}
}

func TestBacklogNonPersonAnswersAgreeOnTheFormAlone(t *testing.T) {
	// The judges agree on "collective" and invent different roles: that is
	// agreement, the roles are not part of a non-person answer.
	a := formReply(FormCollective, role(0, RoleFiller), role(1, RoleFiller))
	b := formReply(FormCollective, role(0, RoleSingleName), role(1, RoleFamily))
	if !Agree(a, b) {
		t.Error("two collective answers with different roles disagree")
	}
	// The form still decides.
	if Agree(a, formReply(FormNotAName, role(0, RoleFiller), role(1, RoleFiller))) {
		t.Error("collective and not_a_name agree")
	}
	// The case fix is still part of the answer.
	if Agree(a, withFix(formReply(FormCollective, role(0, RoleFiller), role(1, RoleFiller)), CaseFix{I: 0, To: CaseFixCapitalize})) {
		t.Error("a non-person answer with a case fix agrees with one without")
	}
	// A person's roles stay part of the answer.
	if Agree(personOf(role(0, RoleGiven), role(1, RoleFamily)), personOf(role(0, RoleFamily), role(1, RoleGiven))) {
		t.Error("two persons with different roles agree")
	}
}

func TestBacklogNonPersonCaseFixStillChecked(t *testing.T) {
	// The case fix of a non-person form is still V4's: only where a defect is.
	fields := []FieldValue{{FieldFirst, "Arkadijs un Boriss"}, {FieldLast, "STRUGACKI"}}
	out := mustAssemble(t, fields, withFix(formReply(FormMultiplePersons,
		role(0, RoleGiven), role(1, RoleSeparator), role(2, RoleGiven), role(3, RoleFamily)), CaseFix{I: 3, To: CaseFixCapitalize}))
	if out.Result.DisplayName != "Arkadijs un Boriss Strugacki" {
		t.Errorf("display = %q", out.Result.DisplayName)
	}
	mustViolate(t, fields, withFix(formReply(FormMultiplePersons,
		role(0, RoleGiven), role(1, RoleSeparator), role(2, RoleGiven), role(3, RoleFamily)), CaseFix{I: 0, To: CaseFixCapitalize}),
		validatorV4)
}

// --- B1: the words of a whole placeholder phrase are filler together ---

func TestBacklogPlaceholderPhraseWordsAreFiller(t *testing.T) {
	// dad0cc2d: "Неизвестный автор" in the nickname field of a real name.
	fields := []FieldValue{{FieldFirst, "Микал"}, {FieldLast, "Хем"}, {FieldNickname, "Неизвестный автор"}}
	out := mustAssemble(t, fields, personOf(role(0, RoleGiven), role(1, RoleFamily), role(2, RoleFiller), role(3, RoleFiller)))
	if r := out.Result; r.DisplayName != "Микал Хем" || r.Nickname != "" || r.SortName != "Хем, Микал" {
		t.Errorf("display/nickname/sort = %q/%q/%q", r.DisplayName, r.Nickname, r.SortName)
	}
	latin := []FieldValue{{FieldFirst, "John"}, {FieldLast, "Smith"}, {FieldNickname, "Unknown Author"}}
	mustAssemble(t, latin, personOf(role(0, RoleGiven), role(1, RoleFamily), role(2, RoleFiller), role(3, RoleFiller)))

	// The phrase admits its words only together: "автор" alone is no filler,
	// nor is it when the first word of the phrase keeps a name role.
	mustViolate(t, []FieldValue{{FieldFirst, "Иван"}, {FieldLast, "автор"}},
		personOf(role(0, RoleGiven), role(1, RoleFiller)), validatorV3)
	mustViolate(t, fields, personOf(role(0, RoleGiven), role(1, RoleFamily), role(2, RoleNickname), role(3, RoleFiller)),
		validatorV3)
}

// --- C: a person written only as a pen name ---

func TestBacklogPenNameOnlyPersonIsValid(t *testing.T) {
	cases := []struct {
		name    string
		fields  []FieldValue
		reply   *Reply
		display string
	}{
		{"d832ca8e Adrialice", []FieldValue{{FieldNickname, "Adrialice"}}, personOf(role(0, RoleNickname)), "Adrialice"},
		{"bda5bbd5 CruorCorvus", []FieldValue{{FieldNickname, "CruorCorvus"}}, personOf(role(0, RoleNickname)), "CruorCorvus"},
		{"41aef828 Anushka Jones", []FieldValue{{FieldNickname, "Anushka Jones"}},
			personOf(role(0, RoleNickname), role(1, RoleNickname)), "Anushka Jones"},
		{"5e030ebd Текстовый Процессор", []FieldValue{{FieldFirst, "Текстовый Процессор"}},
			personOf(role(0, RoleNickname), role(1, RoleNickname)), "Текстовый Процессор"},
	}
	for _, c := range cases {
		out := mustAssemble(t, c.fields, c.reply)
		r := out.Result
		if r.Kind != authornorm.KindPerson || r.DisplayName != c.display || r.Nickname != c.display || r.SortName != c.display {
			t.Errorf("%s: kind/display/nickname/sort = %q/%q/%q/%q", c.name, r.Kind, r.DisplayName, r.Nickname, r.SortName)
		}
		if c.reply.Order != OrderSingleName {
			t.Errorf("%s: order = %q, want single_name", c.name, c.reply.Order)
		}
	}
	// A person still needs a name: separators alone are none.
	mustViolate(t, []FieldValue{{FieldFirst, "--"}}, personOf(role(0, RoleSeparator)), validatorV5)
	// A pen name does not mix with the split name roles: next to a name it
	// is the person's nickname, and the name decides the order.
	rep := personOf(role(0, RoleNickname), role(1, RoleFamily))
	if rep.Order != OrderFamilyFirst {
		t.Errorf("order next to a family name = %q", rep.Order)
	}
}

// --- F: a lowercase name word anywhere in a component may be capitalized ---

func TestBacklogLowercaseNameWordAnywhereTakesACaseFix(t *testing.T) {
	// "Исидор севильский": the second word of the component is lowercase.
	out := mustAssemble(t, []FieldValue{{FieldFirst, "Исидор севильский"}},
		withFix(personOf(role(0, RoleGiven), role(1, RoleFamily)), CaseFix{I: 1, To: CaseFixCapitalize}))
	if out.Result.DisplayName != "Исидор Севильский" {
		t.Errorf("display = %q", out.Result.DisplayName)
	}
	// 452d0303: "Лама Сопа ринпоче" — the title word after the name.
	out = mustAssemble(t, []FieldValue{{FieldLast, "Лама Сопа ринпоче"}},
		withFix(personOf(role(0, RolePrefix), role(1, RoleSingleName), role(2, RoleSuffix)), CaseFix{I: 2, To: CaseFixCapitalize}))
	if out.Result.DisplayName != "Лама Сопа Ринпоче" {
		t.Errorf("display = %q", out.Result.DisplayName)
	}
	// A lowercase particle keeps its case: it is no name word.
	mustViolate(t, []FieldValue{{FieldFirst, "Шарль"}, {FieldLast, "де Голль"}},
		withFix(personOf(role(0, RoleGiven), role(1, RoleFamily), role(2, RoleFamily)), CaseFix{I: 1, To: CaseFixCapitalize}),
		validatorV4)
	// A word already capitalized has no defect.
	mustViolate(t, []FieldValue{{FieldFirst, "Исидор Севильский"}},
		withFix(personOf(role(0, RoleGiven), role(1, RoleFamily)), CaseFix{I: 1, To: CaseFixCapitalize}), validatorV4)
}

func TestBacklogCaseFixOnTheNicknameFieldIsTheCaseTiers(t *testing.T) {
	// 96a54d71: the pair capitalized the nickname "иеросхимонах". The
	// prompt asks for no case fix there; the validator leaves it to the tier
	// (the case tier, judged by the gate on its own), so such an answer never
	// counts against the model's invalid share.
	fields := []FieldValue{{FieldFirst, "Серафим"}, {FieldLast, "Барадель"}, {FieldNickname, "иеросхимонах"}}
	roles := []RoleAssignment{role(0, RoleGiven), role(1, RoleFamily), role(2, RoleNickname)}
	if out := mustAssemble(t, fields, withFix(personOf(roles...), CaseFix{I: 2, To: CaseFixCapitalize})); out.Tier != TierCase {
		t.Errorf("tier = %s, want case", out.Tier)
	}
	if out := mustAssemble(t, fields, personOf(roles...)); out.Tier != TierConfirm {
		t.Errorf("tier = %s, want confirm", out.Tier)
	}
}

// --- B2: closed label words inside a person are filler ---

func TestBacklogLabelWordsInsideAPersonAreFiller(t *testing.T) {
	cases := []struct {
		name    string
		fields  []FieldValue
		reply   *Reply
		display string
	}{
		{"fc2e6fa2 Сергей переводчик Смирнов", []FieldValue{{FieldFirst, "Сергей"}, {FieldMiddle, "переводчик"}, {FieldLast, "Смирнов"}},
			personOf(role(0, RoleGiven), role(1, RoleFiller), role(2, RoleFamily)), "Сергей Смирнов"},
		{"2c86b18e Леонтьев (сценарист)", []FieldValue{{FieldFirst, "Алексей"}, {FieldMiddle, "Николаевич"},
			{FieldLast, "Леонтьев (сценарист)"}},
			personOf(role(0, RoleGiven), role(1, RoleAdditional), role(2, RoleFamily), role(3, RoleSeparator),
				role(4, RoleFiller), role(5, RoleSeparator)), "Алексей Николаевич Леонтьев"},
		{"1e6aa1a4 Горкин А. П., гл. редактор", []FieldValue{{FieldLast, "Горкин А. П., гл. редактор"}},
			personOf(role(0, RoleFamily), role(1, RoleGiven), role(2, RoleAdditional), role(3, RoleSeparator),
				role(4, RoleFiller), role(5, RoleSeparator), role(6, RoleFiller)), "Горкин А. П."},
		{"f74eccc3 Под редакцией А. Г. Маньков", []FieldValue{{FieldFirst, "Под редакцией"}, {FieldLast, "А. Г. Маньков"}},
			personOf(role(0, RoleFiller), role(1, RoleFiller), role(2, RoleGiven), role(3, RoleAdditional), role(4, RoleFamily)),
			"А. Г. Маньков"},
		{"617ecdd7 Юрий МОРОЗ | e-mail", []FieldValue{{FieldFirst, "Юрий"}, {FieldMiddle, "МОРОЗ |"}, {FieldLast, "moroz@onego.ru"}},
			withFix(personOf(role(0, RoleGiven), role(1, RoleFamily), role(2, RoleSeparator), role(3, RoleFiller)),
				CaseFix{I: 1, To: CaseFixCapitalize}), "Юрий Мороз"},
		{"Ю. Томашевский Составитель", []FieldValue{{FieldFirst, "Ю."}, {FieldLast, "Томашевский"}, {FieldNickname, "Составитель"}},
			personOf(role(0, RoleGiven), role(1, RoleFamily), role(2, RoleFiller)), "Ю. Томашевский"},
		{"Translated by John Smith", []FieldValue{{FieldFirst, "Translated by John"}, {FieldLast, "Smith"}},
			personOf(role(0, RoleFiller), role(1, RoleFiller), role(2, RoleGiven), role(3, RoleFamily)), "John Smith"},
		{"Пётр Сидоров, отв. ред.", []FieldValue{{FieldFirst, "Пётр"}, {FieldLast, "Сидоров, отв. ред."}},
			personOf(role(0, RoleGiven), role(1, RoleFamily), role(2, RoleSeparator), role(3, RoleFiller),
				role(4, RoleSeparator), role(5, RoleFiller), role(6, RoleSeparator)), "Пётр Сидоров"},
		{"Анна Белова AKA", []FieldValue{{FieldFirst, "Анна"}, {FieldLast, "Белова"}, {FieldNickname, "AKA"}},
			personOf(role(0, RoleGiven), role(1, RoleFamily), role(2, RoleFiller)), "Анна Белова"},
		{"Олег Белов сост.", []FieldValue{{FieldFirst, "Олег"}, {FieldLast, "Белов сост."}},
			personOf(role(0, RoleGiven), role(1, RoleFamily), role(2, RoleFiller), role(3, RoleSeparator)), "Олег Белов"},
		{"Иван Петров, сост.", []FieldValue{{FieldFirst, "Иван"}, {FieldLast, "Петров, сост."}},
			personOf(role(0, RoleGiven), role(1, RoleFamily), role(2, RoleSeparator), role(3, RoleFiller), role(4, RoleSeparator)),
			"Иван Петров"},
	}
	for _, c := range cases {
		if got := mustAssemble(t, c.fields, c.reply).Result.DisplayName; got != c.display {
			t.Errorf("%s: display = %q, want %q", c.name, got, c.display)
		}
	}
	// The vocabulary is closed: an ordinary word is no label, and "под" or
	// "by" alone is no label either.
	mustViolate(t, []FieldValue{{FieldFirst, "Сергей"}, {FieldMiddle, "психолог"}, {FieldLast, "Смирнов"}},
		personOf(role(0, RoleGiven), role(1, RoleFiller), role(2, RoleFamily)), validatorV3)
	mustViolate(t, []FieldValue{{FieldFirst, "Пётр"}, {FieldMiddle, "под"}, {FieldLast, "Смирнов"}},
		personOf(role(0, RoleGiven), role(1, RoleFiller), role(2, RoleFamily)), validatorV3)
	mustViolate(t, []FieldValue{{FieldFirst, "John"}, {FieldMiddle, "by"}, {FieldLast, "Smith"}},
		personOf(role(0, RoleGiven), role(1, RoleFiller), role(2, RoleFamily)), validatorV3)
	// A word that is also a given name is left out of the labels ("Пер",
	// "Ed"), and so is a title.
	mustViolate(t, []FieldValue{{FieldFirst, "Пер"}, {FieldLast, "Петтерсон"}},
		personOf(role(0, RoleFiller), role(1, RoleFamily)), validatorV3)
	mustViolate(t, []FieldValue{{FieldFirst, "Ed"}, {FieldLast, "McBain"}},
		personOf(role(0, RoleFiller), role(1, RoleFamily)), validatorV3)
	mustViolate(t, []FieldValue{{FieldFirst, "Иоанн"}, {FieldLast, "митрополит"}},
		personOf(role(0, RoleSingleName), role(1, RoleFiller)), validatorV3)
	// A name with a dot is no address: "A.Smith" is an initial and a name.
	mustViolate(t, []FieldValue{{FieldFirst, "John"}, {FieldLast, "A.Smith"}},
		personOf(role(0, RoleGiven), role(1, RoleFiller)), validatorV3)
}

// --- D: a name in parentheses after the name is the person's nickname ---

func TestBacklogNameInParenthesesIsANickname(t *testing.T) {
	cases := []struct {
		name              string
		fields            []FieldValue
		reply             *Reply
		display, nickname string
	}{
		{"1d4bc864 Джеймс Дашнер (Дэшнер)", []FieldValue{{FieldFirst, "Джеймс"}, {FieldLast, "Дашнер (Дэшнер)"}},
			personOf(role(0, RoleGiven), role(1, RoleFamily), role(2, RoleSeparator), role(3, RoleNickname), role(4, RoleSeparator)),
			"Джеймс Дашнер", "Дэшнер"},
		{"952fc008 Леонид Владимирович Владимиров (Финкельштейн)", []FieldValue{{FieldFirst, "Леонид"},
			{FieldMiddle, "Владимирович Владимиров"}, {FieldLast, "(Финкельштейн)"}},
			personOf(role(0, RoleGiven), role(1, RoleAdditional), role(2, RoleFamily), role(3, RoleSeparator),
				role(4, RoleNickname), role(5, RoleSeparator)),
			"Леонид Владимирович Владимиров", "Финкельштейн"},
		{"4ec582ea Шон (Sean) МАКМАЛЛЕН (McMALLEN)", []FieldValue{{FieldFirst, "Шон (Sean)"}, {FieldLast, "МАКМАЛЛЕН (McMALLEN)"}},
			withFix(personOf(role(0, RoleGiven), role(1, RoleSeparator), role(2, RoleNickname), role(3, RoleSeparator),
				role(4, RoleFamily), role(5, RoleSeparator), role(6, RoleNickname), role(7, RoleSeparator)),
				CaseFix{I: 4, To: CaseFixCapitalize}),
			"Шон Макмаллен", "Sean McMALLEN"},
	}
	for _, c := range cases {
		r := mustAssemble(t, c.fields, c.reply).Result
		if r.DisplayName != c.display || r.Nickname != c.nickname {
			t.Errorf("%s: display/nickname = %q/%q, want %q/%q", c.name, r.DisplayName, r.Nickname, c.display, c.nickname)
		}
	}
	// A bracket around a kept name stays.
	r := mustAssemble(t, []FieldValue{{FieldFirst, "Иван"}, {FieldLast, "(Петров)"}},
		personOf(role(0, RoleGiven), role(1, RoleSeparator), role(2, RoleFamily), role(3, RoleSeparator))).Result
	if r.DisplayName != "Иван (Петров)" {
		t.Errorf("display = %q", r.DisplayName)
	}
}

// --- E: a title after or inside the name is a suffix ---

func TestBacklogTitleAfterTheGivenNameIsASuffix(t *testing.T) {
	cases := []struct {
		name   string
		fields []FieldValue
		reply  *Reply
		suffix string
	}{
		{"32f6e370 Иларион Игумен Алфеев", []FieldValue{{FieldFirst, "Иларион"}, {FieldMiddle, "Игумен"}, {FieldLast, "Алфеев"}},
			personOf(role(0, RoleGiven), role(1, RoleSuffix), role(2, RoleFamily)), "Игумен"},
		{"adcec21b Иоанн Святитель Златоуст", []FieldValue{{FieldFirst, "Иоанн"}, {FieldMiddle, "Святитель"}, {FieldLast, "Златоуст"}},
			personOf(role(0, RoleGiven), role(1, RoleSuffix), role(2, RoleFamily)), "Святитель"},
		{"309d3ca3 Фалассий авва", []FieldValue{{FieldFirst, "Фалассий"}, {FieldLast, "авва"}},
			personOf(role(0, RoleSingleName), role(1, RoleSuffix)), "авва"},
		{"Вениамин, еп.", []FieldValue{{FieldFirst, "Вениамин, еп."}},
			personOf(role(0, RoleSingleName), role(1, RoleSeparator), role(2, RoleSuffix), role(3, RoleSeparator)), "еп"},
	}
	for _, c := range cases {
		r := mustAssemble(t, c.fields, c.reply).Result
		if r.Suffix != c.suffix {
			t.Errorf("%s: suffix = %q, want %q", c.name, r.Suffix, c.suffix)
		}
	}
	// A suffix never splits the family name, and never comes before the name.
	mustViolate(t, []FieldValue{{FieldFirst, "Иван"}, {FieldLast, "Гарсия младший Маркес"}},
		personOf(role(0, RoleGiven), role(1, RoleFamily), role(2, RoleSuffix), role(3, RoleFamily)), validatorV5)
	mustViolate(t, []FieldValue{{FieldFirst, "младший Иван"}},
		personOf(role(0, RoleSuffix), role(1, RoleGiven)), validatorV5)
	// A title's place is the answer, not its label: a parsed reply that calls
	// the title after the given name a prefix reads as the suffix, and agrees
	// with the reply that says suffix.
	in := inputOf(t, FieldValue{FieldFirst, "Иоанн"}, FieldValue{FieldMiddle, "Святитель"}, FieldValue{FieldLast, "Златоуст"})
	asPrefix, err := ParseReply([]byte(`{"form":"person","roles":[{"i":0,"role":"given"},{"i":1,"role":"prefix"},` +
		`{"i":2,"role":"family"}],"case_fix":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if verr := asPrefix.Validate(&in); verr != nil {
		t.Fatalf("title after the given name labeled prefix: %v", verr)
	}
	if !Agree(&asPrefix, personOf(role(0, RoleGiven), role(1, RoleSuffix), role(2, RoleFamily))) {
		t.Error("prefix and suffix on the same title disagree")
	}
	// A title before the name reads as the prefix, whatever it was called.
	before, err := ParseReply([]byte(`{"form":"person","roles":[{"i":0,"role":"suffix"},{"i":1,"role":"given"}],"case_fix":[]}`))
	if err != nil || before.Roles[0].Role != RolePrefix {
		t.Errorf("title before the name = %+v, %v", before.Roles, err)
	}
	// The grammar on a reply built by hand is unchanged: a prefix after the
	// first name token is no person.
	mustViolate(t, []FieldValue{{FieldFirst, "Иоанн"}, {FieldMiddle, "Святитель"}, {FieldLast, "Златоуст"}},
		personOf(role(0, RoleGiven), role(1, RolePrefix), role(2, RoleFamily)), validatorV5)
}

// --- Particles under V8: a leading lowercase particle of the family name
// sorts at the end, as the particle role did ---

func TestBacklogFamilyParticleSortsLikeTheParticleRole(t *testing.T) {
	r := mustAssemble(t, []FieldValue{{FieldFirst, "Мигель"}, {FieldLast, "де Сервантес Сааведра"}},
		personOf(role(0, RoleGiven), role(1, RoleFamily), role(2, RoleFamily), role(3, RoleFamily))).Result
	if r.FamilyName != "де Сервантес Сааведра" || r.SortName != "Сервантес Сааведра, Мигель де" {
		t.Errorf("family/sort = %q/%q", r.FamilyName, r.SortName)
	}
	// b9a0f175: a particle inside the family name stays where it is.
	r = mustAssemble(t, []FieldValue{{FieldFirst, "Роже"}, {FieldMiddle, "Мартен"}, {FieldLast, "дю Гар"}},
		personOf(role(0, RoleGiven), role(1, RoleFamily), role(2, RoleFamily), role(3, RoleFamily))).Result
	if r.SortName != "Мартен дю Гар, Роже" {
		t.Errorf("sort = %q", r.SortName)
	}
	// A capitalized particle stays with the family.
	r = mustAssemble(t, []FieldValue{{FieldFirst, "Урсула"}, {FieldLast, "Ле Гуин"}},
		personOf(role(0, RoleGiven), role(1, RoleFamily), role(2, RoleFamily))).Result
	if r.SortName != "Ле Гуин, Урсула" {
		t.Errorf("sort = %q", r.SortName)
	}
}

// The resolve applies an agreed answer from its stored canonical form. A
// non-person canonical form carries no roles, and must still assemble.
func TestBacklogStoredNonPersonAnswerAssembles(t *testing.T) {
	fields := []FieldValue{{FieldFirst, "Коллектив"}, {FieldLast, "авторов"}}
	agreed := formReply(FormCollective, role(0, RoleFiller), role(1, RoleSingleName))
	stored, err := ParseStoredReply(agreed.CanonicalBytes())
	if err != nil {
		t.Fatal(err)
	}
	if r := mustAssemble(t, fields, &stored).Result; r.DisplayName != "Коллектив авторов" || r.Kind != authornorm.KindCollective {
		t.Errorf("display/kind = %q/%q", r.DisplayName, r.Kind)
	}
	// A person's stored answer still needs every token.
	mustViolate(t, fields, formReply(FormPerson, role(0, RoleGiven)), validatorV2)
}
