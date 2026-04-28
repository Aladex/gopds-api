package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gopds-api/database"
	"gopds-api/llm"
	"gopds-api/logging"
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

func (s *CuratedCollectionsService) List(ctx context.Context, page, pageSize int) ([]models.BookCollection, int, error) {
	return database.ListCuratedCollections(ctx, page, pageSize)
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

// ErrAIResolveAlreadyRunning is returned when an LLM-resolve loop is already
// in flight on this collection — UI shows a clearer error than 500.
var ErrAIResolveAlreadyRunning = errors.New("ai resolve already running for this collection")

// LLMResolveAmbiguous walks every ambiguous item and asks the LLM to pick the
// best candidate, augmenting each candidate with its annotation so the model
// can disambiguate by content. Items where the LLM is unsure are left as-is.
// Returns the number of items actually resolved.
//
// Live progress (processed/total + last few decisions) is pushed into
// book_collections.import_stats.ai_progress after every LLM call so the admin
// UI can poll it through /status. Running=true while the loop is active so a
// refreshed page can pick the polling back up.
func (s *CuratedCollectionsService) LLMResolveAmbiguous(ctx context.Context, collectionID int64, decidedByUserID *int64) (int, error) {
	const pageSize = 1000
	items, _, err := database.ListCollectionItems(ctx, collectionID, models.MatchStatusAmbiguous, 1, pageSize)
	if err != nil {
		return 0, err
	}

	// Snapshot the current stats so we don't blow away matched/ambiguous/total
	// counters on every progress write.
	col, err := database.GetCuratedCollection(ctx, collectionID)
	if err != nil {
		return 0, err
	}
	baseStats := models.CollectionImportStats{}
	if col.ImportStats != nil {
		baseStats = *col.ImportStats
		if baseStats.AIProgress != nil && baseStats.AIProgress.Running {
			return 0, ErrAIResolveAlreadyRunning
		}
	}
	startedAt := time.Now().UTC()
	progress := &models.AIResolveProgress{
		Running:   true,
		Total:     len(items),
		StartedAt: startedAt,
		UpdatedAt: startedAt,
	}
	publish := func() {
		snap := baseStats
		copyProg := *progress
		copyProg.UpdatedAt = time.Now().UTC()
		snap.AIProgress = &copyProg
		if err := database.UpdateCollectionImportStats(ctx, collectionID, snap); err != nil {
			logging.Warnf("LLMResolveAmbiguous: publish progress: %v", err)
		}
	}
	publish()
	defer func() {
		progress.Running = false
		publish()
	}()

	llmSvc := llm.NewLLMService()
	resolved := 0
	for _, it := range items {
		cands := readCandidatesFromItem(it)
		decision := models.AIDecision{
			ItemID:        it.ID,
			ExternalTitle: it.ExternalTitle,
			Action:        "skipped",
		}
		if len(cands) == 0 {
			progress.Processed++
			appendRecent(progress, decision)
			publish()
			continue
		}
		ids := make([]int64, 0, len(cands))
		for _, c := range cands {
			ids = append(ids, c.BookID)
		}
		books, err := database.GetBooksByIDs(ids)
		if err != nil {
			logging.Warnf("LLMResolveAmbiguous: load candidates for item %d: %v", it.ID, err)
			progress.Processed++
			appendRecent(progress, decision)
			publish()
			continue
		}
		byID := make(map[int64]llm.AmbiguousCandidate, len(books))
		for _, b := range books {
			authors := make([]string, 0, len(b.Authors))
			for _, a := range b.Authors {
				authors = append(authors, a.FullName)
			}
			byID[b.ID] = llm.AmbiguousCandidate{
				BookID:     b.ID,
				Title:      b.Title,
				Authors:    strings.Join(authors, ", "),
				Annotation: b.Annotation,
			}
		}
		prompt := make([]llm.AmbiguousCandidate, 0, len(cands))
		for _, c := range cands {
			if hit, ok := byID[c.BookID]; ok {
				prompt = append(prompt, hit)
			}
		}
		if len(prompt) == 0 {
			progress.Processed++
			appendRecent(progress, decision)
			publish()
			continue
		}
		bookID, err := llmSvc.ResolveAmbiguousMatch(it.ExternalTitle, it.ExternalAuthor, prompt)
		if err != nil {
			logging.Warnf("LLMResolveAmbiguous: LLM call failed for item %d: %v", it.ID, err)
			progress.Processed++
			appendRecent(progress, decision)
			publish()
			continue
		}
		if bookID == nil {
			progress.Processed++
			appendRecent(progress, decision)
			publish()
			continue
		}
		if err := s.Resolve(ctx, it.ID, *bookID, decidedByUserID); err != nil {
			logging.Warnf("LLMResolveAmbiguous: resolve %d -> %d failed: %v", it.ID, *bookID, err)
			progress.Processed++
			appendRecent(progress, decision)
			publish()
			continue
		}
		resolved++
		decision.Action = "resolved"
		decision.BookID = bookID
		if hit, ok := byID[*bookID]; ok {
			decision.BookTitle = hit.Title
		}
		progress.Processed++
		progress.Resolved = resolved
		appendRecent(progress, decision)
		publish()
	}
	return resolved, nil
}

func appendRecent(p *models.AIResolveProgress, d models.AIDecision) {
	const keep = 5
	p.Recent = append(p.Recent, d)
	if len(p.Recent) > keep {
		p.Recent = p.Recent[len(p.Recent)-keep:]
	}
}

// readCandidatesFromItem extracts the candidates list from external_extra.
func readCandidatesFromItem(it models.BookCollectionItem) []models.MatchCandidate {
	if len(it.ExternalExtra) == 0 {
		return nil
	}
	var extra struct {
		Candidates []models.MatchCandidate `json:"candidates"`
	}
	if err := json.Unmarshal(it.ExternalExtra, &extra); err != nil {
		return nil
	}
	return extra.Candidates
}

// pickAutoResolveTarget reads candidates from external_extra and chooses the
// best one according to the rule "highest score, ties broken by most-recently
// registered book".
func pickAutoResolveTarget(ctx context.Context, it models.BookCollectionItem) (int64, error) {
	cands := readCandidatesFromItem(it)
	if len(cands) == 0 {
		return 0, nil
	}
	maxScore := cands[0].Score
	for _, c := range cands {
		if c.Score > maxScore {
			maxScore = c.Score
		}
	}
	const tolerance = 0.001
	tied := make([]int64, 0, len(cands))
	for _, c := range cands {
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

func (PublicCuratedCollectionsService) List(ctx context.Context, page, pageSize int) ([]models.BookCollection, int, error) {
	return database.ListPublicCuratedCollections(ctx, page, pageSize)
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
