package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopds-api/services"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// wsTestHandler serves test connections, counting them in handlers so a
// test can wait for every one to end.
func wsTestHandler(mgr *services.WebSocketManager, userID int64, username string, isAdmin bool, handlers *sync.WaitGroup) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlers.Add(1)
		defer handlers.Done()
		opts := &websocket.AcceptOptions{InsecureSkipVerify: true}
		conn, err := websocket.Accept(w, r, opts)
		if err != nil {
			return
		}
		defer conn.CloseNow()

		serveWebSocket(mgr, conn, userID, username, isAdmin, "")
	})
}

// handlersEnd bounds the wait for a test's connections to end.
const handlersEnd = 5 * time.Second

func setupTestServer(t *testing.T, userID int64, username string, isAdmin bool) (*httptest.Server, *services.WebSocketManager) {
	t.Helper()

	// Tests run without Redis and without a database: the live authorization
	// hooks default to the handshake snapshot unless a test replaces them.
	// They are put back only once every connection of the test has ended
	// (the cleanup below runs first): a connection outliving its test read
	// them while the next test swapped them — a data race on master's CI.
	// A test that replaces them after this needs no cleanup of its own.
	previousRole := currentUserIsAdmin
	currentUserIsAdmin = func(int64) (bool, error) { return isAdmin, nil }
	previousSession := validateWSSession
	validateWSSession = func(string) error { return nil }
	previousInterval := wsRevalidateInterval
	t.Cleanup(func() {
		currentUserIsAdmin = previousRole
		validateWSSession = previousSession
		wsRevalidateInterval = previousInterval
	})

	mgr := services.NewWebSocketManager()
	var handlers sync.WaitGroup
	s := httptest.NewServer(wsTestHandler(mgr, userID, username, isAdmin, &handlers))
	t.Cleanup(func() {
		s.CloseClientConnections()
		s.Close()
		ended := make(chan struct{})
		go func() { handlers.Wait(); close(ended) }()
		select {
		case <-ended:
		case <-time.After(handlersEnd):
			t.Errorf("a test connection did not end within %s", handlersEnd)
		}
	})
	return s, mgr
}

func dialWS(t *testing.T, server *httptest.Server) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	require.NoError(t, err)
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

// --- Connection lifecycle ---

func TestWSHandler_ConnectAndDisconnect(t *testing.T) {
	s, _ := setupTestServer(t, 1, "alice", false)
	conn := dialWS(t, s)

	err := conn.Close(websocket.StatusNormalClosure, "bye")
	assert.NoError(t, err)
}

// --- Ping / Pong ---

func TestWSHandler_PingPong(t *testing.T) {
	s, _ := setupTestServer(t, 1, "alice", false)
	conn := dialWS(t, s)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := wsjson.Write(ctx, conn, map[string]string{"type": "ping"})
	require.NoError(t, err)

	var response map[string]string
	err = wsjson.Read(ctx, conn, &response)
	require.NoError(t, err)
	assert.Equal(t, "pong", response["type"])
}

// --- Admin broadcast ---

func TestWSHandler_AdminReceivesBroadcast(t *testing.T) {
	s, mgr := setupTestServer(t, 1, "admin", true)
	client := newWSTestClient(t, s)

	// Give the handler time to register the client.
	time.Sleep(50 * time.Millisecond)
	client.subscribeTopic(t, services.TopicScan)

	err := mgr.BroadcastToAdmins("scan_started", map[string]int{"total": 10})
	require.NoError(t, err)

	msg := client.read(t)
	assert.Equal(t, "scan_started", msg["type"])

	data, ok := msg["data"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, float64(10), data["total"])
}

func TestWSHandler_RegularUserDoesNotReceiveBroadcast(t *testing.T) {
	s, mgr := setupTestServer(t, 2, "user", false)
	client := newWSTestClient(t, s)

	time.Sleep(50 * time.Millisecond)

	err := mgr.BroadcastToAdmins("scan_started", nil)
	require.NoError(t, err)

	// The user should not receive anything.
	client.expectSilence(t)
}

// --- Multiple clients ---

