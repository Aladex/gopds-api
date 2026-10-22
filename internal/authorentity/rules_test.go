package authorentity_test

import (
	"testing"

	"gopds-api/internal/authorentity"
)

// D1: every credit of one fingerprint is one entity.
func TestOneFingerprintIsOneEntity(t *testing.T) {
	p := newCatalog(t).book(1, "Война и мир").book(2, "Анна Каренина").
		credit(10, 1, "fp-tolstoy", person("Толстой", "Лев", "Николаевич")).
		credit(11, 2, "fp-tolstoy", person("Толстой", "Лев", "Николаевич")).build()
	same(t, p, 10, 11)
	if got := p.RuleOf(10); got != authorentity.RuleD1 {
		t.Errorf("rule: want %s, got %s", authorentity.RuleD1, got)
	}
}

// D2: fingerprints that differ only in case, word order of the source,
// punctuation, a no-break space or a hyphen spelling are one entity.
func TestSameFullNameKeyIsOneEntity(t *testing.T) {
	p := newCatalog(t).book(1, "Война и мир").book(2, "Анна Каренина").book(3, "Детство").book(4, "Хаджи-Мурат").
		credit(10, 1, "fp-plain", person("Толстой", "Лев", "Николаевич")).
		// "ТОЛСТОЙ Лев Николаевич": upper case family.
		credit(11, 2, "fp-upper", person("ТОЛСТОЙ", "Лев", "Николаевич")).
		// "Толстой, Лев Николаевич": the comma inversion parses to the same parts.
		credit(12, 3, "fp-comma", person("Толстой,", "Лев", "Николаевич")).
		// A no-break space glued to the family name.
		credit(13, 4, "fp-nbsp", person("Толстой ", "Лев", "Николаевич")).build()
	same(t, p, 10, 11)
	same(t, p, 10, 12)
	same(t, p, 10, 13)
	if got := p.RuleOf(11); got != authorentity.RuleD2 {
		t.Errorf("rule: want %s, got %s", authorentity.RuleD2, got)
	}
}

// Words glued together, mojibake and a replacement character are other
// strings, not the same name: they stay separate entities.
func TestGluedAndBrokenNamesStaySeparate(t *testing.T) {
	p := newCatalog(t).book(1, "Война и мир").book(2, "Анна Каренина").book(3, "Детство").book(4, "Юность").
		credit(10, 1, "fp-plain", person("Толстой", "Лев", "Николаевич")).
		credit(11, 2, "fp-glued", person("ТолстойЛев", "Николаевич")).
		credit(12, 3, "fp-mojibake", person("Ð¢Ð¾Ð»ÑÑ‚Ð¾Ð¹", "Ð›ÐµÐ²", "ÐÐ¸ÐºÐ¾Ð»Ð°ÐµÐ²Ð¸Ñ‡").latin()).
		credit(13, 4, "fp-fffd", person("Толст�й", "Лев", "Николаевич")).build()
	apart(t, p, 10, 11)
	apart(t, p, 10, 12)
	apart(t, p, 10, 13)
}

// Father and son: the generation lives in the family field ("Дюма-сын") or
// in the suffix (Jr.), and splits the entity; its spelling does not.
func TestFatherAndSonStaySeparate(t *testing.T) {
	p := newCatalog(t).book(1, "Три мушкетёра").book(2, "Дама с камелиями").book(3, "Рассказы").
		book(4, "Борьба за огонь").book(5, "Пещерный лев").book(6, "Mother Night").book(7, "Letters").
		credit(10, 1, "fp-dumas", person("Дюма", "Александр")).
		credit(11, 2, "fp-dumas-son", person("Дюма-сын", "Александр")).
		credit(12, 3, "fp-dumas-son2", person("Дюма-Сын", "Александр")).
		credit(13, 4, "fp-rosny", person("Рони-старший", "Жозеф", "Анри")).
		credit(14, 5, "fp-rosny2", person("Рони-Старший", "Жозеф", "Анри")).
		credit(15, 6, "fp-vonnegut-jr", person("Vonnegut", "Kurt").latin().withSuffix("Jr.")).
		credit(16, 7, "fp-vonnegut", person("Vonnegut", "Kurt").latin()).build()
	apart(t, p, 10, 11)
	same(t, p, 11, 12)
	same(t, p, 13, 14)
	apart(t, p, 15, 16)
}

