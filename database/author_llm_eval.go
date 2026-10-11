package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The eval run's database side (design K, section 8; migration 31): the
// population it samples from, the anchor candidates, the frozen sets, the
// jobs of each stage, the answers the report is computed from, and the report
// itself. The stage logic and the sampling live in services.

// ErrAuthorLLMEvalSetFrozen marks a second freeze of a set version.
var ErrAuthorLLMEvalSetFrozen = errors.New("database: the eval set is already frozen")

// AuthorLLMPopulationRow is one open ambiguous review input: a fingerprint
// with its local decision class, script, quality flags, extractor version,
// the source fields of one of its credits, and its weight (the credits in
// review with that fingerprint).
type AuthorLLMPopulationRow struct {
	SourceFingerprint []byte   `pg:"source_fingerprint"`
	DecisionClass     string   `pg:"decision_class"`
	Script            *string  `pg:"script"`
	QualityFlags      []string `pg:"quality_flags,array"`
	ExtractorVersion  string   `pg:"extractor_version"`
	First             *string  `pg:"source_first_name"`
	Middle            *string  `pg:"source_middle_name"`
	Last              *string  `pg:"source_last_name"`
	Nickname          *string  `pg:"source_nickname"`
	Weight            int      `pg:"weight"`
}

// AuthorLLMEvalPopulation lists the open ambiguous review inputs the eval
// sets are sampled from.
func AuthorLLMEvalPopulation(ctx context.Context, db pg.DBI) ([]AuthorLLMPopulationRow, error) {
	var rows []AuthorLLMPopulationRow
	// The weights are counted once for all fingerprints: the selection
	// table has no index on the fingerprint, and a per-row count scans it
	// for every item (413 s for 34 366 items on a catalog-sized copy).
	_, err := db.QueryContext(ctx, &rows, `
		WITH weights AS (
			SELECT source_fingerprint, count(*) AS weight FROM book_contributor_credit_selection
			WHERE state = 'review' GROUP BY source_fingerprint)
		SELECT i.source_fingerprint, i.decision_class, r.script, r.quality_flags, r.extractor_version,
			c.source_first_name, c.source_middle_name, c.source_last_name, c.source_nickname,
			coalesce(w.weight, 0) AS weight
		FROM contributor_review_item i
		JOIN contributor_normalization_result r ON r.id = i.proposal_result_id
		LEFT JOIN weights w ON w.source_fingerprint = i.source_fingerprint
		JOIN LATERAL (
			SELECT cc.source_first_name, cc.source_middle_name, cc.source_last_name, cc.source_nickname
			FROM book_contributor_credit cc
			JOIN book_metadata_snapshot s ON s.id = cc.snapshot_id AND s.is_current
			WHERE cc.source_fingerprint = i.source_fingerprint AND cc.role = 'author'
				AND s.extractor_version = r.extractor_version
			ORDER BY cc.id
			LIMIT 1) c ON true
		WHERE i.status = 'open' AND i.reason = 'ambiguous_decision' AND i.scope_fingerprint IS NOT NULL
			AND i.decision_class IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("reading the eval population: %w", err)
	}
	return rows, nil
}

// AuthorLLMAnchorRow is a candidate of the anchor set: a structured_person
// credit the shipped policy selected, one word in first and one in last.
type AuthorLLMAnchorRow struct {
	SourceFingerprint []byte `pg:"source_fingerprint"`
	ExtractorVersion  string `pg:"extractor_version"`
	Script            string `pg:"script"`
	First             string `pg:"source_first_name"`
	Last              string `pg:"source_last_name"`
}

// AuthorLLMAnchorCandidates lists the anchor candidates whose fields the
// catalog's role statistics confirm (design K, section 8): the first word
// stands in a first field in at least minSharePercent of its occurrences, the
// last word in a last field likewise, each with at least minSupport
// occurrences. Cyrillic and Latin only; letters, apostrophes and hyphens only;
// no middle name, no nickname.
func AuthorLLMAnchorCandidates(ctx context.Context, db pg.DBI, minSupport, minSharePercent int) ([]AuthorLLMAnchorRow, error) {
	var rows []AuthorLLMAnchorRow
	err := withoutNestedLoops(ctx, db, func(tx *pg.Tx) error {
		return queryAuthorLLMAnchorCandidates(ctx, tx, &rows, minSupport, minSharePercent)
	})
	return rows, err
}

// withoutNestedLoops runs fn with enable_nestloop off for its statements
// only: in a transaction of its own on a pool or a connection, inside the
// caller's transaction on a *pg.Tx — and there the value it found (the
// caller's own, not the default) is back for its next statements. The anchor-candidate query needs it: the planner
// estimates its filtered credits at one row and its word counts at 200 (on
// the production catalog: ~674 000 credits, 25 157 first and 86 331 last
// words) and nests loops over the CTE scans, which did not finish in 30
// minutes; with hash joins it takes seconds.
func withoutNestedLoops(ctx context.Context, db pg.DBI, fn func(tx *pg.Tx) error) error {
	guarded := func(tx *pg.Tx) error {
		var prior string
		if _, err := tx.QueryOneContext(ctx, pg.Scan(&prior), `SELECT current_setting('enable_nestloop')`); err != nil {
			return fmt.Errorf("reading enable_nestloop: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `SET LOCAL enable_nestloop = off`); err != nil {
			return fmt.Errorf("turning nested loops off: %w", err)
		}
		err := fn(tx)
		// The value found comes back on the error path too: a caller's
		// transaction may go on (a failed statement aborts it, and then the
		// restore fails with it — fn's error is the one returned).
		if _, restoreErr := tx.ExecContext(ctx, `SELECT set_config('enable_nestloop', ?, true)`, prior); restoreErr != nil && err == nil {
			return fmt.Errorf("restoring enable_nestloop: %w", restoreErr)
		}
		return err
	}
	switch d := db.(type) {
	case *pg.Tx:
		return guarded(d)
	case interface {
		RunInTransaction(context.Context, func(*pg.Tx) error) error
	}:
		return d.RunInTransaction(ctx, guarded)
	}
	return fmt.Errorf("database: no transaction for %T", db)
}

func queryAuthorLLMAnchorCandidates(
	ctx context.Context, tx *pg.Tx, rows *[]AuthorLLMAnchorRow, minSupport, minSharePercent int,
) error {
	_, err := tx.QueryContext(ctx, rows, `
		WITH cur AS (
			SELECT c.id, c.source_fingerprint, c.source_first_name, c.source_middle_name, c.source_last_name,
				c.source_nickname, s.extractor_version
			FROM book_contributor_credit c
			JOIN book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
			WHERE c.role = 'author'
		), firsts AS (
			SELECT lower(source_first_name) AS word, count(*) AS n FROM cur
			WHERE source_first_name IS NOT NULL GROUP BY 1
		), lasts AS (
			SELECT lower(source_last_name) AS word, count(*) AS n FROM cur
			WHERE source_last_name IS NOT NULL GROUP BY 1
		)
		SELECT DISTINCT ON (cur.source_fingerprint) cur.source_fingerprint, cur.extractor_version, r.script,
			cur.source_first_name, cur.source_last_name
		FROM book_contributor_credit_selection sel
		JOIN contributor_normalization_result r ON r.id = sel.result_id
		JOIN cur ON cur.id = sel.credit_id
		LEFT JOIN firsts f_as_first ON f_as_first.word = lower(cur.source_first_name)
		LEFT JOIN lasts f_as_last ON f_as_last.word = lower(cur.source_first_name)
		LEFT JOIN firsts l_as_first ON l_as_first.word = lower(cur.source_last_name)
		LEFT JOIN lasts l_as_last ON l_as_last.word = lower(cur.source_last_name)
		WHERE sel.state = 'selected' AND sel.basis = 'automatic'
			AND r.decision_class = 'structured_person' AND r.script IN ('Cyrl', 'Latn')
			AND cur.source_middle_name IS NULL AND cur.source_nickname IS NULL
			AND cur.source_first_name ~ '^[[:alpha:]][[:alpha:]''-]*$'
			AND cur.source_last_name ~ '^[[:alpha:]][[:alpha:]''-]*$'
			AND f_as_first.n + coalesce(f_as_last.n, 0) >= ?0
			AND f_as_first.n * 100 >= ?1 * (f_as_first.n + coalesce(f_as_last.n, 0))
			AND l_as_last.n + coalesce(l_as_first.n, 0) >= ?0
			AND l_as_last.n * 100 >= ?1 * (l_as_last.n + coalesce(l_as_first.n, 0))
		ORDER BY cur.source_fingerprint, cur.id`, minSupport, minSharePercent)
	if err != nil {
		return fmt.Errorf("reading the anchor candidates: %w", err)
	}
	return nil
}

// AuthorLLMNewEvalItem is one item of a set being frozen.
type AuthorLLMNewEvalItem struct {
	ItemSet           models.AuthorLLMEvalItemSet
	Stratum           string
	SourceFingerprint []byte
	ExtractorVersion  string
	Input             json.RawMessage
	Weight            int
	ContextBookID     *int64
	BookMD5           *string
	ContextSHA256     []byte
	Expected          json.RawMessage
}

// FrozenAuthorLLMEvalSets lists the frozen set versions.
func FrozenAuthorLLMEvalSets(ctx context.Context, db pg.DBI) (map[string]bool, error) {
	var versions []string
	if _, err := db.QueryContext(ctx, &versions, `SELECT set_version FROM author_llm_eval_set`); err != nil {
		return nil, fmt.Errorf("listing the frozen eval sets: %w", err)
	}
	out := make(map[string]bool, len(versions))
	for _, v := range versions {
		out[v] = true
	}
	return out, nil
}

// FreezeAuthorLLMEvalSet writes one set version with its items, in one
// transaction: a set row exists only with all of its items. A version already
// frozen is ErrAuthorLLMEvalSetFrozen and changes nothing.
func FreezeAuthorLLMEvalSet(
	ctx context.Context, db *pg.DB, runID int64, version string, sha []byte, items []AuthorLLMNewEvalItem,
) error {
	return db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO author_llm_eval_set (set_version, sha256, items, created_by_run_id)
			VALUES (?0, ?1, ?2, ?3) ON CONFLICT (set_version) DO NOTHING`, version, sha, len(items), runID)
		if err != nil {
			return fmt.Errorf("freezing an eval set: %w", err)
		}
		if res.RowsAffected() == 0 {
			return ErrAuthorLLMEvalSetFrozen
		}
		for i := range items {
			it := &items[i]
			var sum, expected interface{}
			if len(it.ContextSHA256) > 0 {
				sum = it.ContextSHA256
			}
			if len(it.Expected) > 0 {
				expected = string(it.Expected)
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO author_llm_eval_item (set_version, item_set, stratum, source_fingerprint, extractor_version,
					input, weight, context_book_id, book_md5, context_sha256, expected)
				VALUES (?0, ?1, ?2, ?3, ?4, ?5::jsonb, ?6, ?7, ?8, ?9, ?10::jsonb)`,
				version, string(it.ItemSet), it.Stratum, it.SourceFingerprint, it.ExtractorVersion, string(it.Input),
				max(it.Weight, 1), it.ContextBookID, it.BookMD5, sum, expected); err != nil {
				return fmt.Errorf("freezing an eval item: %w", err)
			}
		}
		return nil
	})
}

// AuthorLLMEvalItemRow is a frozen item as the stages read it.
type AuthorLLMEvalItemRow struct {
	ID                int64           `pg:"id"`
	SetVersion        string          `pg:"set_version"`
	ItemSet           string          `pg:"item_set"`
	Stratum           string          `pg:"stratum"`
	SourceFingerprint []byte          `pg:"source_fingerprint"`
	Input             json.RawMessage `pg:"input,type:jsonb"`
	Weight            int             `pg:"weight"`
	ContextSHA256     []byte          `pg:"context_sha256"`
	Expected          json.RawMessage `pg:"expected,type:jsonb"`
}

// AuthorLLMEvalItems lists the frozen items of the given set versions and
// item sets, by id.
func AuthorLLMEvalItems(
	ctx context.Context, db pg.DBI, versions []string, sets []models.AuthorLLMEvalItemSet,
) ([]AuthorLLMEvalItemRow, error) {
	if len(versions) == 0 || len(sets) == 0 {
		return nil, nil
	}
	names := make([]string, len(sets))
	for i, s := range sets {
		names[i] = string(s)
	}
	var rows []AuthorLLMEvalItemRow
	_, err := db.QueryContext(ctx, &rows, `
		SELECT id, set_version, item_set, stratum, source_fingerprint, input, weight, context_sha256, expected
		FROM author_llm_eval_item WHERE set_version IN (?0) AND item_set IN (?1) ORDER BY id`,
		pg.In(versions), pg.In(names))
	if err != nil {
		return nil, fmt.Errorf("reading the eval items: %w", err)
	}
	return rows, nil
}

// AuthorLLMEvalSetRow is a frozen set version.
type AuthorLLMEvalSetRow struct {
	SetVersion string `pg:"set_version" json:"set_version"`
	SHA256     []byte `pg:"sha256" json:"-"`
	Items      int    `pg:"items" json:"items"`
}

// AuthorLLMEvalSetsByVersion reads the given frozen set versions.
func AuthorLLMEvalSetsByVersion(ctx context.Context, db pg.DBI, versions []string) ([]AuthorLLMEvalSetRow, error) {
	if len(versions) == 0 {
		return nil, nil
	}
	var rows []AuthorLLMEvalSetRow
	if _, err := db.QueryContext(ctx, &rows,
		`SELECT set_version, sha256, items FROM author_llm_eval_set WHERE set_version IN (?) ORDER BY set_version`,
		pg.In(versions)); err != nil {
		return nil, fmt.Errorf("reading the eval sets: %w", err)
	}
	return rows, nil
}

// AuthorLLMEvalJobSpec enqueues one stage's jobs of one participant for a list
// of frozen items.
type AuthorLLMEvalJobSpec struct {
	RunID         int64
	ConfigVersion string
	Slot          string
	Purpose       models.AuthorLLMJobPurpose
	// Arm is "" (judges, anchor, fingerprint), "A" or "B".
	Arm        string
	BatchSize  int
	RepeatNo   int
	OutputMode string
	ItemIDs    []int64
	// WithContext asks for the item's excerpt; only items frozen with a
	// relevant excerpt get it.
	WithContext bool
}

// EnqueueAuthorLLMEvalJobs inserts a stage's jobs from the frozen items, in
// one statement; a job that already exists is kept. A run that is not
// running at that moment (paused, ended) gets nothing. It returns how many
// were new.
func EnqueueAuthorLLMEvalJobs(ctx context.Context, db pg.DBI, spec *AuthorLLMEvalJobSpec) (int, error) {
	if len(spec.ItemIDs) == 0 {
		return 0, nil
	}
	var arm interface{}
	if spec.Arm != "" {
		arm = spec.Arm
	}
	res, err := db.ExecContext(ctx, `
		INSERT INTO author_llm_job (config_version, slot, run_id, purpose, eval_item_id, arm, repeat_no, batch_size,
			output_mode, input, with_context)
		SELECT ?0, ?1, ?2, ?3, e.id, ?4, ?5, ?6, ?7, e.input, ?8 AND e.context_sha256 IS NOT NULL
		FROM author_llm_eval_item e
		WHERE e.id IN (?9)
			AND EXISTS (SELECT 1 FROM author_llm_run WHERE id = ?2 AND status = 'running')
		ON CONFLICT DO NOTHING`,
		spec.ConfigVersion, spec.Slot, spec.RunID, string(spec.Purpose), arm, spec.RepeatNo, spec.BatchSize,
		spec.OutputMode, spec.WithContext, pg.In(spec.ItemIDs))
	if err != nil {
		return 0, fmt.Errorf("enqueuing eval jobs: %w", err)
	}
	return res.RowsAffected(), nil
}

// AuthorLLMEvalUnfinished counts the run's jobs of the given purposes that are
// not done yet.
func AuthorLLMEvalUnfinished(ctx context.Context, db pg.DBI, runID int64, purposes ...models.AuthorLLMJobPurpose) (int, error) {
	names := make([]string, len(purposes))
	for i, p := range purposes {
		names[i] = string(p)
	}
	var n int
	if _, err := db.QueryOneContext(ctx, pg.Scan(&n), `
		SELECT count(*) FROM author_llm_job
		WHERE run_id = ?0 AND purpose IN (?1) AND status IN ('pending', 'claimed')`, runID, pg.In(names)); err != nil {
		return 0, fmt.Errorf("counting unfinished eval jobs: %w", err)
	}
	return n, nil
}

// SetAuthorLLMRunStage moves a running run from one stage to the next; a run
// not at from (another replica moved it) or not running (paused while the
// stage was being done) is left alone.
func SetAuthorLLMRunStage(ctx context.Context, db pg.DBI, runID int64, from, to string) (bool, error) {
	res, err := db.ExecContext(ctx, `
		UPDATE author_llm_run SET stage = ?2 WHERE id = ?0 AND stage = ?1 AND status = 'running'`,
		runID, from, to)
	if err != nil {
		return false, fmt.Errorf("moving an LLM run's stage: %w", err)
	}
	return res.RowsAffected() == 1, nil
}

// SetAuthorLLMRunEstimate records a run's token estimate.
func SetAuthorLLMRunEstimate(ctx context.Context, db pg.DBI, runID, tokens int64) error {
	if _, err := db.ExecContext(ctx, `UPDATE author_llm_run SET estimate_tokens = ?1 WHERE id = ?0`, runID, tokens); err != nil {
		return fmt.Errorf("recording a run's estimate: %w", err)
	}
	return nil
}

// SetAuthorLLMRunLearned records what an eval run's fingerprint stage
// learned: the model ids V0 accepts from now on and the fingerprint
// references the checks compare with.
func SetAuthorLLMRunLearned(
	ctx context.Context, db pg.DBI, runID int64, expected map[string][]string, reference map[string]AuthorLLMFingerprintReference,
) error {
	exp, err := json.Marshal(expected)
	if err != nil {
		return err
	}
	ref, err := json.Marshal(reference)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE author_llm_run SET expected_models = ?1::jsonb, reference = ?2::jsonb
		WHERE id = ?0 AND kind = 'eval'`, runID, string(exp), string(ref)); err != nil {
		return fmt.Errorf("recording what the run learned: %w", err)
	}
	return nil
}

// AuthorLLMEvalAnswer is one job's final answer as the report reads it.
type AuthorLLMEvalAnswer struct {
	EvalItemID      int64   `pg:"eval_item_id"`
	Purpose         string  `pg:"purpose"`
	Slot            string  `pg:"slot"`
	Arm             *string `pg:"arm"`
	BatchSize       int     `pg:"batch_size"`
	RepeatNo        int     `pg:"repeat_no"`
	Status          string  `pg:"status"`
	WithContext     bool    `pg:"with_context"`
	ContextState    string  `pg:"context_state"`
	CanonicalSHA256 []byte  `pg:"canonical_sha256"`
	LastErrorClass  *string `pg:"last_error_class"`
	OutputMode      string  `pg:"output_mode"`
	Refused         *bool   `pg:"refused"`
	Tier            *string `pg:"tier"`
	// The answering call's measurements (fingerprint stage).
	ResponseModel    *string `pg:"response_model"`
	PromptTokens     *int64  `pg:"prompt_tokens"`
	CompletionTokens *int64  `pg:"completion_tokens"`
	ReasoningTokens  *int64  `pg:"reasoning_tokens"`
}

// AuthorLLMEvalAnswers lists every job of an eval run with its final answer.
func AuthorLLMEvalAnswers(ctx context.Context, db pg.DBI, runID int64) ([]AuthorLLMEvalAnswer, error) {
	var rows []AuthorLLMEvalAnswer
	_, err := db.QueryContext(ctx, &rows, `
		SELECT j.eval_item_id, j.purpose, j.slot, j.arm, j.batch_size, j.repeat_no, j.status, j.with_context,
			j.context_state, a.canonical_sha256, j.last_error_class, j.output_mode, a.refused, a.tier, c.response_model, c.prompt_tokens,
			c.completion_tokens, c.reasoning_tokens
		FROM author_llm_job j
		LEFT JOIN author_llm_attempt a ON a.id = j.answer_attempt_id
		LEFT JOIN author_llm_call c ON c.id = a.call_id
		WHERE j.run_id = ?0
		ORDER BY j.id`, runID)
	if err != nil {
		return nil, fmt.Errorf("reading the eval answers: %w", err)
	}
	return rows, nil
}

// AuthorLLMEvalPriorFullRuns counts the completed full eval reports of
// configurations with this prompt: the report names which attempt on the same
// holdout this is.
func AuthorLLMEvalPriorFullRuns(ctx context.Context, db pg.DBI, version string) (int, error) {
	var n int
	_, err := db.QueryOneContext(ctx, pg.Scan(&n), `
		SELECT count(*) FROM author_llm_eval_report r
		JOIN author_llm_run run ON run.id = r.run_id
		JOIN author_llm_config c ON c.version = r.config_version
		WHERE run.mode = 'full'
			AND c.prompt_sha256 = (SELECT prompt_sha256 FROM author_llm_config WHERE version = ?0)`, version)
	if err != nil {
		return 0, fmt.Errorf("counting earlier full eval runs: %w", err)
	}
	return n, nil
}

// StoreAuthorLLMEvalReport writes a run's report and completes the run, in one
// transaction, if the run is running at that moment (locked); a run paused
// while its report was being computed is left alone and reported after the
// resume. The digest is the SHA-256 of the stored report's text form —
// exactly the bytes the download returns. A report already written is kept.
func StoreAuthorLLMEvalReport(ctx context.Context, db *pg.DB, runID int64, version string, report []byte) error {
	return RunAuthorLLMTx(ctx, db, func(tx *pg.Tx) error {
		if _, err := lockAuthorLLMRuns(ctx, tx, authorLLMRunWrite, false, []int64{runID}); err != nil {
			return err
		}
		var running bool
		if _, err := tx.QueryOneContext(ctx, pg.Scan(&running),
			`SELECT status = 'running' FROM author_llm_run WHERE id = ?0`, runID); err != nil {
			return fmt.Errorf("reading an eval run's status: %w", err)
		}
		if !running {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO author_llm_eval_report (run_id, config_version, report, report_sha256)
			VALUES (?0, ?1, ?2::jsonb, sha256(convert_to((?2::jsonb)::text, 'UTF8')))
			ON CONFLICT (run_id) DO NOTHING`, runID, version, string(report)); err != nil {
			return fmt.Errorf("storing an eval report: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE author_llm_run SET status = 'completed', paused_reason = NULL, finished_at = now(), stage = 'done'
			WHERE id = ?0 AND status = 'running'`, runID); err != nil {
			return fmt.Errorf("completing an eval run: %w", err)
		}
		return nil
	})
}

// LockAuthorLLMRunRunning locks a run's row for the rest of tx (step 2 of
// the lock order, authorLLMRunWrite) and reports whether the run is running.
// A pause or any other status change waits for tx to end, so the writes tx
// makes after this check are made for a running run — all of them, not the
// ones before a concurrent pause. The lock is exclusive among enqueuers: two
// drivers that both found a stage empty take turns, and the second sees the
// first's jobs when it checks again under the lock (a fingerprint job's
// output mode can change after its enqueue, so its unique key alone no longer
// tells a repeat from a new job).
func LockAuthorLLMRunRunning(ctx context.Context, tx *pg.Tx, runID int64) (bool, error) {
	if _, err := lockAuthorLLMRuns(ctx, tx, authorLLMRunWrite, false, []int64{runID}); err != nil {
		return false, err
	}
	var running bool
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&running),
		`SELECT status = 'running' FROM author_llm_run WHERE id = ?0`, runID); err != nil {
		return false, fmt.Errorf("reading an LLM run's status: %w", err)
	}
	return running, nil
}

