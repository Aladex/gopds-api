package services

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"gopds-api/logging"

	"github.com/coder/websocket"
)

// Broadcast topics. A client receives a topic's events only when it is an
// admin AND subscribed to the topic.
const (
	TopicScan           = "scan"
	TopicFixScan        = "fix_scan"
	TopicDuplicates     = "duplicates"
	TopicGenres         = "genres"
	TopicAuthorMetadata = "author_metadata"
)

// topicForMessageType assigns every admin event type its topic. A type that
// is not listed here is a programming error at the call site and is rejected
// by BroadcastToAdmins.
var topicForMessageType = map[string]string{
	ScanStarted:        TopicScan,
	ArchiveStarted:     TopicScan,
	ArchiveCompleted:   TopicScan,
	ArchiveResetType:   TopicScan,
	ScanCompleted:      TopicScan,
	ScanErrorEventType: TopicScan,
	ScanProgress:       TopicScan,

	FixScanStartedType:   TopicFixScan,
	FixScanProgressType:  TopicFixScan,
	FixScanCompletedType: TopicFixScan,
	FixScanErrorType:     TopicFixScan,

	DuplicateScanProgressType: TopicDuplicates,

	GenreTitleGenStartedType:   TopicGenres,
	GenreTitleGenProgressType:  TopicGenres,
	GenreTitleGenCompletedType: TopicGenres,
}

// knownTopics is the set a client may subscribe to. TopicAuthorMetadata is
// accepted already so the client can subscribe before the first event of that
// kind exists.
var knownTopics = map[string]bool{
	TopicScan:           true,
	TopicFixScan:        true,
	TopicDuplicates:     true,
	TopicGenres:         true,
	TopicAuthorMetadata: true,
}

// IsKnownTopic reports whether topic is one a client may subscribe to.
func IsKnownTopic(topic string) bool {
	return knownTopics[topic]
}

// WSClient represents a connected WebSocket client
type WSClient struct {
	Conn       *websocket.Conn
	ID         uint64
	UserID     int64
	IsAdmin    bool
	Username   string
	NotifyChan chan []byte

	// topics is the client's subscription set, guarded by the manager's mutex.
	topics map[string]bool
	// suspended blocks admin delivery while the periodic revalidation cannot
	// establish the session or the role (an infrastructure error, not a
	// confirmed loss). Subscriptions are kept and resume with the first
	// successful check. Guarded by the manager's mutex.
	suspended bool
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
		topics:     make(map[string]bool),
	}

	m.clients[id] = client
	// The handler logs one Info line per connect; this is the same event.
	logging.Debugf("WebSocket client registered: id=%d (user_id=%d, admin=%v)", id, userID, isAdmin)
	return id
}

// UnregisterClient removes a WebSocket client by its unique ID
func (m *WebSocketManager) UnregisterClient(id uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if client, ok := m.clients[id]; ok {
		logging.Debugf("WebSocket client unregistered: id=%d (user_id=%d)", client.ID, client.UserID)
		delete(m.clients, id)
	}
}

// Subscribe adds topic to the client's subscription set. It returns false
// when the client is gone or the topic is not one a client may subscribe to.
// Admin enforcement is the handler's job: it never lets a non-admin reach
// this point, and BroadcastToAdmins checks IsAdmin again at delivery time.
func (m *WebSocketManager) Subscribe(id uint64, topic string) bool {
	if !knownTopics[topic] {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	client, ok := m.clients[id]
	if !ok {
		return false
	}
	client.topics[topic] = true
	return true
}

// Unsubscribe removes topic from the client's subscription set.
func (m *WebSocketManager) Unsubscribe(id uint64, topic string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if client, ok := m.clients[id]; ok {
		delete(client.topics, topic)
	}
}

// SetAdminRole updates the client's cached role to the current one. Losing
// the role drops every subscription at once: delivery checks IsAdmin, so
// admin events stop with the same lock acquisition.
func (m *WebSocketManager) SetAdminRole(id uint64, isAdmin bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if client, ok := m.clients[id]; ok {
		client.IsAdmin = isAdmin
		if !isAdmin {
			client.topics = make(map[string]bool)
		}
	}
}

// RevokeAdminByUserID drops the admin role on every socket of the user. The
// user-update path calls it when is_superuser flips off, so a demotion takes
// effect without waiting for the next revalidation tick.
func (m *WebSocketManager) RevokeAdminByUserID(userID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, client := range m.clients {
		if client.UserID == userID && client.IsAdmin {
			client.IsAdmin = false
			client.topics = make(map[string]bool)
			logging.Debugf("WebSocket admin role revoked: client_id=%d user_id=%d", client.ID, userID)
		}
	}
}

// HasAdminSubscriptions reports whether the client holds at least one topic
// subscription. Only those sockets participate in the periodic session/role
// revalidation: a socket with nothing admin to receive has nothing to
// protect, and HTTP guards its requests.
func (m *WebSocketManager) HasAdminSubscriptions(id uint64) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	client, ok := m.clients[id]
	return ok && len(client.topics) > 0
}

// SetSuspended blocks or restores admin delivery for the client without
// touching its subscriptions: a revalidation that could not reach the
// session store or the database fails closed, and the next successful check
// resumes delivery exactly where it left off.
func (m *WebSocketManager) SetSuspended(id uint64, suspended bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if client, ok := m.clients[id]; ok {
		client.suspended = suspended
	}
}

// BroadcastToAdmins sends a message to the admin clients subscribed to the
// event's topic via their NotifyChan. The actual write to the WebSocket
// connection happens in the handler's writer goroutine, which eliminates
// concurrent writes to the same connection. An unknown message type is an
// error: every admin event must declare its topic.
func (m *WebSocketManager) BroadcastToAdmins(messageType string, data interface{}) error {
	topic, ok := topicForMessageType[messageType]
	if !ok {
		return fmt.Errorf("unknown websocket message type %q", messageType)
	}

	message := map[string]interface{}{
		"type":  messageType,
		"topic": topic,
		"data":  data,
	}

	jsonData, err := json.Marshal(message)
	if err != nil {
		logging.Errorf("Failed to marshal WebSocket message: %v", err)
		return err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	sentCount := 0
	for _, client := range m.clients {
		if !client.IsAdmin || client.suspended || !client.topics[topic] {
			continue
		}

		select {
		case client.NotifyChan <- jsonData:
			sentCount++
		default:
			logging.Warnf("NotifyChan full for client id=%d, dropping message", client.ID)
		}
	}

	logging.Debugf("Broadcasted %s (topic %s) to %d clients", messageType, topic, sentCount)
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
