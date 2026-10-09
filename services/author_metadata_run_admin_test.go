package services

import (
	"context"
	"testing"
	"time"

	"gopds-api/config"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runAdminConfig() config.AuthorMetadataConfig {
	return config.AuthorMetadataConfig{
		MetadataMaxBytes:   config.AuthorMetadataMaxBytes,
		Extraction:         config.AuthorMetadataStageConfig{Concurrency: 1, ClaimSize: 10, Lease: time.Minute, MaxAttempts: 3},
		LocalNormalization: config.AuthorMetadataStageConfig{Concurrency: 1, ClaimSize: 10, Lease: time.Minute, MaxAttempts: 7},
	}
}

// The retry budgets are the configured workers' budgets, per stage.
func TestRunAdminTakesTheWorkersBudgets(t *testing.T) {
	c := runAdminConfig()
	admin, err := NewAuthorMetadataRunAdmin(NewAuthorMetadataRunService(&pg.DB{}), &pg.DB{}, &c)
	require.NoError(t, err)
	assert.Equal(t, 3, admin.extractionMaxAttempts)
	assert.Equal(t, 7, admin.localMaxAttempts)

	for _, broken := range []func(*config.AuthorMetadataConfig){
		func(c *config.AuthorMetadataConfig) { c.Extraction.MaxAttempts = 0 },
		func(c *config.AuthorMetadataConfig) { c.LocalNormalization.MaxAttempts = 0 },
	} {
		bad := runAdminConfig()
		broken(&bad)
		_, err = NewAuthorMetadataRunAdmin(NewAuthorMetadataRunService(&pg.DB{}), &pg.DB{}, &bad)
		assert.ErrorIs(t, err, ErrInvalidAuthorMetadataRunAdmin)
	}
	_, err = NewAuthorMetadataRunAdmin(nil, &pg.DB{}, &c)
	assert.ErrorIs(t, err, ErrInvalidAuthorMetadataRunAdmin)
	_, err = NewAuthorMetadataRunAdmin(NewAuthorMetadataRunService(&pg.DB{}), nil, &c)
	assert.ErrorIs(t, err, ErrInvalidAuthorMetadataRunAdmin)
	_, err = NewAuthorMetadataRunAdmin(NewAuthorMetadataRunService(&pg.DB{}), &pg.DB{}, nil)
	assert.ErrorIs(t, err, ErrInvalidAuthorMetadataRunAdmin)
}

// A class outside the stage's closed list is refused before any query: the
// admin's database here is an unconnected pool.
func TestRunAdminRetryRefusesClassesOutsideTheStage(t *testing.T) {
	c := runAdminConfig()
	admin, err := NewAuthorMetadataRunAdmin(NewAuthorMetadataRunService(&pg.DB{}), &pg.DB{}, &c)
	require.NoError(t, err)
	for _, tc := range []struct {
		stage AuthorMetadataStage
		class AuthorMetadataErrorClass
	}{
		{AuthorMetadataStageExtraction, AuthorMetadataErrorNormalizerFailed},
		{AuthorMetadataStageExtraction, AuthorMetadataErrorDatabaseInvariant},
		{AuthorMetadataStageLocalNormalization, AuthorMetadataErrorClass("invalid_fb2")},
		{AuthorMetadataStageLocalNormalization, AuthorMetadataErrorNoAuthorCredit},
		{AuthorMetadataStage("llm"), AuthorMetadataErrorTransientDatabase},
	} {
		_, err := admin.Retry(context.Background(), 1, tc.stage, tc.class)
		assert.ErrorIs(t, err, ErrInvalidRetryClass, "%s %s", tc.stage, tc.class)
	}
}
