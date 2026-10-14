package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"gopds-api/internal/authornorm/llmeval"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// A drifted participant's re-learn (migration 35). The provider may switch
// any participant mid-run — another model behind the same name — and its
// periodic check then pauses it for drift. The admin's resume re-learns its
// fingerprint in the current run instead of re-checking it against a
// reference that no longer holds: the fixed request repeated until the calls
// agree (llmeval.RelearnFingerprint, the fingerprint stage's rule), in an
// output mode negotiated again, and the new reference used for the rest of
// the run. Answers of two fingerprints are never mixed: an eval re-asks every
// answer of the participant, a resolve run those its last passed check did
// not cover.

// The re-learn's states.
const (
	authorLLMRelearning      = "relearning"
	authorLLMRelearnLearned  = "learned"
	authorLLMRelearnUnstable = "unstable"
)

// jsonNull is the text of a JSON null.
const jsonNull = "null"

// authorLLMClassRelearned is the class of a job re-asked after its
// participant's re-learn.
const authorLLMClassRelearned = "relearned"

// AuthorLLMRelearnRecord is one re-learn of a run, as the run's report shows
// it: identifiers, references and closed values only.
type AuthorLLMRelearnRecord struct {
	ID           int64                          `pg:"id" json:"-"`
	Participant  string                         `pg:"slot" json:"participant"`
	Status       string                         `pg:"status" json:"status"`
	Repeats      int                            `pg:"repeats" json:"repeats"`
	Reasoning    bool                           `pg:"reasoning" json:"-"`
	OutputMode   string                         `pg:"output_mode" json:"output_mode"`
	OldReference AuthorLLMFingerprintReference  `pg:"old_reference" json:"old_reference"`
	NewReference *AuthorLLMFingerprintReference `pg:"new_reference" json:"new_reference,omitempty"`
	Models       []string                       `pg:"models" json:"models,omitempty"`
	Reason       *string                        `pg:"reason" json:"reason,omitempty"`
	StartedAt    time.Time                      `pg:"started_at" json:"started_at"`
	FinishedAt   *time.Time                     `pg:"finished_at" json:"finished_at,omitempty"`
}

const relearnColumns = `id, slot, status, repeats, reasoning, output_mode, old_reference, new_reference, models,
	reason, started_at, finished_at`

// AuthorLLMRelearns lists a run's re-learns, oldest first.
func AuthorLLMRelearns(ctx context.Context, db pg.DBI, runID int64) ([]AuthorLLMRelearnRecord, error) {
	var rows []AuthorLLMRelearnRecord
	if _, err := db.QueryContext(ctx, &rows, `SELECT `+relearnColumns+` FROM author_llm_relearn
		WHERE run_id = ?0 ORDER BY id`, runID); err != nil {
		return nil, fmt.Errorf("listing a run's re-learns: %w", err)
	}
	return rows, nil
}

// latestAuthorLLMRelearn is a participant's latest re-learn in a run; nil
// when it has none.
func latestAuthorLLMRelearn(ctx context.Context, db pg.DBI, runID int64, slot string) (*AuthorLLMRelearnRecord, error) {
	var r AuthorLLMRelearnRecord
	_, err := db.QueryOneContext(ctx, &r, `SELECT `+relearnColumns+` FROM author_llm_relearn
		WHERE run_id = ?0 AND slot = ?1 ORDER BY id DESC LIMIT 1`, runID, slot)
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading a participant's re-learn: %w", err)
	}
	return &r, nil
}

// latestLearnedAuthorLLMRelearn is a participant's latest learned re-learn in
// a run, whatever came after it — an attempt that did not settle replaces
// nothing; nil when it has none.
func latestLearnedAuthorLLMRelearn(ctx context.Context, db pg.DBI, runID int64, slot string) (*AuthorLLMRelearnRecord, error) {
	var r AuthorLLMRelearnRecord
	_, err := db.QueryOneContext(ctx, &r, `SELECT `+relearnColumns+` FROM author_llm_relearn
		WHERE run_id = ?0 AND slot = ?1 AND status = ?2 AND new_reference IS NOT NULL ORDER BY id DESC LIMIT 1`,
		runID, slot, authorLLMRelearnLearned)
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading a participant's learned reference: %w", err)
	}
	return &r, nil
}

