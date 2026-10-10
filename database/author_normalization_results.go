package database

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"gopds-api/internal/authornorm"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The local normalization store of the author metadata pipeline: immutable
// local results shared by normalization key, the acceptance policy as the
// class table registers it, the one selection resolver every path goes
// through, and the completion accounting of a run.
//
// The resolver owns precedence (contract 3.11): an active credit override
// wins over a fingerprint override, which wins over the automatic outcome.
// The automatic outcome follows scope amendment A3: a registered class is
// selected with its policy version, malformed input is invalid, an ambiguous
// class is in review behind an open review item, and an eligible class the
// policy does not register is unresolved with policy_not_registered. Every
// change is audited by the database trigger; a resolution that changes
// nothing writes nothing. Like the source repository, every function runs
// inside the caller's connection or transaction and never opens its own.

var (
	// ErrNotLocalResult marks a result the local store does not take: only
	// structured and rules results are shared by normalization key.
	ErrNotLocalResult = errors.New("database: not a local normalization result")
	// ErrInvalidAutomaticOutcome marks an outcome without a result, or with
	// a decision outside the closed set.
	ErrInvalidAutomaticOutcome = errors.New("database: invalid automatic outcome")
	// ErrCreditNotResolvable marks a credit that does not exist or is not an
	// author credit; translators never enter the pipeline.
	ErrCreditNotResolvable = errors.New("database: credit is not a resolvable author credit")
	// ErrLocalJobNotFound marks a local normalization job ID with no row.
	ErrLocalJobNotFound = errors.New("database: local normalization job not found")
)

// localMethods is the SQL list of the methods sharing one result per key; it
// must match the predicate of contributor_normalization_result_one_local_per_key.
const localMethods = `('structured', 'rules')`

// duplicateComponentFlag is the credit quality flag the extractor sets when a
// name-bearing child repeats (contract 3.2).
const duplicateComponentFlag = string(authornorm.FlagDuplicateComponent)

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

const insertLocalResultSQL = `INSERT INTO contributor_normalization_result
	(source_fingerprint, normalization_key, extractor_version, normalizer_version, result_schema_version,
	 method, kind, status, decision_class, given_name, additional_names, family_name, nickname, prefix,
	 suffix, display_name, sort_name, search_key, script, quality_flags)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (normalization_key) WHERE method IN ` + localMethods + ` DO NOTHING
RETURNING id`

// InsertLocalResult stores a valid local result and returns its ID. A key
// already stored returns the existing row: any number of credits, workers or
// retries share the one immutable result of a normalization key.
func InsertLocalResult(ctx context.Context, db pg.DBI, r *authornorm.Result) (int64, error) {
	if err := r.Validate(); err != nil {
		return 0, err
	}
	if r.Method != authornorm.MethodStructured && r.Method != authornorm.MethodRules {
		return 0, ErrNotLocalResult
	}
	additional := []string{}
	if r.AdditionalNames != "" {
		additional = append(additional, r.AdditionalNames)
	}
	flags := make([]string, 0, len(r.QualityFlags))
	for _, flag := range r.QualityFlags {
		flags = append(flags, string(flag))
	}

	var id int64
	_, err := db.QueryOneContext(ctx, pg.Scan(&id), insertLocalResultSQL,
		r.SourceFingerprint[:], r.NormalizationKey[:], r.ExtractorVersion, r.NormalizerVersion,
		strconv.Itoa(r.SchemaVersion), string(r.Method), string(r.Kind), string(r.Status), string(r.DecisionClass),
		nullable(r.GivenName), pg.Array(additional), nullable(r.FamilyName), nullable(r.Nickname),
		nullable(r.Prefix), nullable(r.Suffix), nullable(r.DisplayName), nullable(r.SortName),
		nullable(r.SearchKey), nullable(string(r.Script)), pg.Array(flags))
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pg.ErrNoRows) {
		return 0, fmt.Errorf("storing a local result: %w", err)
	}
	// The key was stored first by another credit, worker or attempt.
	_, err = db.QueryOneContext(ctx, pg.Scan(&id), `SELECT id FROM contributor_normalization_result
		WHERE normalization_key = ? AND method IN `+localMethods, r.NormalizationKey[:])
	if err != nil {
		return 0, fmt.Errorf("reading the stored local result: %w", err)
	}
	return id, nil
}

