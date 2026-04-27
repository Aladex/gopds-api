package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"gopds-api/models"
)

// ImportItem is one row of a CSV / textarea paste, as parsed by the admin frontend.
type ImportItem struct {
	Title  string         `json:"title"`
	Author string         `json:"author"`
	Year   int            `json:"year,omitempty"`
	Extra  map[string]any `json:"extra,omitempty"`
}

// ImportParams is the request payload for creating a new curated collection from a list of items.
type ImportParams struct {
	Name      string       `json:"name"`
	SourceURL string       `json:"source_url,omitempty"`
	Items     []ImportItem `json:"items"`
}

// CollectionRepo abstracts persistence for curated collections so that the import
// orchestrator can be unit-tested without a database.
type CollectionRepo interface {
	Create(ctx context.Context, name, sourceURL string) (collectionID int64, err error)
	AddItem(ctx context.Context, collectionID int64, item models.PersistedCollectionItem) error
	UpdateStatus(ctx context.Context, collectionID int64, status, importError string, stats models.CollectionImportStats) error
}

// Matcher is the orchestrator-facing view of the matcher: a single per-item call.
type Matcher interface {
	MatchOne(ctx context.Context, author, title string) (MatchResult, error)
}

// MatcherFunc adapts a plain function into a Matcher.
type MatcherFunc func(ctx context.Context, author, title string) (MatchResult, error)

func (f MatcherFunc) MatchOne(ctx context.Context, author, title string) (MatchResult, error) {
	return f(ctx, author, title)
}

// Import orchestrates the full lifecycle of a curated-collection import synchronously:
// validate → create skeleton (status=importing) → match each item → persist → finalize.
// Returns the new collection ID. On matcher / repo errors after the skeleton has been
// created, the collection is finalized with status=failed; the caller still gets the ID
// so the admin UI can show the partial result.
//
// Use StartImport for the production async-mode entry point used by the admin handler.
func Import(ctx context.Context, params ImportParams, repo CollectionRepo, matcher Matcher) (int64, error) {
	if err := validateImport(params); err != nil {
		return 0, err
	}

	collectionID, err := repo.Create(ctx, params.Name, params.SourceURL)
	if err != nil {
		return 0, fmt.Errorf("create collection: %w", err)
	}

	stats, processErr := processItems(ctx, collectionID, params.Items, repo, matcher)
	if processErr != nil {
		_ = repo.UpdateStatus(ctx, collectionID, models.ImportStatusFailed, processErr.Error(), stats)
		return collectionID, processErr
	}
	if err := repo.UpdateStatus(ctx, collectionID, models.ImportStatusCompleted, "", stats); err != nil {
		return collectionID, fmt.Errorf("finalize collection: %w", err)
	}
	return collectionID, nil
}

// StartImport is the async-mode entry point. It validates input and creates the
// skeleton synchronously (so the handler can return 202 + collection_id), then
// processes items in a background goroutine and finalizes the collection there.
// The background context is detached from the request context so an aborted client
// does not cancel mid-import.
func StartImport(params ImportParams, repo CollectionRepo, matcher Matcher) (int64, error) {
	if err := validateImport(params); err != nil {
		return 0, err
	}
	collectionID, err := repo.Create(context.Background(), params.Name, params.SourceURL)
	if err != nil {
		return 0, fmt.Errorf("create collection: %w", err)
	}
	go func() {
		bg := context.Background()
		stats, procErr := processItems(bg, collectionID, params.Items, repo, matcher)
		if procErr != nil {
			_ = repo.UpdateStatus(bg, collectionID, models.ImportStatusFailed, procErr.Error(), stats)
			return
		}
		_ = repo.UpdateStatus(bg, collectionID, models.ImportStatusCompleted, "", stats)
	}()
	return collectionID, nil
}

// processItems is the per-item match+persist loop shared by Import and StartImport.
// Returns aggregated stats and the first error encountered; the caller is responsible
// for calling repo.UpdateStatus with the appropriate final status.
func processItems(ctx context.Context, collectionID int64, items []ImportItem, repo CollectionRepo, matcher Matcher) (models.CollectionImportStats, error) {
	var stats models.CollectionImportStats
	for i, in := range items {
		res, matchErr := matcher.MatchOne(ctx, in.Author, in.Title)
		if matchErr != nil {
			return stats, fmt.Errorf("match item %d: %w", i, matchErr)
		}

		extra, err := buildExtra(in, res.Candidates)
		if err != nil {
			return stats, fmt.Errorf("encode extra for item %d: %w", i, err)
		}

		persisted := models.PersistedCollectionItem{
			Position:       i,
			ExternalTitle:  in.Title,
			ExternalAuthor: in.Author,
			ExternalExtra:  extra,
			BookID:         res.BookID,
			MatchStatus:    res.Status,
			MatchScore:     res.Score,
		}
		if err := repo.AddItem(ctx, collectionID, persisted); err != nil {
			return stats, fmt.Errorf("save item %d: %w", i, err)
		}

		switch res.Status {
		case models.MatchStatusAutoMatched, models.MatchStatusManual:
			stats.Matched++
		case models.MatchStatusAmbiguous:
			stats.Ambiguous++
		case models.MatchStatusNotFound:
			stats.NotFound++
		}
	}
	return stats, nil
}

func validateImport(p ImportParams) error {
	if p.Name == "" {
		return errors.New("name is required")
	}
	if len(p.Items) == 0 {
		return errors.New("items must not be empty")
	}
	return nil
}

func buildExtra(in ImportItem, candidates []models.MatchCandidate) (json.RawMessage, error) {
	if in.Year == 0 && len(in.Extra) == 0 && len(candidates) == 0 {
		return nil, nil
	}
	payload := make(map[string]any, len(in.Extra)+2)
	for k, v := range in.Extra {
		payload[k] = v
	}
	if in.Year != 0 {
		payload["year"] = in.Year
	}
	if len(candidates) > 0 {
		// Store top-10 to keep the row narrow even when ambiguity is wide.
		top := candidates
		if len(top) > 10 {
			top = top[:10]
		}
		payload["candidates"] = top
	}
	return json.Marshal(payload)
}
