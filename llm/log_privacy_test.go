package llm

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

// The model is handed the reader's words and hands back what it read out of
// them, so this one log line was the widest leak of the three: the query, the
// title and the author all went to the log verbatim. What it records now is
// the shape of the outcome.

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

func TestLogProcessedQueryKeepsTheReadersWordsOut(t *testing.T) {
	hook := logrustest.NewLocal(logging.GetLogger())
	t.Cleanup(hook.Reset)

	const query = "найди мне что-нибудь Толстого про войну"
	command := &Command{Command: "find_book", Title: "Война и мир", Author: "Толстой"}

	logProcessedQuery(query, command)

	out := loggedText(hook)
	require.NotEmpty(t, out, "the call must still log something")
	assert.NotContains(t, out, query, "the query reached the log verbatim")
	for _, leaked := range []string{"Война и мир", "Толстой", "войну"} {
		assert.NotContains(t, out, leaked, "%q reached the log", leaked)
	}

	// What stays is what a dashboard can act on: which command was recognized,
	// how long the query was, and whether the model filled each field.
	assert.Contains(t, out, "find_book")
	assert.Contains(t, out, "39 query runes")
	assert.Contains(t, out, "title: true")
	assert.Contains(t, out, "author: true")
}

func TestLogProcessedQueryReportsEmptyFieldsAsEmpty(t *testing.T) {
	hook := logrustest.NewLocal(logging.GetLogger())
	t.Cleanup(hook.Reset)

	logProcessedQuery("привет", &Command{Command: "unknown"})

	out := loggedText(hook)
	assert.Contains(t, out, "title: false")
	assert.Contains(t, out, "author: false")
	assert.Contains(t, out, "6 query runes")
}
