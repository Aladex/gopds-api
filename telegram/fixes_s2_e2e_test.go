package telegram_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/database"
	"gopds-api/models"
	"gopds-api/telegram/delivery"
	"gopds-api/telegram/tgtest"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The S2 fix round: one button is one request, every upload passes the gate
// right before it, a 429 leaves the job retryable, visibility fails closed,
// and the service's limit holds across accounts.

// queryHook runs before every query the stack's database makes.
type queryHook func(ctx context.Context, query string) error

func (h queryHook) BeforeQuery(ctx context.Context, e *pg.QueryEvent) (context.Context, error) {
	q, err := e.FormattedQuery()
	if err != nil {
		return ctx, nil
	}
	return ctx, h(ctx, string(q))
}

func (queryHook) AfterQuery(context.Context, *pg.QueryEvent) error { return nil }

func smallFile(context.Context, *models.Book, string, int64) (delivery.File, error) {
	return delivery.File{Name: "book.epub", Data: []byte("PK small")}, nil
}

// B1. Two presses of the same button 10 ms apart, the second handled only
// after the first file arrived: one file. The same button pressed again once
// the window is over is a deliberate request: a second file.
func TestOneButtonIsOneRequest(t *testing.T) {
	s := newStack(t)
	var second atomic.Int64
	parked, gate := make(chan struct{}), make(chan struct{})
	var parkedOnce atomic.Bool
	defer func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	}()
	// Holds the second press's handler at its book read until let go.
	s.db.AddQueryHook(queryHook(func(ctx context.Context, q string) error {
		l, ok := database.InboxLeaseFrom(ctx)
		if ok && l.UpdateID == second.Load() && strings.Contains(q, `FROM "opds_catalog_book"`) &&
			parkedOnce.CompareAndSwap(false, true) {
			close(parked)
			<-gate
		}
		return nil
	}))
	s.deliveryCfg.Prepare = smallFile
	d := openCard(t, s)
	first := d.u.Tap(t, ownerTG, &d.card, "📚 EPUB")
	again := d.u.Tap(t, ownerTG, &d.card, "📚 EPUB")
	second.Store(again.ID)
	d.hook(botToken1).Post(t, first)
	time.Sleep(10 * time.Millisecond)
	d.hook(botToken1).Post(t, again)
	select {
	case <-parked:
	case <-time.After(5 * time.Second):
		t.Fatal("the second press never reached its book read")
	}
	d.waitJob(t, models.DeliverySent)
	close(gate)
	d.bm.WaitIdle()
	d.delivery.WaitIdle()
	d.fake.Quiet(t, "sendDocument", 1, 200*time.Millisecond)
	assert.Equal(t, 1, d.count(`SELECT count(*) FROM telegram_delivery`))
	assert.Equal(t, "Файл уже отправлен", d.answers(t, 2)[1])

	// The window is over: the same button now asks for the file again.
	_, err := d.db.Exec(`UPDATE telegram_delivery SET finished_at = now() - interval '11 seconds'`)
	require.NoError(t, err)
	d.tapEPUB(t)
	require.Eventually(t, func() bool {
		return d.count(`SELECT count(*) FROM telegram_delivery WHERE state = 'sent'`) == 2
	}, 10*time.Second, 10*time.Millisecond)
	d.fake.Quiet(t, "sendDocument", 2, 200*time.Millisecond)
}

// refuseOneAnswer makes the next callback answer fail at dial, once armed.
type refuseOneAnswer struct {
	armed   atomic.Bool
	refused atomic.Int32
}

func (h *refuseOneAnswer) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.HasSuffix(r.URL.Path, "/answerCallbackQuery") && h.armed.CompareAndSwap(true, false) {
		h.refused.Add(1)
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("callback answer not sent")}
	}
	return http.DefaultTransport.RoundTrip(r)
}

