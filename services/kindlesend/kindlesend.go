// Package kindlesend sends a book to a reader's Kindle: the queue that bounds
// how many sends run and wait, the book as EPUB, and the mail. The site's
// websocket and the Telegram bot both send through it, so a reader's book is
// on its way once, whichever of them asked, under the same limits.
package kindlesend

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-pg/pg/v10"
	"github.com/spf13/viper"

	"gopds-api/database"
	"gopds-api/email"
	"gopds-api/internal/safeio"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/utils"
)

// What a send comes to. "" is sent: the relay accepted the message — nothing
// here can know more. Everything else is a code the asker turns into words.
const (
	CodeNotConfigured    = "not_configured"
	CodeNotFound         = "not_found"
	CodeTooLarge         = "too_large"
	CodeConversionFailed = "conversion_failed"
	CodeSendFailed       = "send_failed"
	// CodeInProgress answers a request for a book already on its way to the
	// same reader's Kindle: nothing more is sent, and the first request's
	// answer still comes.
	CodeInProgress = "in_progress"
	// CodeBusy is the server protecting itself: too many sends are queued
	// already. Not a quota — the reader may ask again in a minute.
	CodeBusy = "busy"
	// OutcomeDropped is never told to anyone: it is the log's word for a
	// request still waiting for a slot when its asker went away.
	OutcomeDropped = "dropped"
	// outcomeSent is the log's word for "".
	outcomeSent = "sent"
)

// How much Kindle work the server takes on. Execution and admission are
// bounded apart: only Concurrency books convert and mail at once, and at most
// Admission wait or run in all.
const (
	Concurrency = 2
	Admission   = 16
)

// Key is one reader asking for one book.
type Key struct {
	UserID int64
	BookID int64
}

// Queue remembers which sends are under way, admits new ones up to its
// limit, and bounds how many run.
type Queue struct {
	mu       sync.Mutex
	inFlight map[Key]struct{}
	limit    int
	slots    chan struct{}
}

// NewQueue makes a queue running concurrency sends and admitting admission.
func NewQueue(concurrency, admission int) *Queue {
	return &Queue{
		inFlight: make(map[Key]struct{}),
		limit:    admission,
		slots:    make(chan struct{}, concurrency),
	}
}

// Shared is the one queue the site and the bot share.
var Shared = NewQueue(Concurrency, Admission)

// Admit takes the send on, or says why not: CodeInProgress when this reader's
// book is already under way — every tab and the bot share the claim — and
// CodeBusy when the queue is full.
func (q *Queue) Admit(key Key) string {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, busy := q.inFlight[key]; busy {
		return CodeInProgress
	}
	if len(q.inFlight) >= q.limit {
		return CodeBusy
	}
	q.inFlight[key] = struct{}{}
	return ""
}

// Leave gives the claim and the place in the queue back.
func (q *Queue) Leave(key Key) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.inFlight, key)
}

// Size counts the sends admitted and not yet finished.
func (q *Queue) Size() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.inFlight)
}

// Running counts the slots held.
func (q *Queue) Running() int { return len(q.slots) }

// Acquire waits for a slot, or gives up when the asker goes away: a request
// nobody can be told the answer to is not worth a conversion. False means it
// gave up.
//
// A select with both cases ready picks either, so a closed done alone does
// not stop a request when a slot happens to be free: it is checked before
// the wait and again once a slot is held, and the slot handed back.
func (q *Queue) Acquire(done <-chan struct{}) bool {
	if isClosed(done) {
		return false
	}
	select {
	case q.slots <- struct{}{}:
	case <-done:
		return false
	}
	if isClosed(done) {
		q.ReleaseSlot()
		return false
	}
	return true
}

// ReleaseSlot gives a slot back.
func (q *Queue) ReleaseSlot() { <-q.slots }

