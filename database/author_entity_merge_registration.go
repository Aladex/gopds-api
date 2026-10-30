package database

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"gopds-api/internal/authorentity"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// Registration of a merge stratum and the compare-and-insert of its
// decisions (migration 68, plan E4d, §4.4, §4.7). A kind is registered for
// a merge configuration from an eval report that passed it; the build
// applies a decision only while its registration stands and its script is
// not blocked. K's acceptance policy is never read, written or locked here.

// MergeGateJudgeConsensus is the only truth standard a merge registration
// records (§3.4).
const MergeGateJudgeConsensus = "judge_consensus"

// Why a model decision does not apply whatever its candidate (the build's
// lapse reasons, §4.7).
const (
	MergeWithheldUnregistered  = "unregistered"
	MergeWithheldWithdrawn     = "withdrawn"
	MergeWithheldScriptBlocked = "script_blocked"
)

var (
	// ErrMergeRegistrationExists refuses a second registration of a kind
	// for one merge configuration, withdrawn or not.
	ErrMergeRegistrationExists = errors.New("database: the kind is registered for the merge configuration already")
	// ErrMergeRegistrationNotLive is a withdrawal or a block of a
	// registration that is withdrawn or does not exist.
	ErrMergeRegistrationNotLive = errors.New("database: the merge registration is withdrawn or does not exist")
	// ErrMergeRegistrationInvalid is a registration the schema would refuse.
	ErrMergeRegistrationInvalid = errors.New("database: invalid merge registration")
	// ErrAuthorEntityVetoUnanchored refuses every decision insert while a
	// live veto is unanchored (§4.4 step 5, §4.11).
	ErrAuthorEntityVetoUnanchored = errors.New("database: a live merge veto is unanchored")
	// ErrAuthorEntityNoCompletedBuild refuses an insert before any build
	// completed: there are no candidates to compare with.
	ErrAuthorEntityNoCompletedBuild = errors.New("database: no author entity build has completed")
	// ErrMergeApplyDriftHeld refuses an apply's insert once a participant
	// of its merge configuration drifted (MergeApplyDriftHeld).
	ErrMergeApplyDriftHeld = errors.New("database: a merge participant drifted; the apply is held")
)

// MergeRegistration is one registration with its blocked scripts.
type MergeRegistration struct {
	ID                   int64      `pg:"id" json:"id"`
	ConfigVersion        string     `pg:"config_version" json:"config_version"`
	Kind                 string     `pg:"kind" json:"kind"`
	EvidenceReportID     int64      `pg:"evidence_report_id" json:"evidence_report_id"`
	EvidenceReportSHA256 []byte     `pg:"evidence_report_sha256" json:"-"`
	Gate                 string     `pg:"gate" json:"gate"`
	RegisteredAt         time.Time  `pg:"registered_at" json:"registered_at"`
	WithdrawnAt          *time.Time `pg:"withdrawn_at" json:"withdrawn_at,omitempty"`
	BlockedScripts       []string   `pg:"blocked_scripts,array" json:"blocked_scripts"`
	// RunID is the eval run of the evidence report.
	RunID int64 `pg:"run_id" json:"run_id"`
}

// MergeRegistrationInput is a registration to insert; the caller has read
// the report and checked that it passed the kind.
type MergeRegistrationInput struct {
	ConfigVersion string
	Kind          string
	ReportID      int64
	ReportSHA256  []byte
	Gate          string
	ActorID       int64
	// Adjudications, when set, binds the report to the labels it was
	// computed from: a label written since refuses the registration
	// (ErrMergeRegistrationEvidenceStale).
	Adjudications *AuthorLLMAdjudicationBinding
}

// ValidMergeKind reports a kind a registration may name.
func ValidMergeKind(kind string) bool {
	switch kind {
	case authorentity.CandidateSwap, authorentity.CandidateInitials, authorentity.CandidateTranslit,
		authorentity.CandidateBucket:
		return true
	}
	return false
}

// ValidMergeScript reports a script a block may name.
func ValidMergeScript(script string) bool {
	switch script {
	case authorentity.ScriptCyrl, authorentity.ScriptLatn, authorentity.ScriptCyrlLatn:
		return true
	}
	return false
}

