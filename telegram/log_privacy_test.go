package telegram

import (
	"fmt"
	"strings"
	"testing"

	"gopds-api/logging"

	//nolint:depguard // asserting on emitted log output needs logrus' own test hook, and logging wraps logrus
	logrustest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The search contract says no raw query text is logged. The service's own
// completion entry has always honored it; the bot did not — it wrote the
// reader's pending input and the whole running conversation verbatim. These
// pin the replacement: the log says a path was taken and how much text it
// carried, never what the text was.

func captureLog(t *testing.T) *logrustest.Hook {
	t.Helper()
	hook := logrustest.NewLocal(logging.GetLogger())
	t.Cleanup(hook.Reset)
	return hook
}

// loggedText joins every message and field value one capture produced, so an
// assertion cannot miss a leak that moved from the message into a field.
func loggedText(hook *logrustest.Hook) string {
	var b strings.Builder
	for _, entry := range hook.AllEntries() {
		b.WriteString(entry.Message)
		b.WriteByte('\n')
		for key, value := range entry.Data {
			fmt.Fprintf(&b, "%s=%v\n", key, value)
		}
	}
	return b.String()
}

func TestLogSearchContextKeepsTheConversationOut(t *testing.T) {
	hook := captureLog(t)

	// Two runes, one of them astral: the count has to be of code points, and
	// no fragment of the conversation may survive into the entry.
	const conversation = "я ищу книгу про подводные лодки 🚢"

	logSearchContext(4242, conversation)

	out := loggedText(hook)
	require.NotEmpty(t, out, "the call must still log something")
	assert.NotContains(t, out, conversation, "the conversation reached the log verbatim")
	for _, word := range []string{"книгу", "лодки", "подводные"} {
		assert.NotContains(t, out, word, "%q from the conversation reached the log", word)
	}
	assert.Contains(t, out, "4242", "the reader id is what makes the entry useful")
	assert.Contains(t, out, "33 context runes", "the size must be counted in runes, not bytes")
}

func TestLogStatefulInputKeepsTheInputOut(t *testing.T) {
	hook := captureLog(t)

	const input = "Толстой Лев Николаевич"

	logStatefulInput(7, "waiting_for_author", input)

	out := loggedText(hook)
	assert.NotContains(t, out, input, "the reader's input reached the log verbatim")
	assert.NotContains(t, out, "Толстой", "part of the reader's input reached the log")
	assert.Contains(t, out, "waiting_for_author", "the state is what makes the entry useful")
	assert.Contains(t, out, "22 input runes")
}
