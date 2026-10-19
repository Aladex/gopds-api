// Package delivery sends book files into the readers' Telegram chats as
// durable jobs.
//
// A request becomes a row (database.EnqueueTelegramDelivery) before anyone is
// told it was accepted; workers in any process claim rows under a lease and a
// fence, prepare the file, and send it. The rules that matter:
//
//   - Preparing is safe to repeat, so a job whose holder vanished while
//     preparing runs again. Sending is not: a job whose holder vanished while
//     sending, or whose send has an unknown outcome, is closed "uncertain" and
//     never sent again automatically — a second file in the chat is worse than
//     an honest "could not confirm".
//   - Only answers that prove the request never reached Telegram (a refused
//     connection, a 429) put a job back in the queue.
//   - The book's visibility and the chat's binding are checked when the job is
//     accepted, before it is prepared and again right before it is sent.
//   - A file is checked against the upload limit as prepared, before any byte
//     is uploaded; it is never read past the limit.
package delivery

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/go-redis/redis"
	"github.com/go-telegram/bot"
	"github.com/google/uuid"
)

// UploadLimit is the cloud Bot API's limit for a document, read the strict
// way: "50 MB" as 50·10⁶ bytes, which also leaves room for the multipart
// envelope. A Local Bot API server would change only this.
const UploadLimit = 50 * 1000 * 1000

// Config sets the service's limits and pace. Zero values take the defaults.
type Config struct {
	// Workers is how many jobs one process runs at once.
	Workers int
	// PerUser and Total bound the unfinished jobs of one account and of the
	// whole service.
	PerUser, Total int
	// UploadLimit is the largest file sent.
	UploadLimit int64
	// Lease is how long a holder may go without renewing; it renews every
	// Heartbeat.
	Lease, Heartbeat time.Duration
	// Poll is how often an idle worker looks for work it was not woken for.
	Poll time.Duration
	// StatusAfter is how long a job from the bot may run before a status
	// message says what is going on.
	StatusAfter time.Duration
	// ChatActionEvery repeats «uploading a document» while a job runs.
	ChatActionEvery time.Duration
	// ShortWait is the longest 429 wait spent in place, holding the job;
	// longer waits put it back in the queue.
	ShortWait time.Duration
	// NotSentBackoff is the first delay after a request that never left;
	// it doubles with each attempt.
	NotSentBackoff time.Duration
	// MaxAttempts bounds the claims of one job.
	MaxAttempts int
	// PrepareTimeout bounds one conversion, kindlegen included.
	PrepareTimeout time.Duration
	// Retention is how long finished jobs are kept.
	Retention time.Duration
	// Prepare produces the file; nil means PrepareFile.
	Prepare PrepareFunc
	// SiteURL is the library's address, for the «open on the site» buttons.
	SiteURL string
	// Redis shares a bot's 429 between processes and paces chat actions;
	// optional.
	Redis *redis.Client
}

func (c Config) withDefaults() Config {
	set := func(v *time.Duration, d time.Duration) {
		if *v == 0 {
			*v = d
		}
	}
	if c.Workers == 0 {
		c.Workers = 2
	}
	if c.PerUser == 0 {
		c.PerUser = 3
	}
	if c.Total == 0 {
		c.Total = 64
	}
	if c.UploadLimit == 0 {
		c.UploadLimit = UploadLimit
	}
	if c.MaxAttempts == 0 {
		c.MaxAttempts = 3
	}
	set(&c.Lease, 45*time.Second)
	set(&c.Heartbeat, 15*time.Second)
	set(&c.Poll, time.Second)
	set(&c.StatusAfter, 3*time.Second)
	set(&c.ChatActionEvery, 4500*time.Millisecond)
	set(&c.ShortWait, 30*time.Second)
	set(&c.NotSentBackoff, 5*time.Second)
	set(&c.PrepareTimeout, 2*time.Minute)
	set(&c.Retention, 30*24*time.Hour)
	if c.Prepare == nil {
		c.Prepare = PrepareFile
	}
	return c
}

// Bots gives the SDK client of a served bot.
type Bots interface {
	Client(ctx context.Context, botRef int64) (*bot.Bot, error)
}

// Service runs delivery jobs.
type Service struct {
	cfg   Config
	bots  Bots
	owner uuid.UUID
	wake  chan struct{}

	mu      sync.Mutex
	cancel  context.CancelFunc
	loops   sync.WaitGroup
	running sync.WaitGroup
}

// New makes a service; Start runs it.
func New(cfg Config, bots Bots) *Service {
	return &Service{cfg: cfg.withDefaults(), bots: bots, owner: uuid.New(), wake: make(chan struct{}, 1)}
}

// Request asks for one book in one format in one bound chat.
type Request struct {
	UserID, BindingID, BotRef, ChatID, BookID int64
	Format                                    string
	Origin                                    string
	// TapKey identifies the press that asked, for requests from the bot.
	TapKey          string
	OriginMessageID int64
}

