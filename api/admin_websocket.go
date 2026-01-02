package api

import (
	"encoding/json"
	"gopds-api/logging"
	"gopds-api/middlewares"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

// SetupAdminWebSocketRoute sets up the admin WebSocket route
func SetupAdminWebSocketRoute(r *gin.RouterGroup) {
	r.GET("/ws", AdminWebSocketHandler)
}

// AdminWebSocketHandler handles WebSocket connections for admin notifications
func AdminWebSocketHandler(c *gin.Context) {
	// Authenticate user via middleware helpers
	var token string
	var err error

	// Try to get token from header or cookie
	authHeader := c.GetHeader("Authorization")
	if authHeader != "" {
		token = authHeader
	} else {
		token, err = c.Cookie("token")
		if err != nil || token == "" {
			c.AbortWithStatusJSON(401, gin.H{"error": "unauthorized"})
			return
		}
	}

	// Validate token using middleware's validateToken function
	username, userID, isSuperUser, err := middlewares.ValidateTokenPublic(token)
	if err != nil {
		c.AbortWithStatusJSON(401, gin.H{"error": "invalid_token"})
		return
	}

	// Only admins can connect
	if !isSuperUser {
		c.AbortWithStatusJSON(403, gin.H{"error": "admin_required"})
		return
	}

	// Upgrade to WebSocket
	conn, _, _, err := ws.UpgradeHTTP(c.Request, c.Writer)
	if err != nil {
		logging.Errorf("Failed to upgrade to WebSocket: %v", err)
		return
	}
	defer conn.Close()

	// Register client with WebSocket manager
	if wsManager != nil {
		wsManager.RegisterClient(conn, userID, username, isSuperUser)
		defer wsManager.UnregisterClient(conn)
	}

	logging.Infof("Admin WebSocket connected: user=%s (id=%d)", username, userID)

	// Create channels for control
	quit := make(chan struct{})

	// Ticker for ping messages
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Read messages from client
	go func() {
		for {
			msg, op, err := wsutil.ReadClientData(conn)
			if err != nil {
				logging.Warnf("Admin WebSocket read error for user %s: %v", username, err)
				close(quit)
				return
			}

			// Process text messages
			if op == ws.OpText {
				logging.Infof("Admin WebSocket message from %s: %s", username, string(msg))

				// Parse message
				var request map[string]interface{}
				if err := json.Unmarshal(msg, &request); err != nil {
					logging.Warnf("Failed to parse WebSocket message: %v", err)
					continue
				}

				// Handle different message types (can be extended)
				msgType, ok := request["type"].(string)
				if !ok {
					continue
				}

				switch msgType {
				case "ping":
					// Respond with pong
					response := map[string]string{"type": "pong"}
					jsonData, _ := json.Marshal(response)
					_ = wsutil.WriteServerMessage(conn, ws.OpText, jsonData)
				default:
					logging.Infof("Unknown WebSocket message type: %s", msgType)
				}
			}
		}
	}()

	// Send periodic pings
	for {
		select {
		case <-ticker.C:
			if err := wsutil.WriteServerMessage(conn, ws.OpPing, nil); err != nil {
				logging.Warnf("Error sending ping to admin %s: %v", username, err)
				return
			}
		case <-quit:
			logging.Infof("Admin WebSocket closed for user %s", username)
			return
		}
	}
}
