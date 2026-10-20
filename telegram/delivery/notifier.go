package delivery

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/models"

	"github.com/go-telegram/bot"
	tgbot "github.com/go-telegram/bot/models"
)

// longWaitNotice is the 429 wait worth telling the reader about; a shorter
// one passes before the notice would be read, and the notice would be one
// more message for Telegram to limit.
const longWaitNotice = 10 * time.Second

// captionRunes keeps a caption well inside Telegram's 1024 characters.
const captionRunes = 900

// notifier keeps the reader informed while a job runs: «uploading a
// document» in the chat header, a status message when the job takes a while
// (jobs from the bot only — on the site the reader is watching the site), and
// the outcome.
type notifier struct {
	s      *Service
	client *bot.Bot
	job    *models.TelegramDelivery
	book   *models.Book

	mu       sync.Mutex
	statusID int64
	stopped  bool
	// cached is a job sending the bot's file_id: nothing is prepared, and
	// nothing says so.
	cached bool
	done   chan struct{}
	once   sync.Once
}

// start begins the chat action and arms the status message.
func (n *notifier) start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(n.s.cfg.ChatActionEvery)
		defer ticker.Stop()
		status := time.NewTimer(n.s.cfg.StatusAfter)
		defer status.Stop()
		n.chatAction(ctx)
		for {
			select {
			case <-n.done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				n.chatAction(ctx)
			case <-status.C:
				// A card shows the job on its own button (plan-ui.md §1.8).
				if n.job.FromChat() && !n.job.FromPanel() {
					n.showStatus(ctx, n.statusNow())
				}
			}
		}
	}()
}

// statusNow is the status of a job still on its way: preparing, unless the
// bot has the file already.
func (n *notifier) statusNow() string {
	n.mu.Lock()
	cached := n.cached
	n.mu.Unlock()
	if cached {
		return statusSending(n.book, n.job.Format)
	}
	return statusPreparing(n.book, n.job.Format)
}

// uncache is a job whose file_id was refused: its file is prepared now.
func (n *notifier) uncache() {
	n.mu.Lock()
	n.cached = false
	n.mu.Unlock()
}

// stop ends the chat action and the pending status message.
func (n *notifier) stop() {
	n.once.Do(func() { close(n.done) })
}

func (n *notifier) chatAction(ctx context.Context) {
	if n.s.cfg.Redis != nil {
		key := fmt.Sprintf("tg2:act:%d:%d", n.job.BotRef, n.job.ChatID)
		if ok, err := n.s.cfg.Redis.SetNX(key, 1, n.s.cfg.ChatActionEvery-500*time.Millisecond).Result(); err == nil && !ok {
			return
		}
	}
	_, _ = n.client.SendChatAction(ctx, &bot.SendChatActionParams{ChatID: n.job.ChatID, Action: tgbot.ChatActionUploadDocument})
}

// showStatus sends the status message, or edits it when there is one.
func (n *notifier) showStatus(ctx context.Context, text string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.stopped {
		return
	}
	if n.statusID != 0 {
		n.edit(ctx, text, nil)
		return
	}
	msg, err := n.client.SendMessage(ctx, &bot.SendMessageParams{ChatID: n.job.ChatID, Text: text, LinkPreviewOptions: noPreview()})
	if err != nil {
		logging.Warnf("Delivery: job %d: status message: %v", n.job.ID, err)
		return
	}
	n.statusID = int64(msg.ID)
	if err := database.SetTelegramDeliveryStatusMessage(context.WithoutCancel(ctx), n.job.ID, n.statusID); err != nil {
		logging.Warnf("Delivery: job %d: remembering its status message: %v", n.job.ID, err)
	}
}

// sending updates a status message already shown.
func (n *notifier) sending(ctx context.Context) {
	n.mu.Lock()
	shown := n.statusID != 0
	n.mu.Unlock()
	if shown {
		n.showStatus(ctx, statusSending(n.book, n.job.Format))
	}
}

// waiting tells the reader Telegram asked for a pause.
func (n *notifier) waiting(ctx context.Context) {
	if n.job.FromChat() {
		n.showStatus(ctx, copyRateLimited)
	}
}

