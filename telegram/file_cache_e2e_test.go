package telegram_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"gopds-api/internal/scanfixture"
	"gopds-api/models"
	"gopds-api/telegram/delivery"
	"gopds-api/telegram/tgtest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Slice P1-4 (plan.md §6 P1-4): a bot keeps the file_id Telegram gave it for
// a book in a format, and sends the same file again by it — no preparing, no
// upload — while it still stands for the same file.

// uploads and resends split the sendDocument calls into those that uploaded
// a file and those that sent a file_id.
func uploads(f *tgtest.FakeBotAPI) int {
	n := 0
	for _, c := range f.Calls("sendDocument") {
		if _, ok := c.Files["document"]; ok {
			n++
		}
	}
	return n
}

func resends(f *tgtest.FakeBotAPI) int {
	n := 0
	for _, c := range f.Calls("sendDocument") {
		if _, ok := c.Files["document"]; !ok && c.Params["document"] != "" {
			n++
		}
	}
	return n
}

// sentJobs is how many jobs were sent.
func (s *stack) sentJobs() int {
	return s.count(`SELECT count(*) FROM telegram_delivery WHERE state = ?`, models.DeliverySent)
}

// waitSentJobs waits until n jobs were sent.
func (s *stack) waitSentJobs(n int) {
	s.t.Helper()
	require.Eventually(s.t, func() bool { return s.sentJobs() == n }, 15*time.Second, 20*time.Millisecond,
		"%d jobs sent, want %d", s.sentJobs(), n)
	s.delivery.WaitIdle()
}

// pressEPUB presses the old card's EPUB button as a new request: the jobs
// before it ended long enough ago not to count as its repeat
// (database.DeliveryRepeatWindow).
func (d *deliveryStack) pressEPUB(t *testing.T) {
	t.Helper()
	_, err := d.db.Exec(`UPDATE telegram_delivery SET finished_at = now() - interval '1 hour' WHERE finished_at IS NOT NULL`)
	require.NoError(t, err)
	d.tapEPUB(t)
}

// The second send of the same book in the same format is the bot's own
// file: no upload, the same document, a toast that prepares nothing.
func TestTheSecondSendIsTheBotsOwnFile(t *testing.T) {
	d := openCard(t, newStack(t))
	d.pressEPUB(t)
	d.waitSentJobs(1)
	require.Equal(t, 1, uploads(d.fake))
	first := d.fake.Calls("sendDocument")[0]
	var entry models.TelegramFileCache
	require.NoError(t, d.db.Model(&entry).Select())
	assert.Equal(t, models.TelegramFileCache{
		BotRef: entry.BotRef, BookID: d.bookID, Format: "epub", SourceRev: entry.SourceRev,
		ConverterRev: delivery.ConverterRev, FileID: entry.FileID, FileUniqueID: entry.FileUniqueID,
		Bytes: int64(first.Files["document"].Size), CreatedAt: entry.CreatedAt,
	}, entry)
	assert.NotEmpty(t, entry.SourceRev, "the book's md5")
	assert.True(t, strings.HasPrefix(entry.FileID, "doc-"), entry.FileID)

	d.pressEPUB(t)
	d.waitSentJobs(2)
	assert.Equal(t, 1, uploads(d.fake), "nothing uploaded again")
	require.Equal(t, 1, resends(d.fake))
	again := d.fake.Calls("sendDocument")[1]
	assert.Equal(t, entry.FileID, again.Params["document"])
	assert.Equal(t, first.Params["caption"], again.Params["caption"], "the same caption, the same size")
	assert.Equal(t, []string{"Готовлю EPUB…", "Отправляю EPUB…"}, d.answers(t, 2))
	assert.Equal(t, 2, d.count(`SELECT count(*) FROM telegram_delivery WHERE outcome = 'sent'`))
}

// Another format, or the same book after its file changed, is uploaded.
func TestAChangedFileIsUploadedAgain(t *testing.T) {
	d := openCard(t, newStack(t))
	d.pressEPUB(t)
	d.waitSentJobs(1)

	_, err := d.db.Exec(`UPDATE opds_catalog_book SET md5 = 'a-new-archive' WHERE id = ?`, d.bookID)
	require.NoError(t, err)
	d.pressEPUB(t)
	d.waitSentJobs(2)
	assert.Equal(t, 2, uploads(d.fake))
	assert.Zero(t, resends(d.fake))
	assert.Equal(t, 1, d.count(`SELECT count(*) FROM telegram_file_cache WHERE source_rev = 'a-new-archive'`),
		"the new file replaced the old one")
	assert.Equal(t, 1, d.count(`SELECT count(*) FROM telegram_file_cache`))

	_, err = d.db.Exec(`UPDATE telegram_file_cache SET converter_rev = converter_rev - 1`)
	require.NoError(t, err)
	d.pressEPUB(t)
	d.waitSentJobs(3)
	assert.Equal(t, 3, uploads(d.fake), "a file of an older converter is not sent")
}

