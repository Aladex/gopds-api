package database

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"gopds-api/internal/authornorm"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The acceptance policy store (migration 25): which (decision class, script)
// pairs are registered for automatic selection, and the reads the acceptance
// pass uses to select the credits a registration covers.
//
// Policy versions are cumulative integers. Version 1 is the empty policy
// (authornorm.BasePolicyVersion); the registrations ship with the code, each
// change adding its pairs under the next version, and the policy of version N
// is every pair registered in versions 1..N for the normalizer configuration
// in force. A selection names the version it was made under.
//
// Like the rest of the package, every function runs inside the caller's
// connection or transaction and never opens its own.

// CurrentAcceptancePolicyVersion is the newest policy version: the highest
// registered one, or the shipped empty version when nothing is registered.
func CurrentAcceptancePolicyVersion(ctx context.Context, db pg.DBI) (int, error) {
	var version int
	_, err := db.QueryOneContext(ctx, pg.Scan(&version),
		`SELECT greatest(coalesce(max(policy_version::int), 0), ?) FROM author_acceptance_class`,
		authornorm.BasePolicyVersion)
	if err != nil {
		return 0, fmt.Errorf("reading the acceptance policy version: %w", err)
	}
	return version, nil
}

// LoadAcceptancePolicy builds the policy of one exact version, evaluated for
// one exact normalizer configuration, from the class table: every pair
// registered for that configuration in this version or an earlier one. No row
// means the empty policy: nothing is selected automatically. A row the domain
// refuses — an ambiguous or unknown class, an unknown script — fails the load,
// so a bad registration stops selection instead of widening it.
func LoadAcceptancePolicy(
	ctx context.Context,
	db pg.DBI,
	version int,
	normalizerVersion string,
) (authornorm.AcceptancePolicy, error) {
	if version <= 0 {
		return authornorm.AcceptancePolicy{}, authornorm.ErrInvalidPolicyVersion
	}
	var rows []models.AuthorAcceptanceClass
	err := db.ModelContext(ctx, &rows).
		Where("policy_version::int <= ?", version).
		Where("config_version = ?", normalizerVersion).
		Order("id").
		Select()
	if err != nil {
		return authornorm.AcceptancePolicy{}, fmt.Errorf("loading the acceptance policy: %w", err)
	}
	registrations := make([]authornorm.ClassRegistration, 0, len(rows))
	for i := range rows {
		rowVersion, convErr := strconv.Atoi(rows[i].PolicyVersion)
		if convErr != nil {
			return authornorm.AcceptancePolicy{}, authornorm.ErrInvalidPolicyVersion
		}
		var evidence [32]byte
		copy(evidence[:], rows[i].EvidenceReportSHA256)
		registrations = append(registrations, authornorm.ClassRegistration{
			PolicyVersion:  rowVersion,
			DecisionClass:  authornorm.DecisionClass(rows[i].DecisionClass),
			Script:         authornorm.Script(rows[i].Script),
			EvidenceSHA256: evidence,
		})
	}
	return authornorm.NewAcceptancePolicy(version, normalizerVersion, registrations)
}

// LoadCurrentAcceptancePolicy builds the newest policy for one normalizer
// configuration. A registration committed after the version was read is not
// in it, and the next load picks it up.
func LoadCurrentAcceptancePolicy(
	ctx context.Context,
	db pg.DBI,
	normalizerVersion string,
) (authornorm.AcceptancePolicy, error) {
	version, err := CurrentAcceptancePolicyVersion(ctx, db)
	if err != nil {
		return authornorm.AcceptancePolicy{}, err
	}
	return LoadAcceptancePolicy(ctx, db, version, normalizerVersion)
}

// currentLocalResultsSQL joins every current author credit to the local
// result of its normalization input under the normalizer version ?0: the one
// result its fingerprint, its snapshot's extractor version and that
// normalizer version name.
const currentLocalResultsSQL = `FROM book_contributor_credit c
	JOIN book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
	JOIN contributor_normalization_result r ON r.source_fingerprint = c.source_fingerprint
		AND r.extractor_version = s.extractor_version AND r.normalizer_version = ?0
		AND r.method IN ` + localMethods + `
	LEFT JOIN book_contributor_credit_selection sel ON sel.credit_id = c.id
	WHERE c.role = 'author'`

