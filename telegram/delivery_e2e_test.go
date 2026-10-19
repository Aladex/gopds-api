package telegram_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/internal/scanfixture"
	"gopds-api/models"
	"gopds-api/telegram/delivery"
	"gopds-api/telegram/tgtest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Slice S2: the legacy card's format buttons ask the delivery service, and
// the file arrives as a durable job.

// deliveryStack is a bound bot with the book card of the fixture book open.
type deliveryStack struct {
	*stack
	card   tgtest.Call
	bookID int64
	// base is how many messages the chat had before the first press.
	base int
}

// openCard starts s (configured by the caller), binds the bot and gives the
// chat a card of the old interface for the multi-author fixture book: its
// file buttons on a message of the chat, as the cards the legacy bot sent
// stay in chats after S6. Their presses make the S2 delivery jobs these tests
// are about (router.legacyDownload); the v2 card's own presses are the S4
// tests'.
func openCard(t *testing.T, s *stack) *deliveryStack {
	t.Helper()
	s.start()
	user := s.seedUser("reader")
	secret, _ := s.connect(user, botToken1)
	s.linkVia(botToken1, ownerTG, secret)
	home := s.waitMessage(ownerTG, 1)
	bookID := s.books[scanfixture.EntryMultiAuthor]
	return &deliveryStack{stack: s, card: oldCard(t, &home, bookID), bookID: bookID, base: 1}
}

// oldCard is message on, carrying the old interface's file buttons for
// bookID.
func oldCard(t *testing.T, on *tgtest.Call, bookID int64) tgtest.Call {
	t.Helper()
	id := strconv.FormatInt(bookID, 10)
	markup, err := json.Marshal(map[string]any{"inline_keyboard": [][]map[string]string{
		{{"text": "📄 FB2", "callback_data": "download:fb2:" + id}, {"text": "📚 EPUB", "callback_data": "download:epub:" + id}},
		{{"text": "📱 MOBI", "callback_data": "download:mobi:" + id}, {"text": "🗂 ZIP", "callback_data": "download:zip:" + id}},
	}})
	require.NoError(t, err)
	card := *on
	card.Params = map[string]string{"chat_id": on.Params["chat_id"], "text": "война и МИР", "reply_markup": string(markup)}
	return card
}

// told is the chat's first message after the card: what the job told.
func (d *deliveryStack) told() tgtest.Call { return d.waitMessage(ownerTG, d.base+1) }

// tapEPUB presses the card's EPUB button.
func (d *deliveryStack) tapEPUB(t *testing.T) {
	t.Helper()
	rec := d.hook(botToken1).Post(t, d.u.Tap(t, ownerTG, &d.card, "📚 EPUB"))
	require.Equal(t, http.StatusOK, rec.Code)
}

// job is the only delivery row.
func (d *deliveryStack) job(t *testing.T) models.TelegramDelivery {
	t.Helper()
	var jobs []models.TelegramDelivery
	require.NoError(t, d.db.Model(&jobs).Select())
	require.Len(t, jobs, 1)
	return jobs[0]
}

// waitJob waits until the only job reaches state.
func (d *deliveryStack) waitJob(t *testing.T, state string) models.TelegramDelivery {
	t.Helper()
	require.Eventually(t, func() bool {
		return d.count(`SELECT count(*) FROM telegram_delivery WHERE state = ?`, state) == 1
	}, 10*time.Second, 20*time.Millisecond, "job never reached %s", state)
	return d.job(t)
}

// answers waits for n callback answers and returns their texts.
func (d *deliveryStack) answers(t *testing.T, n int) []string {
	t.Helper()
	var texts []string
	for _, c := range d.fake.WaitCalls(t, "answerCallbackQuery", n, 5*time.Second) {
		texts = append(texts, c.Params["text"])
	}
	return texts
}

