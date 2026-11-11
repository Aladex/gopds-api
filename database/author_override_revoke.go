package database

import (
	"context"
	"errors"
	"fmt"

	"gopds-api/internal/authornorm"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// Revoking an override with restore is the undo of a review decision made by
// a triage batch (design/review-triage): the batch's fingerprint override
// stops applying, and the fingerprint's credits go back to review on the
// proposal the decision was about, which reopens the item.

var (
	ErrOverrideNotFound      = errors.New("database: the override does not exist")
	ErrOverrideRevoked       = errors.New("database: the override is already revoked")
	ErrRestoreExtractorMoved = errors.New("database: a credit of the scope is read under another extractor version")
)

// RestoreReport is what one revoke with restore did. Credits are the current
// author credits of the fingerprint it re-resolved. Superseded: a credit
// stayed selected, on a newer active override. Reopened: an open
// ambiguous_decision item exists for the fingerprint afterwards.
type RestoreReport struct {
	Credits    int
	Reopened   bool
	Superseded bool
}

// RevokeOverrideAndRestore revokes one fingerprint-scoped override inside the
// caller's transaction and re-resolves the fingerprint's current author
// credits as the automatic path would have left them: in review, on
// proposalResultID, under class. The lock order is the resolver's: the
// credits first, in ascending ID order, then the override and review items.
//
// A missing override is ErrOverrideNotFound, a credit-scoped one
// ErrInvalidOverrideScope, a revoked one ErrOverrideRevoked. When a credit of
// the fingerprint is read under another extractor version than the
// proposal's, the proposal cannot be selected for it, and the revoke is
// refused with ErrRestoreExtractorMoved before anything is written.
func RevokeOverrideAndRestore(
	ctx context.Context, tx ResolutionTx, overrideID, admin, proposalResultID int64, class authornorm.DecisionClass,
) (RestoreReport, error) {
	if admin <= 0 {
		return RestoreReport{}, ErrInvalidReviewActor
	}
	if proposalResultID <= 0 {
		return RestoreReport{}, ErrInvalidAutomaticOutcome
	}
	override, err := revocableOverride(ctx, tx, overrideID)
	if err != nil {
		return RestoreReport{}, err
	}
	fingerprint := override.SourceFingerprint
	credits, err := lockCreditsForRestore(ctx, tx, fingerprint, proposalResultID)
	if err != nil {
		return RestoreReport{}, err
	}

	res, err := tx.ExecContext(ctx, `UPDATE contributor_manual_override
		SET revoked_at = now(), revoked_by_user_id = ?
		WHERE id = ? AND revoked_at IS NULL`, admin, overrideID)
	if err != nil {
		return RestoreReport{}, fmt.Errorf("revoking the override: %w", err)
	}
	if res.RowsAffected() != 1 {
		return RestoreReport{}, ErrOverrideRevoked
	}
	return restoreCredits(ctx, tx, fingerprint, credits, &AutomaticOutcome{ResultID: proposalResultID,
		DecisionClass: class, Decision: authornorm.Decision{Outcome: authornorm.OutcomeReview}})
}

// revocableOverride reads an unrevoked fingerprint-scoped override.
func revocableOverride(ctx context.Context, tx ResolutionTx, overrideID int64) (*models.ContributorManualOverride, error) {
	if overrideID <= 0 {
		return nil, ErrOverrideNotFound
	}
	override := new(models.ContributorManualOverride)
	err := tx.ModelContext(ctx, override).Where("id = ?", overrideID).Select()
	switch {
	case errors.Is(err, pg.ErrNoRows):
		return nil, ErrOverrideNotFound
	case err != nil:
		return nil, fmt.Errorf("reading the override: %w", err)
	case override.ScopeFingerprint == nil:
		return nil, ErrInvalidOverrideScope
	case override.RevokedAt != nil:
		return nil, ErrOverrideRevoked
	}
	return override, nil
}

// lockCreditsForRestore locks the fingerprint's current author credits in
// ascending ID order and refuses a restore when one of them is read under
// another extractor version than the proposal.
func lockCreditsForRestore(
	ctx context.Context, tx ResolutionTx, fingerprint []byte, proposalResultID int64,
) ([]resolvableCredit, error) {
	credits, err := currentAuthorCreditsOfFingerprint(ctx, tx, fingerprint)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(credits))
	for i := range credits {
		ids[i] = credits[i].ID
		if _, err = tx.ExecContext(ctx, resolutionLockSQL, credits[i].ID); err != nil {
			return nil, fmt.Errorf("locking the credits of the override: %w", err)
		}
	}
	var moved bool
	_, err = tx.QueryOneContext(ctx, pg.Scan(&moved), restoreExtractorMovedSQL,
		proposalResultID, fingerprint, pg.Array(ids))
	switch {
	case errors.Is(err, pg.ErrNoRows):
		return nil, fmt.Errorf("%w: the proposal is no automatic result of the fingerprint", ErrInvalidAutomaticOutcome)
	case err != nil:
		return nil, fmt.Errorf("comparing the credits' extractor versions: %w", err)
	case moved:
		return nil, ErrRestoreExtractorMoved
	}
	return credits, nil
}

