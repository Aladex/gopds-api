package services_test

import (
	"archive/zip"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/config"
	"gopds-api/database"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/internal/authornorm/llmres"
	"gopds-api/internal/scanfixture"
	"gopds-api/llm"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Privacy of the LLM layer (design K, section 6): names and excerpts go to
// the configured endpoint and into PostgreSQL, never into the log — on the
// success path and on every error path, including an endpoint that echoes the
// request back in its error.

const (
	nameCanary    = "Qzcanaryx"
	excerptCanary = "BODYCANARY7731"
	keyCanary     = "sk-KEYCANARY-5501"
)

// canaryBook is a Latin-script FB2 whose author and body carry the canaries.
func canaryBook(now time.Time) []byte {
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description>
<title-info>
<genre>sf</genre>
<author><first-name>%[1]s</first-name><last-name>Smith</last-name></author>
<book-title>Canary Book</book-title>
<lang>en</lang>
</title-info>
<document-info><date>%[3]d</date><id>llm-canary</id></document-info>
</description>
<body><section><p>%[1]s Smith wrote this. %[2]s is a word only this body has, and the clocks were striking.</p></section></body>
</FictionBook>
`, nameCanary, excerptCanary, now.Year()))
}

// ingestCanaryBook writes the canary book into the archives directory and
// scans it; it returns the author credit's fingerprint and extractor version.
func ingestCanaryBook(t *testing.T, e *llmEnv) (fingerprint []byte, extractor string) {
	t.Helper()
	path := filepath.Join(e.archives, "canary.zip")
	f, err := os.Create(path)
	require.NoError(t, err)
	zw := zip.NewWriter(f)
	w, err := zw.Create("canary.fb2")
	require.NoError(t, err)
	_, err = w.Write(canaryBook(time.Now()))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	require.NoError(t, f.Close())
	reader, err := zip.OpenReader(path)
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()

	scanner := services.NewBookScanService(e.archives, t.TempDir(), services.NewLanguageDetector(false, 5*time.Second), true, nil)
	_, err = scanner.ProcessBook(reader.File[0], "canary.zip")
	require.NoError(t, err)

	var credit struct {
		Fingerprint []byte `pg:"source_fingerprint"`
		Extractor   string `pg:"extractor_version"`
	}
	_, err = e.db.QueryOne(&credit, `SELECT c.source_fingerprint, s.extractor_version
		FROM book_contributor_credit c JOIN book_metadata_snapshot s ON s.id = c.snapshot_id
		WHERE c.role = 'author' AND c.source_first_name = ?`, nameCanary)
	require.NoError(t, err)
	return credit.Fingerprint, credit.Extractor
}

func TestAuthorLLMLogsCarryNoNameNoExcerptNoKey(t *testing.T) {
	e := newLLMEnv(t)
	ctx := context.Background()
	fingerprint, extractor := ingestCanaryBook(t, e)
	run := e.run(&database.AuthorLLMRunSpec{})
	input := llmreq.ItemInput{Fields: []llmres.FieldValue{
		{Field: llmres.FieldFirst, Text: nameCanary}, {Field: llmres.FieldLast, Text: "Smith"}}, Script: "Latn"}
	var jobs []database.AuthorLLMNewJob
	for _, slot := range []llmreq.Slot{llmreq.SlotProductionA, llmreq.SlotProductionB} {
		jobs = append(jobs, database.AuthorLLMNewJob{ConfigVersion: e.version, Slot: slot, RunID: &run,
			Purpose: models.AuthorLLMPurposeReview, SourceFingerprint: fingerprint, ExtractorVersion: extractor,
			Input: input, WithContext: true})
	}
	_, err := database.EnqueueAuthorLLMJobs(ctx, e.db, jobs)
	require.NoError(t, err)

	// Every error path first, then answers: production_a gets a 500 that
	// echoes the request and then a malformed body; production_b a reply
	// that echoes the name in an invalid shape.
	var callsA, callsB atomic.Int32
	e.fake.setRespond(func(req *fakeRequest) fakeReply {
		if isFingerprint(req) {
			return fakeReply{}
		}
		if req.Model == "gpt-6-luna" {
			switch callsA.Add(1) {
			case 1:
				return fakeReply{Status: http.StatusInternalServerError, Body: `{"error":{"message":` +
					fmt.Sprintf("%q", req.User) + `}}`}
			case 2:
				return fakeReply{Body: "<html>" + excerptCanary + " " + nameCanary}
			}
			return fakeReply{}
		}
		if callsB.Add(1) == 1 {
			return fakeReply{Content: `{"results":[{"id":"1","form":"` + nameCanary + `"}]}`}
		}
		return fakeReply{}
	})
	logs := captureLogs(t)
	w := e.workerWithKey(keyCanary)
	e.drain(w)

	// The excerpt reached the endpoint: the canaries were in play.
	sentExcerpt := false
	for _, req := range e.fake.served() {
		if strings.Contains(req.User, excerptCanary) {
			sentExcerpt = true
		}
	}
	assert.True(t, sentExcerpt, "the excerpt was not sent")
	assert.Equal(t, 2, e.count(`SELECT count(*) FROM author_llm_job WHERE context_state = 'attached' AND status = 'answered'`))
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_context WHERE payload::text LIKE ?`, "%"+excerptCanary+"%"))
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE verdict = 'agree' AND context_state = 'attached'`))

	// The error paths did run.
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_call WHERE outcome = 'http_error' AND http_status = 500`))
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_call WHERE outcome = 'malformed'`))
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_attempt WHERE validator_class = 'item_invalid'`))

	out := logs.String()
	require.Contains(t, out, "author_llm.call_settled", "the worker logged nothing: the check would be vacuous")
	for _, canary := range []string{nameCanary, excerptCanary, keyCanary, "Smith", "Canary Book"} {
		assert.NotContains(t, out, canary)
	}
	assert.Equal(t, 0, e.count(`SELECT count(*) FROM author_llm_config WHERE identity::text LIKE ? OR base_url LIKE ?`,
		"%KEYCANARY%", "%KEYCANARY%"))
}

