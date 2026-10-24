package llmres

import "testing"

// The stage-1 shapes of eval run 24 (report R3): answers all four
// participants gave alike that the reply contract still had no valid
// encoding for. Every case is a real source shape, named by the prefix of its
// fingerprint.

// --- P1: a family-first name whose particle was cut off to the end ---

func TestRun24TrailingFamilyParticleIsValid(t *testing.T) {
	// FB2 wrote "Дю Морье, Дафна" as first=Морье, middle=Дафна, last=Дю: the
	// particle closes the source, the root of the family name opens it.
	out := mustAssemble(t, []FieldValue{{FieldFirst, "Морье"}, {FieldMiddle, "Дафна"}, {FieldLast, "Дю"}},
		personOf(role(0, RoleFamily), role(1, RoleGiven), role(2, RoleFamily)))
	r := out.Result
	if r.FamilyName != "Дю Морье" || r.GivenName != "Дафна" {
		t.Errorf("fields = %q/%q", r.GivenName, r.FamilyName)
	}
	if r.DisplayName != "Дю Морье Дафна" {
		t.Errorf("display = %q", r.DisplayName)
	}
	if r.SortName != "Дю Морье, Дафна" {
		t.Errorf("sort = %q", r.SortName)
	}
	if out.Tier != TierRestructure {
		t.Errorf("tier = %q", out.Tier)
	}
}

func TestRun24TrailingLowercaseParticleSortsToTheTail(t *testing.T) {
	// fb796c80 "Мюссе Альфред Де", written lowercase: the particle leads the
	// family name and, lowercase, goes to the sort tail as in "Сервантес
	// Сааведра, Мигель де".
	out := mustAssemble(t, []FieldValue{{FieldFirst, "Мюссе"}, {FieldMiddle, "Альфред"}, {FieldLast, "де"}},
		personOf(role(0, RoleFamily), role(1, RoleGiven), role(2, RoleFamily)))
	r := out.Result
	if r.FamilyName != "де Мюссе" || r.DisplayName != "де Мюссе Альфред" || r.SortName != "Мюссе, Альфред де" {
		t.Errorf("family/display/sort = %q/%q/%q", r.FamilyName, r.DisplayName, r.SortName)
	}
}

func TestRun24SplitFamilyStaysInvalidWithoutATrailingParticle(t *testing.T) {
	cases := []struct {
		name   string
		fields []FieldValue
		reply  *Reply
	}{
		{"a name word split from the family", []FieldValue{{FieldFirst, "Иванов"}, {FieldMiddle, "Иван"}, {FieldLast, "Петров"}},
			personOf(role(0, RoleFamily), role(1, RoleGiven), role(2, RoleFamily))},
		{"a name word after the particle", []FieldValue{{FieldFirst, "Мартен"}, {FieldMiddle, "Роже"}, {FieldLast, "дю Гар"}},
			personOf(role(0, RoleFamily), role(1, RoleGiven), role(2, RoleFamily), role(3, RoleFamily))},
		{"a name token after the trailing particle", []FieldValue{{FieldFirst, "Морье"}, {FieldMiddle, "Дафна"},
			{FieldLast, "Дю Петровна"}},
			personOf(role(0, RoleFamily), role(1, RoleGiven), role(2, RoleFamily), role(3, RoleAdditional))},
		{"the particle opens the source", []FieldValue{{FieldFirst, "Дю"}, {FieldMiddle, "Дафна"}, {FieldLast, "Морье"}},
			personOf(role(0, RoleFamily), role(1, RoleGiven), role(2, RoleFamily))},
		{"a given-first name", []FieldValue{{FieldFirst, "Дафна"}, {FieldMiddle, "Морье"}, {FieldLast, "Иван Дю"}},
			personOf(role(0, RoleGiven), role(1, RoleFamily), role(2, RoleAdditional), role(3, RoleFamily))},
		{"a nickname between root and particle", []FieldValue{{FieldFirst, "Морье"}, {FieldMiddle, "Дафна"},
			{FieldLast, "Дю"}},
			personOf(role(0, RoleFamily), role(1, RoleNickname), role(2, RoleFamily))},
		{"a separator inside the trailing particles", []FieldValue{{FieldFirst, "Морье"}, {FieldMiddle, "Дафна"},
			{FieldLast, "Дю - Ла"}},
			personOf(role(0, RoleFamily), role(1, RoleGiven), role(2, RoleFamily), role(3, RoleSeparator), role(4, RoleFamily))},
		// Review R3-r1 finding 1: a glued word is a whole surname, not a
		// particle cut off from another root.
		{"a glued surname after the given name", []FieldValue{{FieldFirst, "Баатц"}, {FieldMiddle, "Шарль"},
			{FieldLast, "д'Артаньян"}},
			personOf(role(0, RoleFamily), role(1, RoleGiven), role(2, RoleFamily))},
		// Review R3-r1: without a given name between them nothing names the
		// person's given name.
		{"only an additional name between root and particle", []FieldValue{{FieldFirst, "Морье"}, {FieldMiddle, "Дафна"},
			{FieldLast, "Дю"}},
			personOf(role(0, RoleFamily), role(1, RoleAdditional), role(2, RoleFamily))},
		{"a nickname beside the given name between root and particle", []FieldValue{{FieldFirst, "Морье"},
			{FieldMiddle, "Дафна Кот"}, {FieldLast, "Дю"}},
			personOf(role(0, RoleFamily), role(1, RoleGiven), role(2, RoleNickname), role(3, RoleFamily))},
		{"a family of particles alone", []FieldValue{{FieldFirst, "Ле"}, {FieldMiddle, "Дафна"}, {FieldLast, "Дю"}},
			personOf(role(0, RoleFamily), role(1, RoleGiven), role(2, RoleFamily))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { mustViolate(t, c.fields, c.reply, validatorV5) })
	}
}