func TestWSHandler_MultipleAdminsBroadcast(t *testing.T) {
	s, mgr := setupTestServer(t, 1, "admin", true)

	client1 := newWSTestClient(t, s)
	client2 := newWSTestClient(t, s)

	time.Sleep(50 * time.Millisecond)
	client1.subscribeTopic(t, services.TopicScan)
	client2.subscribeTopic(t, services.TopicScan)

	err := mgr.BroadcastToAdmins("scan_error", "hello")
	require.NoError(t, err)

	msg1 := client1.read(t)
	msg2 := client2.read(t)
	assert.Equal(t, "scan_error", msg1["type"])
	assert.Equal(t, "scan_error", msg2["type"])
}

// --- Origin validation (OriginCheckMiddleware) ---
// These tests exercise the middleware that runs BEFORE auth,
// ensuring evil origins get 403 regardless of authentication state.

// originTestRouter creates a gin router with OriginCheckMiddleware
// protecting a simple handler that returns 200.
func originTestRouter() *gin.Engine {
	r := gin.New()
	r.GET("/ws", OriginCheckMiddleware(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	return r
}

func TestOriginMiddleware_RejectsEvilOrigin(t *testing.T) {
	viper.Set("app.allowed_origins", []string{"good.example.com"})
	viper.Set("app.devel_mode", false)
	t.Cleanup(func() {
		viper.Set("app.allowed_origins", []string{})
	})

	r := originTestRouter()
	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Origin", "http://evil.com")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "origin not allowed")
}

func TestOriginMiddleware_AllowsConfiguredOrigin(t *testing.T) {
	viper.Set("app.allowed_origins", []string{"good.example.com"})
	viper.Set("app.devel_mode", false)
	t.Cleanup(func() {
		viper.Set("app.allowed_origins", []string{})
	})

	r := originTestRouter()
	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Origin", "http://good.example.com")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestOriginMiddleware_AllowsSameOrigin(t *testing.T) {
	viper.Set("app.allowed_origins", []string{})
	viper.Set("app.devel_mode", false)

	r := originTestRouter()
	req := httptest.NewRequest("GET", "/ws", nil)
	req.Host = "myapp.example.com"
	req.Header.Set("Origin", "http://myapp.example.com")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestOriginMiddleware_AllowsNoOriginHeader(t *testing.T) {
	viper.Set("app.allowed_origins", []string{})
	viper.Set("app.devel_mode", false)

	r := originTestRouter()
	req := httptest.NewRequest("GET", "/ws", nil)
	// No Origin header — same-origin or non-browser client.
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestOriginMiddleware_DevelModeAllowsLocalhost(t *testing.T) {
	viper.Set("app.allowed_origins", []string{})
	viper.Set("app.devel_mode", true)
	t.Cleanup(func() {
		viper.Set("app.devel_mode", false)
	})

	r := originTestRouter()
	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestOriginMiddleware_DevelModeRejectsOtherOrigins(t *testing.T) {
	viper.Set("app.allowed_origins", []string{})
	viper.Set("app.devel_mode", true)
	t.Cleanup(func() {
		viper.Set("app.devel_mode", false)
	})

	r := originTestRouter()
	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Origin", "http://evil.com")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestOriginMiddleware_OriginWithSchemePattern(t *testing.T) {
	viper.Set("app.allowed_origins", []string{"https://secure.example.com"})
	viper.Set("app.devel_mode", false)
	t.Cleanup(func() {
		viper.Set("app.allowed_origins", []string{})
	})

	r := originTestRouter()

	// HTTPS origin matching HTTPS pattern — allowed.
	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Origin", "https://secure.example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// HTTP origin not matching HTTPS pattern — rejected.
	req2 := httptest.NewRequest("GET", "/ws", nil)
	req2.Header.Set("Origin", "http://secure.example.com")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusForbidden, w2.Code)
}

// --- Unknown message ---

func TestWSHandler_UnknownMessageDoesNotCrash(t *testing.T) {
	s, _ := setupTestServer(t, 1, "alice", false)
	conn := dialWS(t, s)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Send a message with no recognized type/format.
	err := wsjson.Write(ctx, conn, map[string]string{"type": "unknown_action"})
	require.NoError(t, err)

	// Connection should stay alive. Send a ping to verify.
	err = wsjson.Write(ctx, conn, map[string]string{"type": "ping"})
	require.NoError(t, err)

	var resp map[string]string
	err = wsjson.Read(ctx, conn, &resp)
	require.NoError(t, err)
	assert.Equal(t, "pong", resp["type"])
}
