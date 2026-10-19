package tgapi

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/go-telegram/bot"
)

// Class is what a failed (or successful) Bot API call means for the caller.
type Class int

const (
	// ClassOK: the call succeeded.
	ClassOK Class = iota
	// ClassNotModified: an edit asked for what the message already shows.
	// Repeating an absolute action is normal, so this counts as success.
	ClassNotModified
	// ClassMessageGone: the message to edit or delete no longer exists or can
	// no longer be edited.
	ClassMessageGone
	// ClassBadRequest: Telegram received the request and refused it (400,
	// 404, 409, 413 and other answered client errors).
	ClassBadRequest
	// ClassForbidden: the bot may not write to this chat (blocked, deactivated).
	ClassForbidden
	// ClassUnauthorized: the token was rejected.
	ClassUnauthorized
	// ClassRateLimited: 429; wait RetryAfter before calling again.
	ClassRateLimited
	// ClassNotSent: the request provably never reached Telegram (it could not
	// be built, or the connection could not be opened). Safe to repeat.
	ClassNotSent
	// ClassUnknown: the request may have reached Telegram and the answer is
	// unknown — a timeout or a broken connection after the body was written,
	// a server error, an unreadable answer. Never repeat a send on this.
	ClassUnknown
)

var classNames = [...]string{
	ClassOK:           "ok",
	ClassNotModified:  "not_modified",
	ClassMessageGone:  "message_gone",
	ClassBadRequest:   "bad_request",
	ClassForbidden:    "forbidden",
	ClassUnauthorized: "unauthorized",
	ClassRateLimited:  "rate_limited",
	ClassNotSent:      "not_sent",
	ClassUnknown:      "unknown",
}

// String is the class name used in logs and metrics labels.
func (c Class) String() string {
	if c >= 0 && int(c) < len(classNames) {
		return classNames[c]
	}
	return fmt.Sprintf("class(%d)", int(c))
}

// The SDK's own messages for errors it does not type. They are the only
// handle on these cases, so they are pinned by tests against the SDK itself.
const (
	sdkBuildForm     = "error build request form for method"
	sdkCreateRequest = "error create request for method"
	sdkUntypedAnswer = "error response from telegram for method"
)

// Telegram's descriptions for the two edit outcomes that are not failures of
// the request itself.
var (
	notModifiedDescriptions = []string{"message is not modified"}
	messageGoneDescriptions = []string{
		"message to edit not found",
		"message can't be edited",
		"message to delete not found",
		"message can't be deleted",
	}
)

// Classify says what err from an SDK call means. The duration is the wait
// Telegram asked for, and is non-zero only for ClassRateLimited.
func Classify(err error) (Class, time.Duration) {
	if err == nil {
		return ClassOK, 0
	}

	// The SDK's own IsTooManyRequestsError is a bare type assertion and
	// misses wrapped errors.
	var tooMany *bot.TooManyRequestsError
	if errors.As(err, &tooMany) {
		return ClassRateLimited, time.Duration(tooMany.RetryAfter) * time.Second
	}
	var migrate *bot.MigrateError
	if errors.As(err, &migrate) {
		return ClassBadRequest, 0
	}

	switch {
	case errors.Is(err, bot.ErrorForbidden):
		return ClassForbidden, 0
	case errors.Is(err, bot.ErrorUnauthorized):
		return ClassUnauthorized, 0
	case errors.Is(err, bot.ErrorBadRequest):
		return classifyBadRequest(err.Error()), 0
	case errors.Is(err, bot.ErrorNotFound), errors.Is(err, bot.ErrorConflict):
		return ClassBadRequest, 0
	}

	msg := err.Error()
	if strings.Contains(msg, sdkBuildForm) || strings.Contains(msg, sdkCreateRequest) {
		return ClassNotSent, 0
	}
	if code, ok := untypedAnswerCode(msg); ok {
		if code >= 400 && code < 500 {
			return ClassBadRequest, 0
		}
		return ClassUnknown, 0
	}
	if neverLeft(err) {
		return ClassNotSent, 0
	}
	return ClassUnknown, 0
}

func classifyBadRequest(msg string) Class {
	lower := strings.ToLower(msg)
	for _, d := range notModifiedDescriptions {
		if strings.Contains(lower, d) {
			return ClassNotModified
		}
	}
	for _, d := range messageGoneDescriptions {
		if strings.Contains(lower, d) {
			return ClassMessageGone
		}
	}
	return ClassBadRequest
}

// untypedAnswerCode reads the HTTP-like code from the SDK's error for an
// answered call it does not type: "error response from telegram for method
// sendDocument, 413 Request Entity Too Large".
func untypedAnswerCode(msg string) (int, bool) {
	i := strings.Index(msg, sdkUntypedAnswer)
	if i < 0 {
		return 0, false
	}
	rest := msg[i+len(sdkUntypedAnswer):]
	comma := strings.Index(rest, ", ")
	if comma < 0 {
		return 0, false
	}
	var code int
	if _, err := fmt.Sscanf(rest[comma+2:], "%d", &code); err != nil {
		return 0, false
	}
	return code, true
}

// neverLeft reports whether a transport error happened before any byte of
// the request could reach Telegram: the connection was never opened. A
// canceled or expired context is not such a case — it cannot be told apart
// from one that ended while the body was in flight, so it stays unknown.
func neverLeft(err error) bool {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return false
	}
	var opErr *net.OpError
	if errors.As(urlErr.Err, &opErr) && opErr.Op == "dial" {
		return true
	}
	var dnsErr *net.DNSError
	return errors.As(urlErr.Err, &dnsErr)
}
