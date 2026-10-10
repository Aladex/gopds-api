package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// State of the LLM author layer (migration 29): the configuration identity,
// runs, jobs, the participants' shared state and the excerpts. The claim and
// the settle live in author_llm_claim.go and author_llm_settle.go.
//
// Nothing here logs, and no error carries a row's content: an input, a reply
// or an excerpt can quote a name or a book.

var (
	// ErrAuthorLLMActiveRun marks a second active LLM run.
	ErrAuthorLLMActiveRun = errors.New("database: an LLM run is already active")
	// ErrAuthorLLMRunState marks a run transition its status does not allow.
	ErrAuthorLLMRunState = errors.New("database: the LLM run is not in a state that allows this")
	// ErrAuthorLLMConfigConflict marks a configuration row whose stored
	// identity differs from the one its version was computed from.
	ErrAuthorLLMConfigConflict = errors.New("database: stored LLM configuration differs from its version")
	// ErrAuthorLLMInvalidJob marks a job description the schema would refuse:
	// a job without a run, or a purpose that does not belong to its run.
	ErrAuthorLLMInvalidJob = errors.New("database: invalid LLM job")
	// ErrAuthorLLMRunEvidence marks a production run (resolve, swap) without
	// the learned model ids and fingerprint references of the production
	// pair: only an eval run may learn.
	ErrAuthorLLMRunEvidence = errors.New("database: a production LLM run needs its eval evidence")
)