// FailAuthorLLMRun ends a run that cannot go on (a stage its stored settings
// make impossible) as failed_systemic with a closed class, so it no longer
// holds the one active slot.
func FailAuthorLLMRun(ctx context.Context, db pg.DBI, runID int64, class string) error {
	if !ValidLeaseErrorClass(class) {
		return ErrInvalidLeaseFailure
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE author_llm_run SET status = 'failed_systemic', paused_reason = NULL, finished_at = now(),
			last_error_class = ?1
		WHERE id = ?0 AND status IN ('pending', 'running', 'paused')`, runID, class); err != nil {
		return fmt.Errorf("failing an LLM run: %w", err)
	}
	return nil
}

// AuthorLLMEvalSkip is one item an eval run left out, and why.
type AuthorLLMEvalSkip struct {
	Stage             string  `pg:"stage" json:"stage"`
	Reason            string  `pg:"reason" json:"reason"`
	SetVersion        *string `pg:"set_version" json:"set_version,omitempty"`
	SourceFingerprint []byte  `pg:"source_fingerprint" json:"-"`
	EvalItemID        *int64  `pg:"eval_item_id" json:"eval_item_id,omitempty"`
	BookID            *int64  `pg:"book_id" json:"book_id,omitempty"`
}

// RecordAuthorLLMEvalSkip records a skipped item once.
func RecordAuthorLLMEvalSkip(ctx context.Context, db pg.DBI, runID int64, skip *AuthorLLMEvalSkip) error {
	var fp interface{}
	if len(skip.SourceFingerprint) > 0 {
		fp = skip.SourceFingerprint
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO author_llm_eval_skip (run_id, stage, reason, set_version, source_fingerprint, eval_item_id, book_id)
		VALUES (?0, ?1, ?2, ?3, ?4, ?5, ?6) ON CONFLICT DO NOTHING`,
		runID, skip.Stage, skip.Reason, skip.SetVersion, fp, skip.EvalItemID, skip.BookID); err != nil {
		return fmt.Errorf("recording a skipped eval item: %w", err)
	}
	return nil
}

