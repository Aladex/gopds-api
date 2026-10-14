package database

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"gopds-api/internal/authornorm"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The resolve run's storage (design K, phase 6, migration 34): its start
// with the registration of the pairs the eval report passed, its review
// jobs, the application of its confirmed verdicts to the credits, the LLM
// outcome the local stream reuses for a credit that joins later, and its
// report.

// ErrAuthorLLMNoEvidence marks a resolve start without a completed full eval
// of the configuration whose report registers a stratum.
var ErrAuthorLLMNoEvidence = errors.New("database: no eval report registers a stratum of this configuration")

// ErrAuthorLLMEvidenceChanged refuses a resolve start whose plan was built
// from an eval report that is no longer the configuration's latest: a
// recomputation published another version in between. Nothing is written;
// a new start plans from the latest version.
var ErrAuthorLLMEvidenceChanged = errors.New("database: the eval report changed while the resolve start was planned")

// TestHookResolveStartLocked is called, when set, inside a resolve start's
// transaction right after it took the claim lock: a test seam to race a
// recomputation against it.
var TestHookResolveStartLocked func()

// AuthorLLMResolveStratum is how a resolve run asks and accepts one stratum,
// as its eval report decided: the arm and the items per request the pair is
// asked with, the tiers an agreed answer may have, whether arm A passed at
// that batch size too and on which tiers (an answer given without the
// excerpt is arm A's and must pass arm A's own gate), and the registered pair
// (the local class and the script).
type AuthorLLMResolveStratum struct {
	Class           string   `json:"class"`
	Script          string   `json:"script"`
	Arm             string   `json:"arm"`
	ItemsPerRequest int      `json:"items_per_request"`
	Tiers           []string `json:"tiers"`
	ArmAPassed      bool     `json:"arm_a_passed"`
	ArmATiers       []string `json:"arm_a_tiers"`
}

// AuthorLLMResolveSettings are a resolve run's settings: the eval report it
// rests on, the plan of every registered stratum, and the passed strata no
// pair can be registered for (no script of their own).
type AuthorLLMResolveSettings struct {
	ReportID       int64                              `json:"report_id"`
	ReportSHA256   string                             `json:"report_sha256"`
	Strata         map[string]AuthorLLMResolveStratum `json:"strata"`
	NotRegistrable []string                           `json:"not_registrable"`
}

// AuthorLLMEvalEvidence is the latest completed full eval of a
// configuration: its report and what it learned of the participants.
type AuthorLLMEvalEvidence struct {
	RunID          int64           `pg:"run_id"`
	ReportID       int64           `pg:"report_id"`
	ReportSHA256   []byte          `pg:"report_sha256"`
	Report         json.RawMessage `pg:"report"`
	ExpectedModels json.RawMessage `pg:"expected_models"`
	Reference      json.RawMessage `pg:"reference"`
}

// LatestAuthorLLMFullEval reads the latest completed full eval run of the
// configuration with its latest report version; nil when there is none.
func LatestAuthorLLMFullEval(ctx context.Context, db pg.DBI, version string) (*AuthorLLMEvalEvidence, error) {
	var ev AuthorLLMEvalEvidence
	_, err := db.QueryOneContext(ctx, &ev, `
		SELECT r.id AS run_id, e.id AS report_id, e.report_sha256, e.report::text AS report, r.expected_models,
			r.reference
		FROM author_llm_run r
		JOIN LATERAL (SELECT id, report_sha256, report FROM author_llm_eval_report
			WHERE run_id = r.id ORDER BY version DESC LIMIT 1) e ON true
		WHERE r.config_version = ?0 AND r.kind = 'eval' AND r.mode = 'full' AND r.status = 'completed'
		ORDER BY r.finished_at DESC, r.id DESC
		LIMIT 1`, version)
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the latest full eval: %w", err)
	}
	if relearnErr := ev.withRelearns(ctx, db); relearnErr != nil {
		return nil, relearnErr
	}
	return &ev, nil
}

