package services

import (
	"context"
	"encoding/json"

	"gopds-api/database"
	"gopds-api/models"
)

// CuratedCollectionRepo adapts package-level database functions to the
// CollectionRepo interface so the import service can persist via the real db.
type CuratedCollectionRepo struct{}

func (CuratedCollectionRepo) Create(ctx context.Context, name, sourceURL string) (int64, error) {
	return database.CreateCuratedCollection(ctx, name, sourceURL)
}

func (CuratedCollectionRepo) AddItem(ctx context.Context, collectionID int64, item models.PersistedCollectionItem) error {
	return database.AddCollectionItem(ctx, collectionID, item)
}

func (CuratedCollectionRepo) UpdateStatus(ctx context.Context, collectionID int64, status, importError string, stats models.CollectionImportStats) error {
	return database.UpdateCollectionImportStatus(ctx, collectionID, status, importError, stats)
}

// MatchDecisionLookup adapts database.LookupMatchDecision to DecisionLookup.
type MatchDecisionLookup struct{}

func (MatchDecisionLookup) Lookup(ctx context.Context, authorNorm, titleNorm string) (*int64, error) {
	return database.LookupMatchDecision(ctx, authorNorm, titleNorm)
}

// TrigramCandidateFinder adapts database.FindCollectionCandidates to CandidateFinder.
type TrigramCandidateFinder struct{}

func (TrigramCandidateFinder) FindCandidates(ctx context.Context, authorNorm, titleNorm string) ([]models.MatchCandidate, error) {
	return database.FindCollectionCandidates(ctx, authorNorm, titleNorm)
}

// NewCuratedMatcher wires DecisionLookup and CandidateFinder backed by the real db
// into a Matcher ready for use by Import.
func NewCuratedMatcher() Matcher {
	dl := MatchDecisionLookup{}
	cf := TrigramCandidateFinder{}
	return MatcherFunc(func(ctx context.Context, author, title string) (MatchResult, error) {
		return MatchOne(ctx, dl, cf, author, title)
	})
}

// CuratedCollectionsService implements the api-facing admin operations on top of
// the package-level db. StartImport delegates to StartImport (async + skeleton sync);
// the rest are thin wrappers around database functions.
type CuratedCollectionsService struct {
	Repo    CollectionRepo
	Matcher Matcher
}

// NewCuratedCollectionsService returns a service wired to the production DAO + matcher.
func NewCuratedCollectionsService() *CuratedCollectionsService {
	return &CuratedCollectionsService{
		Repo:    CuratedCollectionRepo{},
		Matcher: NewCuratedMatcher(),
	}
}

func (s *CuratedCollectionsService) StartImport(ctx context.Context, params ImportParams) (int64, error) {
	return StartImport(params, s.Repo, s.Matcher)
}

func (s *CuratedCollectionsService) List(ctx context.Context) ([]models.BookCollection, error) {
	return database.ListCuratedCollections(ctx)
}

func (s *CuratedCollectionsService) Get(ctx context.Context, id int64) (*models.BookCollection, error) {
	return database.GetCuratedCollection(ctx, id)
}

func (s *CuratedCollectionsService) ListItems(ctx context.Context, collectionID int64, statusFilter string, page, pageSize int) ([]models.BookCollectionItem, int, error) {
	return database.ListCollectionItems(ctx, collectionID, statusFilter, page, pageSize)
}

func (s *CuratedCollectionsService) Resolve(ctx context.Context, itemID, bookID int64, decidedByUserID *int64) error {
	item, err := database.GetCollectionItem(ctx, itemID)
	if err != nil {
		return err
	}
	if err := database.ResolveCollectionItem(ctx, itemID, bookID); err != nil {
		return err
	}
	authorNorm, titleNorm := normalizePair(item.ExternalAuthor, item.ExternalTitle)
	if authorNorm != "" || titleNorm != "" {
		if err := database.SaveMatchDecision(ctx, authorNorm, titleNorm, bookID, decidedByUserID); err != nil {
			return err
		}
	}
	return nil
}

func (s *CuratedCollectionsService) Ignore(ctx context.Context, itemID int64) error {
	return database.IgnoreCollectionItem(ctx, itemID)
}

func (s *CuratedCollectionsService) Update(ctx context.Context, id int64, patch database.CuratedCollectionPatch) error {
	return database.UpdateCuratedCollection(ctx, id, patch)
}

func (s *CuratedCollectionsService) Delete(ctx context.Context, id int64) error {
	return database.DeleteCuratedCollection(ctx, id)
}

// AutoResolveAmbiguous picks a top candidate for every ambiguous item in the
// collection and resolves it. The "top candidate" is the candidate with the
// highest score; on a tie the book registered most recently wins. Returns
// the number of items that were actually resolved.
func (s *CuratedCollectionsService) AutoResolveAmbiguous(ctx context.Context, collectionID int64, decidedByUserID *int64) (int, error) {
	const pageSize = 1000
	items, _, err := database.ListCollectionItems(ctx, collectionID, models.MatchStatusAmbiguous, 1, pageSize)
	if err != nil {
		return 0, err
	}
	resolved := 0
	for _, it := range items {
		bookID, err := pickAutoResolveTarget(ctx, it)
		if err != nil || bookID == 0 {
			continue
		}
		if err := s.Resolve(ctx, it.ID, bookID, decidedByUserID); err != nil {
			continue // best effort: skip this item, keep going
		}
		resolved++
	}
	return resolved, nil
}

// pickAutoResolveTarget reads candidates from external_extra and chooses the
// best one according to the rule "highest score, ties broken by most-recently
// registered book".
func pickAutoResolveTarget(ctx context.Context, it models.BookCollectionItem) (int64, error) {
	if len(it.ExternalExtra) == 0 {
		return 0, nil
	}
	var extra struct {
		Candidates []models.MatchCandidate `json:"candidates"`
	}
	if err := json.Unmarshal(it.ExternalExtra, &extra); err != nil {
		return 0, err
	}
	if len(extra.Candidates) == 0 {
		return 0, nil
	}

	maxScore := extra.Candidates[0].Score
	for _, c := range extra.Candidates {
		if c.Score > maxScore {
			maxScore = c.Score
		}
	}
	const tolerance = 0.001
	tied := make([]int64, 0, len(extra.Candidates))
	for _, c := range extra.Candidates {
		if c.Score >= maxScore-tolerance {
			tied = append(tied, c.BookID)
		}
	}
	return database.PickMostRecentBookID(ctx, tied)
}

// PublicCuratedCollectionsService is the read-only counterpart used by authenticated
// non-admin users.
type PublicCuratedCollectionsService struct{}

func NewPublicCuratedCollectionsService() *PublicCuratedCollectionsService {
	return &PublicCuratedCollectionsService{}
}

func (PublicCuratedCollectionsService) List(ctx context.Context) ([]models.BookCollection, error) {
	return database.ListPublicCuratedCollections(ctx)
}

func (PublicCuratedCollectionsService) Get(ctx context.Context, id int64) (*models.BookCollection, error) {
	return database.GetPublicCuratedCollection(ctx, id)
}

func (PublicCuratedCollectionsService) Books(ctx context.Context, collectionID int64) ([]models.Book, error) {
	return database.GetPublicCollectionBooks(ctx, collectionID)
}

func (PublicCuratedCollectionsService) Covers(ctx context.Context, collectionIDs []int64) (map[int64][]database.CollectionCoverBook, error) {
	return database.GetCollectionCovers(ctx, collectionIDs)
}
