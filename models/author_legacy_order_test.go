package models

import "testing"

// A legacy catalog name, written «Фамилия Имя» by the scan, comes out «Имя
// Фамилия»; a name that does not read as a plain personal name is left as
// the catalog has it. The cases are names of the legacy fallback on prod.
func TestGivenNameFirstTurnsOnlyPlainNames(t *testing.T) {
	cases := []struct{ name, want string }{
		{"Эренбург Илья", "Илья Эренбург"},
		{"Брэдбери Рэй", "Рэй Брэдбери"},
		{"Генри O.", "O. Генри"},
		{"Дайер Ш. Н.", "Ш. Н. Дайер"},
		{"Зерцалов C.А", "C.А Зерцалов"},
		{"Meluch R. M.", "R. M. Meluch"},
		{"Пустынин Иван Кузьмич", "Иван Кузьмич Пустынин"},
		{"Прутков Козьма Петрович", "Козьма Петрович Прутков"},
		{"Любичев А. А.", "А. А. Любичев"},
		{"Богданов (составитель) Г.", "Г. Богданов (составитель)"},
		{"Мамин-Cибиряк Дмитрий", "Дмитрий Мамин-Cибиряк"},
		{"Щеpбаков Александр", "Александр Щеpбаков"},
		{"Константайн Дэ\u00adвид", "Дэ\u00adвид Константайн"},
		// A note in brackets stays with the word it follows.
		{"Зубков (ред.) Владимир", "Владимир Зубков (ред.)"},
		{"Британ (Илия Британ) Илья", "Илья Британ (Илия Британ)"},
		{"Turtledove (Editor) Harry", "Harry Turtledove (Editor)"},
		// Left as they are. Three words or more of any other shape may hold a
		// family name of two (books 514530 and 578341 on prod).
		{"Скотт Браун Дениз", "Скотт Браун Дениз"},
		{"Коложвари Гранпьер Эмиль", "Коложвари Гранпьер Эмиль"},
		{"Льюис Клайв Стейплз", "Льюис Клайв Стейплз"},
		{"Ганди Мохандас (Мохандус) Карамчанд", "Ганди Мохандас (Мохандус) Карамчанд"},
		{"Прутков Иван Сидорович Петров", "Прутков Иван Сидорович Петров"},
		{"Прутков А. Петрович", "Прутков А. Петрович"},
		{"Аш-Шанфара", "Аш-Шанфара"},
		{"Абу Нувас", "Абу Нувас"},
		{"Ибн Аль-Араби", "Ибн Аль-Араби"},
		{"Лe Гофф Жак", "Лe Гофф Жак"},
		{"Mazo de la Roche", "Mazo de la Roche"},
		{"Кевин Дж. Андерсон Фрэнк Xерберт", "Кевин Дж. Андерсон Фрэнк Xерберт"},
		{"Η. О. Лосский", "Η. О. Лосский"},
		{"Hocking Аманда Хоркинг", "Hocking Аманда Хоркинг"},
		{"Литагент Щепетнов Евгений", "Литагент Щепетнов Евгений"},
		{"АСТ (новый каталог ОСНОВНОЙ) Литагент", "АСТ (новый каталог ОСНОВНОЙ) Литагент"},
		{"Горкин А. П., гл. редактор", "Горкин А. П., гл. редактор"},
		{"Каст N/K", "Каст N/K"},
		{"Гуревич 3", "Гуревич 3"},
		{"(ed.) Stephen", "(ed.) Stephen"},
		{"Butlere(tulkotāja) Marta", "Butlere(tulkotāja) Marta"},
		{"Буджолд рис. Пашковского Лоис", "Буджолд рис. Пашковского Лоис"},
		{UnknownAuthorName, UnknownAuthorName},
		{"Сапфо", "Сапфо"},
		{"", ""},
	}
	for _, c := range cases {
		if got := GivenNameFirst(c.name); got != c.want {
			t.Errorf("GivenNameFirst(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

// A Russian book, or one of no language, writes its authors by their short
// names; a book of any other language by its own spelling.
func TestShowsShortNamesForRussianAndNoLanguage(t *testing.T) {
	for lang, want := range map[string]bool{
		"ru": true, "RU": true, "ru-RU": true, "ru_ru": true, " ru ": true, "": true,
		"uk": false, "en": false, "be": false, "rus": false, "bg": false,
	} {
		if got := ShowsShortNames(lang); got != want {
			t.Errorf("ShowsShortNames(%q) = %v, want %v", lang, got, want)
		}
	}
}
