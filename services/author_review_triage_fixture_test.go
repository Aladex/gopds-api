package services

// Fixtures of the review triage tests: open review items of real sources
// resolved through the real repositories, the stored eval answers of the
// judges and the production pair, and pins, all in a scratch database.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/internal/authornorm/llmres"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/require"
)

type triageEnv struct {
	t    *testing.T
	db   *pg.DB
	ctx  context.Context
	book int64
	// llm holds the config version and run of the stored answers, once
	// seeded.
	llmConfig string
	runs      map[int64]bool
}

func newTriageEnv(t *testing.T) *triageEnv {
	t.Helper()
	return &triageEnv{t: t, db: localWorkerDB(t), ctx: context.Background(), runs: map[int64]bool{}}
}

func (e *triageEnv) exec(query string, params ...interface{}) {
	e.t.Helper()
	_, err := e.db.Exec(query, params...)
	require.NoError(e.t, err)
}

func (e *triageEnv) count(query string, params ...interface{}) int {
	e.t.Helper()
	return countOf(e.t, e.db, query, params...)
}

// triageSeed is one seeded source: its fingerprint, its credit and the open
// review item its resolution opened (0 when it opened none).
type triageSeed struct {
	item, credit int64
	fp           []byte
	source       authornorm.SourceValue
	class        authornorm.DecisionClass
}

// seed writes one book with one current author credit of the source, its
// stored local result, and the credit's resolution under the shipped
// policy, all committed.
func (e *triageEnv) seed(pairs ...string) triageSeed {
	e.t.Helper()
	src := source(e.t, pairs...)
	fp := authornorm.SourceFingerprint(src)
	e.book++
	book := e.book
	tx, err := e.db.Begin()
	require.NoError(e.t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`INSERT INTO opds_catalog_book
		(id, filename, path, format, registerdate, docdate, lang, title, annotation, md5)
		VALUES (?, ?, 'fixture.zip', 'fb2', now(), '', 'ru', 'fixture', '', ?)`, book, "book.fb2", md5hex(book))
	require.NoError(e.t, err)
	written, err := database.PersistExtraction(tx, &database.ExtractionInput{
		BookID: book, BookMD5: md5hex(book),
		ExtractorVersion: reviewExtractor, NormalizerVersion: authornorm.NormalizerVersion,
		Origin: models.BookMetadataSnapshotLive, Outcome: models.BookMetadataSnapshotExtracted,
		ArchivePath: "fixture.zip", EntryName: "book.fb2",
		XMLProvenance: []byte(`{}`), SourceISBNs: []string{},
		SourceSequences: []byte(`[]`), QualityFlags: []string{},
		Credits: []database.ExtractionCredit{{Role: models.ContributorRoleAuthor, Source: src}},
	})
	require.NoError(e.t, err)
	var credit int64
	_, err = tx.QueryOne(pg.Scan(&credit), `SELECT id FROM book_contributor_credit WHERE snapshot_id = ?`,
		written.SnapshotID)
	require.NoError(e.t, err)

	result, err := authornorm.Normalize(src, reviewExtractor)
	require.NoError(e.t, err)
	var resultID int64
	_, err = tx.QueryOne(pg.Scan(&resultID), `SELECT id FROM contributor_normalization_result
		WHERE normalization_key = ?`, result.NormalizationKey[:])
	if err != nil {
		resultID, err = database.InsertLocalResult(e.ctx, tx, &result)
		require.NoError(e.t, err)
		_, err = tx.Exec(`UPDATE contributor_normalization_job
			SET status = 'completed', result_id = ?, finished_at = now()
			WHERE normalization_key = ? AND status <> 'completed'`, resultID, result.NormalizationKey[:])
		require.NoError(e.t, err)
	}
	policy, err := database.LoadAcceptancePolicy(e.ctx, tx, 1, authornorm.NormalizerVersion)
	require.NoError(e.t, err)
	decision, err := policy.Decide(&result)
	require.NoError(e.t, err)
	_, err = database.ResolveCredits(e.ctx, tx, fp[:], reviewExtractor, &database.AutomaticOutcome{
		ResultID: resultID, DecisionClass: result.DecisionClass, Decision: decision,
	})
	require.NoError(e.t, err)
	var items []int64
	_, err = tx.Query(&items, `SELECT id FROM contributor_review_item WHERE scope_fingerprint = ? AND status = 'open'`,
		fp[:])
	require.NoError(e.t, err)
	require.NoError(e.t, tx.Commit())
	s := triageSeed{credit: credit, fp: fp[:], source: src, class: result.DecisionClass}
	if len(items) > 0 {
		s.item = items[0]
	}
	return s
}

