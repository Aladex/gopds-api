package services

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Topic broadcast routing: an event reaches only admins subscribed to its topic.

func subscribeAll(t *testing.T, m *WebSocketManager, id uint64, topics ...string) {
	t.Helper()
	for _, topic := range topics {
		require.True(t, m.Subscribe(id, topic), "subscribe %s", topic)
	}
}

func TestBroadcastTopic_OnlySubscribedAdminsReceive(t *testing.T) {
	m := NewWebSocketManager()
	scanCh := make(chan []byte, 4)
	dupCh := make(chan []byte, 4)

	scanID := m.RegisterClient(nil, 1, "admin-scan", true, scanCh)
	dupID := m.RegisterClient(nil, 2, "admin-dup", true, dupCh)
	subscribeAll(t, m, scanID, TopicScan)
	subscribeAll(t, m, dupID, TopicDuplicates)

	err := m.BroadcastToAdmins(ScanProgress, map[string]int{"done": 5})
	require.NoError(t, err)

	require.Len(t, scanCh, 1)
	msg := <-scanCh
	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(msg, &parsed))
	assert.Equal(t, ScanProgress, parsed["type"])
	assert.Equal(t, TopicScan, parsed["topic"])

	assert.Empty(t, dupCh)
}

func TestBroadcastTopic_AdminWithoutSubscriptionGetsNothing(t *testing.T) {
	m := NewWebSocketManager()
	ch := make(chan []byte, 4)
	m.RegisterClient(nil, 1, "admin", true, ch)

	err := m.BroadcastToAdmins(ScanProgress, map[string]int{"done": 5})
	require.NoError(t, err)
	assert.Empty(t, ch)
}

func TestBroadcastTopic_NonAdminNeverReceives(t *testing.T) {
	m := NewWebSocketManager()
	userCh := make(chan []byte, 4)

	// Even if a subscription is recorded for the client, a non-admin must
	// never receive an admin topic event.
	userID := m.RegisterClient(nil, 2, "user", false, userCh)
	subscribeAll(t, m, userID, TopicScan, TopicDuplicates, TopicGenres, TopicFixScan)

	for _, messageType := range []string{
		ScanStarted, ArchiveStarted, ArchiveCompleted,
		ScanCompleted, ScanErrorEventType, ScanProgress,
		FixScanStartedType, FixScanProgressType, FixScanCompletedType, FixScanErrorType,
		GenreTitleGenStartedType, GenreTitleGenProgressType, GenreTitleGenCompletedType,
		"duplicate_scan_progress",
	} {
		require.NoError(t, m.BroadcastToAdmins(messageType, nil))
	}
	assert.Empty(t, userCh)
}

func TestSubscribe_UnknownTopicRefused(t *testing.T) {
	m := NewWebSocketManager()
	ch := make(chan []byte, 4)
	id := m.RegisterClient(nil, 1, "admin", true, ch)

	assert.False(t, m.Subscribe(id, "no_such_topic"))
	assert.False(t, m.Subscribe(99999, TopicScan)) // unknown client
}

func TestUnsubscribe_StopsDelivery(t *testing.T) {
	m := NewWebSocketManager()
	ch := make(chan []byte, 4)
	id := m.RegisterClient(nil, 1, "admin", true, ch)
	subscribeAll(t, m, id, TopicScan)

	require.NoError(t, m.BroadcastToAdmins(ScanProgress, nil))
	require.Len(t, ch, 1)

	m.Unsubscribe(id, TopicScan)
	require.NoError(t, m.BroadcastToAdmins(ScanProgress, nil))
	assert.Len(t, ch, 1, "no delivery after unsubscribe")
}

func TestUnregisterClient_CleansSubscriptions(t *testing.T) {
	m := NewWebSocketManager()
	ch := make(chan []byte, 4)
	id := m.RegisterClient(nil, 1, "admin", true, ch)
	subscribeAll(t, m, id, TopicScan)

	m.UnregisterClient(id)
	require.NoError(t, m.BroadcastToAdmins(ScanProgress, nil))
	assert.Empty(t, ch)

	// Subscribing a gone client must fail instead of panicking.
	assert.False(t, m.Subscribe(id, TopicScan))
}

func TestBroadcastToAdmins_UnknownTypeRejected(t *testing.T) {
	m := NewWebSocketManager()
	ch := make(chan []byte, 4)
	id := m.RegisterClient(nil, 1, "admin", true, ch)
	subscribeAll(t, m, id, TopicScan)

	err := m.BroadcastToAdmins("test_event", "hello")
	assert.Error(t, err)
	assert.Empty(t, ch)
}