// AutomaticOutcome is what the automatic pipeline concluded for one
// normalization input: the stored result and the policy decision on it, or a
// local normalization that failed for good.
type AutomaticOutcome struct {
	ResultID      int64
	DecisionClass authornorm.DecisionClass
	Decision      authornorm.Decision
	// Failed: no result exists; the credits are unresolved with
	// normalizer_failed.
	Failed bool
}

// ResolveReport counts where a resolution left the credits it touched.
type ResolveReport struct {
	States map[models.CreditSelectionState]int
}

// selectionTarget is one row of book_contributor_credit_selection to arrive
// at; manual marks an override-based target, which on the manual path may
// replace an admin's earlier decision, while an automatic one never does.
type selectionTarget struct {
	state    models.CreditSelectionState
	resultID *int64
	basis    *models.CreditSelectionBasis
	override *int64
	policy   *string
	reason   *models.UnresolvedReason
	class    authornorm.DecisionClass
	manual   bool
}

func automaticTarget(out *AutomaticOutcome) (selectionTarget, error) {
	if out.Failed {
		reason := models.UnresolvedNormalizerFailed
		return selectionTarget{state: models.CreditSelectionUnresolved, reason: &reason}, nil
	}
	if out.ResultID <= 0 {
		return selectionTarget{}, ErrInvalidAutomaticOutcome
	}
	result := out.ResultID
	t := selectionTarget{resultID: &result, class: out.DecisionClass}
	switch out.Decision.Outcome {
	case authornorm.OutcomeSelected:
		basis := models.CreditSelectionAutomatic
		policy := strconv.Itoa(out.Decision.PolicyVersion)
		t.state, t.basis, t.policy = models.CreditSelectionSelected, &basis, &policy
	case authornorm.OutcomeInvalid:
		t.state = models.CreditSelectionInvalid
	case authornorm.OutcomeUnresolved:
		reason := models.UnresolvedReason(out.Decision.Reason)
		t.state, t.reason = models.CreditSelectionUnresolved, &reason
	case authornorm.OutcomeReview:
		t.state = models.CreditSelectionReview
	default:
		return selectionTarget{}, ErrInvalidAutomaticOutcome
	}
	return t, nil
}

func overrideTarget(o *models.ContributorManualOverride) selectionTarget {
	basis := models.CreditSelectionFingerprintOverride
	if o.ScopeCreditID != nil {
		basis = models.CreditSelectionCreditOverride
	}
	result, override := o.ResultID, o.ID
	return selectionTarget{
		state: models.CreditSelectionSelected, resultID: &result, basis: &basis, override: &override, manual: true,
	}
}

// resolvableCredit is an author credit the resolver may decide.
type resolvableCredit struct {
	ID                int64
	SourceFingerprint []byte
}

// upsertSelectionSQL writes the target unless the row already says exactly
// that, so a repeated resolution leaves no audit record; unless ?8 — the
// manual path writing an override — it never replaces a decision an admin
// made.
const upsertSelectionSQL = `INSERT INTO book_contributor_credit_selection AS s
	(credit_id, source_fingerprint, state, result_id, basis, override_id, policy_version, unresolved_reason)
VALUES (?0, ?1, ?2, ?3, ?4, ?5, ?6, ?7)
ON CONFLICT (credit_id) DO UPDATE
SET state = EXCLUDED.state, result_id = EXCLUDED.result_id, basis = EXCLUDED.basis,
	override_id = EXCLUDED.override_id, policy_version = EXCLUDED.policy_version,
	unresolved_reason = EXCLUDED.unresolved_reason, decided_by_user_id = NULL, decided_at = now()
WHERE (s.state, s.result_id, s.basis, s.override_id, s.policy_version, s.unresolved_reason, s.decided_by_user_id)
		IS DISTINCT FROM (EXCLUDED.state, EXCLUDED.result_id, EXCLUDED.basis, EXCLUDED.override_id,
			EXCLUDED.policy_version, EXCLUDED.unresolved_reason, NULL::bigint)
	AND (s.decided_by_user_id IS NULL OR ?8)`

