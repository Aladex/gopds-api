package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/middlewares"
	"gopds-api/services"
)

// UnifiedWebSocketHandler handles WebSocket connections for all users.
// Regular users receive book conversion notifications.
// Admin users additionally subscribe to topic events (scan, fix_scan,
// duplicates, genres).
func UnifiedWebSocketHandler(c *gin.Context) {
	username, _ := c.Get("username")
	userIDVal, _ := c.Get("user_id")
	isSuperVal, _ := c.Get("is_superuser")

	userID, _ := userIDVal.(int64)
	isSuperUser, _ := isSuperVal.(bool)
	user, _ := username.(string)

	conn, err := upgradeWebSocket(c)
	if err != nil {
		logging.Errorf("WebSocket upgrade failed: id=%d err=%v", userID, err)
		return
	}
	defer func() { _ = conn.CloseNow() }()

	logging.Infof("WebSocket connected: id=%d admin=%v", userID, isSuperUser)
	serveWebSocket(wsManager, conn, userID, user, isSuperUser, wsAuthToken(c))
}

// wsAuthToken extracts the session token the same way AuthMiddleware does, so
// the connection can re-validate the session while it lives.
func wsAuthToken(c *gin.Context) string {
	if authHeader := c.GetHeader("Authorization"); authHeader != "" {
		return authHeader
	}
	token, _ := c.Cookie("token")
	return token
}

// The outside world behind the socket's authorization checks, swappable in
// tests. The role is read fresh from the database, mirroring what an HTTP
// request would face.
var (
	currentUserIsAdmin = database.IsSuperUserByID
	// wsRevalidateInterval is how often a socket holding admin subscriptions
	// re-checks the session and the role. Tests shrink it.
	wsRevalidateInterval = 60 * time.Second
)

// validateWSSession checks the session token the socket was opened with and
// classifies the failure: a confirmed miss closes the socket, an
// infrastructure error only suspends admin delivery until the next tick.
var validateWSSession = middlewares.ValidateWSSession

// isNormalWSClose reports whether a read error is just the peer leaving the
// usual way: a clean close frame, or a dropped TCP connection without one.
// Those are routine, not warnings.
func isNormalWSClose(err error) bool {
	if errors.Is(err, io.EOF) {
		return true
	}
	code := websocket.CloseStatus(err)
	return code == websocket.StatusNormalClosure ||
		code == websocket.StatusGoingAway ||
		code == websocket.StatusNoStatusRcvd
}

// clientMessage is what a client may send over the socket.
type clientMessage struct {
	Type   string `json:"type"`
	Topic  string `json:"topic"`
	BookID int64  `json:"bookID"`
	Format string `json:"format"`
}

// Wire vocabulary of the socket: inbound client message types, outbound reply
// types and the JSON keys every frame shares.
const (
	wsMsgPing        = "ping"
	wsMsgSubscribe   = "subscribe"
	wsMsgUnsubscribe = "unsubscribe"
	wsMsgConvert     = "convert"

	wsMsgPong           = "pong"
	wsMsgSubscribed     = "subscribed"
	wsMsgUnsubscribed   = "unsubscribed"
	wsMsgSubscribeError = "subscribe_error"
	wsMsgConversion     = "conversion"

	wsKeyType   = "type"
	wsKeyTopic  = "topic"
	wsKeyBookID = "bookID"
	wsKeyFormat = "format"
	wsKeyStatus = "status"

	wsFormatMobi = "mobi"
	wsFormatEpub = "epub"
)

// wsSend delivers one reply frame unless the connection is already going
// away. A client that stops reading must never pin the goroutine answering
// its control frames.
func wsSend(notifyChan chan []byte, done <-chan struct{}, msg []byte) bool {
	select {
	case notifyChan <- msg:
		return true
	case <-done:
		return false
	}
}

