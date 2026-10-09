package database

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopds-api/internal/authornorm"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The manual review half of the author metadata pipeline (contract 3.11):
// immutable manual corrections as new results and overrides, the review
// actions that decide open items, the review queue's stable pagination, and
// the linked books an admin sees on the review screen.
//
// Precedence is not implemented here: every override write re-resolves its
// credits through the one phase-10 resolver (resolveCredit), which owns
// credit override > fingerprint override > automatic. Every writing function
// runs inside the caller's ResolutionTx and never opens or ends a
// transaction.
//
// Lock order (the rule documented at resolveCredit and in the phase-10
// report): a review action locks every credit its item touches — in
// ascending credit-ID order, the order ResolveCredits uses — before it
// changes the review item. The resolver holds a credit's lock while its open
// review item waits on the open-item unique index; changing an item first
// and locking credits after would wait for a credit the resolver holds while
// it waits for the item, which PostgreSQL reports as a deadlock. Overrides
// are written before the re-resolution (the phase-10 manual path): the KEY
// SHARE their credit foreign key takes does not conflict with the resolver's
// NO KEY UPDATE lock.

var (
	// ErrInvalidOverrideScope marks an override without exactly one
	// well-formed scope: one positive credit ID or one 32-byte fingerprint.
	ErrInvalidOverrideScope = errors.New("database: an override needs exactly one well-formed scope")
	// ErrInvalidManualCorrection marks correction fields the manual result
	// would refuse.
	ErrInvalidManualCorrection = errors.New("database: invalid manual correction")
	// ErrInvalidReviewActor marks a non-positive admin user ID.
	ErrInvalidReviewActor = errors.New("database: review decisions need a positive admin user ID")
	// ErrInvalidReviewAction marks a decision outside the closed set, or one
	// without the fields its action needs.
	ErrInvalidReviewAction = errors.New("database: invalid review decision")
	// ErrReviewNotFound marks a review item ID with no row.
	ErrReviewNotFound = errors.New("database: review item not found")
	// ErrReviewConflict marks an item another reviewer already decided: the
	// first transition wins and the caller's rollback discards the rest.
	ErrReviewConflict = errors.New("database: review item already decided")
	// ErrReviewNothingToRetry marks a retry whose input's extractor version
	// cannot be recovered.
	ErrReviewNothingToRetry = errors.New("database: review retry found no extractor version")
	// ErrReviewRetryExhausted marks a retry the local worker could not
	// execute: the input's job has used every attempt of the worker's budget.
	// The retry is refused rather than acknowledged, and the caller's
	// rollback leaves the item open.
	ErrReviewRetryExhausted = errors.New("database: the retried job has no attempts left")
)

// fingerprintLen is the length of a raw SHA-256 source fingerprint.
const fingerprintLen = 32

// OverrideScope is what one override applies to: exactly one of a credit or a
// source fingerprint.
type OverrideScope struct {
	CreditID    int64
	Fingerprint []byte
}

func (s OverrideScope) validate() error {
	hasCredit, hasFingerprint := s.CreditID != 0, len(s.Fingerprint) != 0
	if hasCredit == hasFingerprint {
		return ErrInvalidOverrideScope
	}
	if s.CreditID < 0 || (hasFingerprint && (len(s.Fingerprint) != fingerprintLen)) {
		return ErrInvalidOverrideScope
	}
	return nil
}

// ManualCorrection is the persisted form of one admin's decision about a
// name: the fields of the immutable manual result an override selects. It is
// deliberately not the automatic result type — a manual result carries no
// normalization key and no extractor or normalizer version, so a version
// change never invalidates it.
type ManualCorrection struct {
	GivenName      string
	AdditionalName string
	FamilyName     string
	Nickname       string
	Prefix         string
	Suffix         string
	DisplayName    string
	SortName       string
	SearchKey      string
	Script         string
	Kind           models.NormalizationKind
}

