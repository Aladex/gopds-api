package database

import (
	"context"
	"fmt"
	"time"

	"gopds-api/internal/authornorm"

	"github.com/go-pg/pg/v10"
)

// A new normalizer version reads some inputs differently, and only new
// extractions queue local jobs, so the inputs already waiting on an older
// reading would keep it. The re-normalization only queues those inputs under
// the new version; the local worker normalizes and resolves them as any other
// job, and the resolver closes the old reading's review item in the same
// transaction (supersedeStaleReviewSQL). Nothing is closed or decided here, so
// a job that never runs — the new pod gone, a rollback — leaves every input
// as it was.
//
// The inputs: an open review item, or a credit unresolved for want of a
// registration, on a previous-version result of a class the new version
// reads differently; and every previous-version job a worker of the new
// version will never claim — still waiting, failed, or written after the
// release by a v1 binary still running in a rolling update. Each list skips
// an input that already has a job of the new version, so a repeated sweep
// lists nothing it queued, and a settled job of the new version is left to the
// worker's reconciliation (completedInputsToReconcileSQL), which re-applies
// its stored result.

// RenormalizeInput is one normalization input waiting on an older reading.
type RenormalizeInput struct {
	SourceFingerprint []byte
	ExtractorVersion  string
}

// RenormalizeList names one list of inputs the sweep queues.
type RenormalizeList int

// The lists, in the order a sweep pages through them.
const (
	// RenormalizeReview: open ambiguous review items on a re-read class.
	RenormalizeReview RenormalizeList = iota + 1
	// RenormalizeUnresolved: credits waiting for a registration on a re-read
	// class.
	RenormalizeUnresolved
	// RenormalizeUnfinished: previous-version jobs still waiting or failed.
	RenormalizeUnfinished
	// RenormalizeLate: previous-version jobs written since the release.
	RenormalizeLate
)

// RenormalizeLists returns every list in sweep order.
func RenormalizeLists() []RenormalizeList {
	return []RenormalizeList{RenormalizeReview, RenormalizeUnresolved, RenormalizeUnfinished, RenormalizeLate}
}

// noCurrentJob is true for an input (alias x) without a job of the new
// version ?1.
const noCurrentJob = `NOT EXISTS (SELECT 1 FROM contributor_normalization_job q
			WHERE q.source_fingerprint = x.source_fingerprint AND q.extractor_version = x.extractor_version
				AND q.normalizer_version = ?1)`

// hasCurrentAuthorCredit is true for an input (alias x) with an author credit
// of a current snapshot.
const hasCurrentAuthorCredit = `EXISTS (SELECT 1 FROM book_contributor_credit c
			JOIN book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
			WHERE c.source_fingerprint = x.source_fingerprint AND s.extractor_version = x.extractor_version
				AND c.role = 'author')`

// renormalizeListSQL is each list's input set over ?0 the previous version,
// ?2 the re-read classes and ?3 the release time.
var renormalizeListSQL = map[RenormalizeList]string{
	RenormalizeReview: `SELECT r.source_fingerprint, r.extractor_version
		FROM contributor_review_item i
		JOIN contributor_normalization_result r ON r.id = i.proposal_result_id
		WHERE i.status = 'open' AND i.reason = 'ambiguous_decision' AND i.scope_fingerprint IS NOT NULL
			AND i.decision_class IN (?2) AND r.method IN ` + localMethods + ` AND r.normalizer_version = ?0`,
	RenormalizeUnresolved: `SELECT c.source_fingerprint, s.extractor_version
		FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
		JOIN book_contributor_credit_selection sel ON sel.credit_id = c.id
		JOIN contributor_normalization_result r ON r.id = sel.result_id
		WHERE c.role = 'author' AND sel.state = 'unresolved' AND sel.unresolved_reason = 'policy_not_registered'
			AND sel.decided_by_user_id IS NULL AND sel.override_id IS NULL
			AND r.method IN ` + localMethods + ` AND r.normalizer_version = ?0 AND r.decision_class IN (?2)`,
	RenormalizeUnfinished: `SELECT j.source_fingerprint, j.extractor_version
		FROM contributor_normalization_job j
		WHERE j.normalizer_version = ?0 AND j.status IN ('pending', 'queued', 'failed')`,
	RenormalizeLate: `SELECT j.source_fingerprint, j.extractor_version
		FROM contributor_normalization_job j
		WHERE j.normalizer_version = ?0 AND j.created_at >= ?3`,
}

