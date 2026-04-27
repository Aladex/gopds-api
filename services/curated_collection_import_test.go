package services

import (
	"context"
	"errors"
	"testing"

	"gopds-api/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRepo is an in-memory CollectionRepo used by the orchestrator tests.
type fakeRepo struct {
	createCalls  []createCall
	addedItems   []models.PersistedCollectionItem
	statusCalls  []statusCall
	nextID       int64
	createErr    error
	addItemErr   error
	updateErr    error
}

type createCall struct {
	name      string
	sourceURL string
}

type statusCall struct {
	collectionID int64
	status       string
	importError  string
	stats        models.CollectionImportStats
}

func (r *fakeRepo) Create(ctx context.Context, name, sourceURL string) (int64, error) {
	r.createCalls = append(r.createCalls, createCall{name, sourceURL})
	if r.createErr != nil {
		return 0, r.createErr
	}
	r.nextID++
	return r.nextID, nil
}

func (r *fakeRepo) AddItem(ctx context.Context, collectionID int64, item models.PersistedCollectionItem) error {
	if r.addItemErr != nil {
		return r.addItemErr
	}
	r.addedItems = append(r.addedItems, item)
	return nil
}

func (r *fakeRepo) UpdateStatus(ctx context.Context, collectionID int64, status, importError string, stats models.CollectionImportStats) error {
	r.statusCalls = append(r.statusCalls, statusCall{collectionID, status, importError, stats})
	return r.updateErr
}

// staticMatcher returns a fixed result regardless of inputs.
type staticMatcher struct {
	res MatchResult
	err error
}

func (m *staticMatcher) MatchOne(ctx context.Context, author, title string) (MatchResult, error) {
	return m.res, m.err
}

// scriptedMatcher returns a sequence of results, one per call, in order.
type scriptedMatcher struct {
	results []MatchResult
	calls   int
}

func (m *scriptedMatcher) MatchOne(ctx context.Context, author, title string) (MatchResult, error) {
	if m.calls >= len(m.results) {
		return MatchResult{}, errors.New("scriptedMatcher: out of results")
	}
	res := m.results[m.calls]
	m.calls++
	return res, nil
}

func TestImport_RejectsEmptyName(t *testing.T) {
	repo := &fakeRepo{}
	_, err := Import(
		context.Background(),
		ImportParams{Items: []ImportItem{{Title: "T", Author: "A"}}},
		repo,
		&staticMatcher{res: MatchResult{Status: models.MatchStatusNotFound}},
	)

	assert.Error(t, err)
	assert.Empty(t, repo.createCalls, "skeleton must not be created when validation fails")
}

func TestImport_RejectsEmptyItems(t *testing.T) {
	repo := &fakeRepo{}
	_, err := Import(
		context.Background(),
		ImportParams{Name: "Antiutopias", Items: nil},
		repo,
		&staticMatcher{res: MatchResult{Status: models.MatchStatusNotFound}},
	)

	assert.Error(t, err)
	assert.Empty(t, repo.createCalls, "skeleton must not be created for empty items")
}

func TestImport_SingleAutoMatchedItem(t *testing.T) {
	repo := &fakeRepo{}
	bookID := int64(42)
	collectionID, err := Import(
		context.Background(),
		ImportParams{
			Name:      "My Selection",
			SourceURL: "https://example.com/sel/1",
			Items:     []ImportItem{{Title: "Foo", Author: "Bar"}},
		},
		repo,
		&staticMatcher{res: MatchResult{
			Status: models.MatchStatusAutoMatched,
			BookID: &bookID,
			Score:  0.95,
		}},
	)

	require.NoError(t, err)
	assert.Equal(t, int64(1), collectionID)

	require.Len(t, repo.createCalls, 1)
	assert.Equal(t, "My Selection", repo.createCalls[0].name)
	assert.Equal(t, "https://example.com/sel/1", repo.createCalls[0].sourceURL)

	require.Len(t, repo.addedItems, 1)
	persisted := repo.addedItems[0]
	assert.Equal(t, 0, persisted.Position)
	assert.Equal(t, "Foo", persisted.ExternalTitle)
	assert.Equal(t, "Bar", persisted.ExternalAuthor)
	assert.Equal(t, models.MatchStatusAutoMatched, persisted.MatchStatus)
	require.NotNil(t, persisted.BookID)
	assert.Equal(t, int64(42), *persisted.BookID)
	assert.InDelta(t, 0.95, float64(persisted.MatchScore), 0.0001)

	require.Len(t, repo.statusCalls, 1, "exactly one finalize call")
	assert.Equal(t, models.ImportStatusCompleted, repo.statusCalls[0].status)
	assert.Equal(t, models.CollectionImportStats{Matched: 1}, repo.statusCalls[0].stats)
}

func TestImport_ThreeMixedResults(t *testing.T) {
	repo := &fakeRepo{}
	autoID := int64(7)
	matcher := &scriptedMatcher{results: []MatchResult{
		{Status: models.MatchStatusAutoMatched, BookID: &autoID, Score: 0.9},
		{Status: models.MatchStatusAmbiguous, Candidates: []models.MatchCandidate{{BookID: 11, Score: 0.7}, {BookID: 12, Score: 0.65}}},
		{Status: models.MatchStatusNotFound},
	}}

	_, err := Import(
		context.Background(),
		ImportParams{
			Name: "Mix",
			Items: []ImportItem{
				{Title: "T1", Author: "A1"},
				{Title: "T2", Author: "A2"},
				{Title: "T3", Author: "A3"},
			},
		},
		repo,
		matcher,
	)

	require.NoError(t, err)
	require.Len(t, repo.addedItems, 3)
	assert.Equal(t, []int{0, 1, 2}, []int{
		repo.addedItems[0].Position,
		repo.addedItems[1].Position,
		repo.addedItems[2].Position,
	})
	assert.Equal(t, models.MatchStatusAutoMatched, repo.addedItems[0].MatchStatus)
	assert.Equal(t, models.MatchStatusAmbiguous, repo.addedItems[1].MatchStatus)
	assert.Equal(t, models.MatchStatusNotFound, repo.addedItems[2].MatchStatus)

	require.Len(t, repo.statusCalls, 1)
	assert.Equal(t, models.CollectionImportStats{Matched: 1, Ambiguous: 1, NotFound: 1}, repo.statusCalls[0].stats)
	assert.Equal(t, models.ImportStatusCompleted, repo.statusCalls[0].status)
}

func TestImport_CountsManualAsMatched(t *testing.T) {
	repo := &fakeRepo{}
	cachedID := int64(123)
	_, err := Import(
		context.Background(),
		ImportParams{Name: "n", Items: []ImportItem{{Title: "T", Author: "A"}}},
		repo,
		&staticMatcher{res: MatchResult{Status: models.MatchStatusManual, BookID: &cachedID, Score: 1}},
	)

	require.NoError(t, err)
	require.Len(t, repo.statusCalls, 1)
	assert.Equal(t, models.CollectionImportStats{Matched: 1}, repo.statusCalls[0].stats,
		"manual-from-cache must be counted as matched")
	assert.Equal(t, models.MatchStatusManual, repo.addedItems[0].MatchStatus)
}

func TestImport_MatcherErrorFinalizesAsFailed(t *testing.T) {
	repo := &fakeRepo{}
	_, err := Import(
		context.Background(),
		ImportParams{Name: "n", Items: []ImportItem{{Title: "T", Author: "A"}}},
		repo,
		&staticMatcher{err: errors.New("matcher boom")},
	)

	assert.Error(t, err)
	require.Len(t, repo.statusCalls, 1, "must finalize even on matcher error")
	assert.Equal(t, models.ImportStatusFailed, repo.statusCalls[0].status)
	assert.Contains(t, repo.statusCalls[0].importError, "matcher boom")
}

func TestImport_PersistsYearAsExtra(t *testing.T) {
	repo := &fakeRepo{}
	_, err := Import(
		context.Background(),
		ImportParams{Name: "n", Items: []ImportItem{{Title: "T", Author: "A", Year: 1953}}},
		repo,
		&staticMatcher{res: MatchResult{Status: models.MatchStatusNotFound}},
	)

	require.NoError(t, err)
	require.Len(t, repo.addedItems, 1)
	assert.Contains(t, string(repo.addedItems[0].ExternalExtra), `"year":1953`)
}

func TestImport_NoExtraWhenYearAndExtraEmpty(t *testing.T) {
	repo := &fakeRepo{}
	_, err := Import(
		context.Background(),
		ImportParams{Name: "n", Items: []ImportItem{{Title: "T", Author: "A"}}},
		repo,
		&staticMatcher{res: MatchResult{Status: models.MatchStatusNotFound}},
	)

	require.NoError(t, err)
	require.Len(t, repo.addedItems, 1)
	assert.Nil(t, repo.addedItems[0].ExternalExtra,
		"do not write empty JSON when no extra fields are present")
}
