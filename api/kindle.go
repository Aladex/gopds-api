package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v10"

	"gopds-api/database"
	"gopds-api/email"
	"gopds-api/httputil"
	"gopds-api/internal/safeio"
	"gopds-api/logging"
	"gopds-api/middlewares"
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
// the interface turns into words. "sent" means the relay accepted the
// message — nothing here can know more.
const (
	kindleStatusSent  = "sent"
	kindleStatusError = "error"

	kindleCodeNotConfigured    = "not_configured"
	kindleCodeNotFound         = "not_found"
	kindleCodeTooLarge         = "too_large"
	kindleCodeConversionFailed = "conversion_failed"
	kindleCodeSendFailed       = "send_failed"
	// kindleCodeInProgress answers a request for a book that is already on its
	// way to the same reader's Kindle: nothing more is sent, and the first
	// request's reply still comes.
	kindleCodeInProgress = "in_progress"
	// kindleCodeBusy is the server protecting itself: too many sends are
	// queued already, everyone's or this connection's. Not a quota — the
	// reader may ask again in a minute.
	kindleCodeBusy = "busy"
	// kindleCodeSessionExpired is a request over a socket whose session has
	// since ended. The socket outlives a logout elsewhere; sending mail must
	// not.
	kindleCodeSessionExpired = "session_expired"
	// kindleOutcomeDropped is never sent: it is the log's word for a request
	// still waiting for a slot when its connection closed.
	kindleOutcomeDropped = "dropped"

	wsKeyCode = "code"
)

// How much Kindle work the server takes on. Execution and admission are
// bounded apart: only kindleConcurrency books convert and mail at once, and
// at most kindleAdmission wait or run in all, kindleSocketPending of them for
// any one connection. Without the second bound every distinct book id got a
// goroutine and a claim the moment it was asked for, and a slow relay let a
// single socket pile up as many as it cared to send.
const (
	kindleConcurrency   = 2
	kindleAdmission     = 16
	kindleSocketPending = 8
)

// kindleKey is one reader asking for one book.
type kindleKey struct {
	userID int64
	bookID int64
}

// kindleQueue remembers which sends are under way, admits new ones up to its
// limit, and bounds how many run.
type kindleQueue struct {
	mu       sync.Mutex
	inFlight map[kindleKey]struct{}
	limit    int
	slots    chan struct{}
}

func newKindleQueue(concurrency, admission int) *kindleQueue {
	return &kindleQueue{
		inFlight: make(map[kindleKey]struct{}),
		limit:    admission,
		slots:    make(chan struct{}, concurrency),
	}
}

// admit takes the send on, or says why not: in_progress when this reader's
// book is already under way — every tab of theirs shares the claim, so a
// second tab cannot send it again either — and busy when the queue is full.
func (q *kindleQueue) admit(key kindleKey) string {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, busy := q.inFlight[key]; busy {
		return kindleCodeInProgress
	}
	if len(q.inFlight) >= q.limit {
		return kindleCodeBusy
	}
	q.inFlight[key] = struct{}{}
	return ""
}

// leave gives the claim and the place in the queue back.
func (q *kindleQueue) leave(key kindleKey) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.inFlight, key)
}

// acquire waits for a slot, or gives up when the connection goes away: a
// request nobody can be told the answer to is not worth a conversion. False
// means it gave up.
//
// A select with both cases ready picks either, so a closed connection alone
// does not stop a request when a slot happens to be free: it is checked
// before the wait and again once a slot is held, and the slot handed back.
func (q *kindleQueue) acquire(done <-chan struct{}) bool {
	if isClosed(done) {
		return false
	}
	select {
	case q.slots <- struct{}{}:
	case <-done:
		return false
	}
	if isClosed(done) {
		q.releaseSlot()
		return false
	}
	return true
}

// isClosed reports whether the channel has been closed, without waiting.
func isClosed(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func (q *kindleQueue) releaseSlot() { <-q.slots }

// kindleSends is the one queue every connection shares.
var kindleSends = newKindleQueue(kindleConcurrency, kindleAdmission)

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
}

func newKindleSocket(token string) *kindleSocket {
	return &kindleSocket{token: token, expired: make(chan struct{})}
}

// expire marks the connection's session as gone.
func (k *kindleSocket) expire() {
	k.expireOnce.Do(func() { close(k.expired) })
}

// The outside world behind a Kindle send, swappable in tests.
var (
	kindleTargetOf = database.GetKindleTarget
	kindleBookOf   = epubForKindle
	mailToKindle   = func(d email.KindleDelivery) error { return email.SendToKindle(&d) }
)

// epubContentType is what an EPUB is, to a browser and to a mail client.
const epubContentType = "application/epub+zip"

// kindleBook is a book ready to mail.
type kindleBook struct {
	title    string
	filename string
	data     []byte
}

// epubForKindle converts the book to EPUB in memory. It does not go through
// epubStore: that one is keyed by book alone and emptied by the download, so a
// reader downloading the book while it was being mailed would take the other's
// bytes away.
func epubForKindle(bookID int64) (kindleBook, error) {
	book, data, err := epubOf(bookID, email.MaxKindleBookBytes)
	if err != nil {
		return kindleBook{}, err
	}
	return kindleBook{title: book.Title, filename: book.DownloadName() + ".epub", data: data}, nil
}

