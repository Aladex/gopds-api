package services

import (
	"context"
	"errors"
	"sort"

	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/internal/authornorm/llmres"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The swap run (design K, phase 7) runs on the resolve machinery: the same
// driver, stages, jobs, judges, fingerprint checks and report, with the
// purpose swap_check. Its enqueue computes the catalog's role statistics for
// the run (one snapshot, kept), asks the production pair about the
// candidates of the planned (swap, script) strata — the pilot a hash-ordered
// sample of 500, the full run the rest — and its application selects the new
// llm.swap result only when the pair agrees, both judges confirm it and the
// statistics agree (decision 8). Anything else changes nothing.

// swapTokensPerCandidate is the token estimate of one candidate: the pair
// and both judges, each at the eval's upper figure for one job.
const swapTokensPerCandidate = 4 * authorLLMEvalTokensPerJob

// runRoleStats are the role statistics of a run: computed and stored once,
// read back when the run is resumed, so every candidate and every
// application of the run uses the same snapshot.
func runRoleStats(ctx context.Context, db *pg.DB, runID int64) (roleStats, error) {
	id, err := database.AuthorTokenRoleSnapshotOf(ctx, db, runID)
	if err != nil {
		return nil, err
	}
	if id != 0 {
		counts, readErr := database.AuthorTokenRoleStats(ctx, db, id, nil)
		return roleStats(counts), readErr
	}
	sources, err := database.AuthorTokenRoleSources(ctx, db)
	if err != nil {
		return nil, err
	}
	stats := roleStats{}
	credits := 0
	for i := range sources {
		src := &sources[i]
		row := database.AuthorLLMPopulationRow{First: src.First, Middle: src.Middle, Last: src.Last, Nickname: src.Nickname}
		// A source value that does not build is no evidence; it is left
		// out, one row never more than itself.
		_ = isolateItem(func() error {
			value, buildErr := authornorm.NewSourceValue(componentsOf(&row))
			if buildErr != nil {
				return nil
			}
			if stats.add(llmres.FieldValuesOf(value), src.Credits) == nil {
				credits += src.Credits
			}
			return nil
		})
	}
	if _, err = database.StoreAuthorTokenRoleSnapshot(ctx, db, runID, credits, stats); err != nil {
		return nil, err
	}
	// Read back what is stored: a concurrent store of the same run wins.
	id, err = database.AuthorTokenRoleSnapshotOf(ctx, db, runID)
	if err != nil {
		return nil, err
	}
	counts, err := database.AuthorTokenRoleStats(ctx, db, id, nil)
	return roleStats(counts), err
}

// swapCandidateRows are the swap candidates on the run's statistics, as
// population rows of class swap: the inputs the statistics or the
// morphology rule name among the automatically selected structured_person
// ones.
func swapCandidateRows(ctx context.Context, db *pg.DB, runID int64) ([]database.AuthorLLMPopulationRow, error) {
	stats, err := runRoleStats(ctx, db, runID)
	if err != nil {
		return nil, err
	}
	sources, err := database.AuthorLLMSwapSources(ctx, db)
	if err != nil {
		return nil, err
	}
	var rows []database.AuthorLLMPopulationRow
	for i := range sources {
		row := sources[i].AuthorLLMPopulationRow
		candidate := false
		_ = isolateItem(func() error {
			value, buildErr := authornorm.NewSourceValue(componentsOf(&row))
			if buildErr != nil {
				return nil
			}
			tokens, tokErr := swapTokensOf(llmres.FieldValuesOf(value))
			if tokErr != nil {
				return nil
			}
			_, candidate = swapCandidate(stats, tokens, deref(row.Script))
			return nil
		})
		if candidate {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

// enqueueSwap asks the production pair about the candidates of the planned
// swap strata: the pilot about a hash-ordered sample, the full run about the
// rest (the pilot's are never asked again). A candidate that cannot be sent
// is recorded as skipped.
func (d *AuthorLLMResolveDriver) enqueueSwap(ctx context.Context, run *resolveRun) error {
	rows, err := swapCandidateRows(ctx, d.db, run.ID)
	if err != nil {
		return err
	}
	strata, skipped := classifyPopulation(rows)
	for i := range skipped {
		skipped[i].Stage = resolveSkipStage
		if err := database.RecordAuthorLLMEvalSkip(ctx, d.db, run.ID, &skipped[i]); err != nil {
			return err
		}
	}
	var items []populationItem
	for name, pop := range strata {
		if _, planned := run.Plan.Strata[name]; planned {
			items = append(items, pop...)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return string(evalOrder(items[i].row.SourceFingerprint)) < string(evalOrder(items[j].row.SourceFingerprint))
	})
	if run.Mode == models.AuthorLLMEvalModePilot && len(items) > d.sizes.Pilot {
		items = items[:d.sizes.Pilot]
	}
	if resolveEnqueuePlannedHook != nil {
		resolveEnqueuePlannedHook()
	}
	if err := database.SetAuthorLLMRunEstimate(ctx, d.db, run.ID, int64(len(items))*swapTokensPerCandidate); err != nil {
		return err
	}
	return d.enqueueJobs(ctx, run, items)
}

// swapStatsAgree reports whether the run's role statistics say what the
// pair's agreed answer says (decision 8): the third voice besides the two
// models, without which nothing is swapped.
func (d *AuthorLLMResolveDriver) swapStatsAgree(
	ctx context.Context, tx *pg.Tx, run *resolveRun, v *database.AuthorLLMVerdictToApply, input *llmreq.ItemInput,
) (bool, error) {
	rep, err := llmres.ParseStoredReply(v.Reply)
	if err != nil {
		return false, nil
	}
	tokens, err := swapTokensOf(input.Fields)
	if err != nil {
		return false, nil
	}
	snapshot, err := database.AuthorTokenRoleSnapshotOf(ctx, tx, run.ID)
	if err != nil {
		return false, err
	}
	if snapshot == 0 {
		return false, errors.New("services: a swap run without its role statistics")
	}
	counts, err := database.AuthorTokenRoleStats(ctx, tx, snapshot, swapStatKeys(tokens))
	if err != nil {
		return false, err
	}
	roles := make(map[int]llmres.Role, len(rep.Roles))
	for _, ra := range rep.Roles {
		roles[ra.I] = ra.Role
	}
	return swapStatsAgree(roleStats(counts), tokens, roles), nil
}
