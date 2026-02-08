package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

// wsTestHandler is a plain http.Handler that accepts a WebSocket, registers
// the client, and runs the same read/write loops as UnifiedWebSocketHandler
// but without going through gin's router (which wraps ResponseWriter and
// breaks http.Hijacker support needed by websocket.Accept).
func wsTestHandler(mgr *services.WebSocketManager, userID int64, username string, isAdmin bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opts := &websocket.AcceptOptions{InsecureSkipVerify: true}
		conn, err := websocket.Accept(w, r, opts)
		if err != nil {
			return
		}
		defer conn.CloseNow()

		notifyChan := make(chan []byte, 16)
		quit := make(chan struct{})

		clientID := mgr.RegisterClient(conn, userID, username, isAdmin, notifyChan)
		defer mgr.UnregisterClient(clientID)

		// Reader goroutine.
		go func() {
			for {
				typ, data, err := conn.Read(context.Background())
				if err != nil {
					close(quit)
					return
				}
				if typ != websocket.MessageText {
					continue
				}
				var typed struct {
					Type   string `json:"type"`
					BookID int64  `json:"bookID"`
					Format string `json:"format"`
				}
				if err := json.Unmarshal(data, &typed); err != nil {
					continue
				}
				if typed.Type == "ping" {
					response, _ := json.Marshal(map[string]string{"type": "pong"})
					notifyChan <- response
				}
			}
		}()

		// Writer loop.
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case message := <-notifyChan:
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := conn.Write(ctx, websocket.MessageText, message)
				cancel()
				if err != nil {
					return
				}
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := conn.Ping(ctx)
				cancel()
				if err != nil {
					return
				}
			case <-quit:
				conn.Close(websocket.StatusNormalClosure, "")
				return
			}
		}
	})
}

func setupTestServer(t *testing.T, userID int64, username string, isAdmin bool) (*httptest.Server, *services.WebSocketManager) {
	t.Helper()

	mgr := services.NewWebSocketManager()
	s := httptest.NewServer(wsTestHandler(mgr, userID, username, isAdmin))
	t.Cleanup(s.Close)
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
	conn := dialWS(t, s)

	// Give the handler time to register the client.
	time.Sleep(50 * time.Millisecond)

	err := mgr.BroadcastToAdmins("scan_started", map[string]int{"total": 10})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var msg map[string]interface{}
	err = wsjson.Read(ctx, conn, &msg)
	require.NoError(t, err)
	assert.Equal(t, "scan_started", msg["type"])

	data, ok := msg["data"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, float64(10), data["total"])
}

func TestWSHandler_RegularUserDoesNotReceiveBroadcast(t *testing.T) {
	s, mgr := setupTestServer(t, 2, "user", false)
	conn := dialWS(t, s)

	time.Sleep(50 * time.Millisecond)

	err := mgr.BroadcastToAdmins("scan_started", nil)
	require.NoError(t, err)

	// The user should not receive anything. Try reading with a short timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	var msg json.RawMessage
	err = wsjson.Read(ctx, conn, &msg)
	assert.Error(t, err)
}

// --- Multiple clients ---

func TestWSHandler_MultipleAdminsBroadcast(t *testing.T) {
	s, mgr := setupTestServer(t, 1, "admin", true)

	conn1 := dialWS(t, s)
	conn2 := dialWS(t, s)

	time.Sleep(50 * time.Millisecond)

	err := mgr.BroadcastToAdmins("test_event", "hello")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var msg1, msg2 map[string]interface{}
	require.NoError(t, wsjson.Read(ctx, conn1, &msg1))
	require.NoError(t, wsjson.Read(ctx, conn2, &msg2))
	assert.Equal(t, "test_event", msg1["type"])
	assert.Equal(t, "test_event", msg2["type"])
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
