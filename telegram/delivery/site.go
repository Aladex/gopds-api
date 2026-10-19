package delivery

import (
	"context"
	"errors"
	"time"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/models"
)

// The answers the site gets for a request to send a book to Telegram. The
// site turns each into words; the job's own outcomes are finer than the site
// needs.
const (
	SiteSent             = "sent"
	SiteNotConnected     = "not_connected"
	SiteNotFound         = "not_found"
	SiteTooLarge         = "too_large"
	SiteConversionFailed = "conversion_failed"
	SiteSendFailed       = "send_failed"
	SiteUncertain        = "uncertain"
	SiteBotBlocked       = "bot_blocked"
	SiteTokenRejected    = "token_rejected"
	SiteInProgress       = "in_progress"
	SiteBusy             = "busy"
)

// defaultAwaitPoll is how often a site request looks at its job when no
// signal came: the job may be run by another process.
const defaultAwaitPoll = time.Second

// SendFromSite asks for bookID to be sent to the account's bound chat, in the
// account's default format. A request refused at once comes back as its code
// and no channel. An admitted one comes back as a channel that carries the
// job's outcome once it ends, then closes; it closes without a value when ctx
// ends first — the job itself goes on, and the file still arrives.
func (s *Service) SendFromSite(ctx context.Context, userID, bookID int64) (refused string, outcome <-chan string) {
	binding, code := s.siteBinding(ctx, userID)
	if code != "" {
		return code, nil
	}
	format, err := database.GetTelegramDefaultFormat(ctx, userID)
	if err != nil {
		logging.Warnf("Delivery: reading the default format of user %d: %v", userID, err)
		return SiteSendFailed, nil
	}
	job, admission, err := s.Enqueue(context.WithoutCancel(ctx), &Request{
		UserID: userID, BindingID: binding.ID, BotRef: binding.BotRef, ChatID: binding.TGUserID,
		BookID: bookID, Format: format, Origin: models.DeliveryFromSite,
	})
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrUnavailable):
		return SiteNotFound, nil
	case err != nil:
		logging.Warnf("Delivery: enqueueing book %d for user %d from the site: %v", bookID, userID, err)
		return SiteSendFailed, nil
	case admission == database.InProgress || admission == database.AlreadyTapped:
		return SiteInProgress, nil
	case admission == database.BusyAccount || admission == database.BusyService:
		return SiteBusy, nil
	}
	out := make(chan string, 1)
	go func() {
		defer close(out)
		if ended, ok := s.await(ctx, job.ID); ok {
			out <- s.siteOutcome(context.WithoutCancel(ctx), ended)
		}
	}()
	return "", out
}

// siteBinding is the account's binding when its chat can be sent to, or the
// code that says why not.
func (s *Service) siteBinding(ctx context.Context, userID int64) (binding *models.TelegramBinding, refused string) {
	state, err := database.GetTelegramUserState(ctx, userID)
	if err != nil {
		logging.Warnf("Delivery: reading the Telegram connection of user %d: %v", userID, err)
		return nil, SiteSendFailed
	}
	switch {
	case state.Binding == nil || state.Bot == nil:
		return nil, SiteNotConnected
	case state.Bot.Status == models.TelegramBotTokenRejected:
		return nil, SiteTokenRejected
	case state.Bot.Status != models.TelegramBotActive:
		return nil, SiteNotConnected
	case state.Binding.BlockedAt != nil:
		return nil, SiteBotBlocked
	}
	return state.Binding, ""
}

// siteOutcome is what the site is told about a job that ended.
func (s *Service) siteOutcome(ctx context.Context, job *models.TelegramDelivery) string {
	outcome := ""
	if job.Outcome != nil {
		outcome = *job.Outcome
	}
	switch outcome {
	case models.OutcomeSent:
		return SiteSent
	case models.OutcomeUncertain:
		return SiteUncertain
	case models.OutcomeNotFound:
		return SiteNotFound
	case models.OutcomeTooLarge:
		return SiteTooLarge
	case models.OutcomeConversionFailed:
		return SiteConversionFailed
	case models.OutcomeBotBlocked:
		return SiteBotBlocked
	case models.OutcomeTokenRejected:
		return SiteTokenRejected
	case models.OutcomeUnavailable:
		// The book was withdrawn, or the chat unbound: the binding says which.
		binding, err := database.GetActiveTelegramBindingOfUser(ctx, job.UserID)
		if err == nil && (binding == nil || binding.ID != job.BindingID) {
			return SiteNotConnected
		}
		return SiteNotFound
	}
	return SiteSendFailed
}

// await waits for job id to end and returns it; false when ctx ended first.
// A job ended in this process wakes it at once; one ended by another process
// is seen at the next look.
func (s *Service) await(ctx context.Context, id int64) (*models.TelegramDelivery, bool) {
	wake := s.watch(id)
	defer s.unwatch(id, wake)
	poll := time.NewTicker(s.cfg.AwaitPoll)
	defer poll.Stop()
	for {
		job, err := database.GetTelegramDelivery(ctx, id)
		if err == nil && job.FinishedAt != nil {
			return job, true
		}
		if err != nil && ctx.Err() == nil {
			logging.Warnf("Delivery: looking at job %d: %v", id, err)
		}
		select {
		case <-ctx.Done():
			return nil, false
		case <-wake:
		case <-poll.C:
		}
	}
}

// watch registers for a signal when job id ends in this process.
func (s *Service) watch(id int64) chan struct{} {
	wake := make(chan struct{}, 1)
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if s.watchers == nil {
		s.watchers = make(map[int64][]chan struct{})
	}
	s.watchers[id] = append(s.watchers[id], wake)
	return wake
}

func (s *Service) unwatch(id int64, wake chan struct{}) {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	list := s.watchers[id]
	for i, w := range list {
		if w == wake {
			list = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(list) == 0 {
		delete(s.watchers, id)
	} else {
		s.watchers[id] = list
	}
}

// ended wakes whoever waits on job id: it may have ended.
func (s *Service) ended(id int64) {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	for _, wake := range s.watchers[id] {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}