// ErrMergeRegistrationEvidenceStale refuses a registration whose report's
// labels changed since the report was computed: it must be recomputed.
var ErrMergeRegistrationEvidenceStale = errors.New(
	"database: a label the merge eval report gates on changed since it was computed")

// RegisterMergeKind inserts a registration. It writes no other table: no
// policy version is taken and nothing of K's acceptance is locked. With
// in.Adjudications it first checks, in the same transaction and holding
// the adjudications SHARE, that the report's labels are still the ones it
// read.
func RegisterMergeKind(ctx context.Context, db pg.DBI, in *MergeRegistrationInput) (int64, error) {
	if !ValidMergeKind(in.Kind) || in.Gate != MergeGateJudgeConsensus || len(in.ReportSHA256) != 32 ||
		in.ConfigVersion == "" || in.ReportID <= 0 {
		return 0, ErrMergeRegistrationInvalid
	}
	var id int64
	register := func(tx *pg.Tx) error {
		if in.Adjudications != nil {
			current, err := adjudicationsCurrent(ctx, tx, in.Adjudications)
			if err != nil {
				return err
			}
			if !current {
				return ErrMergeRegistrationEvidenceStale
			}
		}
		return insertMergeRegistration(ctx, tx, in, &id)
	}
	// Inside a caller's transaction it is a part of it: RunInTransaction on
	// a transaction would end it.
	if tx, ok := db.(*pg.Tx); ok {
		return id, register(tx)
	}
	err := db.RunInTransaction(ctx, register)
	return id, err
}

// insertMergeRegistration inserts the registration row.
func insertMergeRegistration(ctx context.Context, tx *pg.Tx, in *MergeRegistrationInput, id *int64) error {
	_, err := tx.QueryOneContext(ctx, pg.Scan(id), `
		INSERT INTO public.author_entity_merge_registration (config_version, kind, evidence_report_id,
			evidence_report_sha256, gate, registered_by_user_id)
		VALUES (?, ?, ?, ?, ?, nullif(?, 0)) RETURNING id`,
		in.ConfigVersion, in.Kind, in.ReportID, in.ReportSHA256, in.Gate, in.ActorID)
	if pgErrorCode(err) == "23505" {
		return ErrMergeRegistrationExists
	}
	if err != nil {
		return fmt.Errorf("registering a merge kind: %w", err)
	}
	return nil
}

