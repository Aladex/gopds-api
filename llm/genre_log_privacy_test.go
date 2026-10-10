package llm

import (
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"gopds-api/config"
	"gopds-api/logging"

	//nolint:depguard // asserting on emitted log output needs logrus' own test hook, and logging wraps logrus
	logrustest "github.com/sirupsen/logrus/hooks/test"
)

// The genre title features log what an operator can act on — tag and title
// lengths, and how many taken names were excluded — never the tag, the model's
// title, the excluded names, the sample books or the API key: all of that is
// prompt, response or credential content.

func TestGenreTitleLogsCarryNoContent(t *testing.T) {
	hook := logrustest.NewLocal(logging.GetLogger())
	t.Cleanup(hook.Reset)

	const (
		canaryKey      = "canary-key-value"
		canaryTag      = "sf_canary_tag"
		canaryTitle    = "Канареечный заголовок"
		canaryExcluded = "Уже занято канарейкой"
	)

	okEndpoint := &llmEndpoint{}
	okSrv := okEndpoint.server(t, chatCompletionBody(canaryTitle), http.StatusOK)
	svc := NewLLMServiceWithClient(NewClient(config.LLMConfig{
		BaseURL: okSrv.URL,
		APIKey:  canaryKey,
		Timeout: 2 * time.Second,
	}))

	if got := svc.GenerateGenreTitleUnique(canaryTag,
		[]GenreBookContext{{Title: "Канарейка-книга", Authors: "Канареечный автор"}},
		[]string{canaryExcluded}); got != canaryTitle {
		t.Fatalf("GenerateGenreTitleUnique() = %q, want the model's title %q", got, canaryTitle)
	}

	// The failure path logs too; it must stay equally content-free.
	failEndpoint := &llmEndpoint{}
	failSrv := failEndpoint.server(t, "boom", http.StatusInternalServerError)
	failSvc := NewLLMServiceWithClient(NewClient(config.LLMConfig{
		BaseURL: failSrv.URL,
		APIKey:  canaryKey,
		Timeout: 2 * time.Second,
	}))
	if got := failSvc.GenerateGenreTitle(canaryTag); got != canaryTag {
		t.Fatalf("GenerateGenreTitle() = %q, want the tag itself on failure", got)
	}

	out := loggedText(hook)
	if out == "" {
		t.Fatal("the genre features must still log something")
	}
	for _, leaked := range []string{
		canaryKey, canaryTag, canaryTitle, canaryExcluded, "Канарейка-книга", "Канареечный автор",
	} {
		if strings.Contains(out, leaked) {
			t.Errorf("%q reached the log", leaked)
		}
	}

	// What stays is what a dashboard can act on: lengths and counts.
	if !strings.Contains(out, "tag 13 runes") {
		t.Errorf("the log no longer carries the tag length; logged:\n%s", out)
	}
	if !strings.Contains(out, "1 taken names excluded") {
		t.Errorf("the log no longer carries the excluded count; logged:\n%s", out)
	}
	if utf8.RuneCountInString(canaryTag) != 13 {
		t.Fatalf("test bug: canaryTag must be 13 runes for the assertion above")
	}
}