func TestRun24ParticleNextToTheRootStaysWhereWritten(t *testing.T) {
	// Without a given name between them the source wrote the family in its
	// own order; nothing is detached or moved.
	out := mustAssemble(t, []FieldValue{{FieldFirst, "Мопассан"}, {FieldLast, "Де"}},
		personOf(role(0, RoleFamily), role(1, RoleFamily)))
	if out.Result.FamilyName != "Мопассан Де" || out.Result.DisplayName != "Мопассан Де" {
		t.Errorf("family/display = %q/%q", out.Result.FamilyName, out.Result.DisplayName)
	}
}

// --- P2: a particle word that is a mononym or a second given name ---

func TestRun24ParticleWordAsASingleName(t *testing.T) {
	// 9bdc1920 "Аль-Фараздак", 5c6595d8 "Абу-ль-Атахия": a laqab with a glued
	// article is a mononym; single_name keeps the token in a name component.
	for _, text := range []string{"Аль-Фараздак", "Абу-ль-Атахия"} {
		out := mustAssemble(t, []FieldValue{{FieldLast, text}}, personOf(role(0, RoleSingleName)))
		if out.Result.DisplayName != text || out.Result.SortName != text {
			t.Errorf("%s: display/sort = %q/%q", text, out.Result.DisplayName, out.Result.SortName)
		}
	}
}

func TestRun24FirstFieldParticleWordKeepsGiven(t *testing.T) {
	// 8f139273 "Абу Мухаммед аль-Касим / аль-Харири", 8a54bb2a "Джей Ди / Кирк":
	// a first-field particle word keeps its field's role, given, also after
	// another given name; additional is refused as in v2 (V8 cannot tell it
	// from "Винсент Ван / Гог", where the particle belongs to the family).
	out := mustAssemble(t, []FieldValue{{FieldFirst, "Абу Мухаммед аль-Касим"}, {FieldLast, "аль-Харири"}},
		personOf(role(0, RoleGiven), role(1, RoleAdditional), role(2, RoleGiven), role(3, RoleFamily)))
	if out.Result.GivenName != "Абу аль-Касим" || out.Result.FamilyName != "аль-Харири" {
		t.Errorf("given/family = %q/%q", out.Result.GivenName, out.Result.FamilyName)
	}
	mustAssemble(t, []FieldValue{{FieldFirst, "Джей Ди"}, {FieldLast, "Кирк"}},
		personOf(role(0, RoleGiven), role(1, RoleGiven), role(2, RoleFamily)))
}

