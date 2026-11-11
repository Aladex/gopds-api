package authortriage

import (
	"bufio"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The triage-v1 audit of batch 1 (design/review-triage/audit): the labeled
// rows of the steps triage-v2 guards, as testdata/batch1-labeled.tsv keeps
// them. Each row is replayed as the candidate it was: the proposal, and for
// steps 1 and 2 the stored answers that gave the name after it.

// labeledRow is one labeled row of the audit.
type labeledRow struct {
	id      int64
	step    Step
	class   string
	credits int
	form    string
	// verdict is same, different or unclear by either labeler, or pair: a
	// named pair read as one collective, by design (DECISION §4).
	verdict       string
	before, after Name
}

// nameField splits a rendered name ("given=…; family=…; kind=…") at its
// keys: a value may hold "; " itself.
var nameField = regexp.MustCompile(`(?:^|; )(given|additional|family|nickname|prefix|suffix|display|sort|kind)=`)

func parseName(t *testing.T, s string) Name {
	t.Helper()
	var n Name
	keys := nameField.FindAllStringSubmatchIndex(s, -1)
	require.NotEmpty(t, keys, s)
	for i, k := range keys {
		end := len(s)
		if i+1 < len(keys) {
			end = keys[i+1][0]
		}
		value := s[k[1]:end]
		switch s[k[2]:k[3]] {
		case "given":
			n.Given = value
		case "additional":
			n.Additional = value
		case "family":
			n.Family = value
		case "nickname":
			n.Nickname = value
		case "prefix":
			n.Prefix = value
		case "suffix":
			n.Suffix = value
		case "display":
			n.Display = value
		case "sort":
			n.Sort = value
		case "kind":
			n.Kind = value
		}
	}
	return n
}

func labeledRows(t *testing.T) []labeledRow {
	t.Helper()
	f, err := os.Open("testdata/batch1-labeled.tsv")
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	var rows []labeledRow
	sc := bufio.NewScanner(f)
	header := true
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		if header {
			header = false
			continue
		}
		cols := strings.Split(line, "\t")
		require.Len(t, cols, 8, line)
		id, err := strconv.ParseInt(cols[0], 10, 64)
		require.NoError(t, err)
		step, err := strconv.Atoi(cols[1])
		require.NoError(t, err)
		credits, err := strconv.Atoi(cols[3])
		require.NoError(t, err)
		rows = append(rows, labeledRow{id: id, step: Step(step), class: cols[2], credits: credits, form: cols[4],
			verdict: cols[5], before: parseName(t, cols[6]), after: parseName(t, cols[7])})
	}
	require.NoError(t, sc.Err())
	require.Len(t, rows, 513)
	return rows
}

// candidateOf is the candidate a labeled row was: steps 1 and 2 with the
// four or the judges' agreeing answers that gave the name after it, step 6
// a collective candidate the build reads as named, step 7 a mononym with its
// source.
func candidateOf(r *labeledRow) *Candidate {
	c := &Candidate{ItemID: r.id, Class: r.class, Reason: "ambiguous_decision", HasProposal: true,
		Credits: r.credits, Proposal: r.before}
	after := r.after
	switch r.step {
	case StepFourModel:
		c.Answers = fourModel(&after, r.form)
	case StepJudgesOnly:
		c.Answers = judgesOnly(&after, r.form)
	case StepAsNamedCollective:
		c.EntityRule = ruleAsNamed
	case StepKeepHandle:
		c.Source = &authorentity.Unselected{Credit: authorentity.Credit{ID: r.id, Fingerprint: "fp",
			Result: &authorentity.Result{Kind: authorentity.KindPerson, DisplayName: r.before.Display}},
			Class: r.class}
	case StepRules, StepSameName, StepPlaceholderOrg, StepParticleOnly:
		// The proposal alone: no labeled row of steps 3, 4 or 8 is here,
		// and step 5 reads nothing else.
	}
	return c
}

