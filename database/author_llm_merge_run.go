package database

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gopds-api/internal/authorentity"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The merge run's storage (E4d-2, migration 69, plan §4.4, §4.6, §6): the
// candidates due to be asked, the start, the cards it freezes and the
// production pair's questions, and the verdicts it turns into decisions
// through InsertAuthorEntityMergeDecisions. K's acceptance policy is never
// read, written or locked here.

// MergeCandidateDue is a candidate of the stored census a merge run would
// ask: of a kind the configuration registered and did not withdraw, of a
// script not blocked in it, never asked under the configuration (no merge
// question of a run, no eval item of its evals) and with no live decision.
type MergeCandidateDue struct {
	Digest      []byte `pg:"digest"`
	Kind        string `pg:"kind"`
	Script      string `pg:"script"`
	InputSHA256 []byte `pg:"input_sha256"`
}

// mergeDueFrom is the due rule of §6 over the stored census, a candidate
// c, for the configuration and extractor version placeholders given: the
// candidates the last completed build that wrote its census left. The
// enqueue reads it again in its insert.
func mergeDueFrom(version, extractor string) string {
	return strings.NewReplacer("{version}", version, "{extractor}", extractor).Replace(`
	FROM public.author_entity_merge_candidate c
	JOIN public.author_entity_merge_registration r ON r.config_version = {version} AND r.kind = c.kind
		AND r.withdrawn_at IS NULL
	WHERE NOT EXISTS (SELECT 1 FROM public.author_entity_merge_script_block b
			WHERE b.registration_id = r.id AND b.script = c.script)
		AND NOT EXISTS (SELECT 1 FROM public.author_entity_merge_decision d
			WHERE d.digest = c.digest AND d.revoked_at IS NULL)
		AND NOT EXISTS (SELECT 1 FROM public.author_llm_job j
			WHERE j.source_fingerprint = c.digest AND j.extractor_version = {extractor} AND j.config_version = {version}
				AND j.purpose = 'merge_check' AND j.eval_item_id IS NULL)
		AND NOT EXISTS (SELECT 1 FROM public.author_llm_eval_item e
			JOIN public.author_llm_job j ON j.eval_item_id = e.id AND j.config_version = {version}
			WHERE e.source_fingerprint = c.digest AND e.extractor_version = {extractor})`)
}

// mergeCandidatesDueSQL reads the due rule.
var mergeCandidatesDueSQL = `SELECT c.digest, c.kind, c.script, c.input_sha256` + mergeDueFrom("?0", "?1") +
	` ORDER BY c.digest`

// MergeCandidatesDue are the candidates a merge run of the configuration
// would ask now, in digest order.
func MergeCandidatesDue(ctx context.Context, db pg.DBI, version string) ([]MergeCandidateDue, error) {
	var rows []MergeCandidateDue
	if _, err := db.QueryContext(ctx, &rows, mergeCandidatesDueSQL, version,
		authorentity.MergeCandidateVersion); err != nil {
		return nil, fmt.Errorf("reading the merge candidates due: %w", err)
	}
	return rows, nil
}

// LatestAuthorLLMMergeEval reads the latest completed merge eval of the
// configuration with its latest report version and its effective
// participants: the evidence a merge run rests on (the model ids V0
// accepts and the fingerprint references its checks compare with); nil
// when there is none.
func LatestAuthorLLMMergeEval(ctx context.Context, db pg.DBI, version string) (*AuthorLLMEvalEvidence, error) {
	return latestAuthorLLMEval(ctx, db, version, models.AuthorLLMEvalMergeModes()...)
}

// AuthorLLMMergeRunSettings are a merge run's settings: the eval its
// evidence comes from, and the live registrations when it started.
type AuthorLLMMergeRunSettings struct {
	EvidenceRunID    int64   `json:"evidence_run_id"`
	EvidenceReportID int64   `json:"evidence_report_id"`
	EvidenceSHA256   string  `json:"evidence_report_sha256"`
	Registrations    []int64 `json:"registrations"`
}

// AuthorLLMMergeRunStart is a merge run to start.
type AuthorLLMMergeRunStart struct {
	ConfigVersion string
	Mode          string
	Evidence      *AuthorLLMEvalEvidence
	Settings      AuthorLLMMergeRunSettings
	// EstimateTokens is the run's estimate, made before it starts: the
	// budget guard holds the run to it.
	EstimateTokens int64
	// CreatedBy is the administrator who started it; zero for none.
	CreatedBy int64
}