// Different patronymics are different people, and the short name is a
// bucket of its own (§2.0): "Алексей Иванов" without a patronymic is not
// either of them without evidence.
func TestDifferentPatronymicsAndTheShortNameStaySeparate(t *testing.T) {
	p := newCatalog(t).book(1, "Общага-на-Крови").book(2, "Сердце пармы").book(3, "Психология").
		book(4, "Футбол как жизнь").
		credit(10, 1, "fp-viktorovich", person("Иванов", "Алексей", "Викторович")).
		credit(11, 2, "fp-aleksandrovich", person("Иванов", "Алексей", "Александрович")).
		credit(12, 3, "fp-short", person("Иванов", "Алексей")).
		credit(13, 4, "fp-short-2", person("ИВАНОВ", "Алексей")).build()
	apart(t, p, 10, 11)
	apart(t, p, 10, 12)
	apart(t, p, 11, 12)
	// The bucket gathers every short fingerprint of the name by D2.
	same(t, p, 12, 13)
}

// D3 by title: a short-name book whose normalized title is already under the
// full name on another book goes to that full name, one book at a time.
func TestShortNameBookMovesByTitle(t *testing.T) {
	p := newCatalog(t).book(1, "Общага-на-Крови").book(2, "Общага на крови").book(3, "Психология").
		credit(10, 1, "fp-full", person("Иванов", "Алексей", "Викторович")).
		credit(11, 2, "fp-short", person("Иванов", "Алексей")).
		credit(12, 3, "fp-short", person("Иванов", "Алексей")).build()
	same(t, p, 10, 11)
	apart(t, p, 10, 12)
	if got := p.RuleOf(11); got != "D3:title" {
		t.Errorf("rule: want D3:title, got %s", got)
	}
}

// D3 by series and by co-author.
func TestShortNameBookMovesBySeriesAndCoauthor(t *testing.T) {
	p := newCatalog(t).
		book(1, "Сердце пармы", 700).book(2, "Золото бунта", 700).
		book(3, "Совместная книга").book(4, "Ещё одна совместная").book(5, "Чужая").
		credit(10, 1, "fp-full", person("Иванов", "Алексей", "Викторович")).
		credit(11, 2, "fp-short", person("Иванов", "Алексей")).
		credit(12, 3, "fp-full", person("Иванов", "Алексей", "Викторович")).
		credit(13, 3, "fp-co", person("Михаил", "Сергей", "Петрович")).
		credit(14, 4, "fp-short", person("Иванов", "Алексей")).
		credit(15, 4, "fp-co", person("Михаил", "Сергей", "Петрович")).
		credit(16, 5, "fp-short", person("Иванов", "Алексей")).build()
	same(t, p, 10, 11)
	same(t, p, 10, 14)
	apart(t, p, 10, 16)
	if got := p.RuleOf(11); got != "D3:series" {
		t.Errorf("rule: want D3:series, got %s", got)
	}
	if got := p.RuleOf(14); got != "D3:coauthor" {
		t.Errorf("rule: want D3:coauthor, got %s", got)
	}
}

// Evidence pointing to two full names with different patronymics leaves the
// book in the bucket.
func TestConflictingEvidenceStaysInTheBucket(t *testing.T) {
	p := newCatalog(t).book(1, "Мост").book(2, "Мост").book(3, "Мост").book(4, "Другое").
		credit(10, 1, "fp-viktorovich", person("Иванов", "Алексей", "Викторович")).
		credit(11, 2, "fp-aleksandrovich", person("Иванов", "Алексей", "Александрович")).
		credit(12, 3, "fp-short", person("Иванов", "Алексей")).
		credit(13, 4, "fp-short", person("Иванов", "Алексей")).build()
	same(t, p, 12, 13)
	apart(t, p, 10, 12)
	apart(t, p, 11, 12)
	if p.Report.Buckets.Conflicting != 1 {
		t.Errorf("conflicting: want 1, got %d", p.Report.Buckets.Conflicting)
	}
	if b := p.Report.Buckets; b.Buckets != 1 || b.WithFullNames != 1 || b.WithFullNamesBooks != 2 {
		t.Errorf("buckets: got %+v", b)
	}
}

