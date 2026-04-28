package services

import (
	"context"
	"strings"
	"unicode"

	"gopds-api/models"
)

// Similarity thresholds for collection import matcher.
// Recall-first since the LLM resolver does the actual disambiguation:
// MidThreshold is low enough to admit title-only prefix hits (where the
// author lane contributes 0), HighThreshold stays high so we never auto-pick
// a single weak result.
const (
	HighThreshold = 0.85
	MidThreshold  = 0.30
)

// MatchResult is the outcome of matching one external pair against the local catalog.
// Status is one of models.MatchStatus* constants.
type MatchResult struct {
	Status     string
	BookID     *int64
	Score      float32
	Candidates []models.MatchCandidate
}

// DecisionLookup looks up a previously-decided manual match for a normalized (author, title) pair.
type DecisionLookup interface {
	Lookup(ctx context.Context, authorNorm, titleNorm string) (*int64, error)
}

// DecisionLookupFunc adapts a plain function into a DecisionLookup.
type DecisionLookupFunc func(ctx context.Context, authorNorm, titleNorm string) (*int64, error)

func (f DecisionLookupFunc) Lookup(ctx context.Context, a, t string) (*int64, error) {
	return f(ctx, a, t)
}

// CandidateFinder runs similarity search over the local catalog and returns candidate books.
type CandidateFinder interface {
	FindCandidates(ctx context.Context, authorNorm, titleNorm string) ([]models.MatchCandidate, error)
}

// CandidateFinderFunc adapts a plain function into a CandidateFinder.
type CandidateFinderFunc func(ctx context.Context, authorNorm, titleNorm string) ([]models.MatchCandidate, error)

func (f CandidateFinderFunc) FindCandidates(ctx context.Context, a, t string) ([]models.MatchCandidate, error) {
	return f(ctx, a, t)
}

// normalizePair lowercases and strips noise from a raw (author, title) pair so that
// it can be compared against the cache and against trigram search results.
// Title is stripped of any subtitle after the first ":" or " — " (em dash with spaces),
// since real-life imports often carry editorial subtitles that the local catalog lacks.
func normalizePair(author, title string) (authorNorm, titleNorm string) {
	return normalizeText(author), normalizeText(trimSubtitle(title))
}

// trimSubtitle drops anything after the first ":" or " — " so that "1984: A Dystopia"
// is matched as "1984". Em dash without surrounding spaces is treated as a regular
// punctuation character and only collapsed into space by normalizeText.
func trimSubtitle(s string) string {
	if i := strings.Index(s, ":"); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, " — "); i >= 0 {
		s = s[:i]
	}
	return s
}

// normalizeText lowercases the input and replaces every run of non-letter/non-digit
// characters with a single space, then trims. This collapses punctuation, quotes
// («» ""), the degree sign and any other noise into uniform whitespace.
func normalizeText(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := true // suppress leading space
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevSpace = false
			continue
		}
		if !prevSpace {
			b.WriteByte(' ')
			prevSpace = true
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// MatchOne resolves one external (author, title) pair against the local catalog.
// It first consults the decision cache; on a cache miss it queries the candidate finder
// and buckets the result into one of (auto_matched / ambiguous / manual / not_found).
func MatchOne(ctx context.Context, dl DecisionLookup, cf CandidateFinder, author, title string) (MatchResult, error) {
	authorNorm, titleNorm := normalizePair(author, title)

	if id, err := dl.Lookup(ctx, authorNorm, titleNorm); err != nil {
		return MatchResult{}, err
	} else if id != nil {
		bookID := *id
		return MatchResult{
			Status: models.MatchStatusManual,
			BookID: &bookID,
			Score:  1,
		}, nil
	}

	candidates, err := cf.FindCandidates(ctx, authorNorm, titleNorm)
	if err != nil {
		return MatchResult{}, err
	}

	// Drop noise below the mid threshold so that 0.3-similarity hits do not
	// pollute the ambiguous bucket — they are effectively misses.
	meaningful := make([]models.MatchCandidate, 0, len(candidates))
	for _, c := range candidates {
		if c.Score >= MidThreshold {
			meaningful = append(meaningful, c)
		}
	}

	if len(meaningful) == 0 {
		return MatchResult{Status: models.MatchStatusNotFound}, nil
	}
	if len(meaningful) == 1 && meaningful[0].Score >= HighThreshold {
		bookID := meaningful[0].BookID
		return MatchResult{
			Status: models.MatchStatusAutoMatched,
			BookID: &bookID,
			Score:  meaningful[0].Score,
		}, nil
	}
	// Ambiguous: keep the top-N candidates and surface the best score so the
	// admin UI can show "we found these N books, score X — pick one".
	return MatchResult{
		Status:     models.MatchStatusAmbiguous,
		Score:      meaningful[0].Score,
		Candidates: meaningful,
	}, nil
}
