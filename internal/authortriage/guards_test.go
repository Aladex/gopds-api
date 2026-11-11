package authortriage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTriageV2DropsSameNameAndParticleOnly(t *testing.T) {
	sameName := candidate("single_field", handle("Андреев Михаил"))
	sameName.EntityRule, sameName.SameName = "same_name", []Name{person("Михаил", "Андреев")}
	particle := candidate("particle", person("Ги", "де Мопассан"))
	particle.Source = source("particle", []string{"particle"}, "Ги", nil, "де Мопассан")

	for title, c := range map[string]struct {
		c    *Candidate
		step Step
	}{
		"same name":     {c: sameName, step: StepSameName},
		"particle only": {c: particle, step: StepParticleOnly},
	} {
		t.Run(title, func(t *testing.T) {
			assert.Equal(t, c.step, DecideV1(c.c, testEnv()).Step, "triage-v1 decides it")
			d := Decide(c.c, testEnv())
			assert.Equal(t, Decision{}, d, "triage-v2 leaves it, with no refusal")
		})
	}
	assert.Equal(t, []Step{StepFourModel, StepJudgesOnly, StepRules, StepPlaceholderOrg, StepAsNamedCollective,
		StepKeepHandle}, ActiveSteps())
	assert.Equal(t, "triage-v2", RuleVersion)
}

// stored is an item whose four models agree on after.
//
//nolint:gocritic // a fixture takes its names by value, as the table rows spell them
func stored(proposal, after Name) *Candidate {
	c := candidate("unusual_characters", proposal)
	c.Answers = fourModel(&after, "person")
	return c
}