// withRelearns makes the evidence the eval's effective one: a participant
// re-learned during the eval was measured — its answers re-asked — under its
// latest learned reference and model ids, which replace the fingerprint
// stage's. The originals stay in the run and its re-learn history.
func (ev *AuthorLLMEvalEvidence) withRelearns(ctx context.Context, db pg.DBI) error {
	relearns, err := AuthorLLMRelearns(ctx, db, ev.RunID)
	if err != nil || len(relearns) == 0 {
		return err
	}
	reference := map[string]AuthorLLMFingerprintReference{}
	expected := map[string][]string{}
	if len(ev.Reference) > 0 && string(ev.Reference) != jsonNull {
		if err = json.Unmarshal(ev.Reference, &reference); err != nil {
			return fmt.Errorf("reading an eval's reference: %w", err)
		}
	}
	if len(ev.ExpectedModels) > 0 && string(ev.ExpectedModels) != jsonNull {
		if err = json.Unmarshal(ev.ExpectedModels, &expected); err != nil {
			return fmt.Errorf("reading an eval's model ids: %w", err)
		}
	}
	for i := range relearns {
		if r := &relearns[i]; r.Status == authorLLMRelearnLearned && r.NewReference != nil {
			reference[r.Participant] = *r.NewReference
			expected[r.Participant] = r.Models
		}
	}
	if ev.Reference, err = json.Marshal(reference); err != nil {
		return err
	}
	ev.ExpectedModels, err = json.Marshal(expected)
	return err
}

// AuthorLLMResolveStart is a resolve run to start: its mode, the evidence
// and the plan.
type AuthorLLMResolveStart struct {
	ConfigVersion string
	Mode          string
	Evidence      *AuthorLLMEvalEvidence
	Settings      AuthorLLMResolveSettings
	CreatedBy     int64
}

// llmEvalSource is the acceptance source of a pair the service registers
// from an eval report (migration 29).
const llmEvalSource = "llm_eval"

// StartAuthorLLMResolveRun creates a running resolve run and, in the same
// transaction (design K, decision R1, option «в»), registers the pair of
// every planned stratum the configuration has not registered yet: source
// llm_eval, the admin who started it, and the report by id and digest, all
// under one new policy version. A run already active refuses with
// ErrAuthorLLMActiveRun and registers nothing; so does, with
// ErrAuthorLLMEvidenceChanged, evidence that is no longer the
// configuration's latest report once the claim lock is held.
func StartAuthorLLMResolveRun(ctx context.Context, db *pg.DB, s *AuthorLLMResolveStart) (id int64, registered int, err error) {
	if s.Evidence == nil || len(s.Settings.Strata) == 0 {
		return 0, 0, ErrAuthorLLMNoEvidence
	}
	settings, err := json.Marshal(s.Settings)
	if err != nil {
		return 0, 0, err
	}
	err = RunAuthorLLMTx(ctx, db, func(tx *pg.Tx) error {
		registered = 0
		if _, lockErr := lockAuthorLLM(ctx, tx, nil); lockErr != nil {
			return lockErr
		}
		if TestHookResolveStartLocked != nil {
			TestHookResolveStartLocked()
		}
		// The plan was built from evidence read before the lock; a
		// recomputation publishes under the same lock, so the latest
		// report read now is the one the registrations may rest on.
		latest, readErr := LatestAuthorLLMFullEval(ctx, tx, s.ConfigVersion)
		if readErr != nil {
			return readErr
		}
		if latest == nil || latest.ReportID != s.Evidence.ReportID ||
			!bytes.Equal(latest.ReportSHA256, s.Evidence.ReportSHA256) {
			return ErrAuthorLLMEvidenceChanged
		}
		if _, liftErr := liftRunBoundPauses(ctx, tx, s.ConfigVersion); liftErr != nil {
			return liftErr
		}
		if _, insertErr := tx.QueryOneContext(ctx, pg.Scan(&id), `
			INSERT INTO author_llm_run (kind, mode, status, config_version, stage, settings, expected_models,
				reference, created_by_user_id, started_at)
			VALUES ('resolve', ?0, 'running', ?1, 'enqueue', ?2::jsonb, ?3::jsonb, ?4::jsonb, ?5, now())
			RETURNING id`, s.Mode, s.ConfigVersion, string(settings), string(s.Evidence.ExpectedModels),
			string(s.Evidence.Reference), s.CreatedBy); insertErr != nil {
			return insertErr
		}
		// The acceptance table is small and written only here and by
		// migrations; the lock keeps the new policy version one per start.
		if _, lockErr := tx.ExecContext(ctx,
			`LOCK TABLE author_acceptance_class IN SHARE ROW EXCLUSIVE MODE`); lockErr != nil {
			return fmt.Errorf("locking the acceptance classes: %w", lockErr)
		}
		var policy int
		if _, readErr := tx.QueryOneContext(ctx, pg.Scan(&policy), `
			SELECT greatest(coalesce(max(policy_version::int), 0), ?0) + 1 FROM author_acceptance_class`,
			authornorm.BasePolicyVersion); readErr != nil {
			return fmt.Errorf("reading the policy version: %w", readErr)
		}
		for _, st := range s.Settings.Strata {
			res, regErr := tx.ExecContext(ctx, `
				INSERT INTO author_acceptance_class (policy_version, decision_class, script, config_version,
					evidence_report_sha256, evidence_report_id, evidence_ref, source, registered_by_user_id)
				VALUES (?0, ?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8)
				ON CONFLICT (config_version, decision_class, script) DO NOTHING`,
				strconv.Itoa(policy), string(authornorm.LLMClass(authornorm.DecisionClass(st.Class))), st.Script,
				s.ConfigVersion, s.Evidence.ReportSHA256, s.Evidence.ReportID,
				"author_llm_eval_report/"+strconv.FormatInt(s.Evidence.ReportID, 10)+".json", llmEvalSource,
				s.CreatedBy)
			if regErr != nil {
				return fmt.Errorf("registering an LLM pair: %w", regErr)
			}
			registered += res.RowsAffected()
		}
		return nil
	})
	if err != nil {
		switch pgErrorCode(err) {
		case "23505":
			return 0, 0, ErrAuthorLLMActiveRun
		case "23514":
			return 0, 0, ErrAuthorLLMRunEvidence
		}
		return 0, 0, fmt.Errorf("starting a resolve run: %w", err)
	}
	return id, registered, nil
}

