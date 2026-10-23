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