// openReviewSQL opens the fingerprint's review item for an ambiguous
// proposal; an item already open for the fingerprint covers the credit.
const openReviewSQL = `INSERT INTO contributor_review_item
	(scope_fingerprint, source_fingerprint, reason, decision_class, proposal_result_id)
VALUES (?0, ?0, ?1, ?2, ?3)
ON CONFLICT (scope_fingerprint, reason) WHERE status = 'open' AND scope_fingerprint IS NOT NULL DO NOTHING`

// ResolutionTx is a connection inside a transaction its caller owns and
// ends. Every function that writes a credit's resolution takes one: the
// credit lock the resolver takes is held only until the transaction ends, so
// on an autocommitted connection it would be released before the override is
// read and the selection written, and precedence would not hold. *pg.Tx is
// one; *pg.DB is not.
type ResolutionTx interface {
	pg.DBI
	Commit() error
	Rollback() error
}

// resolutionLockSQL serializes every resolution of one credit. NO KEY UPDATE
// conflicts with itself but not with the KEY SHARE lock a new override's
// foreign key takes, so writing an override never waits on a resolution;
// resolving the credit afterwards does.
const resolutionLockSQL = `SELECT id FROM book_contributor_credit WHERE id = ? FOR NO KEY UPDATE`

// resolveCredit is the one resolver: the credit's active override if it has
// one, else the automatic outcome, else nothing changes. It returns the state
// the credit is in afterwards ("" when it has no resolution).
//
// Precedence holds under concurrency because the override is read only after
// the credit's lock is taken, by a statement of its own: under READ COMMITTED
// that read sees every override committed before the lock was granted. A
// resolution that wrote an automatic outcome holds the lock until it commits,
// so a manual path — which writes its override, then resolves the credit in
// the same transaction — re-resolves after it and wins. All of this needs the
// lock, the override read and the write in one transaction: see ResolutionTx.
//
// Lock order: a credit's lock comes before any review item it touches. This
// resolver locks the credit and only then inserts the fingerprint's open
// review item, whose unique index waits for any uncommitted change to an open
// item of that fingerprint. A review action (phase 11) that closed or changed
// a review item first and resolved its credits afterwards would wait for a
// credit this resolver holds while the resolver waits for its item: a
// deadlock (40P01). Review actions must therefore lock every affected credit
// first — by resolving them, or with resolutionLockSQL — in ascending credit
// ID order, the order ResolveCredits uses, and change review items after.
//
// Decision order: the selection row is written only under the credit lock,
// so the order of its writes is the order of the decisions — not the start
// times of the transactions that made them, which can run the other way. An
// admin's decision recorded in the row (decided_by_user_id, today the
// review's "leave unresolved") is therefore newer than every override that
// was committed when it was written, and every override committed after it
// went through its writer's manual path — out nil, under this lock, before
// that writer committed — and replaced it there. So the automatic path never
// replaces an admin's decision, with an override target any more than with
// an automatic one; only the manual path does, which is how an explicit later
// correction supersedes it.
func resolveCredit(
	ctx context.Context,
	db ResolutionTx,
	credit resolvableCredit,
	out *AutomaticOutcome,
) (models.CreditSelectionState, error) {
	if _, err := db.ExecContext(ctx, resolutionLockSQL, credit.ID); err != nil {
		return "", fmt.Errorf("locking the credit: %w", err)
	}
	override, err := ActiveOverrideForCredit(db, credit.ID)
	if err != nil {
		return "", fmt.Errorf("reading the credit's override: %w", err)
	}
	var target selectionTarget
	switch {
	case override != nil:
		target = overrideTarget(override)
	case out != nil:
		if target, err = automaticTarget(out); err != nil {
			return "", err
		}
	default:
		return currentState(ctx, db, credit.ID)
	}

	// Only the manual path may replace an admin's decision. An override the
	// automatic path finds is never newer than a decision the row holds: see
	// the ordering rule above.
	replacesAdminDecision := target.manual && out == nil
	if _, err = db.ExecContext(ctx, upsertSelectionSQL,
		credit.ID, credit.SourceFingerprint, string(target.state), target.resultID, target.basis,
		target.override, target.policy, target.reason, replacesAdminDecision); err != nil {
		return "", fmt.Errorf("writing the credit's resolution: %w", err)
	}
	state, err := currentState(ctx, db, credit.ID)
	if err != nil {
		return "", err
	}
	if state == models.CreditSelectionReview && target.state == models.CreditSelectionReview {
		if _, err = db.ExecContext(ctx, openReviewSQL, credit.SourceFingerprint,
			string(models.ReviewAmbiguousDecision), string(target.class), target.resultID); err != nil {
			return "", fmt.Errorf("opening the review item: %w", err)
		}
	}
	return state, nil
}

