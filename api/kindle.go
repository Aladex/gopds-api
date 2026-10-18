package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v10"

	"gopds-api/database"
	"gopds-api/email"
	"gopds-api/httputil"
	"gopds-api/internal/safeio"
	"gopds-api/logging"
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

	wsKeyCode = "code"
)

// kindleConcurrency is how many books are converted and mailed at once, across
// every reader. A conversion holds the whole book in memory and a send holds
// it again as base64; a burst of clicks must queue rather than multiply that.
const kindleConcurrency = 2

// kindleKey is one reader asking for one book.
type kindleKey struct {
	userID int64
	bookID int64
}

// kindleQueue remembers which sends are under way and bounds how many run.
type kindleQueue struct {
	mu       sync.Mutex
	inFlight map[kindleKey]struct{}
	slots    chan struct{}
}

func newKindleQueue(concurrency int) *kindleQueue {
	return &kindleQueue{
		inFlight: make(map[kindleKey]struct{}),
		slots:    make(chan struct{}, concurrency),
	}
}

// claim marks the send as under way, or reports that it already is. Every
// tab of the reader's shares this, so a second tab cannot send the book again
// either.
func (q *kindleQueue) claim(key kindleKey) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, busy := q.inFlight[key]; busy {
		return false
	}
	q.inFlight[key] = struct{}{}
	return true
}

func (q *kindleQueue) release(key kindleKey) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.inFlight, key)
}

// kindleSends is the one queue every connection shares.
var kindleSends = newKindleQueue(kindleConcurrency)

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
// replies when it is done. A second request for the same book while the first
// is under way is answered at once, and nothing more is sent.
func handleKindleRequest(userID, bookID int64, notifyChan chan []byte, done <-chan struct{}) bool {
	key := kindleKey{userID: userID, bookID: bookID}
	if !kindleSends.claim(key) {
		return wsSend(notifyChan, done, kindleReply(bookID, kindleCodeInProgress))
	}
	go func() {
		code := sendBookToKindleSafely(userID, bookID)
		// Released before the reply goes out, so a reader who asks again the
		// moment they see it is not told the first one is still running.
		kindleSends.release(key)
		wsSend(notifyChan, done, kindleReply(bookID, code))
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

// sendBookToKindleSafely turns a panic in the conversion into an error reply.
// Without it the claim above would never be released, and the reader could
// not send that book again until the server restarted.
func sendBookToKindleSafely(userID, bookID int64) (code string) {
	defer func() {
		if r := recover(); r != nil {
			logging.Errorf("Send to Kindle: panic: user_id=%d book_id=%d: %v", userID, bookID, r)
			code = kindleCodeConversionFailed
		}
	}()
	return sendBookToKindle(userID, bookID)
}

// kindleReply is the frame answering one request; an empty code is success.
func kindleReply(bookID int64, code string) []byte {
	reply := map[string]interface{}{
		wsKeyType:   wsMsgKindle,
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

// sendBookToKindle does the work and says how it went: "" when the relay took
// the message, an error code otherwise. One line of log per send, whatever the
// outcome.
func sendBookToKindle(userID, bookID int64) (code string) {
	started := time.Now()
	size := 0
	defer func() {
		outcome := kindleStatusSent
		if code != "" {
			outcome = code
		}
		logging.Infof("Send to Kindle: user_id=%d book_id=%d bytes=%d outcome=%s elapsed=%s",
			userID, bookID, size, outcome, time.Since(started).Round(time.Millisecond))
	}()

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

	kindleSends.slots <- struct{}{}
	defer func() { <-kindleSends.slots }()

	book, err := kindleBookOf(bookID)
	if err != nil {
		return kindleBookError(bookID, err)
	}
	size = len(book.data)

	err = mailToKindle(email.KindleDelivery{
		From:  sender,
		To:    target.Email,
		Title: book.title,
		Book: email.Attachment{
			Filename:    book.filename,
			ContentType: epubContentType,
			Data:        book.data,
		},
	})
	if err != nil {
		if errors.Is(err, email.ErrBookTooLarge) {
			return kindleCodeTooLarge
		}
		logging.Errorf("Send to Kindle: the relay refused: user_id=%d book_id=%d err=%v", userID, bookID, err)
		logging.Debugf("Send to Kindle: refused message was from=%s to=%s", sender, target.Email)
		return kindleCodeSendFailed
	}
	logging.Debugf("Send to Kindle: from=%s to=%s", sender, target.Email)
	return ""
}