// isClosed reports whether the channel has been closed, without waiting.
func isClosed(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

// ContentType is what an EPUB is, to a browser and to a mail client.
const ContentType = "application/epub+zip"

// ErrBookFileMissing is a book whose row exists and whose file does not.
var ErrBookFileMissing = errors.New("book file not found")

// EPUBOf converts one book to EPUB in memory, refusing one over limit bytes
// with safeio.ErrTooLarge. It returns the book's row alongside, for the name.
func EPUBOf(bookID, limit int64) (models.Book, []byte, error) {
	book, err := database.GetBook(bookID)
	if err != nil {
		return book, nil, err
	}
	zipPath := viper.GetString("app.files_path") + book.Path

	if !utils.FileExists(zipPath) {
		return book, nil, fmt.Errorf("%w: %s", ErrBookFileMissing, zipPath)
	}

	bp := utils.NewBookProcessor(book.FileName, zipPath)
	rc, err := bp.Epub()
	if err != nil {
		return book, nil, err
	}
	defer rc.Close()

	data, err := safeio.ReadAll(rc, limit)
	if err != nil {
		return book, nil, err
	}
	return book, data, nil
}

// Book is a book ready to mail.
type Book struct {
	Title    string
	Filename string
	Data     []byte
}

// EPUB converts the book to EPUB in memory, within Kindle's limit. It does
// not go through the site's download store: that one is keyed by book alone
// and emptied by the download, so a reader downloading the book while it was
// being mailed would take the other's bytes away.
func EPUB(bookID int64) (Book, error) {
	book, data, err := EPUBOf(bookID, email.MaxKindleBookBytes)
	if err != nil {
		return Book{}, err
	}
	return Book{Title: book.Title, Filename: book.DownloadName() + ".epub", Data: data}, nil
}

// Sender is one way of sending: where it reads the reader and the book, and
// how it mails. The site swaps these in tests; the bot uses Default.
type Sender struct {
	Queue  *Queue
	Target func(userID int64) (database.KindleTarget, error)
	Book   func(bookID int64) (Book, error)
	Mail   func(email.KindleDelivery) error
}

// Default sends through the shared queue, the database and the mail relay.
func Default() *Sender {
	return &Sender{
		Queue:  Shared,
		Target: database.GetKindleTarget,
		Book:   EPUB,
		Mail:   func(d email.KindleDelivery) error { return email.SendToKindle(&d) },
	}
}

// Check re-checks the asker's right to send, before the wait for a slot and
// again after it: a non-empty code stops the send with that code.
type Check func() string

// BookError names what went wrong getting the book ready.
func BookError(bookID int64, err error) string {
	switch {
	case errors.Is(err, pg.ErrNoRows), errors.Is(err, ErrBookFileMissing):
		return CodeNotFound
	case errors.Is(err, safeio.ErrTooLarge), errors.Is(err, email.ErrBookTooLarge):
		return CodeTooLarge
	default:
		logging.Errorf("Send to Kindle: conversion failed: book_id=%d err=%v", bookID, err)
		return CodeConversionFailed
	}
}

// Run does one send and writes its one line of log. The caller has admitted
// it to the queue, and leaves it after.
//
// The line is written last, after a panic in the conversion has been turned
// into conversion_failed: written inside the send, it recorded the panicking
// book as sent. And the outcome starts out failed — only a send that reached
// the relay's acceptance of the message says otherwise.
func (s *Sender) Run(userID, bookID int64, check Check, done <-chan struct{}) (code string) {
	return s.RunGuarded(userID, bookID, check, nil, done)
}

// RunGuarded is Run with one more check, beforeMail, made after the
// conversion — itself a wait — right before the mail goes, which cannot be
// taken back. nil skips it, as the site's sends do.
func (s *Sender) RunGuarded(userID, bookID int64, check, beforeMail Check, done <-chan struct{}) (code string) {
	started := time.Now()
	size := 0
	code = CodeSendFailed
	defer func() {
		if r := recover(); r != nil {
			logging.Errorf("Send to Kindle: panic: user_id=%d book_id=%d: %v", userID, bookID, r)
			code = CodeConversionFailed
		}
		outcome := code
		if outcome == "" {
			outcome = outcomeSent
		}
		logging.Infof("Send to Kindle: user_id=%d book_id=%d bytes=%d outcome=%s elapsed=%s",
			userID, bookID, size, outcome, time.Since(started).Round(time.Millisecond))
	}()
	code = s.send(userID, bookID, check, beforeMail, done, &size)
	return code
}

// Send does the work and says how it went: "" when the relay took the
// message, a code otherwise, OutcomeDropped when done closed while the
// request waited. size is filled in once the book is converted.
func (s *Sender) Send(userID, bookID int64, check Check, done <-chan struct{}, size *int) string {
	return s.send(userID, bookID, check, nil, done, size)
}

func (s *Sender) send(userID, bookID int64, check, beforeMail Check, done <-chan struct{}, size *int) string {
	if code := check(); code != "" {
		return code
	}
	target, err := s.Target(userID)
	if err != nil {
		logging.Errorf("Send to Kindle: reading the reader failed: user_id=%d err=%v", userID, err)
		return CodeSendFailed
	}
	if target.Email == "" {
		return CodeNotConfigured
	}
	sender := email.KindleSenderAddress(target.SenderID)
	if sender == "" {
		logging.Errorf("Send to Kindle: no sender domain configured: user_id=%d", userID)
		return CodeSendFailed
	}

	if !s.Queue.Acquire(done) {
		return OutcomeDropped
	}
	defer s.Queue.ReleaseSlot()
	// Again after the wait, which may have been long enough for a logout.
	if code := check(); code != "" {
		return code
	}

	book, err := s.Book(bookID)
	if err != nil {
		return BookError(bookID, err)
	}
	*size = len(book.Data)
	if beforeMail != nil {
		if code := beforeMail(); code != "" {
			return code
		}
	}

	err = s.Mail(email.KindleDelivery{
		From:  sender,
		To:    target.Email,
		Title: book.Title,
		Book:  email.Attachment{Filename: book.Filename, ContentType: ContentType, Data: book.Data},
	})
	if err != nil {
		if errors.Is(err, email.ErrBookTooLarge) {
			return CodeTooLarge
		}
		// The class only: what the server said may echo either address, and
		// the addresses are the reader's.
		logging.Errorf("Send to Kindle: the relay refused: user_id=%d book_id=%d %s",
			userID, bookID, email.FailureClass(err))
		logging.Debugf("Send to Kindle: refused from=%s to=%s: %v", sender, target.Email, err)
		return CodeSendFailed
	}
	logging.Debugf("Send to Kindle: from=%s to=%s", sender, target.Email)
	return ""
}
