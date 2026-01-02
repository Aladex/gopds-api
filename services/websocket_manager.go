package services

import (
	"encoding/json"
	"gopds-api/logging"
	"net"
	"sync"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

// WSClient represents a connected WebSocket client
type WSClient struct {
	Conn     net.Conn
	UserID   int64
	IsAdmin  bool
	Username string
	mu       sync.Mutex
}

// WebSocketManager manages WebSocket connections for admin notifications
type WebSocketManager struct {
	clients map[net.Conn]*WSClient
	mu      sync.RWMutex
}

// NewWebSocketManager creates a new WebSocket manager
func NewWebSocketManager() *WebSocketManager {
	return &WebSocketManager{
		clients: make(map[net.Conn]*WSClient),
	}
}

// RegisterClient registers a new WebSocket client
func (m *WebSocketManager) RegisterClient(conn net.Conn, userID int64, username string, isAdmin bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	client := &WSClient{
		Conn:     conn,
		UserID:   userID,
		IsAdmin:  isAdmin,
		Username: username,
	}

	m.clients[conn] = client
	logging.Infof("WebSocket client registered: user=%s (id=%d, admin=%v)", username, userID, isAdmin)
}

// UnregisterClient removes a WebSocket client
func (m *WebSocketManager) UnregisterClient(conn net.Conn) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if client, ok := m.clients[conn]; ok {
		logging.Infof("WebSocket client unregistered: user=%s (id=%d)", client.Username, client.UserID)
		delete(m.clients, conn)
	}
}

// BroadcastToAdmins sends a message to all connected admin clients
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

		client.mu.Lock()
		err := wsutil.WriteServerMessage(client.Conn, ws.OpText, jsonData)
		client.mu.Unlock()

		if err != nil {
			logging.Warnf("Failed to send message to admin %s: %v", client.Username, err)
			continue
		}
		adminCount++
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
