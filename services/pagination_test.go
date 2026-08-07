package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Not asking for a page size and asking for too large a one are different
// questions, and the search limit answered both with the fallback. Invisible
// today, because the fallback and the ceiling are both 100 — and a trap the
// moment they differ: a reader asking for 500 would get the default rather
// than the maximum, which is not what a clamp means and not what anyone
// changing the default would expect.
//
// These bounds are deliberately unequal, so the two paths are told apart by
// the test rather than by the values the search happens to use.
func TestClampLimitTellsUnaskedFromTooLarge(t *testing.T) {
	const (
		fallback = 10
		ceiling  = 100
	)

	for _, tc := range []struct {
		name  string
		limit int
		want  int
	}{
		{"not asked for", 0, fallback},
		{"negative is not a request either", -5, fallback},
		{"asked for more than the ceiling", 500, ceiling},
		{"asked for exactly the ceiling", 100, ceiling},
		{"asked for a usable size", 25, 25},
		{"asked for one row", 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, clampLimit(tc.limit, fallback, ceiling))
		})
	}
}

// The picker's fallback is its ceiling on purpose: not naming a size means
// the full picker. Passing both bounds says so rather than leaving it to a
// coincidence of two constants.
func TestSuggestionLimitFallsBackToTheFullPicker(t *testing.T) {
	assert.Equal(t, maxSuggestionLimit, normalizeSuggestionLimit(0))
	assert.Equal(t, maxSuggestionLimit, normalizeSuggestionLimit(maxSuggestionLimit+1))
	assert.Equal(t, 3, normalizeSuggestionLimit(3))
}

func TestSearchLimitUsesBothBounds(t *testing.T) {
	assert.Equal(t, defaultSearchLimit, normalizeLimit(0), "an unnamed limit takes the default")
	assert.Equal(t, maxSearchLimit, normalizeLimit(maxSearchLimit+1), "an oversized one takes the ceiling")
	assert.Equal(t, 7, normalizeLimit(7), "a usable one is left alone")
}