// AuthorLLMResolveRun is the running resolve run of a configuration as its
// driver reads it.
type AuthorLLMResolveRun struct {
	ID        int64           `pg:"id"`
	Mode      string          `pg:"mode"`
	Stage     *string         `pg:"stage"`
	Settings  json.RawMessage `pg:"settings"`
	Reference json.RawMessage `pg:"reference"`
}

// RunningAuthorLLMResolveRun reads the configuration's running resolve run;
// nil when there is none.
func RunningAuthorLLMResolveRun(ctx context.Context, db pg.DBI, version string) (*AuthorLLMResolveRun, error) {
	var run AuthorLLMResolveRun
	_, err := db.QueryOneContext(ctx, &run, `
		SELECT id, mode, stage, settings, reference FROM author_llm_run
		WHERE kind = 'resolve' AND status = 'running' AND config_version = ?`, version)
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the running resolve run: %w", err)
	}
	return &run, nil
}

// EnqueueAuthorLLMReviewJobs inserts the review jobs of a running resolve
// run in one statement per call; a job whose input already has one under
// the configuration is kept (the pilot's inputs are never asked again by the
// full run). A run that is not running at that moment gets nothing. It
// returns how many were new.
func EnqueueAuthorLLMReviewJobs(ctx context.Context, db *pg.DB, runID int64, jobs []AuthorLLMNewJob) (int, error) {
	if len(jobs) == 0 {
		return 0, nil
	}
	type row struct {
		Slot       string          `json:"slot"`
		FP         string          `json:"fp"`
		Extractor  string          `json:"extractor"`
		Arm        string          `json:"arm"`
		Batch      int             `json:"batch"`
		Mode       string          `json:"mode"`
		Input      json.RawMessage `json:"input"`
		Context    bool            `json:"context"`
		ConfigVers string          `json:"config"`
	}
	rows := make([]row, 0, len(jobs))
	for i := range jobs {
		j := &jobs[i]
		if !j.Slot.Valid() || j.BatchSize < 1 || len(j.SourceFingerprint) != 32 || j.ExtractorVersion == "" {
			return 0, ErrAuthorLLMInvalidJob
		}
		input, err := json.Marshal(j.Input)
		if err != nil {
			return 0, err
		}
		mode := j.OutputMode
		if mode == "" {
			mode = AuthorLLMDefaultOutputMode
		}
		rows = append(rows, row{Slot: string(j.Slot), FP: hex.EncodeToString(j.SourceFingerprint),
			Extractor: j.ExtractorVersion, Arm: j.Arm, Batch: j.BatchSize, Mode: mode, Input: input,
			Context: j.WithContext, ConfigVers: j.ConfigVersion})
	}
	payload, err := json.Marshal(rows)
	if err != nil {
		return 0, err
	}
	inserted := 0
	err = RunAuthorLLMTx(ctx, db, func(tx *pg.Tx) error {
		running, lockErr := LockAuthorLLMRunRunning(ctx, tx, runID)
		if lockErr != nil || !running {
			return lockErr
		}
		res, insertErr := tx.ExecContext(ctx, `
			INSERT INTO author_llm_job (config_version, slot, run_id, purpose, source_fingerprint, extractor_version,
				arm, batch_size, output_mode, input, with_context)
			SELECT r.config, r.slot, ?0, 'review', decode(r.fp, 'hex'), r.extractor, nullif(r.arm, ''), r.batch,
				r.mode, r.input, r.context
			FROM jsonb_to_recordset(?1::jsonb) AS r(slot text, fp text, extractor text, arm text, batch int,
				mode text, input jsonb, context boolean, config text)
			ON CONFLICT DO NOTHING`, runID, string(payload))
		if insertErr != nil {
			return fmt.Errorf("enqueuing review jobs: %w", insertErr)
		}
		inserted = res.RowsAffected()
		// An input already asked under the configuration whose verdict
		// failed transiently, and that has no current verdict since, is
		// asked again whole: its jobs move to this run and start over.
		res, insertErr = tx.ExecContext(ctx, `
			WITH inputs AS (
				SELECT DISTINCT decode(r.fp, 'hex') AS fp, r.extractor, r.config
				FROM jsonb_to_recordset(?1::jsonb) AS r(fp text, extractor text, config text)
			), again AS (
				SELECT i.* FROM inputs i
				WHERE EXISTS (SELECT 1 FROM author_llm_verdict v
						WHERE v.source_fingerprint = i.fp AND v.extractor_version = i.extractor
							AND v.purpose = 'review' AND v.config_version = i.config AND v.settled_at IS NOT NULL
							AND v.outcome_reason IN (?2))
					AND NOT EXISTS (SELECT 1 FROM author_llm_verdict v
						WHERE v.source_fingerprint = i.fp AND v.extractor_version = i.extractor
							AND v.purpose = 'review' AND v.config_version = i.config AND v.verdict <> 'held_drift'
							AND (v.outcome_reason IS NULL OR v.outcome_reason NOT IN (?2)))
			)
			UPDATE author_llm_job j
			SET run_id = ?0, status = 'pending', answer_attempt_id = NULL, finished_at = NULL, call_id = NULL,
				failure_count = 0, invalid_count = 0, retry_single = false, transient_since = NULL,
				last_error_class = ?3, next_attempt_at = now()
			FROM again a
			WHERE j.source_fingerprint = a.fp AND j.extractor_version = a.extractor AND j.config_version = a.config
				AND j.purpose = 'review' AND j.eval_item_id IS NULL AND j.status IN ('answered', 'invalid', 'failed')`,
			runID, string(payload), pg.In(authorLLMTransientOutcomes()), authorLLMClassAskedAgain)
		if insertErr != nil {
			return fmt.Errorf("asking review inputs again: %w", insertErr)
		}
		inserted += res.RowsAffected()
		return nil
	})
	return inserted, err
}