func currentState(ctx context.Context, db pg.DBI, creditID int64) (models.CreditSelectionState, error) {
	var state models.CreditSelectionState
	_, err := db.QueryOneContext(ctx, pg.Scan(&state),
		`SELECT state FROM book_contributor_credit_selection WHERE credit_id = ?`, creditID)
	if errors.Is(err, pg.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading the credit's resolution: %w", err)
	}
	return state, nil
}

// ResolveCredits applies one automatic outcome to every current author credit
// of its normalization input — the exact source fingerprint under the exact
// extractor version — through the resolver. Translator credits and credits of
// superseded snapshots are not touched.
func ResolveCredits(
	ctx context.Context,
	db ResolutionTx,
	fingerprint []byte,
	extractorVersion string,
	out *AutomaticOutcome,
) (ResolveReport, error) {
	if _, err := automaticTarget(out); err != nil {
		return ResolveReport{}, err
	}
	var credits []resolvableCredit
	_, err := db.QueryContext(ctx, &credits, `SELECT c.id, c.source_fingerprint
		FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id
		WHERE c.source_fingerprint = ? AND s.extractor_version = ? AND s.is_current AND c.role = 'author'
		ORDER BY c.id`, fingerprint, extractorVersion)
	if err != nil {
		return ResolveReport{}, fmt.Errorf("listing the credits of an input: %w", err)
	}
	report := ResolveReport{States: map[models.CreditSelectionState]int{}}
	for i := range credits {
		state, resolveErr := resolveCredit(ctx, db, credits[i], out)
		if resolveErr != nil {
			return ResolveReport{}, resolveErr
		}
		report.States[state]++
	}
	return report, nil
}

// ResolveCreditSelection re-resolves one author credit. With out nil — the
// manual path, after an override was written — the credit's active override
// is applied if it has one and nothing changes otherwise; with an outcome it
// is the automatic path for that one credit.
func ResolveCreditSelection(ctx context.Context, db ResolutionTx, creditID int64, out *AutomaticOutcome) error {
	var credit resolvableCredit
	_, err := db.QueryOneContext(ctx, &credit, `SELECT id, source_fingerprint FROM book_contributor_credit
		WHERE id = ? AND role = 'author'`, creditID)
	if errors.Is(err, pg.ErrNoRows) {
		return ErrCreditNotResolvable
	}
	if err != nil {
		return fmt.Errorf("reading the credit: %w", err)
	}
	_, err = resolveCredit(ctx, db, credit, out)
	return err
}