// AuthorLLMEvalSkips lists the skips a run's report shows: those it recorded
// and the freeze skips of the sets it measures, whoever froze them.
func AuthorLLMEvalSkips(ctx context.Context, db pg.DBI, runID int64, versions []string) ([]AuthorLLMEvalSkip, error) {
	if len(versions) == 0 {
		versions = []string{""}
	}
	var rows []AuthorLLMEvalSkip
	if _, err := db.QueryContext(ctx, &rows, `
		SELECT DISTINCT ON (stage, reason, set_version, source_fingerprint, eval_item_id, book_id)
			stage, reason, set_version, source_fingerprint, eval_item_id, book_id
		FROM author_llm_eval_skip
		WHERE run_id = ?0 OR set_version IN (?1)
		ORDER BY stage, reason, set_version, source_fingerprint, eval_item_id, book_id`, runID, pg.In(versions)); err != nil {
		return nil, fmt.Errorf("reading the eval skips: %w", err)
	}
	return rows, nil
}

// AuthorLLMEvalReport is a stored report: its text form and digest.
type AuthorLLMEvalReport struct {
	ID            int64     `pg:"id"`
	RunID         int64     `pg:"run_id"`
	ConfigVersion string    `pg:"config_version"`
	Report        string    `pg:"report"`
	ReportSHA256  []byte    `pg:"report_sha256"`
	CreatedAt     time.Time `pg:"created_at"`
}

