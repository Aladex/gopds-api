package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gopds-api/api"
	"gopds-api/config"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The LLM block's admin as the server wires it: the routes answer from the
// real service on a migrated database, under the configuration the worker
// runs, and a switched-off layer says why and starts nothing.

func llmAdminRouter(t *testing.T, actor int64) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	admin := r.Group("/api/admin", func(c *gin.Context) {
		c.Set("username", "admin")
		c.Set("user_id", actor)
		c.Next()
	})
	api.SetupAdminRoutes(admin)
	return r
}

func llmCall(t *testing.T, r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, "/api/admin/author-metadata/llm"+path,
		strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestAuthorLLMAdminWiring(t *testing.T) {
	db := scratchDB(t)
	var actor int64
	_, err := db.QueryOne(pg.Scan(&actor), `INSERT INTO auth_user (password, is_superuser, username, email, date_joined)
		VALUES ('', true, 'llm-wiring', 'llm-wiring@fixture.local', now()) RETURNING id`)
	require.NoError(t, err)
	c := wiringConfig()
	c.LLM = config.DefaultAuthorLLMConfig()

	t.Run("switched off", func(t *testing.T) {
		initializeAuthorLLMAdminAPI(db, &c, config.LLMConfig{})
		r := llmAdminRouter(t, actor)
		rec := llmCall(t, r, http.MethodGet, "/status", "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var body struct {
			Status services.AuthorLLMStatus `json:"status"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.False(t, body.Status.Enabled)
		assert.Equal(t, config.AuthorLLMDisabledNotConfigured, body.Status.Reason)
		rec = llmCall(t, r, http.MethodPost, "/runs", `{"kind":"eval","mode":"pilot"}`)
		assert.Equal(t, http.StatusConflict, rec.Code)
		assert.JSONEq(t, `{"error":"llm_not_configured"}`, rec.Body.String())
	})

	t.Run("workers off", func(t *testing.T) {
		off := c
		off.Enabled = false
		initializeAuthorLLMAdminAPI(db, &off, config.LLMConfig{BaseURL: "http://gateway.internal:8317/v1"})
		rec := llmCall(t, llmAdminRouter(t, actor), http.MethodGet, "/status", "")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"reason":"workers_disabled"`)
	})

	t.Run("configured", func(t *testing.T) {
		shared := config.LLMConfig{BaseURL: "http://gateway.internal:8317/v1"}
		initializeAuthorLLMAdminAPI(db, &c, shared)
		r := llmAdminRouter(t, actor)

		// The admin and the worker agree on the configuration version.
		worker, reason, err := services.NewAuthorLLMWorkerFromConfig(context.Background(), db, t.TempDir(), &c, shared)
		require.NoError(t, err)
		require.NotNil(t, worker, reason)

		rec := llmCall(t, r, http.MethodPost, "/runs", `{"kind":"eval","mode":"full"}`)
		assert.Equal(t, http.StatusConflict, rec.Code)
		assert.JSONEq(t, `{"error":"prerequisite_incomplete"}`, rec.Body.String())
		// Resolve waits for a full eval, the swaps for their own eval.
		for _, body := range []string{`{"kind":"resolve","mode":"pilot"}`, `{"kind":"eval","mode":"swap"}`,
			`{"kind":"swap","mode":"pilot"}`, `{"kind":"swap","mode":"full"}`} {
			rec = llmCall(t, r, http.MethodPost, "/runs", body)
			assert.Equal(t, http.StatusConflict, rec.Code, body)
			assert.JSONEq(t, `{"error":"prerequisite_incomplete"}`, rec.Body.String(), body)
		}

		rec = llmCall(t, r, http.MethodPost, "/runs", `{"kind":"eval","mode":"pilot"}`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		var started struct {
			Run services.AuthorLLMRunState `json:"run"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &started))
		assert.True(t, started.Run.CurrentConfig, "the admin started a run of another configuration than the worker's")
		var version string
		_, err = db.QueryOne(pg.Scan(&version), `SELECT config_version FROM author_llm_run WHERE id = ?`, started.Run.ID)
		require.NoError(t, err)
		var workerVersion string
		_, err = db.QueryOne(pg.Scan(&workerVersion), `SELECT version FROM author_llm_config WHERE NOT identity ? 'task'`)
		require.NoError(t, err, "one name normalization configuration row: the admin's and the worker's")
		assert.Equal(t, workerVersion, version)

		// The merge configuration's admin (E4c) on the same routes under
		// /merge: its own row, its one step, refused while K's run is active.
		var configs int
		_, err = db.QueryOne(pg.Scan(&configs), `SELECT count(*) FROM author_llm_config WHERE identity->>'task' = 'merge'`)
		require.NoError(t, err)
		assert.Equal(t, 1, configs)
		rec = llmCall(t, r, http.MethodGet, "/merge/status", "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var merge struct {
			Status services.AuthorLLMStatus `json:"status"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &merge))
		assert.True(t, merge.Status.Enabled)
		assert.True(t, merge.Status.OtherActive)
		assert.Nil(t, merge.Status.Run, "the merge card showed K's run")
		require.Len(t, merge.Status.Steps, 5, "merge0 and the four stage modes")
		for _, step := range merge.Status.Steps {
			assert.Equal(t, services.AuthorLLMStepActiveRun, step.Blocked, step.Key)
		}
		rec = llmCall(t, r, http.MethodPost, "/merge/runs", `{"kind":"eval","mode":"merge0"}`)
		assert.Equal(t, http.StatusConflict, rec.Code)
		assert.JSONEq(t, `{"error":"active_run_exists"}`, rec.Body.String())
		rec = llmCall(t, r, http.MethodPost, "/merge/runs", `{"kind":"eval","mode":"pilot"}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.JSONEq(t, `{"error":"invalid_run"}`, rec.Body.String())
		rec = llmCall(t, r, http.MethodPost, "/runs", `{"kind":"eval","mode":"merge0"}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "K's admin started a merge eval")

		// The suite review and its labels (E4c-2b-0): merge routes only.
		rec = llmCall(t, r, http.MethodGet, "/merge/eval-report/"+jsonNumber(started.Run.ID)+"/adversarial", "")
		assert.Equal(t, http.StatusBadRequest, rec.Code, "a K run has no suite")
		assert.JSONEq(t, `{"error":"invalid_run"}`, rec.Body.String())
		rec = llmCall(t, r, http.MethodPut, "/merge/adjudications/999999", `{"label":"same","note":""}`)
		assert.Equal(t, http.StatusConflict, rec.Code)
		assert.JSONEq(t, `{"error":"not_adjudicable"}`, rec.Body.String())
		rec = llmCall(t, r, http.MethodPut, "/merge/adjudications/999999", `{"label":"maybe","note":""}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.JSONEq(t, `{"error":"invalid_request"}`, rec.Body.String())
		rec = llmCall(t, r, http.MethodPut, "/adjudications/999999", `{"label":"same","note":""}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "K's admin labeled an item")

		rec = llmCall(t, r, http.MethodPost, "/runs", `{"kind":"eval","mode":"pilot"}`)
		assert.Equal(t, http.StatusConflict, rec.Code)
		assert.JSONEq(t, `{"error":"active_run_exists"}`, rec.Body.String())
		path := "/runs/" + jsonNumber(started.Run.ID)
		require.Equal(t, http.StatusOK, llmCall(t, r, http.MethodPost, path+"/pause", "").Code)
		rec = llmCall(t, r, http.MethodPost, path+"/pause", "")
		assert.Equal(t, http.StatusConflict, rec.Code)
		assert.JSONEq(t, `{"error":"invalid_transition"}`, rec.Body.String())
		require.Equal(t, http.StatusOK, llmCall(t, r, http.MethodPost, path+"/resume", "").Code)
		rec = llmCall(t, r, http.MethodGet, "/eval-report/"+jsonNumber(started.Run.ID), "")
		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.JSONEq(t, `{"error":"report_not_found"}`, rec.Body.String())
		rec = llmCall(t, r, http.MethodPost, "/runs/999999/pause", "")
		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.JSONEq(t, `{"error":"run_not_found"}`, rec.Body.String())
	})
}

func jsonNumber(n int64) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}
