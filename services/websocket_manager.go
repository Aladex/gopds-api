package services

import (
	"encoding/json"
	"gopds-api/logging"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
)

// WSClient represents a connected WebSocket client
type WSClient struct {
	Conn       *websocket.Conn
	ID         uint64
	UserID     int64
	IsAdmin    bool
	Username   string
	NotifyChan chan []byte
}

var clientIDCounter uint64

// WebSocketManager manages WebSocket connections for admin notifications
type WebSocketManager struct {
	clients map[uint64]*WSClient
	mu      sync.RWMutex
}

// NewWebSocketManager creates a new WebSocket manager
func NewWebSocketManager() *WebSocketManager {
	return &WebSocketManager{
		clients: make(map[uint64]*WSClient),
	}
}

// RegisterClient registers a new WebSocket client and returns its unique ID
func (m *WebSocketManager) RegisterClient(conn *websocket.Conn, userID int64, username string, isAdmin bool, notifyChan chan []byte) uint64 {
	id := atomic.AddUint64(&clientIDCounter, 1)

	m.mu.Lock()
	defer m.mu.Unlock()

	client := &WSClient{
		Conn:       conn,
		ID:         id,
		UserID:     userID,
		IsAdmin:    isAdmin,
		Username:   username,
		NotifyChan: notifyChan,
	}

	m.clients[id] = client
	logging.Infof("WebSocket client registered: user=%s (id=%d, admin=%v)", username, userID, isAdmin)
	return id
}

// UnregisterClient removes a WebSocket client by its unique ID
func (m *WebSocketManager) UnregisterClient(id uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if client, ok := m.clients[id]; ok {
		logging.Infof("WebSocket client unregistered: user=%s (id=%d)", client.Username, client.UserID)
		delete(m.clients, id)
	}
}

// BroadcastToAdmins sends a message to all connected admin clients via their NotifyChan.
// The actual write to the WebSocket connection happens in the handler's writer goroutine,
// which eliminates concurrent writes to the same connection.
func (m *WebSocketManager) BroadcastToAdmins(messageType string, data interface{}) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	message := map[string]interface{}{
		"type": messageType,
		"data": data,
	}

	jsonData, err := json.Marshal(message)
	if err != nil {
		logging.Errorf("Failed to marshal WebSocket message: %v", err)
		return err
	}

	adminCount := 0
	for _, client := range m.clients {
		if !client.IsAdmin {
			continue
		}

		select {
		case client.NotifyChan <- jsonData:
			adminCount++
		default:
			logging.Warnf("NotifyChan full for admin %s, dropping message", client.Username)
		}
	}

	logging.Infof("Broadcasted %s to %d admin clients", messageType, adminCount)
	return nil
}

// GetAdminCount returns the number of connected admin clients
func (m *WebSocketManager) GetAdminCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, client := range m.clients {
		if client.IsAdmin {
			count++
		}
	}
	return count
}

// AdminWSConnection wraps WebSocketManager to implement WebSocketConnection interface
type AdminWSConnection struct {
	manager *WebSocketManager
}

// NewAdminWSConnection creates a new admin WebSocket connection wrapper
func NewAdminWSConnection(manager *WebSocketManager) *AdminWSConnection {
	return &AdminWSConnection{
		manager: manager,
	}
}

// SendMessage implements the WebSocketConnection interface
func (c *AdminWSConnection) SendMessage(messageType string, data interface{}) error {
	return c.manager.BroadcastToAdmins(messageType, data)
}