// AcceptanceCandidate is one normalization input with credits a registration
// covers: unresolved with policy_not_registered, resting on the input's
// current local result, whose pair is registered in the policy version.
type AcceptanceCandidate struct {
	SourceFingerprint []byte
	ExtractorVersion  string
	ResultID          int64
}

// waitingForRegisteredPairSQL narrows currentLocalResultsSQL to the credits a
// registration in policy version ?1 covers: unresolved for want of a
// registration, with no override or administrator's decision, resting on the
// input's current local result, whose pair is registered by that version.
const waitingForRegisteredPairSQL = currentLocalResultsSQL + `
		AND sel.state = 'unresolved' AND sel.unresolved_reason = 'policy_not_registered'
		AND sel.decided_by_user_id IS NULL AND sel.override_id IS NULL AND sel.result_id = r.id
		AND EXISTS (SELECT 1 FROM author_acceptance_class a
			WHERE a.decision_class = r.decision_class AND a.script = r.script
				AND a.config_version = r.normalizer_version AND a.policy_version::int <= ?1)`

// acceptanceCandidatesSQL pages through the inputs in (fingerprint,
// extractor) order after the key ?2/?3, so one pass visits each input once
// even when one of them cannot be selected.
const acceptanceCandidatesSQL = `SELECT DISTINCT c.source_fingerprint, s.extractor_version, r.id AS result_id
	` + waitingForRegisteredPairSQL + `
		AND (?2::bytea IS NULL OR (c.source_fingerprint, s.extractor_version) > (?2, ?3))
	ORDER BY 1, 2
	LIMIT ?4`

// AcceptanceCandidates returns up to limit inputs after the given key (nil
// fingerprint: from the start) whose credits the policy of version now
// selects. Only the normalizer version ?0 counts.
func AcceptanceCandidates(
	ctx context.Context,
	db pg.DBI,
	normalizerVersion string,
	version int,
	after *AcceptanceCandidate,
	limit int,
) ([]AcceptanceCandidate, error) {
	var afterFingerprint []byte
	var afterExtractor string
	if after != nil {
		afterFingerprint, afterExtractor = after.SourceFingerprint, after.ExtractorVersion
	}
	candidates := []AcceptanceCandidate{}
	if _, err := db.QueryContext(ctx, &candidates, acceptanceCandidatesSQL,
		normalizerVersion, version, afterFingerprint, afterExtractor, limit); err != nil {
		return nil, fmt.Errorf("listing acceptance candidates: %w", err)
	}
	return candidates, nil
}

// selectionGate is the part of a credit's resolution the acceptance pass
// reads under the credit's lock.
type selectionGate struct {
	State            models.CreditSelectionState
	Basis            *models.CreditSelectionBasis
	UnresolvedReason *models.UnresolvedReason
	ResultID         *int64
	OverrideID       *int64
	DecidedByUserID  *int64
}

// waitsForPolicy reports a resolution the acceptance pass may change: still
// unresolved for want of a registration, on the input's current result, with
// no override and no administrator's decision behind it. Anything else — a
// selection, an open review, an invalid source, an override, a review
// decision — was decided by something the policy never overrides.
func (g *selectionGate) waitsForPolicy(resultID int64) bool {
	return g.State == models.CreditSelectionUnresolved &&
		g.UnresolvedReason != nil && *g.UnresolvedReason == models.UnresolvedPolicyNotRegistered &&
		g.OverrideID == nil && g.DecidedByUserID == nil &&
		g.ResultID != nil && *g.ResultID == resultID
}

// AcceptancePage is one transaction's share of a candidate input: how many
// credits it listed, which it selected, and the last credit ID it listed,
// which the next page continues after.
type AcceptancePage struct {
	Listed      int
	Selected    int
	SelectedIDs []int64
	Last        int64
}

// normalizationInputLockClass is the first key of the per-input advisory
// locks ("auth"); the second is derived from the input. The two-key form keeps
// them apart from the single-key locks (the migration lock).
const normalizationInputLockClass int32 = 0x61757468