// AuthorLLMLearnedReferences is, per participant, the reference its latest
// learned re-learn in the run replaced the run's own with.
func AuthorLLMLearnedReferences(ctx context.Context, db pg.DBI, runID int64) (map[string]AuthorLLMFingerprintReference, error) {
	rows, err := AuthorLLMRelearns(ctx, db, runID)
	if err != nil {
		return nil, err
	}
	out := map[string]AuthorLLMFingerprintReference{}
	for i := range rows {
		if rows[i].Status == authorLLMRelearnLearned && rows[i].NewReference != nil {
			out[rows[i].Participant] = *rows[i].NewReference
		}
	}
	return out, nil
}

// AuthorLLMResume is a participant's resume.
type AuthorLLMResume struct {
	ConfigVersion string
	Slot          llmreq.Slot
	// Repeats is how many agreeing calls a re-learn needs; Reasoning
	// whether the participant reasons (its reference carries a floor).
	Repeats   int
	Reasoning bool
}

// ResumeAuthorLLMParticipantRelearning lifts a participant's pause. A pause
// for drift (or a re-learn that did not settle) in an active run of the
// configuration starts a re-learn in that run, in the same transaction:
//   - the participant's open calls of the run are fenced — closed as
//     abandoned, their jobs back in the queue — so no answer of the old
//     fingerprint settles after this point;
//   - the re-learn starts in the participant's current output mode, with
//     the reference it replaces.
//
// Any other pause, or no active run, only lifts the pause. It reports
// whether a re-learn started.
func ResumeAuthorLLMParticipantRelearning(ctx context.Context, db *pg.DB, r *AuthorLLMResume) (bool, error) {
	if !r.Slot.Valid() || r.Repeats < 1 {
		return false, ErrAuthorLLMInvalidJob
	}
	started := false
	err := RunAuthorLLMTx(ctx, db, func(tx *pg.Tx) error {
		var txErr error
		started, txErr = resumeAuthorLLMParticipantTx(ctx, tx, r)
		return txErr
	})
	if err != nil {
		return false, fmt.Errorf("resuming an LLM participant: %w", err)
	}
	return started, nil
}

// resumeAuthorLLMParticipantTx is the resume's transaction.
func resumeAuthorLLMParticipantTx(ctx context.Context, tx *pg.Tx, r *AuthorLLMResume) (bool, error) {
	now, err := lockAuthorLLM(ctx, tx, nil)
	if err != nil {
		return false, err
	}
	var runIDs []int64
	if _, err = tx.QueryContext(ctx, &runIDs, `SELECT id FROM author_llm_run
		WHERE config_version = ?0 AND status IN ('running', 'paused')`, r.ConfigVersion); err != nil {
		return false, fmt.Errorf("reading the active run: %w", err)
	}
	locked, err := lockAuthorLLMRuns(ctx, tx, authorLLMRunWrite, false, runIDs)
	if err != nil {
		return false, err
	}
	var paused *string
	if _, err = tx.QueryOneContext(ctx, pg.Scan(&paused), `SELECT paused_reason FROM author_llm_provider_state
		WHERE config_version = ?0 AND slot = ?1 FOR UPDATE`, r.ConfigVersion, string(r.Slot)); err != nil {
		return false, fmt.Errorf("reading the participant state: %w", err)
	}
	// The resume also starts the participant's run of rejected items again.
	if liftErr := execAuthorLLM(ctx, tx, `UPDATE author_llm_provider_state
		SET paused_reason = NULL, paused_at = NULL, rejected_items = '{}'
		WHERE config_version = ?0 AND slot = ?1`, r.ConfigVersion, string(r.Slot)); liftErr != nil {
		return false, liftErr
	}
	relearn := paused != nil && (*paused == string(models.AuthorLLMPausedDrift) ||
		*paused == string(models.AuthorLLMPausedRelearnUnstable))
	if !relearn || len(locked) == 0 {
		return false, nil
	}
	return startAuthorLLMRelearn(ctx, tx, r, locked[0], now)
}