// RenormalizeTarget is what a sweep re-reads: inputs on results of Previous
// in Classes are queued under Current; Since is the release, from which on a
// job of Previous can only have come from a binary still running it. A zero
// Since disables the late list.
type RenormalizeTarget struct {
	Previous, Current string
	Classes           []authornorm.DecisionClass
	Since             time.Time
}

// RenormalizeInputs lists up to limit inputs of one list after the given one
// (nil: from the start), in input order, that have a current author credit
// and no job of the new version.
func RenormalizeInputs(
	ctx context.Context, db pg.DBI, list RenormalizeList, t *RenormalizeTarget, after *RenormalizeInput, limit int,
) ([]RenormalizeInput, error) {
	set, ok := renormalizeListSQL[list]
	if !ok {
		return nil, fmt.Errorf("database: unknown re-normalization list %d", list)
	}
	if len(t.Classes) == 0 || (list == RenormalizeLate && t.Since.IsZero()) {
		return nil, nil
	}
	afterFingerprint, afterExtractor := []byte{}, ""
	if after != nil {
		afterFingerprint, afterExtractor = after.SourceFingerprint, after.ExtractorVersion
	}
	query := `SELECT DISTINCT x.source_fingerprint, x.extractor_version FROM (` + set + `) x
		WHERE (x.source_fingerprint, x.extractor_version) > (?4, ?5)
			AND ` + noCurrentJob + `
			AND ` + hasCurrentAuthorCredit + `
		ORDER BY 1, 2
		LIMIT ?6`
	var inputs []RenormalizeInput
	if _, err := db.QueryContext(ctx, &inputs, query, t.Previous, t.Current, pg.In(t.Classes), t.Since,
		afterFingerprint, afterExtractor, limit); err != nil {
		return nil, fmt.Errorf("listing inputs to re-normalize: %w", err)
	}
	return inputs, nil
}

// ReleaseTime is when the shipped policy of a normalizer version was
// registered — the migration that released the version — or the zero time
// when nothing is registered for it.
func ReleaseTime(ctx context.Context, db pg.DBI, normalizerVersion string) (time.Time, error) {
	var at *time.Time
	if _, err := db.QueryOneContext(ctx, pg.Scan(&at), `SELECT min(registered_at) FROM author_acceptance_class
		WHERE config_version = ? AND source = 'shipped'`, normalizerVersion); err != nil {
		return time.Time{}, fmt.Errorf("reading the release time: %w", err)
	}
	if at == nil {
		return time.Time{}, nil
	}
	return *at, nil
}

// QueueRenormalization queues one input under normalizerVersion and reports
// whether the job is new; a job of that version already queued or done is
// left as it is, so a repeated sweep queues nothing.
func QueueRenormalization(ctx context.Context, db pg.DBI, in RenormalizeInput, normalizerVersion string) (bool, error) {
	key, err := authornorm.NormalizationKey(fingerprintTo32(in.SourceFingerprint), in.ExtractorVersion, normalizerVersion)
	if err != nil {
		return false, err
	}
	res, err := db.ExecContext(ctx, insertJobSQL, key[:], in.SourceFingerprint, in.ExtractorVersion, normalizerVersion)
	if err != nil {
		return false, fmt.Errorf("queueing a re-normalization: %w", err)
	}
	return res.RowsAffected() == 1, nil
}
