package services

import (
	"context"
	"errors"
	"testing"

	"gopds-api/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSearchRepository records the requests it receives and answers with
// canned pages or errors. The service contract is about what reaches the
// repository and what comes back, so the fake never ranks or filters.
type fakeSearchRepository struct {
	bookReq     models.BookSearchRequest
	authorReq   models.AuthorSearchRequest
	suggReq     models.SuggestionRequest
	bookCalls   int
	authorCalls int
	suggCalls   int

	bookPage   models.BookSearchPage
	authorPage models.AuthorSearchPage
	suggResult models.SuggestionResult
	err        error

	// blockOnCtx makes SearchBooks wait for the caller's context to end,
	// which is how a real repository spends a canceled request.
	blockOnCtx bool
}

//nolint:gocritic // the port takes the request by value; this implements it
func (f *fakeSearchRepository) SearchBooks(ctx context.Context, req models.BookSearchRequest) (models.BookSearchPage, error) {
	f.bookCalls++
	f.bookReq = req
	if f.blockOnCtx {
		<-ctx.Done()
		return models.BookSearchPage{}, ctx.Err()
	}
	if f.err != nil {
		return models.BookSearchPage{}, f.err
	}
	page := f.bookPage
	page.Limit = req.Limit
	page.Offset = req.Offset
	return page, nil
}

func (f *fakeSearchRepository) SearchAuthors(ctx context.Context, req models.AuthorSearchRequest) (models.AuthorSearchPage, error) {
	f.authorCalls++
	f.authorReq = req
	if f.err != nil {
		return models.AuthorSearchPage{}, f.err
	}
	page := f.authorPage
	page.Limit = req.Limit
	page.Offset = req.Offset
	return page, nil
}

func (f *fakeSearchRepository) Suggestions(_ context.Context, req models.SuggestionRequest) (models.SuggestionResult, error) {
	f.suggCalls++
	f.suggReq = req
	if f.err != nil {
		return models.SuggestionResult{}, f.err
	}
	return f.suggResult, nil
}

func TestSearchServiceBookValidation(t *testing.T) {
	repoErr := errors.New("repository exploded")

	cases := []struct {
		name      string
		req       models.BookSearchRequest
		wantErr   error
		wantCalls int
		checkReq  func(t *testing.T, got models.BookSearchRequest)
	}{
		{
			name:      "an empty query without an exact book id is rejected",
			req:       models.BookSearchRequest{Query: ""},
			wantErr:   ErrEmptyQuery,
			wantCalls: 0,
		},
		{
			name:      "a whitespace-only query is empty after trimming",
			req:       models.BookSearchRequest{Query: " \t\n "},
			wantErr:   ErrEmptyQuery,
			wantCalls: 0,
		},
		{
			name: "an exact book id makes an empty query a navigation request",
			req:  models.BookSearchRequest{Query: "  ", ExactBookID: 42, Limit: 10},
			checkReq: func(t *testing.T, got models.BookSearchRequest) {
				assert.Equal(t, "", got.Query)
				assert.Equal(t, int64(42), got.ExactBookID)
			},
			wantCalls: 1,
		},
		{
			name: "the query reaches the repository trimmed",
			req:  models.BookSearchRequest{Query: "  война и мир  ", Limit: 10},
			checkReq: func(t *testing.T, got models.BookSearchRequest) {
				assert.Equal(t, "война и мир", got.Query)
			},
			wantCalls: 1,
		},
		{
			name: "an unnamed limit becomes the default page size",
			req:  models.BookSearchRequest{Query: "война"},
			checkReq: func(t *testing.T, got models.BookSearchRequest) {
				assert.Equal(t, defaultSearchLimit, got.Limit)
			},
			wantCalls: 1,
		},
		{
			name: "an oversized limit is clamped to the maximum",
			req:  models.BookSearchRequest{Query: "война", Limit: 500},
			checkReq: func(t *testing.T, got models.BookSearchRequest) {
				assert.Equal(t, maxSearchLimit, got.Limit)
			},
			wantCalls: 1,
		},
		{
			name: "a negative limit becomes the default page size",
			req:  models.BookSearchRequest{Query: "война", Limit: -5},
			checkReq: func(t *testing.T, got models.BookSearchRequest) {
				assert.Equal(t, defaultSearchLimit, got.Limit)
			},
			wantCalls: 1,
		},
		{
			name:      "a negative offset is invalid pagination",
			req:       models.BookSearchRequest{Query: "война", Limit: 10, Offset: -1},
			wantErr:   ErrInvalidPagination,
			wantCalls: 0,
		},
		{
			name: "all languages is no filter for the repository",
			req:  models.BookSearchRequest{Query: "война", Limit: 10, Language: allLanguages},
			checkReq: func(t *testing.T, got models.BookSearchRequest) {
				assert.Equal(t, "", got.Language)
			},
			wantCalls: 1,
		},
		{
			name: "a concrete language passes through",
			req:  models.BookSearchRequest{Query: "война", Limit: 10, Language: "ru"},
			checkReq: func(t *testing.T, got models.BookSearchRequest) {
				assert.Equal(t, "ru", got.Language)
			},
			wantCalls: 1,
		},
		{
			name:      "repository errors propagate",
			req:       models.BookSearchRequest{Query: "война", Limit: 10},
			wantErr:   repoErr,
			wantCalls: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeSearchRepository{err: tc.wantErr}
			if errors.Is(tc.wantErr, ErrEmptyQuery) || errors.Is(tc.wantErr, ErrInvalidPagination) {
				repo.err = nil
			}
			svc := NewSearchService(repo)

			_, err := svc.SearchBooks(context.Background(), tc.req)
			if tc.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.wantCalls, repo.bookCalls)
			if tc.checkReq != nil {
				tc.checkReq(t, repo.bookReq)
			}
		})
	}
}

