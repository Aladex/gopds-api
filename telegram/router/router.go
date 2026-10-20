// Package router is the v2 bot (plan-ui.md): a pinned home panel, typed text
// as search with its own results panel, book cards rendered rich or plain,
// files through the delivery service — and no slash commands, /start and its
// deep links aside.
//
// Every update comes here from the telegram package's inbox dispatcher, under
// the update's inbox lease; durable writes check that lease. Nothing here
// keeps state in memory between updates: views, nodes and panels live in
// PostgreSQL, and a button carries a signed envelope (package cbv2) that
// names them.
package router

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gopds-api/config"
	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/services"
	"gopds-api/services/kindlesend"
	"gopds-api/telegram/delivery"
	"gopds-api/telegram/screens"

	"github.com/go-telegram/bot"
	tgbot "github.com/go-telegram/bot/models"
)

// Screen numbers a node's screen (plan.md §2.4). Values are stored; never
// reuse or renumber them.
type Screen int16

// The screens.
const (
	ScrHome Screen = 1 + iota
	ScrResultsBooks
	ScrResultsAuthors
	ScrBook
	ScrBookAnnotation
	// ScrNotice is a one-off message (a hint, an error) that became a panel
	// so its «Домой» works; it is never re-rendered.
	ScrNotice Screen = 100
)

// The router's defaults.
const (
	defaultSearchTimeout  = 3 * time.Second
	defaultTypingAfter    = 300 * time.Millisecond
	defaultPinCheckEvery  = 15 * time.Minute
	defaultRichRetryAfter = 7 * 24 * time.Hour
	defaultChatHold       = 3 * time.Minute
	defaultChatWait       = 30 * time.Second
)

// The old typed searches kept as silent aliases.
const (
	cmdBooks   = "/b"
	cmdAuthors = "/a"
)

// ViewSearch is a typed search: query and language.
const ViewSearch int16 = 1

// Bot is one served bot as an update finds it.
type Bot struct {
	Client  *bot.Bot
	Ref     int64
	TGBotID int64
	// Username is the bot's @name, without the @; "" when not known.
	Username string
	// Active is the bot's row status being active.
	Active bool
	NavKey []byte
	// Binding is the bot's active binding: whose library it serves and who
	// owns it in Telegram.
	Binding *models.TelegramBinding
}

// Bots finds a served bot by its row id, for work that does not start from
// an update: delivery jobs ending.
type Bots interface {
	V2Bot(ctx context.Context, botRef int64) (*Bot, error)
	// CheckInline asks Telegram whether the bot answers inline queries, and
	// keeps the answer.
	CheckInline(ctx context.Context, botRef int64) (bool, error)
}

// Config sets the router up.
type Config struct {
	Search   services.PublicSearch
	Delivery *delivery.Service
	SiteURL  string
	// Donate is how the library may be supported; none hides the button.
	Donate []config.DonateMethod
	// AuthorLines names the authors of listed books from the author layer;
	// nil names the catalog authors.
	AuthorLines *services.AuthorLines
	// Kindle sends books to Kindle; nil is kindlesend.Default().
	Kindle *kindlesend.Sender
	// MediaBaseURL is where Telegram can fetch covers from, for inline
	// results; "" means none (plan-ui.md §3.4).
	MediaBaseURL string
	// PostersPath is where cover files live; "" means no covers.
	PostersPath func() string
	// SearchTimeout bounds each search branch; zero is 3 s.
	SearchTimeout time.Duration
	// TypingAfter is how long a search may take before «typing» shows; zero
	// is 300 ms.
	TypingAfter time.Duration
	// PinCheckEvery is how often «Домой» may check the pin; zero is 15 min.
	PinCheckEvery time.Duration
	// RichRetryAfter is how long a bot whose rich message Telegram refused is
	// kept plain; zero is 7 days.
	RichRetryAfter time.Duration
	// ChatHold bounds how long one transition holds its chat (a holder that
	// died is outlived) and outlasts a Telegram call; zero is 3 min. ChatWait is how long an update
	// waits for its chat before it is handed back; zero is 30 s.
	ChatHold, ChatWait time.Duration
}