// startAuthorLLMRelearn fences the participant's open calls of the run and
// starts its re-learn from its current reference.
func startAuthorLLMRelearn(ctx context.Context, tx *pg.Tx, r *AuthorLLMResume, runID int64, now time.Time) (bool, error) {
	old, err := currentAuthorLLMReference(ctx, tx, runID, string(r.Slot))
	if err != nil || old == nil {
		// A run still learning its fingerprints has nothing to re-learn:
		// its stage measures the participant anyway.
		return false, err
	}
	if fenceErr := fenceAuthorLLMCalls(ctx, tx, runID, string(r.Slot), now); fenceErr != nil {
		return false, fenceErr
	}
	mode := old.OutputMode
	if mode == "" {
		mode = AuthorLLMDefaultOutputMode
	}
	oldJSON, err := json.Marshal(old)
	if err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO author_llm_relearn (run_id, config_version, slot, repeats, reasoning, output_mode,
			old_reference, started_at)
		VALUES (?0, ?1, ?2, ?3, ?4, ?5, ?6::jsonb, ?7)`,
		runID, r.ConfigVersion, string(r.Slot), r.Repeats, r.Reasoning, mode, string(oldJSON), now); err != nil {
		return false, fmt.Errorf("starting a re-learn: %w", err)
	}
	return true, nil
}

// currentAuthorLLMReference is the reference a participant is checked
// against in the run now: its latest learned re-learn's, else the run's own.
func currentAuthorLLMReference(ctx context.Context, tx *pg.Tx, runID int64, slot string) (*AuthorLLMFingerprintReference, error) {
	learned, err := latestLearnedAuthorLLMRelearn(ctx, tx, runID, slot)
	if err != nil {
		return nil, err
	}
	if learned != nil {
		return learned.NewReference, nil
	}
	var raw *string
	if _, err = tx.QueryOneContext(ctx, pg.Scan(&raw), `SELECT (reference -> ?1)::text FROM author_llm_run WHERE id = ?0`,
		runID, slot); err != nil {
		return nil, fmt.Errorf("reading a run's reference: %w", err)
	}
	if raw == nil || *raw == jsonNull {
		return nil, nil
	}
	var ref AuthorLLMFingerprintReference
	if err := json.Unmarshal([]byte(*raw), &ref); err != nil {
		return nil, fmt.Errorf("reading a run's reference: %w", err)
	}
	return &ref, nil
}

// fenceAuthorLLMCalls closes a participant's open calls of a run as
// abandoned, as the run's close does: their settle finds the lease lost and
// changes nothing, their attempts are closed, their claimed jobs go back to
// the queue without a failure counted, and a check in flight is no longer.
func fenceAuthorLLMCalls(ctx context.Context, tx *pg.Tx, runID int64, slot string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		WITH open AS (
			SELECT id FROM author_llm_call WHERE run_id = ?0 AND slot = ?1 AND finished_at IS NULL FOR UPDATE
		), closed AS (
			UPDATE author_llm_call c
			SET finished_at = ?2, outcome = 'abandoned', error_class = ?3, settled_tokens = c.reserved_tokens,
				lease_owner = NULL, lease_expires_at = NULL
			FROM open o WHERE c.id = o.id
			RETURNING c.id
		), attempts AS (
			UPDATE author_llm_attempt a SET finished_at = ?2, outcome = 'abandoned'
			FROM closed WHERE a.call_id = closed.id AND a.finished_at IS NULL
			RETURNING a.job_id
		), checks AS (
			UPDATE author_llm_provider_state p SET check_call_id = NULL
			FROM closed WHERE p.check_call_id = closed.id
			RETURNING p.slot
		)
		UPDATE author_llm_job j SET call_id = NULL, status = 'pending', next_attempt_at = ?2
		FROM attempts WHERE j.id = attempts.job_id AND j.status = 'claimed'`,
		runID, slot, now, AuthorLLMClassDrift); err != nil {
		return fmt.Errorf("fencing a participant's calls: %w", err)
	}
	return nil
}