// EnsureAuthorLLMConfig stores a configuration identity under its version,
// with its four participants and their shared state (the AIMD window starts at
// concurrencyStart). Idempotent: a version already stored is checked to hold
// the same identity and left as it is.
func EnsureAuthorLLMConfig(ctx context.Context, db *pg.DB, id *llmreq.Identity, concurrencyStart int) (string, error) {
	if concurrencyStart < 1 {
		return "", fmt.Errorf("database: concurrency start must be positive, got %d", concurrencyStart)
	}
	version := id.Version()
	identity := string(id.CanonicalJSON())
	err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO author_llm_config
				(version, base_url, identity, prompt_sha256, schema_sha256, validator_version, tokenizer_version,
				 context_version)
			VALUES (?0, ?1, ?2::jsonb, ?3, ?4, ?5, ?6, ?7)
			ON CONFLICT (version) DO NOTHING`,
			version, id.BaseURL, identity, id.PromptSHA256, id.SchemaSHA256, id.ValidatorVersion,
			id.TokenizerVersion, id.ContextVersion); err != nil {
			return fmt.Errorf("storing the LLM configuration: %w", err)
		}
		var same bool
		if _, err := tx.QueryOneContext(ctx, pg.Scan(&same),
			`SELECT identity = ?1::jsonb FROM author_llm_config WHERE version = ?0`, version, identity); err != nil {
			return fmt.Errorf("reading the LLM configuration: %w", err)
		}
		if !same {
			return ErrAuthorLLMConfigConflict
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO author_llm_endpoint_state (base_url) VALUES (?0) ON CONFLICT (base_url) DO NOTHING`,
			id.BaseURL); err != nil {
			return fmt.Errorf("storing the LLM endpoint state: %w", err)
		}
		for _, p := range id.Participants {
			params, err := json.Marshal(struct {
				Temperature     *float64 `json:"temperature,omitempty"`
				ReasoningEffort string   `json:"reasoning_effort,omitempty"`
			}{p.Temperature, p.ReasoningEffort})
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO author_llm_participant (config_version, slot, model, params)
				VALUES (?0, ?1, ?2, ?3::jsonb)
				ON CONFLICT (config_version, slot) DO NOTHING`,
				version, string(p.Slot), p.Model, string(params)); err != nil {
				return fmt.Errorf("storing an LLM participant: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO author_llm_provider_state (config_version, slot, concurrency)
				VALUES (?0, ?1, ?2)
				ON CONFLICT (config_version, slot) DO NOTHING`,
				version, string(p.Slot), concurrencyStart); err != nil {
				return fmt.Errorf("storing an LLM participant state: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return version, nil
}

// AuthorLLMFingerprintReference is what a fingerprint check compares with,
// measured by the eval run: the prompt tokens of the fixed request, and for a
// reasoning participant the floor of its reasoning (or completion) tokens.
type AuthorLLMFingerprintReference struct {
	PromptTokens   int64 `json:"prompt_tokens"`
	ReasoningFloor int64 `json:"reasoning_floor,omitempty"`
	// OutputMode is the mode the reference was measured in: the schema or
	// the tool definition is part of the prompt the tokens count.
	OutputMode string `json:"output_mode"`
}

// AuthorLLMRunSpec describes a new run.
type AuthorLLMRunSpec struct {
	Kind           models.AuthorLLMRunKind
	Mode           string
	ConfigVersion  string
	EstimateTokens int64
	// ExpectedModels is per slot the model ids V0 accepts; nil while the
	// run learns them (the eval fingerprint stage).
	ExpectedModels map[llmreq.Slot][]string
	// Reference is per slot the fingerprint reference. A production run
	// (resolve, swap) must carry both for the production pair; an eval run
	// may carry none.
	Reference map[llmreq.Slot]AuthorLLMFingerprintReference
	CreatedBy *int64
}

// nullableJSON marshals v, or returns nil for a nil map.
func nullableJSON[M ~map[llmreq.Slot]V, V any](m M) (*string, error) {
	if m == nil {
		return nil, nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	s := string(raw)
	return &s, nil
}

// CreateAuthorLLMRun records a pending run. The one-active-run index refuses
// a second active run with ErrAuthorLLMActiveRun.
func CreateAuthorLLMRun(ctx context.Context, db pg.DBI, spec *AuthorLLMRunSpec) (int64, error) {
	expected, err := nullableJSON(spec.ExpectedModels)
	if err != nil {
		return 0, err
	}
	reference, err := nullableJSON(spec.Reference)
	if err != nil {
		return 0, err
	}
	var id int64
	_, err = db.QueryOneContext(ctx, pg.Scan(&id), `
		INSERT INTO author_llm_run (kind, mode, config_version, estimate_tokens, expected_models, reference,
			created_by_user_id)
		VALUES (?0, ?1, ?2, ?3, ?4::jsonb, ?5::jsonb, ?6)
		RETURNING id`,
		string(spec.Kind), spec.Mode, spec.ConfigVersion, spec.EstimateTokens, expected, reference, spec.CreatedBy)
	if err != nil {
		switch pgErrorCode(err) {
		case "23505":
			return 0, ErrAuthorLLMActiveRun
		case "23514":
			return 0, ErrAuthorLLMRunEvidence
		}
		return 0, fmt.Errorf("creating an LLM run: %w", err)
	}
	return id, nil
}

// pgErrorCode is the SQLSTATE of a PostgreSQL error, empty otherwise.
func pgErrorCode(err error) string {
	var pgErr pg.Error
	if errors.As(err, &pgErr) {
		return pgErr.Field('C')
	}
	return ""
}

// transitionAuthorLLMRun moves a run between statuses, refusing a move its
// current status does not allow.
func transitionAuthorLLMRun(ctx context.Context, db pg.DBI, id int64, set string, from ...models.AuthorLLMRunStatus) error {
	statuses := make([]string, len(from))
	for i, s := range from {
		statuses[i] = string(s)
	}
	res, err := db.ExecContext(ctx, `UPDATE author_llm_run SET `+set+` WHERE id = ?0 AND status IN (?1)`,
		id, pg.In(statuses))
	if err != nil {
		return fmt.Errorf("changing an LLM run: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrAuthorLLMRunState
	}
	return nil
}

// StartAuthorLLMRun moves a pending run to running.
func StartAuthorLLMRun(ctx context.Context, db pg.DBI, id int64) error {
	return transitionAuthorLLMRun(ctx, db, id, `status = 'running', started_at = coalesce(started_at, now())`,
		models.AuthorLLMRunPending)
}

// PauseAuthorLLMRun pauses a running run. Calls already sent are still
// settled; nothing new is claimed for the run.
func PauseAuthorLLMRun(ctx context.Context, db pg.DBI, id int64, reason models.AuthorLLMRunPauseReason) error {
	return transitionAuthorLLMRun(ctx, db, id,
		fmt.Sprintf(`status = 'paused', paused_reason = '%s'`, pauseReasonLiteral(reason)),
		models.AuthorLLMRunRunning)
}

// pauseReasonLiteral keeps the spliced reason inside its closed set.
func pauseReasonLiteral(reason models.AuthorLLMRunPauseReason) string {
	if reason == models.AuthorLLMRunPausedBudget {
		return string(models.AuthorLLMRunPausedBudget)
	}
	return string(models.AuthorLLMRunPausedAdmin)
}

// ResumeAuthorLLMRun continues a paused run.
func ResumeAuthorLLMRun(ctx context.Context, db pg.DBI, id int64) error {
	return transitionAuthorLLMRun(ctx, db, id, `status = 'running', paused_reason = NULL`, models.AuthorLLMRunPaused)
}

// CompleteAuthorLLMRun ends a running or paused run.
func CompleteAuthorLLMRun(ctx context.Context, db pg.DBI, id int64) error {
	return transitionAuthorLLMRun(ctx, db, id, `status = 'completed', paused_reason = NULL, finished_at = now()`,
		models.AuthorLLMRunRunning, models.AuthorLLMRunPaused)
}

// GetAuthorLLMRun reads one run.
func GetAuthorLLMRun(ctx context.Context, db pg.DBI, id int64) (*models.AuthorLLMRun, error) {
	var run models.AuthorLLMRun
	if _, err := db.QueryOneContext(ctx, &run, `SELECT * FROM author_llm_run WHERE id = ?`, id); err != nil {
		return nil, fmt.Errorf("reading an LLM run: %w", err)
	}
	return &run, nil
}

// AuthorLLMDefaultOutputMode is the output mode of a job or a check that
// names none: strict Structured Outputs.
const AuthorLLMDefaultOutputMode = "json_schema"

// AuthorLLMNewJob describes one job to enqueue.
type AuthorLLMNewJob struct {
	ConfigVersion string
	Slot          llmreq.Slot
	// RunID is required: every job belongs to a run.
	RunID             *int64
	Purpose           models.AuthorLLMJobPurpose
	SourceFingerprint []byte
	ExtractorVersion  string
	EvalItemID        *int64
	Arm               string
	RepeatNo          int
	BatchSize         int
	OutputMode        string
	Input             llmreq.ItemInput
	WithContext       bool
}

// EnqueueAuthorLLMJobs inserts jobs, skipping any whose key already has one.
// It returns how many were new.
func EnqueueAuthorLLMJobs(ctx context.Context, db pg.DBI, jobs []AuthorLLMNewJob) (int, error) {
	inserted := 0
	for i := range jobs {
		j := &jobs[i]
		if !j.Slot.Valid() || j.BatchSize < 0 || j.RunID == nil {
			return inserted, ErrAuthorLLMInvalidJob
		}
		input, err := json.Marshal(j.Input)
		if err != nil {
			return inserted, err
		}
		batch := j.BatchSize
		if batch == 0 {
			batch = 1
		}
		mode := j.OutputMode
		if mode == "" {
			mode = AuthorLLMDefaultOutputMode
		}
		var fingerprint, extractor, arm interface{}
		if len(j.SourceFingerprint) > 0 {
			fingerprint = j.SourceFingerprint
		}
		if j.ExtractorVersion != "" {
			extractor = j.ExtractorVersion
		}
		if j.Arm != "" {
			arm = j.Arm
		}
		res, err := db.ExecContext(ctx, `
			INSERT INTO author_llm_job (config_version, slot, run_id, purpose, source_fingerprint, extractor_version,
				eval_item_id, arm, repeat_no, batch_size, output_mode, input, with_context)
			VALUES (?0, ?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11::jsonb, ?12)
			ON CONFLICT DO NOTHING`,
			j.ConfigVersion, string(j.Slot), j.RunID, string(j.Purpose), fingerprint, extractor,
			j.EvalItemID, arm, j.RepeatNo, batch, mode, string(input), j.WithContext)
		if err != nil {
			if code := pgErrorCode(err); code == "23514" || code == "23503" {
				return inserted, fmt.Errorf("%w: %s", ErrAuthorLLMInvalidJob, code)
			}
			return inserted, fmt.Errorf("enqueuing an LLM job: %w", err)
		}
		inserted += res.RowsAffected()
	}
	return inserted, nil
}

// GetAuthorLLMProviderState reads one participant's shared state.
func GetAuthorLLMProviderState(ctx context.Context, db pg.DBI, version string, slot llmreq.Slot) (*models.AuthorLLMProviderState, error) {
	var st models.AuthorLLMProviderState
	if _, err := db.QueryOneContext(ctx, &st,
		`SELECT * FROM author_llm_provider_state WHERE config_version = ?0 AND slot = ?1`, version, string(slot)); err != nil {
		return nil, fmt.Errorf("reading an LLM participant state: %w", err)
	}
	return &st, nil
}

// ResumeAuthorLLMParticipant lifts a participant's pause (the admin's
// "continue" after fixing the cause).
func ResumeAuthorLLMParticipant(ctx context.Context, db pg.DBI, version string, slot llmreq.Slot) error {
	_, err := db.ExecContext(ctx, `
		UPDATE author_llm_provider_state SET paused_reason = NULL, paused_at = NULL
		WHERE config_version = ?0 AND slot = ?1`, version, string(slot))
	if err != nil {
		return fmt.Errorf("resuming an LLM participant: %w", err)
	}
	return nil
}

// AuthorLLMEndpointState is the endpoint's own state.
type AuthorLLMEndpointState struct {
	BaseURL      string     `pg:"base_url"`
	PausedReason *string    `pg:"paused_reason"`
	PausedAt     *time.Time `pg:"paused_at"`
}

// GetAuthorLLMEndpointState reads the state of a configuration's endpoint.
func GetAuthorLLMEndpointState(ctx context.Context, db pg.DBI, version string) (*AuthorLLMEndpointState, error) {
	var st AuthorLLMEndpointState
	if _, err := db.QueryOneContext(ctx, &st, `
		SELECT e.base_url, e.paused_reason, e.paused_at FROM author_llm_endpoint_state e
		JOIN author_llm_config c ON c.base_url = e.base_url WHERE c.version = ?0`, version); err != nil {
		return nil, fmt.Errorf("reading the LLM endpoint state: %w", err)
	}
	return &st, nil
}

// ResumeAuthorLLMEndpoint lifts the pause of a configuration's endpoint (the
// admin's "continue" once the quota is back), for every configuration there.
func ResumeAuthorLLMEndpoint(ctx context.Context, db pg.DBI, version string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE author_llm_endpoint_state SET paused_reason = NULL, paused_at = NULL
		WHERE base_url = (SELECT base_url FROM author_llm_config WHERE version = ?0)`, version)
	if err != nil {
		return fmt.Errorf("resuming an LLM endpoint: %w", err)
	}
	return nil
}

// AuthorLLMContextBook is a book an excerpt for a fingerprint may be read
// from, in the order they are tried.
type AuthorLLMContextBook struct {
	BookID      int64  `pg:"book_id"`
	BookMD5     string `pg:"book_md5"`
	ArchivePath string `pg:"archive_path"`
	EntryName   string `pg:"entry_name"`
	// CreditDisplay is the credit's own source display name, left out of
	// the excerpt's other credits.
	CreditDisplay string `pg:"source_display_name"`
}

// authorLLMContextCandidates bounds how many books are tried for one excerpt.
const authorLLMContextCandidates = 5

// AuthorLLMContextBooks lists the books a fingerprint's excerpt may come from:
// current snapshots of the extractor version carrying an author credit with
// the fingerprint, lowest book id first.
func AuthorLLMContextBooks(ctx context.Context, db pg.DBI, fingerprint []byte, extractor string) ([]AuthorLLMContextBook, error) {
	var books []AuthorLLMContextBook
	_, err := db.QueryContext(ctx, &books, `
		SELECT DISTINCT ON (s.book_id) s.book_id, s.book_md5, s.archive_path, s.entry_name, c.source_display_name
		FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id
		WHERE c.source_fingerprint = ?0 AND c.role = 'author' AND s.is_current AND s.extractor_version = ?1
		ORDER BY s.book_id, c.id
		LIMIT ?2`, fingerprint, extractor, authorLLMContextCandidates)
	if err != nil {
		return nil, fmt.Errorf("listing excerpt books: %w", err)
	}
	return books, nil
}

// AuthorLLMContextBookByID is the excerpt book of an eval item: the frozen
// book, if its current snapshot is still the frozen file, with the display
// name of the item's own credit in it — left out of the excerpt exactly as a
// review job's is.
func AuthorLLMContextBookByID(ctx context.Context, db pg.DBI, bookID int64, md5 string, fingerprint []byte) (*AuthorLLMContextBook, error) {
	var book AuthorLLMContextBook
	_, err := db.QueryOneContext(ctx, &book, `
		SELECT s.book_id, s.book_md5, s.archive_path, s.entry_name, c.source_display_name
		FROM book_metadata_snapshot s
		JOIN book_contributor_credit c ON c.snapshot_id = s.id AND c.role = 'author' AND c.source_fingerprint = ?2
		WHERE s.book_id = ?0 AND s.book_md5 = ?1 AND s.is_current
		ORDER BY c.id
		LIMIT 1`, bookID, md5, fingerprint)
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading an excerpt book: %w", err)
	}
	return &book, nil
}

// AuthorLLMContextKey identifies an excerpt: one file version of a book,
// read by one reader version, leaving out one credit (by the SHA-256 of its
// display name).
type AuthorLLMContextKey struct {
	BookID        int64
	BookMD5       string
	Version       string
	ExcludeSHA256 []byte
}

// StoreAuthorLLMContext stores an excerpt; the first writer of a key wins and
// a later one with the same key keeps it. It returns the stored digest.
func StoreAuthorLLMContext(ctx context.Context, db pg.DBI, key *AuthorLLMContextKey, payload, sha []byte) ([]byte, error) {
	var stored []byte
	_, err := db.QueryOneContext(ctx, pg.Scan(&stored), `
		WITH ins AS (
			INSERT INTO author_llm_context (book_id, book_md5, context_version, exclude_sha256, payload, sha256)
			VALUES (?0, ?1, ?2, ?3, ?4::jsonb, ?5)
			ON CONFLICT (book_id, book_md5, context_version, exclude_sha256) DO NOTHING
			RETURNING sha256)
		SELECT sha256 FROM ins
		UNION ALL
		SELECT sha256 FROM author_llm_context
		WHERE book_id = ?0 AND book_md5 = ?1 AND context_version = ?2 AND exclude_sha256 = ?3
		LIMIT 1`, key.BookID, key.BookMD5, key.Version, key.ExcludeSHA256, string(payload), sha)
	if err != nil {
		return nil, fmt.Errorf("storing an excerpt: %w", err)
	}
	return stored, nil
}

// LoadAuthorLLMContext reads an excerpt's payload by digest; nil when it was
// purged or never stored.
func LoadAuthorLLMContext(ctx context.Context, db pg.DBI, sha []byte) ([]byte, error) {
	var payload *string
	_, err := db.QueryOneContext(ctx, pg.Scan(&payload),
		`SELECT payload::text FROM author_llm_context WHERE sha256 = ?0 AND payload IS NOT NULL LIMIT 1`, sha)
	if errors.Is(err, pg.ErrNoRows) || payload == nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading an excerpt: %w", err)
	}
	return []byte(*payload), nil
}

// SetAuthorLLMJobContext records what became of a claimed job's excerpt. It
// is fenced by the call that holds the job.
func SetAuthorLLMJobContext(
	ctx context.Context, db pg.DBI, jobID, callID int64, state models.AuthorLLMContextState, bookID *int64, sha []byte,
) error {
	var shaParam interface{}
	if len(sha) > 0 {
		shaParam = sha
	}
	res, err := db.ExecContext(ctx, `
		UPDATE author_llm_job SET context_state = ?2, context_book_id = ?3, context_sha256 = ?4
		WHERE id = ?0 AND call_id = ?1 AND status = 'claimed'`,
		jobID, callID, string(state), bookID, shaParam)
	if err != nil {
		return fmt.Errorf("recording a job's excerpt: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrAuthorLLMLeaseLost
	}
	return nil
}

// FoldAuthorLLMTokenTally folds the token deltas into one row per run. The
// sums are the same before and after; rows a concurrent writer has not
// committed yet are left for the next fold.
func FoldAuthorLLMTokenTally(ctx context.Context, db pg.DBI) error {
	_, err := db.ExecContext(ctx, `
		WITH gone AS (DELETE FROM author_llm_token_tally RETURNING run_id, tokens)
		INSERT INTO author_llm_token_tally (run_id, tokens)
		SELECT run_id, sum(tokens) FROM gone GROUP BY run_id
		HAVING sum(tokens) <> 0`)
	if err != nil {
		return fmt.Errorf("folding the LLM token tally: %w", err)
	}
	return nil
}

// AuthorLLMTokensSpent is what the calls of one run spent, the reserves of
// its open calls included.
func AuthorLLMTokensSpent(ctx context.Context, db pg.DBI, runID int64) (int64, error) {
	var spent int64
	if _, err := db.QueryOneContext(ctx, pg.Scan(&spent),
		`SELECT coalesce(sum(tokens), 0) FROM author_llm_token_tally WHERE run_id = ?0`, runID); err != nil {
		return 0, fmt.Errorf("reading LLM tokens spent: %w", err)
	}
	return spent, nil
}