// Errors of Enqueue that the caller tells the reader about.
var (
	ErrUnknownFormat = errors.New("delivery: unknown format")
	ErrNotFound      = errors.New("delivery: no such book")
	ErrUnavailable   = errors.New("delivery: book not available")
)

// Formats are the formats a book is delivered in.
var Formats = []string{"epub", "fb2", "mobi", "zip"}

func knownFormat(f string) bool {
	for _, known := range Formats {
		if f == known {
			return true
		}
	}
	return false
}

// Enqueue records a request and wakes a worker. The returned admission says
// whether a new job was made, the same press or file was already on its way
// (that job is returned), or a limit was reached.
func (s *Service) Enqueue(ctx context.Context, r Request) (models.TelegramDelivery, database.Admission, error) {
	if !knownFormat(r.Format) {
		return models.TelegramDelivery{}, 0, ErrUnknownFormat
	}
	book, err := database.GetBookWithAuthors(ctx, r.BookID)
	if errors.Is(err, pg.ErrNoRows) {
		return models.TelegramDelivery{}, 0, ErrNotFound
	}
	if err != nil {
		return models.TelegramDelivery{}, 0, err
	}
	if !visible(book) {
		return models.TelegramDelivery{}, 0, ErrUnavailable
	}
	row := &models.TelegramDelivery{
		UserID: r.UserID, BindingID: r.BindingID, BotRef: r.BotRef, ChatID: r.ChatID, BookID: r.BookID,
		Format: r.Format, Origin: r.Origin,
	}
	if r.TapKey != "" {
		key := r.TapKey
		row.TapKey = &key
	}
	if r.OriginMessageID != 0 {
		id := r.OriginMessageID
		row.OriginMessageID = &id
	}
	job, admission, err := database.EnqueueTelegramDelivery(ctx, row, s.cfg.PerUser, s.cfg.Total)
	if err == nil && admission == database.Admitted {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	return job, admission, err
}

// visible is the library's rule for handing a book out.
func visible(b *models.Book) bool {
	return b.Approved && !b.DuplicateHidden
}

// Start runs the workers and the sweeper until Stop.
func (s *Service) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	for i := 0; i < s.cfg.Workers; i++ {
		s.loops.Add(1)
		go s.work(ctx)
	}
	s.loops.Add(1)
	go s.sweep(ctx)
}

// Stop stops taking work and waits for the jobs in hand to finish.
func (s *Service) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.loops.Wait()
	s.running.Wait()
}

// WaitIdle waits for the jobs in hand; for tests.
func (s *Service) WaitIdle() { s.running.Wait() }

func (s *Service) work(ctx context.Context) {
	defer s.loops.Done()
	for {
		jobs, err := database.ClaimTelegramDeliveries(ctx, s.owner, s.cfg.Lease, 1)
		if err != nil && ctx.Err() == nil {
			logging.Warnf("Delivery: claiming jobs: %v", err)
		}
		if len(jobs) == 1 {
			s.running.Add(1)
			// A job in hand runs to its end, Stop or not: cutting an upload
			// short only turns a delivery into an uncertain one.
			s.run(context.WithoutCancel(ctx), &jobs[0])
			s.running.Done()
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-time.After(s.cfg.Poll):
		}
	}
}

// sweep closes jobs whose holder vanished mid-send and tells their chats, and
// prunes old rows.
func (s *Service) sweep(ctx context.Context) {
	defer s.loops.Done()
	ticker := time.NewTicker(s.cfg.Lease / 3)
	defer ticker.Stop()
	lastPrune := time.Time{}
	for {
		closed, err := database.SweepTelegramDeliveries(ctx, s.cfg.MaxAttempts)
		if err != nil && ctx.Err() == nil {
			logging.Warnf("Delivery: sweeping jobs: %v", err)
		}
		for i := range closed {
			job := &closed[i]
			logging.Warnf("Delivery: job %d closed %s after its holder stopped", job.ID, job.State)
			s.tellClosed(context.WithoutCancel(ctx), job)
		}
		if time.Since(lastPrune) > time.Hour {
			if err := database.PruneTelegramDeliveries(ctx, s.cfg.Retention); err != nil && ctx.Err() == nil {
				logging.Warnf("Delivery: pruning: %v", err)
			}
			lastPrune = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// tellClosed tells a chat about a job the sweeper closed.
func (s *Service) tellClosed(ctx context.Context, job *models.TelegramDelivery) {
	client, err := s.bots.Client(ctx, job.BotRef)
	if err != nil || client == nil {
		return
	}
	book, err := database.GetBookWithAuthors(ctx, job.BookID)
	if err != nil {
		book = &models.Book{ID: job.BookID}
	}
	outcome := models.OutcomeUncertain
	if job.Outcome != nil {
		outcome = *job.Outcome
	}
	n := &notifier{s: s, client: client, job: job, book: book}
	if job.StatusMessageID != nil {
		n.statusID = *job.StatusMessageID
	}
	n.finish(ctx, outcome, 0)
}

func (s *Service) String() string { return fmt.Sprintf("delivery(%d workers)", s.cfg.Workers) }