// WithdrawMergeRegistration withdraws a registration: the next build lapses
// every decision of its kind (withdrawn) and splits their merges. It does
// not wait for builds: a build past its snapshot is stopped by the fence.
func WithdrawMergeRegistration(ctx context.Context, db pg.DBI, id, actor int64) error {
	res, err := db.ExecContext(ctx, `
		UPDATE public.author_entity_merge_registration SET withdrawn_at = now(), withdrawn_by_user_id = nullif(?, 0)
		WHERE id = ? AND withdrawn_at IS NULL`, actor, id)
	if err != nil {
		return fmt.Errorf("withdrawing a merge registration: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrMergeRegistrationNotLive
	}
	return nil
}

// BlockMergeScript blocks one script of a live registration: the next build
// lapses its decisions of that script (script_blocked). A block already
// there is kept as it is.
func BlockMergeScript(ctx context.Context, db pg.DBI, registrationID int64, script string, actor int64) error {
	if !ValidMergeScript(script) {
		return ErrMergeRegistrationInvalid
	}
	res, err := db.ExecContext(ctx, `
		INSERT INTO public.author_entity_merge_script_block (registration_id, script, blocked_by_user_id)
		SELECT id, ?, nullif(?, 0) FROM public.author_entity_merge_registration WHERE id = ? AND withdrawn_at IS NULL
		ON CONFLICT (registration_id, script) DO NOTHING`, script, actor, registrationID)
	if err != nil {
		return fmt.Errorf("blocking a merge script: %w", err)
	}
	if res.RowsAffected() == 0 {
		var live bool
		if _, err = db.QueryOneContext(ctx, pg.Scan(&live), `SELECT EXISTS (SELECT 1
			FROM public.author_entity_merge_registration WHERE id = ? AND withdrawn_at IS NULL)`,
			registrationID); err != nil {
			return fmt.Errorf("reading a merge registration: %w", err)
		}
		if !live {
			return ErrMergeRegistrationNotLive
		}
	}
	return nil
}

const mergeRegistrationColumns = `r.id, r.config_version, r.kind, r.evidence_report_id, r.evidence_report_sha256,
	r.gate, r.registered_at, r.withdrawn_at, rep.run_id,
	coalesce((SELECT array_agg(b.script ORDER BY b.script) FROM public.author_entity_merge_script_block b
		WHERE b.registration_id = r.id), '{}') AS blocked_scripts`

// MergeRegistrations lists the registrations, newest first.
func MergeRegistrations(ctx context.Context, db pg.DBI) ([]MergeRegistration, error) {
	var rows []MergeRegistration
	if _, err := db.QueryContext(ctx, &rows, `SELECT `+mergeRegistrationColumns+`
		FROM public.author_entity_merge_registration r
		JOIN public.author_llm_eval_report rep ON rep.id = r.evidence_report_id
		ORDER BY r.id DESC`); err != nil {
		return nil, fmt.Errorf("reading the merge registrations: %w", err)
	}
	return rows, nil
}

// MergeRegistrationByID is a registration; nil when there is none.
func MergeRegistrationByID(ctx context.Context, db pg.DBI, id int64) (*MergeRegistration, error) {
	var rows []MergeRegistration
	if _, err := db.QueryContext(ctx, &rows, `SELECT `+mergeRegistrationColumns+`
		FROM public.author_entity_merge_registration r
		JOIN public.author_llm_eval_report rep ON rep.id = r.evidence_report_id
		WHERE r.id = ?`, id); err != nil {
		return nil, fmt.Errorf("reading a merge registration: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// MergeRegistrationOf is the registration of a kind for a merge
// configuration; nil when there is none.
func MergeRegistrationOf(ctx context.Context, db pg.DBI, configVersion, kind string) (*MergeRegistration, error) {
	var rows []MergeRegistration
	if _, err := db.QueryContext(ctx, &rows, `SELECT `+mergeRegistrationColumns+`
		FROM public.author_entity_merge_registration r
		JOIN public.author_llm_eval_report rep ON rep.id = r.evidence_report_id
		WHERE r.config_version = ? AND r.kind = ?`, configVersion, kind); err != nil {
		return nil, fmt.Errorf("reading a merge registration: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// The start lock of the entity build ('ebld', 0), in the two-key form like
// the author layer's: StartAuthorEntityBuild and
// InsertAuthorEntityMergeDecisions take it exclusively, so no build starts
// between an insert's active-build check and its commit (§4.4).
const (
	entityBuildStartLockClass int32 = 0x65626c64
	entityBuildStartLockKey   int32 = 0
)

// lockEntityBuildStart takes the start lock for the transaction.
func lockEntityBuildStart(ctx context.Context, tx pg.DBI) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(?, ?)`, entityBuildStartLockClass,
		entityBuildStartLockKey); err != nil {
		return fmt.Errorf("taking the entity build start lock: %w", err)
	}
	return nil
}

// inTx runs fn in db's transaction, or in a new one when db is a pool.
func inTx(ctx context.Context, db pg.DBI, fn func(tx pg.DBI) error) error {
	if pool, ok := db.(*pg.DB); ok {
		return pool.RunInTransaction(ctx, func(tx *pg.Tx) error { return fn(tx) })
	}
	return fn(db)
}

// MergeVerdict is a same verdict to insert as a decision: the digest of the
// item it was asked on (frozen when asked), where it was asked and why it
// is trusted.
type MergeVerdict struct {
	Digest        [32]byte
	ConfigVersion string
	Kind          string
	ReportID      int64
	VerdictIDs    []int64
}

// MergeInsertReport counts what an insert did with its verdicts.
type MergeInsertReport struct {
	Inserted int `json:"inserted"`
	// LapsedBeforeApply: the stored census has no candidate of the digest
	// (its card or members changed since the verdict was asked).
	LapsedBeforeApply int `json:"lapsed_before_apply"`
	// AlreadyDecided: a live decision of the digest exists.
	AlreadyDecided int `json:"already_decided"`
	// Withheld: the kind is not registered, withdrawn, or the script blocked.
	Withheld map[string]int `json:"withheld,omitempty"`
	// SideUnrecorded: a side of the candidate is credited on no current book.
	SideUnrecorded int   `json:"side_unrecorded,omitempty"`
	BuildRunID     int64 `json:"build_run_id"`
	// Refused counts the verdicts a refused transaction and those after it
	// did not get to; RefusedReason says why (a closed code). The chunks
	// before the refusal are committed and counted above.
	Refused       int    `json:"refused,omitempty"`
	RefusedReason string `json:"refused_reason,omitempty"`
}

// The closed codes of MergeInsertReport.RefusedReason.
const (
	MergeRefusedBuildActive      = "build_active"
	MergeRefusedVetoUnanchored   = "veto_unanchored"
	MergeRefusedNoCompletedBuild = "no_completed_build"
	MergeRefusedDriftHeld        = "drift_held"
	MergeRefusedError            = "error"
)

// mergeRefusedReason is the closed code of an insert's refusal.
func mergeRefusedReason(err error) string {
	switch {
	case errors.Is(err, ErrAuthorEntityBuildActive):
		return MergeRefusedBuildActive
	case errors.Is(err, ErrAuthorEntityVetoUnanchored):
		return MergeRefusedVetoUnanchored
	case errors.Is(err, ErrAuthorEntityNoCompletedBuild):
		return MergeRefusedNoCompletedBuild
	case errors.Is(err, ErrMergeApplyDriftHeld):
		return MergeRefusedDriftHeld
	}
	return MergeRefusedError
}

// MergeInsertOptions are an insert's actor and, for an apply from an eval,
// the configuration and run whose drift holds it: when DriftConfig is set,
// every transaction refuses (ErrMergeApplyDriftHeld) once
// MergeApplyDriftHeld holds for them — a drift that came after the apply's
// own check, or between two chunks, stops the chunks from there on.
//
// Settle, when set, runs at the end of each chunk's transaction with what
// became of every verdict of the chunk, by digest (MergeInsertOutcome*): a
// merge run settles its verdicts there, so a committed chunk never leaves
// them unsettled and a refused one settles nothing.
type MergeInsertOptions struct {
	ActorID     int64
	DriftConfig string
	DriftRunID  int64
	Settle      func(ctx context.Context, tx *pg.Tx, outcomes map[[32]byte]string) error
}

// What an insert did with one verdict, as MergeInsertOptions.Settle gets
// it; the withheld ones carry their MergeWithheld* reason.
const (
	MergeInsertOutcomeInserted          = "inserted"
	MergeInsertOutcomeLapsedBeforeApply = "lapsed_before_apply"
	MergeInsertOutcomeAlreadyDecided    = "already_decided"
	MergeInsertOutcomeSideUnrecorded    = "side_unrecorded"
)

func (r *MergeInsertReport) add(o *MergeInsertReport) {
	r.Inserted += o.Inserted
	r.LapsedBeforeApply += o.LapsedBeforeApply
	r.AlreadyDecided += o.AlreadyDecided
	r.SideUnrecorded += o.SideUnrecorded
	r.BuildRunID = o.BuildRunID
	for k, n := range o.Withheld {
		if r.Withheld == nil {
			r.Withheld = map[string]int{}
		}
		r.Withheld[k] += n
	}
}

// mergeInsertChunk bounds one transaction of an insert, so the start lock
// is never held for long.
const mergeInsertChunk = 500

// TestHookMergeInsertChecked runs inside an insert's transaction after its
// active-build and veto checks and before its drift check, holding the
// start lock (a test seam).
var TestHookMergeInsertChecked func()

// InsertAuthorEntityMergeDecisions is the compare-and-insert of §4.4: in
// transactions of at most mergeInsertChunk verdicts, each under the build
// start lock, with no build pending or running (ErrAuthorEntityBuildActive)
// and no live veto unanchored (ErrAuthorEntityVetoUnanchored), it inserts a
// same decision for each verdict whose digest is a candidate of the stored
// census — the one the last completed build that wrote its census left;
// with no build active it changes only in a build's completion — with that
// candidate's members and books, under the live
// registration of its kind when its script is not blocked. Every other
// verdict is counted, not inserted. A refusal ends the insert; the chunks
// before it stay, and the report counts the verdicts it did not get to.
func InsertAuthorEntityMergeDecisions(
	ctx context.Context, db *pg.DB, verdicts []MergeVerdict, opts *MergeInsertOptions,
) (*MergeInsertReport, error) {
	if opts == nil {
		opts = &MergeInsertOptions{}
	}
	total := &MergeInsertReport{}
	for start := 0; start < len(verdicts); start += mergeInsertChunk {
		chunk := verdicts[start:min(start+mergeInsertChunk, len(verdicts))]
		var rep *MergeInsertReport
		err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
			var err error
			rep, err = insertMergeDecisionsTx(ctx, tx, chunk, opts)
			return err
		})
		if err != nil {
			total.Refused, total.RefusedReason = len(verdicts)-start, mergeRefusedReason(err)
			return total, err
		}
		total.add(rep)
	}
	return total, nil
}

// insertMergeDecisionsTx is one chunk's transaction.
func insertMergeDecisionsTx(
	ctx context.Context, tx *pg.Tx, verdicts []MergeVerdict, opts *MergeInsertOptions,
) (*MergeInsertReport, error) {
	latest, err := mayInsertMergeDecisions(ctx, tx, opts)
	if err != nil {
		return nil, err
	}
	actor := opts.ActorID
	rep := &MergeInsertReport{BuildRunID: latest}
	skip := map[string]bool{}
	var inserted []insertedDecision
	for {
		if _, err = tx.ExecContext(ctx, `SAVEPOINT merge_insert`); err != nil {
			return nil, err
		}
		inserted, err = insertCurrentDecisions(ctx, tx, verdicts, skip, actor)
		if err != nil {
			return nil, err
		}
		ids := make([]int64, len(inserted))
		for i := range inserted {
			ids[i] = inserted[i].ID
		}
		unrecorded, err := recordMergeDecisionBooks(ctx, tx, ids)
		if err != nil {
			return nil, err
		}
		if len(unrecorded) == 0 {
			rep.Inserted = len(inserted)
			for i := range inserted {
				skip[string(inserted[i].Digest)] = true
			}
			break
		}
		// A side on no book refuses its decision: insert the others again
		// without it.
		if _, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT merge_insert`); err != nil {
			return nil, err
		}
		for _, d := range unrecorded {
			skip[string(d)] = true
		}
	}
	rep.SideUnrecorded = len(skip) - rep.Inserted
	outcomes := make(map[[32]byte]string, len(verdicts))
	for d := range skip {
		outcomes[[32]byte([]byte(d))] = MergeInsertOutcomeSideUnrecorded
	}
	for i := range inserted {
		outcomes[[32]byte(inserted[i].Digest)] = MergeInsertOutcomeInserted
	}
	if err := countNotInserted(ctx, tx, verdicts, skip, rep, outcomes); err != nil {
		return nil, err
	}
	if opts.Settle != nil {
		if err := opts.Settle(ctx, tx, outcomes); err != nil {
			return nil, err
		}
	}
	return rep, nil
}

// mayInsertMergeDecisions takes the build start lock for tx and checks
// that decisions may be inserted: no build pending or running, no live veto
// unanchored, a build completed, and — for an apply from an eval — no
// participant drifted, read in this transaction. It returns the latest
// completed build.
func mayInsertMergeDecisions(ctx context.Context, tx *pg.Tx, opts *MergeInsertOptions) (int64, error) {
	if err := lockEntityBuildStart(ctx, tx); err != nil {
		return 0, err
	}
	var active bool
	var latest *int64
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&active, &latest), `
		SELECT EXISTS (SELECT 1 FROM public.author_entity_build_run WHERE status IN ('pending', 'running')),
			(SELECT max(id) FROM public.author_entity_build_run WHERE status = 'completed')`); err != nil {
		return 0, fmt.Errorf("reading the entity builds: %w", err)
	}
	if active {
		return 0, ErrAuthorEntityBuildActive
	}
	unanchored, err := AuthorEntityVetoUnanchored(ctx, tx)
	if err != nil {
		return 0, err
	}
	if unanchored {
		return 0, ErrAuthorEntityVetoUnanchored
	}
	if latest == nil {
		return 0, ErrAuthorEntityNoCompletedBuild
	}
	if TestHookMergeInsertChecked != nil {
		TestHookMergeInsertChecked()
	}
	if opts.DriftConfig != "" {
		held, err := MergeApplyDriftHeld(ctx, tx, opts.DriftConfig, opts.DriftRunID)
		if err != nil {
			return 0, err
		}
		if held {
			return 0, ErrMergeApplyDriftHeld
		}
	}
	return *latest, nil
}

// insertedDecision is a decision an insert made.
type insertedDecision struct {
	ID     int64  `pg:"id"`
	Digest []byte `pg:"digest"`
}

// insertCurrentDecisions inserts, in one statement each, the decisions of
// the verdicts whose digest is a stored candidate under a live
// registration of the verdict's configuration and kind with the script not
// blocked, and no live decision of the digest yet; then their members. It
// returns the new decisions.
func insertCurrentDecisions(
	ctx context.Context, tx pg.DBI, verdicts []MergeVerdict, skip map[string]bool, actor int64,
) ([]insertedDecision, error) {
	var digests, configs, kinds []string
	var reports []int64
	var verdictIDs []string
	for i := range verdicts {
		v := &verdicts[i]
		if skip[string(v.Digest[:])] {
			continue
		}
		digests = append(digests, hex.EncodeToString(v.Digest[:]))
		configs, kinds, reports = append(configs, v.ConfigVersion), append(kinds, v.Kind), append(reports, v.ReportID)
		verdictIDs = append(verdictIDs, bigintArray(v.VerdictIDs))
	}
	if len(digests) == 0 {
		return nil, nil
	}
	var inserted []insertedDecision
	if _, err := tx.QueryContext(ctx, &inserted, `
		INSERT INTO public.author_entity_merge_decision (digest, config_version, kind, script, item_kind, credit_id,
			credit_ordinal, members_sha256, input_sha256, outcome, source, verdict_ids, eval_report_id, registration_id,
			created_by_user_id)
		SELECT c.digest, u.config, c.kind, c.script, c.item_kind, c.credit_id, c.credit_ordinal, c.members_sha256,
			c.input_sha256, 'same', 'model', u.verdicts::bigint[], nullif(u.report, 0), r.id, nullif(?, 0)
		FROM unnest(?::text[], ?::text[], ?::text[], ?::bigint[], ?::text[]) AS u(digest, config, kind, report, verdicts)
		JOIN public.author_entity_merge_candidate c ON c.digest = decode(u.digest, 'hex') AND c.kind = u.kind
		JOIN public.author_entity_merge_registration r ON r.config_version = u.config AND r.kind = c.kind
			AND r.withdrawn_at IS NULL
		WHERE NOT EXISTS (SELECT 1 FROM public.author_entity_merge_script_block b
				WHERE b.registration_id = r.id AND b.script = c.script)
			AND NOT EXISTS (SELECT 1 FROM public.author_entity_merge_decision d
				WHERE d.digest = c.digest AND d.revoked_at IS NULL)
		ORDER BY c.digest
		RETURNING id, digest`, actor, pg.Array(digests), pg.Array(configs), pg.Array(kinds), pg.Array(reports),
		pg.Array(verdictIDs)); err != nil {
		return nil, fmt.Errorf("inserting the merge decisions: %w", err)
	}
	if len(inserted) == 0 {
		return nil, nil
	}
	ids := make([]int64, len(inserted))
	for i := range inserted {
		ids[i] = inserted[i].ID
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO public.author_entity_merge_decision_member (decision_id, side, source_fingerprint,
			extractor_version, ordinal)
		SELECT d.id, m.side, m.source_fingerprint, m.extractor_version, m.ordinal
		FROM public.author_entity_merge_decision d
		JOIN public.author_entity_merge_candidate_member m ON m.digest = d.digest
		WHERE d.id = ANY(?::bigint[])`, pg.Array(ids)); err != nil {
		return nil, fmt.Errorf("inserting the merge decision members: %w", err)
	}
	return inserted, nil
}

// countNotInserted counts why each verdict not inserted (not in skip, which
// holds the inserted and the side-unrecorded ones) was not: no candidate in
// the stored census, a live decision already, or the registration of its
// kind (withheld by reason); outcomes gets each one's class by digest.
func countNotInserted(
	ctx context.Context, tx pg.DBI, verdicts []MergeVerdict, skip map[string]bool, rep *MergeInsertReport,
	outcomes map[[32]byte]string,
) error {
	var digests, configs, kinds []string
	for i := range verdicts {
		v := &verdicts[i]
		if skip[string(v.Digest[:])] {
			continue
		}
		digests = append(digests, hex.EncodeToString(v.Digest[:]))
		configs, kinds = append(configs, v.ConfigVersion), append(kinds, v.Kind)
	}
	if len(digests) == 0 {
		return nil
	}
	var rows []struct {
		Digest string `pg:"digest"`
		Class  string `pg:"class"`
	}
	if _, err := tx.QueryContext(ctx, &rows, `
		SELECT u.digest, CASE
				WHEN c.digest IS NULL THEN ?
				WHEN d.id IS NOT NULL THEN ?
				WHEN r.id IS NULL THEN ?
				WHEN r.withdrawn_at IS NOT NULL THEN ?
				ELSE ? END AS class
		FROM unnest(?::text[], ?::text[], ?::text[]) AS u(digest, config, kind)
		LEFT JOIN public.author_entity_merge_candidate c ON c.digest = decode(u.digest, 'hex') AND c.kind = u.kind
		LEFT JOIN public.author_entity_merge_decision d ON d.digest = c.digest AND d.revoked_at IS NULL
		LEFT JOIN public.author_entity_merge_registration r ON r.config_version = u.config AND r.kind = u.kind`,
		MergeInsertOutcomeLapsedBeforeApply, MergeInsertOutcomeAlreadyDecided,
		MergeWithheldUnregistered, MergeWithheldWithdrawn, MergeWithheldScriptBlocked,
		pg.Array(digests), pg.Array(configs), pg.Array(kinds)); err != nil {
		return fmt.Errorf("counting the verdicts not inserted: %w", err)
	}
	for _, r := range rows {
		if raw, err := hex.DecodeString(r.Digest); err == nil && len(raw) == 32 {
			outcomes[[32]byte(raw)] = r.Class
		}
		switch r.Class {
		case MergeInsertOutcomeLapsedBeforeApply:
			rep.LapsedBeforeApply++
		case MergeInsertOutcomeAlreadyDecided:
			rep.AlreadyDecided++
		default:
			if rep.Withheld == nil {
				rep.Withheld = map[string]int{}
			}
			rep.Withheld[r.Class]++
		}
	}
	return nil
}

// MergeApplyItem is a census or holdout item of a merge eval run: the
// candidate it was asked on (its digest, frozen when asked) and its
// stratum (kind.script).
type MergeApplyItem struct {
	ID          int64  `pg:"id"`
	Stratum     string `pg:"stratum"`
	Fingerprint []byte `pg:"source_fingerprint"`
}

// MergeApplyItems are the items of a merge eval run asked on candidates of
// a kind: the census or holdout and the extra Latin questions of the
// holdout (item set holdout), never the suite or the anchors.
func MergeApplyItems(ctx context.Context, db pg.DBI, runID int64, kind string) ([]MergeApplyItem, error) {
	var rows []MergeApplyItem
	if _, err := db.QueryContext(ctx, &rows, `
		SELECT i.id, i.stratum, i.source_fingerprint FROM public.author_llm_eval_item i
		WHERE i.item_set = 'holdout' AND i.extractor_version = ? AND split_part(i.stratum, '.', 1) = ?
			AND EXISTS (SELECT 1 FROM public.author_llm_job j WHERE j.eval_item_id = i.id AND j.run_id = ?)
		ORDER BY i.id`, authorentity.MergeCandidateVersion, kind, runID); err != nil {
		return nil, fmt.Errorf("reading a merge eval run's items: %w", err)
	}
	return rows, nil
}

// MergeApplyDriftHeld reports a participant of the merge configuration
// paused for a fingerprint drift (or a re-learn that did not settle), or a
// re-learn of the run still going: the model behind the answers may have
// changed, so nothing of them is applied until an administrator resumes.
func MergeApplyDriftHeld(ctx context.Context, db pg.DBI, configVersion string, runID int64) (bool, error) {
	var held bool
	if _, err := db.QueryOneContext(ctx, pg.Scan(&held), `
		SELECT EXISTS (SELECT 1 FROM public.author_llm_provider_state
				WHERE config_version = ? AND paused_reason IN (?))
			OR EXISTS (SELECT 1 FROM public.author_llm_relearn WHERE run_id = ? AND status = 'relearning')`,
		configVersion, pg.In([]string{string(models.AuthorLLMPausedDrift), string(models.AuthorLLMPausedRelearnUnstable)}),
		runID); err != nil {
		return false, fmt.Errorf("reading the merge configuration's drift: %w", err)
	}
	return held, nil
}