// workerWithKey is a worker whose client sends a key.
func (e *llmEnv) workerWithKey(key string) *services.AuthorLLMWorker {
	e.t.Helper()
	client := llm.NewClient(config.LLMConfig{BaseURL: e.srv.URL + "/v1", APIKey: key, Timeout: time.Second})
	w, err := services.NewAuthorLLMWorker(e.db, client, &services.AuthorLLMWorkerConfig{
		Settings: e.settings, Identity: e.identity, ConfigVersion: e.version, ArchivesDir: e.archives,
		BaseBackoff: time.Millisecond, MaxBackoff: 10 * time.Millisecond,
	})
	require.NoError(e.t, err)
	return w
}

func TestAuthorLLMWorkerFromConfig(t *testing.T) {
	db := scanfixture.ScratchDB(t)
	ctx := context.Background()
	c := config.AuthorMetadataConfig{LLM: config.DefaultAuthorLLMConfig(), PollInterval: time.Second}

	w, reason, err := services.NewAuthorLLMWorkerFromConfig(ctx, db, t.TempDir(), &c, config.LLMConfig{BaseURL: ""})
	require.NoError(t, err)
	assert.Nil(t, w)
	assert.Equal(t, config.AuthorLLMDisabledNotConfigured, reason)

	same := c
	same.LLM.Models.JudgeB = same.LLM.Models.ProductionA
	w, reason, err = services.NewAuthorLLMWorkerFromConfig(ctx, db, t.TempDir(), &same,
		config.LLMConfig{BaseURL: "http://gw/v1"})
	require.NoError(t, err)
	assert.Nil(t, w)
	assert.Equal(t, config.AuthorLLMDisabledModels, reason)

	w, reason, err = services.NewAuthorLLMWorkerFromConfig(ctx, db, t.TempDir(), &c,
		config.LLMConfig{BaseURL: "HTTP://Gateway.Bots.svc:8317/v1/", APIKey: keyCanary})
	require.NoError(t, err)
	require.NotNil(t, w, "disabled: %s", reason)
	var stored struct {
		BaseURL  string `pg:"base_url"`
		Identity string `pg:"identity"`
	}
	_, err = db.QueryOne(&stored, `SELECT base_url, identity::text AS identity FROM author_llm_config`)
	require.NoError(t, err)
	assert.Equal(t, "http://gateway.bots.svc:8317/v1", stored.BaseURL)
	assert.NotContains(t, stored.Identity, "KEYCANARY")
	var participants int
	_, err = db.QueryOne(pg.Scan(&participants), `SELECT count(*) FROM author_llm_participant`)
	require.NoError(t, err)
	assert.Equal(t, 4, participants)

	// The same settings again: the same version, nothing new.
	_, _, err = services.NewAuthorLLMWorkerFromConfig(ctx, db, t.TempDir(), &c,
		config.LLMConfig{BaseURL: "http://gateway.bots.svc:8317/v1", APIKey: "another-key"})
	require.NoError(t, err)
	var configs int
	_, err = db.QueryOne(pg.Scan(&configs), `SELECT count(*) FROM author_llm_config`)
	require.NoError(t, err)
	assert.Equal(t, 1, configs, "the key is not part of the configuration identity")
}