// GetAuthorLLMEvalReport reads a run's report for download: the text whose
// SHA-256 is the stored digest.
func GetAuthorLLMEvalReport(ctx context.Context, db pg.DBI, runID int64) (*AuthorLLMEvalReport, error) {
	var r AuthorLLMEvalReport
	_, err := db.QueryOneContext(ctx, &r, `
		SELECT id, run_id, config_version, report::text AS report, report_sha256, created_at
		FROM author_llm_eval_report WHERE run_id = ?0`, runID)
	if err != nil {
		return nil, fmt.Errorf("reading an eval report: %w", err)
	}
	return &r, nil
}

// CreateAuthorLLMEvalRun records and starts an eval run at its first stage,
// with its settings. Starting is instant: the stages run in the background.
func CreateAuthorLLMEvalRun(ctx context.Context, db *pg.DB, version, mode string, settings []byte, createdBy *int64) (int64, error) {
	id, _, err := StartAuthorLLMEvalRun(ctx, db, version, mode, settings, createdBy)
	return id, err
}

// StartAuthorLLMEvalRun is an explicit start of an eval run: it creates the
// run, and in the same transaction lifts the participants' bad_request
// pauses of the configuration — a previous run's request the provider
// refused; the new run's fingerprint stage negotiates the request format
// again. Every other pause, a throttle and the endpoint's pause stay. It
// follows the lock order: the claim lock, then the new run, then the
// participants' state. It returns the run and how many pauses it lifted.
func StartAuthorLLMEvalRun(
	ctx context.Context, db *pg.DB, version, mode string, settings []byte, createdBy *int64,
) (id int64, resumed int, err error) {
	err = RunAuthorLLMTx(ctx, db, func(tx *pg.Tx) error {
		if _, lockErr := lockAuthorLLM(ctx, tx, nil); lockErr != nil {
			return lockErr
		}
		if _, insertErr := tx.QueryOneContext(ctx, pg.Scan(&id), `
			INSERT INTO author_llm_run (kind, mode, status, config_version, stage, settings, created_by_user_id,
				started_at)
			VALUES ('eval', ?0, 'running', ?1, 'freeze', ?2::jsonb, ?3, now())
			RETURNING id`, mode, version, string(settings), createdBy); insertErr != nil {
			return insertErr
		}
		resumed, err = liftRunBoundPauses(ctx, tx, version)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if pgErrorCode(err) == "23505" {
			return 0, 0, ErrAuthorLLMActiveRun
		}
		return 0, 0, fmt.Errorf("creating an eval run: %w", err)
	}
	return id, resumed, nil
}

// liftRunBoundPauses lifts the participant pauses a new run does not inherit:
// a rejected request format (the eval negotiates the mode again), and a
// drift or an unsettled re-learn (the run checks or learns its own
// fingerprints).
func liftRunBoundPauses(ctx context.Context, tx *pg.Tx, version string) (int, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE author_llm_provider_state SET paused_reason = NULL, paused_at = NULL
		WHERE config_version = ?0 AND paused_reason IN (?1)`, version, pg.In([]string{
		string(models.AuthorLLMPausedBadRequest), string(models.AuthorLLMPausedDrift),
		string(models.AuthorLLMPausedRelearnUnstable)}))
	if err != nil {
		return 0, fmt.Errorf("lifting the run-bound pauses: %w", err)
	}
	return res.RowsAffected(), nil
}

// AuthorLLMEvalRunWaits counts what an eval run's stage waits for besides
// its own jobs: any job of the run not finished (a re-learned participant's
// re-asked answers of an earlier stage), and any re-learn still running.
func AuthorLLMEvalRunWaits(ctx context.Context, db pg.DBI, runID int64) (int, error) {
	var n int
	if _, err := db.QueryOneContext(ctx, pg.Scan(&n), `
		SELECT (SELECT count(*) FROM author_llm_job WHERE run_id = ?0 AND status IN ('pending', 'claimed'))
			+ (SELECT count(*) FROM author_llm_relearn WHERE run_id = ?0 AND status = 'relearning')`, runID); err != nil {
		return 0, fmt.Errorf("counting what an eval run waits for: %w", err)
	}
	return n, nil
}
