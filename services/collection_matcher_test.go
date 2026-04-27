package services

import (
	"context"
	"errors"
	"testing"

	"gopds-api/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizePair(t *testing.T) {
	tests := []struct {
		name      string
		author    string
		title     string
		wantAuth  string
		wantTitle string
	}{
		{
			name:      "lowercase and degree sign stripped",
			author:    "Рэй Брэдбери",
			title:     "451° по Фаренгейту",
			wantAuth:  "рэй брэдбери",
			wantTitle: "451 по фаренгейту",
		},
		{
			name:      "guillemets stripped",
			author:    "Лев Толстой",
			title:     "«Война и мир»",
			wantAuth:  "лев толстой",
			wantTitle: "война и мир",
		},
		{
			name:      "subtitle after colon trimmed and dot from initials removed",
			author:    "Дж. Оруэлл",
			title:     "1984: А-фантазия",
			wantAuth:  "дж оруэлл",
			wantTitle: "1984",
		},
		{
			name:      "subtitle after em dash trimmed",
			author:    "Маргарет Этвуд",
			title:     "Рассказ — Служанки",
			wantAuth:  "маргарет этвуд",
			wantTitle: "рассказ",
		},
		{
			name:      "extra whitespace and uppercase collapsed",
			author:    "  Лев   ТОЛСТОЙ  ",
			title:     "  Анна   Каренина  ",
			wantAuth:  "лев толстой",
			wantTitle: "анна каренина",
		},
		{
			name:      "empty inputs",
			author:    "",
			title:     "",
			wantAuth:  "",
			wantTitle: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, ti := normalizePair(tt.author, tt.title)
			assert.Equal(t, tt.wantAuth, a, "author")
			assert.Equal(t, tt.wantTitle, ti, "title")
		})
	}
}

func TestMatchOne_CacheHit(t *testing.T) {
	bookID := int64(42)
	finderCalled := false

	res, err := MatchOne(
		context.Background(),
		DecisionLookupFunc(func(ctx context.Context, a, t string) (*int64, error) {
			return &bookID, nil
		}),
		CandidateFinderFunc(func(ctx context.Context, a, t string) ([]models.MatchCandidate, error) {
			finderCalled = true
			return nil, nil
		}),
		"Лев Толстой", "Война и мир",
	)

	require.NoError(t, err)
	assert.Equal(t, models.MatchStatusManual, res.Status)
	require.NotNil(t, res.BookID)
	assert.Equal(t, int64(42), *res.BookID)
	assert.False(t, finderCalled, "candidate finder must not be called on cache hit")
}

func TestMatchOne_AutoMatched_SingleHighScoreCandidate(t *testing.T) {
	res, err := MatchOne(
		context.Background(),
		DecisionLookupFunc(func(ctx context.Context, a, t string) (*int64, error) {
			return nil, nil
		}),
		CandidateFinderFunc(func(ctx context.Context, a, t string) ([]models.MatchCandidate, error) {
			return []models.MatchCandidate{{BookID: 7, Score: 0.95}}, nil
		}),
		"Рэй Брэдбери", "451° по Фаренгейту",
	)

	require.NoError(t, err)
	assert.Equal(t, models.MatchStatusAutoMatched, res.Status)
	require.NotNil(t, res.BookID)
	assert.Equal(t, int64(7), *res.BookID)
	assert.InDelta(t, 0.95, float64(res.Score), 0.0001)
}

func TestMatchOne_Ambiguous_SingleMidScoreCandidate(t *testing.T) {
	res, err := MatchOne(
		context.Background(),
		DecisionLookupFunc(func(ctx context.Context, a, t string) (*int64, error) {
			return nil, nil
		}),
		CandidateFinderFunc(func(ctx context.Context, a, t string) ([]models.MatchCandidate, error) {
			return []models.MatchCandidate{{BookID: 9, Score: 0.6}}, nil
		}),
		"Some Author", "Some Title",
	)

	require.NoError(t, err)
	assert.Equal(t, models.MatchStatusAmbiguous, res.Status)
	assert.Nil(t, res.BookID, "ambiguous result must not pick a winner")
	require.Len(t, res.Candidates, 1)
	assert.Equal(t, int64(9), res.Candidates[0].BookID)
}

func TestMatchOne_Ambiguous_MultipleCandidates(t *testing.T) {
	res, err := MatchOne(
		context.Background(),
		DecisionLookupFunc(func(ctx context.Context, a, t string) (*int64, error) {
			return nil, nil
		}),
		CandidateFinderFunc(func(ctx context.Context, a, t string) ([]models.MatchCandidate, error) {
			return []models.MatchCandidate{
				{BookID: 1, Score: 0.9},
				{BookID: 2, Score: 0.88},
				{BookID: 3, Score: 0.7},
			}, nil
		}),
		"Some Author", "Some Title",
	)

	require.NoError(t, err)
	assert.Equal(t, models.MatchStatusAmbiguous, res.Status)
	assert.Nil(t, res.BookID)
	require.Len(t, res.Candidates, 3)
}

func TestMatchOne_NotFound_NoCandidates(t *testing.T) {
	res, err := MatchOne(
		context.Background(),
		DecisionLookupFunc(func(ctx context.Context, a, t string) (*int64, error) {
			return nil, nil
		}),
		CandidateFinderFunc(func(ctx context.Context, a, t string) ([]models.MatchCandidate, error) {
			return nil, nil
		}),
		"Unknown Author", "Unknown Title",
	)

	require.NoError(t, err)
	assert.Equal(t, models.MatchStatusNotFound, res.Status)
	assert.Nil(t, res.BookID)
	assert.Empty(t, res.Candidates)
}

func TestMatchOne_NotFound_AllCandidatesBelowMidThreshold(t *testing.T) {
	res, err := MatchOne(
		context.Background(),
		DecisionLookupFunc(func(ctx context.Context, a, t string) (*int64, error) {
			return nil, nil
		}),
		CandidateFinderFunc(func(ctx context.Context, a, t string) ([]models.MatchCandidate, error) {
			return []models.MatchCandidate{{BookID: 5, Score: 0.3}}, nil
		}),
		"Author", "Title",
	)

	require.NoError(t, err)
	assert.Equal(t, models.MatchStatusNotFound, res.Status)
	assert.Empty(t, res.Candidates)
}

func TestMatchOne_DecisionLookupError(t *testing.T) {
	_, err := MatchOne(
		context.Background(),
		DecisionLookupFunc(func(ctx context.Context, a, t string) (*int64, error) {
			return nil, errors.New("db down")
		}),
		CandidateFinderFunc(func(ctx context.Context, an, tn string) ([]models.MatchCandidate, error) {
			t.Fatal("finder must not be called when lookup fails")
			return nil, nil
		}),
		"Author", "Title",
	)
	assert.Error(t, err)
}

func TestMatchOne_CandidateFinderError(t *testing.T) {
	_, err := MatchOne(
		context.Background(),
		DecisionLookupFunc(func(ctx context.Context, a, t string) (*int64, error) {
			return nil, nil
		}),
		CandidateFinderFunc(func(ctx context.Context, a, t string) ([]models.MatchCandidate, error) {
			return nil, errors.New("query failed")
		}),
		"Author", "Title",
	)
	assert.Error(t, err)
}