// seedOpen is seed of a source that must open a review item of class.
func (e *triageEnv) seedOpen(class authornorm.DecisionClass, pairs ...string) triageSeed {
	e.t.Helper()
	s := e.seed(pairs...)
	require.Equal(e.t, class, s.class, "the fixture source %v has another class", pairs)
	require.NotZero(e.t, s.item, "the fixture source %v opened no review item", pairs)
	return s
}

// seedSelected is seed of a person source the admin selected as it is
// structured: a selected credit the rules read the given names of.
func (e *triageEnv) seedSelected(pairs ...string) {
	e.t.Helper()
	s := e.seed(pairs...)
	r, err := authornorm.Normalize(s.source, reviewExtractor)
	require.NoError(e.t, err)
	_, err = reviewService(e.t, e.db, DefaultAuthorMetadataReviewConfig()).ApplyOverride(e.ctx,
		database.OverrideScope{Fingerprint: s.fp}, 7, &AdminCorrection{GivenName: r.GivenName,
			FamilyName: r.FamilyName, DisplayName: r.DisplayName, SortName: r.SortName, SearchKey: r.SearchKey,
			Script: string(r.Script),
			Kind:   models.NormalizationPerson})
	require.NoError(e.t, err)
	require.Equal(e.t, models.CreditSelectionSelected, selectionOf(e.t, e.db, s.credit).State)
}

// personReply is a stored reply that reads the source's words in order as
// the roles given.
func personReply(roles ...llmres.Role) *llmres.Reply {
	rep := &llmres.Reply{Form: llmres.FormPerson, Roles: []llmres.RoleAssignment{}, CaseFix: []llmres.CaseFix{}}
	for i, r := range roles {
		rep.Roles = append(rep.Roles, llmres.RoleAssignment{I: i, Role: r})
	}
	return rep
}

// formReply is a stored reply of a form without roles.
func formReply(form llmres.Form) *llmres.Reply {
	return &llmres.Reply{Form: form, Roles: []llmres.RoleAssignment{}, CaseFix: []llmres.CaseFix{}}
}

// answer is one slot's stored reply; canonical overrides the reply's own
// canonical form when set (a reply that no longer validates still has the
// canonical it was stored with).
type answer struct {
	slot      string
	reply     llmres.Reply
	raw       string
	canonical []byte
}

func answersOf(reply *llmres.Reply, slots ...string) []answer {
	out := make([]answer, 0, len(slots))
	for _, s := range slots {
		out = append(out, answer{slot: s, reply: *reply})
	}
	return out
}

var allSlots = []string{"production_a", "production_b", "judge_a", "judge_b"}

