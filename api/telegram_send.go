package api

import (
	"context"
	"sync"
)

// Send to Telegram: the book sent into the reader's chat with their own bot.
//
// The bot's delivery service does the work, as durable jobs; the socket only
// asks and waits for the answer, like the Kindle sends beside it. A socket
// that goes away before the answer loses nothing but the answer: the job goes
// on, and the file still arrives in the chat.

// TelegramDelivery is the bot's delivery service as the socket sees it.
type TelegramDelivery interface {
	// SendFromSite asks for the book to be sent to the user's bound chat. A
	// request refused at once comes back as its code; an admitted one as a
	// channel that carries the outcome's code and closes, or closes without
	// one when ctx ends first.
	SendFromSite(ctx context.Context, userID, bookID int64) (string, <-chan string)
}

var (
	telegramDeliveryMu sync.RWMutex
	telegramDelivery   TelegramDelivery
)

// SetTelegramDelivery hands the socket the delivery service; nil takes it
// away.
func SetTelegramDelivery(d TelegramDelivery) {
	telegramDeliveryMu.Lock()
	defer telegramDeliveryMu.Unlock()
	telegramDelivery = d
}

func currentTelegramDelivery() TelegramDelivery {
	telegramDeliveryMu.RLock()
	defer telegramDeliveryMu.RUnlock()
	return telegramDelivery
}

// wsMsgTelegram is both the request to send a book to the reader's Telegram
// and its reply.
const wsMsgTelegram = "telegram"

// telegramCodeSendFailed answers a request nothing could be done with: no
// delivery service in this process, or a refusal without a code.
const telegramCodeSendFailed = "send_failed"

// telegramSent is the outcome code of a delivered file.
const telegramSent = "sent"

// handleTelegramRequest asks for the book to be sent to the reader's chat and
// replies when the job ends. The session is checked first, as for Kindle; a
// request that cannot be taken on is answered at once.
func handleTelegramRequest(userID, bookID int64, sock *kindleSocket, notifyChan chan []byte, done <-chan struct{}) bool {
	if sock == nil {
		sock = newKindleSocket("")
	}
	if sock.telegramPending.Load() >= kindleSocketPending {
		return wsSend(notifyChan, done, telegramReply(bookID, kindleCodeBusy))
	}
	sock.telegramPending.Add(1)
	go func() {
		defer sock.telegramPending.Add(-1)
		code := runTelegramSend(userID, bookID, sock, done)
		if code == kindleOutcomeDropped {
			return
		}
		wsSend(notifyChan, done, telegramReply(bookID, code))
		if code == kindleCodeSessionExpired {
			sock.expire()
		}
	}()
	return true
}

// runTelegramSend makes the request and waits for its outcome; it returns
// kindleOutcomeDropped when the connection went away first.
func runTelegramSend(userID, bookID int64, sock *kindleSocket, done <-chan struct{}) string {
	if code := sock.sessionCode(userID); code != "" {
		return code
	}
	d := currentTelegramDelivery()
	if d == nil {
		return telegramCodeSendFailed
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-done:
			cancel()
		case <-ctx.Done():
		}
	}()
	code, outcome := d.SendFromSite(ctx, userID, bookID)
	if code != "" || outcome == nil {
		if code == "" {
			code = telegramCodeSendFailed
		}
		return code
	}
	result, ok := <-outcome
	if !ok {
		return kindleOutcomeDropped
	}
	return result
}

// telegramReply is the frame answering one request.
func telegramReply(bookID int64, code string) []byte {
	if code == telegramSent {
		code = ""
	}
	return sendReply(wsMsgTelegram, bookID, code)
}