// authorLLMClassAskedAgain marks a review job a later run asks again after
// its input's verdict failed transiently.
const authorLLMClassAskedAgain = "asked_again"

// authorLLMTransientOutcomes are the verdict outcomes a later run asks
// again.
func authorLLMTransientOutcomes() []string {
	return []string{string(models.UnresolvedLLMFailed), string(models.UnresolvedLLMJudgeFailed)}
}

// AuthorLLMTransientInput is an input whose current verdict of a
// configuration failed transiently but whose credits were left unresolved
// for it, as earlier resolve runs applied such a verdict (run 15):
// with the review item its proposal was closed from.
type AuthorLLMTransientInput struct {
	SourceFingerprint []byte `pg:"source_fingerprint"`
	ExtractorVersion  string `pg:"extractor_version"`
	ProposalResultID  int64  `pg:"proposal_result_id"`
	DecisionClass     string `pg:"decision_class"`
}

// AuthorLLMTransientInputs lists the configuration's inputs whose current
// verdict failed transiently and that have credits unresolved for that
// reason, no admin decision on them and no open review item.
func AuthorLLMTransientInputs(ctx context.Context, db pg.DBI, version string) ([]AuthorLLMTransientInput, error) {
	var rows []AuthorLLMTransientInput
	_, err := db.QueryContext(ctx, &rows, `
		SELECT DISTINCT ON (v.source_fingerprint, v.extractor_version) v.source_fingerprint, v.extractor_version,
			i.proposal_result_id, i.decision_class
		FROM author_llm_verdict v
		JOIN contributor_review_item i ON i.source_fingerprint = v.source_fingerprint
			AND i.reason = 'ambiguous_decision' AND i.resolution = 'automatic' AND i.decision_class IS NOT NULL
		WHERE v.config_version = ?0 AND v.purpose = 'review' AND v.settled_at IS NOT NULL AND v.outcome_reason IN (?1)
			AND NOT EXISTS (SELECT 1 FROM author_llm_verdict w
				WHERE w.source_fingerprint = v.source_fingerprint AND w.extractor_version = v.extractor_version
					AND w.purpose = v.purpose AND w.config_version = v.config_version AND w.verdict <> 'held_drift'
					AND (w.outcome_reason IS NULL OR w.outcome_reason NOT IN (?1)))
			AND NOT EXISTS (SELECT 1 FROM contributor_review_item o
				WHERE o.scope_fingerprint = v.source_fingerprint AND o.reason = 'ambiguous_decision' AND o.status = 'open')
			AND EXISTS (SELECT 1 FROM book_contributor_credit_selection s
				WHERE s.source_fingerprint = v.source_fingerprint AND s.state = 'unresolved'
					AND s.unresolved_reason = v.outcome_reason AND s.decided_by_user_id IS NULL)
		ORDER BY v.source_fingerprint, v.extractor_version, i.id DESC`, version, pg.In(authorLLMTransientOutcomes()))
	if err != nil {
		return nil, fmt.Errorf("listing the transiently failed LLM inputs: %w", err)
	}
	return rows, nil
}

