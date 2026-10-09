package main

// Phase 20 merge with phase 17: the review API wiring failure is a closed
// event like every other author metadata record, never the error's text.

import (
	"strings"
	"testing"

	"gopds-api/api"
	"gopds-api/config"
	"gopds-api/logging"
	"gopds-api/services"

	//nolint:depguard // the test raises the logger to its most verbose level
	"github.com/sirupsen/logrus"
	//nolint:depguard // asserting on emitted log output needs logrus' own test hook, and logging wraps logrus
	logrustest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReviewAPIWiringFailureIsAClosedEvent(t *testing.T) {
	hook := logrustest.NewLocal(logging.GetLogger())
	t.Cleanup(hook.Reset)
	previous := logging.GetLogger().GetLevel()
	logging.GetLogger().SetLevel(logrus.TraceLevel)
	t.Cleanup(func() { logging.GetLogger().SetLevel(previous) })

	// Without a database the review service refuses to build; the switch is
	// off, so nothing else touches the missing pool.
	c := config.AuthorMetadataConfig{}
	wiringErr := api.SetAuthorMetadataReviewService(nil, &c)
	require.Error(t, wiringErr)
	hook.Reset()

	require.Nil(t, initializeAuthorMetadata(nil, t.TempDir(), &c))

	var notWired []*logrus.Entry
	for _, entry := range hook.AllEntries() {
		line, err := entry.String()
		require.NoError(t, err)
		assert.NotContains(t, line, wiringErr.Error(), "the error text is never logged")
		for _, v := range entry.Data {
			s, ok := v.(string)
			if ok {
				assert.NotContains(t, s, "database", "no field carries the error text")
			}
		}
		if entry.Message == string(services.AuthorMetadataEventReviewAPINotWired) {
			notWired = append(notWired, entry)
		}
		assert.True(t, strings.HasPrefix(entry.Message, "author_metadata."), "only closed events: %q", entry.Message)
	}
	require.Len(t, notWired, 1)
	assert.Equal(t, logrus.ErrorLevel, notWired[0].Level)
	assert.Equal(t, logrus.Fields{"stage": string(services.AuthorMetadataStageReview)}, notWired[0].Data)
}
