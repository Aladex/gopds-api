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

// withNickname is n with a nickname the display does not show.
//
//nolint:gocritic // a fixture takes its name by value, as the table rows spell it
func withNickname(n Name, nickname string) Name {
	n.Nickname = nickname
	return n
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
		"a whole Latin look-alike word in a Cyrillic name is refused, not rewritten": {
			c:       stored(person("Capa", "Сил"), person("Capa", "Сил")),
			refusal: RefusedLookalikeWord,
		},
		"a real Latin surname of look-alike letters is never rewritten": {
			c:       stored(person("Игорь", "Cox"), person("Игорь", "Cox")),
			refusal: RefusedLookalikeWord,
		},
		"a whole look-alike word the source writes mixed": {
			c:     stored(Name{Display: "Сapa Сил", Kind: KindPerson}, person("Capa", "Сил")),
			after: person("Сара", "Сил"), fixes: []string{FixLookalike},
		},
		"a Latin initial of a Cyrillic name": {
			c:     stored(person("B.", "Баландин"), person("B.", "Баландин")),
			after: person("В.", "Баландин"), fixes: []string{FixLookalike},
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
			c: stored(Name{Display: "Роберт , Блох", Kind: KindPerson}, Name{Given: "Роберт,", Family: "Блох", Display: "Роберт, Блох",
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
		"abbreviated given names keep their dots": {
			c:     stored(Name{Display: "Влад. Азов", Kind: KindPerson}, person("Влад.", "Азов")),
			after: person("Влад.", "Азов"),
		},
		"an abbreviated Latin given name keeps its dot": {
			c: stored(Name{Display: "Jas. R. Petrin", Kind: KindPerson}, Name{Given: "Jas.", Additional: "R.",
				Family: "Petrin", Display: "Jas. R. Petrin", Sort: "Petrin, Jas. R.", Kind: KindPerson}),
			after: Name{Given: "Jas.", Additional: "R.", Family: "Petrin", Display: "Jas. R. Petrin",
				Sort: "Petrin, Jas. R.", Kind: KindPerson},
		},
		"transliterated initials keep their dots": {
			c: stored(Name{Display: "Ди. Джи. Виски", Kind: KindPerson}, Name{Given: "Ди.", Additional: "Джи.",
				Family: "Виски", Display: "Ди. Джи. Виски", Sort: "Виски, Ди. Джи.", Kind: KindPerson}),
			after: Name{Given: "Ди.", Additional: "Джи.", Family: "Виски", Display: "Ди. Джи. Виски",
				Sort: "Виски, Ди. Джи.", Kind: KindPerson},
		},
		"degrees keep their dots": {
			c: stored(Name{Display: "Dr. med. Hauards K. Katlers", Kind: KindPerson}, Name{Given: "Hauards",
				Additional: "K.", Family: "Katlers", Prefix: "Dr. med.", Display: "Dr. med. Hauards K. Katlers",
				Sort: "Katlers, Hauards K.", Kind: KindPerson}),
			after: Name{Given: "Hauards", Additional: "K.", Family: "Katlers", Prefix: "Dr. med.",
				Display: "Dr. med. Hauards K. Katlers", Sort: "Katlers, Hauards K.", Kind: KindPerson},
		},
		"a compound initial keeps its dot": {
			c:     stored(Name{Display: "J-P. Sartre", Kind: KindPerson}, person("J-P.", "Sartre")),
			after: person("J-P.", "Sartre"),
		},
		"the author's own comma stays": {
			c: stored(Name{Display: "James Wesley, Rawles", Kind: KindPerson}, Name{Given: "James",
				Additional: "Wesley", Family: "Rawles", Display: "James Wesley, Rawles", Sort: "Rawles, James Wesley",
				Kind: KindPerson}),
			after: Name{Given: "James", Additional: "Wesley", Family: "Rawles", Display: "James Wesley, Rawles",
				Sort: "Rawles, James Wesley", Kind: KindPerson},
		},
		"a trailing dot the source glued is kept": {
			c: stored(Name{Display: "Андрей Битов.", Kind: KindPerson}, Name{Given: "Андрей", Family: "Битов",
				Display: "Андрей Битов.", Sort: "Битов, Андрей", Kind: KindPerson}),
			after: Name{Given: "Андрей", Family: "Битов", Display: "Андрей Битов.", Sort: "Битов, Андрей",
				Kind: KindPerson},
		},
		"a suffix comma and an apostrophe stay": {
			c: stored(Name{Display: "John O’Neill, Jr.", Kind: KindPerson}, Name{Given: "John", Family: "O’Neill",
				Suffix: "Jr.", Display: "John O’Neill, Jr.", Sort: "O’Neill, John", Kind: KindPerson}),
			after: Name{Given: "John", Family: "O’Neill", Suffix: "Jr.", Display: "John O’Neill, Jr.",
				Sort: "O’Neill, John", Kind: KindPerson},
		},
		"a comma that ends the display": {
			c: stored(Name{Display: "doc. K. Kalmikovs,", Kind: KindPerson}, Name{Given: "K.", Family: "Kalmikovs",
				Prefix: "doc", Display: "doc. K. Kalmikovs,", Sort: "Kalmikovs, K.", Kind: KindPerson}),
			refusal: RefusedStrayPunctuation,
		},
		"Ed is a given name": {
			c:     stored(person("Ed", "McBain"), person("Ed", "McBain")),
			after: person("Ed", "McBain"),
		},
		"Ed dropped is a lost word": {
			c:       stored(person("Ed", "McBain"), handle("McBain")),
			refusal: RefusedLostToken,
		},
		"Per is a given name": {
			c:     stored(person("Пер", "Валё"), person("Пер", "Валё")),
			after: person("Пер", "Валё"),
		},
		"an editor's mark dropped": {
			c:     stored(Name{Display: "Роберт Коули (ред.)", Kind: KindPerson}, person("Роберт", "Коули")),
			after: person("Роберт", "Коули"),
		},
		"an abbreviated role kept as a given name": {
			c: stored(Name{Display: "ред. Коули", Kind: KindPerson}, Name{Given: "ред.", Family: "Коули",
				Display: "ред. Коули", Sort: "Коули, ред.", Kind: KindPerson}),
			refusal: RefusedRoleAsName,
		},
		"a DJ prefix": {
			c: stored(Name{Display: "DJ Miller", Kind: KindPerson}, Name{Family: "Miller", Prefix: "DJ",
				Display: "DJ Miller", Sort: "Miller", Kind: KindPerson}),
			after: Name{Family: "Miller", Prefix: "DJ", Display: "DJ Miller", Sort: "Miller", Kind: KindPerson},
		},
		"a mark set apart in the answer too": {
			c: stored(Name{Display: "Чарлз . Мессенджер", Kind: KindPerson}, Name{Given: "Чарлз", Additional: ".",
				Family: "Мессенджер", Display: "Чарлз . Мессенджер", Sort: "Мессенджер, Чарлз .", Kind: KindPerson}),
			after: person("Чарлз", "Мессенджер"), fixes: []string{FixPunctuation},
		},
		"a sort key comma the source set apart stays in the sort": {
			c: stored(Name{Display: "Блох , Роберт", Kind: KindPerson}, Name{Given: "Роберт", Family: "Блох",
				Display: "Роберт Блох", Sort: "Блох, Роберт", Kind: KindPerson}),
			after: person("Роберт", "Блох"),
		},
		"a suffix comma the source set apart stays": {
			c: stored(Name{Display: "Иван Иванов , мл.", Kind: KindPerson}, Name{Given: "Иван", Family: "Иванов",
				Suffix: "мл.", Display: "Иван Иванов, мл.", Sort: "Иванов, Иван", Kind: KindPerson}),
			after: Name{Given: "Иван", Family: "Иванов", Suffix: "мл.", Display: "Иван Иванов, мл.",
				Sort: "Иванов, Иван", Kind: KindPerson},
		},
		"an editor's mark in parentheses without a dot dropped": {
			c:     stored(Name{Display: "Роберт Коули (ред)", Kind: KindPerson}, person("Роберт", "Коули")),
			after: person("Роберт", "Коули"),
		},
		"a role word dropped": {
			c:     stored(Name{Display: "Ирина Зюзина художник", Kind: KindPerson}, person("Ирина", "Зюзина")),
			after: person("Ирина", "Зюзина"),
		},
		"a dotted initial with a dot set apart after it, 30131": {
			c: stored(Name{Display: "Фрэйн Б. .", Kind: KindPerson}, Name{Given: "Б.", Family: "Фрэйн",
				Display: "Фрэйн Б..", Sort: "Фрэйн, Б.", Kind: KindPerson}),
			after: Name{Given: "Б.", Family: "Фрэйн", Display: "Фрэйн Б.", Sort: "Фрэйн, Б.", Kind: KindPerson},
			fixes: []string{FixPunctuation},
		},
		"a dotted initial with a dot set apart after it, 236612": {
			c: stored(Name{Display: "Монк С. .", Kind: KindPerson}, Name{Given: "С.", Family: "Монк",
				Display: "Монк С..", Sort: "Монк, С.", Kind: KindPerson}),
			after: Name{Given: "С.", Family: "Монк", Display: "Монк С.", Sort: "Монк, С.", Kind: KindPerson},
			fixes: []string{FixPunctuation},
		},
		"an extra dot in every field goes once from each": {
			c: stored(Name{Display: "Фрэйн Б. .", Kind: KindPerson}, Name{Given: "Б..", Family: "Фрэйн",
				Display: "Фрэйн Б..", Sort: "Фрэйн, Б..", Kind: KindPerson}),
			after: Name{Given: "Б.", Family: "Фрэйн", Display: "Фрэйн Б.", Sort: "Фрэйн, Б.", Kind: KindPerson},
			fixes: []string{FixPunctuation},
		},
		"initials whose dots the source set apart keep them": {
			c: stored(Name{Display: "J . R . R . Tolkien", Kind: KindPerson}, Name{Given: "J.", Additional: "R. R.",
				Family: "Tolkien", Display: "J. R. R. Tolkien", Sort: "Tolkien, J. R. R.", Kind: KindPerson}),
			after: Name{Given: "J.", Additional: "R. R.", Family: "Tolkien", Display: "J. R. R. Tolkien",
				Sort: "Tolkien, J. R. R.", Kind: KindPerson},
		},
		"an initial's dot the answer still sets apart stays": {
			c: stored(Name{Display: "J . Tolkien", Kind: KindPerson}, Name{Given: "J.", Family: "Tolkien",
				Display: "J . Tolkien", Sort: "Tolkien, J.", Kind: KindPerson}),
			after: Name{Given: "J.", Family: "Tolkien", Display: "J . Tolkien", Sort: "Tolkien, J.", Kind: KindPerson},
		},
		"an abbreviation with a dot set apart after it keeps its own": {
			c: stored(Name{Display: "Влад. . Азов", Kind: KindPerson}, Name{Given: "Влад.", Family: "Азов",
				Display: "Влад.. Азов", Sort: "Азов, Влад.", Kind: KindPerson}),
			after: Name{Given: "Влад.", Family: "Азов", Display: "Влад. Азов", Sort: "Азов, Влад.", Kind: KindPerson},
			fixes: []string{FixPunctuation},
		},
		"a word the source writes twice keeps the larger need": {
			c: stored(Name{Display: "Ли. . Ли , Ван", Kind: KindPerson}, Name{Given: "Ли.", Additional: "Ли",
				Family: "Ван", Display: "Ли. Ли Ван", Sort: "Ван, Ли. Ли", Kind: KindPerson}),
			after: Name{Given: "Ли.", Additional: "Ли", Family: "Ван", Display: "Ли. Ли Ван", Sort: "Ван, Ли. Ли",
				Kind: KindPerson},
		},
		"joined Latin initials of a Cyrillic name, 196897": {
			c: stored(Name{Display: "A.B. Ломанов", Kind: KindPerson}, Name{Given: "A.B.", Family: "Ломанов",
				Display: "A.B. Ломанов", Sort: "Ломанов, A.B.", Kind: KindPerson}),
			after: Name{Given: "А.В.", Family: "Ломанов", Display: "А.В. Ломанов", Sort: "Ломанов, А.В.",
				Kind: KindPerson},
			fixes: []string{FixLookalike},
		},
		"joined initials beside one of no look-alike": {
			c: stored(Name{Display: "A.B. L. Ломанов", Kind: KindPerson}, Name{Given: "A.B.", Additional: "L.",
				Family: "Ломанов", Display: "A.B. L. Ломанов", Sort: "Ломанов, A.B. L.", Kind: KindPerson}),
			refusal: RefusedLookalikeWord,
		},
		"an undotted Latin letter stays refused": {
			c:       stored(person("A", "Хазанов"), person("A", "Хазанов")),
			refusal: RefusedLookalikeWord,
		},
		"Latin initials of which one has no look-alike": {
			c: stored(Name{Display: "E. L. Джеймс", Kind: KindPerson}, Name{Given: "E.", Additional: "L.",
				Family: "Джеймс", Display: "E. L. Джеймс", Sort: "Джеймс, E. L.", Kind: KindPerson}),
			refusal: RefusedLookalikeWord,
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
		"a one-word nickname beside a full display": {
			c: stored(Name{Display: "Руслан Балабеков Lorgar666", Kind: KindPerson},
				withNickname(person("Руслан", "Балабеков"), "Lorgar666")),
			after: withNickname(person("Руслан", "Балабеков"), "Lorgar666"),
		},
		"a one-word nickname in parentheses": {
			c: stored(Name{Display: "Мария Капилина (Пичугина)", Kind: KindPerson},
				withNickname(person("Мария", "Капилина"), "Пичугина")),
			refusal: RefusedLostToken,
		},
		"a one-word nickname in quotes": {
			c: stored(Name{Display: "Хардин Джесси «Волк»", Kind: KindPerson},
				withNickname(person("Хардин", "Джесси"), "Волк")),
			refusal: RefusedLostToken,
		},
		"a nickname of two words": {
			c: stored(Name{Display: "Михаил Валерин August Flieger", Kind: KindPerson},
				withNickname(person("Михаил", "Валерин"), "August Flieger")),
			refusal: RefusedLostToken,
		},
		"a bare number is a namesake's tag, no nickname": {
			c: stored(Name{Display: "Евгений Александров 2", Kind: KindPerson},
				withNickname(person("Евгений", "Александров"), "2")),
			refusal: RefusedLostToken,
		},
		"a lost word that is no nickname": {
			c:       stored(Name{Display: "Анна Мудрова Петровна", Kind: KindPerson}, person("Анна", "Мудрова")),
			refusal: RefusedLostToken,
		},
		"a clerical title dropped": {
			c:     stored(Name{Display: "Рафаил Нойка иеромонах", Kind: KindPerson}, person("Рафаил", "Нойка")),
			after: person("Рафаил", "Нойка"),
		},
		"a role in parentheses dropped": {
			c:     stored(Name{Display: "Анна Мудрова (сост.)", Kind: KindPerson}, person("Анна", "Мудрова")),
			after: person("Анна", "Мудрова"),
		},
		"a display word in no part": {
			c: stored(Name{Given: "Kelley", Additional: "St.", Family: "John", Display: "Kelley St. John",
				Kind: KindPerson}, Name{Given: "Kelley", Family: "John", Display: "Kelley St. John",
				Sort: "St. John, Kelley", Kind: KindPerson}),
			refusal: RefusedUnparsedWord,
		},
		"a sort word in no part": {
			c: stored(Name{Display: "Kelley John", Kind: KindPerson}, Name{Given: "Kelley", Family: "John",
				Display: "Kelley John", Sort: "St. John, Kelley", Kind: KindPerson}),
			refusal: RefusedUnparsedWord,
		},
		"a mononym has no parts to hold its display": {
			c:     stored(handle("Басё"), Name{Display: "Басё", Sort: "Басё", Kind: KindPerson}),
			after: Name{Display: "Басё", Sort: "Басё", Kind: KindPerson},
		},
		"a title off the role lists moved to the prefix": {
			c: stored(Name{Display: "Доктор Иван Петров", Kind: KindPerson}, Name{Given: "Иван", Family: "Петров",
				Prefix: "Доктор", Display: "Иван Петров", Sort: "Петров, Иван", Kind: KindPerson}),
			after: Name{Given: "Иван", Family: "Петров", Prefix: "Доктор", Display: "Иван Петров",
				Sort: "Петров, Иван", Kind: KindPerson},
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

	c = candidate("unusual_characters", Name{Display: "Монк С. .", Kind: KindPerson})
	c.Answers = judgesOnly(&Name{Given: "С.", Family: "Монк", Display: "Монк С..", Sort: "Монк, С.",
		Kind: KindPerson}, "person")
	d = Decide(c, testEnv())
	assert.Equal(t, StepJudgesOnly, d.Step)
	assert.Equal(t, Name{Given: "С.", Family: "Монк", Display: "Монк С.", Sort: "Монк, С.", Kind: KindPerson}, d.After)

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
		"an editor's mark in a named collective": {c: named("Иван Петров и Анна Смит (ред.)"),
			step: StepAsNamedCollective, refusal: RefusedRoleAsName},
		"Ed in a named pair": {c: named("Ed McBain и Анна Смит"), step: StepAsNamedCollective},
		"editors after a comma read as an organization": {c: organization("Shane McKenzie, eds"),
			step: StepPlaceholderOrg, refusal: RefusedRoleAsName},
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
		"a magazine":                {c: mononym("Компьютерра", 3), refusal: RefusedListedName},
		"a church":                  {c: mononym("РПЦ", 3), refusal: RefusedListedName},
		"the Bible":                 {c: mononym("Bībele", 3), refusal: RefusedListedName},
		"a placeholder word":        {c: mononym("Пользователь", 3), refusal: RefusedListedName},
		"five capitals":             {c: mononym("ИГСОА", 3), refusal: RefusedShortCaps},
		"two Latin capitals":        {c: mononym("VS", 1), refusal: RefusedShortCaps},
		"six capitals are a handle": {c: mononym("ОХАЙЕЗ", 1)},
		"a handle that begins like a listed name": {c: mononym("Терран", 1)},
		"the listed publisher":                    {c: mononym("Терра", 1), refusal: RefusedListedName},
		"a magazine's longer name":                {c: mononym("Журналист", 1), refusal: RefusedListedName},
		"a capitalized handle":                    {c: mononym("Зорро", 19)},
		"a script with no case":                   {c: mononym("盛世", 2)},
		"twenty credits go to a person":           {c: mononym("Зорро", 20), refusal: RefusedHeavy},
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