// AuthorLLMResolveProgress is what a resolve run still waits for: jobs not
// answered yet, verdicts not confirmed yet (an agreed answer the judges have
// not judged, or answers no fingerprint check has covered yet), and
// confirmed verdicts not applied yet.
type AuthorLLMResolveProgress struct {
	OpenJobs    int `pg:"open_jobs"`
	Unconfirmed int `pg:"unconfirmed"`
	Unsettled   int `pg:"unsettled"`
}

// Done reports whether the run has nothing left to wait for.
func (p AuthorLLMResolveProgress) Done() bool {
	return p.OpenJobs == 0 && p.Unconfirmed == 0 && p.Unsettled == 0
}

// AuthorLLMResolveRunProgress counts what a resolve run still waits for.
func AuthorLLMResolveRunProgress(ctx context.Context, db pg.DBI, runID int64) (AuthorLLMResolveProgress, error) {
	var p AuthorLLMResolveProgress
	_, err := db.QueryOneContext(ctx, &p, `
		SELECT
			(SELECT count(*) FROM author_llm_job WHERE run_id = ?0 AND status IN ('pending', 'claimed')) AS open_jobs,
			(SELECT count(*) FROM author_llm_verdict
				WHERE run_id = ?0 AND verdict <> 'held_drift' AND confirmed_at IS NULL) AS unconfirmed,
			(SELECT count(*) FROM author_llm_verdict
				WHERE run_id = ?0 AND verdict <> 'held_drift' AND confirmed_at IS NOT NULL
					AND settled_at IS NULL) AS unsettled`, runID)
	if err != nil {
		return p, fmt.Errorf("reading a resolve run's progress: %w", err)
	}
	return p, nil
}

// AuthorLLMVerdictToApply is a confirmed verdict not applied yet, with what
// its application needs: the pair's agreed reply and input, the judges'
// judgement, and the input's review item (the local class and proposal).
type AuthorLLMVerdictToApply struct {
	ID                int64   `pg:"id"`
	SourceFingerprint []byte  `pg:"source_fingerprint"`
	ExtractorVersion  string  `pg:"extractor_version"`
	Verdict           string  `pg:"verdict"`
	Tier              *string `pg:"tier"`
	// The context state of each production job: the excerpt each
	// participant actually answered with.
	ContextStateA string          `pg:"context_state_a"`
	ContextStateB string          `pg:"context_state_b"`
	Judgement     *string         `pg:"judgement"`
	Reply         json.RawMessage `pg:"reply"`
	Input         json.RawMessage `pg:"input"`
	Arm           *string         `pg:"arm"`
	BatchSize     int             `pg:"batch_size"`
	// The local proposal of the input's latest ambiguous review item.
	DecisionClass    *string  `pg:"decision_class"`
	ProposalResultID *int64   `pg:"proposal_result_id"`
	ProposalScript   *string  `pg:"script"`
	QualityFlags     []string `pg:"quality_flags,array"`
}

