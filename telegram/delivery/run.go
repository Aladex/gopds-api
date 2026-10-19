package delivery

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"time"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/telegram/tgapi"

	"github.com/go-pg/pg/v10"
	"github.com/go-telegram/bot"
	tgbot "github.com/go-telegram/bot/models"
)

// errLeaseLost cancels a job whose lease could not be renewed.
var errLeaseLost = errors.New("delivery: lease lost")

// rateLimitKey shares a bot's 429 with every process.
func rateLimitKey(botRef int64) string { return "tg2:ra:" + strconv.FormatInt(botRef, 10) }

// run takes one claimed job to its end, or to the point where it is no longer
// this holder's.
func (s *Service) run(parent context.Context, job *models.TelegramDelivery) {
	// Deferred first, so it runs last: after the job's own work.
	defer s.cardEnded(parent, job.ID, job)
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	h := s.hold(ctx, cancel, database.LeaseOf(job))
	defer h.stopBeat()

	client, err := s.bots.Client(ctx, job.BotRef)
	if err != nil || client == nil {
		// The bot is not served any more: disconnected, or its token refused.
		s.finish(ctx, h.lease, nil, models.OutcomeUnavailable)
		return
	}
	book, err := database.GetBookWithAuthors(ctx, job.BookID)
	if errors.Is(err, pg.ErrNoRows) {
		s.finish(ctx, h.lease, s.notifierFor(client, job, &models.Book{ID: job.BookID}), models.OutcomeNotFound)
		return
	}
	if err != nil {
		logging.Warnf("Delivery: job %d: reading book %d: %v", job.ID, job.BookID, err)
		s.retryLater(ctx, job, h, s.notifierFor(client, job, &models.Book{ID: job.BookID}))
		return
	}
	n := s.notifierFor(client, job, book)
	n.start(ctx)
	defer n.stop()

	if !s.stillDeliverable(ctx, job, book, h.lease, n) {
		return
	}
	file, ok := s.prepare(ctx, job, book, h.lease, n)
	if !ok {
		return
	}
	// The book is read again for the caption; what may be sent is decided by
	// the gate in front of the upload, which a failed read never replaces.
	fresh, err := database.GetBookWithAuthors(ctx, job.BookID)
	if errors.Is(err, pg.ErrNoRows) {
		s.finish(ctx, h.lease, n, models.OutcomeNotFound)
		return
	}
	if err != nil {
		logging.Warnf("Delivery: job %d: reading book %d again: %v", job.ID, job.BookID, err)
		s.retryLater(ctx, job, h, n)
		return
	}
	n.sending(ctx)
	s.send(ctx, client, job, fresh, file, h, n)
}

// prepare produces the job's file, closing the job when it cannot be made or
// may not be sent; false when the job is done with.
func (s *Service) prepare(
	ctx context.Context, job *models.TelegramDelivery, book *models.Book, lease database.DeliveryLease, n *notifier,
) (File, bool) {
	prepCtx, prepCancel := context.WithTimeout(ctx, s.cfg.PrepareTimeout)
	file, err := s.cfg.Prepare(prepCtx, book, job.Format, s.cfg.UploadLimit)
	prepCancel()
	if ctx.Err() != nil {
		return File{}, false // the lease is gone; the next holder prepares again
	}
	switch {
	case errors.Is(err, ErrTooLarge):
		s.finish(ctx, lease, n, models.OutcomeTooLarge)
	case errors.Is(err, ErrFileMissing):
		s.finish(ctx, lease, n, models.OutcomeNotFound)
	case err != nil:
		logging.Warnf("Delivery: job %d: preparing book %d as %s: %v", job.ID, job.BookID, job.Format, err)
		s.finish(ctx, lease, n, models.OutcomeConversionFailed)
	case int64(len(file.Data)) > s.cfg.UploadLimit:
		// Whatever prepared it, a file over the limit is not uploaded.
		s.finish(ctx, lease, n, models.OutcomeTooLarge)
	default:
		return file, true
	}
	return File{}, false
}

// stillDeliverable re-checks the book and the chat's binding before the work
// of preparing. A job that may not go out is closed unavailable; its chat is
// told only while it is still the reader's — an ended binding leaves a chat
// the library no longer writes to.
func (s *Service) stillDeliverable(
	ctx context.Context, job *models.TelegramDelivery, book *models.Book, lease database.DeliveryLease, n *notifier,
) bool {
	binding, err := database.GetActiveTelegramBinding(ctx, job.BotRef)
	if err != nil || binding == nil || binding.ID != job.BindingID {
		s.finish(ctx, lease, nil, models.OutcomeUnavailable)
		return false
	}
	if !visible(book) {
		s.finish(ctx, lease, n, models.OutcomeUnavailable)
		return false
	}
	return true
}

