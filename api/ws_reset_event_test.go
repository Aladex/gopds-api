package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gopds-api/internal/scanfixture"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Resetting an archive removes it from the scanned list while no scan is
// running, so no scan_started/scan_completed pair ever announces the change.
// The endpoint itself has to publish archive_reset or every other open admin
// tab keeps showing the archive as scanned until it is reloaded.
func TestResetArchiveScanStatusPublishesArchiveReset(t *testing.T) {
	db := scanfixture.ScratchDB(t)

	scannedAt := time.Now()
	_, err := db.Model(&models.Catalog{CatName: "reset-me", IsScanned: true, ScannedAt: &scannedAt}).Insert()
	require.NoError(t, err)

	mgr := services.NewWebSocketManager()
	previous := wsManager
	wsManager = mgr
	t.Cleanup(func() { wsManager = previous })

	ch := make(chan []byte, 4)
	id := mgr.RegisterClient(nil, 1, "watcher", true, ch)
	require.True(t, mgr.Subscribe(id, services.TopicScan))

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodDelete,
		"/api/admin/scan/reset/reset-me?confirm=true", http.NoBody)
	c.Params = gin.Params{{Key: "name", Value: "reset-me"}}

	ResetArchiveScanStatus(c)
	require.Equal(t, http.StatusOK, recorder.Code)

	var envelope struct {
		Type  string `json:"type"`
		Topic string `json:"topic"`
		Data  struct {
			ArchiveName string `json:"archive_name"`
		} `json:"data"`
	}
	select {
	case msg := <-ch:
		require.NoError(t, json.Unmarshal(msg, &envelope))
		assert.Equal(t, services.ArchiveResetType, envelope.Type)
		assert.Equal(t, services.TopicScan, envelope.Topic)
		assert.Equal(t, "reset-me", envelope.Data.ArchiveName)
	case <-time.After(2 * time.Second):
		t.Fatal("resetting an archive must publish archive_reset to scan-topic subscribers")
	}
}