// (a) A press sends the file: acknowledged at once, «uploading» while it is
// prepared, then the document with its caption — and nothing else attached.
func TestATapDeliversTheFile(t *testing.T) {
	d := openCard(t, newStack(t))
	d.tapEPUB(t)

	doc := d.fake.WaitAnswered(t, "sendDocument", 1, 15*time.Second)[0]
	job := d.waitJob(t, models.DeliverySent)
	assert.Equal(t, "Готовлю EPUB…", d.answers(t, 1)[0])
	actions := d.fake.Calls("sendChatAction")
	require.NotEmpty(t, actions)
	assert.Equal(t, "upload_document", actions[0].Params["action"])

	file := doc.Files["document"]
	assert.True(t, strings.HasSuffix(file.Filename, ".epub"), file.Filename)
	assert.True(t, bytes.HasPrefix(file.Data, []byte("PK")), "an EPUB is a zip")
	assert.True(t, strings.HasPrefix(doc.Params["caption"], "война и МИР — "), doc.Params["caption"])
	assert.Contains(t, doc.Params["caption"], "\nEPUB · ")
	_, hasMarkup := doc.Params["reply_markup"]
	assert.False(t, hasMarkup, "the document carries no keyboard")
	require.NotNil(t, job.TGMessageID)
	assert.Equal(t, doc.MessageID, *job.TGMessageID)
	assert.Equal(t, int64(len(file.Data)), *job.FileBytes)
}

// (b) Two presses on the same button, 10 ms apart: one file; the second press
// hears that it is already being prepared.
func TestTwoTapsMakeOneFile(t *testing.T) {
	s := newStack(t)
	gate := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gate) }) }
	t.Cleanup(release)
	s.deliveryCfg.Prepare = func(ctx context.Context, b *models.Book, f string, limit int64) (delivery.File, error) {
		select {
		case <-gate:
		case <-ctx.Done():
			return delivery.File{}, ctx.Err()
		}
		return delivery.PrepareFile(ctx, b, f, limit)
	}
	d := openCard(t, s)

	d.tapEPUB(t)
	time.Sleep(10 * time.Millisecond)
	d.tapEPUB(t)
	assert.Equal(t, []string{"Готовлю EPUB…", "Файл уже готовится"}, d.answers(t, 2))

	release()
	d.waitJob(t, models.DeliverySent)
	d.fake.Quiet(t, "sendDocument", 1, 300*time.Millisecond)
}

// (c) A 429 with retry_after 1: the same job tries exactly once more, after
// the wait, and one file arrives.
func TestARateLimitIsWaitedOut(t *testing.T) {
	s := newStack(t)
	s.fake.On(tgtest.Rule{Method: "sendDocument", Reply: tgtest.TooManyRequests(1), Times: 1})
	d := openCard(t, s)
	d.tapEPUB(t)

	d.waitJob(t, models.DeliverySent)
	docs := d.fake.Calls("sendDocument")
	require.Len(t, docs, 2)
	assert.GreaterOrEqual(t, docs[1].At.Sub(docs[0].At), time.Second)
	assert.Len(t, d.fake.AnsweredCalls("sendDocument"), 2)
	assert.Equal(t, 1, d.count(`SELECT count(*) FROM telegram_delivery`))
}

const uncertainText = "Не удалось подтвердить отправку\nвойна и МИР · EPUB\n\n" +
	"Файл мог уже появиться в чате. Проверь последние сообщения.\nПовторная отправка может создать второй файл."

// (d) Telegram read the file and never answered: the outcome is unknown. The
// chat is told so, with a button that says what pressing it does — and the
// file is not sent again, not even by another process afterwards.
func TestAnUnansweredSendIsNotRepeated(t *testing.T) {
	s := newStack(t)
	s.clientTimeout = time.Second
	s.fake.On(tgtest.Rule{Method: "sendDocument", Reply: s.fake.Hang(5 * time.Second)})
	d := openCard(t, s)
	d.tapEPUB(t)

	job := d.waitJob(t, models.DeliveryUncertain)
	assert.Equal(t, models.OutcomeUncertain, *job.Outcome)
	told := d.told()
	assert.Equal(t, uncertainText, told.Params["text"])
	assert.Equal(t, [][]string{{"Отправить ещё раз"}}, tgtest.ButtonTexts(t, &told))

	other := d.secondPod()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	other.bm.StartInboxSweeper(ctx, time.Hour)
	time.Sleep(time.Second)
	d.fake.Quiet(t, "sendDocument", 1, 200*time.Millisecond)
	assert.Equal(t, models.DeliveryUncertain, d.job(t).State)
}

// failFirstDial makes the first sendDocument fail the way a refused
// connection does, before anything left the process.
type failFirstDial struct {
	done atomic.Bool
}

