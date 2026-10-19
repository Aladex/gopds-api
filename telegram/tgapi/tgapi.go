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
	"strings"
	"time"

	"github.com/go-telegram/bot"
)

// DefaultServerURL is the cloud Bot API.
const DefaultServerURL = "https://api.telegram.org"

// DefaultTimeout bounds one outgoing call when the Endpoint names no client.
// It covers a document upload of the cloud limit on a slow link.
const DefaultTimeout = 2 * time.Minute

// Endpoint is the Bot API server a bot talks to and the client it uses.
type Endpoint struct {
	// ServerURL is the Bot API base URL; empty means DefaultServerURL.
	ServerURL string
	// HTTPClient carries the calls; nil means a client with DefaultTimeout.
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

// Client returns the HTTP client calls go through.
func (e Endpoint) Client() *http.Client {
	if e.HTTPClient != nil {
		return e.HTTPClient
	}
	return &http.Client{Timeout: DefaultTimeout}
}

// Options are the SDK options that send a bot's calls to this endpoint. The
// client is set only when the endpoint names one, so the SDK's own default
// stays in force otherwise.
func (e Endpoint) Options() []bot.Option {
	opts := []bot.Option{bot.WithServerURL(e.Server())}
	if e.HTTPClient != nil {
		// The poll timeout only matters for getUpdates, which webhook bots
		// never call.
		opts = append(opts, bot.WithHTTPClient(time.Minute, e.HTTPClient))
	}
	return opts
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

// RedactURL removes the request URL from a transport error. Method URLs carry
// the bot token, and net/http puts the URL into the error text.
func RedactURL(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		urlErr.URL = "(redacted)"
	}
	return err
}