// LocalNormalizationInput is one local job and the stored source it
// normalizes. HasAuthorCredit is false when no author credit — current or
// superseded — carries the job's fingerprint under its extractor version: a
// job seeded for translators only, which the worker refuses.
type LocalNormalizationInput struct {
	JobID             int64
	NormalizationKey  []byte
	SourceFingerprint []byte
	ExtractorVersion  string
	NormalizerVersion string
	// ResultID is the stored result of a completed job, nil otherwise.
	ResultID *int64

	HasAuthorCredit bool
	First           *string
	Middle          *string
	Last            *string
	Nickname        *string
	DisplayName     string
	// DuplicateComponent is set when any author credit of the input carries
	// the duplicate_component flag. The v1 fingerprint does not cover the
	// flag, so credits sharing a key may disagree; the input then fails
	// closed to the more ambiguous reading.
	DuplicateComponent bool
}

// LoadLocalNormalizationInput reads a job and the canonical source behind it.
func LoadLocalNormalizationInput(ctx context.Context, db pg.DBI, jobID int64) (LocalNormalizationInput, error) {
	in := LocalNormalizationInput{JobID: jobID}
	_, err := db.QueryOneContext(ctx, &in, `SELECT normalization_key, source_fingerprint,
			extractor_version, normalizer_version, result_id
		FROM contributor_normalization_job WHERE id = ?`, jobID)
	if errors.Is(err, pg.ErrNoRows) {
		return LocalNormalizationInput{}, ErrLocalJobNotFound
	}
	if err != nil {
		return LocalNormalizationInput{}, fmt.Errorf("reading the local job: %w", err)
	}

	var source struct {
		First       *string
		Middle      *string
		Last        *string
		Nickname    *string
		DisplayName string
		Duplicate   bool
	}
	_, err = db.QueryOneContext(ctx, &source, `SELECT c.source_first_name AS first,
			c.source_middle_name AS middle, c.source_last_name AS last, c.source_nickname AS nickname,
			c.source_display_name AS display_name,
			bool_or(? = ANY (c.quality_flags)) OVER () AS duplicate
		FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id
		WHERE c.source_fingerprint = ? AND s.extractor_version = ? AND c.role = 'author'
		ORDER BY c.id
		LIMIT 1`, duplicateComponentFlag, in.SourceFingerprint, in.ExtractorVersion)
	if errors.Is(err, pg.ErrNoRows) {
		return in, nil
	}
	if err != nil {
		return LocalNormalizationInput{}, fmt.Errorf("reading the job's source: %w", err)
	}
	in.HasAuthorCredit = true
	in.First, in.Middle, in.Last, in.Nickname = source.First, source.Middle, source.Last, source.Nickname
	in.DisplayName, in.DuplicateComponent = source.DisplayName, source.Duplicate
	return in, nil
}

// RecordLocalNormalization persists one computed local normalization inside
// the caller's transaction: the shared result of the key, the resolution of
// every current author credit of the input, and the owner-fenced completion
// of the job. A lost lease fails the completion with authornorm.ErrLeaseLost,
// and the caller's rollback discards the rest with it.
//
// The statements take their locks in the documented order — credits (and any
// review items the resolver opens) first, the job last — the same order a
// review retry uses. Completing the job before resolving the credits would
// hold the job's row lock while waiting for a credit a retry holds that is
// itself waiting for the job: the cycle PostgreSQL reports as 40P01. The
// resolution running before the fence is safe for the same reason the fence
// is: one transaction, so a lost lease rolls the resolutions back with it.
func RecordLocalNormalization(
	ctx context.Context,
	tx ResolutionTx,
	jobID int64,
	owner string,
	r *authornorm.Result,
	d authornorm.Decision,
) (ResolveReport, error) {
	resultID, err := InsertLocalResult(ctx, tx, r)
	if err != nil {
		return ResolveReport{}, err
	}
	report, resolveErr := ResolveCredits(ctx, tx, r.SourceFingerprint[:], r.ExtractorVersion, &AutomaticOutcome{
		ResultID: resultID, DecisionClass: r.DecisionClass, Decision: d,
	})
	if resolveErr != nil {
		return ResolveReport{}, resolveErr
	}
	if completeErr := CompleteLocalNormalizationJob(ctx, tx, jobID, owner, resultID); completeErr != nil {
		return ResolveReport{}, completeErr
	}
	return report, nil
}