// B1, the reviewer's replay probe. A press repeating the first one is
// answered with its job; its callback answer fails, so its update is replayed
// — after the repeat window. The replay is the same press: no second job.
func TestADedupedPressStaysDedupedWhenItsUpdateIsReplayed(t *testing.T) {
	s := newStack(t)
	fail := &refuseOneAnswer{}
	s.transport = fail
	s.deliveryCfg.Prepare = smallFile
	d := openCard(t, s)
	d.tapEPUB(t)
	d.waitJob(t, models.DeliverySent)
	d.bm.WaitIdle()
	d.delivery.WaitIdle()

	fail.armed.Store(true)
	again := d.u.Tap(t, ownerTG, &d.card, "📚 EPUB")
	d.hook(botToken1).Post(t, again)
	d.bm.WaitIdle()
	require.Equal(t, int32(1), fail.refused.Load())
	require.Equal(t, 1, d.count(`SELECT count(*) FROM telegram_delivery`))
	require.Equal(t, 1, d.count(`SELECT count(*) FROM telegram_update_inbox
		WHERE update_id = ? AND state = 'pending' AND reply_started_at IS NULL`, again.ID),
		"the unsent answer leaves the update to be replayed")

	_, err := d.db.Exec(`UPDATE telegram_delivery SET finished_at = now() - interval '11 seconds'`)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.bm.StartInboxSweeper(ctx, time.Hour)
	require.Eventually(t, func() bool {
		return d.count(`SELECT count(*) FROM telegram_update_inbox WHERE update_id = ? AND state = 'done'`, again.ID) == 1
	}, 5*time.Second, 10*time.Millisecond)
	d.bm.WaitIdle()
	d.delivery.WaitIdle()
	assert.Equal(t, 1, d.count(`SELECT count(*) FROM telegram_delivery`))
	d.fake.Quiet(t, "sendDocument", 1, 200*time.Millisecond)
}

// parkSendingEdit parks the «Отправляю…» status edit — the last thing a
// holder does before its upload — until the returned release is called.
func parkSendingEdit(t *testing.T, s *stack) (edited <-chan struct{}, release func()) {
	t.Helper()
	editing, let := make(chan struct{}), make(chan struct{})
	var once atomic.Bool
	release = func() {
		if once.CompareAndSwap(false, true) {
			close(let)
		}
	}
	s.fake.On(tgtest.Rule{
		Method: "editMessageText",
		Match:  func(c tgtest.Call) bool { return strings.HasPrefix(c.Params["text"], "Отправляю") },
		Times:  1,
		Reply: func(w http.ResponseWriter, r *http.Request, c *tgtest.Call) {
			close(editing)
			select {
			case <-let:
			case <-r.Context().Done():
			}
			tgtest.OK(map[string]any{
				"message_id": 999, "date": time.Now().Unix(),
				"chat": map[string]any{"id": ownerTG, "type": "private"}, "text": "status",
			})(w, r, c)
		},
	})
	return editing, release
}

// gatedStack is a stack whose holder never notices on its own that it lost
// its lease, and shows its status message at once.
func gatedStack(t *testing.T) (d *deliveryStack, release func()) {
	t.Helper()
	s := newStack(t)
	s.deliveryCfg.Heartbeat = time.Hour
	s.deliveryCfg.Lease = 10 * time.Second
	s.deliveryCfg.StatusAfter = time.Millisecond
	// Preparing lasts until the status message is up, so there is one to edit.
	shown := make(chan struct{})
	var shownOnce atomic.Bool
	show := func() {
		if shownOnce.CompareAndSwap(false, true) {
			close(shown)
		}
	}
	s.deliveryCfg.Prepare = func(ctx context.Context, b *models.Book, f string, limit int64) (delivery.File, error) {
		<-shown
		return smallFile(ctx, b, f, limit)
	}
	edited, release := parkSendingEdit(t, s)
	d = openCard(t, s)
	// Registered after the processes' own cleanups, so they run before
	// those Stops, which wait for the parked holder.
	t.Cleanup(release)
	t.Cleanup(show)
	d.tapEPUB(t)
	d.told()
	show()
	select {
	case <-edited:
	case <-time.After(5 * time.Second):
		t.Fatal("the sending status edit never started")
	}
	require.Empty(t, d.fake.Calls("sendDocument"))
	return d, release
}

// B2. The holder loses the job while its status edit is in flight: when the
// edit returns, the upload does not start.
func TestALostLeaseStopsTheUploadAfterTheStatusEdit(t *testing.T) {
	d, release := gatedStack(t)
	defer release()
	_, err := d.db.Exec(`UPDATE telegram_delivery SET lease_expires_at = now() - interval '1 second'`)
	require.NoError(t, err)
	taken, err := database.ClaimTelegramDeliveries(context.Background(), uuid.New(), time.Minute, 1)
	require.NoError(t, err)
	require.Len(t, taken, 1, "another process takes the job over")
	release()
	d.delivery.WaitIdle()
	d.fake.Quiet(t, "sendDocument", 0, 200*time.Millisecond)
	job := d.job(t)
	assert.Equal(t, models.DeliveryPreparing, job.State, "the job is the new holder's, untouched")
	assert.Equal(t, taken[0].Fence, job.Fence)
}