// serveWebSocket runs the read/write loops of one connection until the peer
// goes away. It is shared by UnifiedWebSocketHandler and the tests. The
// manager may be nil, in which case the connection only serves its own
// request/reply traffic.
func serveWebSocket(mgr *services.WebSocketManager, conn *websocket.Conn, userID int64, username string, isAdmin bool, token string) {
	notifyChan := make(chan []byte, 16)
	quit := make(chan struct{})
	// done closes when the connection tears down in any direction; every
	// reply send selects on it.
	done := make(chan struct{})
	defer close(done)
	defer func() { _ = conn.CloseNow() }()

	var clientID uint64
	if mgr != nil {
		clientID = mgr.RegisterClient(conn, userID, username, isAdmin, notifyChan)
		defer mgr.UnregisterClient(clientID)
	}

	// Reader goroutine: handles incoming messages from the client.
	go func() {
		for {
			typ, data, err := conn.Read(context.Background())
			if err != nil {
				// A normal tab close is routine: Debug at most, and the
				// numeric id rather than a user name.
				if isNormalWSClose(err) {
					logging.Debugf("WebSocket peer gone: id=%d", userID)
				} else {
					logging.Warnf("WebSocket read error: id=%d err=%v", userID, err)
				}
				close(quit)
				return
			}

			if typ != websocket.MessageText {
				continue
			}

			var typed clientMessage
			if err := json.Unmarshal(data, &typed); err != nil {
				logging.Warnf("Failed to parse WebSocket message: id=%d err=%v", userID, err)
				continue
			}

			if !handleClientMessage(mgr, clientID, userID, typed, len(data), notifyChan, done) {
				return
			}
		}
	}()

	// The writer loop is the connection's owner: it returns when the
	// connection ends, whatever ended it.
	runWriterLoop(mgr, conn, userID, clientID, token, notifyChan, quit)
}

// runWriterLoop is the single goroutine that writes to the connection. It
// also runs the periodic session/role revalidation for sockets that hold
// admin subscriptions: the socket must not outlive the authorization it was
// opened with. A confirmed-missing session closes it; a confirmed role loss
// drops the subscriptions; an inconclusive check (session store or database
// unreachable) only suspends admin delivery until a tick succeeds — an
// outage is not an authorization decision.
func runWriterLoop(
	mgr *services.WebSocketManager,
	conn *websocket.Conn,
	userID int64,
	clientID uint64,
	token string,
	notifyChan chan []byte,
	quit chan struct{},
) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	revalidate := time.NewTicker(wsRevalidateInterval)
	defer revalidate.Stop()

	for {
		select {
		case message := <-notifyChan:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := conn.Write(ctx, websocket.MessageText, message)
			cancel()
			if err != nil {
				logging.Warnf("WebSocket write error: id=%d err=%v", userID, err)
				return
			}
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := conn.Ping(ctx)
			cancel()
			if err != nil {
				logging.Warnf("WebSocket ping error: id=%d err=%v", userID, err)
				return
			}
		case <-revalidate.C:
			// Regular users' sockets carry only their own request/reply
			// traffic, which HTTP already guards: nothing to revalidate.
			if mgr == nil || !mgr.HasAdminSubscriptions(clientID) {
				continue
			}
			if err := validateWSSession(token); err != nil {
				if errors.Is(err, middlewares.ErrSessionMissing) {
					logging.Infof("WebSocket session no longer valid, closing: id=%d", userID)
					return
				}
				// The session store did not answer: suspend delivery, keep
				// the socket, retry on the next tick.
				logging.Warnf("WebSocket session check inconclusive, suspending: id=%d err=%v", userID, err)
				mgr.SetSuspended(clientID, true)
				continue
			}
			adminNow, err := currentUserIsAdmin(userID)
			if err != nil {
				logging.Warnf("WebSocket role re-check failed, suspending: id=%d err=%v", userID, err)
				mgr.SetSuspended(clientID, true)
				continue
			}
			mgr.SetAdminRole(clientID, adminNow)
			mgr.SetSuspended(clientID, false)
		case <-quit:
			logging.Infof("WebSocket closed: id=%d", userID)
			// The peer is already going away; a failure to say so
			// politely changes nothing on this side.
			_ = conn.Close(websocket.StatusNormalClosure, "")
			return
		}
	}
}