// LocalInput is one normalization input: a source fingerprint under an
// extractor version.
type LocalInput struct {
	SourceFingerprint []byte
	ExtractorVersion  string
}

// FailedInputsToSettle lists up to limit inputs of local jobs of one
// normalizer version that ended failed — on a worker's last failed attempt or
// on a claim that found the last attempt abandoned — and still have current
// author credits the accounting counts as pending: without a resolution, or
// in review with no open review item (a review retry closed the item and
// handed the input back to this job, which then failed). The caller settles
// each in its own transaction with ResolveCredits and
// AutomaticOutcome{Failed: true}: the credits become unresolved with
// normalizer_failed unless an override decides them. A credit under an open
// review item stays with that item.
func FailedInputsToSettle(ctx context.Context, db pg.DBI, normalizerVersion string, limit int) ([]LocalInput, error) {
	var inputs []LocalInput
	_, err := db.QueryContext(ctx, &inputs, `SELECT DISTINCT j.source_fingerprint, j.extractor_version
		FROM contributor_normalization_job j
		JOIN book_contributor_credit c ON c.source_fingerprint = j.source_fingerprint AND c.role = 'author'
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
			AND s.extractor_version = j.extractor_version
		LEFT JOIN book_contributor_credit_selection sel ON sel.credit_id = c.id
		WHERE j.status = 'failed' AND j.normalizer_version = ?
			AND (sel.credit_id IS NULL
				OR (sel.state = 'review'
					AND NOT EXISTS (SELECT 1 FROM contributor_review_item i
						WHERE i.status = 'open' AND i.scope_fingerprint = c.source_fingerprint)
					AND NOT EXISTS (SELECT 1 FROM contributor_review_item i
						WHERE i.status = 'open' AND i.scope_credit_id = c.id)))
		ORDER BY 1, 2
		LIMIT ?`, normalizerVersion, limit)
	if err != nil {
		return nil, fmt.Errorf("listing failed local jobs: %w", err)
	}
	return inputs, nil
}

// completedInputsToReconcileSQL lists completed local jobs of one normalizer
// version whose input has a current author credit the stored result does not
// account for yet: a credit without a resolution — it arrived after the job
// completed, and its key was already queued, so it brought no job — or an
// automatic resolution made before a credit with the duplicate_component flag
// joined the input while the result was computed without it.
const completedInputsToReconcileSQL = `SELECT DISTINCT j.id
	FROM contributor_normalization_job j
	JOIN contributor_normalization_result r ON r.id = j.result_id
	JOIN book_contributor_credit c ON c.source_fingerprint = j.source_fingerprint AND c.role = 'author'
	JOIN book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
		AND s.extractor_version = j.extractor_version
	LEFT JOIN book_contributor_credit_selection sel ON sel.credit_id = c.id
	WHERE j.status = 'completed' AND j.normalizer_version = ?0
		AND (sel.credit_id IS NULL
			OR (sel.state <> 'review' AND sel.override_id IS NULL AND sel.decided_by_user_id IS NULL
				AND NOT (?1 = ANY (r.quality_flags))
				AND EXISTS (SELECT 1 FROM book_contributor_credit d
					JOIN book_metadata_snapshot ds ON ds.id = d.snapshot_id
					WHERE d.source_fingerprint = j.source_fingerprint AND d.role = 'author'
						AND ds.extractor_version = j.extractor_version
						AND ?1 = ANY (d.quality_flags))))
	ORDER BY j.id
	LIMIT ?2`

