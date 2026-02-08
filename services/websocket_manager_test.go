package services

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWebSocketManager(t *testing.T) {
	m := NewWebSocketManager()
	require.NotNil(t, m)
	assert.Empty(t, m.clients)
}

func TestRegisterAndUnregisterClient(t *testing.T) {
	m := NewWebSocketManager()
	ch := make(chan []byte, 1)

	id := m.RegisterClient(nil, 42, "alice", false, ch)
	assert.NotZero(t, id)

	// Client should be in the map.
	m.mu.RLock()
	client, ok := m.clients[id]
	m.mu.RUnlock()
	require.True(t, ok)
	assert.Equal(t, int64(42), client.UserID)
	assert.Equal(t, "alice", client.Username)
	assert.False(t, client.IsAdmin)

	// Unregister.
	m.UnregisterClient(id)
	m.mu.RLock()
	_, ok = m.clients[id]
	m.mu.RUnlock()
	assert.False(t, ok)
}

func TestUnregisterNonexistentClient(t *testing.T) {
	m := NewWebSocketManager()
	// Should not panic.
	m.UnregisterClient(99999)
}

func TestRegisterMultipleClients_UniqueIDs(t *testing.T) {
	m := NewWebSocketManager()
	ch1 := make(chan []byte, 1)
	ch2 := make(chan []byte, 1)

	id1 := m.RegisterClient(nil, 1, "user1", false, ch1)
	id2 := m.RegisterClient(nil, 2, "user2", true, ch2)
	assert.NotEqual(t, id1, id2)
}

func TestBroadcastToAdmins_OnlyAdmins(t *testing.T) {
	m := NewWebSocketManager()
	adminCh := make(chan []byte, 4)
	userCh := make(chan []byte, 4)

	m.RegisterClient(nil, 1, "admin", true, adminCh)
	m.RegisterClient(nil, 2, "user", false, userCh)

	err := m.BroadcastToAdmins("scan_progress", map[string]int{"done": 5})
	require.NoError(t, err)

	// Admin should have received a message.
	require.Len(t, adminCh, 1)
	msg := <-adminCh
	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(msg, &parsed))
	assert.Equal(t, "scan_progress", parsed["type"])

	// Regular user should not have received anything.
	assert.Empty(t, userCh)
}

func TestBroadcastToAdmins_MultipleAdmins(t *testing.T) {
	m := NewWebSocketManager()
	ch1 := make(chan []byte, 4)
	ch2 := make(chan []byte, 4)

	m.RegisterClient(nil, 1, "admin1", true, ch1)
	m.RegisterClient(nil, 2, "admin2", true, ch2)

	err := m.BroadcastToAdmins("test", "hello")
	require.NoError(t, err)

	assert.Len(t, ch1, 1)
	assert.Len(t, ch2, 1)
}

func TestBroadcastToAdmins_FullChannelDropsMessage(t *testing.T) {
	m := NewWebSocketManager()
	// Buffer of 1, fill it up first.
	ch := make(chan []byte, 1)
	ch <- []byte("blocking")

	m.RegisterClient(nil, 1, "admin", true, ch)

	// Should not block and should not error.
	err := m.BroadcastToAdmins("test", "data")
	require.NoError(t, err)

	// Channel still has only the original message.
	assert.Len(t, ch, 1)
}

func TestBroadcastToAdmins_NoClients(t *testing.T) {
	m := NewWebSocketManager()
	err := m.BroadcastToAdmins("test", nil)
	require.NoError(t, err)
}

func TestGetAdminCount(t *testing.T) {
	m := NewWebSocketManager()
	assert.Equal(t, 0, m.GetAdminCount())

	ch1 := make(chan []byte, 1)
	ch2 := make(chan []byte, 1)
	ch3 := make(chan []byte, 1)

	m.RegisterClient(nil, 1, "admin1", true, ch1)
	m.RegisterClient(nil, 2, "user", false, ch2)
	id3 := m.RegisterClient(nil, 3, "admin2", true, ch3)

	assert.Equal(t, 2, m.GetAdminCount())

	m.UnregisterClient(id3)
	assert.Equal(t, 1, m.GetAdminCount())
}

func TestAdminWSConnection_SendMessage(t *testing.T) {
	m := NewWebSocketManager()
	ch := make(chan []byte, 4)
	m.RegisterClient(nil, 1, "admin", true, ch)

	wsConn := NewAdminWSConnection(m)
	err := wsConn.SendMessage("scan_done", map[string]bool{"ok": true})
	require.NoError(t, err)

	require.Len(t, ch, 1)
	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(<-ch, &parsed))
	assert.Equal(t, "scan_done", parsed["type"])
}

func TestConcurrentRegisterUnregister(t *testing.T) {
	m := NewWebSocketManager()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			ch := make(chan []byte, 1)
			id := m.RegisterClient(nil, int64(n), "user", n%2 == 0, ch)
			m.UnregisterClient(id)
		}(i)
	}
	wg.Wait()

	m.mu.RLock()
	assert.Empty(t, m.clients)
	m.mu.RUnlock()
}

func TestConcurrentBroadcast(t *testing.T) {
	m := NewWebSocketManager()
	channels := make([]chan []byte, 10)
	for i := 0; i < 10; i++ {
		channels[i] = make(chan []byte, 100)
		m.RegisterClient(nil, int64(i), "admin", true, channels[i])
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_ = m.BroadcastToAdmins("event", n)
		}(i)
	}
	wg.Wait()

	for _, ch := range channels {
		assert.Equal(t, 50, len(ch))
	}
}
