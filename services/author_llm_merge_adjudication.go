package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"gopds-api/database"
	"gopds-api/internal/authorentity"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// Adjudicating the merge eval's accepted suite items (E4c-2b-0): an
// administrator reads each namesake item the production rule accepted and
// labels it one person, two people or unclear; a recomputed report then
// fails a stratum only on the accepts not labeled one person.

// ErrAuthorLLMInvalidAdjudication is a label outside the closed set, or a
// note over its bound.
var ErrAuthorLLMInvalidAdjudication = errors.New("services: invalid adjudication")

// adjudicationNoteMax is the longest note (migration 67's check).
const adjudicationNoteMax = 2000

// RecomputeAuthorLLMMergeEvalReport computes a completed merge eval's report
// again from the run's stored answers and the suite's current
// adjudications — no model is asked anything — under the merge
// configuration's identity, and stores it as the run's next report version
// (an equal report is not stored again). The settings are the stored
// report's. A K identity, a run that is not a completed merge eval or one
// of another version is refused.
func RecomputeAuthorLLMMergeEvalReport(
	ctx context.Context, db *pg.DB, identity *llmreq.Identity, version string, runID int64,
) (latest *database.AuthorLLMEvalReport, stored bool, err error) {
	if identity == nil || identity.Task != llmreq.TaskMerge {
		return nil, false, ErrAuthorLLMInvalidRun
	}
	row, err := database.GetAuthorLLMRunByID(ctx, db, runID)
	if err != nil {
		return nil, false, err
	}
	switch {
	case row.Kind != models.AuthorLLMRunEval || !models.IsAuthorLLMEvalMergeMode(row.Mode):
		return nil, false, ErrAuthorLLMInvalidRun
	case row.ConfigVersion != version:
		return nil, false, ErrAuthorLLMRunConfigChanged
	case row.Status != models.AuthorLLMRunCompleted:
		return nil, false, database.ErrAuthorLLMRunState
	}
	generation, err := database.AuthorLLMRelearnGeneration(ctx, db, runID)
	if err != nil {
		return nil, false, err
	}
	prev, err := database.GetAuthorLLMEvalReport(ctx, db, runID)
	if err != nil {
		return nil, false, err
	}
	var storedDoc struct {
		Settings AuthorLLMEvalSettings `json:"settings"`
	}
	if err = json.Unmarshal([]byte(prev.Report), &storedDoc); err != nil {
		return nil, false, fmt.Errorf("reading a merge eval report: %w", err)
	}
	run := &evalRun{ID: row.ID, Mode: row.Mode, Settings: storedDoc.Settings}
	d := NewAuthorLLMEvalDriver(db, identity, version, DefaultAuthorLLMEvalSizes(), "", nil, 0)
	doc, err := d.mergeReportDoc(ctx, run)
	if err != nil {
		return nil, false, err
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, false, err
	}
	return database.RecomputeAuthorLLMEvalReport(ctx, db, runID, version, raw, generation)
}

// AuthorLLMAdversarialReview is a merge eval run's accepted suite items, to
// adjudicate.
type AuthorLLMAdversarialReview struct {
	RunID int64                      `json:"run_id"`
	Items []AuthorLLMAdversarialItem `json:"items"`
}

// AuthorLLMAdversarialItem is one accepted suite item: its two sides as the
// models saw them (compact), the four answers and the current label.
type AuthorLLMAdversarialItem struct {
	EvalItemID   int64                           `json:"eval_item_id"`
	Stratum      string                          `json:"stratum"`
	A            AuthorLLMAdversarialSide        `json:"a"`
	B            AuthorLLMAdversarialSide        `json:"b"`
	Verdicts     map[string]string               `json:"verdicts"`
	Adjudication *database.AuthorLLMAdjudication `json:"adjudication,omitempty"`
}

// AuthorLLMAdversarialSide is one side's names and books.
type AuthorLLMAdversarialSide struct {
	Names []string                   `json:"names"`
	Size  string                     `json:"size"`
	Books []AuthorLLMAdversarialBook `json:"books"`
}