// send uploads the file and records what Telegram's answer means. Every
// attempt passes the gate first, after any wait, so an attempt never starts
// on a lease, a binding or a book checked before the wait.
func (s *Service) send(
	ctx context.Context, client *bot.Bot, job *models.TelegramDelivery, book *models.Book, file File, h *hold, n *notifier,
) {
	waited := false
	for {
		if wait := s.rateLimited(job.BotRef); wait > 0 && !s.waitOut(ctx, job, h, n, wait, &waited) {
			return
		}
		if !s.startSend(ctx, job, h, n, len(file.Data)) {
			return
		}
		params := &bot.SendDocumentParams{
			ChatID:   job.ChatID,
			Document: &tgbot.InputFileUpload{Filename: file.Name, Data: bytes.NewReader(file.Data)},
			Caption:  fileCaption(book, job.Format, len(file.Data)),
		}
		if s.cfg.Cards != nil && job.FromPanel() {
			name, caption, markup := s.cfg.Cards.File(ctx, job, book, len(file.Data))
			params.Document = &tgbot.InputFileUpload{Filename: name, Data: bytes.NewReader(file.Data)}
			params.Caption = caption
			if markup != nil {
				params.ReplyMarkup = markup
			}
		}
		msg, err := client.SendDocument(ctx, params)
		class, wait := tgapi.Classify(err)
		if class == tgapi.ClassRateLimited {
			s.shareRateLimit(job.BotRef, wait)
			if !s.waitOut(ctx, job, h, n, wait, &waited) {
				return
			}
			continue
		}
		s.settle(ctx, job, h, n, class, msg, err)
		return
	}
}

// startSend passes the gate in front of an upload; false when the upload may
// not begin, the job then closed, put back, or left to its new holder.
func (s *Service) startSend(ctx context.Context, job *models.TelegramDelivery, h *hold, n *notifier, size int) bool {
	err := database.StartTelegramDeliverySend(ctx, h.lease, int64(size))
	switch {
	case err == nil:
		return true
	case errors.Is(err, database.ErrDeliveryLeaseLost):
		logging.Warnf("Delivery: job %d is no longer ours to send", job.ID)
	case errors.Is(err, database.ErrDeliveryUnbound):
		s.finish(ctx, h.lease, nil, models.OutcomeUnavailable)
	case errors.Is(err, database.ErrDeliveryWithdrawn):
		s.finish(ctx, h.lease, n, models.OutcomeUnavailable)
	default:
		logging.Warnf("Delivery: job %d: checking it before the send: %v", job.ID, err)
		s.retryLater(ctx, job, h, n)
	}
	return false
}

// waitOut handles a 429 — Telegram's own word that nothing was sent. The job
// goes back to the queue for the wait first, so it stays retryable by any
// process whatever happens to this one; then, once per job and only for a
// short wait, this process waits and takes it back. It reports whether to
// try again now.
func (s *Service) waitOut(
	ctx context.Context, job *models.TelegramDelivery, h *hold, n *notifier, wait time.Duration, waited *bool,
) bool {
	if !h.requeue(ctx, wait) {
		return false
	}
	if wait > longWaitNotice { // after the requeue: no Telegram call widens the window before it
		n.waiting(ctx)
	}
	if wait > s.cfg.ShortWait || *waited {
		return false
	}
	*waited = true
	if !s.sleep(ctx, wait) {
		return false // stopping: the queued job is any process's
	}
	lease, err := database.ResumeTelegramDelivery(ctx, job.ID, s.owner, s.cfg.Lease)
	if err != nil {
		logging.Warnf("Delivery: job %d: taking it back after the wait: %v", job.ID, err)
		return false
	}
	h.beat(lease)
	return true
}

// settle records what one answer of Telegram means for the job.
func (s *Service) settle(
	ctx context.Context, job *models.TelegramDelivery, h *hold, n *notifier,
	class tgapi.Class, msg *tgbot.Message, err error,
) {
	lease := h.lease
	switch class {
	case tgapi.ClassOK:
		if markErr := database.MarkTelegramDeliverySent(context.WithoutCancel(ctx), lease, int64(msg.ID)); markErr != nil {
			logging.Warnf("Delivery: job %d sent, but recording it: %v", job.ID, markErr)
		}
		s.ended(job.ID)
		n.sent(ctx)
	case tgapi.ClassNotSent:
		if int(job.Attempt) < s.cfg.MaxAttempts {
			backoff := s.backoff(job)
			logging.Warnf("Delivery: job %d did not reach Telegram, again in %s: %v", job.ID, backoff, tgapi.RedactURL(err))
			h.requeue(ctx, backoff)
			return
		}
		s.finish(ctx, lease, n, models.OutcomeSendRejected)
	case tgapi.ClassForbidden:
		s.finish(ctx, lease, nil, models.OutcomeBotBlocked)
	case tgapi.ClassUnauthorized:
		if markErr := database.SetTelegramBotStatus(ctx, job.BotRef, models.TelegramBotTokenRejected); markErr != nil {
			logging.Warnf("Delivery: marking telegram bot %d: %v", job.BotRef, markErr)
		}
		s.finish(ctx, lease, nil, models.OutcomeTokenRejected)
	case tgapi.ClassUnknown, tgapi.ClassRateLimited:
		logging.Warnf("Delivery: job %d: the send's outcome is unknown: %v", job.ID, tgapi.RedactURL(err))
		s.finishAs(ctx, lease, n, models.DeliveryUncertain, models.OutcomeUncertain)
	case tgapi.ClassBadRequest, tgapi.ClassNotModified, tgapi.ClassMessageGone:
		logging.Warnf("Delivery: job %d: Telegram refused the file: %v", job.ID, tgapi.RedactURL(err))
		s.finish(ctx, lease, n, models.OutcomeSendRejected)
	}
}

