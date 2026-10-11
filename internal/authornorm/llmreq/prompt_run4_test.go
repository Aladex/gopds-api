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
		`("Князев К. Милослав", "Ли В.Б."): never relabel it as given or family.`,
	} {
		if !strings.Contains(prose, rule) {
			t.Errorf("the system prompt does not state %q", rule)
		}
	}
}