// AuthorLLMVerdictsToApply lists up to limit confirmed, unapplied verdicts
// of a run, oldest first.
func AuthorLLMVerdictsToApply(ctx context.Context, db pg.DBI, runID int64, limit int) ([]AuthorLLMVerdictToApply, error) {
	var rows []AuthorLLMVerdictToApply
	_, err := db.QueryContext(ctx, &rows, `
		SELECT v.id, v.source_fingerprint, v.extractor_version, v.verdict, v.tier, j.context_state AS context_state_a,
			jb.context_state AS context_state_b, v.judgement, a.reply, j.input, j.arm, j.batch_size, i.decision_class,
			i.proposal_result_id, r.script, r.quality_flags
		FROM author_llm_verdict v
		JOIN author_llm_job j ON j.id = v.job_a_id
		JOIN author_llm_job jb ON jb.id = v.job_b_id
		LEFT JOIN author_llm_attempt a ON a.id = v.attempt_a_id
		LEFT JOIN LATERAL (
			SELECT ri.decision_class, ri.proposal_result_id FROM contributor_review_item ri
			WHERE ri.scope_fingerprint = v.source_fingerprint AND ri.reason = 'ambiguous_decision'
			ORDER BY ri.id DESC LIMIT 1) i ON true
		LEFT JOIN contributor_normalization_result r ON r.id = i.proposal_result_id
		WHERE v.run_id = ?0 AND v.verdict <> 'held_drift' AND v.confirmed_at IS NOT NULL AND v.settled_at IS NULL
		ORDER BY v.id
		LIMIT ?1`, runID, limit)
	if err != nil {
		return nil, fmt.Errorf("listing the verdicts to apply: %w", err)
	}
	return rows, nil
}

// AuthorLLMRegistration is the policy version under which the configuration
// registered an LLM class for a script; "" when it did not.
func AuthorLLMRegistration(ctx context.Context, db pg.DBI, version string, class authornorm.DecisionClass, script string) (string, error) {
	var policy string
	_, err := db.QueryOneContext(ctx, pg.Scan(&policy), `
		SELECT policy_version FROM author_acceptance_class
		WHERE config_version = ?0 AND decision_class = ?1 AND script = ?2 AND source = 'llm_eval'`,
		version, string(class), script)
	if errors.Is(err, pg.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading an LLM registration: %w", err)
	}
	return policy, nil
}

const insertLLMResultSQL = `INSERT INTO contributor_normalization_result
	(source_fingerprint, normalization_key, extractor_version, normalizer_version, result_schema_version,
	 method, kind, status, decision_class, given_name, additional_names, family_name, nickname, prefix,
	 suffix, display_name, sort_name, search_key, script, quality_flags)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (normalization_key) WHERE method = 'llm' DO NOTHING
RETURNING id`

// InsertLLMResult stores a valid LLM result and returns its ID; a key stored
// before returns that row: one immutable LLM result per input and
// configuration.
func InsertLLMResult(ctx context.Context, db pg.DBI, r *authornorm.Result) (int64, error) {
	if err := r.Validate(); err != nil {
		return 0, err
	}
	if r.Method != authornorm.MethodLLM {
		return 0, ErrNotLocalResult
	}
	var id int64
	_, err := db.QueryOneContext(ctx, pg.Scan(&id), insertLLMResultSQL, resultInsertParams(r)...)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pg.ErrNoRows) {
		return 0, fmt.Errorf("storing an LLM result: %w", err)
	}
	_, err = db.QueryOneContext(ctx, pg.Scan(&id), `SELECT id FROM contributor_normalization_result
		WHERE normalization_key = ? AND method = 'llm'`, r.NormalizationKey[:])
	if err != nil {
		return 0, fmt.Errorf("reading the stored LLM result: %w", err)
	}
	return id, nil
}

// AuthorLLMVerdictApplication is how a verdict was applied: the selected result and
// its policy version, or the reason it was not.
type AuthorLLMVerdictApplication struct {
	ResultID      *int64
	PolicyVersion *string
	Reason        *string
	Stratum       string
}