// Router handles the updates of v2 bots.
type Router struct {
	cfg    Config
	bots   Bots
	kindle *kindlesend.Sender
	// background counts the work an update leaves running: Kindle sends.
	background sync.WaitGroup
}

// Wait waits for the work updates left running.
func (r *Router) Wait() { r.background.Wait() }

// New makes a router.
func New(cfg *Config, bots Bots) *Router {
	c := *cfg
	if c.SearchTimeout == 0 {
		c.SearchTimeout = defaultSearchTimeout
	}
	if c.TypingAfter == 0 {
		c.TypingAfter = defaultTypingAfter
	}
	if c.PinCheckEvery == 0 {
		c.PinCheckEvery = defaultPinCheckEvery
	}
	if c.RichRetryAfter == 0 {
		c.RichRetryAfter = defaultRichRetryAfter
	}
	if c.ChatHold == 0 {
		c.ChatHold = defaultChatHold
	}
	if c.ChatWait == 0 {
		c.ChatWait = defaultChatWait
	}
	if c.PostersPath == nil {
		c.PostersPath = func() string { return "" }
	}
	kindle := c.Kindle
	if kindle == nil {
		kindle = kindlesend.Default()
	}
	return &Router{cfg: c, bots: bots, kindle: kindle}
}

// Handle takes one update. An error means nothing was done: the chat stayed
// busy.
//
// The whole update — panel reads, moves, renders and the Telegram calls —
// runs holding its chat (plan-ui.md §5.1), so that two edits of one panel
// never reach Telegram in the opposite order of their revisions, whichever
// pod sends them. It reports false for a bot no longer served by the v2
// router once the chat is held — switched to maintenance meanwhile.
func (r *Router) Handle(ctx context.Context, b *Bot, update *tgbot.Update) (bool, error) {
	if chatID, ok := chatOf(update); ok {
		asked := time.Now()
		unlock, err := database.LockTelegramChat(ctx, b.Ref, chatID, r.cfg.ChatHold, r.cfg.ChatWait)
		if err != nil {
			return false, fmt.Errorf("holding chat %d: %w", chatID, err)
		}
		defer unlock()
		ctx = context.WithValue(ctx, chatWaitKey{}, time.Since(asked))
		// What b says was read before the wait: the bot may have been
		// relinked — a new owner, a new generation — or switched back
		// meanwhile. Only what holds now authorizes the update.
		fresh, err := r.bots.V2Bot(ctx, b.Ref)
		if err != nil {
			return false, fmt.Errorf("reading telegram bot %d again: %w", b.Ref, err)
		}
		if fresh == nil {
			return false, nil
		}
		b = fresh
		// Before the chat is let go: the repaints owed in it.
		defer r.runRepaints(ctx, b, chatID)
	}
	switch {
	case update.InlineQuery != nil:
		r.inline(ctx, b, update.InlineQuery)
	case update.CallbackQuery != nil:
		return r.callback(ctx, b, update.CallbackQuery), nil
	case update.Message != nil:
		r.message(ctx, b, update.Message)
	}
	return true, nil
}

// chatWaitKey keys how long an update waited for its chat.
type chatWaitKey struct{}

// heldFor is how long the update in ctx waited for its chat.
func heldFor(ctx context.Context) time.Duration {
	d, _ := ctx.Value(chatWaitKey{}).(time.Duration)
	return d
}

// chatOf is the chat an update happens in.
func chatOf(update *tgbot.Update) (int64, bool) {
	switch {
	case update.Message != nil:
		return update.Message.Chat.ID, true
	case update.CallbackQuery != nil && update.CallbackQuery.Message.Message != nil:
		return update.CallbackQuery.Message.Message.Chat.ID, true
	}
	return 0, false
}

