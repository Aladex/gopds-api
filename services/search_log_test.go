package services

import (
	"context"
	"errors"
	"testing"

	"gopds-api/logging"
	"gopds-api/models"

	logrustest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One completion entry per service call is the search observability contract:
// mode, query size in runes, correlation hash, language, scope, page size,
// total, duration and error class — and never the raw query text.
func TestSearchServiceLogsCompletion(t *testing.T) {
	repoErr := errors.New("repository exploded")

	cases := []struct {
		name        string
		repoErr     error
		call        func(svc PublicSearch) error
		wantFields  map[string]interface{}
		absentText  string
		wantEntries int
	}{
		{
			name: "a successful book search logs once with the returned hash",
			call: func(svc PublicSearch) error {
				_, err := svc.SearchBooks(context.Background(), models.BookSearchRequest{
					Query: "война и мир", Limit: 10, Language: "ru",
				})
				return err
			},
			wantFields: map[string]interface{}{
				"mode":        "books",
				"query_runes": 11,
				"query_hash":  "deadbeef",
				"language":    "ru",
				"scope":       "none",
				"returned":    2,
				"total":       81,
				"error_class": "none",
			},
			absentText:  "война",
			wantEntries: 1,
		},
		{
			name: "a cyrillic query counts runes not bytes",
			call: func(svc PublicSearch) error {
				_, err := svc.SearchBooks(context.Background(), models.BookSearchRequest{Query: "ёж", Limit: 10})
				return err
			},
			wantFields: map[string]interface{}{
				"query_runes": 2,
				"error_class": "none",
			},
			absentText:  "ёж",
			wantEntries: 1,
		},
		{
			name: "the all-languages code logs as no filter",
			call: func(svc PublicSearch) error {
				_, err := svc.SearchBooks(context.Background(), models.BookSearchRequest{Query: "война", Limit: 10, Language: allLanguages})
				return err
			},
			wantFields: map[string]interface{}{
				"language":    "",
				"error_class": "none",
			},
			absentText:  "война",
			wantEntries: 1,
		},
		{
			name: "an author-scoped book search names the scope",
			call: func(svc PublicSearch) error {
				_, err := svc.SearchBooks(context.Background(), models.BookSearchRequest{Query: "война", Limit: 10, AuthorID: 7})
				return err
			},
			wantFields: map[string]interface{}{
				"scope":       "author",
				"error_class": "none",
			},
			absentText:  "война",
			wantEntries: 1,
		},
		{
			name:    "a repository failure logs the error class and an unavailable hash",
			repoErr: repoErr,
			call: func(svc PublicSearch) error {
				_, err := svc.SearchBooks(context.Background(), models.BookSearchRequest{Query: "война", Limit: 10})
				return err
			},
			wantFields: map[string]interface{}{
				"mode":        "books",
				"query_hash":  "unavailable",
				"error_class": "repository",
			},
			absentText:  "война",
			wantEntries: 1,
		},
		{
			name: "a validation rejection logs without reaching the repository",
			call: func(svc PublicSearch) error {
				_, err := svc.SearchBooks(context.Background(), models.BookSearchRequest{Query: "  "})
				return err
			},
			wantFields: map[string]interface{}{
				"mode":        "books",
				"query_hash":  "unavailable",
				"error_class": "validation",
				"returned":    0,
			},
			wantEntries: 1,
		},
		{
			name:    "a cancelled call logs the canceled class",
			repoErr: context.Canceled,
			call: func(svc PublicSearch) error {
				_, err := svc.SearchBooks(context.Background(), models.BookSearchRequest{Query: "война", Limit: 10})
				return err
			},
			wantFields: map[string]interface{}{
				"error_class": "canceled",
			},
			absentText:  "война",
			wantEntries: 1,
		},
		{
			name: "an author search logs its own mode",
			call: func(svc PublicSearch) error {
				_, err := svc.SearchAuthors(context.Background(), models.AuthorSearchRequest{Query: "толстой", Limit: 10})
				return err
			},
			wantFields: map[string]interface{}{
				"mode":        "authors",
				"query_runes": 7,
				"query_hash":  "deadbeef",
				"returned":    1,
				"total":       30,
				"error_class": "none",
			},
			absentText:  "толстой",
			wantEntries: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeSearchRepository{
				err:        tc.repoErr,
				bookPage:   models.BookSearchPage{Books: []models.Book{{}, {}}, Total: 81, QueryHash: "deadbeef"},
				authorPage: models.AuthorSearchPage{Authors: []models.Author{{}}, Total: 30, QueryHash: "deadbeef"},
			}
			svc := NewSearchService(repo)
			hook := logrustest.NewLocal(logging.GetLogger())
			defer hook.Reset()

			_ = tc.call(svc)

			entries := hook.AllEntries()
			require.Len(t, entries, tc.wantEntries)
			entry := entries[0]
			for key, want := range tc.wantFields {
				assert.Equal(t, want, entry.Data[key], "field %q", key)
			}
			assert.Contains(t, entry.Data, "duration_ms")
			if tc.absentText != "" {
				assert.NotContains(t, entry.Message, tc.absentText)
				for key, value := range entry.Data {
					if s, ok := value.(string); ok {
						assert.NotContains(t, s, tc.absentText, "field %q leaks the query", key)
					}
				}
			}
		})
	}
}
