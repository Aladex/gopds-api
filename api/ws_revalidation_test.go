package api

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopds-api/middlewares"
)

// Round 3: the revalidation loop distinguishes a confirmed session loss from
// an infrastructure blip, fails closed on both for delivery, and only spends
// checks on sockets that hold admin subscriptions.

// stubRoleSource swaps the live role lookup for a flippable test one.
func stubRoleSource(t *testing.T, admin, roleErr *atomic.Bool) {
	t.Helper()
	previous := currentUserIsAdmin
	currentUserIsAdmin = func(userID int64) (bool, error) {
		if roleErr != nil && roleErr.Load() {
			return false, assert.AnError
		}
		return admin.Load(), nil
	}
	t.Cleanup(func() { currentUserIsAdmin = previous })
}

// sessionStubError boxes the error a stubSessionSource returns, because an
// atomic.Value cannot hold a plain nil.
type sessionStubError struct{ err error }

// stubSessionSource swaps the session check; the boxed error is returned
// when set.
func stubSessionSource(t *testing.T, sessionErr *atomic.Value) {
	t.Helper()
	sessionErr.Store(sessionStubError{})
	previous := validateWSSession
	validateWSSession = func(string) error {
		return sessionErr.Load().(sessionStubError).err
	}
	t.Cleanup(func() { validateWSSession = previous })
}

// fastRevalidation shortens the revalidation period for the test.
func fastRevalidation(t *testing.T) {
	t.Helper()
	previous := wsRevalidateInterval
	wsRevalidateInterval = 20 * time.Millisecond
	t.Cleanup(func() { wsRevalidateInterval = previous })
}

// drainPing reads until the pong for a ping arrives and reports every
// non-pong frame seen on the way.
func drainUntilPong(t *testing.T, client *wsTestClient) []map[string]interface{} {
	t.Helper()
	var seen []map[string]interface{}
	for i := 0; i < 20; i++ {
		msg := client.read(t)
		if msg["type"] == "pong" {
			return seen
		}
		seen = append(seen, msg)
	}
	t.Fatal("never got the pong barrier")
	return nil
}

func TestWSHandler_RevokedAdminLosesDeliveryAndSubscription(t *testing.T) {
	var adminNow atomic.Bool
	adminNow.Store(true)
	s, mgr := setupTestServer(t, 1, "admin", true)
	stubRoleSource(t, &adminNow, nil)
	fastRevalidation(t)
	client := newWSTestClient(t, s)

	client.subscribeTopic(t, "scan")
	client.write(t, map[string]string{"type": "ping"})
	require.Equal(t, "pong", client.read(t)["type"])

	// Delivery works while the role is current.
	require.NoError(t, mgr.BroadcastToAdmins("scan_progress", map[string]int{"done": 1}))
	require.Equal(t, "scan_progress", client.read(t)["type"])

	// The role is revoked elsewhere; the next revalidation tick must cut the
	// socket off from admin topics.
	adminNow.Store(false)
	time.Sleep(150 * time.Millisecond)

	require.NoError(t, mgr.BroadcastToAdmins("scan_progress", map[string]int{"done": 2}))
	client.write(t, map[string]string{"type": "subscribe", "topic": "duplicates"})

	// The subscribe answer arrives; the broadcast must not.
	for i := 0; i < 10; i++ {
		msg := client.read(t)
		if msg["type"] == "subscribe_error" {
			assert.Equal(t, "duplicates", msg["topic"])
			assert.Equal(t, "forbidden", msg["error"])
			return
		}
		require.NotEqual(t, "scan_progress", msg["type"], "revoked admin still receives topic events")
	}
	t.Fatal("never got the subscribe refusal")
}

func TestWSHandler_SubscribeFailsClosedWhenRoleCheckFails(t *testing.T) {
	var adminNow atomic.Bool
	adminNow.Store(true)
	var roleErr atomic.Bool
	roleErr.Store(true)
	s, _ := setupTestServer(t, 1, "admin", true)
	stubRoleSource(t, &adminNow, &roleErr)
	client := newWSTestClient(t, s)

	client.write(t, map[string]string{"type": "subscribe", "topic": "scan"})
	ack := client.read(t)
	require.Equal(t, "subscribe_error", ack["type"], "a failed role lookup must refuse, not allow")
}

func TestWSHandler_LostSessionClosesSocket(t *testing.T) {
	var sessionErr atomic.Value
	s, _ := setupTestServer(t, 1, "admin", true)
	stubSessionSource(t, &sessionErr)
	fastRevalidation(t)

	client := newWSTestClient(t, s)
	client.subscribeTopic(t, "scan")
	client.write(t, map[string]string{"type": "ping"})
	require.Equal(t, "pong", client.read(t)["type"])

	// A confirmed missing session: the store answered, the token is not in it.
	sessionErr.Store(sessionStubError{middlewares.ErrSessionMissing})

	// The reader's message channel closes when the connection is torn down.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case _, ok := <-client.msgs:
			if !ok {
				return // channel closed: the socket is gone, as required
			}
		case <-deadline:
			t.Fatal("socket stayed open after the session was confirmed gone")
		}
	}
}