// message handles what the owner wrote.
func (r *Router) message(ctx context.Context, b *Bot, msg *tgbot.Message) {
	if msg.Chat.Type != tgbot.ChatTypePrivate || msg.From == nil || b.Binding == nil || msg.From.ID != b.Binding.TGUserID {
		return
	}
	r.setupChat(ctx, b, msg.Chat.ID)
	if msg.ViaBot != nil {
		r.viaBot(ctx, b, msg)
		return
	}
	if msg.PinnedMessage != nil {
		// The reader pinned something of their own: theirs to keep.
		return
	}
	text := msg.Text
	if text == "" {
		r.notice(ctx, b, msg.Chat.ID, screens.CopyNotText, true)
		return
	}
	if msg.ReplyToMessage != nil && r.reply(ctx, b, msg) {
		return
	}
	if strings.HasPrefix(text, "/") {
		r.command(ctx, b, msg.Chat.ID, text)
		return
	}
	query := strings.Join(strings.Fields(text), " ")
	switch {
	case query == "":
		r.notice(ctx, b, msg.Chat.ID, screens.CopyEmptyQuery, false)
	case utf8.RuneCountInString(query) > screens.MaxQuery:
		r.notice(ctx, b, msg.Chat.ID, screens.CopyTooLong, false)
	default:
		r.search(ctx, b, msg.Chat.ID, query, 0)
	}
}

// command handles typed /…: /start, and the old /b and /a kept as silent
// aliases of a search (decisions-plan-ui.md Q3). Every other command gets a
// hint: the bot has none.
func (r *Router) command(ctx context.Context, b *Bot, chatID int64, text string) {
	cmd, rest, _ := strings.Cut(text, " ")
	if at := strings.IndexByte(cmd, '@'); at > 0 {
		cmd = cmd[:at]
	}
	query := strings.Join(strings.Fields(rest), " ")
	switch {
	case cmd == "/start":
		r.start(ctx, b, chatID, false)
	case (cmd == cmdBooks || cmd == cmdAuthors) && query != "" && utf8.RuneCountInString(query) <= screens.MaxQuery:
		tab := uint8(0)
		if cmd == cmdAuthors {
			tab = 1
		}
		r.search(ctx, b, chatID, query, tab)
	default:
		r.notice(ctx, b, chatID, screens.CopyNoCommands, true)
	}
}

// Linked greets a chat just bound to the library: its home panel.
func (r *Router) Linked(ctx context.Context, b *Bot, chatID int64) {
	r.setupChat(ctx, b, chatID)
	r.start(ctx, b, chatID, true)
}

// notice sends a one-off message; with home it carries «Домой».
func (r *Router) notice(ctx context.Context, b *Bot, chatID int64, text string, home bool) {
	if !home {
		r.sendPlainText(ctx, b, chatID, text)
		return
	}
	node := &models.TelegramNode{UserID: b.Binding.UserID, Screen: int16(ScrNotice)}
	if err := upsertNode(ctx, node); err != nil {
		logging.Warnf("Telegram bot %d: recording a notice: %v", b.Ref, err)
		r.sendPlainText(ctx, b, chatID, text)
		return
	}
	r.sendPanel(ctx, b, chatID, node, func(k *keys) screens.Screen {
		return screens.Notice(text, k.home())
	}, false)
}

// creditAuthors gives books their author lines, when the library names
// authors from the author layer.
func (r *Router) creditAuthors(ctx context.Context, books []models.Book) {
	if r.cfg.AuthorLines == nil || len(books) == 0 {
		return
	}
	lines := r.cfg.AuthorLines.Page(ctx, books)
	for i := range books {
		if i < len(lines) {
			books[i].AuthorLine = lines[i]
		}
	}
}

// creditBook is creditAuthors for one book.
func (r *Router) creditBook(ctx context.Context, book *models.Book) {
	books := []models.Book{*book}
	r.creditAuthors(ctx, books)
	book.AuthorLine = books[0].AuthorLine
}