// handleKindleRequest starts sending a book to the reader's Kindle and
// replies when it is done. A request that cannot be taken on — the book
// already on its way, the queue full — is answered at once.
func handleKindleRequest(userID, bookID int64, sock *kindleSocket, notifyChan chan []byte, done <-chan struct{}) bool {
	if sock == nil {
		sock = newKindleSocket("")
	}
	key := kindleKey{userID: userID, bookID: bookID}
	if code := kindleSends.admit(key); code != "" {
		return wsSend(notifyChan, done, kindleReply(bookID, code))
	}
	if sock.pending.Load() >= kindleSocketPending {
		kindleSends.leave(key)
		return wsSend(notifyChan, done, kindleReply(bookID, kindleCodeBusy))
	}
	sock.pending.Add(1)
	go func() {
		code := runKindleSend(userID, bookID, sock.token, done)
		// Released before the reply goes out, so a reader who asks again the
		// moment they see it is not told the first one is still running.
		kindleSends.leave(key)
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

// kindleBookError names what went wrong getting the book ready.
func kindleBookError(bookID int64, err error) string {
	switch {
	case errors.Is(err, pg.ErrNoRows), errors.Is(err, errBookFileMissing):
		return kindleCodeNotFound
	case errors.Is(err, safeio.ErrTooLarge), errors.Is(err, email.ErrBookTooLarge):
		return kindleCodeTooLarge
	default:
		logging.Errorf("Send to Kindle: conversion failed: book_id=%d err=%v", bookID, err)
		return kindleCodeConversionFailed
	}
}

// runKindleSend does one send and writes its one line of log.
//
// The line is written last, after a panic in the conversion has been turned
// into conversion_failed: written inside the send, as it was, it recorded the
// panicking book as sent. And the outcome starts out failed — only a send
// that reached the relay's acceptance of the message says otherwise.
func runKindleSend(userID, bookID int64, token string, done <-chan struct{}) (code string) {
	started := time.Now()
	size := 0
	code = kindleCodeSendFailed
	defer func() {
		if r := recover(); r != nil {
			logging.Errorf("Send to Kindle: panic: user_id=%d book_id=%d: %v", userID, bookID, r)
			code = kindleCodeConversionFailed
		}
		outcome := code
		if outcome == "" {
			outcome = kindleStatusSent
		}
		logging.Infof("Send to Kindle: user_id=%d book_id=%d bytes=%d outcome=%s elapsed=%s",
			userID, bookID, size, outcome, time.Since(started).Round(time.Millisecond))
	}()
	code = sendBookToKindle(userID, bookID, token, done, &size)
	return code
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

// kindleSessionCode checks that the socket's session still stands. A session
// confirmed gone is session_expired; a session store that does not answer
// fails the send closed, as send_failed, since mail cannot be unsent.
func kindleSessionCode(userID int64, token string) string {
	err := validateWSSession(token)
	if err == nil {
		return ""
	}
	if errors.Is(err, middlewares.ErrSessionMissing) {
		return kindleCodeSessionExpired
	}
	logging.Warnf("Send to Kindle: session check inconclusive: user_id=%d err=%v", userID, err)
	return kindleCodeSendFailed
}

// sendBookToKindle does the work and says how it went: "" when the relay took
// the message, an error code otherwise, kindleOutcomeDropped when the
// connection went away while the request waited. size is filled in once the
// book is converted.
func sendBookToKindle(userID, bookID int64, token string, done <-chan struct{}, size *int) string {
	if code := kindleSessionCode(userID, token); code != "" {
		return code
	}
	target, err := kindleTargetOf(userID)
	if err != nil {
		logging.Errorf("Send to Kindle: reading the reader failed: user_id=%d err=%v", userID, err)
		return kindleCodeSendFailed
	}
	if target.Email == "" {
		return kindleCodeNotConfigured
	}
	sender := email.KindleSenderAddress(target.SenderID)
	if sender == "" {
		logging.Errorf("Send to Kindle: no sender domain configured: user_id=%d", userID)
		return kindleCodeSendFailed
	}

	if !kindleSends.acquire(done) {
		return kindleOutcomeDropped
	}
	defer kindleSends.releaseSlot()
	// Again after the wait, which may have been long enough for a logout.
	if code := kindleSessionCode(userID, token); code != "" {
		return code
	}

	book, err := kindleBookOf(bookID)
	if err != nil {
		return kindleBookError(bookID, err)
	}
	*size = len(book.data)

	err = mailToKindle(email.KindleDelivery{
		From:  sender,
		To:    target.Email,
		Title: book.title,
		Book:  email.Attachment{Filename: book.filename, ContentType: epubContentType, Data: book.data},
	})
	if err != nil {
		if errors.Is(err, email.ErrBookTooLarge) {
			return kindleCodeTooLarge
		}
		// The class only: what the server said may echo either address, and
		// the addresses are the reader's.
		logging.Errorf("Send to Kindle: the relay refused: user_id=%d book_id=%d %s",
			userID, bookID, email.FailureClass(err))
		logging.Debugf("Send to Kindle: refused from=%s to=%s: %v", sender, target.Email, err)
		return kindleCodeSendFailed
	}
	logging.Debugf("Send to Kindle: from=%s to=%s", sender, target.Email)
	return ""
}