// A book under both the short and the full name has two people on it: the
// short credit is not moved to the full name that already stands beside it.
func TestTheFullNameOnTheSameBookIsNoEvidence(t *testing.T) {
	p := newCatalog(t).book(1, "Мост").book(2, "Мост", 5).book(3, "Другое", 5).
		credit(10, 1, "fp-full", person("Иванов", "Алексей", "Викторович")).
		credit(11, 2, "fp-full", person("Иванов", "Алексей", "Викторович")).
		credit(12, 2, "fp-short", person("Иванов", "Алексей")).
		credit(13, 3, "fp-short", person("Иванов", "Алексей")).build()
	apart(t, p, 11, 12)
	// Book 3 shares series 5 with book 2, which is the full name's.
	same(t, p, 10, 13)
}

// "[СИ]" in the title is a split mark (decision 3b, option b): the samizdat
// book stays in the bucket even with evidence.
func TestSamizdatBookStaysInTheBucket(t *testing.T) {
	p := newCatalog(t).book(1, "Мост", 9).book(2, "Мост [СИ]", 9).book(3, "Мост", 9).book(4, "Другое").
		credit(10, 1, "fp-full", person("Иванов", "Алексей", "Викторович")).
		credit(11, 2, "fp-short", person("Иванов", "Алексей")).
		credit(12, 3, "fp-short", person("Иванов", "Алексей")).
		credit(13, 4, "fp-short", person("Иванов", "Алексей")).build()
	apart(t, p, 10, 11)
	same(t, p, 11, 13)
	// The book without the mark moves by its title.
	same(t, p, 10, 12)
	if p.Report.Buckets.SamizdatKept != 1 {
		t.Errorf("samizdat kept: want 1, got %d", p.Report.Buckets.SamizdatKept)
	}
}

// A short name of initials is not merged in E1: "Толстой Л." is not moved to
// "Толстой Л. Н." even with a shared title.
func TestInitialsAreNotMoved(t *testing.T) {
	p := newCatalog(t).book(1, "Мост").book(2, "Мост").
		credit(10, 1, "fp-full", person("Толстой", "Л.", "Н.")).
		credit(11, 2, "fp-short", person("Толстой", "Л.")).build()
	apart(t, p, 10, 11)
}

// A typo in the patronymic merges with the nearest patronymic of the same
// name only with strong evidence; distance alone is no evidence, and a
// male/female pair or two people on one book never merge.
func TestPatronymicTypoMergesOnlyWithEvidence(t *testing.T) {
	t.Run("with a shared series", func(t *testing.T) {
		p := newCatalog(t).book(1, "Первая", 3).book(2, "Вторая", 3).
			credit(10, 1, "fp-right", person("Иванов", "Алексей", "Викторович")).
			credit(11, 2, "fp-typo", person("Иванов", "Алексей", "Викторовыч")).build()
		same(t, p, 10, 11)
		if got := p.RuleOf(11); got != authorentity.RuleTypo {
			t.Errorf("rule: want %s, got %s", authorentity.RuleTypo, got)
		}
	})
	t.Run("without evidence", func(t *testing.T) {
		p := newCatalog(t).book(1, "Первая").book(2, "Вторая").
			credit(10, 1, "fp-right", person("Иванов", "Алексей", "Викторович")).
			credit(11, 2, "fp-typo", person("Иванов", "Алексей", "Викторовыч")).build()
		apart(t, p, 10, 11)
	})
	t.Run("male and female", func(t *testing.T) {
		p := newCatalog(t).book(1, "Первая", 3).book(2, "Вторая", 3).
			credit(10, 1, "fp-male", person("Иванов", "Саша", "Викторович")).
			credit(11, 2, "fp-female", person("Иванов", "Саша", "Викторовна")).build()
		apart(t, p, 10, 11)
	})
	t.Run("both on one book", func(t *testing.T) {
		p := newCatalog(t).book(1, "Первая", 3).book(2, "Вторая", 3).
			credit(10, 1, "fp-right", person("Иванов", "Алексей", "Викторович")).
			credit(11, 1, "fp-typo", person("Иванов", "Алексей", "Викторовыч")).
			credit(12, 2, "fp-typo", person("Иванов", "Алексей", "Викторовыч")).build()
		apart(t, p, 10, 11)
	})
	t.Run("two nearest candidates", func(t *testing.T) {
		p := newCatalog(t).book(1, "Первая", 3).book(2, "Вторая", 3).book(3, "Третья", 3).
			credit(10, 1, "fp-a", person("Иванов", "Алексей", "Петрович")).
			credit(11, 2, "fp-b", person("Иванов", "Алексей", "Петровиш")).
			credit(12, 3, "fp-c", person("Иванов", "Алексей", "Петровия")).build()
		// fp-a has two candidates at distance 1 and picks neither; fp-b
		// and fp-c are at distance 1 from each other as well as from fp-a.
		apart(t, p, 10, 11)
		apart(t, p, 10, 12)
	})
}