// validate checks the correction the way the schema will: a selectable kind,
// a known script, and the non-empty display name and search key a normalized
// result owes.
func (c *ManualCorrection) validate() error {
	switch c.Kind {
	case models.NormalizationPerson, models.NormalizationCollective, models.NormalizationUnknown:
	case models.NormalizationMalformed:
		return fmt.Errorf("%w: a manual result cannot select a malformed kind", ErrInvalidManualCorrection)
	default:
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidManualCorrection, c.Kind)
	}
	if err := authornorm.Script(c.Script).Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidManualCorrection, err)
	}
	if strings.TrimSpace(c.DisplayName) == "" || strings.TrimSpace(c.SearchKey) == "" {
		return fmt.Errorf("%w: a normalized manual result needs a display name and a search key", ErrInvalidManualCorrection)
	}
	return nil
}

// manualResultColumns is the shared field list of a manual result insert.
const manualResultColumns = `source_fingerprint, result_schema_version, method, kind, status,
	given_name, additional_names, family_name, nickname, prefix, suffix,
	display_name, sort_name, search_key, script, quality_flags, created_by_user_id`

func manualResultValues(fingerprint []byte, c *ManualCorrection, admin int64) []interface{} {
	additional := []string{}
	if c.AdditionalName != "" {
		additional = append(additional, c.AdditionalName)
	}
	return []interface{}{
		fingerprint, strconv.Itoa(authornorm.ResultSchemaVersion), models.NormalizationManual, c.Kind,
		models.NormalizationNormalized,
		nullable(c.GivenName), pg.Array(additional), nullable(c.FamilyName), nullable(c.Nickname),
		nullable(c.Prefix), nullable(c.Suffix), nullable(c.DisplayName), nullable(c.SortName),
		nullable(c.SearchKey), nullable(c.Script), pg.Array([]string{}), admin,
	}
}

const insertManualResultSQL = `INSERT INTO contributor_normalization_result
	(` + manualResultColumns + `)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
RETURNING id`

// reviewItemRow is the review item as the actions read it.
type reviewItemRow struct {
	ID                int64
	ScopeCreditID     *int64
	ScopeFingerprint  []byte
	SourceFingerprint []byte
	Reason            models.ReviewReason
	ProposalResultID  *int64
	Status            models.ReviewStatus
}

const reviewItemSQL = `SELECT id, scope_credit_id, scope_fingerprint, source_fingerprint, reason,
	proposal_result_id, status FROM contributor_review_item WHERE id = ?`

// OverrideReport is what one override write did.
type OverrideReport struct {
	ResultID   int64
	OverrideID int64
	Resolved   map[models.CreditSelectionState]int
}

// currentAuthorCreditsOfFingerprint lists the current author credits that
// carry the exact fingerprint, in ascending ID order — the order every
// locking path uses.
func currentAuthorCreditsOfFingerprint(ctx context.Context, db pg.DBI, fingerprint []byte) ([]resolvableCredit, error) {
	var credits []resolvableCredit
	_, err := db.QueryContext(ctx, &credits, `SELECT c.id, c.source_fingerprint
		FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id
		WHERE c.source_fingerprint = ? AND s.is_current AND c.role = 'author'
		ORDER BY c.id`, fingerprint)
	if err != nil {
		return nil, fmt.Errorf("listing the credits of a fingerprint: %w", err)
	}
	return credits, nil
}

// affectedCreditsOfItem lists the credits an item's decision touches, in
// ascending ID order: the scoped credit for a credit item, every current
// author credit of the fingerprint for a fingerprint item.
func affectedCreditsOfItem(ctx context.Context, db pg.DBI, item *reviewItemRow) ([]resolvableCredit, error) {
	if item.ScopeCreditID != nil {
		var credit resolvableCredit
		_, err := db.QueryOneContext(ctx, &credit, `SELECT id, source_fingerprint
			FROM book_contributor_credit WHERE id = ? AND role = 'author'`, *item.ScopeCreditID)
		if errors.Is(err, pg.ErrNoRows) {
			return nil, ErrCreditNotResolvable
		}
		if err != nil {
			return nil, fmt.Errorf("reading the item's credit: %w", err)
		}
		return []resolvableCredit{credit}, nil
	}
	return currentAuthorCreditsOfFingerprint(ctx, db, item.SourceFingerprint)
}