// CompletedInputsToReconcile returns up to limit IDs of completed local jobs
// of one normalizer version whose current author credits the stored result
// does not account for yet; the worker resolves them from that result.
func CompletedInputsToReconcile(ctx context.Context, db pg.DBI, normalizerVersion string, limit int) ([]int64, error) {
	var ids []int64
	if _, err := db.QueryContext(ctx, &ids, completedInputsToReconcileSQL,
		normalizerVersion, duplicateComponentFlag, limit); err != nil {
		return nil, fmt.Errorf("listing completed inputs to reconcile: %w", err)
	}
	return ids, nil
}

// LoadLocalResult reads a stored local result back as the domain value, so
// the policy can decide it again without normalizing again.
func LoadLocalResult(ctx context.Context, db pg.DBI, id int64) (authornorm.Result, error) {
	row := new(models.ContributorNormalizationResult)
	if err := db.ModelContext(ctx, row).Where("id = ?", id).Select(); err != nil {
		return authornorm.Result{}, fmt.Errorf("reading a local result: %w", err)
	}
	if row.Method != models.NormalizationStructured && row.Method != models.NormalizationRules {
		return authornorm.Result{}, ErrNotLocalResult
	}
	schema, err := strconv.Atoi(row.ResultSchemaVersion)
	if err != nil {
		return authornorm.Result{}, fmt.Errorf("%w: schema version %q", authornorm.ErrInvalidSchemaVersion, row.ResultSchemaVersion)
	}
	r := authornorm.Result{
		GivenName: deref(row.GivenName), FamilyName: deref(row.FamilyName), Nickname: deref(row.Nickname),
		Prefix: deref(row.Prefix), Suffix: deref(row.Suffix), DisplayName: deref(row.DisplayName),
		SortName: deref(row.SortName), SearchKey: deref(row.SearchKey),
		Script: authornorm.Script(deref(row.Script)), Kind: authornorm.Kind(row.Kind),
		Status: authornorm.Status(row.Status), Method: authornorm.Method(row.Method),
		DecisionClass:     authornorm.DecisionClass(deref(row.DecisionClass)),
		ExtractorVersion:  deref(row.ExtractorVersion),
		NormalizerVersion: deref(row.NormalizerVersion),
		SchemaVersion:     schema,
	}
	if len(row.AdditionalNames) > 0 {
		r.AdditionalNames = row.AdditionalNames[0]
	}
	for _, flag := range row.QualityFlags {
		r.QualityFlags = append(r.QualityFlags, authornorm.QualityFlag(flag))
	}
	copy(r.SourceFingerprint[:], row.SourceFingerprint)
	copy(r.NormalizationKey[:], row.NormalizationKey)
	if validateErr := r.Validate(); validateErr != nil {
		return authornorm.Result{}, validateErr
	}
	return r, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// CreditAccounting is where the current author credits of a run (or of the
// whole catalog) stand. Pending counts every credit that is not accounted:
// no resolution yet, a local job of its input still pending, or a review
// state without an open review item.
type CreditAccounting struct {
	Credits    int
	Selected   int
	Invalid    int
	Review     int
	Unresolved map[models.UnresolvedReason]int
	Pending    int
}

// Settled reports the local-normalization half of run completion (contract
// 3.9, amendment A3): every credit is selected, invalid, unresolved with a
// closed reason, or in open review. An open review backlog does not block it.
func (a CreditAccounting) Settled() bool { return a.Pending == 0 }

// The credit accounting judges every current author credit in scope and
// groups the verdicts. The scope is its own CTE per caller — the catalog, or
// the run's books joined through its items — and the pending local inputs are
// one distinct set joined to the credits, so the plan stays joins at any size.
// A subquery under an OR (the former "?0 IS NULL OR book_id IN (...)" and
// "OR EXISTS (pending job)") is planned as a SubPlan per credit, and once its
// hashed form misses work_mem it rescans the whole set for every credit: the
// catalog-sized run never completed on it. The open review checks stay
// correlated: each is one lookup in a unique partial index.
const (
	accountingCatalogScope = `WITH credits AS (
	SELECT c.id, c.source_fingerprint, s.extractor_version
	FROM book_contributor_credit c
	JOIN book_metadata_snapshot s ON s.id = c.snapshot_id
	WHERE s.is_current AND c.role = 'author'
)`
	accountingRunScope = `WITH credits AS (
	SELECT c.id, c.source_fingerprint, s.extractor_version
	FROM author_metadata_run_item ri
	JOIN book_metadata_snapshot s ON s.book_id = ri.book_id AND s.is_current
	JOIN book_contributor_credit c ON c.snapshot_id = s.id AND c.role = 'author'
	WHERE ri.run_id = ?0
)`
	accountingVerdictsSQL = `, pending_inputs AS (
	SELECT DISTINCT source_fingerprint, extractor_version
	FROM contributor_normalization_job WHERE status = 'pending'
), judged AS (
	SELECT sel.state, sel.unresolved_reason,
		(sel.credit_id IS NULL
			OR pi.source_fingerprint IS NOT NULL
			OR (sel.state = 'review'
				AND NOT EXISTS (SELECT 1 FROM contributor_review_item i
					WHERE i.status = 'open' AND i.scope_fingerprint = cr.source_fingerprint)
				AND NOT EXISTS (SELECT 1 FROM contributor_review_item i
					WHERE i.status = 'open' AND i.scope_credit_id = cr.id))) AS pending
	FROM credits cr
	LEFT JOIN book_contributor_credit_selection sel ON sel.credit_id = cr.id
	LEFT JOIN pending_inputs pi
		ON pi.source_fingerprint = cr.source_fingerprint AND pi.extractor_version = cr.extractor_version
)
SELECT coalesce(state, '') AS state, coalesce(unresolved_reason, '') AS reason, pending, count(*) AS credits
FROM judged
GROUP BY 1, 2, 3`
)

func creditAccounting(ctx context.Context, db pg.DBI, runID *int64) (CreditAccounting, error) {
	var groups []struct {
		State   models.CreditSelectionState
		Reason  models.UnresolvedReason
		Pending bool
		Credits int
	}
	query, params := accountingCatalogScope+accountingVerdictsSQL, []interface{}{}
	if runID != nil {
		query, params = accountingRunScope+accountingVerdictsSQL, []interface{}{*runID}
	}
	if _, err := db.QueryContext(ctx, &groups, query, params...); err != nil {
		return CreditAccounting{}, fmt.Errorf("accounting author credits: %w", err)
	}
	var a CreditAccounting
	for _, g := range groups {
		a.Credits += g.Credits
		if g.Pending {
			a.Pending += g.Credits
			continue
		}
		switch g.State {
		case models.CreditSelectionSelected:
			a.Selected += g.Credits
		case models.CreditSelectionInvalid:
			a.Invalid += g.Credits
		case models.CreditSelectionReview:
			a.Review += g.Credits
		case models.CreditSelectionUnresolved:
			if a.Unresolved == nil {
				a.Unresolved = map[models.UnresolvedReason]int{}
			}
			a.Unresolved[g.Reason] += g.Credits
		}
	}
	return a, nil
}

// AuthorCreditAccountingForRun accounts the current author credits of the
// run's books.
func AuthorCreditAccountingForRun(ctx context.Context, db pg.DBI, runID int64) (CreditAccounting, error) {
	return creditAccounting(ctx, db, &runID)
}

// AuthorCreditAccounting accounts every current author credit of the catalog.
func AuthorCreditAccounting(ctx context.Context, db pg.DBI) (CreditAccounting, error) {
	return creditAccounting(ctx, db, nil)
}