// "(N)" in the source fields is a namesake tag: its fingerprint stays its own
// entity and takes no part in D2 or D3.
func TestNamesakeTagStaysItsOwnEntity(t *testing.T) {
	p := newCatalog(t).book(1, "Мост").book(2, "Мост").book(3, "Другое").
		credit(10, 1, "fp-plain", person("Иванов", "Алексей", "Викторович")).
		credit(11, 2, "fp-tagged", person("Иванов", "Алексей", "Викторович").withTag(2)).
		credit(12, 3, "fp-tagged-other", person("Иванов", "Алексей", "Викторович").withTag(2)).build()
	apart(t, p, 10, 11)
	apart(t, p, 11, 12)
}

// Placeholders and names without a letter or digit get no entity.
func TestPlaceholdersAndEmptyNamesGetNoEntity(t *testing.T) {
	p := newCatalog(t).book(1, "Сказки").book(2, "Песни").
		credit(10, 1, "fp-unknown", person("неизвестен", "Автор")).
		credit(11, 2, "fp-dash", person("—", "…")).
		// Symbols survive the search key: still no letter or digit.
		book(3, "Стихи").credit(12, 3, "fp-symbols", person("+++", "~")).build()
	none(t, p, 10)
	none(t, p, 11)
	none(t, p, 12)
	if p.Report.NoEntity[authorentity.NoEntityPlaceholder] != 1 || p.Report.NoEntity[authorentity.NoEntityNoLetters] != 2 {
		t.Errorf("no entity: got %v", p.Report.NoEntity)
	}
}

// D4: the nickname of a result is a name variant of the entity, not an
// entity of its own; every credit name is a variant too, with its books.
func TestNicknameIsANameVariant(t *testing.T) {
	p := newCatalog(t).book(1, "Мост").book(2, "Река").
		credit(10, 1, "fp-a", person("Петров", "Иван", "Сергеевич").withNickname("Капитан")).
		credit(11, 2, "fp-b", person("ПЕТРОВ", "Иван", "Сергеевич")).build()
	same(t, p, 10, 11)
	names := p.NamesOf(p.EntityOf(10))
	want := map[string]int{"иван сергеевич петров": 2, "капитан": 1}
	if len(names) != len(want) {
		t.Fatalf("names: want %v, got %v", want, names)
	}
	for _, n := range names {
		if want[n.SearchKey] != n.Books {
			t.Errorf("name %q: want %d books, got %d", n.SearchKey, want[n.SearchKey], n.Books)
		}
	}
}

