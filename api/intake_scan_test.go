package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/internal/scanfixture"

	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v10"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testIntakeToken = "intake-token-of-at-least-32-characters"

// intakeRouter serves the route the way cmd/gopds does, with the token set.
func intakeRouter(t *testing.T, token string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	viper.Set("scanning.intake_token", token)
	t.Cleanup(func() { viper.Set("scanning.intake_token", "") })
	r := gin.New()
	SetupIntakeRoutes(r.Group("/api/intake"))
	return r
}

func postIntake(r *gin.Engine, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/intake/scan", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func intakeAnswer(t *testing.T, w *httptest.ResponseRecorder) IntakeScanResponse {
	t.Helper()
	var out IntakeScanResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out), w.Body.String())
	return out
}

// Nothing configured means no route, and a placeholder counts as nothing: the
// path is public and a session is not asked for.
func TestIntakeRouteIsOffWithoutARealToken(t *testing.T) {
	for _, token := range []string{"", "short", strings.Repeat(" ", 40)} {
		w := postIntake(intakeRouter(t, token), token, `{"name":"new.zip"}`)
		assert.Equal(t, http.StatusNotFound, w.Code, "token %q", token)
	}
}

func TestIntakeRouteRefusesAnyOtherToken(t *testing.T) {
	r := intakeRouter(t, testIntakeToken)
	for _, token := range []string{"", testIntakeToken + "x", testIntakeToken[1:]} {
		assert.Equal(t, http.StatusUnauthorized, postIntake(r, token, `{"name":"new.zip"}`).Code, "token %q", token)
	}
}

// The token reaches one file directly under the archives directory, whatever
// the name says.
func TestIntakeRouteTakesOnlyAFileName(t *testing.T) {
	r := intakeRouter(t, testIntakeToken)
	for _, body := range []string{``, `{}`, `{"name":""}`, `{"name":"../secret.zip"}`, `{"name":"sub/new.zip"}`,
		`{"name":"/etc/new.zip"}`, `{"name":"new.txt"}`, `{"name":".."}`} {
		assert.Equal(t, http.StatusBadRequest, postIntake(r, testIntakeToken, body).Code, body)
	}
}

// waitIntakeScan waits for the background scan the route started.
func waitIntakeScan(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool { return !scanState.isRunning() }, 30*time.Second, 10*time.Millisecond)
}

// The whole conversation of the job: the first call starts the scan, a later
// one reports the result and reads nothing. The archive is deleted between
// them, so a second pass over it could not go unnoticed.
func TestIntakeScanStartsOnceAndThenReports(t *testing.T) {
	t.Setenv("LLM_API_KEY", "")
	db := scanfixture.ScratchDB(t)
	dir, covers := t.TempDir()+"/", t.TempDir()+"/"
	viper.Set("app.files_path", dir)
	viper.Set("app.posters_path", covers)
	t.Cleanup(func() {
		viper.Set("app.files_path", "")
		viper.Set("app.posters_path", "")
	})
	scanState = bookScanState{}
	r := intakeRouter(t, testIntakeToken)
	path := scanfixture.WriteArchive(t, dir, time.Now())
	body := `{"name":"` + filepath.Base(path) + `"}`

	w := postIntake(r, testIntakeToken, body)
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	assert.Equal(t, IntakeScanStarted, intakeAnswer(t, w).Status)
	waitIntakeScan(t)

	var books int
	_, err := db.QueryOne(pg.Scan(&books), `SELECT count(*) FROM opds_catalog_book WHERE path = ?`, filepath.Base(path))
	require.NoError(t, err)
	require.Equal(t, 3, books)

	require.NoError(t, os.Remove(path))
	w = postIntake(r, testIntakeToken, body)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	answer := intakeAnswer(t, w)
	assert.Equal(t, IntakeScanScanned, answer.Status)
	assert.Equal(t, 3, answer.BooksCount)
	assert.Zero(t, answer.ErrorsCount)
	assert.False(t, scanState.isRunning(), "a scanned archive was scanned again")
}

// A scan in progress is reported as such and is not joined by a second one.
func TestIntakeScanWaitsForARunningScan(t *testing.T) {
	scanfixture.ScratchDB(t)
	r := intakeRouter(t, testIntakeToken)
	scanState = bookScanState{}
	require.True(t, scanState.tryStart("admin", time.Now()))
	t.Cleanup(func() { scanState.finish("admin") })

	w := postIntake(r, testIntakeToken, `{"name":"new.zip"}`)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, IntakeScanRunning, intakeAnswer(t, w).Status)
	assert.Equal(t, "admin", scanState.sessionID)
}

// check_only tells the job how things stand and starts nothing, so a dry run
// and a job that gave up on a failing scan can still look.
func TestIntakeCheckOnlyStartsNothing(t *testing.T) {
	t.Setenv("LLM_API_KEY", "")
	scanfixture.ScratchDB(t)
	dir := t.TempDir() + "/"
	viper.Set("app.files_path", dir)
	t.Cleanup(func() { viper.Set("app.files_path", "") })
	scanState = bookScanState{}
	path := scanfixture.WriteArchive(t, dir, time.Now())

	w := postIntake(intakeRouter(t, testIntakeToken), testIntakeToken,
		`{"name":"`+filepath.Base(path)+`","check_only":true}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, IntakeScanIdle, intakeAnswer(t, w).Status)
	assert.False(t, scanState.isRunning())
	catalog, err := database.GetScanStatus(filepath.Base(path))
	require.NoError(t, err)
	assert.Nil(t, catalog)
}

func TestIntakeScanReportsAMissingArchive(t *testing.T) {
	scanfixture.ScratchDB(t)
	viper.Set("app.files_path", t.TempDir()+"/")
	t.Cleanup(func() { viper.Set("app.files_path", "") })
	scanState = bookScanState{}
	w := postIntake(intakeRouter(t, testIntakeToken), testIntakeToken, `{"name":"absent.zip"}`)
	assert.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	assert.False(t, scanState.isRunning())
	catalog, err := database.GetScanStatus("absent.zip")
	require.NoError(t, err)
	assert.Nil(t, catalog, "a refused name left a catalog row")
}
