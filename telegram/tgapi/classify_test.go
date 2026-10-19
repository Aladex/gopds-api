package tgapi_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gopds-api/telegram/tgapi"
	"gopds-api/telegram/tgtest"

	"github.com/go-telegram/bot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testToken = "123456789:AAH-secret-part-of-the-token-0123456789"

// call makes one real SDK call against the fake and classifies its error, so
// the classifier is pinned against what the SDK actually returns rather than
// against hand-built errors.
func call(t *testing.T, ep tgapi.Endpoint, method string) error {
	t.Helper()
	b, err := tgapi.New(testToken, ep)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	switch method {
	case "sendMessage":
		_, err = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: 4242, Text: "hello"})
	case "editMessageText":
		_, err = b.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: 4242, MessageID: 7, Text: "hello"})
	default:
		t.Fatalf("unsupported method %s", method)
	}
	return err
}

func TestClassifyAnswersOfTheBotAPI(t *testing.T) {
	cases := []struct {
		name   string
		method string
		reply  tgtest.Reply
		want   tgapi.Class
		wait   time.Duration
	}{
		{name: "success", method: "sendMessage", want: tgapi.ClassOK},
		{name: "edit to the same content", method: "editMessageText", reply: tgtest.NotModified(), want: tgapi.ClassNotModified},
		{name: "edited message is gone", method: "editMessageText",
			reply: tgtest.Error(http.StatusBadRequest, "Bad Request: message to edit not found"), want: tgapi.ClassMessageGone},
		{name: "message can no longer be edited", method: "editMessageText",
			reply: tgtest.Error(http.StatusBadRequest, "Bad Request: message can't be edited"), want: tgapi.ClassMessageGone},
		{name: "other bad request", method: "sendMessage",
			reply: tgtest.Error(http.StatusBadRequest, "Bad Request: chat not found"), want: tgapi.ClassBadRequest},
		{name: "file too large, untyped by the SDK", method: "sendMessage",
			reply: tgtest.Error(http.StatusRequestEntityTooLarge, "Request Entity Too Large"), want: tgapi.ClassBadRequest},
		{name: "not found", method: "sendMessage",
			reply: tgtest.Error(http.StatusNotFound, "Not Found"), want: tgapi.ClassBadRequest},
		{name: "blocked by the user", method: "sendMessage", reply: tgtest.Forbidden(""), want: tgapi.ClassForbidden},
		{name: "token rejected", method: "sendMessage",
			reply: tgtest.Error(http.StatusUnauthorized, "Unauthorized"), want: tgapi.ClassUnauthorized},
		{name: "rate limited", method: "sendMessage", reply: tgtest.TooManyRequests(7),
			want: tgapi.ClassRateLimited, wait: 7 * time.Second},
		{name: "server error", method: "sendMessage",
			reply: tgtest.Error(http.StatusInternalServerError, "Internal Server Error"), want: tgapi.ClassUnknown},
		{name: "proxy page instead of JSON", method: "sendMessage",
			reply: func(w http.ResponseWriter, _ *http.Request, _ *tgtest.Call) {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte("<html>502 Bad Gateway</html>"))
			}, want: tgapi.ClassUnknown},
		{name: "connection dropped after the body was read", method: "sendMessage",
			reply: tgtest.CloseAfterRead(), want: tgapi.ClassUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := tgtest.NewFakeBotAPI(t)
			if tc.reply != nil {
				fake.On(tgtest.Rule{Method: tc.method, Reply: tc.reply})
			}
			class, wait := tgapi.Classify(call(t, tgapi.Endpoint{ServerURL: fake.URL()}, tc.method))
			assert.Equal(t, tc.want, class, "class %s", class)
			assert.Equal(t, tc.wait, wait)
			require.Len(t, fake.Calls(tc.method), 1, "the call reached the fake exactly once")
		})
	}
}

// A 429 stays a 429 when callers wrap it; the SDK's IsTooManyRequestsError
// would not see it.
func TestClassifyWrappedRateLimit(t *testing.T) {
	fake := tgtest.NewFakeBotAPI(t)
	fake.On(tgtest.Rule{Method: "sendMessage", Reply: tgtest.TooManyRequests(3)})
	err := fmt.Errorf("delivering job 17: %w", call(t, tgapi.Endpoint{ServerURL: fake.URL()}, "sendMessage"))

	class, wait := tgapi.Classify(err)
	assert.Equal(t, tgapi.ClassRateLimited, class)
	assert.Equal(t, 3*time.Second, wait)
}

// A refused connection means nothing left the process: safe to repeat. The
// transport error names the URL, which carries the token, unless redacted.
func TestClassifyUnreachableServerAsNotSent(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()

	err := call(t, tgapi.Endpoint{ServerURL: srv.URL}, "sendMessage")
	class, _ := tgapi.Classify(err)
	assert.Equal(t, tgapi.ClassNotSent, class)

	// The SDK masks the token in its own errors; RedactURL is for direct
	// net/http calls, and must leave an SDK error intact.
	assert.NotContains(t, err.Error(), "secret-part")
	direct := tgapi.RedactURL(httpGet(srv.URL + "/bot" + testToken + "/getMe"))
	require.Error(t, direct)
	assert.NotContains(t, direct.Error(), "secret-part")
	class, _ = tgapi.Classify(direct)
	assert.Equal(t, tgapi.ClassNotSent, class)
}

func httpGet(u string) error {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req) // #nosec G107 -- a test URL on a closed local server
	if err == nil {
		_ = resp.Body.Close()
	}
	return err
}

// A call that outlives its deadline after the request was sent has an
// unknown outcome: the message may exist.
func TestClassifyTimeoutAfterSendAsUnknown(t *testing.T) {
	fake := tgtest.NewFakeBotAPI(t)
	fake.On(tgtest.Rule{Method: "sendMessage", Reply: fake.Hang(10 * time.Second)})
	ep := tgapi.Endpoint{ServerURL: fake.URL(), HTTPClient: &http.Client{Timeout: 200 * time.Millisecond}}

	class, _ := tgapi.Classify(call(t, ep, "sendMessage"))
	assert.Equal(t, tgapi.ClassUnknown, class)
}

func TestClassifyNilIsOK(t *testing.T) {
	class, wait := tgapi.Classify(nil)
	assert.Equal(t, tgapi.ClassOK, class)
	assert.Zero(t, wait)
}

func TestClassNames(t *testing.T) {
	for c := tgapi.ClassOK; c <= tgapi.ClassUnknown; c++ {
		name := c.String()
		assert.NotEmpty(t, name)
		assert.False(t, strings.HasPrefix(name, "class("), "class %d has no name", int(c))
	}
}

func TestEndpointDefaults(t *testing.T) {
	var ep tgapi.Endpoint
	assert.Equal(t, tgapi.DefaultServerURL, ep.Server())
	assert.Equal(t, "https://api.telegram.org/bot1:x/getMe", ep.MethodURL("1:x", "getMe"))
	assert.Equal(t, tgapi.DefaultTimeout, ep.Client().Timeout)

	ep.ServerURL = "http://127.0.0.1:8081/"
	assert.Equal(t, "http://127.0.0.1:8081/bot1:x/getMe", ep.MethodURL("1:x", "getMe"))
}