// normalizationInputLockKey derives the input's second lock key. Two inputs
// sharing a key only wait for each other.
func normalizationInputLockKey(fingerprint []byte, extractorVersion string) int32 {
	h := sha256.New()
	h.Write(fingerprint)
	h.Write([]byte{0})
	h.Write([]byte(extractorVersion))
	return int32(binary.BigEndian.Uint32(h.Sum(nil)[:4])) // #nosec G115 -- a hash folded into the lock's key space
}

// LockNormalizationInput takes the input's transaction lock: the author
// credits of one fingerprint under one extractor version. The credit writer
// holds it while it adds author credits (PersistExtraction) and the acceptance
// pass while it checks the input's ambiguity and selects a page of its
// credits, so the check and the page see the same credits.
func LockNormalizationInput(ctx context.Context, tx ResolutionTx, fingerprint []byte, extractorVersion string) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(?, ?)`,
		normalizationInputLockClass, normalizationInputLockKey(fingerprint, extractorVersion)); err != nil {
		return fmt.Errorf("locking the normalization input: %w", err)
	}
	return nil
}

// lockAuthorInputs takes the lock of every input the author rows join, in
// ascending key order so two writers never wait for each other in a cycle.
func lockAuthorInputs(ctx context.Context, conn pg.DBI, extractorVersion string, rows []creditRow) error {
	keys := make([]int32, 0, len(rows))
	for i := range rows {
		if rows[i].role == models.ContributorRoleAuthor {
			keys = append(keys, normalizationInputLockKey(rows[i].fingerprint[:], extractorVersion))
		}
	}
	slices.Sort(keys)
	for _, key := range slices.Compact(keys) {
		if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_xact_lock(?, ?)`, normalizationInputLockClass, key); err != nil {
			return fmt.Errorf("locking a normalization input: %w", err)
		}
	}
	return nil
}

// SelectRegisteredCredits applies an automatic selection to the credits of one
// candidate input that still wait for the policy, inside the caller's
// transaction: at most limit current author credits with an ID above after, in
// ID order, so the transaction's locks and work stay bounded however many
// credits the input has. Each credit is locked and re-read first, so a
// decision made since the candidate was listed stands; the write itself goes
// through the one resolver, so an override still wins.
func SelectRegisteredCredits(
	ctx context.Context,
	tx ResolutionTx,
	candidate *AcceptanceCandidate,
	out *AutomaticOutcome,
	after int64,
	limit int,
) (AcceptancePage, error) {
	var page AcceptancePage
	if out == nil || out.ResultID != candidate.ResultID || !out.Decision.Selected() || limit <= 0 {
		return page, ErrInvalidAutomaticOutcome
	}
	var credits []resolvableCredit
	_, err := tx.QueryContext(ctx, &credits, `SELECT c.id, c.source_fingerprint
		FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id
		WHERE c.source_fingerprint = ? AND s.extractor_version = ? AND s.is_current AND c.role = 'author'
			AND c.id > ?
		ORDER BY c.id
		LIMIT ?`, candidate.SourceFingerprint, candidate.ExtractorVersion, after, limit)
	if err != nil {
		return page, fmt.Errorf("listing the credits of a candidate: %w", err)
	}
	page.Listed = len(credits)
	for i := range credits {
		page.Last = credits[i].ID
		if _, err = tx.ExecContext(ctx, resolutionLockSQL, credits[i].ID); err != nil {
			return page, fmt.Errorf("locking the credit: %w", err)
		}
		var gate selectionGate
		gate, found, gateErr := readSelectionGate(ctx, tx, credits[i].ID)
		if gateErr != nil {
			return page, gateErr
		}
		if !found || !gate.waitsForPolicy(candidate.ResultID) {
			continue
		}
		state, resolveErr := resolveCredit(ctx, tx, credits[i], out)
		if resolveErr != nil {
			return page, resolveErr
		}
		if state == models.CreditSelectionSelected {
			page.Selected++
			page.SelectedIDs = append(page.SelectedIDs, credits[i].ID)
		}
	}
	return page, nil
}

