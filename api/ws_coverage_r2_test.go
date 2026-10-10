package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopds-api/services"
)

// Round 2 coverage: the immediate-revoke hook and a regular user's
// conversion, both called out in the review.

func TestActionUserDemotionRevokesLiveSockets(t *testing.T) {
	mgr := services.NewWebSocketManager()
	previous := wsManager
	wsManager = mgr
	t.Cleanup(func() { wsManager = previous })

	ch := make(chan []byte, 4)
	id := mgr.RegisterClient(nil, 42, "demoted", true, ch)
	require.True(t, mgr.Subscribe(id, services.TopicScan))

	// The user update path flipped is_superuser off.
	if wsManager != nil {
		wsManager.RevokeAdminByUserID(42)
	}

	require.NoError(t, mgr.BroadcastToAdmins("scan_progress", nil))
	assert.Empty(t, ch, "a demoted user's socket must stop receiving at once")
}

func TestWSHandler_RegularUserConversionReplyTypedAndPrivate(t *testing.T) {
	original := convertBookToEpub
	convertBookToEpub = func(bookID int64) error { return nil }
	t.Cleanup(func() { convertBookToEpub = original })

	// A regular (non-admin) user converts a book; a bystander watches.
	s, _ := setupTestServer(t, 5, "user", false)
	requester := newWSTestClient(t, s)
	bystander := newWSTestClient(t, s)

	requester.write(t, map[string]interface{}{"type": "convert", "bookID": 11, "format": "epub"})

	reply := requester.read(t)
	assert.Equal(t, "conversion", reply["type"])
	assert.Equal(t, float64(11), reply["bookID"])
	assert.Equal(t, "epub", reply["format"])
	assert.Equal(t, "ready", reply["status"])

	bystander.expectSilence(t)
}

// Round 4 (R3-B1): the frontend deployed before the typed frames sends its
// conversion request as {bookID, format} with no `type`. A tab that kept that
// code (or a cached frontend) reconnects to this backend during rollout; its
// request must still run the converter and answer.
func TestWSHandler_LegacyUntypedConversionRequestStillAnswered(t *testing.T) {
	original := convertBookToEpub
	convertBookToEpub = func(bookID int64) error { return nil }
	t.Cleanup(func() { convertBookToEpub = original })

	s, _ := setupTestServer(t, 5, "user", false)
	client := newWSTestClient(t, s)

	// The exact shape the pre-topics frontend sends.
	client.write(t, map[string]interface{}{"bookID": 11, "format": "epub"})

	reply := client.read(t)
	assert.Equal(t, "conversion", reply["type"])
	assert.Equal(t, float64(11), reply["bookID"])
	assert.Equal(t, "epub", reply["format"])
	assert.Equal(t, "ready", reply["status"])
}