func TestStoredAnswerGuards(t *testing.T) {
	for title, c := range map[string]struct {
		c       *Candidate
		refusal Refusal
		after   Name
		fixes   []string
	}{
		"Latin look-alikes inside a Cyrillic word": {
			c:     stored(person("Аpтуp", "Хинеман"), person("Аpтуp", "Хинеман")),
			after: person("Артур", "Хинеман"), fixes: []string{FixLookalike},
		},
		"a whole Latin look-alike word in a Cyrillic name": {
			c:     stored(person("Capa", "Сил"), person("Capa", "Сил")),
			after: person("Сара", "Сил"), fixes: []string{FixLookalike},
		},
		"a Cyrillic look-alike inside a Latin word": {
			c: stored(person("Kārlis", "Маrkss"), person("Kārlis", "Маrkss")),
			after: Name{Given: "Kārlis", Family: "Markss", Display: "Kārlis Markss", Sort: "Markss, Kārlis",
				Kind: KindPerson},
			fixes: []string{FixLookalike},
		},
		"a real Latin word stays beside a Cyrillic one": {
			c:     stored(person("Игорь", "Numus"), person("Игорь", "Numus")),
			after: person("Игорь", "Numus"),
		},
		"a mixed word with a letter of no look-alike stays": {
			c:     stored(person("Иван", "Стогоff"), person("Иван", "Стогоff")),
			after: person("Иван", "Стогоff"),
		},
		"Cyrillic initials of a Latin name": {
			c: stored(Name{Display: "С. А. Ekshut", Kind: KindPerson}, Name{Given: "С.", Additional: "А.",
				Family: "Ekshut", Display: "С. А. Ekshut", Sort: "Ekshut, С. А.", Kind: KindPerson}),
			after: Name{Given: "C.", Additional: "A.", Family: "Ekshut", Display: "C. A. Ekshut",
				Sort: "Ekshut, C. A.", Kind: KindPerson},
			fixes: []string{FixLookalike},
		},
		"a stray dot after a full given name": {
			c: stored(Name{Given: "Чарлз", Additional: ".", Family: "Мессенджер", Display: "Чарлз . Мессенджер",
				Kind: KindPerson}, Name{Given: "Чарлз", Family: "Мессенджер", Display: "Чарлз. Мессенджер",
				Sort: "Мессенджер, Чарлз", Kind: KindPerson}),
			after: person("Чарлз", "Мессенджер"), fixes: []string{FixPunctuation},
		},
		"a stray comma in a part": {
			c: stored(person("Роберт", "Блох"), Name{Given: "Роберт,", Family: "Блох", Display: "Роберт, Блох",
				Sort: "Блох, Роберт,", Kind: KindPerson}),
			after: person("Роберт", "Блох"), fixes: []string{FixPunctuation},
		},
		"initials, a suffix separator and an abbreviated title keep their marks": {
			c: stored(Name{Display: "проф. А. Б. Иванов, мл.", Kind: KindPerson},
				Name{Given: "А.", Additional: "Б.", Family: "Иванов", Prefix: "проф", Suffix: "мл.",
					Display: "проф. А. Б. Иванов, мл.", Sort: "Иванов, А. Б.", Kind: KindPerson}),
			after: Name{Given: "А.", Additional: "Б.", Family: "Иванов", Prefix: "проф", Suffix: "мл.",
				Display: "проф. А. Б. Иванов, мл.", Sort: "Иванов, А. Б.", Kind: KindPerson},
		},
		"an abbreviated title and a glued suffix keep their dots": {
			c: stored(Name{Display: "архиеп. Уильям Онкен-мл.", Kind: KindPerson},
				Name{Given: "Уильям", Family: "Онкен-мл.", Prefix: "архиеп", Display: "архиеп. Уильям Онкен-мл.",
					Sort: "Онкен-мл., Уильям", Kind: KindPerson}),
			after: Name{Given: "Уильям", Family: "Онкен-мл.", Prefix: "архиеп", Display: "архиеп. Уильям Онкен-мл.",
				Sort: "Онкен-мл., Уильям", Kind: KindPerson},
		},
		"a collective's punctuation is its title's": {
			c:     stored(Name{Display: "Журнал «Полдень, XXI век»", Kind: "person"}, *assembled("Журнал «Полдень, XXI век»", "collective")),
			after: *assembled("Журнал «Полдень, XXI век»", "collective"),
		},
		"a word lost to the nickname": {
			c: stored(Name{Given: "Игорь", Family: "Numus", Display: "Игорь Numus", Kind: KindPerson},
				Name{Given: "Игорь", Nickname: "Numus", Display: "Игорь", Sort: "Игорь", Kind: KindPerson}),
			refusal: RefusedLostToken,
		},
		"a word moved to a role prefix": {
			c: stored(Name{Given: "Лука", Family: "Войно-Ясенецкий", Display: "святитель Лука Войно-Ясенецкий",
				Kind: KindPerson}, Name{Given: "Лука", Family: "Войно-Ясенецкий", Prefix: "святитель",
				Display: "Лука Войно-Ясенецкий", Sort: "Войно-Ясенецкий, Лука", Kind: KindPerson}),
			after: Name{Given: "Лука", Family: "Войно-Ясенецкий", Prefix: "святитель",
				Display: "Лука Войно-Ясенецкий", Sort: "Войно-Ясенецкий, Лука", Kind: KindPerson},
		},
		"a Latin «i» read as «и» is no lost word": {
			c:     stored(person("Эдуард", "Акулiн"), person("Эдуард", "Акулин")),
			after: person("Эдуард", "Акулин"),
		},
		"case, order and a library comma are no lost word": {
			c:     stored(Name{Display: "ФОЛКНЕР, Уильям", Kind: KindPerson}, person("Уильям", "Фолкнер")),
			after: person("Уильям", "Фолкнер"),
		},
		"a role word kept as the given name": {
			c: stored(Name{Display: "художник Ирина Зюзина", Kind: KindPerson},
				Name{Given: "Художник", Additional: "Ирина", Family: "Зюзина", Display: "Художник Ирина Зюзина",
					Sort: "Зюзина, Художник Ирина", Kind: KindPerson}),
			refusal: RefusedRoleAsName,
		},
		"a clerical title kept as the given name": {
			c: stored(Name{Display: "Иеромонах Пантелеимон", Kind: KindPerson},
				person("Иеромонах", "Пантелеимон")),
			refusal: RefusedRoleAsName,
		},
		"a family name that is a clerical title is a name": {
			c:     stored(Name{Display: "Евгений Монах", Kind: KindPerson}, person("Евгений", "Монах")),
			after: person("Евгений", "Монах"),
		},
		"a prefix in capitals": {
			c: stored(Name{Display: "ИЕРОМОНАХ Пантелеимон", Kind: KindPerson},
				Name{Given: "Пантелеимон", Prefix: "ИЕРОМОНАХ", Display: "ИЕРОМОНАХ Пантелеимон",
					Sort: "Пантелеимон", Kind: KindPerson}),
			refusal: RefusedRoleAsName,
		},
	} {
		t.Run(title, func(t *testing.T) {
			d := Decide(c.c, testEnv())
			if c.refusal != "" {
				assert.Equal(t, Step(0), d.Step)
				assert.Equal(t, c.refusal, d.Refusal)
				assert.Equal(t, StepFourModel, d.Evidence.RefusedStep)
				assert.Equal(t, c.refusal, d.Evidence.Refusal)
				assert.Equal(t, int64(14), d.Evidence.RunID, "the refused answer's evidence is kept")
				return
			}
			require.Equal(t, StepFourModel, d.Step)
			assert.Empty(t, d.Refusal)
			assert.Equal(t, c.after, d.After)
			assert.Equal(t, c.fixes, d.Evidence.Fixes)
		})
	}
}