// The entity's name is the display name of its credits with the most books,
// then the fullest, then the lowest result id.
func TestEntityNameIsTheMostCommonFullestName(t *testing.T) {
	p := newCatalog(t).book(1, "А").book(2, "Б").book(3, "В").
		credit(10, 1, "fp-upper", person("ТОЛСТОЙ", "Лев", "Николаевич")).
		credit(11, 2, "fp-upper", person("ТОЛСТОЙ", "Лев", "Николаевич")).
		credit(12, 3, "fp-plain", person("Толстой", "Лев", "Николаевич")).build()
	e := p.Entity(p.EntityOf(12))
	if e == nil || e.DisplayName != "Лев Николаевич ТОЛСТОЙ" {
		t.Fatalf("name: got %+v", e)
	}
}

// One credit whose handling panics is left out with its reason; the pass
// goes on for the others (§1.5).
func TestOneBadCreditNeverStopsThePass(t *testing.T) {
	defer authorentity.PanicOnCredit(11)()
	p := newCatalog(t).book(1, "Мост").book(2, "Река").book(3, "Лес").
		credit(10, 1, "fp-a", person("Петров", "Иван", "Сергеевич")).
		credit(11, 2, "fp-b", person("Петров", "Иван", "Сергеевич")).
		credit(12, 3, "fp-c", person("Петров", "Иван", "Сергеевич")).build()
	none(t, p, 11)
	same(t, p, 10, 12)
	if p.Report.NoEntity[authorentity.NoEntityItemFailed] != 1 {
		t.Errorf("item failed: got %v", p.Report.NoEntity)
	}
}

// Review r1, finding 2: an asymmetric tie. Петрович–Петровыч and
// Петровыч–Петровыц are one edit apart, Петрович–Петровыц two: Петровыч has
// two equally close candidates and is ambiguous, so no union reaches it from
// either side.
func TestAnAmbiguousPatronymicTakesNoUnionFromEitherSide(t *testing.T) {
	p := newCatalog(t).book(1, "Первая", 3).book(2, "Вторая", 3).book(3, "Третья", 3).
		credit(10, 1, "fp-a", person("Иванов", "Алексей", "Петрович")).
		credit(11, 2, "fp-b", person("Иванов", "Алексей", "Петровыч")).
		credit(12, 3, "fp-c", person("Иванов", "Алексей", "Петровыц")).build()
	apart(t, p, 10, 11)
	apart(t, p, 12, 11)
	apart(t, p, 10, 12)
}

// Review r1, finding 3: Петрович and Петровыц are credited on one book, so
// they are two people; Петровыч on another book must not bridge them.
func TestTwoCoCreditedPatronymicsAreNeverBridged(t *testing.T) {
	p := newCatalog(t).book(1, "Первая", 3).book(2, "Вторая", 3).
		credit(10, 1, "fp-a", person("Иванов", "Алексей", "Петрович")).
		credit(11, 1, "fp-c", person("Иванов", "Алексей", "Петровыц")).
		credit(12, 2, "fp-b", person("Иванов", "Алексей", "Петровыч")).build()
	apart(t, p, 10, 11)
}

// The same separation where the bridge node is not ambiguous: Петрович and
// Пятравыч share book 1; Петровыч (book 2) is one edit from Петрович and two
// from Пятравыч. Петрович and Петровыч merge; Пятравыч stays apart.
func TestATypoMergeNeverJoinsAComponentWithACoCreditedName(t *testing.T) {
	p := newCatalog(t).book(1, "Первая", 3).book(2, "Вторая", 3).
		credit(10, 1, "fp-a", person("Иванов", "Алексей", "Петрович")).
		credit(11, 1, "fp-c", person("Иванов", "Алексей", "Пятравыч")).
		credit(12, 2, "fp-b", person("Иванов", "Алексей", "Петровыч")).build()
	same(t, p, 10, 12)
	apart(t, p, 10, 11)
	apart(t, p, 12, 11)
}