func TestBroadcastTopic_SlowClientDoesNotBlockOthers(t *testing.T) {
	m := NewWebSocketManager()
	slowCh := make(chan []byte, 1)
	fastCh := make(chan []byte, 4)

	slowID := m.RegisterClient(nil, 1, "admin-slow", true, slowCh)
	fastID := m.RegisterClient(nil, 2, "admin-fast", true, fastCh)
	subscribeAll(t, m, slowID, TopicScan)
	subscribeAll(t, m, fastID, TopicScan)

	// Fill the slow client's buffer; the broadcast must drop there and still
	// reach the fast client.
	slowCh <- []byte("blocking")
	err := m.BroadcastToAdmins(ScanProgress, nil)
	require.NoError(t, err)

	assert.Len(t, slowCh, 1)
	assert.Len(t, fastCh, 1)
}

func TestBroadcastTopic_EnvelopeCarriesTopicPerEvent(t *testing.T) {
	m := NewWebSocketManager()
	ch := make(chan []byte, 4)
	id := m.RegisterClient(nil, 1, "admin", true, ch)
	subscribeAll(t, m, id, TopicFixScan, TopicGenres)

	require.NoError(t, m.BroadcastToAdmins(FixScanProgressType, nil))
	require.NoError(t, m.BroadcastToAdmins(GenreTitleGenStartedType, nil))
	require.NoError(t, m.BroadcastToAdmins(ScanProgress, nil))

	require.Len(t, ch, 2)
	var first, second map[string]interface{}
	require.NoError(t, json.Unmarshal(<-ch, &first))
	require.NoError(t, json.Unmarshal(<-ch, &second))
	assert.Equal(t, TopicFixScan, first["topic"])
	assert.Equal(t, TopicGenres, second["topic"])
}

