package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	//nolint:depguard // asserting on emitted log output needs logrus' own test hook, and logging wraps logrus
	"github.com/sirupsen/logrus"
	//nolint:depguard // same justification: the capture hook ships in its own package
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopds-api/logging"
	"gopds-api/services"
)

// Topic subscriptions over the wire: a client states what it wants and the
// server delivers only that.

// wsTestClient wraps a connection with a single background reader: canceling
// a pending Read would make coder/websocket close the whole connection, so
// reads never stop once started and tests pick messages off a channel.
type wsTestClient struct {
	conn *websocket.Conn
	msgs chan map[string]interface{}
}

func newWSTestClient(t *testing.T, server *httptest.Server) *wsTestClient {
	t.Helper()
	c := &wsTestClient{
		conn: dialWS(t, server),
		msgs: make(chan map[string]interface{}, 64),
	}
	go func() {
		for {
			var msg map[string]interface{}
			if err := wsjson.Read(context.Background(), c.conn, &msg); err != nil {
				close(c.msgs)
				return
			}
			c.msgs <- msg
		}
	}()
	return c
}

func (c *wsTestClient) write(t *testing.T, v interface{}) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, wsjson.Write(ctx, c.conn, v))
}

func (c *wsTestClient) read(t *testing.T) map[string]interface{} {
	t.Helper()
	select {
	case msg, ok := <-c.msgs:
		require.True(t, ok, "connection closed while waiting for a message")
		return msg
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a WebSocket message")
		return nil
	}
}

// expectSilence asserts that nothing arrives within the grace period.
func (c *wsTestClient) expectSilence(t *testing.T) {
	t.Helper()
	select {
	case msg, ok := <-c.msgs:
		if !ok {
			t.Fatal("connection closed while expecting silence")
		}
		t.Fatalf("expected no message, got %v", msg)
	case <-time.After(200 * time.Millisecond):
	}
}

// waitClosed blocks until the background reader reports the connection
// gone, i.e. the server side finished its teardown.
func (c *wsTestClient) waitClosed(t *testing.T) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case _, ok := <-c.msgs:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("connection still open")
		}
	}
}

func (c *wsTestClient) subscribeTopic(t *testing.T, topic string) {
	t.Helper()
	c.write(t, map[string]string{"type": "subscribe", "topic": topic})
	ack := c.read(t)
	require.Equal(t, "subscribed", ack["type"])
	require.Equal(t, topic, ack["topic"])
}

func TestWSHandler_AdminReceivesTopicOnlyAfterSubscribe(t *testing.T) {
	s, mgr := setupTestServer(t, 1, "admin", true)
	client := newWSTestClient(t, s)
	time.Sleep(50 * time.Millisecond)

	// No subscription yet: the broadcast must not arrive.
	require.NoError(t, mgr.BroadcastToAdmins(services.ScanStarted, map[string]int{"total": 10}))
	client.expectSilence(t)

	client.subscribeTopic(t, services.TopicScan)
	require.NoError(t, mgr.BroadcastToAdmins(services.ScanStarted, map[string]int{"total": 10}))

	msg := client.read(t)
	assert.Equal(t, services.ScanStarted, msg["type"])
	assert.Equal(t, services.TopicScan, msg["topic"])
}

func TestWSHandler_AdminReceivesOnlySubscribedTopic(t *testing.T) {
	s, mgr := setupTestServer(t, 1, "admin", true)
	client := newWSTestClient(t, s)
	time.Sleep(50 * time.Millisecond)

	client.subscribeTopic(t, services.TopicDuplicates)

	require.NoError(t, mgr.BroadcastToAdmins(services.ScanProgress, map[string]int{"done": 1}))
	client.expectSilence(t)

	require.NoError(t, mgr.BroadcastToAdmins("duplicate_scan_progress", map[string]int{"processed": 3}))
	msg := client.read(t)
	assert.Equal(t, "duplicate_scan_progress", msg["type"])
	assert.Equal(t, services.TopicDuplicates, msg["topic"])
}

func TestWSHandler_NonAdminSubscribeRefused(t *testing.T) {
	s, mgr := setupTestServer(t, 2, "user", false)
	client := newWSTestClient(t, s)
	time.Sleep(50 * time.Millisecond)

	client.write(t, map[string]string{"type": "subscribe", "topic": services.TopicScan})
	refusal := client.read(t)
	assert.Equal(t, "subscribe_error", refusal["type"])
	assert.Equal(t, services.TopicScan, refusal["topic"])

	// And nothing is ever delivered afterwards.
	require.NoError(t, mgr.BroadcastToAdmins(services.ScanStarted, nil))
	client.expectSilence(t)
}

func TestWSHandler_SubscribeUnknownTopicRefused(t *testing.T) {
	s, _ := setupTestServer(t, 1, "admin", true)
	client := newWSTestClient(t, s)
	time.Sleep(50 * time.Millisecond)

	client.write(t, map[string]string{"type": "subscribe", "topic": "no_such_topic"})
	refusal := client.read(t)
	assert.Equal(t, "subscribe_error", refusal["type"])
}