func TestRun24ParticleMovesStayInvalid(t *testing.T) {
	cases := []struct {
		name   string
		fields []FieldValue
		reply  *Reply
	}{
		{"a middle-field particle as given", []FieldValue{{FieldFirst, "И"}, {FieldMiddle, "де"}, {FieldLast, "Рок-Казбеков"}},
			personOf(role(0, RoleGiven), role(1, RoleGiven), role(2, RoleFamily))},
		{"a last-field particle as given", []FieldValue{{FieldFirst, "Мигель"}, {FieldLast, "де Сервантес"}},
			personOf(role(0, RoleGiven), role(1, RoleGiven), role(2, RoleFamily))},
		{"a first-field particle as a title", []FieldValue{{FieldFirst, "дас"}, {FieldLast, "Яшошадулал"}},
			personOf(role(0, RolePrefix), role(1, RoleGiven))},
		// Review R3-r1 finding 2: "Ван" of "Винсент Ван Гог" belongs to the
		// family; a first-field particle closing its field is never additional,
		// so "Джей Ди Кирк" (8a54bb2a) can't be told from it and stays "given".
		{"a family particle closing the first field as additional", []FieldValue{{FieldFirst, "Винсент Ван"}, {FieldLast, "Гог"}},
			personOf(role(0, RoleGiven), role(1, RoleAdditional), role(2, RoleFamily))},
		{"8a54bb2a a particle closing the first field as additional", []FieldValue{{FieldFirst, "Джей Ди"}, {FieldLast, "Кирк"}},
			personOf(role(0, RoleGiven), role(1, RoleAdditional), role(2, RoleFamily))},
		{"a non-nasab particle inside the first field as additional", []FieldValue{{FieldFirst, "Мигель де Сервантес"}},
			personOf(role(0, RoleGiven), role(1, RoleAdditional), role(2, RoleFamily))},
		// Review R3-r1 finding 3: a whole split name is no mononym.
		{"a split name as single_name", []FieldValue{{FieldFirst, "Мигель"}, {FieldLast, "де Сервантес"}},
			personOf(role(0, RoleSingleName), role(1, RoleSingleName), role(2, RoleSingleName))},
		{"a glued particle word as one of several single_name tokens", []FieldValue{{FieldFirst, "Абу"},
			{FieldLast, "аль-Фараздак"}},
			personOf(role(0, RoleSingleName), role(1, RoleSingleName))},
		{"8f139273 a glued first-field word as additional", []FieldValue{{FieldFirst, "Абу Мухаммед аль-Касим"},
			{FieldLast, "аль-Харири"}},
			personOf(role(0, RoleGiven), role(1, RoleAdditional), role(2, RoleAdditional), role(3, RoleFamily))},
		{"94d1c233 a nasab of a first-field chain as additional", []FieldValue{{FieldFirst, "Абу Бакр Мухаммад ибн Абд аль-Азиз"},
			{FieldLast, "Ибн аль-Кутыйя"}},
			personOf(role(0, RoleGiven), role(1, RoleAdditional), role(2, RoleAdditional), role(3, RoleAdditional),
				role(4, RoleAdditional), role(5, RoleAdditional), role(6, RoleFamily), role(7, RoleFamily))},
		// Review R3-r2 findings 1 and 2: no word of the first field stands in
		// for a nasab chain.
		{"a glued surname after a given name Ben", []FieldValue{{FieldFirst, "Ben d'Artagnan"}, {FieldLast, "Smith"}},
			personOf(role(0, RoleGiven), role(1, RoleAdditional), role(2, RoleFamily))},
		{"a glued first-field word after an ibn of another field", []FieldValue{{FieldLast, "ibn Smith"},
			{FieldFirst, "John d'Artagnan"}},
			personOf(role(0, RoleFamily), role(1, RoleFamily), role(2, RoleGiven), role(3, RoleAdditional))},
		{"an ibn before a separator", []FieldValue{{FieldFirst, "John ibn -"}, {FieldLast, "Smith"}},
			personOf(role(0, RoleGiven), role(1, RoleAdditional), role(2, RoleSeparator), role(3, RoleFamily))},
		{"an ibn before a family root", []FieldValue{{FieldFirst, "John ibn Smith"}},
			personOf(role(0, RoleGiven), role(1, RoleAdditional), role(2, RoleFamily))},
		{"a glued word of the first field as additional without a nasab", []FieldValue{{FieldFirst, "Абу аль-Касим"},
			{FieldLast, "аль-Харири"}},
			personOf(role(0, RoleGiven), role(1, RoleAdditional), role(2, RoleFamily))},
		{"a standalone particle as the only name next to a nickname", []FieldValue{{FieldLast, "Ле"}, {FieldNickname, "Кот"}},
			personOf(role(0, RoleSingleName), role(1, RoleNickname))},
		{"a glued last-field word as additional after a nasab", []FieldValue{{FieldFirst, "Мухаммад ибн Абд"},
			{FieldLast, "аль-Азиз"}},
			personOf(role(0, RoleGiven), role(1, RoleAdditional), role(2, RoleAdditional), role(3, RoleAdditional))},
		{"a particle as a nickname outside the nickname field", []FieldValue{{FieldFirst, "Габриак"}, {FieldMiddle, "Черубина"},
			{FieldLast, "Де"}},
			personOf(role(0, RoleNickname), role(1, RoleNickname), role(2, RoleNickname))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { mustViolate(t, c.fields, c.reply, validatorV8) })
	}
}