// settleAuthorLLMRelearnCheck settles a check of a participant that is
// re-learning. A rejected request format moves the re-learn to the next
// output mode (its earlier measurements no longer count); after the last
// mode the re-learn does not settle. Once Repeats checks answered in the
// mode, the fingerprint stage's rule decides: learned — the new reference
// stands from now on and the participant's answers of the old fingerprint
// are re-asked — or unstable — the participant stays paused and nothing is
// voided. handled is false for a call the generic check settle takes (no
// response for another reason).
func settleAuthorLLMRelearnCheck(ctx context.Context, tx *pg.Tx, call *settledCall, s *AuthorLLMSettlement,
	rl *AuthorLLMRelearnRecord, now time.Time, report *AuthorLLMSettleReport) (handled bool, err error) {
	if s.Outcome == models.AuthorLLMCallHTTPError && s.HTTPStatus == http.StatusBadRequest {
		i := slices.Index(authorLLMOutputModeOrder, rl.OutputMode)
		if i >= 0 && i+1 < len(authorLLMOutputModeOrder) {
			return true, execAuthorLLM(ctx, tx, `UPDATE author_llm_relearn SET output_mode = ?1 WHERE id = ?0`,
				rl.ID, authorLLMOutputModeOrder[i+1])
		}
		return true, endAuthorLLMRelearnUnstable(ctx, tx, call, rl, authorLLMNoOutputMode+": "+call.Slot, now, report)
	}
	if s.Outcome != models.AuthorLLMCallAnswered {
		return false, nil
	}
	var calls []struct {
		PromptTokens *int64  `pg:"prompt_tokens"`
		Model        *string `pg:"response_model"`
		Reasoning    *int64  `pg:"reasoning"`
	}
	if _, err = tx.QueryContext(ctx, &calls, `
		SELECT prompt_tokens, response_model, coalesce(reasoning_tokens, completion_tokens) AS reasoning
		FROM author_llm_call
		WHERE run_id = ?0 AND slot = ?1 AND kind = 'fingerprint' AND outcome = 'answered'
			AND output_mode = ?2 AND created_at >= ?3
		ORDER BY id
		LIMIT ?4`, call.RunID, call.Slot, rl.OutputMode, rl.StartedAt, rl.Repeats); err != nil {
		return true, fmt.Errorf("reading a re-learn's checks: %w", err)
	}
	if len(calls) < rl.Repeats {
		return true, nil
	}
	ms := make([]llmeval.FingerprintMeasurement, 0, len(calls))
	for _, c := range calls {
		m := llmeval.FingerprintMeasurement{Participant: call.Slot,
			Answered: c.PromptTokens != nil && c.Model != nil}
		if m.Answered {
			m.Model, m.PromptTokens = *c.Model, *c.PromptTokens
			if c.Reasoning != nil {
				m.Reasoning = *c.Reasoning
			}
		}
		ms = append(ms, m)
	}
	others, err := currentAuthorLLMPrompts(ctx, tx, call.RunID)
	if err != nil {
		return true, err
	}
	fp, reason := llmeval.RelearnFingerprint(call.Slot, ms, rl.Reasoning, others)
	if reason != "" {
		return true, endAuthorLLMRelearnUnstable(ctx, tx, call, rl, reason, now, report)
	}
	return true, learnAuthorLLMRelearn(ctx, tx, call, rl, &fp, now)
}

// authorLLMNoOutputMode is the reason of a re-learn no output mode answered.
const authorLLMNoOutputMode = "no_output_mode"

// currentAuthorLLMPrompts is every participant's current reference prompt
// token count in the run.
func currentAuthorLLMPrompts(ctx context.Context, tx *pg.Tx, runID int64) (map[string]int64, error) {
	out := map[string]int64{}
	for _, slot := range llmreq.Slots() {
		ref, err := currentAuthorLLMReference(ctx, tx, runID, string(slot))
		if err != nil {
			return nil, err
		}
		if ref != nil {
			out[string(slot)] = ref.PromptTokens
		}
	}
	return out, nil
}

