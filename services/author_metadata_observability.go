package services

import (
	"strconv"
	"strings"

	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/logging"
	"gopds-api/models"
)

// The one log shape of the author metadata pipeline (plan phase 20, contract
// 3.14). Every new component — the extraction and local workers, the runner,
// the live dual write, the review service — logs an AuthorMetadataEvent and
// nothing else: a closed event name as the message and only identifiers,
// counts and closed values as fields. There is deliberately no way to pass an
// error, free text or an arbitrary map: an error message can quote a source
// name, a title or a database row, so a failure is reported by its closed
// class and, for a PostgreSQL error, its SQLSTATE.

// AuthorMetadataEventName is a closed event name; it is the log message.
type AuthorMetadataEventName string

// The closed set of events.
const (
	// Extraction stream.
	AuthorMetadataEventExtractionItemCompleted AuthorMetadataEventName = "author_metadata.extraction_item_completed"
	AuthorMetadataEventExtractionItemRetried   AuthorMetadataEventName = "author_metadata.extraction_item_retried"
	AuthorMetadataEventExtractionLeaseLost     AuthorMetadataEventName = "author_metadata.extraction_lease_lost"
	AuthorMetadataEventExtractionRunPaused     AuthorMetadataEventName = "author_metadata.extraction_run_paused"
	AuthorMetadataEventExtractionRunEnded      AuthorMetadataEventName = "author_metadata.extraction_run_ended"
	AuthorMetadataEventExtractionWriteFailed   AuthorMetadataEventName = "author_metadata.extraction_write_failed"
	AuthorMetadataEventRunCompleted            AuthorMetadataEventName = "author_metadata.run_completed"
	// A full run's seeding ended: every book has its item and the run runs.
	AuthorMetadataEventRunSeeded AuthorMetadataEventName = "author_metadata.run_seeded"

	// Local normalization stream.
	AuthorMetadataEventLocalJobCompleted   AuthorMetadataEventName = "author_metadata.local_job_completed"
	AuthorMetadataEventLocalAttemptFailed  AuthorMetadataEventName = "author_metadata.local_attempt_failed"
	AuthorMetadataEventLocalLeaseLost      AuthorMetadataEventName = "author_metadata.local_lease_lost"
	AuthorMetadataEventLocalUndecidable    AuthorMetadataEventName = "author_metadata.local_result_undecidable"
	AuthorMetadataEventLocalInputsSettled  AuthorMetadataEventName = "author_metadata.local_inputs_settled"
	AuthorMetadataEventLocalInputsResolved AuthorMetadataEventName = "author_metadata.local_inputs_reconciled"
	// The credits an acceptance pass selected under the newest policy.
	AuthorMetadataEventAcceptanceApplied AuthorMetadataEventName = "author_metadata.acceptance_applied"

	// Live dual write.
	AuthorMetadataEventSourcePersisted AuthorMetadataEventName = "author_metadata.source_persisted"
	AuthorMetadataEventSourceSkipped   AuthorMetadataEventName = "author_metadata.source_skipped"
	// A rescan path could not refresh a book's source for a reason that is
	// not the document's; the legacy update went through.
	AuthorMetadataEventSourceRefreshFailed AuthorMetadataEventName = "author_metadata.source_refresh_failed"

	// Review.
	AuthorMetadataEventReviewAction       AuthorMetadataEventName = "author_metadata.review_action"
	AuthorMetadataEventReviewActionFailed AuthorMetadataEventName = "author_metadata.review_action_failed"
	AuthorMetadataEventReviewAPINotWired  AuthorMetadataEventName = "author_metadata.review_api_not_wired"

	// Runner and process lifecycle.
	AuthorMetadataEventWorkersStarted         AuthorMetadataEventName = "author_metadata.workers_started"
	AuthorMetadataEventWorkersStopped         AuthorMetadataEventName = "author_metadata.workers_stopped"
	AuthorMetadataEventWorkerStopped          AuthorMetadataEventName = "author_metadata.worker_stopped_unexpectedly"
	AuthorMetadataEventWorkerBatchFailed      AuthorMetadataEventName = "author_metadata.worker_batch_failed"
	AuthorMetadataEventWorkersDisabled        AuthorMetadataEventName = "author_metadata.workers_disabled"
	AuthorMetadataEventWorkersNotStarted      AuthorMetadataEventName = "author_metadata.workers_not_started"
	AuthorMetadataEventWorkersShutdownTimeout AuthorMetadataEventName = "author_metadata.workers_shutdown_timeout"
	AuthorMetadataEventRunsAPINotWired        AuthorMetadataEventName = "author_metadata.runs_api_not_wired"
	// A background refresh of a run's stored figures failed; the next read
	// starts another.
	AuthorMetadataEventAggregatesRefreshFailed AuthorMetadataEventName = "author_metadata.aggregates_refresh_failed"

	// Author display read model: a walk over the catalog ended; its count is
	// the books whose rows differed from their line.
	AuthorMetadataEventDisplayWalkFinished AuthorMetadataEventName = "author_metadata.display_walk_finished"
)