func (f *failFirstDial) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.HasSuffix(r.URL.Path, "/sendDocument") && !f.done.Swap(true) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	}
	return http.DefaultTransport.RoundTrip(r)
}

// (e) A send that never left — the connection was refused — goes back to the
// queue and is sent on the next try: one file.
func TestARefusedConnectionIsRetried(t *testing.T) {
	s := newStack(t)
	s.transport = &failFirstDial{}
	d := openCard(t, s)
	d.tapEPUB(t)

	job := d.waitJob(t, models.DeliverySent)
	assert.Equal(t, int16(2), job.Attempt, "claimed once more after the refused connection")
	assert.Len(t, d.fake.Calls("sendDocument"), 1, "only the second try reached Telegram")
}

// (f) A file over the upload limit is not uploaded: the chat is told, with the
// other formats and the book on the site.
func TestAFileOverTheLimitIsNotUploaded(t *testing.T) {
	s := newStack(t)
	s.deliveryCfg.Prepare = func(ctx context.Context, b *models.Book, f string, limit int64) (delivery.File, error) {
		return delivery.File{Name: "big.epub", Data: make([]byte, 51*1000*1000)}, nil
	}
	d := openCard(t, s)
	d.tapEPUB(t)

	job := d.waitJob(t, models.DeliveryFailed)
	assert.Equal(t, models.OutcomeTooLarge, *job.Outcome)
	told := d.told()
	assert.Equal(t, "Этот файл слишком большой для отправки ботом\nвойна и МИР · EPUB · больше 50 МБ\n\n"+
		"Его можно скачать на сайте библиотеки.", told.Params["text"])
	assert.Equal(t, [][]string{{"FB2", "MOBI", "ZIP"}, {"Открыть на сайте"}}, tgtest.ButtonTexts(t, &told))
	assert.Equal(t, delivery.BookURL(stackSiteURL, d.bookID), tgtest.Keyboard(t, &told)[1][0].URL)
	assert.Empty(t, d.fake.Calls("sendDocument"))
}

// (g) The book is withdrawn while its file is being prepared: it is not sent.
func TestABookWithdrawnMidwayIsNotSent(t *testing.T) {
	s := newStack(t)
	s.deliveryCfg.Prepare = func(ctx context.Context, b *models.Book, f string, limit int64) (delivery.File, error) {
		if _, err := s.db.Exec(`UPDATE opds_catalog_book SET approved = false WHERE id = ?`, b.ID); err != nil {
			return delivery.File{}, err
		}
		return delivery.File{Name: "book.epub", Data: []byte("PK small")}, nil
	}
	d := openCard(t, s)
	d.tapEPUB(t)

	job := d.waitJob(t, models.DeliveryFailed)
	assert.Equal(t, models.OutcomeUnavailable, *job.Outcome)
	assert.Equal(t, "Эта книга больше недоступна.", d.told().Params["text"])
	assert.Empty(t, d.fake.Calls("sendDocument"))
}

// The chat is unbound while the file is prepared: the job ends unavailable,
// nothing is sent, and the chat — no longer the reader's — is not written to.
func TestAnUnboundChatIsNotWrittenTo(t *testing.T) {
	s := newStack(t)
	s.deliveryCfg.Prepare = func(ctx context.Context, b *models.Book, f string, limit int64) (delivery.File, error) {
		if _, err := s.db.Exec(`UPDATE telegram_binding SET status = 'revoked', revoked_at = now() WHERE status = 'active'`); err != nil {
			return delivery.File{}, err
		}
		return delivery.File{Name: "book.epub", Data: []byte("PK small")}, nil
	}
	d := openCard(t, s)
	d.tapEPUB(t)

	job := d.waitJob(t, models.DeliveryFailed)
	assert.Equal(t, models.OutcomeUnavailable, *job.Outcome)
	d.delivery.WaitIdle()
	d.fake.Quiet(t, "sendMessage", d.base, 200*time.Millisecond)
	assert.Empty(t, d.fake.Calls("sendDocument"))
	assert.Empty(t, d.fake.Calls("editMessageText"))
}