// backoff is the delay before the job's next attempt: NotSentBackoff,
// doubling with each attempt made.
func (s *Service) backoff(job *models.TelegramDelivery) time.Duration {
	return s.cfg.NotSentBackoff << max(job.Attempt-1, 0)
}

// retryLater puts back a job the library could not check or read, a few
// times; then it closes the job, saying the library failed. Nothing is sent
// on what could not be checked.
func (s *Service) retryLater(ctx context.Context, job *models.TelegramDelivery, h *hold, n *notifier) {
	if int(job.Attempt) < s.cfg.MaxAttempts {
		h.requeue(ctx, s.backoff(job))
		return
	}
	s.finish(ctx, h.lease, n, models.OutcomeLibraryFailed)
}

// finish closes a job as failed with outcome and tells the chat; n may be nil
// when the chat cannot be written to.
func (s *Service) finish(ctx context.Context, lease database.DeliveryLease, n *notifier, outcome string) {
	s.finishAs(ctx, lease, n, models.DeliveryFailed, outcome)
}

func (s *Service) finishAs(ctx context.Context, lease database.DeliveryLease, n *notifier, state, outcome string) {
	if err := database.FinishTelegramDelivery(context.WithoutCancel(ctx), lease, state, outcome); err != nil {
		// Someone else holds the job now; they tell the chat.
		logging.Warnf("Delivery: job %d: closing as %s: %v", lease.ID, outcome, err)
		return
	}
	s.ended(lease.ID)
	if n != nil {
		n.finish(ctx, outcome, 0)
	}
}

// hold is a running job's lease and the heartbeat that renews it. The lease
// changes when the job is put back in the queue and taken again.
type hold struct {
	s      *Service
	ctx    context.Context
	cancel context.CancelCauseFunc
	lease  database.DeliveryLease
	stop   chan struct{}
	done   chan struct{}
}

func (s *Service) hold(ctx context.Context, cancel context.CancelCauseFunc, lease database.DeliveryLease) *hold {
	h := &hold{s: s, ctx: ctx, cancel: cancel}
	h.beat(lease)
	return h
}

// beat starts renewing lease; a failed renewal cancels the job.
func (h *hold) beat(lease database.DeliveryLease) {
	h.lease = lease
	h.stop, h.done = make(chan struct{}), make(chan struct{})
	go h.s.heartbeat(h.ctx, h.cancel, lease, h.stop, h.done)
}

// stopBeat stops the renewals and waits for the last one.
func (h *hold) stopBeat() {
	if h.stop == nil {
		return
	}
	close(h.stop)
	<-h.done
	h.stop = nil
}

// requeue gives the job back to the queue, to run after delay; false when it
// was no longer this holder's to give.
func (h *hold) requeue(ctx context.Context, delay time.Duration) bool {
	h.stopBeat()
	if err := database.RequeueTelegramDelivery(context.WithoutCancel(ctx), h.lease, delay); err != nil {
		logging.Warnf("Delivery: job %d: requeueing: %v", h.lease.ID, err)
		return false
	}
	return true
}

// heartbeat renews the lease until stop; a failed renewal cancels the job.
func (s *Service) heartbeat(
	ctx context.Context, cancel context.CancelCauseFunc, lease database.DeliveryLease, stop <-chan struct{}, done chan<- struct{},
) {
	defer close(done)
	ticker := time.NewTicker(s.cfg.Heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			if err := database.HeartbeatTelegramDelivery(ctx, lease, s.cfg.Lease); err != nil {
				logging.Warnf("Delivery: job %d lost its lease: %v", lease.ID, err)
				cancel(errLeaseLost)
				return
			}
		}
	}
}

// rateLimited is how long the bot must still wait after a 429 another
// process or job saw.
func (s *Service) rateLimited(botRef int64) time.Duration {
	if s.cfg.Redis == nil {
		return 0
	}
	until, err := s.cfg.Redis.Get(rateLimitKey(botRef)).Int64()
	if err != nil {
		return 0
	}
	return time.Until(time.UnixMilli(until))
}

func (s *Service) shareRateLimit(botRef int64, wait time.Duration) {
	if s.cfg.Redis == nil || wait <= 0 {
		return
	}
	until := time.Now().Add(wait).UnixMilli()
	_ = s.cfg.Redis.Set(rateLimitKey(botRef), until, wait).Err()
}

// sleep waits d; false when the job's context ended or the service is
// stopping first.
func (s *Service) sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-s.quitting():
		return false
	case <-t.C:
		return true
	}
}

func (s *Service) notifierFor(client *bot.Bot, job *models.TelegramDelivery, book *models.Book) *notifier {
	return &notifier{s: s, client: client, job: job, book: book, done: make(chan struct{})}
}