// AuthorMetadataEventNames lists every event.
func AuthorMetadataEventNames() []AuthorMetadataEventName {
	return []AuthorMetadataEventName{
		AuthorMetadataEventExtractionItemCompleted, AuthorMetadataEventExtractionItemRetried,
		AuthorMetadataEventExtractionLeaseLost, AuthorMetadataEventExtractionRunPaused,
		AuthorMetadataEventExtractionRunEnded, AuthorMetadataEventExtractionWriteFailed,
		AuthorMetadataEventRunCompleted, AuthorMetadataEventRunSeeded,
		AuthorMetadataEventLocalJobCompleted, AuthorMetadataEventLocalAttemptFailed,
		AuthorMetadataEventLocalLeaseLost, AuthorMetadataEventLocalUndecidable,
		AuthorMetadataEventLocalInputsSettled, AuthorMetadataEventLocalInputsResolved,
		AuthorMetadataEventAcceptanceApplied,
		AuthorMetadataEventSourcePersisted, AuthorMetadataEventSourceSkipped, AuthorMetadataEventSourceRefreshFailed,
		AuthorMetadataEventReviewAction, AuthorMetadataEventReviewActionFailed,
		AuthorMetadataEventReviewAPINotWired,
		AuthorMetadataEventWorkersStarted, AuthorMetadataEventWorkersStopped,
		AuthorMetadataEventWorkerStopped, AuthorMetadataEventWorkerBatchFailed,
		AuthorMetadataEventWorkersDisabled, AuthorMetadataEventWorkersNotStarted,
		AuthorMetadataEventWorkersShutdownTimeout, AuthorMetadataEventRunsAPINotWired,
		AuthorMetadataEventAggregatesRefreshFailed, AuthorMetadataEventDisplayWalkFinished,
	}
}

// Stages of events outside the two leased streams.
const (
	AuthorMetadataStageDualWrite AuthorMetadataStage = "dual_write"
	AuthorMetadataStageReview    AuthorMetadataStage = "review"
	AuthorMetadataStageRunner    AuthorMetadataStage = "runner"
	// AuthorMetadataStageAcceptance is the start-up acceptance pass.
	AuthorMetadataStageAcceptance AuthorMetadataStage = "acceptance"
	// AuthorMetadataStageDisplayModel is the author display read-model worker.
	AuthorMetadataStageDisplayModel AuthorMetadataStage = "display_model"
)

// AuthorMetadataEvent is one log record. Every field is an identifier, a
// count or a closed value; zero values are left out of the record.
type AuthorMetadataEvent struct {
	Name         AuthorMetadataEventName
	Stage        AuthorMetadataStage
	RunID        int64
	ItemID       int64
	BookID       int64
	JobID        int64
	ReviewItemID int64
	AttemptNo    int
	// Status is a closed value: an item status, a policy outcome or a
	// review action.
	Status string
	Class  AuthorMetadataErrorClass
	// SQLState is the SQLSTATE of a PostgreSQL error, never its message.
	SQLState string
	// Worker is a worker's fixed label.
	Worker string
	Count  int
	// PolicyVersion is an acceptance policy version.
	PolicyVersion int
}

// AuthorMetadataEventLevel is how loud an event is.
type AuthorMetadataEventLevel int

// Event levels.
const (
	AuthorMetadataEventDebug AuthorMetadataEventLevel = iota
	AuthorMetadataEventInfo
	AuthorMetadataEventWarn
	AuthorMetadataEventError
)

// The text fields are typed only for readability: a string type does not
// close its values, so every text value is checked against its closed set
// when the event is written. Anything else is replaced by a fixed sentinel —
// the original is never logged, not even in part.
const (
	// authorMetadataUnknownValue replaces a stage, status, class, SQLSTATE or
	// worker label outside its closed set.
	authorMetadataUnknownValue = "unknown"
	// authorMetadataUnknownEvent replaces an event name outside the set.
	authorMetadataUnknownEvent AuthorMetadataEventName = "author_metadata.unknown_event"
	// maxWorkerIndex bounds the index of a worker label.
	maxWorkerIndex = 999
)

func closedSet[T ~string](values ...T) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, v := range values {
		set[string(v)] = true
	}
	return set
}

var (
	knownEventNames = closedSet(AuthorMetadataEventNames()...)
	knownStages     = closedSet(
		AuthorMetadataStageExtraction, AuthorMetadataStageLocalNormalization,
		AuthorMetadataStageDualWrite, AuthorMetadataStageReview, AuthorMetadataStageRunner,
		AuthorMetadataStageAcceptance, AuthorMetadataStageDisplayModel,
	)
	knownClasses  = closedSet(database.AuthorMetadataAttemptErrorClasses()...)
	knownStatuses = authorMetadataEventStatuses()
)

