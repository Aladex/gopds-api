// Package tgapi is how the service talks to the Bot API: one place that
// decides which server a bot calls and what an outgoing error means.
//
// The SDK is used as a typed HTTP client only. Which server it reaches is an
// Endpoint, so tests point every bot at a fake Bot API (tgtest) and a Local
// Bot API server would be one setting. What a failed call means for the
// caller — harmless, rejected, rate-limited, never sent, or unknown — is
// Classify, because the delivery code must never repeat a send whose outcome
// it does not know.
package tgapi

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/go-telegram/bot"
)

// DefaultServerURL is the cloud Bot API.
const DefaultServerURL = "https://api.telegram.org"

// DefaultTimeout bounds one outgoing call when the Endpoint names no client.
// It covers a document upload of the cloud limit on a slow link.
const DefaultTimeout = 2 * time.Minute

// sdkDefaultTimeout is the timeout of the SDK's own default client, kept for
// bots built with Options alone.
const sdkDefaultTimeout = time.Minute

// Endpoint is the Bot API server a bot talks to and the client it uses.
type Endpoint struct {
	// ServerURL is the Bot API base URL; empty means DefaultServerURL.
	ServerURL string
	// HTTPClient carries the calls; nil means a client with DefaultTimeout.
	// Whichever it is, the calls never follow redirects (see Client).
	HTTPClient *http.Client
}

// Server returns the base URL, without a trailing slash.
func (e Endpoint) Server() string {
	if e.ServerURL == "" {
		return DefaultServerURL
	}
	return strings.TrimRight(e.ServerURL, "/")
}

// MethodURL is the URL of one Bot API method for token. It carries the token,
// so it must never be logged.
func (e Endpoint) MethodURL(token, method string) string {
	return e.Server() + "/bot" + token + "/" + method
}

// Client returns the HTTP client calls go through: the endpoint's own, or
// one with DefaultTimeout.
//
// It never follows a redirect. Classify calls a dial failure "not sent",
// which is true only of the first hop: a server that accepted a POST and
// redirected it elsewhere would make an accepted send look unsent, and a
// retry would repeat it. A redirect comes back to the caller as the answer.
func (e Endpoint) Client() *http.Client {
	return e.client(DefaultTimeout)
}

func (e Endpoint) client(timeout time.Duration) *http.Client {
	c := &http.Client{Timeout: timeout}
	if e.HTTPClient != nil {
		copied := *e.HTTPClient
		c = &copied
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}

// Options are the SDK options that send a bot's calls to this endpoint, with
// the SDK's own default timeout when the endpoint names no client.
func (e Endpoint) Options() []bot.Option {
	// The poll timeout only matters for getUpdates, which webhook bots never
	// call.
	return []bot.Option{
		bot.WithServerURL(e.Server()),
		bot.WithHTTPClient(time.Minute, e.client(sdkDefaultTimeout)),
	}
}

// New builds an SDK client for token on this endpoint, with Client() as its
// HTTP client. It makes no call: getMe is skipped, so building a client never
// depends on Telegram being reachable. extra options come after the
// endpoint's own.
func New(token string, e Endpoint, extra ...bot.Option) (*bot.Bot, error) {
	opts := []bot.Option{
		bot.WithServerURL(e.Server()),
		bot.WithHTTPClient(time.Minute, e.Client()),
		bot.WithSkipGetMe(),
	}
	return bot.New(token, append(opts, extra...)...)
}

// RedactURL removes the bot token from a transport error. Method URLs carry
// the token, and net/http names the URL in its errors.
//
// It is safe on wrapped errors: a wrapper made by fmt.Errorf keeps the text it
// was given, so the URL is cleared in the *url.Error and the returned error's
// text is rebuilt without the token. errors.Is and errors.As still see the
// original chain.
func RedactURL(err error) error {
	if err == nil {
		return nil
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		urlErr.URL = redactedURL
	}
	text := tokenInText.ReplaceAllString(err.Error(), "bot***")
	if text == err.Error() {
		return err
	}
	return &redactedError{text: text, err: err}
}

// redactedURL stands for a URL that named a token.
const redactedURL = "(redacted)"

// tokenInText matches the token part of a Bot API URL path.
var tokenInText = regexp.MustCompile(`bot\d+:[A-Za-z0-9_-]+`)

// redactedError is an error with its text cleaned and its chain kept.
type redactedError struct {
	text string
	err  error
}

func (e *redactedError) Error() string { return e.text }
func (e *redactedError) Unwrap() error { return e.err }