// StartAuthorLLMMergeRun creates a running merge run under the claim lock,
// like a resolve start, but registers nothing: a merge run asks only kinds
// registered already. A run already active refuses with
// ErrAuthorLLMActiveRun; evidence that is no longer the configuration's
// latest merge eval once the lock is held refuses with
// ErrAuthorLLMEvidenceChanged.
func StartAuthorLLMMergeRun(ctx context.Context, db *pg.DB, s *AuthorLLMMergeRunStart) (int64, error) {
	if s.Evidence == nil {
		return 0, ErrAuthorLLMNoEvidence
	}
	settings, err := json.Marshal(s.Settings)
	if err != nil {
		return 0, err
	}
	var by interface{}
	if s.CreatedBy > 0 {
		by = s.CreatedBy
	}
	var id int64
	err = RunAuthorLLMTx(ctx, db, func(tx *pg.Tx) error {
		if _, lockErr := lockAuthorLLM(ctx, tx, nil); lockErr != nil {
			return lockErr
		}
		latest, readErr := LatestAuthorLLMMergeEval(ctx, tx, s.ConfigVersion)
		if readErr != nil {
			return readErr
		}
		if latest == nil || latest.ReportID != s.Evidence.ReportID {
			return ErrAuthorLLMEvidenceChanged
		}
		if _, liftErr := liftRunBoundPauses(ctx, tx, s.ConfigVersion); liftErr != nil {
			return liftErr
		}
		_, insertErr := tx.QueryOneContext(ctx, pg.Scan(&id), `
			INSERT INTO author_llm_run (kind, mode, status, config_version, stage, settings, expected_models,
				reference, created_by_user_id, started_at, estimate_tokens)
			VALUES (?6, ?0, 'running', ?1, 'enqueue', ?2::jsonb, ?3::jsonb, ?4::jsonb, ?5, now(), ?7)
			RETURNING id`, s.Mode, s.ConfigVersion, string(settings), string(s.Evidence.ExpectedModels),
			string(s.Evidence.Reference), by, string(models.AuthorLLMRunMerge), s.EstimateTokens)
		return insertErr
	})
	if err != nil {
		switch pgErrorCode(err) {
		case "23505":
			return 0, ErrAuthorLLMActiveRun
		case "23514":
			return 0, ErrAuthorLLMRunEvidence
		}
		if errors.Is(err, ErrAuthorLLMEvidenceChanged) {
			return 0, err
		}
		return 0, fmt.Errorf("starting a merge run: %w", err)
	}
	return id, nil
}

// RunningAuthorLLMMergeRun reads the configuration's running merge run;
// nil when there is none.
func RunningAuthorLLMMergeRun(ctx context.Context, db pg.DBI, version string) (*AuthorLLMResolveRun, error) {
	var run AuthorLLMResolveRun
	_, err := db.QueryOneContext(ctx, &run, `
		SELECT id, kind, mode, stage, settings, reference FROM author_llm_run
		WHERE kind = ? AND status = 'running' AND config_version = ?`, string(models.AuthorLLMRunMerge), version)
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the running merge run: %w", err)
	}
	return &run, nil
}

// AuthorLLMMergeCard is a candidate's card to freeze: its digest, kind and
// script, and the model input exactly as the build hashed it.
type AuthorLLMMergeCard struct {
	Digest []byte
	Kind   string
	Script string
	Input  []byte
}