// B2. The chat is unbound while the status edit is in flight: the file is not
// sent to it, and the chat is not written to.
func TestAnUnbindingStopsTheUploadAfterTheStatusEdit(t *testing.T) {
	d, release := gatedStack(t)
	defer release()
	_, err := d.db.Exec(`UPDATE telegram_binding SET status = 'revoked', revoked_at = now() WHERE status = 'active'`)
	require.NoError(t, err)
	before := len(d.fake.Calls("sendMessage"))
	release()
	job := d.waitJob(t, models.DeliveryFailed)
	assert.Equal(t, models.OutcomeUnavailable, *job.Outcome)
	d.delivery.WaitIdle()
	d.fake.Quiet(t, "sendDocument", 0, 200*time.Millisecond)
	assert.Len(t, d.fake.Calls("sendMessage"), before)
}

// B3. Telegram answers 429 and the process stops during the wait: the job
// is in the queue, not closed uncertain, and another process delivers it.
func TestAStopDuringA429WaitStillDeliversOnce(t *testing.T) {
	s := newStack(t)
	s.deliveryCfg.Prepare = smallFile
	s.deliveryCfg.Heartbeat = time.Hour
	s.deliveryCfg.ShortWait = 30 * time.Second
	s.fake.On(tgtest.Rule{Method: "sendDocument", Times: 1, Reply: tgtest.TooManyRequests(2)})
	d := openCard(t, s)
	d.tapEPUB(t)
	d.fake.WaitAnswered(t, "sendDocument", 1, 5*time.Second)
	require.Eventually(t, func() bool { return d.job(t).State == models.DeliveryQueued }, 5*time.Second, 10*time.Millisecond)
	job := d.job(t)
	assert.Nil(t, job.SendStartedAt, "the rejected attempt is not a send in progress")
	closed, err := database.SweepTelegramDeliveries(context.Background(), 3)
	require.NoError(t, err)
	assert.Empty(t, closed)

	d.delivery.Stop() // the process goes away mid-wait
	d.secondPod()
	sent := d.waitJob(t, models.DeliverySent)
	assert.Equal(t, models.OutcomeSent, *sent.Outcome)
	d.fake.Quiet(t, "sendDocument", 2, 200*time.Millisecond)
}

// B4, the reviewer's probe. The book is withdrawn while it is prepared and
// the next read of it fails: nothing is sent.
func TestAFailedBookReadIsNotSent(t *testing.T) {
	s := newStack(t)
	var armed atomic.Bool
	s.db.AddQueryHook(queryHook(func(_ context.Context, q string) error {
		if strings.Contains(q, `FROM "opds_catalog_book"`) && armed.CompareAndSwap(true, false) {
			return errors.New("injected read failure")
		}
		return nil
	}))
	s.deliveryCfg.Prepare = func(ctx context.Context, b *models.Book, f string, limit int64) (delivery.File, error) {
		if _, err := s.db.Exec(`UPDATE opds_catalog_book SET approved = false WHERE id = ?`, b.ID); err != nil {
			return delivery.File{}, err
		}
		armed.Store(true)
		return smallFile(ctx, b, f, limit)
	}
	d := openCard(t, s)
	d.tapEPUB(t)
	job := d.waitJob(t, models.DeliveryFailed)
	assert.Equal(t, models.OutcomeUnavailable, *job.Outcome)
	assert.Empty(t, d.fake.Calls("sendDocument"))
}

// B4. The read after preparing fails, the book still visible: nothing is
// sent on the earlier read; the next attempt reads it and sends.
func TestAFailedFreshReadWaitsForTheNextAttempt(t *testing.T) {
	s := newStack(t)
	var counting atomic.Bool
	var workerReads atomic.Int32
	s.db.AddQueryHook(queryHook(func(ctx context.Context, q string) error {
		_, handler := database.InboxLeaseFrom(ctx)
		if !counting.Load() || handler || !strings.Contains(q, `FROM "opds_catalog_book"`) {
			return nil
		}
		if workerReads.Add(1) == 2 { // the first attempt's read after preparing
			return errors.New("injected read failure")
		}
		return nil
	}))
	s.deliveryCfg.Prepare = smallFile
	d := openCard(t, s)
	counting.Store(true) // the delivery's reads only
	d.tapEPUB(t)
	job := d.waitJob(t, models.DeliverySent)
	assert.Equal(t, int16(2), job.Attempt, "sent by the attempt after the failed read")
	d.fake.Quiet(t, "sendDocument", 1, 200*time.Millisecond)
}

