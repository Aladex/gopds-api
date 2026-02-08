package api

import (
	"context"
	"encoding/json"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"

	"gopds-api/logging"
)

// SetupUnifiedWebSocketRoute registers the unified WebSocket endpoint.
func SetupUnifiedWebSocketRoute(r *gin.RouterGroup) {
	r.GET("/ws", UnifiedWebSocketHandler)
}

// UnifiedWebSocketHandler handles WebSocket connections for all users.
// Regular users receive book conversion notifications.
// Admin users additionally receive scan progress and duplicate events.
func UnifiedWebSocketHandler(c *gin.Context) {
	username, _ := c.Get("username")
	userIDVal, _ := c.Get("user_id")
	isSuperVal, _ := c.Get("is_superuser")

	userID, _ := userIDVal.(int64)
	isSuperUser, _ := isSuperVal.(bool)
	user, _ := username.(string)

	conn, err := upgradeWithOriginCheck(c)
	if err != nil {
		logging.Errorf("WebSocket upgrade failed for user %s: %v", user, err)
		return
	}
	defer conn.CloseNow()

	notifyChan := make(chan []byte, 16)
	quit := make(chan struct{})

	var clientID uint64
	if wsManager != nil {
		clientID = wsManager.RegisterClient(conn, userID, user, isSuperUser, notifyChan)
		defer wsManager.UnregisterClient(clientID)
	}

	logging.Infof("WebSocket connected: user=%s (id=%d, admin=%v)", user, userID, isSuperUser)

	// Reader goroutine: handles incoming messages from the client.
	go func() {
		for {
			typ, data, err := conn.Read(context.Background())
			if err != nil {
				logging.Warnf("WebSocket read error for user %s: %v", user, err)
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
				logging.Warnf("Failed to parse WebSocket message from %s: %v", user, err)
				continue
			}

			switch {
			case typed.Type == "ping":
				response, _ := json.Marshal(map[string]string{"type": "pong"})
				notifyChan <- response

			case typed.Format == "mobi" || typed.Format == "epub":
				handleConversionRequest(typed.BookID, typed.Format, notifyChan)

			default:
				logging.Infof("Unknown WebSocket message from %s: %s", user, string(data))
			}
		}
	}()

	// Writer loop: the single goroutine that writes to the connection.
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case message := <-notifyChan:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := conn.Write(ctx, websocket.MessageText, message)
			cancel()
			if err != nil {
				logging.Warnf("WebSocket write error for user %s: %v", user, err)
				return
			}
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := conn.Ping(ctx)
			cancel()
			if err != nil {
				logging.Warnf("WebSocket ping error for user %s: %v", user, err)
				return
			}
		case <-quit:
			logging.Infof("WebSocket closed for user %s", user)
			conn.Close(websocket.StatusNormalClosure, "")
			return
		}
	}
}

// handleConversionRequest starts a book conversion in a goroutine and sends
// the result to notifyChan when done.
func handleConversionRequest(bookID int64, format string, notifyChan chan []byte) {
	go func() {
		var err error
		switch format {
		case "mobi":
			err = ConvertBookToMobi(bookID)
		case "epub":
			err = ConvertBookToEpub(bookID)
		default:
			logging.Warnf("Unsupported conversion format: %s", format)
			return
		}

		result := map[string]interface{}{
			"bookID": bookID,
			"format": format,
		}

		if err != nil {
			logging.Errorf("Failed to convert book %d to %s: %v", bookID, format, err)
			result["status"] = "error"
			result["error"] = err.Error()
		} else {
			result["status"] = "ready"
		}

		message, _ := json.Marshal(result)
		notifyChan <- message
	}()
}
