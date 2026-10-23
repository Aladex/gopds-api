package services

import (
	"context"
	"errors"
	"time"

	"gopds-api/database"
	"gopds-api/internal/authornorm"

	"github.com/go-pg/pg/v10"
)

// Automatic acceptance per (decision class, script). The registered pairs ship
// with the code (migration 25 and later ones); every credit of a registered
// pair — the ones waiting when the pair arrives and every later one — is
// selected automatically. New credits are decided by the local worker under the
// newest policy; the credits that were waiting when a pair arrived are picked
// up by the acceptance pass, which runs on its own: once at every start, and
// again in every local worker batch, so a credit the worker decided under the
// previous version while a migration added a pair is picked up too. The pass
// is idempotent: a credit it selected no longer waits, and a credit decided by
// anything else is never touched.
//
// Logs carry counts and versions only, never a name or other source text
// (contract 3.14).

// acceptanceApplyBatch is how many inputs one page of the pass lists.
const acceptanceApplyBatch = 200

// ErrInvalidAuthorAcceptanceConfig marks a pass without a normalizer version
// or with a non-positive batch.
var ErrInvalidAuthorAcceptanceConfig = errors.New("services: invalid author acceptance config")

// ApplyAcceptancePolicy selects, under the newest policy, the current author
// credits still unresolved for want of a registration whose pair is now
// registered, and returns how many it left selected — committed work included
// when it returns an error. It pages through the inputs
// once, batch inputs per page, and through each input's credits batch per
// transaction, so a transaction's locks and work are bounded, a failure or a
// stop loses at most one page, and a rerun repeats nothing. An input whose
// stored result the policy does not select — an ambiguous one included — is
// left as it is.
func ApplyAcceptancePolicy(ctx context.Context, db *pg.DB, normalizerVersion string, batch int) (int, error) {
	if batch <= 0 || normalizerVersion == "" {
		return 0, ErrInvalidAuthorAcceptanceConfig
	}
	policy, err := database.LoadCurrentAcceptancePolicy(ctx, db, normalizerVersion)
	if err != nil || policy.Empty() {
		return 0, err
	}
	selected := 0
	defer func() { logAcceptanceApplied(selected, policy.Version()) }()
	var after *database.AcceptanceCandidate
	for {
		candidates, listErr := database.AcceptanceCandidates(ctx, db, normalizerVersion, policy.Version(), after, batch)
		if listErr != nil {
			return selected, listErr
		}
		for i := range candidates {
			n, applyErr := applyAcceptanceCandidate(ctx, db, &policy, &candidates[i], batch)
			selected += n
			if applyErr != nil {
				return selected, applyErr
			}
		}
		if len(candidates) < batch {
			return selected, nil
		}
		after = &candidates[len(candidates)-1]
	}
}

// applyAcceptanceCandidate selects the input's waiting credits, batch credits
// per transaction, and returns how many it leaves selected — with an error,
// how many the committed transactions left selected.
//
// Each transaction takes the input's lock first (database.LockNormalizationInput,
// which the credit writer takes too), then decides the input's stored result
// the way the local reconciliation does — against the input's current
// duplicate_component reading — and only then lists and selects its page. A
// flagged credit therefore either committed before the check, which sees it,
// or waits until the page committed. When a later page finds the input
// ambiguous, the pass takes back its own earlier selections of the input,
// batch per transaction, so it never ends with an automatic selection of an
// input it saw ambiguous. A flag that arrives after the pass finished the
// input is the local reconciliation's case, as for the worker's own
// selections.
func applyAcceptanceCandidate(
	ctx context.Context,
	db *pg.DB,
	policy *authornorm.AcceptancePolicy,
	candidate *database.AcceptanceCandidate,
	batch int,
) (int, error) {
	stored, err := database.LoadLocalResult(ctx, db, candidate.ResultID)
	if err != nil {
		return 0, err
	}
	// Where the pass found the credits: what a demotion puts back.
	undo := &database.AutomaticOutcome{
		ResultID: candidate.ResultID, DecisionClass: stored.DecisionClass,
		Decision: authornorm.Decision{
			Outcome: authornorm.OutcomeUnresolved, Reason: authornorm.ReasonPolicyNotRegistered,
			PolicyVersion: policy.Version(),
		},
	}
	selected := 0
	var own []int64
	var after int64
	for {
		var step acceptanceStep
		err = db.RunInTransaction(ctx, func(tx *pg.Tx) error {
			var stepErr error
			step, stepErr = acceptancePage(ctx, tx, policy, &stored, candidate, own, after, batch, undo)
			return stepErr
		})
		if err != nil {
			return selected, err
		}
		if step.ambiguous {
			selected -= step.demoted
			own = own[step.taken:]
			if len(own) == 0 {
				return selected, nil
			}
			continue
		}
		selected += step.page.Selected
		own = append(own, step.page.SelectedIDs...)
		if step.page.Listed < batch {
			return selected, nil
		}
		after = step.page.Last
	}
}

// acceptanceStep is what one transaction of a candidate did: a page selected,
// or — the input found ambiguous — taken of the pass's own selections and
// demoted.
type acceptanceStep struct {
	page      database.AcceptancePage
	ambiguous bool
	taken     int
	demoted   int
}