// storeAnswers writes an eval item of the seed's input in run (a new eval
// run per run number) and one answered first-repeat job per answer.
func (e *triageEnv) storeAnswers(s *triageSeed, run int64, answers ...answer) {
	e.t.Helper()
	runID := e.llmRun(run)
	fields := llmres.FieldValuesOf(s.source)
	input, err := json.Marshal(llmreq.ItemInput{Fields: fields, Script: "Cyrl", Flags: []string{}})
	require.NoError(e.t, err)
	var item int64
	_, err = e.db.QueryOne(pg.Scan(&item), `
		INSERT INTO author_llm_eval_item (set_version, item_set, stratum, source_fingerprint, extractor_version,
			input, weight)
		VALUES (?, 'holdout', 'review.triage', ?, ?, ?::jsonb, 1) RETURNING id`,
		fmt.Sprintf("v1.s.%d", run), s.fp, reviewExtractor, string(input))
	require.NoError(e.t, err)
	for i := range answers {
		a := &answers[i]
		reply := a.raw
		if reply == "" {
			raw, marshalErr := json.Marshal(a.reply)
			require.NoError(e.t, marshalErr)
			reply = string(raw)
		}
		canonical := a.canonical
		if canonical == nil {
			sum := sha256.Sum256(a.reply.CanonicalBytes())
			canonical = sum[:]
		}
		refused := a.reply.Refused()
		var tier interface{}
		if !refused {
			tier = "confirm"
		}
		purpose := "eval_judge"
		if strings.HasPrefix(a.slot, "production") {
			purpose = "eval_candidate"
		}
		var job int64
		_, err = e.db.QueryOne(pg.Scan(&job), `
			INSERT INTO author_llm_job (config_version, slot, run_id, purpose, eval_item_id, input)
			VALUES (?, ?, ?, ?, ?, ?::jsonb) RETURNING id`, e.llmConfig, a.slot, runID, purpose, item, string(input))
		require.NoError(e.t, err)
		var call int64
		_, err = e.db.QueryOne(pg.Scan(&call), `
			INSERT INTO author_llm_call (config_version, slot, run_id, kind, items, output_mode, prompt_bound_bytes,
				output_ceiling, reserved_tokens, settled_tokens, finished_at, outcome)
			VALUES (?, ?, ?, 'jobs', 1, 'json_schema', 100, 10, 20, 0, now(), 'answered') RETURNING id`,
			e.llmConfig, a.slot, runID)
		require.NoError(e.t, err)
		var attempt int64
		_, err = e.db.QueryOne(pg.Scan(&attempt), `
			INSERT INTO author_llm_attempt (job_id, attempt_no, call_id, item_key, finished_at, outcome, reply,
				canonical_sha256, tier, refused)
			VALUES (?, 1, ?, '0', now(), 'answered', ?::jsonb, ?, ?, ?) RETURNING id`,
			job, call, reply, canonical, tier, refused)
		require.NoError(e.t, err)
		e.exec(`UPDATE author_llm_job SET status = 'answered', answer_attempt_id = ?, attempt_count = 1,
			finished_at = now() WHERE id = ?`, attempt, job)
	}
}

// llmRun is the eval run of run number n, made on first use, with the
// configuration and its four participants.
func (e *triageEnv) llmRun(n int64) int64 {
	e.t.Helper()
	if e.llmConfig == "" {
		e.llmConfig = "llm1-" + strings.Repeat("b", 64)
		e.exec(`INSERT INTO author_llm_config (version, base_url, identity, prompt_sha256, schema_sha256,
			validator_version, tokenizer_version, context_version)
			VALUES (?, 'http://llm.local/v1', '{"task":"triage"}', ?, ?, 'v', 't', 'c')`,
			e.llmConfig, strings.Repeat("c", 64), strings.Repeat("d", 64))
		for _, slot := range allSlots {
			e.exec(`INSERT INTO author_llm_participant (config_version, slot, model) VALUES (?, ?, ?)`,
				e.llmConfig, slot, "model-"+slot)
		}
	}
	var id int64
	_, err := e.db.QueryOne(pg.Scan(&id), `SELECT id FROM author_llm_run WHERE reference ->> 'n' = ?`,
		fmt.Sprint(n))
	if err == nil {
		return id
	}
	_, err = e.db.QueryOne(pg.Scan(&id), `
		INSERT INTO author_llm_run (kind, mode, status, config_version, reference, finished_at)
		VALUES ('eval', 'dev', 'completed', ?, jsonb_build_object('n', ?::text), now()) RETURNING id`,
		e.llmConfig, fmt.Sprint(n))
	require.NoError(e.t, err)
	e.exec(`INSERT INTO author_llm_eval_set (set_version, sha256, items, created_by_run_id)
		VALUES (?, sha256(convert_to(?, 'UTF8')), 1, ?)`, fmt.Sprintf("v1.s.%d", n), fmt.Sprint(n), id)
	return id
}

