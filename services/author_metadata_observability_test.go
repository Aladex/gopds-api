package services

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"sort"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/logging"

	//nolint:depguard // the test raises the logger to its most verbose level
	"github.com/sirupsen/logrus"
	//nolint:depguard // asserting on emitted log output needs logrus' own test hook, and logging wraps logrus
	logrustest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Phase 20 (REFACTOR): every new author-metadata component logs through one
// closed event shape. These pin the shape itself.

func TestAuthorMetadataEventNamesAreClosed(t *testing.T) {
	names := AuthorMetadataEventNames()
	require.NotEmpty(t, names)
	seen := map[AuthorMetadataEventName]bool{}
	pattern := regexp.MustCompile(`^author_metadata\.[a-z_]+$`)
	for _, name := range names {
		assert.Regexp(t, pattern, string(name))
		assert.False(t, seen[name], "duplicate event name %s", name)
		seen[name] = true
	}
}

func TestAuthorMetadataEventFieldsAreTheSafeSet(t *testing.T) {
	full := AuthorMetadataEvent{
		Name: AuthorMetadataEventExtractionItemCompleted, Stage: AuthorMetadataStageExtraction,
		RunID: 1, ItemID: 2, BookID: 3, JobID: 4, ReviewItemID: 5, AttemptNo: 6,
		Status: "extracted", Class: AuthorMetadataErrorTransientDatabase, SQLState: "23514",
		Worker: "extraction", Count: 7, PolicyVersion: 8,
	}
	keys := make([]string, 0)
	for key := range full.fields() {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	assert.Equal(t, []string{
		"attempt_no", "book_id", "class", "count", "item_id", "job_id", "policy_version",
		"review_item_id", "run_id", "sqlstate", "stage", "status", "worker",
	}, keys, "only identifiers, counts and closed values")

	sparse := AuthorMetadataEvent{Name: AuthorMetadataEventWorkersStarted, Count: 2}
	assert.Equal(t, logging.Fields{"count": 2}, sparse.fields(), "zero values are left out")
}

func TestLogAuthorMetadataEventWritesNameAndFields(t *testing.T) {
	hook := logrustest.NewLocal(logging.GetLogger())
	t.Cleanup(hook.Reset)
	previous := logging.GetLogger().GetLevel()
	logging.GetLogger().SetLevel(logrus.TraceLevel)
	t.Cleanup(func() { logging.GetLogger().SetLevel(previous) })

	LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{
		Name: AuthorMetadataEventLocalAttemptFailed, Stage: AuthorMetadataStageLocalNormalization,
		JobID: 9, AttemptNo: 2, Class: AuthorMetadataErrorNormalizerFailed,
	})
	require.Len(t, hook.AllEntries(), 1)
	entry := hook.LastEntry()
	assert.Equal(t, logrus.WarnLevel, entry.Level)
	assert.Equal(t, string(AuthorMetadataEventLocalAttemptFailed), entry.Message)
	assert.Equal(t, logrus.Fields{
		"stage": "local_normalization", "job_id": int64(9), "attempt_no": 2, "class": "normalizer_failed",
	}, entry.Data)
}

// Fix round 1 (review B1): every text field of an event is checked against its
// closed set when it is written. A value outside it — here source text — is
// replaced by a fixed sentinel; the original never reaches the log.
func TestEventDTOClosesEveryTextField(t *testing.T) {
	typ := reflect.TypeOf(AuthorMetadataEvent{})
	texts := 0
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Type.Kind() != reflect.String {
			continue
		}
		texts++
		t.Run(field.Name, func(t *testing.T) {
			hook := privacyLog(t)
			event := &AuthorMetadataEvent{
				Name: AuthorMetadataEventLocalJobCompleted, Stage: AuthorMetadataStageLocalNormalization,
				JobID: 1, Status: "review",
			}
			reflect.ValueOf(event).Elem().FieldByName(field.Name).SetString("Zqxcanary Private Person")
			LogAuthorMetadataEvent(AuthorMetadataEventInfo, event)
			require.Len(t, hook.AllEntries(), 1, "the event is still written")
			assert.Empty(t, leaks(t, hook), "source text through %s", field.Name)
		})
	}
	assert.Equal(t, 6, texts, "Name, Stage, Status, Class, SQLState, Worker")
}