// (h) A process that stalls while preparing loses the job to another, which
// prepares it again and sends it: one file. The stalled one, when it wakes,
// cannot send — its lease is gone.
func TestAStallWhilePreparingIsTakenOver(t *testing.T) {
	s := newStack(t)
	var calls atomic.Int32
	// Each holder's prepare stalls on its own gate: the first is a stuck
	// converter, the second keeps the new holder preparing while the old one
	// wakes and tries to send.
	gates := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	var onces [2]sync.Once
	open := func(i int) { onces[i].Do(func() { close(gates[i]) }) }
	// A defer, not a cleanup: it must run before the processes' Stop, which
	// waits for the stalled holders.
	defer func() { open(0); open(1) }()
	s.deliveryCfg.Prepare = func(ctx context.Context, b *models.Book, f string, limit int64) (delivery.File, error) {
		if n := int(calls.Add(1)); n <= len(gates) {
			<-gates[n-1] // ignores ctx on purpose
		}
		return delivery.PrepareFile(context.WithoutCancel(ctx), b, f, limit)
	}
	// The stalled holder must not notice it lost the lease before it tries
	// to send — no heartbeat in time — so what stops it is the fence checked
	// when the send starts, not its own cancellation.
	s.deliveryCfg.Heartbeat = time.Hour
	s.deliveryCfg.Lease = 10 * time.Second
	// One worker per process: the takeover is the other process's, and the
	// first process going idle means the old holder is done.
	s.deliveryCfg.Workers = 1
	d := openCard(t, s)
	d.tapEPUB(t)
	require.Eventually(t, func() bool { return calls.Load() == 1 }, 5*time.Second, 10*time.Millisecond)

	// The holder's lease runs out; another process takes the job over and
	// is still preparing it when the old holder wakes.
	_, err := d.db.Exec(`UPDATE telegram_delivery SET lease_expires_at = now() - interval '1 second'`)
	require.NoError(t, err)
	other := d.secondPod()
	require.Eventually(t, func() bool { return calls.Load() == 2 }, 5*time.Second, 10*time.Millisecond)
	open(0)
	d.delivery.WaitIdle()
	d.fake.Quiet(t, "sendDocument", 0, 200*time.Millisecond)
	assert.Equal(t, models.DeliveryPreparing, d.job(t).State, "the old holder left the job to the new one")

	open(1)
	d.waitJob(t, models.DeliverySent)
	other.delivery.WaitIdle()
	d.fake.Quiet(t, "sendDocument", 1, 200*time.Millisecond)
}

// (h) A process that stops while sending: the job is closed uncertain by the
// sweeper of another — never sent twice.
func TestAStopWhileSendingIsUncertain(t *testing.T) {
	s := newStack(t)
	parked := make(chan struct{})
	var parkedOnce sync.Once
	letGo := func() { parkedOnce.Do(func() { close(parked) }) }
	t.Cleanup(letGo)
	var entered atomic.Int32
	s.fake.On(tgtest.Rule{
		Method: "sendDocument",
		Match:  func(tgtest.Call) bool { entered.Add(1); return true },
		Reply: func(w http.ResponseWriter, r *http.Request, c *tgtest.Call) {
			select {
			case <-parked:
			case <-r.Context().Done():
			}
		},
	})
	d := openCard(t, s)
	d.tapEPUB(t)
	require.Eventually(t, func() bool { return entered.Load() == 1 }, 10*time.Second, 10*time.Millisecond)

	_, err := d.db.Exec(`UPDATE telegram_delivery SET lease_expires_at = now() - interval '1 second'`)
	require.NoError(t, err)
	d.secondPod()
	job := d.waitJob(t, models.DeliveryUncertain)
	assert.Equal(t, models.OutcomeUncertain, *job.Outcome)
	letGo()
	time.Sleep(500 * time.Millisecond)
	assert.Equal(t, int32(1), entered.Load(), "exactly one upload")
}

// (i) The owner blocked the bot: the job fails as bot_blocked, the binding is
// marked, and nothing more is written to a chat that refuses.
func TestABlockedBotFailsTheJob(t *testing.T) {
	s := newStack(t)
	s.fake.On(tgtest.Rule{Method: "sendDocument", Reply: tgtest.Forbidden("")})
	d := openCard(t, s)
	d.tapEPUB(t)

	job := d.waitJob(t, models.DeliveryFailed)
	assert.Equal(t, models.OutcomeBotBlocked, *job.Outcome)
	assert.Equal(t, 1, d.count(`SELECT count(*) FROM telegram_binding WHERE status = 'active' AND blocked_at IS NOT NULL`))
	d.settle(ownerTG, d.base)
}