// pinByName assigns the seed's fingerprint to a new entity whose name is
// pinned.
func (e *triageEnv) pinByName(s *triageSeed) {
	e.t.Helper()
	var entity string
	_, err := e.db.QueryOne(pg.Scan(&entity), `INSERT INTO author_entity (id, kind, display_name, sort_name,
		search_key, name_pinned) VALUES (gen_random_uuid(), 'person', 'Имя', 'Имя', 'имя', true) RETURNING id`)
	require.NoError(e.t, err)
	e.exec(`INSERT INTO author_entity_assignment (source_fingerprint, extractor_version, entity_id, method, rule)
		VALUES (?, ?, ?, 'deterministic', 'same_name')`, s.fp, reviewExtractor, entity)
}

// rowsJSON is every row of the table as JSON, in a fixed order: a
// before-and-after witness that nothing in it changed.
func (e *triageEnv) rowsJSON(table string) string {
	e.t.Helper()
	var out string
	_, err := e.db.QueryOne(pg.Scan(&out), `SELECT coalesce(string_agg(to_jsonb(x)::text, E'\n' ORDER BY x::text), '')
		FROM `+table+` x`)
	require.NoError(e.t, err)
	return out
}

// authorLayerTables are the tables a dry run must never write.
var authorLayerTables = []string{"contributor_manual_override", "book_contributor_credit_selection",
	"contributor_review_item", "contributor_normalization_result", "book_contributor_credit"}

func (e *triageEnv) authorLayer() map[string]string {
	out := map[string]string{}
	for _, table := range authorLayerTables {
		out[table] = e.rowsJSON(table)
	}
	return out
}

// planOnce runs a dry run to its plan with a loop of the given config.
func (e *triageEnv) planOnce(loop *AuthorReviewTriageLoop) *database.TriageBatch {
	e.t.Helper()
	batch, err := NewAuthorReviewTriageAdmin(e.db).StartDryRun(e.ctx, 7)
	require.NoError(e.t, err)
	worked, err := loop.RunOnce(e.ctx)
	require.NoError(e.t, err)
	require.True(e.t, worked)
	view, err := database.ReadTriageBatch(e.ctx, e.db, batch.ID)
	require.NoError(e.t, err)
	require.Equal(e.t, database.TriageBatchPlanned, view.Batch.Status)
	return &view.Batch
}

func (e *triageEnv) loop(cfg AuthorReviewTriageConfig) *AuthorReviewTriageLoop {
	return NewAuthorReviewTriageLoop(e.db, reviewService(e.t, e.db, DefaultAuthorMetadataReviewConfig()), cfg)
}

// triageRow is one stored plan row of a batch.
type triageRow struct {
	ID           int64  `pg:"id"`
	ReviewItemID int64  `pg:"review_item_id"`
	Step         int16  `pg:"step"`
	Rule         string `pg:"rule"`
	Action       string `pg:"action"`
	Exclusion    string `pg:"exclusion"`
	Outcome      string `pg:"outcome"`
	Planned      string `pg:"planned"`
	Evidence     string `pg:"evidence"`
	UndoOutcome  string `pg:"undo_outcome"`
	ResultID     int64  `pg:"result_id"`
	OverrideID   int64  `pg:"override_id"`
}

// rowsOf reads the batch's plan rows by review item.
func (e *triageEnv) rowsOf(batchID int64) map[int64]triageRow {
	e.t.Helper()
	var rows []triageRow
	_, err := e.db.Query(&rows, `SELECT id, review_item_id, coalesce(step, 0) AS step, coalesce(rule, '') AS rule,
		coalesce(action, '') AS action, coalesce(exclusion, '') AS exclusion, outcome,
		coalesce(planned::text, '') AS planned, coalesce(evidence::text, '') AS evidence,
		coalesce(undo_outcome, '') AS undo_outcome,
		coalesce(result_id, 0) AS result_id, coalesce(override_id, 0) AS override_id
		FROM author_review_triage_item WHERE batch_id = ?`, batchID)
	require.NoError(e.t, err)
	out := map[int64]triageRow{}
	for i := range rows {
		out[rows[i].ReviewItemID] = rows[i]
	}
	return out
}
