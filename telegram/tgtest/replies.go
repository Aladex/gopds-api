package tgtest

import (
	"net/http"
	"strconv"
	"time"
)

// OK answers {"ok":true,"result":result}.
func OK(result any) Reply {
	return func(w http.ResponseWriter, _ *http.Request, _ *Call) { writeResult(w, result) }
}

// Error answers a Bot API error with code and description, as Telegram does.
func Error(code int, description string) Reply {
	return func(w http.ResponseWriter, _ *http.Request, _ *Call) { writeError(w, code, description) }
}

// TooManyRequests answers 429 with parameters.retry_after.
func TooManyRequests(retryAfter int) Reply {
	return func(w http.ResponseWriter, _ *http.Request, _ *Call) {
		writeAnswer(w, http.StatusTooManyRequests, apiAnswer{
			ErrorCode:   http.StatusTooManyRequests,
			Description: "Too Many Requests: retry after " + strconv.Itoa(retryAfter),
			Parameters:  map[string]any{"retry_after": retryAfter},
		})
	}
}

// Forbidden answers 403, by default the way Telegram says the user blocked
// the bot.
func Forbidden(description string) Reply {
	if description == "" {
		description = "Forbidden: bot was blocked by the user"
	}
	return Error(http.StatusForbidden, description)
}

// notModified is Telegram's description of an edit that changes nothing.
const notModified = "Bad Request: message is not modified: specified new message content and reply markup " +
	"are exactly the same as a current content and reply markup of the message"

// NotModified answers an edit that changes nothing.
func NotModified() Reply {
	return Error(http.StatusBadRequest, notModified)
}

// Hang reads the request and then answers nothing until the client gives up
// or d passes, whichever comes first; after d it answers like a healthy
// Telegram would have. The request reached the server, so for the caller the
// outcome is unknown.
func (f *FakeBotAPI) Hang(d time.Duration) Reply {
	return func(w http.ResponseWriter, r *http.Request, c *Call) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(d):
		}
		f.defaultReply(w, r, c)
	}
}

// Gate holds the answer until gate is closed, then answers like a healthy
// Telegram — for a test to look at what happens while a call is in flight.
func (f *FakeBotAPI) Gate(gate <-chan struct{}) Reply {
	return func(w http.ResponseWriter, r *http.Request, c *Call) {
		select {
		case <-r.Context().Done():
			return
		case <-gate:
		}
		f.defaultReply(w, r, c)
	}
}

// CloseAfterRead reads the request and drops the connection without an
// answer: the body was delivered, the outcome is unknown.
func CloseAfterRead() Reply {
	return func(w http.ResponseWriter, _ *http.Request, _ *Call) { dropConnection(w) }
}

func dropConnection(w http.ResponseWriter) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		// HTTP/2: abort the stream instead.
		panic(http.ErrAbortHandler)
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		// HTTP/2 again: the writer supports Hijack, the connection does not.
		panic(http.ErrAbortHandler)
	}
	_ = conn.Close()
}