// AuthorLLMAdversarialBook is a sample book: title, year, genre codes.
type AuthorLLMAdversarialBook struct {
	Title  string   `json:"title"`
	Year   string   `json:"year,omitempty"`
	Genres []string `json:"genres,omitempty"`
}

// adversarialSideOf is a card, compact.
func adversarialSideOf(c *authorentity.MergeCard) AuthorLLMAdversarialSide {
	side := AuthorLLMAdversarialSide{Names: c.Names, Size: c.Size, Books: make([]AuthorLLMAdversarialBook, 0, len(c.Books))}
	for i := range c.Books {
		b := &c.Books[i]
		side.Books = append(side.Books, AuthorLLMAdversarialBook{Title: b.Title, Year: b.Year, Genres: b.Genres})
	}
	return side
}

// acceptedSuiteIDs are the accepted suite items a stored merge report
// names, in report order without repeats.
func acceptedSuiteIDs(report string) ([]int64, error) {
	var doc struct {
		Evaluation struct {
			Strata map[string]struct {
				Combos map[string]struct {
					Adversarial *struct {
						AcceptedIDs []int64 `json:"accepted_ids"`
					} `json:"adversarial"`
				} `json:"combos"`
			} `json:"strata"`
		} `json:"evaluation"`
	}
	if err := json.Unmarshal([]byte(report), &doc); err != nil {
		return nil, fmt.Errorf("reading a merge eval report: %w", err)
	}
	var ids []int64
	for _, stratum := range doc.Evaluation.Strata {
		for _, combo := range stratum.Combos {
			if combo.Adversarial != nil {
				ids = append(ids, combo.Adversarial.AcceptedIDs...)
			}
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

// contestedIDs are the contested accepts a stored merge report lists —
// the census's or holdout's and the extra questions' — in id order
// without repeats.
func contestedIDs(report string) ([]int64, error) {
	type review struct {
		IDs map[string][]int64 `json:"ids"`
	}
	var doc struct {
		Evaluation struct {
			Strata map[string]struct {
				Combos map[string]struct {
					ContestedReview *review `json:"contested_review"`
					Supplement      *struct {
						ContestedReview *review `json:"contested_review"`
					} `json:"supplement"`
				} `json:"combos"`
			} `json:"strata"`
		} `json:"evaluation"`
	}
	if err := json.Unmarshal([]byte(report), &doc); err != nil {
		return nil, fmt.Errorf("reading a merge eval report: %w", err)
	}
	var ids []int64
	add := func(r *review) {
		if r == nil {
			return
		}
		for _, byScript := range r.IDs {
			ids = append(ids, byScript...)
		}
	}
	for _, stratum := range doc.Evaluation.Strata {
		for _, combo := range stratum.Combos {
			add(combo.ContestedReview)
			if combo.Supplement != nil {
				add(combo.Supplement.ContestedReview)
			}
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

// mergeAnswerInvalid is an answer that ended without a valid one.
const mergeAnswerInvalid = "invalid"

// mergeAnswerWord is a merge answer as the review shows it.
func mergeAnswerWord(a *database.AuthorLLMEvalAnswer) string {
	got := answerOf(a)
	switch {
	case !got.Valid:
		return mergeAnswerInvalid
	case got.Canonical == mergeSameCanonical:
		return llmreq.MergeSame
	case got.Canonical == mergeDifferentCanonical:
		return llmreq.MergeDifferent
	case got.Canonical == mergeCannotTellCanon:
		return llmreq.MergeCannotTell
	}
	return mergeAnswerInvalid
}

// mergeVerdicts are each item's first answers by participant: the pair's
// and the judges'.
func mergeVerdicts(answers []database.AuthorLLMEvalAnswer) map[int64]map[string]string {
	verdicts := map[int64]map[string]string{}
	for i := range answers {
		a := &answers[i]
		if a.RepeatNo != 0 || (a.Purpose != string(models.AuthorLLMPurposeEvalCandidate) &&
			a.Purpose != string(models.AuthorLLMPurposeEvalJudge)) {
			continue
		}
		if verdicts[a.EvalItemID] == nil {
			verdicts[a.EvalItemID] = map[string]string{}
		}
		verdicts[a.EvalItemID][a.Slot] = mergeAnswerWord(a)
	}
	return verdicts
}

// AuthorLLMAdversarialItems are the suite items a merge eval run's latest
// report accepted, with their sides, the four answers and the current
// labels.
func AuthorLLMAdversarialItems(ctx context.Context, db pg.DBI, runID int64) (*AuthorLLMAdversarialReview, error) {
	return mergeReviewItems(ctx, db, runID, acceptedSuiteIDs)
}

// AuthorLLMContestedItems are the contested accepts a merge eval run's
// latest report lists — the pair agreed same, the judges did not both
// confirm it — in the suite review's shape: a human labels them, and a
// label same or unclear clears the error the gate counts (they are never
// applied either way).
func AuthorLLMContestedItems(ctx context.Context, db pg.DBI, runID int64) (*AuthorLLMAdversarialReview, error) {
	return mergeReviewItems(ctx, db, runID, contestedIDs)
}

// mergeReviewItems are the items ids picks from a merge eval run's latest
// report, with their sides, the four answers and the current labels.
func mergeReviewItems(
	ctx context.Context, db pg.DBI, runID int64, idsOf func(report string) ([]int64, error),
) (*AuthorLLMAdversarialReview, error) {
	row, err := database.GetAuthorLLMRunByID(ctx, db, runID)
	if err != nil {
		return nil, err
	}
	if row.Kind != models.AuthorLLMRunEval || !models.IsAuthorLLMEvalMergeMode(row.Mode) {
		return nil, ErrAuthorLLMInvalidRun
	}
	report, err := database.GetAuthorLLMEvalReport(ctx, db, runID)
	if errors.Is(err, pg.ErrNoRows) {
		return nil, ErrAuthorLLMReportNotFound
	}
	if err != nil {
		return nil, err
	}
	ids, err := idsOf(report.Report)
	if err != nil {
		return nil, err
	}
	items, err := database.AuthorLLMEvalItemsByID(ctx, db, ids)
	if err != nil {
		return nil, err
	}
	labels, err := database.AuthorLLMAdjudications(ctx, db, ids)
	if err != nil {
		return nil, err
	}
	answers, err := database.AuthorLLMEvalAnswers(ctx, db, runID)
	if err != nil {
		return nil, err
	}
	verdicts := mergeVerdicts(answers)
	out := &AuthorLLMAdversarialReview{RunID: runID, Items: make([]AuthorLLMAdversarialItem, 0, len(items))}
	for i := range items {
		it := &items[i]
		var input authorentity.MergeInput
		if err := json.Unmarshal(it.Input, &input); err != nil {
			return nil, fmt.Errorf("reading a suite item's input: %w", err)
		}
		item := AuthorLLMAdversarialItem{EvalItemID: it.ID, Stratum: it.Stratum, A: adversarialSideOf(&input.A),
			B: adversarialSideOf(&input.B), Verdicts: verdicts[it.ID]}
		if l, ok := labels[it.ID]; ok {
			item.Adjudication = &l
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// AdjudicateAuthorLLMEvalItem labels an accepted suite item; by is the
// administrator's name.
func AdjudicateAuthorLLMEvalItem(ctx context.Context, db pg.DBI, itemID int64, label, note, by string) error {
	if !database.ValidAdjudicationLabel(label) || len(note) > adjudicationNoteMax || by == "" {
		return ErrAuthorLLMInvalidAdjudication
	}
	if err := database.AdjudicateAuthorLLMEvalItem(ctx, db, itemID, label, note, by); err != nil {
		return err
	}
	LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
		Name: AuthorMetadataEventLLMItemAdjudicated, Stage: AuthorMetadataStageRunner, ItemID: itemID, Status: label})
	return nil
}
