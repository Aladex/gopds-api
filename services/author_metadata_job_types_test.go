package services

import (
	"testing"
	"time"

	"gopds-api/database"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixedJitter(fraction float64) func(time.Duration) time.Duration {
	return func(limit time.Duration) time.Duration { return time.Duration(float64(limit) * fraction) }
}

func validPolicy() AuthorMetadataRetryPolicy {
	return AuthorMetadataRetryPolicy{
		MaxAttempts: 5,
		BaseDelay:   10 * time.Second,
		MaxDelay:    5 * time.Minute,
		Jitter:      fixedJitter(0),
	}
}

func TestAuthorMetadataRetryPolicyValidate(t *testing.T) {
	require.NoError(t, validPolicy().Validate())

	for name, mutate := range map[string]func(p *AuthorMetadataRetryPolicy){
		"zero attempts":     func(p *AuthorMetadataRetryPolicy) { p.MaxAttempts = 0 },
		"negative attempts": func(p *AuthorMetadataRetryPolicy) { p.MaxAttempts = -1 },
		"zero base":         func(p *AuthorMetadataRetryPolicy) { p.BaseDelay = 0 },
		"negative max":      func(p *AuthorMetadataRetryPolicy) { p.MaxDelay = -time.Second },
		"max below base":    func(p *AuthorMetadataRetryPolicy) { p.MaxDelay = time.Second },
		"no jitter source":  func(p *AuthorMetadataRetryPolicy) { p.Jitter = nil },
	} {
		p := validPolicy()
		mutate(&p)
		assert.ErrorIs(t, p.Validate(), ErrInvalidAuthorMetadataRetryPolicy, name)
	}
}

// The delay doubles per attempt up to the cap; the injected jitter picks a
// point in the upper half of that window, so the same jitter source always
// gives the same schedule.
func TestAuthorMetadataRetryPolicyDelay(t *testing.T) {
	p := validPolicy()

	p.Jitter = fixedJitter(0)
	for attempt, want := range map[int]time.Duration{
		0: 5 * time.Second, 1: 5 * time.Second, 2: 10 * time.Second, 3: 20 * time.Second,
		5: 80 * time.Second, 6: 150 * time.Second, 60: 150 * time.Second, 1 << 20: 150 * time.Second,
	} {
		assert.Equal(t, want, p.Delay(attempt), "attempt %d, no jitter", attempt)
	}

	p.Jitter = fixedJitter(1)
	assert.Equal(t, 10*time.Second, p.Delay(1))
	assert.Equal(t, 5*time.Minute, p.Delay(100))

	// A jitter source returning out-of-range values is clamped.
	p.Jitter = func(time.Duration) time.Duration { return -time.Hour }
	assert.Equal(t, 5*time.Second, p.Delay(1))
	p.Jitter = func(time.Duration) time.Duration { return time.Hour }
	assert.Equal(t, 10*time.Second, p.Delay(1))
}

// A failure carries the policy's delay for the attempt that failed and its
// attempt limit.
func TestAuthorMetadataRetryPolicyFailure(t *testing.T) {
	p := validPolicy()
	f := p.Failure(AuthorMetadataErrorTransientDatabase, 3)
	assert.Equal(t, database.LeaseFailure{
		ErrorClass:  "transient_database",
		RetryAfter:  20 * time.Second,
		MaxAttempts: 5,
	}, f)
}

// The closed error classes are exactly the shape the schema accepts, and the
// stages are exactly the leased streams.
func TestAuthorMetadataJobTypesAreClosed(t *testing.T) {
	for _, class := range AuthorMetadataErrorClasses() {
		assert.True(t, database.ValidLeaseErrorClass(string(class)), class)
	}
	assert.Contains(t, AuthorMetadataErrorClasses(), AuthorMetadataErrorMaxAttemptsExceeded)
	assert.Equal(t, database.LeaseErrorMaxAttemptsExceeded, string(AuthorMetadataErrorMaxAttemptsExceeded))
	assert.Equal(t, database.LeaseErrorLeaseExpired, string(AuthorMetadataErrorLeaseExpired))

	assert.Equal(t, database.LeaseStreamExtraction, AuthorMetadataStageExtraction.LeaseStream())
	assert.Equal(t, database.LeaseStreamLocalNormalization, AuthorMetadataStageLocalNormalization.LeaseStream())
	assert.Len(t, AuthorMetadataStages(), 2, "no LLM stage (scope amendment A1)")
}
