package services

import (
	"testing"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmres"
)

// The role statistics' rules (design K, section 4, decision 8), on the
// shapes of the design's probes: the swapped Иванов | Сергей and the Chinese
// order of Лу | Синь.

func swapFields(first, middle, last string) []llmres.FieldValue {
	var out []llmres.FieldValue
	for _, f := range []struct {
		field llmres.Field
		text  string
	}{{llmres.FieldFirst, first}, {llmres.FieldMiddle, middle}, {llmres.FieldLast, last}} {
		if f.text != "" {
			out = append(out, llmres.FieldValue{Field: f.field, Text: f.text})
		}
	}
	return out
}

func swapTokensFor(t *testing.T, first, middle, last string) []swapToken {
	t.Helper()
	tokens, err := swapTokensOf(swapFields(first, middle, last))
	if err != nil {
		t.Fatalf("tokens: %v", err)
	}
	return tokens
}

// catalogStats counts natural credits: each given name in the first field
// and each family name in the last field n times, plus the extra credits.
func catalogStats(t *testing.T, n int, extra ...[3]string) roleStats {
	t.Helper()
	stats := roleStats{}
	for _, name := range [][2]string{{"Сергей", "Иванов"}, {"Ёлка", "Петров"}} {
		if err := stats.add(swapFields(name[0], "", name[1]), n); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range extra {
		if err := stats.add(swapFields(e[0], e[1], e[2]), 1); err != nil {
			t.Fatal(err)
		}
	}
	return stats
}

func TestRoleStatsCountSearchKeys(t *testing.T) {
	stats := catalogStats(t, 3, [3]string{"ИВАНОВ", "", "сергей"})
	if got := stats["иванов"]; got != (database.AuthorTokenRoleCounts{First: 1, Last: 3}) {
		t.Errorf("иванов = %+v", got)
	}
	// ё is е in the search key.
	if got := stats["елка"].First; got != 3 {
		t.Errorf("елка first = %d", got)
	}
}

func TestSwapCandidateByStatistics(t *testing.T) {
	swapped := swapTokensFor(t, "Иванов", "", "Сергей")
	// 10 natural credits and the swapped one: 10 of 11 is over 90%.
	stats := catalogStats(t, 10, [3]string{"Иванов", "", "Сергей"})
	rule, ok := swapCandidate(stats, swapped, "Cyrl")
	if !ok || rule != swapByStatistics {
		t.Fatalf("Иванов | Сергей is not a statistics candidate: %q %v", rule, ok)
	}
	// 8 of 9 is under 90%: no longer dominant.
	if _, ok := swapCandidate(catalogStats(t, 8, [3]string{"Иванов", "", "Сергей"}), swapped, "Cyrl"); ok {
		t.Error("a share under 90% made a candidate")
	}
	// 4 occurrences are under the support of 5, however clean.
	if _, ok := swapCandidate(catalogStats(t, 4), swapped, "Cyrl"); ok {
		t.Error("a support under 5 made a candidate")
	}
	// The natural order is no candidate.
	if _, ok := swapCandidate(stats, swapTokensFor(t, "Сергей", "", "Иванов"), "Cyrl"); ok {
		t.Error("Сергей | Иванов is a candidate")
	}
	// Every word of the first field must be a family name: one given name
	// among them is enough to refuse.
	if _, ok := swapCandidate(stats, swapTokensFor(t, "Иванов Сергей", "", "Сергей"), "Cyrl"); ok {
		t.Error("a first field with a given name in it is a candidate")
	}
}

func TestSwapCandidateChineseOrderIsNone(t *testing.T) {
	stats := catalogStats(t, 10, [3]string{"Лу", "", "Синь"})
	if _, ok := swapCandidate(stats, swapTokensFor(t, "Лу", "", "Синь"), "Cyrl"); ok {
		t.Error("Лу | Синь is a candidate: its words are rare")
	}
}

func TestSwapCandidateByMorphology(t *testing.T) {
	stats := roleStats{}
	rule, ok := swapCandidate(stats, swapTokensFor(t, "Петрович", "Иван", "Сидоров"), "Cyrl")
	if !ok || rule != swapByMorphology {
		t.Errorf("a patronymic in the first field is not a candidate: %q %v", rule, ok)
	}
	// In the last field the ending is mostly a family name: no candidate.
	if _, ok := swapCandidate(stats, swapTokensFor(t, "Роман", "", "Абрамович"), "Cyrl"); ok {
		t.Error("a family name ending in -ович in the last field is a candidate")
	}
	// Cyrillic only, and the ending alone is not a word.
	if _, ok := swapCandidate(stats, swapTokensFor(t, "Petrovich", "", "Smith"), "Latn"); ok {
		t.Error("a Latin word is a morphology candidate")
	}
	if _, ok := swapCandidate(stats, swapTokensFor(t, "Ович", "", "Сидоров"), "Cyrl"); ok {
		t.Error("the bare ending is a candidate")
	}
}

func TestSwapStatsAgree(t *testing.T) {
	stats := catalogStats(t, 10, [3]string{"Иванов", "", "Сергей"})
	swapped := swapTokensFor(t, "Иванов", "", "Сергей")
	swap := map[int]llmres.Role{0: llmres.RoleFamily, 1: llmres.RoleGiven}
	if !swapStatsAgree(stats, swapped, swap) {
		t.Error("the statistics do not agree with the swap of Иванов | Сергей")
	}
	// A name in its natural order agrees word by word, but there is nothing
	// to swap: the family name is not from the first field.
	natural := swapTokensFor(t, "Сергей", "", "Иванов")
	if swapStatsAgree(stats, natural, map[int]llmres.Role{0: llmres.RoleGiven, 1: llmres.RoleFamily}) {
		t.Error("the statistics agree with a swap of a name in its natural order")
	}
	// Without a first field there is no swap to agree with.
	if swapStatsAgree(stats, swapTokensFor(t, "", "", "Иванов"), map[int]llmres.Role{0: llmres.RoleFamily}) {
		t.Error("the statistics agree with a swap of a name without a first field")
	}
	// The answer that keeps the FB2 fields is not what they say.
	if swapStatsAgree(stats, swapped, map[int]llmres.Role{0: llmres.RoleGiven, 1: llmres.RoleFamily}) {
		t.Error("the statistics agree with the FB2 fields")
	}
	// Another restructure is not the swap either.
	if swapStatsAgree(stats, swapped, map[int]llmres.Role{0: llmres.RoleFamily, 1: llmres.RoleFamily}) {
		t.Error("the statistics agree with two family names")
	}
	// The models read Лу | Синь family first; the statistics are silent.
	lu := swapTokensFor(t, "Лу", "", "Синь")
	if swapStatsAgree(catalogStats(t, 10, [3]string{"Лу", "", "Синь"}), lu,
		map[int]llmres.Role{0: llmres.RoleFamily, 1: llmres.RoleGiven}) {
		t.Error("the statistics agree with a Chinese name they know nothing of")
	}
	// A middle word must be dominant in the middle field and keep its role.
	middle := catalogStats(t, 10, [3]string{"Иванов", "", "Сергей"})
	if err := middle.add(swapFields("Олег", "Петрович", "Смирнов"), 6); err != nil {
		t.Fatal(err)
	}
	withMiddle := swapTokensFor(t, "Иванов", "Петрович", "Сергей")
	if !swapStatsAgree(middle, withMiddle, map[int]llmres.Role{0: llmres.RoleFamily, 1: llmres.RoleAdditional, 2: llmres.RoleGiven}) {
		t.Error("a dominant middle word kept in its field does not agree")
	}
	if swapStatsAgree(middle, withMiddle, map[int]llmres.Role{0: llmres.RoleFamily, 1: llmres.RoleGiven, 2: llmres.RoleGiven}) {
		t.Error("a middle word moved to the given name agrees")
	}
}