func TestWSHandler_InconclusiveSessionSuspendsThenResumes(t *testing.T) {
	var sessionErr atomic.Value
	s, mgr := setupTestServer(t, 1, "admin", true)
	stubSessionSource(t, &sessionErr)
	fastRevalidation(t)
	client := newWSTestClient(t, s)

	client.subscribeTopic(t, "scan")
	require.NoError(t, mgr.BroadcastToAdmins("scan_progress", map[string]int{"done": 1}))
	require.Equal(t, "scan_progress", client.read(t)["type"])

	// A Redis blip: the check cannot answer. Delivery must stop (fail closed)
	// while the socket itself stays open.
	sessionErr.Store(sessionStubError{middlewares.ErrSessionInconclusive})
	time.Sleep(150 * time.Millisecond)

	require.NoError(t, mgr.BroadcastToAdmins("scan_progress", map[string]int{"done": 2}))
	client.write(t, map[string]string{"type": "ping"})
	for _, msg := range drainUntilPong(t, client) {
		require.NotEqual(t, "scan_progress", msg["type"],
			"an inconclusive session check must suspend admin delivery")
	}

	// The store recovers: delivery resumes without any resubscription.
	sessionErr.Store(sessionStubError{})
	time.Sleep(150 * time.Millisecond)
	require.NoError(t, mgr.BroadcastToAdmins("scan_progress", map[string]int{"done": 3}))
	client.write(t, map[string]string{"type": "ping"})
	seen := drainUntilPong(t, client)
	require.NotEmpty(t, seen, "delivery must resume after a successful revalidation")
	assert.Equal(t, "scan_progress", seen[len(seen)-1]["type"])
}

func TestWSHandler_InconclusiveRoleCheckSuspendsDelivery(t *testing.T) {
	var adminNow atomic.Bool
	adminNow.Store(true)
	var roleErr atomic.Bool
	s, mgr := setupTestServer(t, 1, "admin", true)
	stubRoleSource(t, &adminNow, &roleErr)
	fastRevalidation(t)
	client := newWSTestClient(t, s)

	client.subscribeTopic(t, "scan")
	require.NoError(t, mgr.BroadcastToAdmins("scan_progress", map[string]int{"done": 1}))
	require.Equal(t, "scan_progress", client.read(t)["type"])

	// The periodic role lookup fails: the current role is unknown, and an
	// unknown authorization must not keep receiving admin events.
	roleErr.Store(true)
	time.Sleep(150 * time.Millisecond)

	require.NoError(t, mgr.BroadcastToAdmins("scan_progress", map[string]int{"done": 2}))
	client.write(t, map[string]string{"type": "ping"})
	for _, msg := range drainUntilPong(t, client) {
		require.NotEqual(t, "scan_progress", msg["type"],
			"fail-closed violation: admin frame after a failed role revalidation")
	}

	// The database answers again: the role is still there, delivery resumes.
	roleErr.Store(false)
	time.Sleep(150 * time.Millisecond)
	require.NoError(t, mgr.BroadcastToAdmins("scan_progress", map[string]int{"done": 3}))
	client.write(t, map[string]string{"type": "ping"})
	seen := drainUntilPong(t, client)
	require.NotEmpty(t, seen, "delivery must resume once the role check succeeds again")
	assert.Equal(t, "scan_progress", seen[len(seen)-1]["type"])
}

func TestWSHandler_RegularSocketIsNotRevalidated(t *testing.T) {
	s, _ := setupTestServer(t, 5, "user", false)
	fastRevalidation(t)

	// Counting stubs go in AFTER setupTestServer, which installs defaults.
	var sessionCalls atomic.Int64
	var roleCalls atomic.Int64
	previousSession := validateWSSession
	validateWSSession = func(string) error { sessionCalls.Add(1); return nil }
	previousRole := currentUserIsAdmin
	currentUserIsAdmin = func(int64) (bool, error) { roleCalls.Add(1); return false, nil }
	t.Cleanup(func() {
		validateWSSession = previousSession
		currentUserIsAdmin = previousRole
	})

	client := newWSTestClient(t, s)
	sessionCalls.Store(0)
	roleCalls.Store(0)
	time.Sleep(150 * time.Millisecond)

	assert.Zero(t, sessionCalls.Load(), "a socket without admin subscriptions must not be revalidated")
	assert.Zero(t, roleCalls.Load(), "a socket without admin subscriptions must not be revalidated")

	// The socket still serves its own traffic.
	client.write(t, map[string]string{"type": "ping"})
	require.Equal(t, "pong", client.read(t)["type"])
}

// Round 2 (N3): control replies must not pin a goroutine on a client that
// stopped reading.
func TestWSHandler_ControlReplyUnblocksWhenConnectionDies(t *testing.T) {
	notifyChan := make(chan []byte, 1)
	notifyChan <- []byte("stuck") // the client is not draining anything
	done := make(chan struct{})

	finished := make(chan struct{})
	go func() {
		handleClientMessage(nil, 1, 1, clientMessage{Type: "ping"}, 20, notifyChan, done)
		close(finished)
	}()

	select {
	case <-finished:
		t.Fatal("the pong send should be stuck behind a full buffer")
	case <-time.After(100 * time.Millisecond):
	}

	close(done)
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("reader goroutine leaked behind a full reply buffer")
	}
}

func TestWSHandler_ConversionReplyUnblocksWhenConnectionDies(t *testing.T) {
	started := make(chan struct{})
	original := convertBookToMobi
	convertBookToMobi = func(bookID int64) error { close(started); return nil }
	t.Cleanup(func() { convertBookToMobi = original })

	notifyChan := make(chan []byte, 1)
	notifyChan <- []byte("stuck")
	done := make(chan struct{})

	finished := make(chan struct{})
	go func() {
		sendConversionReply(7, "mobi", notifyChan, done)
		close(finished)
	}()

	// The conversion itself runs; only the reply send must be cancellable.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("conversion never ran")
	}
	time.Sleep(50 * time.Millisecond)
	close(done)
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("conversion goroutine leaked behind a full reply buffer")
	}
}