// The per-book book_processed event is gone: its payload rides the throttled
// scan_progress frame as last_book_title.
func TestPublishProgressCoalescesLastBookTitle(t *testing.T) {
	m := NewWebSocketManager()
	ch := make(chan []byte, 4)
	id := m.RegisterClient(nil, 1, "admin", true, ch)
	require.True(t, m.Subscribe(id, TopicScan))

	scanner := NewBookScanService(t.TempDir(), t.TempDir(), nil, false, nil)
	scanner.SetScanEventPublisher(NewScanEventPublisher(NewAdminWSConnection(m)))

	scanner.progressMu.Lock()
	scanner.lastBookTitle = "A Book Title"
	scanner.progressMu.Unlock()

	scanner.PublishProgress("archive.zip", 1, 2, 10, 20, 5)

	require.Len(t, ch, 1)
	var parsed struct {
		Type  string `json:"type"`
		Topic string `json:"topic"`
		Data  struct {
			BooksProcessed int    `json:"books_processed"`
			LastBookTitle  string `json:"last_book_title"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(<-ch, &parsed))
	assert.Equal(t, ScanProgress, parsed.Type)
	assert.Equal(t, TopicScan, parsed.Topic)
	assert.Equal(t, 10, parsed.Data.BooksProcessed)
	assert.Equal(t, "A Book Title", parsed.Data.LastBookTitle)
}

// --- Round 2: the hub must notice when an admin loses the role ---

func TestRevokeAdminByUserID(t *testing.T) {
	m := NewWebSocketManager()
	revokedCh := make(chan []byte, 4)
	otherCh := make(chan []byte, 4)

	// Two sockets of the same user plus one of another admin.
	first := m.RegisterClient(nil, 7, "a", true, revokedCh)
	second := m.RegisterClient(nil, 7, "a", true, make(chan []byte, 4))
	other := m.RegisterClient(nil, 8, "b", true, otherCh)
	subscribeAll(t, m, first, TopicScan)
	subscribeAll(t, m, second, TopicScan)
	subscribeAll(t, m, other, TopicScan)

	m.RevokeAdminByUserID(7)

	require.NoError(t, m.BroadcastToAdmins(ScanProgress, nil))
	assert.Empty(t, revokedCh, "revoked user's socket must not receive admin events")
	require.Len(t, otherCh, 1, "other admins keep receiving")

	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, id := range []uint64{first, second} {
		client := m.clients[id]
		assert.False(t, client.IsAdmin, "revoked socket must not stay admin")
		assert.Empty(t, client.topics, "revoke drops every subscription")
	}
	assert.True(t, m.clients[other].IsAdmin)
	assert.True(t, m.clients[other].topics[TopicScan])
}

func TestSetAdminRolePromotesAndDemotes(t *testing.T) {
	m := NewWebSocketManager()
	ch := make(chan []byte, 4)
	id := m.RegisterClient(nil, 1, "user", false, ch)

	// Promotion makes the socket eligible for delivery after a subscribe.
	m.SetAdminRole(id, true)
	subscribeAll(t, m, id, TopicGenres)
	require.NoError(t, m.BroadcastToAdmins(GenreTitleGenStartedType, nil))
	require.Len(t, ch, 1)

	// Demotion drops subscriptions and stops delivery at once.
	m.SetAdminRole(id, false)
	require.NoError(t, m.BroadcastToAdmins(GenreTitleGenStartedType, nil))
	assert.Len(t, ch, 1)
	m.mu.RLock()
	assert.Empty(t, m.clients[id].topics)
	m.mu.RUnlock()
}

// --- Round 2: completion flushes the coalesced title (N1), resets publish ---

func TestScanCompletedFlushesLastBookTitle(t *testing.T) {
	m := NewWebSocketManager()
	ch := make(chan []byte, 4)
	id := m.RegisterClient(nil, 1, "admin", true, ch)
	require.True(t, m.Subscribe(id, TopicScan))

	scanner := NewBookScanService(t.TempDir(), t.TempDir(), nil, false, nil)
	scanner.SetScanEventPublisher(NewScanEventPublisher(NewAdminWSConnection(m)))

	scanner.progressMu.Lock()
	scanner.lastBookTitle = "Final Book"
	scanner.progressMu.Unlock()

	scanner.PublishScanCompleted(&ScanReport{TotalArchives: 1, ProcessedBooks: 3})

	require.Len(t, ch, 1)
	var parsed struct {
		Type  string `json:"type"`
		Topic string `json:"topic"`
		Data  struct {
			TotalBooks    int    `json:"total_books"`
			LastBookTitle string `json:"last_book_title"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(<-ch, &parsed))
	assert.Equal(t, ScanCompleted, parsed.Type)
	assert.Equal(t, TopicScan, parsed.Topic)
	assert.Equal(t, 3, parsed.Data.TotalBooks)
	// The coalesced title must ride the completion frame: the last progress
	// tick may have fired before the last book landed.
	assert.Equal(t, "Final Book", parsed.Data.LastBookTitle)
}

func TestPublishArchiveResetBroadcastsOnScanTopic(t *testing.T) {
	m := NewWebSocketManager()
	ch := make(chan []byte, 4)
	id := m.RegisterClient(nil, 1, "admin", true, ch)
	require.True(t, m.Subscribe(id, TopicScan))

	publisher := NewScanEventPublisher(NewAdminWSConnection(m))
	publisher.PublishArchiveReset("some-archive.zip")

	require.Len(t, ch, 1)
	var parsed struct {
		Type  string `json:"type"`
		Topic string `json:"topic"`
		Data  struct {
			ArchiveName string `json:"archive_name"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(<-ch, &parsed))
	assert.Equal(t, ArchiveResetType, parsed.Type)
	assert.Equal(t, TopicScan, parsed.Topic)
	assert.Equal(t, "some-archive.zip", parsed.Data.ArchiveName)
}

// Round 3: suspension blocks delivery without losing subscriptions, and only
// subscribed sockets count as revalidation candidates.
func TestSetSuspendedBlocksAndRestoresDelivery(t *testing.T) {
	m := NewWebSocketManager()
	ch := make(chan []byte, 4)
	id := m.RegisterClient(nil, 1, "admin", true, ch)
	require.True(t, m.Subscribe(id, TopicScan))

	m.SetSuspended(id, true)
	require.NoError(t, m.BroadcastToAdmins("scan_progress", nil))
	assert.Empty(t, ch, "a suspended client must not receive admin events")
	assert.True(t, m.HasAdminSubscriptions(id), "suspension keeps the subscription set")

	m.SetSuspended(id, false)
	require.NoError(t, m.BroadcastToAdmins("scan_progress", nil))
	require.Len(t, ch, 1, "delivery resumes with the subscription intact")
}

func TestHasAdminSubscriptionsFollowsTheTopicSet(t *testing.T) {
	m := NewWebSocketManager()
	ch := make(chan []byte, 4)
	id := m.RegisterClient(nil, 1, "admin", true, ch)

	assert.False(t, m.HasAdminSubscriptions(id))
	require.True(t, m.Subscribe(id, TopicScan))
	assert.True(t, m.HasAdminSubscriptions(id))

	m.Unsubscribe(id, TopicScan)
	assert.False(t, m.HasAdminSubscriptions(id))

	// A demotion clears the topics: nothing left to revalidate for.
	require.True(t, m.Subscribe(id, TopicScan))
	m.SetAdminRole(id, false)
	assert.False(t, m.HasAdminSubscriptions(id))
}