// --- P3: the Bulgarian and Ukrainian spellings of "unknown author" ---

func TestRun24RegionalPlaceholderNicknameIsFiller(t *testing.T) {
	cases := []struct {
		name   string
		fields []FieldValue
	}{
		{"3c6e4d03 Неизвестен автор", []FieldValue{{FieldFirst, "Нийл"}, {FieldLast, "Геймън"}, {FieldNickname, "Неизвестен автор"}}},
		{"79062c54 Невідомий автор", []FieldValue{{FieldFirst, "Богдан"}, {FieldLast, "Коломыйчук"},
			{FieldNickname, "Невідомий автор"}}},
		{"Автор невідомий", []FieldValue{{FieldFirst, "Богдан"}, {FieldLast, "Коломыйчук"}, {FieldNickname, "Автор невідомий"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := mustAssemble(t, c.fields,
				personOf(role(0, RoleGiven), role(1, RoleFamily), role(2, RoleFiller), role(3, RoleFiller)))
			if out.Result.Nickname != "" || out.Result.FamilyName == "" {
				t.Errorf("nickname/family = %q/%q", out.Result.Nickname, out.Result.FamilyName)
			}
		})
	}
	// A lone "Невідомий" is a placeholder word, like "Неизвестный".
	mustAssemble(t, []FieldValue{{FieldFirst, "Богдан"}, {FieldLast, "Коломыйчук"}, {FieldNickname, "Невідомий"}},
		personOf(role(0, RoleGiven), role(1, RoleFamily), role(2, RoleFiller)))
}

func TestRun24RegionalPlaceholderWordsAreFillerOnlyTogether(t *testing.T) {
	// "автор" is filler only inside the whole phrase.
	mustViolate(t, []FieldValue{{FieldFirst, "Богдан"}, {FieldLast, "Коломыйчук"}, {FieldNickname, "Невідомий автор"}},
		personOf(role(0, RoleGiven), role(1, RoleFamily), role(2, RoleNickname), role(3, RoleFiller)), validatorV3)
	mustViolate(t, []FieldValue{{FieldFirst, "Нийл"}, {FieldLast, "Геймън"}, {FieldNickname, "Неизвестен автор"}},
		personOf(role(0, RoleGiven), role(1, RoleFamily), role(2, RoleNickname), role(3, RoleFiller)), validatorV3)
}
