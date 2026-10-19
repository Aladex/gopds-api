package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/gin-gonic/gin"

	"gopds-api/database"
	"gopds-api/email"
	"gopds-api/httputil"
	"gopds-api/logging"
	"gopds-api/middlewares"
	"gopds-api/services/kindlesend"
)

// Send to Kindle: the reader's Kindle address, and the book mailed to it.
//
// The address is set and cleared over HTTP, like the rest of the profile. The
// sending rides the websocket, like the conversions it is made of: a book
// takes seconds to convert and mail, and the socket already carries a reply
// back to the tab that asked, where an HTTP request would have to be held open
// for the whole of it.

// kindleEmailRequest is the body of PUT /api/books/kindle.
type kindleEmailRequest struct {
	KindleEmail string `json:"kindle_email"`
}

// errInvalidKindleEmail is the one refusal the profile shows for an address.
var errInvalidKindleEmail = errors.New("invalid_kindle_email")

// SetKindleEmail stores the reader's Send-to-Kindle address.
// Auth godoc
// @Summary Set the Kindle address
// @Description Stores the reader's Send-to-Kindle address. Only addresses at kindle.com
// @Description and free.kindle.com are accepted; the address is trimmed and lower-cased.
// @Tags users
// @Accept  json
// @Produce  json
// @Param  body body kindleEmailRequest true "Kindle address"
// @Success 200 {object} models.LoggedInUser
// @Failure 400 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /api/books/kindle [put]
func SetKindleEmail(c *gin.Context) {
	var req kindleEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("invalid_request"))
		return
	}
	address, err := email.NormalizeKindleAddress(req.KindleEmail)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, errInvalidKindleEmail)
		return
	}
	storeKindleEmail(c, address)
}

// ClearKindleEmail removes the reader's Send-to-Kindle address.
// Auth godoc
// @Summary Clear the Kindle address
// @Description Removes the reader's Send-to-Kindle address. Their sender address stays as it is.
// @Tags users
// @Produce  json
// @Success 200 {object} models.LoggedInUser
// @Failure 500 {object} httputil.HTTPError
// @Router /api/books/kindle [delete]
func ClearKindleEmail(c *gin.Context) {
	storeKindleEmail(c, "")
}

// storeKindleEmail writes the address and answers with the whole profile, so
// the interface replaces its copy of the user rather than patching one field.
func storeKindleEmail(c *gin.Context, address string) {
	userID := c.GetInt64("user_id")
	user, err := database.SetKindleEmail(userID, address)
	if err != nil {
		logging.Errorf("Storing the Kindle address failed: user_id=%d err=%v", userID, err)
		httputil.NewError(c, http.StatusInternalServerError, errors.New("kindle_email_not_saved"))
		return
	}
	hf, err := database.HaveFavs(user.ID)
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	// The address itself is the reader's, and only worth writing down while
	// debugging.
	if address == "" {
		logging.Infof("Kindle address cleared: user_id=%d", userID)
	} else {
		logging.Infof("Kindle address set: user_id=%d", userID)
		logging.Debugf("Kindle address set: user_id=%d address=%s", userID, address)
	}
	c.JSON(http.StatusOK, profileOf(&user, hf))
}

// What a Kindle reply says. Status is sent or error; an error carries a code
// the interface turns into words. The codes are the send's
// (services/kindlesend), and session_expired the socket's own.
const (
	kindleStatusSent  = "sent"
	kindleStatusError = "error"

	kindleCodeNotConfigured    = kindlesend.CodeNotConfigured
	kindleCodeNotFound         = kindlesend.CodeNotFound
	kindleCodeTooLarge         = kindlesend.CodeTooLarge
	kindleCodeConversionFailed = kindlesend.CodeConversionFailed
	kindleCodeSendFailed       = kindlesend.CodeSendFailed
	kindleCodeInProgress       = kindlesend.CodeInProgress
	kindleCodeBusy             = kindlesend.CodeBusy
	// kindleCodeSessionExpired is a request over a socket whose session has
	// since ended. The socket outlives a logout elsewhere; sending mail must
	// not.
	kindleCodeSessionExpired = "session_expired"
	kindleOutcomeDropped     = kindlesend.OutcomeDropped

	wsKeyCode = "code"
)

// How much Kindle work the server takes on: the shared queue's bounds
// (kindlesend), and at most kindleSocketPending requests pending for any one
// connection. Without the last bound a slow relay let a single socket pile up
// as many as it cared to send.
const (
	kindleConcurrency   = kindlesend.Concurrency
	kindleAdmission     = kindlesend.Admission
	kindleSocketPending = 8
)

// kindleQueue is the queue every Kindle send goes through.
type kindleQueue = kindlesend.Queue

func newKindleQueue(concurrency, admission int) *kindleQueue {
	return kindlesend.NewQueue(concurrency, admission)
}

// kindleSends is the one queue every connection — and the bot — shares.
var kindleSends = kindlesend.Shared