func TestAuthorLLMExhaustedAbandonedJobFailsTheVerdict(t *testing.T) {
	e := newLLMEnv(t)
	ctx := context.Background()
	e.settings.MaxAttempts = 1
	run := e.run(&database.AuthorLLMRunSpec{})
	e.reviewPair(&run, "exhausted", givenFamily, 1)
	passChecks(t, e.worker())

	dead, _, err := database.ClaimAuthorLLMCall(ctx, e.db, e.claimOptions(llmreq.SlotProductionA, database.NewLeaseOwner(), nil))
	require.NoError(t, err)
	require.NotNil(t, dead)
	w := e.worker()
	claim, _, err := w.Claim(ctx, llmreq.SlotProductionB)
	require.NoError(t, err)
	require.NoError(t, w.Process(ctx, llmreq.SlotProductionB, claim))
	assert.Equal(t, 0, e.count(`SELECT count(*) FROM author_llm_verdict`), "production_a is still in flight")

	later := dead.LeaseExpiresAt.Add(time.Second)
	_, _, err = database.ClaimAuthorLLMCall(ctx, e.db, e.claimOptions(llmreq.SlotProductionA, database.NewLeaseOwner(), &later))
	require.NoError(t, err)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_job WHERE slot = 'production_a' AND status = 'failed'
		AND last_error_class = 'lease_expired'`))
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_verdict WHERE verdict = 'failed'`))
}

func TestAuthorLLMSchemaKeepsHistory(t *testing.T) {
	e := newLLMEnv(t)
	ctx := context.Background()
	run := e.run(&database.AuthorLLMRunSpec{})
	e.reviewPair(&run, "history", givenFamily, 1)
	e.drain(e.worker())

	refused := func(query string, params ...any) {
		t.Helper()
		_, err := e.db.Exec(query, params...)
		assert.Error(t, err, "allowed: %s", query)
	}
	// A closed attempt and a closed call are history.
	refused(`UPDATE author_llm_attempt SET outcome = 'invalid' WHERE outcome = 'answered'`)
	refused(`UPDATE author_llm_attempt SET reply = '{}'::jsonb`)
	refused(`DELETE FROM author_llm_attempt`)
	refused(`UPDATE author_llm_call SET settled_tokens = 1`)
	refused(`UPDATE author_llm_call SET outcome = 'abandoned'`)
	refused(`DELETE FROM author_llm_call`)
	// The configuration identity never changes.
	refused(`UPDATE author_llm_config SET base_url = 'http://other/v1'`)
	refused(`UPDATE author_llm_participant SET model = 'other'`)
	refused(`DELETE FROM author_llm_participant`)
	// A job keeps its input.
	refused(`UPDATE author_llm_job SET input = '{}'::jsonb`)
	refused(`UPDATE author_llm_job SET slot = 'judge_a'`)

	// Frozen eval sets: written once.
	_, err := e.db.Exec(`INSERT INTO author_llm_eval_set (set_version, sha256, items, created_by_run_id)
		VALUES ('s1', decode(repeat('ab', 32), 'hex'), 1, ?)`, run)
	require.NoError(t, err)
	_, err = e.db.Exec(`INSERT INTO author_llm_eval_item (set_version, item_set, stratum, source_fingerprint,
			extractor_version, input, weight)
		VALUES ('s1', 'holdout', 'initials.cyrl', ?, 'x1', '{}'::jsonb, 3)`, testFingerprint("eval"))
	require.NoError(t, err)
	refused(`UPDATE author_llm_eval_item SET weight = 4`)
	refused(`DELETE FROM author_llm_eval_item`)
	refused(`UPDATE author_llm_eval_set SET items = 2`)

	// An excerpt's text may be purged, never changed or brought back.
	sha := testFingerprint("excerpt")
	_, err = database.StoreAuthorLLMContext(ctx, e.db, &database.AuthorLLMContextKey{BookID: 1, BookMD5: "md5",
		Version: llmres.ContextVersion, ExcludeSHA256: testFingerprint("nobody")}, []byte(`{"title":"t"}`), sha)
	require.NoError(t, err)
	refused(`UPDATE author_llm_context SET payload = '{"title":"u"}'::jsonb`)
	_, err = e.db.Exec(`UPDATE author_llm_context SET payload = NULL, purged_at = now()`)
	require.NoError(t, err)
	refused(`UPDATE author_llm_context SET payload = '{"title":"t"}'::jsonb, purged_at = NULL`)
	refused(`UPDATE author_llm_context SET sha256 = ?`, testFingerprint("other"))

	// The verdict's input and sides are fixed; only its verdict, its
	// confirmation and its result move.
	refused(`UPDATE author_llm_verdict SET job_a_id = job_b_id`)
	refused(`DELETE FROM author_llm_verdict`)

	// One active run.
	_, err = database.CreateAuthorLLMRun(ctx, e.db, &database.AuthorLLMRunSpec{Kind: models.AuthorLLMRunEval,
		Mode: "dev", ConfigVersion: e.version})
	assert.ErrorIs(t, err, database.ErrAuthorLLMActiveRun)
}