// SettleAuthorLLMVerdict records how a confirmed verdict was applied. A
// verdict settled before is left as it is; it reports whether this call
// settled it.
func SettleAuthorLLMVerdict(ctx context.Context, tx ResolutionTx, verdictID int64, s *AuthorLLMVerdictApplication) (bool, error) {
	var stratum interface{}
	if s.Stratum != "" {
		stratum = s.Stratum
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE author_llm_verdict SET settled_at = now(), result_id = ?1, policy_version = ?2, outcome_reason = ?3,
			stratum = ?4
		WHERE id = ?0 AND settled_at IS NULL AND confirmed_at IS NOT NULL AND verdict <> 'held_drift'`,
		verdictID, s.ResultID, s.PolicyVersion, s.Reason, stratum)
	if err != nil {
		return false, fmt.Errorf("settling an LLM verdict: %w", err)
	}
	return res.RowsAffected() == 1, nil
}

// CurrentAuthorCredits counts the input's author credits in current
// snapshots: zero once a rescan removed the input.
func CurrentAuthorCredits(ctx context.Context, tx ResolutionTx, fingerprint []byte, extractorVersion string) (int, error) {
	var n int
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&n), `SELECT count(*) FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id
		WHERE c.source_fingerprint = ?0 AND s.extractor_version = ?1 AND s.is_current AND c.role = 'author'`,
		fingerprint, extractorVersion); err != nil {
		return 0, fmt.Errorf("counting an input's credits: %w", err)
	}
	return n, nil
}

// CloseAutomaticReviewItems closes the input's open ambiguous review item as
// decided by the service: with the LLM result it selected, or none.
func CloseAutomaticReviewItems(ctx context.Context, tx ResolutionTx, fingerprint []byte, resultID *int64) (int, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE contributor_review_item SET status = 'closed', resolution = 'automatic', resolution_result_id = ?1,
			finished_at = now()
		WHERE scope_fingerprint = ?0 AND reason = 'ambiguous_decision' AND status = 'open'`, fingerprint, resultID)
	if err != nil {
		return 0, fmt.Errorf("closing a review item automatically: %w", err)
	}
	return res.RowsAffected(), nil
}

// TestHookAfterLLMOutcomeRead is called, when set, right after the resolver
// read an input's settled LLM outcome: a test seam to interleave a
// concurrent writer at that point. Production code never sets it.
var TestHookAfterLLMOutcomeRead func(fingerprint []byte, found bool)

// AuthorLLMOutcome is the LLM layer's settled decision on an input: the
// selected LLM result, or the reason it stayed unresolved.
type AuthorLLMOutcome struct {
	ResultID      *int64  `pg:"result_id"`
	DecisionClass *string `pg:"decision_class"`
	PolicyVersion *string `pg:"policy_version"`
	Reason        *string `pg:"outcome_reason"`
}

// SettledAuthorLLMOutcome is the latest applied LLM decision on an input of
// any configuration; nil when the LLM layer has not decided it (an
// application that failed, a gone input or a transient failure decides
// nothing).
func SettledAuthorLLMOutcome(ctx context.Context, db pg.DBI, fingerprint []byte, extractorVersion string) (*AuthorLLMOutcome, error) {
	var out AuthorLLMOutcome
	_, err := db.QueryOneContext(ctx, &out, `
		SELECT v.result_id, r.decision_class, v.policy_version, v.outcome_reason
		FROM author_llm_verdict v LEFT JOIN contributor_normalization_result r ON r.id = v.result_id
		WHERE v.source_fingerprint = ?0 AND v.extractor_version = ?1 AND v.purpose = 'review'
			AND v.settled_at IS NOT NULL AND v.verdict <> 'held_drift'
			AND (v.outcome_reason IS NULL OR v.outcome_reason NOT IN (?2))
		ORDER BY v.settled_at DESC, v.id DESC
		LIMIT 1`, fingerprint, extractorVersion, pg.In(models.AuthorLLMUndecidedOutcomes()))
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the LLM outcome of an input: %w", err)
	}
	return &out, nil
}

// AuthorLLMResolveCount is one stratum's and outcome's verdict count of a
// resolve run.
type AuthorLLMResolveCount struct {
	Stratum string  `pg:"stratum"`
	Reason  *string `pg:"outcome_reason"`
	Applied bool    `pg:"applied"`
	Count   int     `pg:"count"`
}

// AuthorLLMUnknownStratum names, in a resolve report, the verdicts settled
// without a stratum (an input whose stratum could not be read back).
const AuthorLLMUnknownStratum = "unknown"

// AuthorLLMResolveCounts counts a resolve run's settled verdicts by stratum
// and outcome.
func AuthorLLMResolveCounts(ctx context.Context, db pg.DBI, runID int64) ([]AuthorLLMResolveCount, error) {
	var rows []AuthorLLMResolveCount
	_, err := db.QueryContext(ctx, &rows, `
		SELECT coalesce(stratum, ?1) AS stratum, outcome_reason, result_id IS NOT NULL AS applied, count(*) AS count
		FROM author_llm_verdict
		WHERE run_id = ?0 AND settled_at IS NOT NULL AND verdict <> 'held_drift'
		GROUP BY 1, 2, 3 ORDER BY 1, 2`, runID, AuthorLLMUnknownStratum)
	if err != nil {
		return nil, fmt.Errorf("counting a resolve run's verdicts: %w", err)
	}
	return rows, nil
}

