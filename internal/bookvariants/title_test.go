package bookvariants

import (
	"slices"
	"testing"
)

func TestNormalizeTitle(t *testing.T) {
	const hobbit = "хоббит или туда и обратно"
	cases := []struct {
		raw       string
		key       string
		tags      []string
		groupable bool
	}{
		// Case, punctuation and spacing do not tell two editions apart.
		{"Хоббит, или Туда и Обратно", hobbit, nil, true},
		{"Хоббит или туда и обратно", hobbit, nil, true},
		{"Хоббит, или  Туда и обратно", hobbit, nil, true},
		{"Ёлка", "елка", nil, true},
		{"Елка", "елка", nil, true},

		// A trailing tag names the source or the edition: stripped, returned.
		{"Возвращение [litres]", "возвращение", []string{"litres"}, true}, //nolint:misspell // a catalog tag
		{"Возвращение [СИ]", "возвращение", []string{"СИ"}, true},
		{"Возвращение [ЛП]", "возвращение", []string{"ЛП"}, true},
		{"Возвращение [сборник litres]", "возвращение", []string{"сборник litres"}, true}, //nolint:misspell // a catalog tag
		{"Возвращение [publisher: SelfPub]", "возвращение", []string{"publisher: SelfPub"}, true},
		{"Возвращение [2-е изд.]", "возвращение", []string{"2-е изд."}, true},
		{"Если кто-то это построит, все умрут [If Anyone Builds It, Everyone Dies]",
			"если кто то это построит все умрут", []string{"If Anyone Builds It, Everyone Dies"}, true},
		{"Рассказы [сборник] [litres]", "рассказы", []string{"сборник", "litres"}, true}, //nolint:misspell // a catalog tag
		{"Рассказы [сборник рассказов]", "рассказы", []string{"сборник рассказов"}, true},

		// A volume, an issue or a bilingual edition is another book: kept.
		{"Сага [Том II]", "сага том ii", nil, true},
		{"Сага [Книга 1]", "сага книга 1", nil, true},
		{"Сага [Книга 2]", "сага книга 2", nil, true},
		{"Сага [Часть первая]", "сага часть первая", nil, true},
		{"Сага [1 том]", "сага 1 том", nil, true},
		{"Сага [2-я часть]", "сага 2 я часть", nil, true},
		{"Сага [2]", "сага 2", nil, true},
		{"Журнал [Наука и жизнь № 10]", "журнал наука и жизнь 10", nil, true},
		{"ГПиМРМ [английский и русский параллельные тексты]", "гпимрм английский и русский параллельные тексты", nil, true},
		{"Сказки [билингва]", "сказки билингва", nil, true},

		// A form tag names another text by the same title: kept (coordinator, 08.11).
		{"Людям Настоящего [проза]", "людям настоящего проза", nil, true},
		{"Людям Настоящего [поэзия]", "людям настоящего поэзия", nil, true},
		{"Людям Настоящего [Стихи]", "людям настоящего стихи", nil, true},
		{"Наследник [роман]", "наследник роман", nil, true},
		{"Наследник [Повесть]", "наследник повесть", nil, true},
		{"Наследник [рассказ]", "наследник рассказ", nil, true},
		{"Наследник [Рассказы]", "наследник рассказы", nil, true},
		{"Наследник [story]", "наследник story", nil, true},
		{"Наследник [пьеса]", "наследник пьеса", nil, true},
		{"Наследник [Повести и рассказы]", "наследник повести и рассказы", nil, true},
		// A kept bracket stops the stripping: the tag after it still goes.
		{"Наследник [роман] [litres]", "наследник роман", []string{"litres"}, true}, //nolint:misspell // a catalog tag

		// Round brackets are not tags.
		{"Хоббит (пер. В. Маториной)", "хоббит пер в маториной", nil, true},

		// Junk does not group.
		{"unknown", "unknown", nil, false},
		{"Upload", "upload", nil, false},
		{"", "", nil, false},
		{"   ", "", nil, false},
		{"Без названия", "без названия", nil, false},
		{"???", "", nil, false},
		{"★★★", "", nil, false},
		{"…", "", nil, false},

		// A title that is only a tag keeps it: nothing would be left.
		{"[litres]", "litres", nil, true}, //nolint:misspell // a catalog tag
		{"1984", "1984", nil, true},
	}
	for _, tc := range cases {
		key, tags, groupable := NormalizeTitle(tc.raw)
		if key != tc.key || !slices.Equal(tags, tc.tags) || groupable != tc.groupable {
			t.Errorf("NormalizeTitle(%q) = %q, %q, %v; want %q, %q, %v",
				tc.raw, key, tags, groupable, tc.key, tc.tags, tc.groupable)
		}
	}
}

func TestNormalizeTitleSeparatesVolumesAndForms(t *testing.T) {
	pairs := [][2]string{
		{"Сага [Книга 1]", "Сага [Книга 2]"},
		{"Сага [Том I]", "Сага [Том II]"},
		{"Людям Настоящего [проза]", "Людям Настоящего [поэзия]"},
		{"Хоббит", "Хоббит (пер. В. Маториной)"},
	}
	for _, p := range pairs {
		a, _, _ := NormalizeTitle(p[0])
		b, _, _ := NormalizeTitle(p[1])
		if a == b {
			t.Errorf("%q and %q share the key %q", p[0], p[1], a)
		}
	}
}