// SQLSTATE is the one value an event takes from a database error, and a
// trigger can raise any five characters as its code — a short source title
// included. A well-formed code is therefore no evidence of a safe one: only
// the codes the pipeline classifies are written as they are, any other code
// of a standard class is written as that class's generic code (<class>000),
// and everything else is the sentinel.
var knownSQLStates = closedSet(
	"23000", "23001", "23502", "23503", "23505", "23514", "23P01", // integrity
	"22001", "22003", "22021", "22P02", // data
	"25P02",          // in failed transaction
	"40001", "40P01", // serialization, deadlock
	"42501", "42703", "42P01", // privilege, undefined column/table
	"53100", "53200", "53300", // disk, memory, connections
	"55P03",          // lock not available
	"57014", "57P01", // query canceled, admin shutdown
	"08000", "08001", "08003", "08006", // connection
	"P0001", // raise_exception
	"XX000", // internal error
)

// standardSQLStateClasses are the classes of the PostgreSQL error-code
// table (Appendix A).
var standardSQLStateClasses = closedSet(
	"00", "01", "02", "03", "08", "09", "0A", "0B", "0F", "0L", "0P", "0Z",
	"20", "21", "22", "23", "24", "25", "26", "27", "28", "2B", "2D", "2F",
	"34", "38", "39", "3B", "3D", "3F", "40", "42", "44", "53", "54", "55",
	"57", "58", "72", "F0", "HV", "P0", "XX",
)

// sqlStateLength is the length of an SQLSTATE code.
const sqlStateLength = 5

// sqlStateClassLength is the length of its class prefix.
const sqlStateClassLength = 2

func closedSQLState(code string) string {
	if knownSQLStates[code] {
		return code
	}
	if len(code) == sqlStateLength && standardSQLStateClasses[code[:sqlStateClassLength]] {
		return code[:sqlStateClassLength] + "000"
	}
	return authorMetadataUnknownValue
}

// retryScheduledStatus is the status of a failed attempt that will be retried.
const retryScheduledStatus = "retry_scheduled"

// authorMetadataEventStatuses is the closed set of event statuses: run item
// statuses, policy outcomes, review actions and the retry marker.
func authorMetadataEventStatuses() map[string]bool {
	set := closedSet(models.AuthorMetadataRunItemStatuses()...)
	for v := range closedSet(
		authornorm.OutcomeSelected, authornorm.OutcomeReview, authornorm.OutcomeUnresolved, authornorm.OutcomeInvalid,
	) {
		set[v] = true
	}
	for v := range closedSet(models.ReviewResolutions()...) {
		set[v] = true
	}
	set[retryScheduledStatus] = true
	return set
}

func closedValue(value string, known map[string]bool) string {
	if known[value] {
		return value
	}
	return authorMetadataUnknownValue
}

func closedEventName(name AuthorMetadataEventName) AuthorMetadataEventName {
	if knownEventNames[string(name)] {
		return name
	}
	return authorMetadataUnknownEvent
}

// closedWorkerLabel accepts the labels the runner's stages carry: the
// extraction stage's, the acceptance pass's, the LLM worker's and the author display
// model's own labels,
// and a local normalization loop's label with its index.
func closedWorkerLabel(label string) string {
	if label == string(AuthorMetadataStageExtraction) || label == string(AuthorMetadataStageAcceptance) ||
		label == authorLLMWorkerName || label == string(AuthorMetadataStageDisplayModel) {
		return label
	}
	index, ok := strings.CutPrefix(label, string(AuthorMetadataStageLocalNormalization)+"-")
	if !ok {
		return authorMetadataUnknownValue
	}
	n, err := strconv.Atoi(index)
	if err != nil || n < 0 || n > maxWorkerIndex || strconv.Itoa(n) != index {
		return authorMetadataUnknownValue
	}
	return label
}

func (e *AuthorMetadataEvent) fields() logging.Fields {
	f := logging.Fields{}
	put := func(key string, value interface{}, set bool) {
		if set {
			f[key] = value
		}
	}
	put("stage", closedValue(string(e.Stage), knownStages), e.Stage != "")
	put("run_id", e.RunID, e.RunID != 0)
	put("item_id", e.ItemID, e.ItemID != 0)
	put("book_id", e.BookID, e.BookID != 0)
	put("job_id", e.JobID, e.JobID != 0)
	put("review_item_id", e.ReviewItemID, e.ReviewItemID != 0)
	put("attempt_no", e.AttemptNo, e.AttemptNo != 0)
	put("status", closedValue(e.Status, knownStatuses), e.Status != "")
	put("class", closedValue(string(e.Class), knownClasses), e.Class != "")
	put("sqlstate", closedSQLState(e.SQLState), e.SQLState != "")
	put("worker", closedWorkerLabel(e.Worker), e.Worker != "")
	put("count", e.Count, e.Count != 0)
	put("policy_version", e.PolicyVersion, e.PolicyVersion != 0)
	return f
}

// AuthorMetadataSQLState is the SQLSTATE of a PostgreSQL error and empty for
// any other error: the only part of an error an event may carry.
func AuthorMetadataSQLState(err error) string { return sqlState(err) }

// LogAuthorMetadataEvent writes one event: its name as the message, its safe
// fields as the structured fields.
func LogAuthorMetadataEvent(level AuthorMetadataEventLevel, e *AuthorMetadataEvent) {
	entry := logging.WithFields(e.fields())
	name := string(closedEventName(e.Name))
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