// sent removes the status message: the file speaks for itself.
func (n *notifier) sent(ctx context.Context) {
	n.stop()
	n.mu.Lock()
	defer n.mu.Unlock()
	n.stopped = true
	if n.statusID == 0 {
		return
	}
	if _, err := n.client.DeleteMessage(ctx, &bot.DeleteMessageParams{ChatID: n.job.ChatID, MessageID: int(n.statusID)}); err != nil {
		n.edit(ctx, copySent, nil)
	}
}

// finish tells the reader how a job ended that did not end in the file. The
// chat is written to only for outcomes the reader can act on, and only for
// jobs asked for in the chat: a job from the site is answered on the site.
func (n *notifier) finish(ctx context.Context, outcome string, _ int) {
	if n.done != nil {
		n.stop()
	}
	text, markup := outcomeScreen(n.s.cfg.SiteURL, n.book, n.job.Format, outcome)
	if text == "" || !n.job.FromChat() {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.stopped = true
	if n.statusID != 0 && n.edit(ctx, text, markup) {
		return
	}
	params := &bot.SendMessageParams{ChatID: n.job.ChatID, Text: text, LinkPreviewOptions: noPreview()}
	if markup != nil {
		params.ReplyMarkup = markup
	}
	if _, err := n.client.SendMessage(ctx, params); err != nil {
		logging.Warnf("Delivery: job %d: telling the outcome: %v", n.job.ID, err)
	}
}

// edit changes the status message; the caller holds mu.
func (n *notifier) edit(ctx context.Context, text string, markup *tgbot.InlineKeyboardMarkup) bool {
	params := &bot.EditMessageTextParams{
		ChatID: n.job.ChatID, MessageID: int(n.statusID), Text: text, LinkPreviewOptions: noPreview(),
	}
	if markup != nil {
		params.ReplyMarkup = markup
	}
	_, err := n.client.EditMessageText(ctx, params)
	return err == nil
}

func noPreview() *tgbot.LinkPreviewOptions {
	off := true
	return &tgbot.LinkPreviewOptions{IsDisabled: &off}
}

// The reader-facing words of a delivery. Russian, informal, as the concept's
// §C.5 screens put them; the first line names what happened.
const (
	copySent        = "Файл отправлен"
	copyRateLimited = "Telegram попросил подождать. Продолжу автоматически."
)

// ScreenText is a toast for the press that asked for a file.
const (
	ToastPreparing   = "Готовлю %s…"
	ToastSending     = "Отправляю %s…"
	ToastInProgress  = "Файл уже готовится"
	ToastOnItsWay    = "Файл уже отправляется"
	ToastAlreadySent = "Файл уже отправлен"
	ToastJustEnded   = "Попробуй ещё раз через несколько секунд"
	ToastBusyAccount = "Уже готовлю три файла. Дождись их"
	ToastBusyService = "Сейчас много отправок, попробуй через минуту."
	ToastUnavailable = "Эта книга больше недоступна."
)

// StartToast is the toast for a press that started a job: a file the bot
// has already is not prepared, and is not said to be.
func StartToast(format string, cached bool) string {
	if cached {
		return fmt.Sprintf(ToastSending, upper(format))
	}
	return fmt.Sprintf(ToastPreparing, upper(format))
}

// RepeatToast is the toast for a press that repeats the request of job;
// cached is a job sending a file the bot has already.
func RepeatToast(job *models.TelegramDelivery, cached bool) string {
	switch {
	case job.FinishedAt == nil && cached:
		return ToastOnItsWay
	case job.FinishedAt == nil:
		return ToastInProgress
	case job.State == models.DeliverySent:
		return ToastAlreadySent
	}
	return ToastJustEnded
}

func upper(format string) string { return strings.ToUpper(format) }

// BookLine is «title — authors», or the title alone.
func BookLine(b *models.Book) string {
	var names []string
	for _, a := range b.Authors {
		if name := strings.TrimSpace(a.FullName); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return b.Title
	}
	return b.Title + " — " + strings.Join(names, ", ")
}

func statusPreparing(b *models.Book, format string) string {
	return fmt.Sprintf("Готовлю %s\n%s\n\nПришлю файл сюда, когда он будет готов.", upper(format), BookLine(b))
}

func statusSending(b *models.Book, format string) string {
	return fmt.Sprintf("Отправляю %s в Telegram\n%s", upper(format), BookLine(b))
}

// fileCaption names the book under the document, with the size of the file
// that was actually sent.
func fileCaption(b *models.Book, format string, size int) string {
	line := BookLine(b)
	if utf8.RuneCountInString(line) > captionRunes {
		line = string([]rune(line)[:captionRunes]) + "…"
	}
	return fmt.Sprintf("%s\n%s · %s", line, upper(format), HumanSize(size))
}

// HumanSize writes a size the Russian way: «850 КБ», «1,4 МБ».
func HumanSize(n int) string {
	const kb, mb = 1000, 1000 * 1000
	if n < mb {
		return fmt.Sprintf("%d КБ", (n+kb-1)/kb)
	}
	const tenths = 10
	whole, tenth := n/mb, (n%mb)/(mb/tenths)
	if tenth == 0 {
		return fmt.Sprintf("%d МБ", whole)
	}
	return fmt.Sprintf("%d,%d МБ", whole, tenth)
}

// outcomeScreen is what the chat is told about an outcome, with buttons that
// lead on: the same format again, the other formats (the bot's own download
// buttons), the book on the site. Empty text means nothing is said — the
// file itself, or a chat that cannot be written to.
func outcomeScreen(siteURL string, b *models.Book, format, outcome string) (string, *tgbot.InlineKeyboardMarkup) {
	title := b.Title
	switch outcome {
	case models.OutcomeConversionFailed:
		text := fmt.Sprintf("Не удалось подготовить %s\n%s\n\nПопробуй другой формат или открой книгу на сайте.", upper(format), title)
		return text, keyboard(siteURL, b.ID, format, "Повторить "+upper(format), true)
	case models.OutcomeSendRejected:
		text := fmt.Sprintf("Файл готов, но Telegram не принял отправку. Можно повторить\n%s · %s", title, upper(format))
		return text, keyboard(siteURL, b.ID, format, "Повторить "+upper(format), false)
	case models.OutcomeUncertain:
		text := fmt.Sprintf("Не удалось подтвердить отправку\n%s · %s\n\nФайл мог уже появиться в чате. Проверь последние сообщения.\n"+
			"Повторная отправка может создать второй файл.", title, upper(format))
		return text, keyboard("", b.ID, format, "Отправить ещё раз", false)
	case models.OutcomeTooLarge:
		text := fmt.Sprintf("Этот файл слишком большой для отправки ботом\n%s · %s · больше 50 МБ\n\nЕго можно скачать на сайте библиотеки.",
			title, upper(format))
		return text, keyboard(siteURL, b.ID, format, "", true)
	case models.OutcomeLibraryFailed:
		head := "Не удалось отправить " + upper(format)
		if title != "" { // the book itself may be what could not be read
			head += "\n" + title
		}
		return head + "\n\nБиблиотека сейчас не отвечает. Попробуй ещё раз чуть позже.",
			keyboard("", b.ID, format, "Повторить "+upper(format), false)
	case models.OutcomeNotFound, models.OutcomeUnavailable:
		return ToastUnavailable, nil
	}
	return "", nil
}

// keyboard builds an outcome's buttons: again (when again is named), the
// other formats (when others), and the book on the site (when siteURL is
// set). The download buttons are the legacy card's own callbacks.
func keyboard(siteURL string, bookID int64, format, again string, others bool) *tgbot.InlineKeyboardMarkup {
	id := strconv.FormatInt(bookID, 10)
	var rows [][]tgbot.InlineKeyboardButton
	if again != "" {
		rows = append(rows, []tgbot.InlineKeyboardButton{{Text: again, CallbackData: "download:" + format + ":" + id}})
	}
	if others {
		var row []tgbot.InlineKeyboardButton
		for _, f := range Formats {
			if f != format {
				row = append(row, tgbot.InlineKeyboardButton{Text: upper(f), CallbackData: "download:" + f + ":" + id})
			}
		}
		rows = append(rows, row)
	}
	if siteURL != "" {
		label := "Открыть на сайте"
		rows = append(rows, []tgbot.InlineKeyboardButton{{Text: label, URL: BookURL(siteURL, bookID)}})
	}
	if len(rows) == 0 {
		return nil
	}
	return &tgbot.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// BookURL is the site's page of one book.
func BookURL(siteURL string, bookID int64) string {
	return strings.TrimRight(siteURL, "/") + "/books/page/1?book_id=" + strconv.FormatInt(bookID, 10)
}