// insertOverride writes the immutable override of one scope over one result.
func insertOverride(ctx context.Context, tx ResolutionTx, scope OverrideScope, fingerprint []byte, resultID, admin int64) (int64, error) {
	var overrideID int64
	_, err := tx.QueryOneContext(ctx, pg.Scan(&overrideID), `INSERT INTO contributor_manual_override
		(scope_credit_id, scope_fingerprint, source_fingerprint, result_id, created_by_user_id)
		VALUES (?, ?, ?, ?, ?)
		RETURNING id`, nullableInt(scope.CreditID), scope.Fingerprint, fingerprint, resultID, admin)
	if err != nil {
		return 0, fmt.Errorf("writing the override: %w", err)
	}
	return overrideID, nil
}

// resolveAffected re-resolves credits through the one resolver with no
// automatic outcome — the manual path: an active override is applied and
// nothing changes otherwise.
func resolveAffected(ctx context.Context, tx ResolutionTx, credits []resolvableCredit) (map[models.CreditSelectionState]int, error) {
	states := map[models.CreditSelectionState]int{}
	for i := range credits {
		state, err := resolveCredit(ctx, tx, credits[i], nil)
		if err != nil {
			return nil, err
		}
		states[state]++
	}
	return states, nil
}

// ApplyOverride writes one admin correction as an immutable manual result and
// the scope's newest override, then re-resolves every current author credit
// the scope covers through the one resolver — override first,
// resolveCredit(…, nil) after, in the caller's ResolutionTx (the phase-10
// manual path). An edit is the next override of the same scope: the newest
// wins, and the earlier result and override stay untouched as history. A
// fingerprint override with no credits yet is fine: credits persisted later
// resolve under it.
func ApplyOverride(
	ctx context.Context,
	tx ResolutionTx,
	scope OverrideScope,
	admin int64,
	correction *ManualCorrection,
) (OverrideReport, error) {
	if err := scope.validate(); err != nil {
		return OverrideReport{}, err
	}
	if err := correction.validate(); err != nil {
		return OverrideReport{}, err
	}
	if admin <= 0 {
		return OverrideReport{}, ErrInvalidReviewActor
	}

	var fingerprint []byte
	var credits []resolvableCredit
	if scope.CreditID != 0 {
		var credit resolvableCredit
		_, err := tx.QueryOneContext(ctx, &credit, `SELECT id, source_fingerprint
			FROM book_contributor_credit WHERE id = ? AND role = 'author'`, scope.CreditID)
		if errors.Is(err, pg.ErrNoRows) {
			return OverrideReport{}, ErrCreditNotResolvable
		}
		if err != nil {
			return OverrideReport{}, fmt.Errorf("reading the override's credit: %w", err)
		}
		fingerprint, credits = credit.SourceFingerprint, []resolvableCredit{credit}
	} else {
		fingerprint = scope.Fingerprint
		var err error
		credits, err = currentAuthorCreditsOfFingerprint(ctx, tx, fingerprint)
		if err != nil {
			return OverrideReport{}, err
		}
	}

	var resultID int64
	_, err := tx.QueryOneContext(ctx, pg.Scan(&resultID), insertManualResultSQL, manualResultValues(fingerprint, correction, admin)...)
	if err != nil {
		return OverrideReport{}, fmt.Errorf("writing the manual result: %w", err)
	}
	overrideID, err := insertOverride(ctx, tx, scope, fingerprint, resultID, admin)
	if err != nil {
		return OverrideReport{}, err
	}
	states, err := resolveAffected(ctx, tx, credits)
	if err != nil {
		return OverrideReport{}, err
	}
	return OverrideReport{ResultID: resultID, OverrideID: overrideID, Resolved: states}, nil
}

// ReviewAction is the closed set of review transitions (contract 3.11).
type ReviewAction string

const (
	ReviewAccept          ReviewAction = "accepted"
	ReviewEdit            ReviewAction = "edited"
	ReviewClassify        ReviewAction = "classified"
	ReviewLeaveUnresolved ReviewAction = "left_unresolved"
	ReviewRetry           ReviewAction = "retried"
)

