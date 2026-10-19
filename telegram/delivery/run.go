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
	lease := database.LeaseOf(job)
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	go s.heartbeat(ctx, cancel, lease)

	client, err := s.bots.Client(ctx, job.BotRef)
	if err != nil || client == nil {
		// The bot is not served any more: disconnected, or its token refused.
		s.finish(ctx, lease, nil, models.OutcomeUnavailable)
		return
	}
	book, err := database.GetBookWithAuthors(ctx, job.BookID)
	if errors.Is(err, pg.ErrNoRows) {
		n := s.notifierFor(client, job, &models.Book{ID: job.BookID})
		s.finish(ctx, lease, n, models.OutcomeNotFound)
		return
	}
	if err != nil {
		logging.Warnf("Delivery: job %d: reading book %d: %v", job.ID, job.BookID, err)
		_ = database.RequeueTelegramDelivery(ctx, lease, s.cfg.NotSentBackoff)
		return
	}
	n := s.notifierFor(client, job, book)
	n.start(ctx)
	defer n.stop()

	if outcome, ok := s.stillDeliverable(ctx, job, book); !ok {
		s.finish(ctx, lease, n, outcome)
		return
	}

	prepCtx, prepCancel := context.WithTimeout(ctx, s.cfg.PrepareTimeout)
	file, err := s.cfg.Prepare(prepCtx, book, job.Format, s.cfg.UploadLimit)
	prepCancel()
	if ctx.Err() != nil {
		return // the lease is gone; the next holder prepares again
	}
	switch {
	case errors.Is(err, ErrTooLarge):
		s.finish(ctx, lease, n, models.OutcomeTooLarge)
		return
	case errors.Is(err, ErrFileMissing):
		s.finish(ctx, lease, n, models.OutcomeNotFound)
		return
	case err != nil:
		logging.Warnf("Delivery: job %d: preparing book %d as %s: %v", job.ID, job.BookID, job.Format, err)
		s.finish(ctx, lease, n, models.OutcomeConversionFailed)
		return
	case int64(len(file.Data)) > s.cfg.UploadLimit:
		// Whatever prepared it, a file over the limit is not uploaded.
		s.finish(ctx, lease, n, models.OutcomeTooLarge)
		return
	}

	// The book may have been withdrawn, or the chat unbound, while it was
	// being prepared.
	if fresh, err := database.GetBookWithAuthors(ctx, job.BookID); err == nil {
		book = fresh
	} else if errors.Is(err, pg.ErrNoRows) {
		s.finish(ctx, lease, n, models.OutcomeNotFound)
		return
	}
	if outcome, ok := s.stillDeliverable(ctx, job, book); !ok {
		s.finish(ctx, lease, n, outcome)
		return
	}

	if err := database.StartTelegramDeliverySend(ctx, lease, int64(len(file.Data))); err != nil {
		return
	}
	n.sending(ctx)
	s.send(ctx, client, job, book, file, lease, n)
}

// stillDeliverable re-checks the book and the chat's binding.
func (s *Service) stillDeliverable(ctx context.Context, job *models.TelegramDelivery, book *models.Book) (string, bool) {
	if !visible(book) {
		return models.OutcomeUnavailable, false
	}
	binding, err := database.GetActiveTelegramBinding(ctx, job.BotRef)
	if err != nil || binding == nil || binding.ID != job.BindingID {
		return models.OutcomeUnavailable, false
	}
	return "", true
}

// send uploads the file and records what Telegram's answer means.
func (s *Service) send(
	ctx context.Context, client *bot.Bot, job *models.TelegramDelivery, book *models.Book, file File,
	lease database.DeliveryLease, n *notifier,
) {
	waited := false
	for {
		if wait := s.rateLimited(job.BotRef); wait > 0 {
			if wait > s.cfg.ShortWait || waited {
				s.requeue(ctx, lease, n, wait)
				return
			}
			waited = true
			if !sleep(ctx, wait) {
				return
			}
		}
		msg, err := client.SendDocument(ctx, &bot.SendDocumentParams{
			ChatID:   job.ChatID,
			Document: &tgbot.InputFileUpload{Filename: file.Name, Data: bytes.NewReader(file.Data)},
			Caption:  fileCaption(book, job.Format, len(file.Data)),
		})
		class, wait := tgapi.Classify(err)
		switch class {
		case tgapi.ClassOK:
			if err := database.MarkTelegramDeliverySent(context.WithoutCancel(ctx), lease, int64(msg.ID)); err != nil {
				logging.Warnf("Delivery: job %d sent, but recording it: %v", job.ID, err)
			}
			n.sent(ctx)
			return
		case tgapi.ClassRateLimited:
			s.shareRateLimit(job.BotRef, wait)
			if wait > s.cfg.ShortWait || waited {
				s.requeue(ctx, lease, n, wait)
				return
			}
			waited = true
			if !sleep(ctx, wait) {
				return
			}
		case tgapi.ClassNotSent:
			if int(job.Attempt) < s.cfg.MaxAttempts {
				backoff := s.cfg.NotSentBackoff << (job.Attempt - 1)
				logging.Warnf("Delivery: job %d did not reach Telegram, again in %s: %v", job.ID, backoff, tgapi.RedactURL(err))
				_ = database.RequeueTelegramDelivery(ctx, lease, backoff)
				return
			}
			s.finish(ctx, lease, n, models.OutcomeSendRejected)
			return
		case tgapi.ClassForbidden:
			s.finish(ctx, lease, nil, models.OutcomeBotBlocked)
			return
		case tgapi.ClassUnauthorized:
			if err := database.SetTelegramBotStatus(ctx, job.BotRef, models.TelegramBotTokenRejected); err != nil {
				logging.Warnf("Delivery: marking telegram bot %d: %v", job.BotRef, err)
			}
			s.finish(ctx, lease, nil, models.OutcomeTokenRejected)
			return
		case tgapi.ClassUnknown:
			logging.Warnf("Delivery: job %d: the send's outcome is unknown: %v", job.ID, tgapi.RedactURL(err))
			s.finishAs(ctx, lease, n, models.DeliveryUncertain, models.OutcomeUncertain)
			return
		default:
			logging.Warnf("Delivery: job %d: Telegram refused the file: %v", job.ID, tgapi.RedactURL(err))
			s.finish(ctx, lease, n, models.OutcomeSendRejected)
			return
		}
	}
}

// requeue puts a job back for after a 429 wait, telling the chat when the
// wait is long enough to notice.
func (s *Service) requeue(ctx context.Context, lease database.DeliveryLease, n *notifier, wait time.Duration) {
	if wait > longWaitNotice {
		n.waiting(ctx)
	}
	if err := database.RequeueTelegramDelivery(ctx, lease, wait); err != nil {
		logging.Warnf("Delivery: job %d: requeueing: %v", lease.ID, err)
	}
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
	if n != nil {
		n.finish(ctx, outcome, 0)
	}
}

// heartbeat renews the lease; a failed renewal cancels the job.
func (s *Service) heartbeat(ctx context.Context, cancel context.CancelCauseFunc, lease database.DeliveryLease) {
	ticker := time.NewTicker(s.cfg.Heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
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

// sleep waits d or until ctx ends; false when ctx ended.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (s *Service) notifierFor(client *bot.Bot, job *models.TelegramDelivery, book *models.Book) *notifier {
	return &notifier{s: s, client: client, job: job, book: book, done: make(chan struct{})}
}