// The public runner path: a stage worker whose name is source text cannot
// put it into the log as the worker label.
func TestRunnerCannotLogSourceTextAsWorkerLabel(t *testing.T) {
	hook := privacyLog(t)
	w := &fakeStageWorker{name: "Zqxcanary Private Person", err: errors.New("worker failed")}
	r := NewAuthorMetadataRunner(w)
	require.NoError(t, r.Start(context.Background(), func(context.Context) error { return nil }))
	runnerEventually(t, func() bool { return w.finished.Load() == 1 })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, r.Shutdown(ctx))
	assert.Empty(t, leaks(t, hook))
	stopped := eventsNamed(hook, AuthorMetadataEventWorkerStopped)
	require.Len(t, stopped, 1)
	assert.Equal(t, authorMetadataUnknownValue, stopped[0].Data["worker"])
}

// Known values pass through unchanged; the sentinels are what anything else
// becomes.
func TestEventDTOKeepsKnownValues(t *testing.T) {
	e := AuthorMetadataEvent{
		Name: AuthorMetadataEventExtractionItemRetried, Stage: AuthorMetadataStageExtraction,
		Status: "retry_scheduled", Class: AuthorMetadataErrorExtractionFailed, SQLState: "23514",
		Worker: "local_normalization-2",
	}
	f := e.fields()
	assert.Equal(t, "extraction", f["stage"])
	assert.Equal(t, "retry_scheduled", f["status"])
	assert.Equal(t, "extraction_failed", f["class"])
	assert.Equal(t, "23514", f["sqlstate"])
	assert.Equal(t, "local_normalization-2", f["worker"])
	for _, bad := range []string{"2351", "abcde", "235140", "Zqx12", "ALICE", "ZQXCA", "12345", "AB000"} {
		e.SQLState = bad
		assert.Equal(t, authorMetadataUnknownValue, e.fields()["sqlstate"], bad)
	}
	// A code outside the allow-list but in a standard SQLSTATE class is
	// reported as that class: two characters out of a closed list.
	for code, want := range map[string]string{"23999": "23000", "40ABC": "40000", "XXALI": "XX000", "P0003": "P0000"} {
		e.SQLState = code
		assert.Equal(t, want, e.fields()["sqlstate"], code)
	}
	for _, known := range []string{"23505", "23503", "23514", "40001", "40P01", "55P03", "57014", "08006"} {
		e.SQLState = known
		assert.Equal(t, known, e.fields()["sqlstate"], known)
	}
	for _, bad := range []string{"local_normalization-", "local_normalization-x", "local_normalization-1000", "extraction-1"} {
		e.Worker = bad
		assert.Equal(t, authorMetadataUnknownValue, e.fields()["worker"], bad)
	}
}

// The closed vocabulary of error classes lives in database (the aggregates
// read it); every class a component of services defines must be in it, so a
// new class cannot reach the log or the aggregates as "unknown"/"other".
func TestEveryErrorClassIsInTheClosedVocabulary(t *testing.T) {
	vocabulary := map[string]bool{}
	for _, class := range database.AuthorMetadataAttemptErrorClasses() {
		vocabulary[class] = true
	}
	defined := append(AuthorMetadataErrorClasses(), AuthorMetadataLocalWorkerErrorClasses()...)
	defined = append(defined,
		AuthorMetadataErrorArchiveUnreadable, AuthorMetadataErrorDatabaseInvariant, AuthorMetadataErrorVersionMismatch,
		AuthorMetadataErrorExtractorMisconfigured, AuthorMetadataErrorExtractionFailed,
	)
	for _, class := range defined {
		assert.True(t, vocabulary[string(class)], "%s missing from database.AuthorMetadataAttemptErrorClasses", class)
	}
}