// StoreAuthorLLMResolveReport writes a resolve run's report and completes the
// run, in one transaction, if the run is running at that moment; a report
// already written is kept.
func StoreAuthorLLMResolveReport(ctx context.Context, db *pg.DB, runID int64, report []byte) error {
	return RunAuthorLLMTx(ctx, db, func(tx *pg.Tx) error {
		running, err := LockAuthorLLMRunRunning(ctx, tx, runID)
		if err != nil || !running {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO author_llm_resolve_report (run_id, report, report_sha256)
			VALUES (?0, ?1::jsonb, sha256(convert_to((?1::jsonb)::text, 'UTF8')))
			ON CONFLICT (run_id) DO NOTHING`, runID, string(report)); err != nil {
			return fmt.Errorf("storing a resolve report: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE author_llm_run SET status = 'completed', paused_reason = NULL, finished_at = now(), stage = 'done'
			WHERE id = ?0 AND status = 'running'`, runID); err != nil {
			return fmt.Errorf("completing a resolve run: %w", err)
		}
		return nil
	})
}

// AuthorLLMResolveReport is a stored resolve report.
type AuthorLLMResolveReport struct {
	Report       string    `pg:"report"`
	ReportSHA256 []byte    `pg:"report_sha256"`
	CreatedAt    time.Time `pg:"created_at"`
}

// GetAuthorLLMResolveReport reads a resolve run's report; pg.ErrNoRows when
// it has none.
func GetAuthorLLMResolveReport(ctx context.Context, db pg.DBI, runID int64) (*AuthorLLMResolveReport, error) {
	var r AuthorLLMResolveReport
	if _, err := db.QueryOneContext(ctx, &r, `
		SELECT report::text AS report, report_sha256, created_at FROM author_llm_resolve_report WHERE run_id = ?`,
		runID); err != nil {
		return nil, err
	}
	return &r, nil
}

// llmPrecedence enforces LLM > local for every local automatic writer: a
// local outcome (reconcile, the local stream, a failed local job) on an input
// the LLM layer has decided becomes that decision — its selected LLM result,
// or unresolved with its reason on the local result. The decision is read
// under the input's lock, the lock the resolve run holds while it applies a
// verdict, so it cannot be applied between this read and the writes that
// follow: an application either committed before the read (and is seen) or
// waits for this transaction (and overwrites it after).
func llmPrecedence(
	ctx context.Context, db ResolutionTx, fingerprint []byte, extractorVersion string, out *AutomaticOutcome,
) (*AutomaticOutcome, error) {
	if err := LockNormalizationInput(ctx, db, fingerprint, extractorVersion); err != nil {
		return nil, err
	}
	decided, err := SettledAuthorLLMOutcome(ctx, db, fingerprint, extractorVersion)
	if err != nil {
		return nil, err
	}
	if TestHookAfterLLMOutcomeRead != nil {
		TestHookAfterLLMOutcomeRead(fingerprint, decided != nil)
	}
	if decided == nil {
		return out, nil
	}
	if llm := decided.outcome(out); llm != nil {
		return llm, nil
	}
	return out, nil
}

// outcome is the resolver's outcome of the LLM decision for an input whose
// local outcome is local: the selected LLM result, or unresolved on the local
// result with the LLM's reason. nil when the decision cannot stand in (a
// failed local job has no result to leave unresolved on).
func (o *AuthorLLMOutcome) outcome(local *AutomaticOutcome) *AutomaticOutcome {
	if o.ResultID != nil {
		policy, err := strconv.Atoi(derefString(o.PolicyVersion))
		if err != nil || o.DecisionClass == nil {
			return nil
		}
		return &AutomaticOutcome{ResultID: *o.ResultID, DecisionClass: authornorm.DecisionClass(*o.DecisionClass),
			Decision: authornorm.Decision{Outcome: authornorm.OutcomeSelected, PolicyVersion: policy}, LLM: true}
	}
	if o.Reason == nil || local.Failed || local.ResultID <= 0 {
		return nil
	}
	return &AutomaticOutcome{ResultID: local.ResultID, DecisionClass: local.DecisionClass, LLM: true,
		Decision: authornorm.Decision{Outcome: authornorm.OutcomeUnresolved, Reason: authornorm.UnresolvedReason(*o.Reason)}}
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