// The judges-only step passes the same guards.
func TestJudgesOnlyAnswerIsGuarded(t *testing.T) {
	c := candidate("unusual_characters", Name{Given: "Игорь", Family: "Numus", Display: "Игорь Numus",
		Kind: KindPerson})
	c.Answers = judgesOnly(&Name{Given: "Игорь", Nickname: "Numus", Display: "Игорь", Sort: "Игорь",
		Kind: KindPerson}, "person")
	d := Decide(c, testEnv())
	assert.Equal(t, RefusedLostToken, d.Refusal)
	assert.Equal(t, StepJudgesOnly, d.Evidence.RefusedStep)

	c = candidate("mixed_script", person("Аpтуp", "Хинеман"))
	c.Answers = judgesOnly(ptrName(person("Аpтуp", "Хинеман")), "person")
	d = Decide(c, testEnv())
	assert.Equal(t, StepJudgesOnly, d.Step)
	assert.Equal(t, person("Артур", "Хинеман"), d.After)
}

func TestCollectiveGuards(t *testing.T) {
	named := func(display string) *Candidate {
		c := candidate("collective_candidate", Name{Display: display, Kind: KindCollective})
		c.EntityRule = "as_named"
		return c
	}
	organization := func(display string) *Candidate {
		return candidate("unusual_characters", Name{Display: display, Kind: KindPerson})
	}
	for title, c := range map[string]struct {
		c       *Candidate
		step    Step
		refusal Refusal
	}{
		"a list of genres":  {c: named("Мифы и легенды"), step: StepAsNamedCollective, refusal: RefusedGenreList},
		"genres in English": {c: named("Tales and Legends"), step: StepAsNamedCollective, refusal: RefusedGenreList},
		"a kind of book":    {c: named("[ сборник ]"), step: StepAsNamedCollective, refusal: RefusedGenreList},
		"a placeholder with more after it": {c: named("Неизвестный автор - Сказки"), step: StepAsNamedCollective,
			refusal: RefusedPlaceholderTitle},
		"one person with two titles": {c: named("Давид Царь и Пророк"), step: StepAsNamedCollective, refusal: RefusedOnePersonTitles},
		"one person of two countries": {c: named("Христофор Греческий и Датский"), step: StepAsNamedCollective,
			refusal: RefusedOnePersonTitles},
		"a tale title":                    {c: named("Лиса и журавль"), step: StepAsNamedCollective, refusal: RefusedTitleShape},
		"a named pair stays a collective": {c: named("Анна и Сергей Литвиновы"), step: StepAsNamedCollective},
		"a pair of family names":          {c: named("Фрай и Лори"), step: StepAsNamedCollective},
		"brothers by name":                {c: named("Якоб и Вильгельм Гримм братья"), step: StepAsNamedCollective},
		"and others":                      {c: named("Ян МЕЛИН и др."), step: StepAsNamedCollective},
		"and others in full":              {c: named("Л. Леонов и другие"), step: StepAsNamedCollective},
		"an organization with «и» in it":  {c: named("Коллектив авторов -- Биографии и мемуары"), step: StepAsNamedCollective},
		"an editor read as an organization": {c: organization("Shane McKenzie, ed."), step: StepPlaceholderOrg,
			refusal: RefusedRoleAsName},
		"genres read as an organization": {c: organization("Epic, legends and tales"), step: StepPlaceholderOrg,
			refusal: RefusedGenreList},
		"a magazine named for genres": {c: organization("Журнал «Мифы и легенды»"), step: StepPlaceholderOrg},
		"a magazine":                  {c: organization("Журнал «Искатель»"), step: StepPlaceholderOrg},
		"a placeholder phrase is unknown, unguarded": {c: organization("Автор неизвестен"),
			step: StepPlaceholderOrg},
	} {
		t.Run(title, func(t *testing.T) {
			d := Decide(c.c, testEnv())
			if c.refusal != "" {
				assert.Equal(t, Step(0), d.Step)
				assert.Equal(t, c.refusal, d.Refusal)
				assert.Equal(t, c.step, d.Evidence.RefusedStep)
				return
			}
			assert.Equal(t, c.step, d.Step)
			assert.Empty(t, d.Refusal)
		})
	}
}