// D3 respects the same separation: a short-name book is not moved into a
// merged full name one of whose names already stands on that book.
func TestAShortNameBookIsNotMovedIntoAnEntityOnTheSameBook(t *testing.T) {
	p := newCatalog(t).book(1, "Первая", 3).book(2, "Вторая", 3).book(4, "Первая").
		credit(10, 1, "fp-a", person("Иванов", "Алексей", "Петрович")).
		credit(12, 2, "fp-b", person("Иванов", "Алексей", "Петровыч")).
		// Book 4: the typo variant and the short name, two people; the
		// title points at fp-a, whose entity holds fp-b.
		credit(13, 4, "fp-b", person("Иванов", "Алексей", "Петровыч")).
		credit(14, 4, "fp-short", person("Иванов", "Алексей")).build()
	same(t, p, 10, 12)
	apart(t, p, 10, 14)
}

// Review r1, finding 4: a selected collective has no person parts; it is an
// entity of kind collective keyed by its name, never merged with a person
// of the same words.
func TestASelectedCollectiveIsAnEntityOfItsOwnKind(t *testing.T) {
	c := newCatalog(t).book(1, "Выпуск 1").book(2, "Выпуск 2").book(3, "Сказки")
	c.collective(10, 1, "fp-nauka", "Журнал Наука").
		collective(11, 2, "fp-nauka-2", "ЖУРНАЛ «НАУКА»").
		credit(12, 3, "fp-person", nickOnly("Журнал Наука"))
	p := c.build()
	same(t, p, 10, 11)
	apart(t, p, 10, 12)
	if e := p.Entity(p.EntityOf(10)); e == nil || e.Kind != authorentity.KindCollective {
		t.Errorf("collective: got %+v", e)
	}
	if e := p.Entity(p.EntityOf(12)); e == nil || e.Kind != authorentity.KindPerson {
		t.Errorf("nickname-only person: got %+v", e)
	}
	if p.Report.NoEntity[authorentity.NoEntityNoLetters] != 0 {
		t.Errorf("no entity: got %v", p.Report.NoEntity)
	}
}

// A person known by a nickname alone ("The Captain") is an entity keyed by
// it: one per nickname.
func TestANicknameOnlyPersonIsAnEntity(t *testing.T) {
	p := newCatalog(t).book(1, "А").book(2, "Б").book(3, "В").
		credit(10, 1, "fp-cap", nickOnly("The Captain").latin()).
		credit(11, 2, "fp-cap-2", nickOnly("the captain").latin()).
		credit(12, 3, "fp-other", nickOnly("The Major").latin()).build()
	same(t, p, 10, 11)
	apart(t, p, 10, 12)
}

// A name co-credited on the same book is no typo candidate at all: it
// neither merges nor makes a tie. Петрович shares book 1 with Петровыч, so
// its one candidate is Петровиш (book 2) and the two merge; Петровыч stays.
func TestACoCreditedNameIsNoTypoCandidate(t *testing.T) {
	p := newCatalog(t).book(1, "Первая", 3).book(2, "Вторая", 3).
		credit(10, 1, "fp-x", person("Иванов", "Алексей", "Петрович")).
		credit(11, 1, "fp-y", person("Иванов", "Алексей", "Петровыч")).
		credit(12, 2, "fp-z", person("Иванов", "Алексей", "Петровиш")).build()
	same(t, p, 10, 12)
	apart(t, p, 10, 11)
}

// Review r2: a selected collective that is only a stub — "Коллектив
// авторов", the label of many unnamed authors — is no entity (§1.4): two
// unrelated books under it are never gathered. A named collective still is.
func TestACollectiveStubGetsNoEntity(t *testing.T) {
	c := newCatalog(t).book(1, "Сказки народов мира").book(2, "Справочник садовода").book(3, "Выпуск 1")
	c.collective(10, 1, "fp-stub", "Коллектив авторов").
		collective(11, 2, "fp-stub-2", "КОЛЛЕКТИВ АВТОРОВ").
		collective(12, 3, "fp-nauka", "Журнал Наука")
	p := c.build()
	none(t, p, 10)
	none(t, p, 11)
	if e := p.Entity(p.EntityOf(12)); e == nil || e.Kind != authorentity.KindCollective {
		t.Errorf("named collective: got %+v", e)
	}
	if p.Report.NoEntity[authorentity.NoEntityPlaceholder] != 2 {
		t.Errorf("no entity: got %v", p.Report.NoEntity)
	}
}