// handleClientMessage dispatches one parsed client frame. It returns false
// when the connection died mid-reply and the reader should stop.
func handleClientMessage(
	mgr *services.WebSocketManager,
	clientID uint64,
	userID int64,
	typed clientMessage,
	frameSize int,
	notifyChan chan []byte,
	done <-chan struct{},
) bool {
	// The pre-topics frontend sends its conversion request as {bookID, format}
	// with no `type`; old tabs and cached frontends keep that shape for one
	// release of rollout. Normalize it to the typed frame. Control frames
	// without a recognized type stay rejected.
	frameType := typed.Type
	if frameType == "" && (typed.Format == wsFormatMobi || typed.Format == wsFormatEpub) {
		frameType = wsMsgConvert
	}

	switch frameType {
	case wsMsgPing:
		response, _ := json.Marshal(map[string]string{wsKeyType: wsMsgPong})
		return wsSend(notifyChan, done, response)

	case wsMsgSubscribe:
		return handleSubscribe(mgr, clientID, userID, typed.Topic, notifyChan, done)

	case wsMsgUnsubscribe:
		if mgr != nil {
			mgr.Unsubscribe(clientID, typed.Topic)
		}
		response, _ := json.Marshal(map[string]string{wsKeyType: wsMsgUnsubscribed, wsKeyTopic: typed.Topic})
		return wsSend(notifyChan, done, response)

	case wsMsgConvert:
		handleConversionRequest(typed.BookID, typed.Format, notifyChan, done)
		return true

	default:
		// The frame is whatever the client sent. Its size and the fields that
		// were recognized are what debugging needs; the payload itself is not
		// ours to write down.
		logging.Debugf("Unknown WebSocket message: id=%d %d bytes, type=%q, format=%q",
			userID, frameSize, typed.Type, typed.Format)
		return true
	}
}

// handleSubscribe records a topic subscription for the connection. The role
// is read fresh from the database, never trusted from the handshake, and a
// failed lookup fails closed. A refused subscription is never stored, and
// nothing is ever delivered for it.
func handleSubscribe(
	mgr *services.WebSocketManager,
	clientID uint64,
	userID int64,
	topic string,
	notifyChan chan []byte,
	done <-chan struct{},
) bool {
	refuse := func(reason string) bool {
		response, _ := json.Marshal(map[string]string{wsKeyType: wsMsgSubscribeError, wsKeyTopic: topic, jsonKeyError: reason})
		return wsSend(notifyChan, done, response)
	}

	if mgr == nil {
		return refuse("unavailable")
	}
	if !services.IsKnownTopic(topic) {
		return refuse("unknown_topic")
	}
	// Every topic so far carries admin data. No user identifiers in the log:
	// the refusal says enough without them.
	adminNow, err := currentUserIsAdmin(userID)
	if err != nil {
		logging.Warnf("Role check failed, refusing subscription: topic=%q client_id=%d", topic, clientID)
		return refuse("unavailable")
	}
	if !adminNow {
		logging.Warnf("Refused admin topic subscription: topic=%q client_id=%d", topic, clientID)
		return refuse("forbidden")
	}
	if !mgr.Subscribe(clientID, topic) {
		return refuse("unavailable")
	}
	response, _ := json.Marshal(map[string]string{wsKeyType: wsMsgSubscribed, wsKeyTopic: topic})
	return wsSend(notifyChan, done, response)
}

// convertBookToMobi and convertBookToEpub are the conversion entry points;
// tests replace them to keep the reply envelope off the database.
var (
	convertBookToMobi = ConvertBookToMobi
	convertBookToEpub = ConvertBookToEpub
)

// handleConversionRequest starts a book conversion in a goroutine and sends
// the result to notifyChan when done.
func handleConversionRequest(bookID int64, format string, notifyChan chan []byte, done <-chan struct{}) {
	go sendConversionReply(bookID, format, notifyChan, done)
}

// sendConversionReply runs the conversion and delivers the reply. The reply
// send gives up when the connection dies first, so a client that stopped
// reading never pins this goroutine.
func sendConversionReply(bookID int64, format string, notifyChan chan []byte, done <-chan struct{}) {
	var err error
	switch format {
	case wsFormatMobi:
		err = convertBookToMobi(bookID)
	case wsFormatEpub:
		err = convertBookToEpub(bookID)
	default:
		logging.Warnf("Unsupported conversion format: %s", format)
		return
	}

	result := map[string]interface{}{
		wsKeyType:   wsMsgConversion,
		wsKeyBookID: bookID,
		wsKeyFormat: format,
	}

	if err != nil {
		logging.Errorf("Failed to convert book %d to %s: %v", bookID, format, err)
		result[wsKeyStatus] = jsonKeyError
		result[jsonKeyError] = err.Error()
	} else {
		result[wsKeyStatus] = "ready"
	}

	message, _ := json.Marshal(result)
	wsSend(notifyChan, done, message)
}