// ReviewDecision is one admin's decision on one open review item.
type ReviewDecision struct {
	Action ReviewAction
	// Correction carries the edited name for ReviewEdit.
	Correction *ManualCorrection
	// Kind is the classification for ReviewClassify.
	Kind models.NormalizationKind
	// RetryNormalizerVersion is the version the retry re-queues the input
	// under (scope amendment A4: retry re-runs the local normalizer, never
	// an LLM; a changed version is a new normalization key and therefore a
	// new immutable result, an unchanged one reuses the stored result).
	RetryNormalizerVersion string
	// RetryMaxAttempts is the local worker's attempt budget
	// (LeaseClaimOptions.MaxAttempts). Attempt numbers are immutable history,
	// so a reopened job keeps its count, and a job that has used the whole
	// budget would only be ended again by the next claim: such a retry is
	// refused instead of acknowledged.
	RetryMaxAttempts int
}

func (d ReviewDecision) validate() error {
	switch d.Action {
	case ReviewAccept, ReviewLeaveUnresolved, ReviewRetry:
	case ReviewEdit:
		if d.Correction == nil {
			return fmt.Errorf("%w: an edit needs a correction", ErrInvalidReviewAction)
		}
		if err := d.Correction.validate(); err != nil {
			return err
		}
	case ReviewClassify:
		switch d.Kind {
		case models.NormalizationPerson, models.NormalizationCollective, models.NormalizationUnknown:
		case models.NormalizationMalformed:
			return fmt.Errorf("%w: a manual result cannot select a malformed kind", ErrInvalidReviewAction)
		default:
			return fmt.Errorf("%w: unknown classification %q", ErrInvalidReviewAction, d.Kind)
		}
	default:
		return fmt.Errorf("%w: unknown action %q", ErrInvalidReviewAction, d.Action)
	}
	if d.Action == ReviewRetry && strings.TrimSpace(d.RetryNormalizerVersion) == "" {
		return fmt.Errorf("%w: a retry needs the normalizer version", ErrInvalidReviewAction)
	}
	if d.Action == ReviewRetry && d.RetryMaxAttempts <= 0 {
		return fmt.Errorf("%w: a retry needs the worker's attempt budget", ErrInvalidReviewAction)
	}
	return nil
}

// ReviewReport is what one review action did.
type ReviewReport struct {
	// ResultID is the manual result an accept, edit or classify selected;
	// zero otherwise.
	ResultID int64
	// JobQueued: a retry created a pending local job (false when the input
	// was already queued).
	JobQueued bool
	Credits   int
}

// proposalAsCorrection copies a proposal result into the manual correction an
// accept keeps or a classify re-kinds: the manual result must not carry the
// proposal's normalization key or versions, only its decided name.
func proposalAsCorrection(
	ctx context.Context, tx ResolutionTx, item *reviewItemRow, kind models.NormalizationKind,
) (ManualCorrection, []byte, error) {
	if item.ProposalResultID == nil {
		return ManualCorrection{}, nil, ErrInvalidReviewAction
	}
	row := new(models.ContributorNormalizationResult)
	if err := tx.ModelContext(ctx, row).Where("id = ?", *item.ProposalResultID).Select(); err != nil {
		return ManualCorrection{}, nil, fmt.Errorf("reading the proposal: %w", err)
	}
	additional := ""
	if len(row.AdditionalNames) > 0 {
		additional = row.AdditionalNames[0]
	}
	correction := ManualCorrection{
		GivenName: deref(row.GivenName), AdditionalName: additional,
		FamilyName: deref(row.FamilyName), Nickname: deref(row.Nickname),
		Prefix: deref(row.Prefix), Suffix: deref(row.Suffix),
		DisplayName: deref(row.DisplayName), SortName: deref(row.SortName),
		SearchKey: deref(row.SearchKey), Script: deref(row.Script),
	}
	if kind != "" {
		correction.Kind = kind
	} else {
		correction.Kind = row.Kind
	}
	return correction, row.SourceFingerprint, nil
}

// closeReviewSQL closes the open item exactly once: the status predicate
// makes the second concurrent reviewer's update match no row, which the
// caller reports as ErrReviewConflict after its rollback.
const closeReviewSQL = `UPDATE contributor_review_item
	SET status = 'closed', resolution = ?, resolution_result_id = ?, resolved_by_user_id = ?, finished_at = now()
	WHERE id = ? AND status = 'open'`