// FreezeAuthorLLMMergeInputs freezes the cards not frozen yet and returns
// the frozen card of every one of them by digest: a card frozen before is
// kept as it is, whatever was passed now.
func FreezeAuthorLLMMergeInputs(
	ctx context.Context, db pg.DBI, cards []AuthorLLMMergeCard,
) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage, len(cards))
	if len(cards) == 0 {
		return out, nil
	}
	digests := make([]string, len(cards))
	kinds := make([]string, len(cards))
	scripts := make([]string, len(cards))
	inputs := make([]string, len(cards))
	for i := range cards {
		digests[i] = hex.EncodeToString(cards[i].Digest)
		kinds[i], scripts[i], inputs[i] = cards[i].Kind, cards[i].Script, string(cards[i].Input)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO public.author_llm_merge_input (digest, kind, script, input)
		SELECT decode(u.digest, 'hex'), u.kind, u.script, u.input::jsonb
		FROM unnest(?::text[], ?::text[], ?::text[], ?::text[]) AS u(digest, kind, script, input)
		ON CONFLICT (digest) DO NOTHING`, pg.Array(digests), pg.Array(kinds), pg.Array(scripts),
		pg.Array(inputs)); err != nil {
		return nil, fmt.Errorf("freezing the merge cards: %w", err)
	}
	var rows []struct {
		Digest []byte `pg:"digest"`
		Input  string `pg:"input"`
	}
	if _, err := db.QueryContext(ctx, &rows, `
		SELECT digest, input::text AS input FROM public.author_llm_merge_input
		WHERE digest IN (SELECT decode(d, 'hex') FROM unnest(?::text[]) AS d)`, pg.Array(digests)); err != nil {
		return nil, fmt.Errorf("reading the frozen merge cards: %w", err)
	}
	for _, r := range rows {
		out[string(r.Digest)] = json.RawMessage(r.Input)
	}
	return out, nil
}

// EnqueueAuthorLLMMergeJobs asks the production pair, one question per
// request, about each frozen card of due, the job's input copied from the
// frozen card; modes are the pair's output modes. The insert reads the due
// rule again: a candidate that stopped being due since it was read — its
// kind withdrawn, its script blocked, a decision or a question of it
// stored, or its census row gone (a digest names its input hash) — is not
// asked.
// A run that is not running at that moment gets nothing. It returns how
// many jobs were new.
func EnqueueAuthorLLMMergeJobs(
	ctx context.Context, db *pg.DB, runID int64, version string, due []MergeCandidateDue, modes map[llmreq.Slot]string,
) (int, error) {
	if len(due) == 0 {
		return 0, nil
	}
	digests := make([]string, len(due))
	for i := range due {
		digests[i] = hex.EncodeToString(due[i].Digest)
	}
	mode := func(slot llmreq.Slot) string {
		if m := modes[slot]; m != "" {
			return m
		}
		return AuthorLLMDefaultOutputMode
	}
	inserted := 0
	err := RunAuthorLLMTx(ctx, db, func(tx *pg.Tx) error {
		running, err := LockAuthorLLMRunRunning(ctx, tx, runID)
		if err != nil || !running {
			return err
		}
		res, err := tx.ExecContext(ctx, `
			INSERT INTO author_llm_job (config_version, slot, run_id, purpose, source_fingerprint, extractor_version,
				batch_size, output_mode, input)
			SELECT ?1, s.slot, ?0, ?2, f.digest, ?3, 1, s.mode, f.input
			FROM public.author_llm_merge_input f
			CROSS JOIN (VALUES (?4, ?5), (?6, ?7)) AS s(slot, mode)
			WHERE f.digest IN (SELECT decode(d, 'hex') FROM unnest(?8::text[]) AS d)
				AND EXISTS (SELECT 1`+mergeDueFrom("?1", "?3")+` AND c.digest = f.digest)
			ORDER BY f.digest, s.slot
			ON CONFLICT DO NOTHING`, runID, version, string(models.AuthorLLMPurposeMergeCheck),
			authorentity.MergeCandidateVersion, string(llmreq.SlotProductionA), mode(llmreq.SlotProductionA),
			string(llmreq.SlotProductionB), mode(llmreq.SlotProductionB), pg.Array(digests))
		if err != nil {
			return fmt.Errorf("enqueuing merge questions: %w", err)
		}
		inserted = res.RowsAffected()
		return nil
	})
	return inserted, err
}

// AuthorLLMMergeVerdict is a confirmed merge verdict not settled yet, with
// what the production rule reads: the pair's comparison and agreed answer,
// the judges' judgement, the four jobs that answered, and the candidate's
// kind and script as frozen.
type AuthorLLMMergeVerdict struct {
	ID        int64   `pg:"id"`
	Digest    []byte  `pg:"source_fingerprint"`
	Verdict   string  `pg:"verdict"`
	Judgement *string `pg:"judgement"`
	// Canonical is the digest of the pair's (production_a's) answer.
	Canonical []byte `pg:"canonical_sha256"`
	JobA      int64  `pg:"job_a_id"`
	JobB      int64  `pg:"job_b_id"`
	JudgeA    *int64 `pg:"judge_a_id"`
	JudgeB    *int64 `pg:"judge_b_id"`
	Kind      string `pg:"kind"`
	Script    string `pg:"script"`
}

// AuthorLLMMergeVerdicts lists up to limit confirmed, unsettled verdicts of
// a merge run, oldest first.
func AuthorLLMMergeVerdicts(ctx context.Context, db pg.DBI, runID int64, limit int) ([]AuthorLLMMergeVerdict, error) {
	var rows []AuthorLLMMergeVerdict
	if _, err := db.QueryContext(ctx, &rows, `
		SELECT v.id, v.source_fingerprint, v.verdict, v.judgement, a.canonical_sha256, v.job_a_id, v.job_b_id,
			(SELECT j.id FROM author_llm_job j WHERE j.source_fingerprint = v.source_fingerprint
				AND j.extractor_version = v.extractor_version AND j.purpose = v.purpose
				AND j.config_version = v.config_version AND j.slot = 'judge_a' AND j.eval_item_id IS NULL) AS judge_a_id,
			(SELECT j.id FROM author_llm_job j WHERE j.source_fingerprint = v.source_fingerprint
				AND j.extractor_version = v.extractor_version AND j.purpose = v.purpose
				AND j.config_version = v.config_version AND j.slot = 'judge_b' AND j.eval_item_id IS NULL) AS judge_b_id,
			f.kind, f.script
		FROM author_llm_verdict v
		JOIN public.author_llm_merge_input f ON f.digest = v.source_fingerprint
		LEFT JOIN author_llm_attempt a ON a.id = v.attempt_a_id
		WHERE v.run_id = ?0 AND v.purpose = ?2 AND v.verdict <> 'held_drift' AND v.confirmed_at IS NOT NULL
			AND v.settled_at IS NULL
		ORDER BY v.id
		LIMIT ?1`, runID, limit, string(models.AuthorLLMPurposeMergeCheck)); err != nil {
		return nil, fmt.Errorf("listing the merge verdicts to apply: %w", err)
	}
	return rows, nil
}

// SettleAuthorLLMMergeVerdict records how a confirmed merge verdict ended:
// applied when reason is empty, otherwise why not; stratum is its kind and
// script. A verdict settled before is left as it is.
func SettleAuthorLLMMergeVerdict(ctx context.Context, db pg.DBI, verdictID int64, reason, stratum string) error {
	var r interface{}
	if reason != "" {
		r = reason
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE author_llm_verdict SET settled_at = now(), outcome_reason = ?1, stratum = ?2
		WHERE id = ?0 AND purpose = ?3 AND settled_at IS NULL AND confirmed_at IS NOT NULL
			AND verdict <> 'held_drift'`, verdictID, r, stratum, string(models.AuthorLLMPurposeMergeCheck)); err != nil {
		return fmt.Errorf("settling a merge verdict: %w", err)
	}
	return nil
}

// AuthorLLMMergeRunCount is one stratum's and outcome's verdict count of a
// merge run; an applied verdict has no reason.
type AuthorLLMMergeRunCount struct {
	Stratum string  `pg:"stratum"`
	Reason  *string `pg:"outcome_reason"`
	Count   int     `pg:"count"`
}

// AuthorLLMMergeRunCounts counts a merge run's settled verdicts by stratum
// and outcome.
func AuthorLLMMergeRunCounts(ctx context.Context, db pg.DBI, runID int64) ([]AuthorLLMMergeRunCount, error) {
	var rows []AuthorLLMMergeRunCount
	if _, err := db.QueryContext(ctx, &rows, `
		SELECT coalesce(stratum, ?1) AS stratum, outcome_reason, count(*) AS count
		FROM author_llm_verdict
		WHERE run_id = ?0 AND settled_at IS NOT NULL AND verdict <> 'held_drift'
		GROUP BY 1, 2 ORDER BY 1, 2`, runID, AuthorLLMUnknownStratum); err != nil {
		return nil, fmt.Errorf("counting a merge run's verdicts: %w", err)
	}
	return rows, nil
}
