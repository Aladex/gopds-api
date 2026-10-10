package services

import (
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/logging"
)

// The one log shape of the LLM author layer. Like AuthorMetadataEvent it has
// no way to carry free text: a closed event name, identifiers, a slot, closed
// classes, an HTTP status, counts and token numbers. Names, excerpts, prompts,
// replies and the endpoint's error texts never reach the log — a provider's
// error message can quote the request.

// AuthorLLMEventName is a closed event name; it is the log message.
type AuthorLLMEventName string

const (
	AuthorLLMEventCallSettled    AuthorLLMEventName = "author_llm.call_settled"
	AuthorLLMEventCallLeaseLost  AuthorLLMEventName = "author_llm.call_lease_lost"
	AuthorLLMEventCallFailed     AuthorLLMEventName = "author_llm.call_failed"
	AuthorLLMEventParticipantOff AuthorLLMEventName = "author_llm.participant_paused"
	// #nosec G101 -- an event name, not a credential
	AuthorLLMEventCheckPassed   AuthorLLMEventName = "author_llm.fingerprint_passed"
	AuthorLLMEventCheckFailed   AuthorLLMEventName = "author_llm.fingerprint_failed"
	AuthorLLMEventClaimFailed   AuthorLLMEventName = "author_llm.claim_failed"
	AuthorLLMEventDisabled      AuthorLLMEventName = "author_llm.disabled"
	AuthorLLMEventStarted       AuthorLLMEventName = "author_llm.started"
	AuthorLLMEventContextFailed AuthorLLMEventName = "author_llm.context_failed"
)

// AuthorLLMEventNames lists every event.
func AuthorLLMEventNames() []AuthorLLMEventName {
	return []AuthorLLMEventName{
		AuthorLLMEventCallSettled, AuthorLLMEventCallLeaseLost, AuthorLLMEventCallFailed,
		AuthorLLMEventParticipantOff, AuthorLLMEventCheckPassed, AuthorLLMEventCheckFailed,
		AuthorLLMEventClaimFailed, AuthorLLMEventDisabled, AuthorLLMEventStarted, AuthorLLMEventContextFailed,
	}
}

// AuthorLLMEvent is one log record; zero values are left out.
type AuthorLLMEvent struct {
	Name   AuthorLLMEventName
	CallID int64
	RunID  int64
	Slot   llmreq.Slot
	// Kind, Outcome and Class are closed values.
	Kind       string
	Outcome    string
	Class      string
	HTTPStatus int
	Items      int
	Answered   int
	Invalid    int
	Tokens     int64
	LatencyMS  int64
	// SQLState is the SQLSTATE of a database error, never its message.
	SQLState string
}

// authorLLMClosedValues is every text value an event may carry; anything
// else is written as the sentinel.
var authorLLMClosedValues = closedSet(
	// call kinds
	"jobs", "fingerprint",
	// call outcomes
	"answered", "http_error", "timeout", "transport_error", "malformed", "abandoned",
	// client and settle classes, pause reasons, context states, disabled reasons
	"auth", "quota_exhausted", "rate_limited", "bad_request", "server_error", "malformed_response", "canceled",
	"model_mismatch", "drift", "lease_expired", "attached", "irrelevant", "unavailable", "none",
	"llm_not_configured", "llm_participants_invalid", "budget_run", "budget_one_off", "budget_monthly",
	"archive_unreadable", "excerpt_unreadable",
)

var (
	knownAuthorLLMEvents = closedSet(AuthorLLMEventNames()...)
	knownAuthorLLMSlots  = closedSet(llmreq.Slots()...)
)

func (e *AuthorLLMEvent) fields() logging.Fields {
	f := logging.Fields{}
	put := func(key string, value interface{}, set bool) {
		if set {
			f[key] = value
		}
	}
	put("call_id", e.CallID, e.CallID != 0)
	put("run_id", e.RunID, e.RunID != 0)
	put("slot", closedValue(string(e.Slot), knownAuthorLLMSlots), e.Slot != "")
	put("kind", closedValue(e.Kind, authorLLMClosedValues), e.Kind != "")
	put("outcome", closedValue(e.Outcome, authorLLMClosedValues), e.Outcome != "")
	put("class", closedValue(e.Class, authorLLMClosedValues), e.Class != "")
	put("http_status", e.HTTPStatus, e.HTTPStatus != 0)
	put("items", e.Items, e.Items != 0)
	put("answered", e.Answered, e.Answered != 0)
	put("invalid", e.Invalid, e.Invalid != 0)
	put("tokens", e.Tokens, e.Tokens != 0)
	put("latency_ms", e.LatencyMS, e.LatencyMS != 0)
	put("sqlstate", closedSQLState(e.SQLState), e.SQLState != "")
	return f
}

// LogAuthorLLMEvent writes one event at the given level.
func LogAuthorLLMEvent(level AuthorMetadataEventLevel, e *AuthorLLMEvent) {
	entry := logging.WithFields(e.fields())
	name := string(e.Name)
	if !knownAuthorLLMEvents[name] {
		name = "author_llm.unknown_event"
	}
	switch level {
	case AuthorMetadataEventDebug:
		entry.Debug(name)
	case AuthorMetadataEventInfo:
		entry.Info(name)
	case AuthorMetadataEventWarn:
		entry.Warn(name)
	case AuthorMetadataEventError:
		entry.Error(name)
	default:
		entry.Error(name)
	}
}