func TestWSHandler_UnsubscribeStopsDelivery(t *testing.T) {
	s, mgr := setupTestServer(t, 1, "admin", true)
	client := newWSTestClient(t, s)
	time.Sleep(50 * time.Millisecond)

	client.subscribeTopic(t, services.TopicScan)
	require.NoError(t, mgr.BroadcastToAdmins(services.ScanProgress, nil))
	client.read(t)

	client.write(t, map[string]string{"type": "unsubscribe", "topic": services.TopicScan})
	ack := client.read(t)
	require.Equal(t, "unsubscribed", ack["type"])

	require.NoError(t, mgr.BroadcastToAdmins(services.ScanProgress, nil))
	client.expectSilence(t)
}

func TestWSHandler_DisconnectCleansUpSubscription(t *testing.T) {
	s, mgr := setupTestServer(t, 1, "admin", true)
	client := newWSTestClient(t, s)
	client.subscribeTopic(t, services.TopicScan)

	require.NoError(t, client.conn.Close(websocket.StatusNormalClosure, "bye"))
	// Let the handler notice the close and unregister.
	require.Eventually(t, func() bool {
		return mgr.GetAdminCount() == 0
	}, 2*time.Second, 20*time.Millisecond)

	require.NoError(t, mgr.BroadcastToAdmins(services.ScanStarted, nil))
}

func TestWSHandler_PongReachesOnlyRequester(t *testing.T) {
	s, _ := setupTestServer(t, 1, "admin", true)
	client1 := newWSTestClient(t, s)
	client2 := newWSTestClient(t, s)
	time.Sleep(50 * time.Millisecond)

	client1.write(t, map[string]string{"type": "ping"})
	resp := client1.read(t)
	assert.Equal(t, "pong", resp["type"])

	// The second connection's traffic is its own; no cross-delivery.
	client2.expectSilence(t)
}

// --- Conversion replies carry a type and stay on the requester's connection ---

func TestWSHandler_ConversionReplyTypedAndOnlyToRequester(t *testing.T) {
	original := convertBookToMobi
	convertBookToMobi = func(bookID int64) error { return nil }
	t.Cleanup(func() { convertBookToMobi = original })

	s, _ := setupTestServer(t, 1, "admin", true)
	requester := newWSTestClient(t, s)
	bystander := newWSTestClient(t, s)
	time.Sleep(50 * time.Millisecond)

	requester.write(t, map[string]interface{}{"type": "convert", "bookID": 7, "format": "mobi"})

	reply := requester.read(t)
	assert.Equal(t, "conversion", reply["type"])
	assert.Equal(t, float64(7), reply["bookID"])
	assert.Equal(t, "ready", reply["status"])

	// The reply is request/reply traffic: the other connection never sees it.
	bystander.expectSilence(t)
}

// --- Connection teardown logging: quiet, and without user names ---

func TestIsNormalWSClose(t *testing.T) {
	normal := []error{
		io.EOF,
		fmt.Errorf("read frame: %w", io.EOF),
		websocket.CloseError{Code: websocket.StatusNormalClosure},
		websocket.CloseError{Code: websocket.StatusGoingAway},
		websocket.CloseError{Code: websocket.StatusNoStatusRcvd},
	}
	for _, err := range normal {
		assert.True(t, isNormalWSClose(err), "%v should count as a normal close", err)
	}

	abnormal := []error{
		errors.New("connection reset by peer"),
		websocket.CloseError{Code: websocket.StatusInternalError},
	}
	for _, err := range abnormal {
		assert.False(t, isNormalWSClose(err), "%v should NOT count as a normal close", err)
	}
}

// sawCloseLine reports whether the writer loop's farewell line arrived.
func sawCloseLine(entries []*logrus.Entry) bool {
	for _, e := range entries {
		if strings.HasPrefix(e.Message, "WebSocket closed:") {
			return true
		}
	}
	return false
}

func TestWSHandler_NormalCloseStaysQuietAndNameless(t *testing.T) {
	logger, hook := test.NewNullLogger()
	logging.SetLogger(logger)
	t.Cleanup(func() { logging.SetLogger(nil) })

	s, _ := setupTestServer(t, 1, "secretname", true)
	client := newWSTestClient(t, s)
	time.Sleep(50 * time.Millisecond)

	// An ordinary tab close: a clean GoingAway close frame from the client.
	require.NoError(t, client.conn.Close(websocket.StatusGoingAway, "bye"))
	client.waitClosed(t)
	// The reader records its own exit before the channel close the test
	// waits on, but the writer's farewell line can still be in flight.
	deadline := time.Now().Add(3 * time.Second)
	for !sawCloseLine(hook.AllEntries()) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}

	for _, e := range hook.AllEntries() {
		assert.NotContains(t, e.Message, "secretname", "logs must not carry user names")
		if e.Level == logrus.WarnLevel || e.Level == logrus.ErrorLevel {
			t.Errorf("a normal close must not log at %v: %s", e.Level, e.Message)
		}
	}
}
