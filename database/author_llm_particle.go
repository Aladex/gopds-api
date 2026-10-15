package database

import (
	"context"
	"encoding/json"
	"fmt"

	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/internal/authornorm/llmres"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The particle hold (review B1/B2 of 9cc500c). V8 refuses a reply that drops
// a name particle from the person's name components; it can also refuse a
// correct reading (a particle-listed word that is a given name in a badly
// assigned field). A verdict V8 decided — its pair or a judge stayed invalid
// on V8, or its pair's agreed reply, stored before V8 existed, fails V8 now —
// is therefore held for manual review: it is settled for the run's progress
// and report, but it selects no LLM result and decides nothing for the
// input, whose credits stay in review (or go back to it), new credits of the
// fingerprint included. It stays final for automatic application: the input
// is not asked again under the configuration.

// authorLLMParticleClass is the validator class the worker records for a V8
// violation.
const authorLLMParticleClass = "v8_violation"

// authorLLMParticleRejectedSQL is true for a verdict (alias v) a V8
// rejection made invalid: a job of the production pair ended invalid with a
// V8 violation among its replies (verdict invalid), or a judge's job did
// (judgement invalid).
const authorLLMParticleRejectedSQL = `EXISTS (SELECT 1 FROM author_llm_job pj
		JOIN author_llm_attempt pa ON pa.job_id = pj.id AND pa.validator_class = '` + authorLLMParticleClass + `'
		WHERE pj.status = 'invalid'
			AND ((v.verdict = 'invalid' AND pj.id IN (v.job_a_id, v.job_b_id))
				OR (v.verdict = 'agree' AND v.judgement = 'invalid' AND pj.source_fingerprint = v.source_fingerprint
					AND pj.extractor_version = v.extractor_version AND pj.purpose = v.purpose
					AND pj.config_version = v.config_version AND pj.slot IN ('judge_a', 'judge_b')
					AND pj.eval_item_id IS NULL)))`

// AuthorLLMParticleEvidence is what decides whether a verdict is held on the
// particle rule: the pair's comparison, whether a V8 rejection made it
// invalid, and the pair's agreed reply with the input it answered.
type AuthorLLMParticleEvidence struct {
	Verdict          string          `pg:"verdict"`
	ParticleRejected bool            `pg:"particle_rejected"`
	Reply            json.RawMessage `pg:"reply"`
	Input            json.RawMessage `pg:"input"`
}

// ParticleHeld reports a verdict the particle rule holds for manual review:
// a V8 rejection made it invalid, or the pair's agreed reply fails V8 now. A
// reply or input that cannot be read again is not held here: the apply-time
// assembly refuses it as invalid, as before.
func (e *AuthorLLMParticleEvidence) ParticleHeld() bool {
	if e.ParticleRejected {
		return true
	}
	if models.AuthorLLMVerdictValue(e.Verdict) != models.AuthorLLMVerdictAgree || len(e.Reply) == 0 {
		return false
	}
	var input llmreq.ItemInput
	if json.Unmarshal(e.Input, &input) != nil {
		return false
	}
	in, err := llmres.NewInput(input.Fields)
	if err != nil {
		return false
	}
	rep, err := llmres.ParseStoredReply(e.Reply)
	if err != nil {
		return false
	}
	return rep.CheckParticles(&in) != nil
}

// AuthorLLMHeldInput is an input whose latest LLM decision the particle rule
// holds but some of whose credits left review for it — closed by an earlier
// binary (run 17: unresolved llm_invalid) or selected on a result stored
// before V8 (runs 15 and 16) — with the local proposal of its latest
// ambiguous review item, which the reopening puts the credits back on.
type AuthorLLMHeldInput struct {
	SourceFingerprint []byte `pg:"source_fingerprint"`
	ExtractorVersion  string `pg:"extractor_version"`
	DecisionClass     string `pg:"decision_class"`
	ProposalResultID  int64  `pg:"proposal_result_id"`
	AuthorLLMParticleEvidence
}

// authorLLMHeldCandidateSQL selects the inputs the particle rule may hold
// (decided in Go, ParticleHeld) whose credits are not all in review: the
// latest decided verdict of each input of any configuration — the one
// SettledAuthorLLMOutcome reads — that is a V8 rejection or carries an
// agreed reply, a current author credit out of review and not under an
// override, and no credit an admin decided. ?0 are the outcomes that decide
// nothing; ?1/?2 narrow it to one input when not NULL.
const authorLLMHeldCandidateSQL = `
	SELECT x.source_fingerprint, x.extractor_version, i.decision_class, i.proposal_result_id, x.verdict,
		x.particle_rejected, x.reply, x.input
	FROM (
		SELECT DISTINCT ON (v.source_fingerprint, v.extractor_version) v.source_fingerprint, v.extractor_version,
			v.verdict, ` + authorLLMParticleRejectedSQL + ` AS particle_rejected, a.reply, j.input
		FROM author_llm_verdict v
		LEFT JOIN author_llm_attempt a ON a.id = v.attempt_a_id
		LEFT JOIN author_llm_job j ON j.id = v.job_a_id
		WHERE ` + authorLLMDecidingPurposeSQL + ` AND v.settled_at IS NOT NULL AND v.verdict <> 'held_drift'
			AND (v.outcome_reason IS NULL OR v.outcome_reason NOT IN (?0))
			AND (?1::bytea IS NULL OR (v.source_fingerprint = ?1 AND v.extractor_version = ?2))
		ORDER BY v.source_fingerprint, v.extractor_version, v.settled_at DESC, v.id DESC) x
	JOIN LATERAL (
		SELECT ri.decision_class, ri.proposal_result_id FROM contributor_review_item ri
		WHERE ri.scope_fingerprint = x.source_fingerprint AND ri.reason = 'ambiguous_decision'
			AND ri.decision_class IS NOT NULL
		ORDER BY ri.id DESC LIMIT 1) i ON true
	JOIN contributor_normalization_result pr ON pr.id = i.proposal_result_id
		AND pr.extractor_version = x.extractor_version
	WHERE (x.particle_rejected OR (x.verdict = 'agree' AND x.reply IS NOT NULL))
		AND EXISTS (SELECT 1 FROM book_contributor_credit c
			JOIN book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
				AND s.extractor_version = x.extractor_version
			JOIN book_contributor_credit_selection sel ON sel.credit_id = c.id
			WHERE c.source_fingerprint = x.source_fingerprint AND c.role = 'author'
				AND sel.state <> 'review' AND sel.override_id IS NULL)
		AND NOT EXISTS (SELECT 1 FROM book_contributor_credit_selection sel
			WHERE sel.source_fingerprint = x.source_fingerprint AND sel.decided_by_user_id IS NOT NULL)
	ORDER BY x.source_fingerprint, x.extractor_version`

// AuthorLLMHeldInputs lists the inputs the particle rule holds whose credits
// left review (authorLLMHeldCandidateSQL, then ParticleHeld).
func AuthorLLMHeldInputs(ctx context.Context, db pg.DBI) ([]AuthorLLMHeldInput, error) {
	return authorLLMHeldInputs(ctx, db, nil, "")
}

// AuthorLLMHeldInputEligible reports whether one input is still one
// AuthorLLMHeldInputs lists: checked again under the input's lock before it
// is reopened.
func AuthorLLMHeldInputEligible(ctx context.Context, tx ResolutionTx, fingerprint []byte, extractorVersion string) (bool, error) {
	rows, err := authorLLMHeldInputs(ctx, tx, fingerprint, extractorVersion)
	return len(rows) > 0, err
}

func authorLLMHeldInputs(ctx context.Context, db pg.DBI, fingerprint []byte, extractorVersion string) ([]AuthorLLMHeldInput, error) {
	var narrow interface{}
	if fingerprint != nil {
		narrow = fingerprint
	}
	var rows []AuthorLLMHeldInput
	if _, err := db.QueryContext(ctx, &rows, authorLLMHeldCandidateSQL, pg.In(models.AuthorLLMUndecidedOutcomes()),
		narrow, extractorVersion); err != nil {
		return nil, fmt.Errorf("listing the inputs held on the particle rule: %w", err)
	}
	held := rows[:0]
	for i := range rows {
		if rows[i].ParticleHeld() {
			held = append(held, rows[i])
		}
	}
	return held, nil
}