// A book without an md5 is never cached: every send uploads.
func TestABookWithoutMD5IsNotCached(t *testing.T) {
	d := openCard(t, newStack(t))
	_, err := d.db.Exec(`UPDATE opds_catalog_book SET md5 = '' WHERE id = ?`, d.bookID)
	require.NoError(t, err)
	d.pressEPUB(t)
	d.waitSentJobs(1)
	d.pressEPUB(t)
	d.waitSentJobs(2)
	assert.Equal(t, 2, uploads(d.fake))
	assert.Zero(t, d.count(`SELECT count(*) FROM telegram_file_cache`))
}

// A file_id is the bot's own: another bot uploads its own file.
func TestAFileIsCachedPerBot(t *testing.T) {
	d := openCard(t, newStack(t))
	d.pressEPUB(t)
	d.waitSentJobs(1)

	other := d.seedUser("other reader")
	secret, _ := d.connect(other, botToken2)
	d.linkVia(botToken2, otherTG, secret)
	d.waitMessage(otherTG, 1)
	card := oldCard(t, &d.fake.Calls("sendMessage")[len(d.fake.Calls("sendMessage"))-1], d.bookID)
	require.Equal(t, http.StatusOK, d.hook(botToken2).Post(t, d.u.Tap(t, otherTG, &card, "📚 EPUB")).Code)
	d.waitSentJobs(2)
	assert.Equal(t, 2, uploads(d.fake))
	assert.Equal(t, 2, d.count(`SELECT count(*) FROM telegram_file_cache`))
}

// A cache hit passes the same checks as an upload: a binding ended or a book
// withdrawn while the job waited sends nothing.
func TestACachedSendStillChecksBindingAndBook(t *testing.T) {
	for name, withdraw := range map[string]string{
		"binding revoked": `UPDATE telegram_binding SET status = 'revoked', revoked_at = now()`,
		"book withdrawn":  `UPDATE opds_catalog_book SET approved = false`,
	} {
		t.Run(name, func(t *testing.T) {
			d := openCard(t, newStack(t))
			d.pressEPUB(t)
			d.waitSentJobs(1)
			d.delivery.Stop()

			d.pressEPUB(t)
			require.Eventually(t, func() bool {
				return d.count(`SELECT count(*) FROM telegram_delivery WHERE state = 'queued'`) == 1
			}, 5*time.Second, 20*time.Millisecond)
			_, err := d.db.Exec(withdraw)
			require.NoError(t, err)
			next := d.secondPod()
			require.Eventually(t, func() bool {
				return next.count(`SELECT count(*) FROM telegram_delivery WHERE outcome = ?`, models.OutcomeUnavailable) == 1
			}, 15*time.Second, 20*time.Millisecond)
			next.delivery.WaitIdle()
			assert.Len(t, next.fake.Calls("sendDocument"), 1, "only the first send")
		})
	}
}

// A file_id Telegram no longer knows is forgotten and the file uploaded,
// once: the job is sent, never uncertain.
func TestARefusedFileIsUploadedOnce(t *testing.T) {
	d := openCard(t, newStack(t))
	d.pressEPUB(t)
	d.waitSentJobs(1)
	var before models.TelegramFileCache
	require.NoError(t, d.db.Model(&before).Select())

	d.fake.ForgetFiles(botToken1)
	d.pressEPUB(t)
	d.waitSentJobs(2)
	calls := d.fake.Calls("sendDocument")
	require.Len(t, calls, 3, "upload, the refused file_id, one upload")
	assert.Equal(t, before.FileID, calls[1].Params["document"])
	assert.Contains(t, calls[2].Files, "document")
	assert.Zero(t, d.count(`SELECT count(*) FROM telegram_delivery WHERE state = 'uncertain'`))
	var after models.TelegramFileCache
	require.NoError(t, d.db.Model(&after).Select())
	assert.Equal(t, before.FileID, after.FileID, "the fake gives the same file the same id again")
	assert.Equal(t, 1, d.count(`SELECT count(*) FROM telegram_file_cache`))
}