// TestSearchServiceBookVisibilityGate pins the rule every client shares: the
// two flags that widen what a request may see belong to whoever moderates, not
// to whoever asks. Adapters only report identity; the service does the
// clearing, so a caller that forgets the check cannot silently show more.
func TestSearchServiceBookVisibilityGate(t *testing.T) {
	cases := []struct {
		name           string
		req            models.BookSearchRequest
		wantUnapproved bool
		wantHidden     bool
	}{
		{
			name: "a non-moderator never reaches the repository with widened visibility",
			req: models.BookSearchRequest{
				Query: "война", Limit: 10, Unapproved: true, IncludeHidden: true,
			},
			wantUnapproved: false,
			wantHidden:     false,
		},
		{
			name: "a declared moderator keeps both flags",
			req: models.BookSearchRequest{
				Query: "война", Limit: 10, Moderator: true, Unapproved: true, IncludeHidden: true,
			},
			wantUnapproved: true,
			wantHidden:     true,
		},
		{
			name: "an unflagged request stays plain even for a moderator",
			req: models.BookSearchRequest{
				Query: "война", Limit: 10, Moderator: true,
			},
			wantUnapproved: false,
			wantHidden:     false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeSearchRepository{}
			svc := NewSearchService(repo)

			_, err := svc.SearchBooks(context.Background(), tc.req)
			require.NoError(t, err)

			require.Equal(t, 1, repo.bookCalls)
			assert.Equal(t, tc.wantUnapproved, repo.bookReq.Unapproved)
			assert.Equal(t, tc.wantHidden, repo.bookReq.IncludeHidden)
		})
	}
}

// A Cyrillic query is counted in runes, never bytes: len("ёж") is 4 bytes but
// 2 runes, and any byte-based gate would misread it.
func TestSearchServiceCountsCyrillicInRunes(t *testing.T) {
	repo := &fakeSearchRepository{}
	svc := NewSearchService(repo)

	_, err := svc.SearchBooks(context.Background(), models.BookSearchRequest{Query: "ёж", Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, "ёж", repo.bookReq.Query)
	assert.Equal(t, 2, runeLength(repo.bookReq.Query))
}

func TestSearchServiceAuthorValidation(t *testing.T) {
	cases := []struct {
		name      string
		req       models.AuthorSearchRequest
		wantErr   error
		wantCalls int
		checkReq  func(t *testing.T, got models.AuthorSearchRequest)
	}{
		{
			name:      "an empty author query is rejected",
			req:       models.AuthorSearchRequest{Query: "  "},
			wantErr:   ErrEmptyQuery,
			wantCalls: 0,
		},
		{
			name: "the author query reaches the repository trimmed",
			req:  models.AuthorSearchRequest{Query: "  толстой  ", Limit: 10},
			checkReq: func(t *testing.T, got models.AuthorSearchRequest) {
				assert.Equal(t, "толстой", got.Query)
			},
			wantCalls: 1,
		},
		{
			name: "all languages is no filter for authors either",
			req:  models.AuthorSearchRequest{Query: "толстой", Limit: 10, Language: allLanguages},
			checkReq: func(t *testing.T, got models.AuthorSearchRequest) {
				assert.Equal(t, "", got.Language)
			},
			wantCalls: 1,
		},
		{
			name:      "a negative offset is invalid pagination",
			req:       models.AuthorSearchRequest{Query: "толстой", Limit: 10, Offset: -10},
			wantErr:   ErrInvalidPagination,
			wantCalls: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeSearchRepository{}
			svc := NewSearchService(repo)

			_, err := svc.SearchAuthors(context.Background(), tc.req)
			if tc.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.wantCalls, repo.authorCalls)
			if tc.checkReq != nil {
				tc.checkReq(t, repo.authorReq)
			}
		})
	}
}