// The harness replays the audit: triage-v1 decides every labeled row as the
// batch did.
func TestLabeledRowsReplayTriageV1(t *testing.T) {
	for _, r := range labeledRows(t) {
		d := DecideV1(candidateOf(&r), &Env{})
		require.Equal(t, r.step, d.Step, "item %d", r.id)
		require.Equal(t, r.after, d.After, "item %d", r.id)
	}
}

// labeledFix is what triage-v2 makes of a labeled row: a refusal, or the
// name after its fixes.
type labeledFix struct {
	refusal Refusal
	after   *Name
}

func refused(r Refusal) labeledFix { return labeledFix{refusal: r} }

// fixedName is the row's name after with the guards' fixes.
func fixedName(given, additional, family, display, sort string) labeledFix {
	return labeledFix{after: &Name{Given: given, Additional: additional, Family: family, Display: display,
		Sort: sort, Kind: KindPerson}}
}

// labeledErrors are the rows a labeler called different or unclear that a
// guard of triage-v2 is meant to fix or refuse.
var labeledErrors = map[int64]labeledFix{
	// Step 1: look-alike letters folded.
	58244:  fixedName("Артур", "", "Хинеман", "Артур Хинеман", "Хинеман, Артур"),
	266271: refused(RefusedLookalikeWord), // «Capa Сил»: a whole look-alike word, refused rather than rewritten
	58235:  fixedName("Джин", "", "Родденберри", "Джин Родденберри", "Родденберри, Джин"),
	128729: fixedName("И", "", "Мантель-Нечко", "И Мантель-Нечко", "Мантель-Нечко, И"),
	318956: fixedName("В.", "", "Баландин", "В. Баландин", "Баландин, В."),
	25138:  fixedName("Ричард", "С.", "Хогланд", "Ричард С. Хогланд", "Хогланд, Ричард С."),
	// Step 1: a stray dot or comma.
	244412: fixedName("Чарлз", "", "Мессенджер", "Чарлз Мессенджер", "Мессенджер, Чарлз"),
	244411: fixedName("Дэйвид", "", "Исби", "Дэйвид Исби", "Исби, Дэйвид"),
	244413: fixedName("Джильберто", "", "Виллаэрмоза", "Джильберто Виллаэрмоза", "Виллаэрмоза, Джильберто"),
	314096: fixedName("Александр", "", "Райн", "Александр Райн", "Райн, Александр"),
	317181: fixedName("Роберт", "", "Блох", "Роберт Блох", "Блох, Роберт"),
	// Step 1: a word of the display missing from the parts.
	297402: refused(RefusedUnparsedWord), // «Kelley St. John»: St. in the display, family «John»
	// Step 1: a word of the display lost to the nickname.
	41864:  refused(RefusedLostToken), // «Игорь Numus» → «Игорь»
	303542: refused(RefusedLostToken), // «Константин СИшный» → «Константин»
	176630: refused(RefusedLostToken), // «Xxcoy Silentsigh» → «Xxcoy»
	310879: refused(RefusedLostToken), // «Максим Марин Пенчов Пелов» → «Максим»
	61993:  refused(RefusedLostToken), // «… Головкина (Римская-Корсакова)»
	63833:  refused(RefusedLostToken), // unclear: «Антонина Бересклет (Клименкова)»
	320684: refused(RefusedLostToken), // unclear: «Celeste Unknown» → «Celeste»
	// Step 2.
	244319: refused(RefusedLostToken),        // «ИЕРОМОНАХ ПАНТЕЛЕИМОН (ЛЕДИН)»: Ледин lost, the prefix in capitals
	314835: refused(RefusedLostToken),        // two pen names merged and dropped from the display
	320687: refused(RefusedLostToken),        // unclear: «Аноним Райкар» → «Райкар»
	321084: refused(RefusedLostToken),        // unclear: «Аноним Лера» → «Лера»
	318514: refused(RefusedStrayPunctuation), // «doc. K. Kalmikovs,»: the display ends in a comma
	// Step 5.
	240641: refused(RefusedRoleAsName),       // «Shane McKenzie, ed.»
	321342: refused(RefusedGenreList),        // «Epic, legends and tales»
	321168: refused(RefusedGenreList),        // «Эпосы, мифы, легенды и сказания»
	320714: refused(RefusedGenreList),        // «Эпосы, легенды и сказания»
	320908: refused(RefusedPlaceholderTitle), // «Автор неизвестен -- Эпосы, …»
	320154: refused(RefusedPlaceholderTitle), // «Автор неизвестен - Эпосы, …»
	// Step 6.
	321510: refused(RefusedOnePersonTitles),  // «Давид Царь и Пророк»
	321456: refused(RefusedOnePersonTitles),  // «Христофор Греческий и Датский»
	320032: refused(RefusedPlaceholderTitle), // «Автор неизвестен -- Эротика и секс»
	321347: refused(RefusedGenreList),        // «[ сборник ]»
	321338: refused(RefusedTitleShape),       // «Хитър Петър и змеят»
	320320: refused(RefusedTitleShape),       // «вълкът и лисицата»
	319966: refused(RefusedTitleShape),       // «Заек и лъв»
	320708: refused(RefusedTitleShape),       // «Козата и заекът»
	321508: refused(RefusedTitleShape),       // «Ловецът и неговият син»
	320781: refused(RefusedTitleShape),       // «Мъдрият Кемине и богатият бай»
	321449: refused(RefusedTitleShape),       // «сламката и въгленът»
	321098: refused(RefusedTitleShape),       // «Андрей-стрелеца и сват Наум»
	// Step 7.
	7946:   refused(RefusedListedName), // «Компьютерра»
	19579:  refused(RefusedListedName), // «РПЦ»
	316807: refused(RefusedListedName), // «Bībele»
	163908: refused(RefusedListedName), // «пользователь»
	179475: refused(RefusedListedName), // unclear: «Терра», the publisher or a handle
	141225: refused(RefusedShortCaps),  // unclear: «ИГСО»
}