// acceptancePage is one transaction of a candidate: the input's lock, the
// reconciliation's decision against the current duplicate reading, and then
// either a page of selections or, for an input that is not this policy's to
// select, the demotion of up to batch of the pass's own earlier selections.
func acceptancePage(
	ctx context.Context,
	tx *pg.Tx,
	policy *authornorm.AcceptancePolicy,
	stored *authornorm.Result,
	candidate *database.AcceptanceCandidate,
	own []int64,
	after int64,
	batch int,
	undo *database.AutomaticOutcome,
) (acceptanceStep, error) {
	var step acceptanceStep
	if err := database.LockNormalizationInput(ctx, tx, candidate.SourceFingerprint, candidate.ExtractorVersion); err != nil {
		return step, err
	}
	duplicate, err := database.InputHasDuplicateComponent(ctx, tx, candidate.SourceFingerprint, candidate.ExtractorVersion)
	if err != nil {
		return step, err
	}
	out, decideErr := reconciledOutcome(policy, duplicate, stored, candidate.ResultID)
	if decideErr != nil || !out.Decision.Selected() {
		step.ambiguous, step.taken = true, min(batch, len(own))
		step.demoted, err = database.DemoteAcceptanceSelections(ctx, tx, candidate, own[:step.taken], undo)
		return step, err
	}
	step.page, err = database.SelectRegisteredCredits(ctx, tx, candidate, &out, after, batch)
	return step, err
}

func logAcceptanceApplied(selected, version int) {
	if selected > 0 {
		LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
			Name: AuthorMetadataEventAcceptanceApplied, Stage: AuthorMetadataStageAcceptance,
			Count: selected, PolicyVersion: version,
		})
	}
}

// AuthorAcceptancePass is the runner stage that brings the shipped normalizer
// and policy into force once at start: the inputs a new normalizer version
// reads differently are queued again under it, and the credits that waited
// for a pair a new release registered are selected without an
// administrator, whether or not the workers run.
type AuthorAcceptancePass struct {
	db *pg.DB
}

// NewAuthorAcceptancePass builds the start-up pass over db.
func NewAuthorAcceptancePass(db *pg.DB) *AuthorAcceptancePass { return &AuthorAcceptancePass{db: db} }

// Name is the stage's fixed label.
func (p *AuthorAcceptancePass) Name() string { return string(AuthorMetadataStageAcceptance) }

// Run queues the re-read inputs, then applies the newest policy once and
// returns; a canceled ctx stops it at the next input.
func (p *AuthorAcceptancePass) Run(ctx context.Context) error {
	if _, err := RenormalizeRereadInputs(ctx, p.db, acceptanceApplyBatch); err != nil {
		return err
	}
	_, err := ApplyAcceptancePolicy(ctx, p.db, authornorm.NormalizerVersion, acceptanceApplyBatch)
	return err
}

// RenormalizeRereadInputs queues under authornorm.NormalizerVersion every
// input still waiting on authornorm.PreviousNormalizerVersion: an open review
// item or a credit unresolved for want of a registration on a result of a
// class the new version reads differently (authornorm.RereadClasses), and
// every previous-version job the new worker will never claim — waiting,
// failed, or written since the release by a v1 binary still running
// (database.RenormalizeLists). It only queues: the local worker normalizes
// the inputs, and its resolver closes an old reading's review item when the
// new one is applied. The start-up pass runs it, and so does every local
// worker batch, which is what catches a v1 binary's late writes in a rolling
// update.
//
// Each input is queued on its own: one that fails is logged by its
// fingerprint and SQLSTATE and skipped, and the sweep goes on past it, so it
// can neither stop the sweep nor starve the inputs after it at the next
// start. It returns how many jobs it queued; only a list that cannot be read
// is an error. A repeated sweep queues nothing: an input with a job of the
// new version is not listed again.
func RenormalizeRereadInputs(ctx context.Context, db *pg.DB, batch int) (int, error) {
	if batch <= 0 {
		return 0, ErrInvalidAuthorAcceptanceConfig
	}
	target := database.RenormalizeTarget{
		Previous: authornorm.PreviousNormalizerVersion, Current: authornorm.NormalizerVersion,
		Classes: authornorm.RereadClasses(),
	}
	released, err := database.ReleaseTime(ctx, db, target.Current)
	if err != nil {
		return 0, err
	}
	if !released.IsZero() {
		// A v1 transaction that began before the release may commit its job
		// after it with an earlier created_at: look back a margin.
		target.Since = released.Add(-renormalizeLateMargin)
	}
	queued := 0
	defer func() { logRenormalizeQueued(queued) }()
	for _, list := range database.RenormalizeLists() {
		var after *database.RenormalizeInput
		for {
			inputs, listErr := database.RenormalizeInputs(ctx, db, list, &target, after, batch)
			if listErr != nil {
				return queued, listErr
			}
			for i := range inputs {
				fresh, queueErr := database.QueueRenormalization(ctx, db, inputs[i], target.Current)
				if queueErr != nil {
					if ctx.Err() != nil {
						return queued, ctx.Err()
					}
					LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{
						Name: AuthorMetadataEventRenormalizeSkipped, Stage: AuthorMetadataStageAcceptance,
						Fingerprint: inputs[i].SourceFingerprint, SQLState: AuthorMetadataSQLState(queueErr),
					})
					continue
				}
				if fresh {
					queued++
				}
			}
			if len(inputs) < batch {
				break
			}
			after = &inputs[len(inputs)-1]
		}
	}
	return queued, nil
}

// renormalizeLateMargin is how far before the release the late list looks.
const renormalizeLateMargin = time.Hour

func logRenormalizeQueued(queued int) {
	if queued > 0 {
		LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
			Name: AuthorMetadataEventRenormalizeQueued, Stage: AuthorMetadataStageAcceptance, Count: queued,
		})
	}
}