// A canceled caller must surface context.Canceled, not a generic failure:
// adapters map the two to different outcomes.
func TestSearchServicePropagatesCancellation(t *testing.T) {
	repo := &fakeSearchRepository{blockOnCtx: true}
	svc := NewSearchService(repo)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := svc.SearchBooks(ctx, models.BookSearchRequest{Query: "война", Limit: 10})
		done <- err
	}()
	cancel()

	err := <-done
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// TestSearchServiceSuggestionsValidation pins the suggestions boundary: the
// three-rune minimum never reaches the repository, kind and language are
// normalized at the service, the compact limit is enforced, and repository
// failures travel back untouched.
func TestSearchServiceSuggestionsValidation(t *testing.T) {
	cases := []struct {
		name      string
		req       models.SuggestionRequest
		wantCalls int
		check     func(t *testing.T, req models.SuggestionRequest)
		wantErr   error
	}{
		{
			name:      "two runes never reach the repository",
			req:       models.SuggestionRequest{Query: "ёж", Kind: models.SuggestionAll},
			wantCalls: 0,
		},
		{
			name:      "three cyrillic runes reach the repository, trimmed",
			req:       models.SuggestionRequest{Query: "  ёжи  ", Kind: models.SuggestionAll},
			wantCalls: 1,
			check: func(t *testing.T, req models.SuggestionRequest) {
				assert.Equal(t, "ёжи", req.Query)
			},
		},
		{
			name:      "an empty kind means all",
			req:       models.SuggestionRequest{Query: "война"},
			wantCalls: 1,
			check: func(t *testing.T, req models.SuggestionRequest) {
				assert.Equal(t, models.SuggestionAll, req.Kind)
			},
		},
		{
			name:      "an unknown kind is rejected",
			req:       models.SuggestionRequest{Query: "война", Kind: "titles"},
			wantCalls: 0,
			wantErr:   ErrInvalidSuggestionKind,
		},
		{
			name:      "the all-languages code becomes no filter",
			req:       models.SuggestionRequest{Query: "война", Kind: models.SuggestionAll, Language: "all"},
			wantCalls: 1,
			check: func(t *testing.T, req models.SuggestionRequest) {
				assert.Equal(t, "", req.Language)
			},
		},
		{
			name:      "an unnamed limit becomes the compact default",
			req:       models.SuggestionRequest{Query: "война", Kind: models.SuggestionAll},
			wantCalls: 1,
			check: func(t *testing.T, req models.SuggestionRequest) {
				assert.Equal(t, 15, req.Limit)
			},
		},
		{
			name:      "an oversized limit clamps to the compact picker",
			req:       models.SuggestionRequest{Query: "война", Kind: models.SuggestionAll, Limit: 100},
			wantCalls: 1,
			check: func(t *testing.T, req models.SuggestionRequest) {
				assert.Equal(t, 15, req.Limit)
			},
		},
		{
			name:      "an explicit small limit survives",
			req:       models.SuggestionRequest{Query: "война", Kind: models.SuggestionBook, Limit: 5},
			wantCalls: 1,
			check: func(t *testing.T, req models.SuggestionRequest) {
				assert.Equal(t, 5, req.Limit)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeSearchRepository{}
			svc := NewSearchService(repo)

			result, err := svc.Suggestions(context.Background(), tc.req)

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, result.Suggestions, "an empty picker is [], never nil")
			}
			assert.Equal(t, tc.wantCalls, repo.suggCalls)
			if tc.check != nil {
				tc.check(t, repo.suggReq)
			}
		})
	}

	t.Run("a repository failure propagates", func(t *testing.T) {
		boom := errors.New("repository exploded")
		repo := &fakeSearchRepository{err: boom}
		svc := NewSearchService(repo)

		_, err := svc.Suggestions(context.Background(), models.SuggestionRequest{
			Query: "война", Kind: models.SuggestionAll,
		})

		require.ErrorIs(t, err, boom)
	})
}