// labeledUnguarded are the rows called different that no guard of
// triage-v2 is meant for, and why.
var labeledUnguarded = map[int64]string{
	161480: "an empty sort name: no error, the credit sorts by the file's last name (creditSortKey; coordinator ruling)",
	46814:  "an empty sort name: no error, the credit sorts by the file's last name (creditSortKey; coordinator ruling)",
	319117: "«Папа Римский Франциск»: the name only in the display and the sort",
	311558: "«Махант Аведьянатх»: the name only in the display and the sort",
	47904:  "«Онкен-мл.»: the generational suffix kept inside the family name",
	209661: "«Ursu,a K. Le Guin»: a damaged person's name the organization test reads as a role after a comma",
	320863: "«Ум и Щастие»: a tale title whose words are capitalized like two names",
}

func TestTriageV2FixesOrRefusesTheLabeledErrors(t *testing.T) {
	seen := map[int64]bool{}
	for _, r := range labeledRows(t) {
		if r.verdict != "different" && r.verdict != "unclear" {
			continue
		}
		want, guarded := labeledErrors[r.id]
		_, unguarded := labeledUnguarded[r.id]
		if r.verdict == "different" {
			require.True(t, guarded || unguarded, "item %d is a labeled error no row of the table covers", r.id)
		}
		if !guarded {
			continue
		}
		seen[r.id] = true
		d := Decide(candidateOf(&r), &Env{})
		if want.refusal != "" {
			assert.Equal(t, Step(0), d.Step, "item %d is left", r.id)
			assert.Equal(t, want.refusal, d.Refusal, "item %d", r.id)
			assert.Equal(t, r.step, d.Evidence.RefusedStep, "item %d", r.id)
			assert.Equal(t, want.refusal, d.Evidence.Refusal, "item %d", r.id)
			continue
		}
		assert.Equal(t, r.step, d.Step, "item %d", r.id)
		assert.Empty(t, d.Refusal, "item %d", r.id)
		w := *want.after
		w.Nickname, w.Suffix = r.after.Nickname, r.after.Suffix
		assert.Equal(t, w, d.After, "item %d", r.id)
		assert.Equal(t, ActionEdit, d.Action, "item %d", r.id)
	}
	for id := range labeledErrors {
		assert.True(t, seen[id], "item %d of the table is no labeled error", id)
	}
}