func TestAuthorLLMEvalRegistrationNeedsItsReport(t *testing.T) {
	e := newLLMEnv(t)
	run := e.run(&database.AuthorLLMRunSpec{Kind: models.AuthorLLMRunEval, Mode: "dev"})
	var admin int64
	_, err := e.db.QueryOne(pg.Scan(&admin), `INSERT INTO auth_user (password, is_superuser, username, email, date_joined)
		VALUES ('', true, 'llm-admin', 'llm-admin@fixture.local', now()) RETURNING id`)
	require.NoError(t, err)
	var report int64
	_, err = e.db.QueryOne(pg.Scan(&report), `INSERT INTO author_llm_eval_report (run_id, config_version, report, report_sha256)
		VALUES (?, ?, '{"passed":["initials.cyrl"]}'::jsonb, decode(repeat('cd', 32), 'hex')) RETURNING id`, run, e.version)
	require.NoError(t, err)

	insert := func(source, ref string, actor *int64, reportID *int64, sha string) error {
		_, err := e.db.Exec(`INSERT INTO author_acceptance_class (policy_version, decision_class, script, config_version,
				evidence_report_sha256, evidence_ref, source, registered_by_user_id, evidence_report_id)
			VALUES ('3', 'llm.initials', 'Cyrl', ?, decode(?, 'hex'), ?, ?, ?, ?)`,
			e.version, sha, ref, source, actor, reportID)
		return err
	}
	good := strings.Repeat("cd", 32)
	refTo := fmt.Sprintf("author_llm_eval_report/%d.json", report)
	assert.Error(t, insert("llm_eval", refTo, &admin, nil, good), "an llm_eval registration names its report")
	assert.Error(t, insert("llm_eval", refTo, nil, &report, good), "and the admin who started the resolve run")
	assert.Error(t, insert("llm_eval", "author_llm_eval_report/999.json", &admin, &report, good), "and the report's own path")
	assert.Error(t, insert("llm_eval", refTo, &admin, &report, strings.Repeat("00", 32)), "and the report's own digest")
	assert.Error(t, insert("shipped", "internal/x.json", nil, &report, good), "a shipped registration names no report")
	assert.NoError(t, insert("llm_eval", refTo, &admin, &report, good))
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_acceptance_class WHERE source = 'llm_eval'`))
}

// An excerpt that does not mention the name is about someone else (a book
// signed with the real name, a credit with the pen name): it is not sent.
func TestAuthorLLMIrrelevantExcerptIsNotSent(t *testing.T) {
	e := newLLMEnv(t)
	fingerprint, extractor := ingestCanaryBook(t, e)
	run := e.run(&database.AuthorLLMRunSpec{})
	other := llmreq.ItemInput{Fields: []llmres.FieldValue{
		{Field: llmres.FieldFirst, Text: "Иван"}, {Field: llmres.FieldLast, Text: "Петров"}}, Script: "Cyrl"}
	_, err := database.EnqueueAuthorLLMJobs(context.Background(), e.db, []database.AuthorLLMNewJob{{
		ConfigVersion: e.version, Slot: llmreq.SlotProductionA, RunID: &run, Purpose: models.AuthorLLMPurposeReview,
		SourceFingerprint: fingerprint, ExtractorVersion: extractor, Input: other, WithContext: true}})
	require.NoError(t, err)
	e.drain(e.worker())

	var served []fakeRequest
	for _, req := range e.fake.served() {
		if !isFingerprint(&req) {
			served = append(served, req)
		}
	}
	require.Len(t, served, 1)
	require.Len(t, served[0].Items, 1)
	assert.Equal(t, "null", string(served[0].Items[0].BookContext))
	assert.NotContains(t, served[0].User, excerptCanary)
	assert.Equal(t, 1, e.count(`SELECT count(*) FROM author_llm_job WHERE context_state = 'irrelevant'
		AND context_book_id IS NOT NULL AND context_sha256 IS NULL`))
	assert.Equal(t, 0, e.count(`SELECT count(*) FROM author_llm_context`), "an irrelevant excerpt is not stored")
}