// kindleSocket is one connection's part in Kindle sending: the session it
// was opened with, re-checked before any mail goes out, and how many of its
// requests are pending. Only the connection's reader goroutine admits, so
// the count needs no lock against itself.
type kindleSocket struct {
	token   string
	pending atomic.Int32
	// telegramPending counts the connection's Telegram requests, bounded
	// like its Kindle ones.
	telegramPending atomic.Int32

	// expired closes once a Kindle request finds the session gone. The
	// writer loop then delivers what is queued — the session_expired reply
	// among it — and closes the connection, as the admin revalidation does:
	// nothing more runs on a dead token, and the client's reconnect comes
	// back with the session it has now.
	expired    chan struct{}
	expireOnce sync.Once

	// session and isAdmin are the authorization lookups as they were when
	// the connection opened. The connection's goroutines read only these,
	// never the package variables tests swap: a connection a finished test
	// left running cannot race the next test's swap.
	session func(token string) error
	isAdmin func(userID int64) (bool, error)
}

func newKindleSocket(token string) *kindleSocket {
	return &kindleSocket{token: token, expired: make(chan struct{}),
		session: validateWSSession, isAdmin: currentUserIsAdmin}
}

// expire marks the connection's session as gone.
func (k *kindleSocket) expire() {
	k.expireOnce.Do(func() { close(k.expired) })
}

// kindleBook is a book ready to mail.
type kindleBook = kindlesend.Book

// The outside world behind a Kindle send, swappable in tests.
var (
	kindleTargetOf = database.GetKindleTarget
	kindleBookOf   = kindlesend.EPUB
	mailToKindle   = func(d email.KindleDelivery) error { return email.SendToKindle(&d) }
)

// kindleSender is the site's sender: the shared queue, and the world as the
// variables above have it at the time of the call.
func kindleSender() *kindlesend.Sender {
	return &kindlesend.Sender{
		Queue:  kindleSends,
		Target: func(userID int64) (database.KindleTarget, error) { return kindleTargetOf(userID) },
		Book:   func(bookID int64) (kindlesend.Book, error) { return kindleBookOf(bookID) },
		Mail:   func(d email.KindleDelivery) error { return mailToKindle(d) },
	}
}

// handleKindleRequest starts sending a book to the reader's Kindle and
// replies when it is done. A request that cannot be taken on — the book
// already on its way, the queue full — is answered at once.
func handleKindleRequest(userID, bookID int64, sock *kindleSocket, notifyChan chan []byte, done <-chan struct{}) bool {
	if sock == nil {
		sock = newKindleSocket("")
	}
	key := kindlesend.Key{UserID: userID, BookID: bookID}
	queue := kindleSends
	if code := queue.Admit(key); code != "" {
		return wsSend(notifyChan, done, kindleReply(bookID, code))
	}
	if sock.pending.Load() >= kindleSocketPending {
		queue.Leave(key)
		return wsSend(notifyChan, done, kindleReply(bookID, kindleCodeBusy))
	}
	sock.pending.Add(1)
	go func() {
		code := sock.sendToKindle(userID, bookID, done)
		// Released before the reply goes out, so a reader who asks again the
		// moment they see it is not told the first one is still running.
		queue.Leave(key)
		sock.pending.Add(-1)
		if code != kindleOutcomeDropped {
			wsSend(notifyChan, done, kindleReply(bookID, code))
		}
		if code == kindleCodeSessionExpired {
			sock.expire()
		}
	}()
	return true
}

// runKindleSend does one send for a connection opened with token, and writes
// its one line of log.
func runKindleSend(userID, bookID int64, token string, done <-chan struct{}) string {
	return newKindleSocket(token).sendToKindle(userID, bookID, done)
}

// sendToKindle does one send for this connection, and writes its one line of
// log.
func (k *kindleSocket) sendToKindle(userID, bookID int64, done <-chan struct{}) string {
	return kindleSender().Run(userID, bookID, func() string { return k.sessionCode(userID) }, done)
}

// kindleReply is the frame answering one request; an empty code is success.
func kindleReply(bookID int64, code string) []byte {
	return sendReply(wsMsgKindle, bookID, code)
}

// sendReply is the frame answering one request to send a book somewhere —
// Kindle or Telegram, named by msgType; an empty code is success.
func sendReply(msgType string, bookID int64, code string) []byte {
	reply := map[string]interface{}{
		wsKeyType:   msgType,
		wsKeyBookID: bookID,
		wsKeyStatus: kindleStatusSent,
	}
	if code != "" {
		reply[wsKeyStatus] = kindleStatusError
		reply[wsKeyCode] = code
	}
	message, _ := json.Marshal(reply)
	return message
}

// sessionCode checks that the socket's session still stands. A session
// confirmed gone is session_expired; a session store that does not answer
// fails the send closed, as send_failed, since mail cannot be unsent.
func (k *kindleSocket) sessionCode(userID int64) string {
	err := k.session(k.token)
	if err == nil {
		return ""
	}
	if errors.Is(err, middlewares.ErrSessionMissing) {
		return kindleCodeSessionExpired
	}
	logging.Warnf("Send to Kindle: session check inconclusive: user_id=%d err=%v", userID, err)
	return kindleCodeSendFailed
}