func TestKeepHandleGuards(t *testing.T) {
	mononym := func(word string, credits int) *Candidate {
		c := candidate("mononym", handle(word))
		c.Source = source("mononym", []string{"single_token"}, word, nil, "")
		c.Credits = credits
		return c
	}
	for title, c := range map[string]struct {
		c       *Candidate
		refusal Refusal
	}{
		"a magazine":                    {c: mononym("Компьютерра", 3), refusal: RefusedListedName},
		"a church":                      {c: mononym("РПЦ", 3), refusal: RefusedListedName},
		"the Bible":                     {c: mononym("Bībele", 3), refusal: RefusedListedName},
		"a placeholder word":            {c: mononym("Пользователь", 3), refusal: RefusedListedName},
		"five capitals":                 {c: mononym("ИГСОА", 3), refusal: RefusedShortCaps},
		"two Latin capitals":            {c: mononym("VS", 1), refusal: RefusedShortCaps},
		"six capitals are a handle":     {c: mononym("ОХАЙЕЗ", 1)},
		"a capitalized handle":          {c: mononym("Зорро", 19)},
		"a script with no case":         {c: mononym("盛世", 2)},
		"twenty credits go to a person": {c: mononym("Зорро", 20), refusal: RefusedHeavy},
	} {
		t.Run(title, func(t *testing.T) {
			d := Decide(c.c, testEnv())
			if c.refusal != "" {
				assert.Equal(t, Step(0), d.Step)
				assert.Equal(t, c.refusal, d.Refusal)
				assert.Equal(t, StepKeepHandle, d.Evidence.RefusedStep)
				return
			}
			assert.Equal(t, StepKeepHandle, d.Step)
			assert.Empty(t, d.Refusal)
		})
	}
}

// The rules of step 3 pass no guard: their reads are the build's own.
func TestRulesStepIsUnguarded(t *testing.T) {
	c := candidate("case_defect", Name{Given: "МИХАИЛ", Family: "АНДРЕЕВ", Display: "МИХАИЛ АНДРЕЕВ",
		Sort: "АНДРЕЕВ, МИХАИЛ", Kind: "person"})
	c.Source = source("case_defect", []string{"all_caps"}, "МИХАИЛ", nil, "АНДРЕЕВ")
	c.Credits = 500
	assert.Equal(t, StepRules, Decide(c, testEnv()).Step)
}
