package services_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The output mode is negotiated per participant at the fingerprint stage:
// json_schema first; a provider that rejects the request format with an HTTP
// 400 (the official DeepSeek API: "This response_format type is unavailable
// now") is asked the same fixed request in json_object, then tool. The first
// mode that answers is the participant's reference mode, and every later call
// of that participant in the run uses it.

// responseFormat is a request's output mode: the response_format type, or
// "tool" for a forced tool call.
func responseFormat(req *fakeRequest) string {
	var body struct {
		ResponseFormat *struct {
			Type string `json:"type"`
		} `json:"response_format"`
		Tools []json.RawMessage `json:"tools"`
	}
	if json.Unmarshal([]byte(req.Raw), &body) != nil {
		return ""
	}
	if body.ResponseFormat != nil {
		return body.ResponseFormat.Type
	}
	if len(body.Tools) > 0 {
		return "tool"
	}
	return ""
}

const formatRejected = `{"error":{"message":"This response_format type is unavailable now","type":"invalid_request_error"}}`

// rejecting answers byFields, except that production_b's requests in the
// rejected modes get a 400.
func rejecting(rejected ...string) func(*fakeRequest) fakeReply {
	answer := byFields(distinctLabs)
	return func(req *fakeRequest) fakeReply {
		if req.Model == "deepseek-v4-pro" {
			for _, mode := range rejected {
				if responseFormat(req) == mode {
					return fakeReply{Status: http.StatusBadRequest, Body: formatRejected}
				}
			}
		}
		return answer(req)
	}
}

func modesOf(e *llmEnv, model string, fingerprint bool) []string {
	e.fake.mu.Lock()
	defer e.fake.mu.Unlock()
	var out []string
	for i := range e.fake.requests {
		req := &e.fake.requests[i]
		if req.Model == model && isFingerprint(req) == fingerprint {
			out = append(out, responseFormat(req))
		}
	}
	return out
}