// endAuthorLLMRelearnUnstable ends a re-learn that did not settle: the
// participant is paused with relearn_unstable and nothing is voided.
func endAuthorLLMRelearnUnstable(ctx context.Context, tx *pg.Tx, call *settledCall, rl *AuthorLLMRelearnRecord,
	reason string, now time.Time, report *AuthorLLMSettleReport) error {
	if err := execAuthorLLM(ctx, tx, `UPDATE author_llm_relearn SET status = ?1, reason = ?2, finished_at = ?3
		WHERE id = ?0`, rl.ID, authorLLMRelearnUnstable, reason, now); err != nil {
		return err
	}
	return pauseAuthorLLMParticipant(ctx, tx, call, models.AuthorLLMPausedRelearnUnstable, now, report)
}

// learnAuthorLLMRelearn ends a re-learn with its new reference. The
// participant's checks start over from it (the check covers what came
// before its own call), and the answers of the old fingerprint are re-asked:
// in an eval run every answer of the participant (a participant's metrics
// come from one fingerprint), in a resolve run those since its last passed
// check (verdicts confirmed before stand). The re-asked jobs, and every job
// of the participant still waiting, go out in the re-learned output mode.
func learnAuthorLLMRelearn(ctx context.Context, tx *pg.Tx, call *settledCall, rl *AuthorLLMRelearnRecord,
	fp *llmeval.ParticipantFingerprint, now time.Time) error {
	ref, err := json.Marshal(AuthorLLMFingerprintReference{PromptTokens: fp.PromptTokens,
		ReasoningFloor: fp.ReasoningFloor, OutputMode: rl.OutputMode})
	if err != nil {
		return err
	}
	modelIDs, err := json.Marshal(fp.Models)
	if err != nil {
		return err
	}
	if endErr := execAuthorLLM(ctx, tx, `UPDATE author_llm_relearn
		SET status = ?1, new_reference = ?2::jsonb, models = ?3::jsonb, finished_at = ?4 WHERE id = ?0`,
		rl.ID, authorLLMRelearnLearned, string(ref), string(modelIDs), now); endErr != nil {
		return endErr
	}
	var kind string
	var lastPass *time.Time
	if _, err = tx.QueryOneContext(ctx, pg.Scan(&kind, &lastPass), `
		SELECT r.kind, s.last_check_passed_at FROM author_llm_run r, author_llm_provider_state s
		WHERE r.id = ?0 AND s.config_version = ?1 AND s.slot = ?2`, call.RunID, call.ConfigVersion, call.Slot); err != nil {
		return fmt.Errorf("reading the re-learned run: %w", err)
	}
	if kind == string(models.AuthorLLMRunEval) {
		if voidErr := execAuthorLLM(ctx, tx, `
			UPDATE author_llm_job SET status = 'pending', answer_attempt_id = NULL, finished_at = NULL, call_id = NULL,
				failure_count = 0, transient_since = NULL, invalid_count = 0, retry_single = false, last_error_class = ?3,
				next_attempt_at = ?2
			WHERE run_id = ?0 AND slot = ?1 AND purpose <> ?4 AND status IN ('answered', 'invalid', 'failed')`,
			call.RunID, call.Slot, now, authorLLMClassRelearned, string(models.AuthorLLMPurposeEvalFingerprint)); voidErr != nil {
			return voidErr
		}
	} else if _, err = voidAuthorLLMWindow(ctx, tx, call.ConfigVersion, call.Slot, call.RunID, lastPass, now,
		authorLLMClassRelearned); err != nil {
		return err
	}
	if modeErr := execAuthorLLM(ctx, tx, `UPDATE author_llm_job SET output_mode = ?2
		WHERE run_id = ?0 AND slot = ?1 AND status = 'pending' AND purpose <> ?3 AND output_mode <> ?2`,
		call.RunID, call.Slot, rl.OutputMode, string(models.AuthorLLMPurposeEvalFingerprint)); modeErr != nil {
		return modeErr
	}
	return execAuthorLLM(ctx, tx, `UPDATE author_llm_provider_state
		SET check_run_id = ?2, last_check_passed_at = ?3, answered_since_check = 0
		WHERE config_version = ?0 AND slot = ?1`, call.ConfigVersion, call.Slot, call.RunID, call.CreatedAt)
}