// B4. The gate itself cannot check the book: no upload; the job is tried a
// few times, then closed, and the chat is told the library failed.
func TestAGateThatCannotCheckTheBookSendsNothing(t *testing.T) {
	s := newStack(t)
	var failures atomic.Int32
	s.db.AddQueryHook(queryHook(func(_ context.Context, q string) error {
		if strings.Contains(q, "FROM opds_catalog_book WHERE id =") && strings.Contains(q, "FOR SHARE") {
			failures.Add(1)
			return errors.New("injected check failure")
		}
		return nil
	}))
	s.deliveryCfg.Prepare = smallFile
	d := openCard(t, s)
	d.tapEPUB(t)
	job := d.waitJob(t, models.DeliveryFailed)
	assert.Equal(t, models.OutcomeLibraryFailed, *job.Outcome)
	assert.Equal(t, int32(3), failures.Load(), "one check per attempt, MaxAttempts attempts")
	assert.Empty(t, d.fake.Calls("sendDocument"))
	told := d.told().Params["text"]
	assert.True(t, strings.HasPrefix(told, "Не удалось отправить EPUB\n"), told)
	assert.Contains(t, told, "Библиотека сейчас не отвечает")
}

// F2. 63 unfinished jobs; two accounts are admitted at once: the service's
// limit of 64 holds.
func TestTheServiceLimitHoldsAcrossAccounts(t *testing.T) {
	s := newStack(t).start()
	a, b := s.seedUser("a"), s.seedUser("b")
	sa, _ := s.connect(a, botToken1)
	s.linkVia(botToken1, ownerTG, sa)
	sb, _ := s.connect(b, botToken2)
	s.linkVia(botToken2, otherTG, sb)
	s.bm.WaitIdle()
	s.delivery.Stop()
	var bindings []models.TelegramBinding
	require.NoError(t, s.db.Model(&bindings).Where("status = 'active'").Order("user_id").Select())
	require.Len(t, bindings, 2)
	_, err := s.db.Exec(`
		INSERT INTO telegram_delivery (user_id, binding_id, bot_ref, chat_id, book_id, format, origin)
		SELECT ?, ?, ?, ?, n, 'fb2', 'site' FROM generate_series(1, 63) n`,
		a, bindings[0].ID, bindings[0].BotRef, ownerTG)
	require.NoError(t, err)

	// Both admissions read the count before either inserts — unless they are
	// serialized, which is the point.
	var reads atomic.Int32
	both := make(chan struct{})
	s.db.AddQueryHook(queryHook(func(ctx context.Context, q string) error {
		if strings.Contains(q, "count(*) FILTER (WHERE user_id") {
			if reads.Add(1) == 2 {
				close(both)
			}
			select {
			case <-both:
			case <-time.After(time.Second):
			case <-ctx.Done():
			}
		}
		return nil
	}))
	results := make(chan database.Admission, 2)
	for i := range 2 {
		go func(bnd models.TelegramBinding, book int64) {
			_, admission, err := database.EnqueueTelegramDelivery(context.Background(), &models.TelegramDelivery{
				UserID: bnd.UserID, BindingID: bnd.ID, BotRef: bnd.BotRef, ChatID: bnd.TGUserID,
				BookID: book, Format: "fb2", Origin: models.DeliveryFromSite,
			}, 1000, 64)
			assert.NoError(t, err)
			results <- admission
		}(bindings[i], int64(1000+i))
	}
	got := []database.Admission{<-results, <-results}
	assert.ElementsMatch(t, []database.Admission{database.Admitted, database.BusyService}, got)
	assert.Equal(t, 64, s.count(`SELECT count(*) FROM telegram_delivery WHERE finished_at IS NULL`))
}

// Copy. The service is full because of other accounts: the toast does not
// claim the reader has three files on the way.
func TestAFullServiceSaysSo(t *testing.T) {
	s := newStack(t)
	s.deliveryCfg.Total = 2
	d := openCard(t, s)
	other := d.seedUser("other")
	var binding models.TelegramBinding
	require.NoError(t, d.db.Model(&binding).Where("status = 'active'").Select())
	_, err := d.db.Exec(`
		INSERT INTO telegram_delivery (user_id, binding_id, bot_ref, chat_id, book_id, format, origin, next_attempt_at)
		SELECT ?, ?, ?, ?, n, 'fb2', 'site', now() + interval '1 hour' FROM generate_series(1, 2) n`,
		other, binding.ID, binding.BotRef, ownerTG)
	require.NoError(t, err)
	d.tapEPUB(t)
	assert.Equal(t, "Сейчас много отправок, попробуй через минуту.", d.answers(t, 1)[0])
}