// When the upload after a refused file_id fails too, the job ends as that
// failure: no third try, no loop.
func TestARefusedFileIsNotRetriedInALoop(t *testing.T) {
	d := openCard(t, newStack(t))
	d.pressEPUB(t)
	d.waitSentJobs(1)

	d.fake.ForgetFiles(botToken1)
	d.fake.On(tgtest.Rule{Method: "sendDocument", Match: func(c tgtest.Call) bool { _, ok := c.Files["document"]; return ok },
		Reply: tgtest.Error(http.StatusBadRequest, "Bad Request: wrong file identifier/HTTP URL specified")})
	d.pressEPUB(t)
	require.Eventually(t, func() bool {
		return d.count(`SELECT count(*) FROM telegram_delivery WHERE outcome = ?`, models.OutcomeSendRejected) == 1
	}, 15*time.Second, 20*time.Millisecond)
	d.delivery.WaitIdle()
	assert.Len(t, d.fake.Calls("sendDocument"), 3)
	assert.Zero(t, d.count(`SELECT count(*) FROM telegram_file_cache`), "the refused file_id is forgotten")
}

// On the v2 card the button says the file is being sent, not prepared; the
// status of a slow cached send from an old card says the same.
func TestACachedSendNeverSaysPreparing(t *testing.T) {
	v := newV2(t)
	_, card := v.openFixtureCard()
	v.tap(&card, "EPUB")
	v.waitSentJobs(1)

	gate := make(chan struct{})
	t.Cleanup(func() { close(gate) })
	v.fake.On(tgtest.Rule{Method: "sendDocument", Match: func(c tgtest.Call) bool { return c.Params["document"] != "" },
		Reply: v.fake.Gate(gate), Times: 1})
	card = v.latest(card.MessageID)
	v.tap(&card, "EPUB")
	marked := v.latest(card.MessageID)
	assert.Equal(t, "⏳ Отправляю EPUB…", tgtest.ButtonTexts(t, &marked)[0][0])
	assert.Equal(t, "Отправляю EPUB…", v.lastAnswer())
	assertDisabled(t, &marked, "⏳ Отправляю EPUB…")
	v.fake.WaitCalls(t, "sendDocument", 2, 5*time.Second)
}

// An old card's slow cached send shows «sending» in its status message.
func TestASlowCachedSendTellsSending(t *testing.T) {
	s := newStack(t)
	s.deliveryCfg.StatusAfter = 50 * time.Millisecond
	d := openCard(t, s)
	d.pressEPUB(t)
	d.waitSentJobs(1)

	gate := make(chan struct{})
	d.fake.On(tgtest.Rule{Method: "sendDocument", Match: func(c tgtest.Call) bool { return c.Params["document"] != "" },
		Reply: d.fake.Gate(gate), Times: 1})
	texts := len(d.messagesTo(ownerTG))
	d.pressEPUB(t)
	require.Eventually(t, func() bool { return len(d.messagesTo(ownerTG)) > texts }, 5*time.Second, 10*time.Millisecond)
	close(gate)
	d.waitSentJobs(2)
	for _, text := range d.messagesTo(ownerTG)[texts:] {
		assert.NotContains(t, text, "Готовлю")
	}
	assert.True(t, strings.HasPrefix(d.messagesTo(ownerTG)[texts], "Отправляю EPUB"), d.messagesTo(ownerTG)[texts])
}

// The site's «В Telegram» sends the bot's own file too, and tells the site
// nothing but the outcome.
func TestTheSiteSendsTheBotsOwnFile(t *testing.T) {
	s := newStack(t).start()
	user := connectedReader(t, s)
	ws := s.siteSession(user)
	book := s.books[scanfixture.EntryMultiAuthor]

	ws.ask(book)
	assert.Equal(t, "sent", ws.reply(15*time.Second).Status)
	ws.ask(book)
	assert.Equal(t, siteReply{Type: "telegram", BookID: book, Status: "sent"}, ws.reply(15*time.Second))
	assert.Equal(t, 1, uploads(s.fake))
	assert.Equal(t, 1, resends(s.fake))
}

// No file_id ever leaves the bot: inline results carry none.
func TestNoFileIDInInlineResults(t *testing.T) {
	v := newV2(t)
	_, card := v.openFixtureCard()
	v.tap(&card, "EPUB")
	v.waitSentJobs(1)
	var entry models.TelegramFileCache
	require.NoError(t, v.db.Model(&entry).Select())
	a := v.ask(ownerTG, sender, "мир", "")
	require.NotEmpty(t, a.Results)
	raw, err := json.Marshal(a.Results)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), entry.FileID)
	assert.NotContains(t, a.call.Params["results"], entry.FileID)
	entryJSON, err := json.Marshal(entry)
	require.NoError(t, err)
	assert.NotContains(t, string(entryJSON), entry.FileID, "the model never serializes it")
}