// Why a row labeled same changes under triage-v2.
const (
	whyNickname   = "lost token: a nickname of more than one word, or with a bare number, left the display"
	whyAside      = "lost token: a part in parentheses or quotes left the display"
	whyLookalike  = "Cyrillic initials of a Latin name folded to Latin"
	whyTitleShape = "a pair with a lower-case word reads as a title: refused to the people"
	whyHeavy      = "a kept handle on 20 credits or more goes to the people"
	whyShortCaps  = "a kept handle of five capitals or fewer goes to the people"
)

// labeledSameChanged are the rows labeled same (or a named pair) that
// triage-v2 decides otherwise than triage-v1, and why.
var labeledSameChanged = map[int64]struct {
	fix    labeledFix
	reason string
}{
	12207:  {refused(RefusedLostToken), whyAside}, // "Г. А. Тюньдешев (Харамоос)"
	37767:  {fixedName("C.", "A.", "Ekshut", "C. A. Ekshut", "Ekshut, C. A."), whyLookalike},
	57332:  {refused(RefusedLostToken), whyNickname},    // "Михаил Валерин August Flieger"
	73567:  {refused(RefusedLostToken), whyNickname},    // "Doris May Lessing Little Dorrit"
	95444:  {refused(RefusedLostToken), whyNickname},    // "Николай Александрович Романов Николай II"
	101587: {refused(RefusedLostToken), whyNickname},    // "Отем Грей Grey Autumn"
	102622: {refused(RefusedLostToken), whyNickname},    // "Александр Михайлович Гуков Карась 72"
	208603: {refused(RefusedLostToken), whyAside},       // "Вильям (Уильям) Виллис (Уиллис)"
	241808: {refused(RefusedLostToken), whyAside},       // "Хардин Джесси «Волк»"
	314735: {refused(RefusedLostToken), whyNickname},    // "Алексис Опсокополос Alexis Opsokopolos"
	318676: {refused(RefusedLostToken), whyAside},       // "Сатеник (Сати) Жоровна Епремян"
	320685: {refused(RefusedLostToken), whyNickname},    // "Константин Владимирович Ткаченко К. и Т. Енко"
	311449: {refused(RefusedLostToken), whyAside},       // "Марк «Всплеск» Астон"
	321055: {refused(RefusedTitleShape), whyTitleShape}, // "Материалы православных и светских СМИ"
	321069: {refused(RefusedTitleShape), whyTitleShape}, // "Волкова Дарья и whiterose"
	10711:  {refused(RefusedHeavy), whyHeavy},           // "Radclyffe"
	18261:  {refused(RefusedHeavy), whyHeavy},           // "Менандр"
	25840:  {refused(RefusedShortCaps), whyShortCaps},   // "VS"
}

// The regression set: every row labeled same, or a named pair, keeps the
// decision triage-v1 made, except the rows of labeledSameChanged.
func TestTriageV2KeepsTheLabeledSameRows(t *testing.T) {
	for _, r := range labeledRows(t) {
		if r.verdict != "same" && r.verdict != "pair" {
			continue
		}
		d := Decide(candidateOf(&r), &Env{})
		change, changed := labeledSameChanged[r.id]
		if !changed {
			assert.Equal(t, r.step, d.Step, "item %d %q", r.id, r.before.Display)
			assert.Empty(t, d.Refusal, "item %d %q", r.id, r.before.Display)
			assert.Equal(t, r.after, d.After, "item %d %q", r.id, r.before.Display)
			continue
		}
		if change.fix.refusal != "" {
			assert.Equal(t, change.fix.refusal, d.Refusal, "item %d: %s", r.id, change.reason)
			continue
		}
		w := *change.fix.after
		w.Nickname, w.Suffix = r.after.Nickname, r.after.Suffix
		assert.Equal(t, r.step, d.Step, "item %d: %s", r.id, change.reason)
		assert.Equal(t, w, d.After, "item %d: %s", r.id, change.reason)
	}
}