func TestAuthorLLMFingerprintNegotiatesTheOutputMode(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	e.fake.setRespond(rejecting("json_schema"))
	ctx := context.Background()
	run, err := e.llmAdmin().Start(ctx, "eval", "pilot", e.adminUser())
	require.NoError(t, err)
	stages := e.evalUntilDone(run.ID)
	assert.Equal(t, []string{"freeze", "fingerprint", "anchor", "judge", "candidates", "repeat", "report"}, stages,
		"the run did not get past the fingerprint")

	// production_b: refused once in json_schema, then fingerprinted in
	// json_object — every repeat — and every job of it in json_object.
	fingerprints := modesOf(e, "deepseek-v4-pro", true)
	require.NotEmpty(t, fingerprints)
	assert.Equal(t, "json_schema", fingerprints[0])
	assert.Equal(t, 1, strings.Count(strings.Join(fingerprints, ","), "json_schema"),
		"the rejected mode was tried again: %v", fingerprints)
	// Every repeat of the stage, and the periodic checks after it, in
	// json_object.
	assert.GreaterOrEqual(t, strings.Count(strings.Join(fingerprints, ","), "json_object"), evalSizes.FingerprintRepeats)
	for _, m := range fingerprints[1:] {
		assert.Equal(t, "json_object", m, "a fingerprint after the rejection: %v", fingerprints)
	}
	assert.Positive(t, e.count(`SELECT count(*) FROM author_llm_call WHERE slot = 'production_b' AND kind = 'fingerprint'
		AND output_mode = 'json_object'`), "a periodic check of production_b in its negotiated mode")
	jobs := modesOf(e, "deepseek-v4-pro", false)
	require.NotEmpty(t, jobs)
	for _, m := range jobs {
		assert.Equal(t, "json_object", m)
	}
	// The others accept json_schema and stay on it.
	for _, model := range []string{"gpt-6-luna", "gpt-6-sol", "claude-opus-5"} {
		for _, m := range append(modesOf(e, model, true), modesOf(e, model, false)...) {
			assert.Equal(t, "json_schema", m, model)
		}
	}
	assert.Nil(t, e.state("production_b").PausedReason, "a format rejection paused the participant")

	var raw string
	_, err = e.db.QueryOne(pg.Scan(&raw), `SELECT reference::text FROM author_llm_run WHERE id = ?`, run.ID)
	require.NoError(t, err)
	var reference map[string]struct {
		OutputMode string `json:"output_mode"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &reference))
	assert.Equal(t, "json_object", reference["production_b"].OutputMode)
	assert.Equal(t, "json_schema", reference["production_a"].OutputMode)

	// The report shows the negotiated modes.
	text, _, err := services.AuthorLLMEvalReportFile(ctx, e.db, run.ID)
	require.NoError(t, err)
	var doc struct {
		Settings struct {
			OutputModes map[string]string `json:"output_modes"`
		} `json:"settings"`
	}
	require.NoError(t, json.Unmarshal(text, &doc))
	assert.Equal(t, map[string]string{"production_a": "json_schema", "production_b": "json_object",
		"judge_a": "json_schema", "judge_b": "json_schema"}, doc.Settings.OutputModes)
}

func TestAuthorLLMFingerprintFailsWhenNoOutputModeWorks(t *testing.T) {
	e := newLLMEnv(t)
	ingestEvalCatalogue(t, e)
	e.fake.setRespond(rejecting("json_schema", "json_object", "tool"))
	ctx := context.Background()
	run, err := e.llmAdmin().Start(ctx, "eval", "pilot", e.adminUser())
	require.NoError(t, err)
	stages := e.evalUntilDone(run.ID)
	assert.Equal(t, []string{"freeze", "fingerprint", "report"}, stages)
	r := downloadReport(t, e, run.ID)
	assert.Contains(t, reasonsOf(&r), "no_output_mode: production_b")
	fingerprints := strings.Join(modesOf(e, "deepseek-v4-pro", true), ",")
	assert.Equal(t, "json_schema,json_object,tool", fingerprints, "each mode tried once, in order")
	assert.Empty(t, modesOf(e, "deepseek-v4-pro", false), "a participant without a mode got jobs")
}

// A server error, a 429 or a timeout says nothing about the request format:
// the participant stays on json_schema.
func TestAuthorLLMServerErrorIsNotAFormatRejection(t *testing.T) {
	for name, reply := range map[string]fakeReply{
		"5xx":     {Status: http.StatusServiceUnavailable, Body: `{"error":{"message":"overloaded"}}`},
		"429":     {Status: http.StatusTooManyRequests, Body: `{"error":{"message":"slow down"}}`},
		"timeout": {Status: -1},
	} {
		t.Run(name, func(t *testing.T) {
			e := newLLMEnv(t)
			e.settings.RequestTimeout = 200 * time.Millisecond
			ingestEvalCatalogue(t, e)
			answer := byFields(distinctLabs)
			e.fake.setRespond(func(req *fakeRequest) fakeReply {
				if req.Model != "deepseek-v4-pro" {
					return answer(req)
				}
				if reply.Status == -1 {
					time.Sleep(time.Second)
					return answer(req)
				}
				return reply
			})
			ctx := context.Background()
			run, err := e.llmAdmin().Start(ctx, "eval", "pilot", e.adminUser())
			require.NoError(t, err)
			driver := services.NewAuthorLLMEvalDriver(e.db, &e.identity, e.version, evalSizes, e.archives, nil, time.Second)
			w := e.worker()
			for range 6 {
				_, err = driver.Step(ctx)
				require.NoError(t, err)
				_, _ = w.RunOnce(ctx)
			}
			require.NotEmpty(t, modesOf(e, "deepseek-v4-pro", true), "production_b was never asked")
			for _, m := range modesOf(e, "deepseek-v4-pro", true) {
				assert.Equal(t, "json_schema", m, "a %s was taken for a format rejection", name)
			}
			assert.Zero(t, e.count(`SELECT count(*) FROM author_llm_job WHERE run_id = ? AND output_mode <> 'json_schema'`, run.ID))
		})
	}
}