// selectedAutomaticallyOn reports a selection the acceptance pass makes: an
// automatic one on the given result, with no override and no administrator's
// decision behind it.
func (g *selectionGate) selectedAutomaticallyOn(resultID int64) bool {
	return g.State == models.CreditSelectionSelected && g.Basis != nil &&
		*g.Basis == models.CreditSelectionAutomatic && g.ResultID != nil && *g.ResultID == resultID &&
		g.OverrideID == nil && g.DecidedByUserID == nil
}

// waitingOutcome reports the outcome a demotion may write: the candidate's
// result, unresolved for want of a registration.
func waitingOutcome(candidate *AcceptanceCandidate, out *AutomaticOutcome) bool {
	return out != nil && out.ResultID == candidate.ResultID &&
		out.Decision.Outcome == authornorm.OutcomeUnresolved && out.Decision.Reason == authornorm.ReasonPolicyNotRegistered
}

func readSelectionGate(ctx context.Context, tx ResolutionTx, creditID int64) (selectionGate, bool, error) {
	var gate selectionGate
	_, err := tx.QueryOneContext(ctx, &gate, `SELECT state, basis, unresolved_reason, result_id, override_id,
			decided_by_user_id
		FROM book_contributor_credit_selection WHERE credit_id = ?`, creditID)
	if errors.Is(err, pg.ErrNoRows) {
		return gate, false, nil
	}
	if err != nil {
		return gate, false, fmt.Errorf("reading the credit's resolution: %w", err)
	}
	return gate, true, nil
}

// DemoteAcceptanceSelections takes back automatic selections the acceptance
// pass made on the candidate's result, inside the caller's transaction: each
// credit still selected automatically on that result, with no override and no
// administrator's decision, goes back to the outcome out — unresolved with
// policy_not_registered, where the pass found it. A credit decided by
// anything else since stands. It returns how many it demoted.
func DemoteAcceptanceSelections(
	ctx context.Context,
	tx ResolutionTx,
	candidate *AcceptanceCandidate,
	creditIDs []int64,
	out *AutomaticOutcome,
) (int, error) {
	if !waitingOutcome(candidate, out) {
		return 0, ErrInvalidAutomaticOutcome
	}
	ids := slices.Clone(creditIDs)
	slices.Sort(ids)
	demoted := 0
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, resolutionLockSQL, id); err != nil {
			return demoted, fmt.Errorf("locking the credit: %w", err)
		}
		gate, found, err := readSelectionGate(ctx, tx, id)
		if err != nil {
			return demoted, err
		}
		if !found || !gate.selectedAutomaticallyOn(candidate.ResultID) {
			continue
		}
		var credit resolvableCredit
		if _, err = tx.QueryOneContext(ctx, &credit, `SELECT id, source_fingerprint FROM book_contributor_credit
			WHERE id = ?`, id); err != nil {
			return demoted, fmt.Errorf("reading the credit: %w", err)
		}
		state, resolveErr := resolveCredit(ctx, tx, credit, out)
		if resolveErr != nil {
			return demoted, resolveErr
		}
		if state == models.CreditSelectionUnresolved {
			demoted++
		}
	}
	return demoted, nil
}

// InputHasDuplicateComponent reports whether any author credit of the input —
// its fingerprint under its extractor version, current or superseded — carries
// the duplicate_component flag: the same reading LoadLocalNormalizationInput
// gives the local worker. The v1 fingerprint does not cover the flag, so a
// credit with it can join an input whose result was computed without it, and
// the input is then ambiguous.
func InputHasDuplicateComponent(ctx context.Context, db pg.DBI, fingerprint []byte, extractorVersion string) (bool, error) {
	var duplicate bool
	_, err := db.QueryOneContext(ctx, pg.Scan(&duplicate), `SELECT EXISTS (
			SELECT 1 FROM book_contributor_credit c
			JOIN book_metadata_snapshot s ON s.id = c.snapshot_id
			WHERE c.source_fingerprint = ? AND s.extractor_version = ? AND c.role = 'author'
				AND ? = ANY (c.quality_flags))`, fingerprint, extractorVersion, duplicateComponentFlag)
	if err != nil {
		return false, fmt.Errorf("reading the input's duplicate flag: %w", err)
	}
	return duplicate, nil
}