// restoreCredits re-resolves the locked credits on the automatic outcome and
// reports where they ended.
func restoreCredits(
	ctx context.Context, tx ResolutionTx, fingerprint []byte, credits []resolvableCredit, out *AutomaticOutcome,
) (RestoreReport, error) {
	report := RestoreReport{Credits: len(credits)}
	var marks creditMarks
	for i := range credits {
		state, err := resolveCredit(ctx, tx, credits[i], out, &marks)
		if err != nil {
			return RestoreReport{}, err
		}
		if state == models.CreditSelectionSelected {
			report.Superseded = true
		}
	}
	if err := marks.flush(ctx, tx); err != nil {
		return RestoreReport{}, err
	}
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&report.Reopened), `SELECT EXISTS (
		SELECT 1 FROM contributor_review_item
		WHERE scope_fingerprint = ? AND reason = 'ambiguous_decision' AND status = 'open')`,
		fingerprint); err != nil {
		return RestoreReport{}, fmt.Errorf("looking for the reopened review item: %w", err)
	}
	return report, nil
}

// restoreExtractorMovedSQL reports a credit of ?2 (the locked IDs) whose
// snapshot is read under another extractor version than the proposal ?0, an
// automatic result of the fingerprint ?1; no row when it is not one.
const restoreExtractorMovedSQL = `SELECT EXISTS (
		SELECT 1 FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id
		WHERE c.id = ANY (?2::bigint[]) AND s.extractor_version IS DISTINCT FROM r.extractor_version)
	FROM contributor_normalization_result r
	WHERE r.id = ?0 AND r.source_fingerprint = ?1 AND r.method <> 'manual'`

// fingerprintPinnedPredicate is true when the fingerprint pin.fp is pinned by
// hand: it is a member of an unrevoked merge pin, or the build assigned it to
// an entity whose name is pinned or that holds a pinned assignment (rule
// pin:...). The dry run reads it per item as a lateral subquery.
const fingerprintPinnedPredicate = `(EXISTS (
		SELECT 1 FROM author_entity_merge_pin_member m
		JOIN author_entity_merge_pin p ON p.id = m.pin_id
		WHERE m.source_fingerprint = pin.fp AND p.revoked_at IS NULL)
	OR EXISTS (
		SELECT 1 FROM author_entity_assignment a
		JOIN author_entity e ON e.id = a.entity_id
		WHERE a.source_fingerprint = pin.fp
			AND (e.name_pinned OR EXISTS (
				SELECT 1 FROM author_entity_assignment o
				WHERE o.entity_id = a.entity_id AND o.rule LIKE 'pin:%'))))`

// fingerprintPinnedSQL asks fingerprintPinnedPredicate of one fingerprint.
const fingerprintPinnedSQL = `SELECT ` + fingerprintPinnedPredicate + ` FROM (SELECT ?::bytea AS fp) pin`

// FingerprintPinned reports whether a fingerprint is pinned by hand (see
// fingerprintPinnedPredicate): a triage batch never decides such a name.
func FingerprintPinned(ctx context.Context, db pg.DBI, fingerprint []byte) (bool, error) {
	var pinned bool
	if _, err := db.QueryOneContext(ctx, pg.Scan(&pinned), fingerprintPinnedSQL, fingerprint); err != nil {
		return false, fmt.Errorf("reading the fingerprint's pins: %w", err)
	}
	return pinned, nil
}