func closeReviewItem(ctx context.Context, tx ResolutionTx, itemID, resultID, admin int64, action ReviewAction) error {
	res, err := tx.ExecContext(ctx, closeReviewSQL, string(action), nullableInt(resultID), admin, itemID)
	if err != nil {
		return fmt.Errorf("closing the review item: %w", err)
	}
	if res.RowsAffected() != 1 {
		return ErrReviewConflict
	}
	return nil
}

func nullableInt(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

func stringPtr(s string) *string { return &s }

// leaveUnresolvedSQL writes the admin's closed unresolved verdict. It is a
// terminal human decision like an override: it replaces any earlier
// automatic or override resolution, and a later automatic resolution will
// not replace it back, nor revive an older override (the resolver's
// decision-order rule at resolveCredit). decided_at records when, not the
// order: that is the order of the writes under the credit lock. The
// DISTINCT FROM predicate keeps a repeat from writing an audit record for no
// change.
const leaveUnresolvedSQL = `INSERT INTO book_contributor_credit_selection AS s
	(credit_id, source_fingerprint, state, unresolved_reason, decided_by_user_id)
VALUES (?0, ?1, 'unresolved', 'review_left_unresolved', ?2)
ON CONFLICT (credit_id) DO UPDATE
SET state = 'unresolved', result_id = NULL, basis = NULL, override_id = NULL, policy_version = NULL,
	unresolved_reason = 'review_left_unresolved', decided_by_user_id = EXCLUDED.decided_by_user_id, decided_at = now()
WHERE (s.state, s.result_id, s.basis, s.override_id, s.policy_version, s.unresolved_reason, s.decided_by_user_id)
	IS DISTINCT FROM ('unresolved', NULL, NULL, NULL, NULL, 'review_left_unresolved', EXCLUDED.decided_by_user_id)`

// retryExtractor recovers the extractor version a retry's normalization key
// needs: the proposal's own input first, else any current credit of the
// scope's fingerprint.
func retryExtractor(ctx context.Context, tx ResolutionTx, item *reviewItemRow) (string, error) {
	if item.ProposalResultID != nil {
		var extractor string
		_, err := tx.QueryOneContext(ctx, pg.Scan(&extractor),
			`SELECT extractor_version FROM contributor_normalization_result WHERE id = ?`, *item.ProposalResultID)
		if err == nil && strings.TrimSpace(extractor) != "" {
			return extractor, nil
		}
		if err != nil && !errors.Is(err, pg.ErrNoRows) {
			return "", fmt.Errorf("reading the proposal's extractor version: %w", err)
		}
	}
	var extractor string
	_, err := tx.QueryOneContext(ctx, pg.Scan(&extractor), `SELECT s.extractor_version
		FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id
		WHERE c.source_fingerprint = ? AND s.is_current AND c.role = 'author'
		ORDER BY c.id LIMIT 1`, item.SourceFingerprint)
	if errors.Is(err, pg.ErrNoRows) {
		return "", ErrReviewNothingToRetry
	}
	if err != nil {
		return "", fmt.Errorf("reading the input's extractor version: %w", err)
	}
	return extractor, nil
}

// retryLocalJob re-queues the input under the given normalizer version: a
// changed version is a new key and gets a new pending job; an unchanged one
// finds the stored job and — if it ended failed or completed — reopens it as
// pending with a cleared lease. The attempt identities stay monotonic: the
// counter is never reset, so the next claim appends the next attempt number
// instead of colliding with the immutable history.
//
// The worker's budget is compared with that same counter, so a stored job
// that has used all maxAttempts could never be claimed again: reopening it
// would acknowledge a retry that only the next claim's exhaustion ends, with
// the item closed and the credit left in review. Such a retry is refused
// with ErrReviewRetryExhausted. The job is locked for the check — the last
// lock of the order, after the credits and the item.
func retryLocalJob(
	ctx context.Context, tx ResolutionTx, fingerprint []byte, extractor, normalizerVersion string, maxAttempts int,
) (bool, error) {
	key, err := authornorm.NormalizationKey(fingerprintTo32(fingerprint), extractor, normalizerVersion)
	if err != nil {
		return false, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO contributor_normalization_job
		(normalization_key, source_fingerprint, extractor_version, normalizer_version)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (normalization_key) DO NOTHING`, key[:], fingerprint, extractor, normalizerVersion)
	if err != nil {
		return false, fmt.Errorf("queueing the retry: %w", err)
	}
	if res.RowsAffected() == 1 {
		return true, nil
	}
	var job struct {
		ID           int64
		Status       models.NormalizationJobStatus
		AttemptCount int
	}
	if _, err = tx.QueryOneContext(ctx, &job, `SELECT id, status, attempt_count
		FROM contributor_normalization_job WHERE normalization_key = ? FOR UPDATE`, key[:]); err != nil {
		return false, fmt.Errorf("reading the retried job: %w", err)
	}
	if job.AttemptCount >= maxAttempts {
		return false, ErrReviewRetryExhausted
	}
	if job.Status == models.NormalizationJobPending {
		return false, nil
	}
	if _, err = tx.ExecContext(ctx, `UPDATE contributor_normalization_job
		SET status = 'pending', result_id = NULL, last_error_class = NULL,
			lease_owner = NULL, lease_expires_at = NULL,
			next_attempt_at = now(), finished_at = NULL
		WHERE id = ?`, job.ID); err != nil {
		return false, fmt.Errorf("reopening the retried job: %w", err)
	}
	return false, nil
}

func fingerprintTo32(f []byte) [32]byte {
	var raw [32]byte
	copy(raw[:], f)
	return raw
}

// ApplyReviewAction decides one open review item inside the caller's
// ResolutionTx, following the lock-order rule: the item's affected credits
// are locked first, in ascending ID order, and only then does the item — or
// anything the resolver may wait on — change. The first transition wins; a
// concurrent or repeated decision on the same item returns ErrReviewConflict
// and the caller's rollback discards whatever it wrote.
func ApplyReviewAction(
	ctx context.Context,
	tx ResolutionTx,
	itemID, admin int64,
	decision ReviewDecision,
) (ReviewReport, error) {
	if itemID <= 0 {
		return ReviewReport{}, ErrReviewNotFound
	}
	if admin <= 0 {
		return ReviewReport{}, ErrInvalidReviewActor
	}
	if err := decision.validate(); err != nil {
		return ReviewReport{}, err
	}

	var item reviewItemRow
	_, err := tx.QueryOneContext(ctx, &item, reviewItemSQL, itemID)
	if errors.Is(err, pg.ErrNoRows) {
		return ReviewReport{}, ErrReviewNotFound
	}
	if err != nil {
		return ReviewReport{}, fmt.Errorf("reading the review item: %w", err)
	}
	if item.Status != models.ReviewOpen {
		return ReviewReport{}, ErrReviewConflict
	}

	// The lock-order rule: every affected credit, ascending, before any
	// review-item write.
	credits, err := affectedCreditsOfItem(ctx, tx, &item)
	if err != nil {
		return ReviewReport{}, err
	}
	for i := range credits {
		if _, lockErr := tx.ExecContext(ctx, resolutionLockSQL, credits[i].ID); lockErr != nil {
			return ReviewReport{}, fmt.Errorf("locking the review's credits: %w", lockErr)
		}
	}

	report := ReviewReport{Credits: len(credits)}
	switch decision.Action {
	case ReviewAccept, ReviewEdit, ReviewClassify:
		report.ResultID, err = decideWithOverride(ctx, tx, &item, credits, admin, decision)
	case ReviewLeaveUnresolved:
		err = applyLeaveUnresolved(ctx, tx, &item, credits, admin)
	case ReviewRetry:
		// The lock order is credits, then review items, then jobs: the item
		// closes before the retry touches the job queue, so a worker
		// completion that follows the same order can never form a cycle with
		// it.
		if closeErr := closeReviewItem(ctx, tx, itemID, 0, admin, decision.Action); closeErr != nil {
			return ReviewReport{}, closeErr
		}
		report.JobQueued, err = applyRetry(ctx, tx, &item, &decision)
	default:
		return ReviewReport{}, fmt.Errorf("%w: unknown action %q", ErrInvalidReviewAction, decision.Action)
	}
	if err != nil {
		return ReviewReport{}, err
	}
	return report, nil
}

// decideWithOverride performs an accept, edit or classify: the manual result
// (the proposal copied, or the admin's correction), the scope's newest
// override, and the re-resolution of the item's credits.
func decideWithOverride(
	ctx context.Context,
	tx ResolutionTx,
	item *reviewItemRow,
	credits []resolvableCredit,
	admin int64,
	decision ReviewDecision,
) (int64, error) {
	correction, fingerprint := decision.Correction, item.SourceFingerprint
	if decision.Action != ReviewEdit {
		proposed, _, corrErr := proposalAsCorrection(ctx, tx, item, decision.Kind)
		if corrErr != nil {
			return 0, corrErr
		}
		correction = &proposed
	}
	if validErr := correction.validate(); validErr != nil {
		return 0, validErr
	}
	var resultID int64
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&resultID), insertManualResultSQL,
		manualResultValues(fingerprint, correction, admin)...); err != nil {
		return 0, fmt.Errorf("writing the manual result: %w", err)
	}
	scope := OverrideScope{Fingerprint: item.ScopeFingerprint}
	if item.ScopeCreditID != nil {
		scope = OverrideScope{CreditID: *item.ScopeCreditID, Fingerprint: nil}
	}
	if _, err := insertOverride(ctx, tx, scope, fingerprint, resultID, admin); err != nil {
		return 0, err
	}
	if _, err := resolveAffected(ctx, tx, credits); err != nil {
		return 0, err
	}
	if err := closeReviewItem(ctx, tx, item.ID, resultID, admin, decision.Action); err != nil {
		return 0, err
	}
	return resultID, nil
}

// applyLeaveUnresolved writes the closed unresolved verdict on every credit
// the item covers, then closes the item.
func applyLeaveUnresolved(
	ctx context.Context, tx ResolutionTx, item *reviewItemRow, credits []resolvableCredit, admin int64,
) error {
	for i := range credits {
		if _, err := tx.ExecContext(ctx, leaveUnresolvedSQL, credits[i].ID, credits[i].SourceFingerprint, admin); err != nil {
			return fmt.Errorf("leaving the credit unresolved: %w", err)
		}
	}
	return closeReviewItem(ctx, tx, item.ID, 0, admin, ReviewLeaveUnresolved)
}

// applyRetry re-queues the item's input under the decision's normalizer
// version and attempt budget.
func applyRetry(ctx context.Context, tx ResolutionTx, item *reviewItemRow, decision *ReviewDecision) (bool, error) {
	extractor, err := retryExtractor(ctx, tx, item)
	if err != nil {
		return false, err
	}
	return retryLocalJob(ctx, tx, item.SourceFingerprint, extractor, decision.RetryNormalizerVersion,
		decision.RetryMaxAttempts)
}

// ReviewListFilter pages the open review queue with the (created_at, id)
// cursor the schema's open-page index backs: created_at alone repeats or
// skips rows whenever two items share a timestamp, which one transaction
// seeding several items produces naturally.
type ReviewListFilter struct {
	Limit int
	// AfterCreatedAt and AfterID are the cursor: items strictly after
	// (AfterCreatedAt, AfterID). The zero values start from the beginning.
	AfterCreatedAt time.Time
	AfterID        int64
	// Reason narrows the queue to one review reason when set.
	Reason models.ReviewReason
}

// DefaultReviewListLimit is the page size the queue serves when the caller
// asks for none; MaxReviewListLimit caps it.
const (
	DefaultReviewListLimit = 100
	MaxReviewListLimit     = 100
)

func (f *ReviewListFilter) normalize() error {
	if f.Limit == 0 {
		f.Limit = DefaultReviewListLimit
	}
	if f.Limit < 0 || f.Limit > MaxReviewListLimit {
		return fmt.Errorf("%w: limit %d", ErrInvalidReviewAction, f.Limit)
	}
	if f.AfterID < 0 {
		return fmt.Errorf("%w: negative cursor ID", ErrInvalidReviewAction)
	}
	if f.AfterID != 0 && f.AfterCreatedAt.IsZero() {
		return fmt.Errorf("%w: cursor ID without a timestamp", ErrInvalidReviewAction)
	}
	return nil
}

// ListOpenReviewItems returns one page of the open review queue in
// (created_at, id) order after the cursor.
func ListOpenReviewItems(ctx context.Context, db pg.DBI, filter ReviewListFilter) ([]models.ContributorReviewItem, error) {
	if err := filter.normalize(); err != nil {
		return nil, err
	}
	var after *time.Time
	if !filter.AfterCreatedAt.IsZero() {
		after = &filter.AfterCreatedAt
	}
	var reason *string
	if filter.Reason != "" {
		reason = stringPtr(string(filter.Reason))
	}
	var items []models.ContributorReviewItem
	_, err := db.QueryContext(ctx, &items, `SELECT * FROM contributor_review_item
		WHERE status = 'open'
			AND (?::timestamptz IS NULL OR (created_at, id) > (?, ?))
			AND (?::text IS NULL OR reason = ?)
		ORDER BY created_at, id
		LIMIT ?`, after, after, filter.AfterID, reason, reason, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("listing open review items: %w", err)
	}
	return items, nil
}

// ReviewLinkedBook is one book whose current snapshot carries a credit of a
// source fingerprint. It exists for the admin review screen only (privacy
// 3.11/3.14): linked books are shown to a person and never enter any
// provider or queue input — with no LLM stream, nothing else consumes them.
type ReviewLinkedBook struct {
	BookID int64
	Title  string
}

// LinkedBooksOfFingerprint lists the books — id and title only — whose
// current snapshot carries an author or translator credit with the exact
// source fingerprint.
func LinkedBooksOfFingerprint(ctx context.Context, db pg.DBI, fingerprint []byte, limit int) ([]ReviewLinkedBook, error) {
	if len(fingerprint) != fingerprintLen {
		return nil, ErrInvalidOverrideScope
	}
	if limit <= 0 {
		limit = 50
	}
	var books []ReviewLinkedBook
	_, err := db.QueryContext(ctx, &books, `SELECT DISTINCT s.book_id, b.title
		FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id
		JOIN opds_catalog_book b ON b.id = s.book_id
		WHERE c.source_fingerprint = ? AND s.is_current
		ORDER BY s.book_id
		LIMIT ?`, fingerprint, limit)
	if err != nil {
		return nil, fmt.Errorf("listing linked books: %w", err)
	}
	return books, nil
}

// FlagManualSchemaIncompatibility opens a review item — reason
// incompatible_manual_schema — for every scope whose active override selects
// a manual result written under a different result schema version than
// currentSchemaVersion (contract 3.11: the old result stays in history, the
// decision is flagged for review, not rewritten). Manual rows are immutable,
// so the visible flag lives on the open item and in the service's view; the
// selection is untouched. Opening is idempotent: a scope already under
// review for this reason is left alone.
func FlagManualSchemaIncompatibility(ctx context.Context, db pg.DBI, currentSchemaVersion int) (int, error) {
	if currentSchemaVersion <= 0 {
		return 0, authornorm.ErrInvalidSchemaVersion
	}
	res, err := db.ExecContext(ctx, `WITH active AS (
			SELECT DISTINCT ON (coalesce(scope_credit_id, 0), coalesce(scope_fingerprint, '\x'::bytea))
				scope_credit_id, scope_fingerprint, source_fingerprint, result_id
			FROM contributor_manual_override
			ORDER BY coalesce(scope_credit_id, 0), coalesce(scope_fingerprint, '\x'::bytea), id DESC
		)
		INSERT INTO contributor_review_item (scope_credit_id, scope_fingerprint, source_fingerprint, reason, proposal_result_id)
		SELECT a.scope_credit_id, a.scope_fingerprint, a.source_fingerprint, 'incompatible_manual_schema', a.result_id
		FROM active a
		JOIN contributor_normalization_result r ON r.id = a.result_id
		WHERE r.result_schema_version <> ?
		ON CONFLICT DO NOTHING`, strconv.Itoa(currentSchemaVersion))
	if err != nil {
		return 0, fmt.Errorf("flagging incompatible manual schemas: %w", err)
	}
	return res.RowsAffected(), nil
}
