package llmreq

import (
	"strings"
	"testing"
)

// The run-4 ambiguities the prompt settles: a Western middle name, a
// double surname and a nickname field that reads like a full name.
func TestSystemPromptSettlesTheRun4Ambiguities(t *testing.T) {
	prose := strings.Join(strings.Fields(SystemPrompt), " ")
	for _, rule := range []string{
		`Only the first given name is "given".`,
		`Western middle names too ("Дж. Майкл Стражински"`,
		`double surname is "family" twice ("Х Гарсия Мартинес"`,
		`Every token of the nickname field is "nickname"`,
		`("Князев К. Милослав", "Ли В.Б."): never relabel it as given or family`,
	} {
		if !strings.Contains(prose, rule) {
			t.Errorf("the system prompt does not state %q", rule)
		}
	}
}

// The R1 contract rules: every answer the review backlog showed the reply
// grammar could not encode has a rule here that the validators accept.
func TestSystemPromptStatesTheBacklogContract(t *testing.T) {
	prose := strings.Join(strings.Fields(SystemPrompt), " ")
	for _, rule := range []string{
		// A: a non-person form is its form; its roles are not used.
		`For every form other than "person" the roles are not used: label every token "filler".`,
		// B1, B2: filler is a closed list, labels and placeholder phrases included.
		`"filler" is only for words of these closed lists`,
		`Неизвестный автор, Автор неизвестен`,
		`под редакцией, переводчик, иллюстратор`,
		`an e-mail or web address. Any other word is never "filler"`,
		// C: a pen name alone is a person of nickname tokens.
		`A person written only as a pen name has only "nickname" tokens`,
		// D: a name in parentheses is the nickname.
		`A name in parentheses after the name`,
		`is "nickname", and its brackets are "separator"`,
		// E: a title after the given name is a suffix.
		`A title written after the given name, even inside the name, is "suffix"`,
		// V8: a particle word takes the family role or its field's role.
		`never takes the role "particle": it is "family" when it belongs to the family name`,
		// No case fix on the nickname field.
		`Never for a token of the nickname field, a particle, a title or an initial`,
	} {
		if !strings.Contains(prose, rule) {
			t.Errorf("the system prompt does not state %q", rule)
		}
	}
}

// The R3 contract rules: the stage-1 shapes of eval run 24 that every
// participant answered alike and the validators now accept.
func TestSystemPromptStatesTheStage1Contract(t *testing.T) {
	prose := strings.Join(strings.Fields(SystemPrompt), " ")
	for _, rule := range []string{
		// P3: the Bulgarian and Ukrainian placeholder phrases are filler.
		`Неизвестен автор, Невідомий автор`,
		// P1: a particle cut off to the end of a family-first name is family.
		`cut off to the end of a family-first name, "Морье Дафна Дю": "Морье" and "Дю" are "family"`,
		// V8: a first-field particle word is given, even as a second given name.
		`"given", even after another given name: "Джей Ди Кирк", "Абу Мухаммед аль-Касим"`,
		// P2: a glued one-word mononym.
		`A one-word name with a glued particle ("Аль-Фараздак") is "single_name".`,
	} {
		if !strings.Contains(prose, rule) {
			t.Errorf("the system prompt does not state %q", rule)
		}
	}
}
