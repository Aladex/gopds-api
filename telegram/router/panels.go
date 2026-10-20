package router

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"gopds-api/database"
	"gopds-api/internal/posters"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/telegram/cbv2"
	"gopds-api/telegram/screens"
	"gopds-api/telegram/tgapi"

	"github.com/go-telegram/bot"
	tgbot "github.com/go-telegram/bot/models"
)

// maxCoverBytes is the largest cover uploaded: Telegram takes a photo of up
// to 10 MB by upload.
const maxCoverBytes = 10 << 20

// keys builds the envelopes of one rendering of a panel: they carry the
// binding's generation and the revision the panel will have.
type keys struct {
	b    *Bot
	rev  uint16
	rich bool
}

func (k *keys) env(e cbv2.Envelope) *cbv2.Envelope {
	e.Generation = uint16(k.b.Binding.Generation) // #nosec G115 -- generations are 1..65535 (migration 43)
	e.Revision = k.rev
	return &e
}

func (k *keys) home() *cbv2.Envelope { return k.env(cbv2.Envelope{Op: cbv2.OpHome}) }

// render paints a screen for the revision and mode in k.
type render func(k *keys) screens.Screen

// richFor reports whether the account's new panels are rich: its setting,
// unless Telegram recently refused this bot a rich message.
func (r *Router) richFor(ctx context.Context, b *Bot) bool {
	ui, err := database.GetTelegramUserUI(ctx, b.Binding.UserID)
	if err != nil || ui.CardStyle != models.TelegramPanelRich {
		return false
	}
	bui, err := database.GetTelegramBotUI(ctx, b.Ref)
	if err != nil {
		return false
	}
	return bui.RichRejectedAt == nil || time.Since(*bui.RichRejectedAt) > r.cfg.RichRetryAfter
}

// sendPanel sends node as a new panel and records it.
func (r *Router) sendPanel(
	ctx context.Context, b *Bot, chatID int64, node *models.TelegramNode, paint render, home bool,
) *models.TelegramPanel {
	// The home panel is plain in both modes (plan-ui.md §1.1): there is
	// nothing on it for a rich message to add.
	rich := !home && r.richFor(ctx, b)
	screen := paint(&keys{b: b, rev: 1, rich: rich})
	msg, mode, err := r.send(ctx, b, chatID, &screen, rich)
	if err != nil {
		if !errors.Is(err, database.ErrInboxLeaseLost) {
			logging.Warnf("Telegram bot %d: sending a panel: %v", b.Ref, tgapi.RedactURL(err))
		}
		return nil
	}
	if mode == models.TelegramPanelPlain && rich {
		// Sent plain after a refusal: the buttons it carries were painted
		// for the same revision, and stay valid.
		logging.Infof("Telegram bot %d: panel sent plain after a rich refusal", b.Ref)
	}
	panel := &models.TelegramPanel{
		BotRef: b.Ref, ChatID: chatID, MessageID: int64(msg.ID), UserID: b.Binding.UserID, BindingID: b.Binding.ID,
		NodeID: node.ID, Revision: 1, Mode: mode, IsHome: home,
	}
	if err := database.InsertTelegramPanel(ctx, panel); err != nil {
		if !errors.Is(err, database.ErrInboxLeaseLost) {
			logging.Warnf("Telegram bot %d: recording a panel: %v", b.Ref, err)
		}
		return nil
	}
	return panel
}

// movePanel points panel at node, if its revision is still expected, and
// edits the message. It reports false when another press moved the panel
// first.
func (r *Router) movePanel(
	ctx context.Context, b *Bot, panel *models.TelegramPanel, expected int32, node *models.TelegramNode, paint render,
) bool {
	rev, err := database.MoveTelegramPanel(ctx, panel, expected, node.ID)
	if err != nil {
		if !errors.Is(err, database.ErrPanelMoved) && !errors.Is(err, database.ErrInboxLeaseLost) {
			logging.Warnf("Telegram bot %d: moving a panel: %v", b.Ref, err)
		}
		return false
	}
	panel.Revision, panel.NodeID = rev, node.ID
	rich := panel.Mode == models.TelegramPanelRich
	if rich && !panel.IsHome && !r.richFor(ctx, b) {
		// The reader chose plain cards (or Telegram refuses rich ones): a
		// message keeps its kind, so the screen goes out as a new plain
		// panel.
		r.replacePanel(ctx, b, panel, node, paint)
		return true
	}
	screen := paint(&keys{b: b, rev: uint16(rev), rich: rich}) // #nosec G115 -- revisions are 1..65534
	err = r.edit(ctx, b, panel, &screen)
	var refused richRefusedError
	switch {
	case err == nil:
	case errors.As(err, &refused):
		// A message keeps its kind (plan-ui.md §2.4): the screen goes out
		// as a new plain panel, and the old one loses its buttons.
		r.replacePanel(ctx, b, panel, node, paint)
	default:
		logging.Warnf("Telegram bot %d: editing a panel: %v", b.Ref, tgapi.RedactURL(err))
	}
	return true
}

// replacePanel sends node as a new panel in place of one that cannot show
// it, takes the old one's buttons away, and hands it the old one's
// outstanding work: a file on its way repaints the new card, not the old.
func (r *Router) replacePanel(ctx context.Context, b *Bot, old *models.TelegramPanel, node *models.TelegramNode, paint render) {
	r.stripKeyboard(ctx, b, old)
	replacement := r.sendPanel(ctx, b, old.ChatID, node, paint, old.IsHome)
	if replacement == nil {
		return
	}
	if err := database.RetireTelegramPanel(ctx, old, replacement); err != nil && !errors.Is(err, database.ErrInboxLeaseLost) {
		logging.Warnf("Telegram bot %d: handing a panel's work to its replacement: %v", b.Ref, err)
	}
}

// richRefusedError is Telegram refusing a rich message for a reason other
// than its photo.
type richRefusedError struct{ err error }

func (e richRefusedError) Error() string { return e.err.Error() }

// send sends a screen, rich or plain, and reports the mode it went out in.
// A screen refused rich goes out as its plain self, with its plain keyboard.
func (r *Router) send(ctx context.Context, b *Bot, chatID int64, screen *screens.Screen, rich bool) (*tgbot.Message, string, error) {
	if rich {
		shown := screen.In(true)
		markup, err := r.markup(b, chatID, shown.Keys)
		if err != nil {
			return nil, "", err
		}
		msg, richErr := r.withCoverRetry(ctx, b, shown, func(s *screens.Screen) (*tgbot.Message, error) {
			content, err := s.Doc.Rich(r.encoder(b, chatID))
			if err != nil {
				return nil, err
			}
			return b.Client.SendRichMessage(ctx, &bot.SendRichMessageParams{ChatID: chatID, RichMessage: content, ReplyMarkup: markup})
		})
		if richErr == nil {
			return msg, models.TelegramPanelRich, nil
		}
		var refused richRefusedError
		if !errors.As(richErr, &refused) {
			return nil, "", richErr
		}
		r.richRefused(ctx, b, richErr)
	}
	markup, err := r.markup(b, chatID, screen.Keys)
	if err != nil {
		return nil, "", err
	}
	msg, err := b.Client.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID, Text: screen.Doc.Plain(), ParseMode: tgbot.ParseModeHTML,
		LinkPreviewOptions: noPreview(), ReplyMarkup: markup,
	})
	return msg, models.TelegramPanelPlain, err
}

// edit replaces a panel's content with screen, in the panel's mode.
func (r *Router) edit(ctx context.Context, b *Bot, panel *models.TelegramPanel, screen *screens.Screen) error {
	rich := panel.Mode == models.TelegramPanelRich
	screen = screen.In(rich)
	markup, err := r.markup(b, panel.ChatID, screen.Keys)
	if err != nil {
		return err
	}
	if rich {
		_, err = r.withCoverRetry(ctx, b, screen, func(s *screens.Screen) (*tgbot.Message, error) {
			content, encErr := s.Doc.Rich(r.encoder(b, panel.ChatID))
			if encErr != nil {
				return nil, encErr
			}
			return b.Client.EditMessageText(ctx, &bot.EditMessageTextParams{
				ChatID: panel.ChatID, MessageID: int(panel.MessageID), RichMessage: &content, ReplyMarkup: markup,
			})
		})
		var refused richRefusedError
		if errors.As(err, &refused) {
			r.richRefused(ctx, b, err)
		}
	} else {
		_, err = b.Client.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID: panel.ChatID, MessageID: int(panel.MessageID), Text: screen.Doc.Plain(),
			ParseMode: tgbot.ParseModeHTML, LinkPreviewOptions: noPreview(), ReplyMarkup: markup,
		})
	}
	if class, _ := tgapi.Classify(err); class == tgapi.ClassNotModified {
		return nil
	}
	return err
}

// withCoverRetry makes a rich call, and on a refused cover tries again: an
// unknown file_id with an upload, a refused upload without the cover. A
// refusal that is not about the cover comes back as richRefusedError. An
// uploaded cover's file_id is remembered.
func (r *Router) withCoverRetry(
	ctx context.Context, b *Bot, screen *screens.Screen, call func(*screens.Screen) (*tgbot.Message, error),
) (*tgbot.Message, error) {
	current := *screen
	for attempt := 0; attempt < 3; attempt++ {
		msg, err := call(&current)
		if err == nil {
			if cover, ok := current.Cover(); ok && cover.FileID == "" {
				r.rememberCover(ctx, b, cover.BookID, msg)
			}
			return msg, nil
		}
		class, _ := tgapi.Classify(err)
		if class != tgapi.ClassBadRequest {
			return nil, err
		}
		cover, hasCover := current.Cover()
		switch {
		case hasCover && isPhotoError(err) && cover.FileID != "":
			_ = database.DropTelegramCoverFile(ctx, b.Ref, cover.BookID)
			if len(cover.Data) == 0 {
				current = current.WithoutPhoto()
			} else {
				current = current.WithCover(screens.Photo{BookID: cover.BookID, Data: cover.Data})
			}
		case hasCover && isPhotoError(err):
			logging.Warnf("Telegram bot %d: the cover of book %d was refused: %v", b.Ref, cover.BookID, tgapi.RedactURL(err))
			current = current.WithoutPhoto()
		default:
			return nil, richRefusedError{err}
		}
	}
	return nil, errors.New("router: cover retries exhausted")
}

// isPhotoError reports a refusal about the cover rather than the message.
func isPhotoError(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, word := range []string{"file identifier", "file_id", "photo", "image", "media", "wrong file"} {
		if strings.Contains(msg, word) {
			return true
		}
	}
	return false
}

// richRefused keeps the bot plain for a while (plan-ui.md §2.5).
func (r *Router) richRefused(ctx context.Context, b *Bot, err error) {
	logging.Warnf("Telegram bot %d: a rich message was refused, going plain: %v", b.Ref, tgapi.RedactURL(err))
	if markErr := database.MarkTelegramRichRejected(ctx, b.Ref); markErr != nil {
		logging.Warnf("Telegram bot %d: recording the rich refusal: %v", b.Ref, markErr)
	}
}

// rememberCover keeps the file_id of a cover just uploaded.
func (r *Router) rememberCover(ctx context.Context, b *Bot, bookID int64, msg *tgbot.Message) {
	if msg == nil || msg.RichMessage == nil {
		return
	}
	for i := range msg.RichMessage.Blocks {
		photo := msg.RichMessage.Blocks[i].RichBlockPhoto
		if photo == nil || len(photo.Photo) == 0 {
			continue
		}
		largest := photo.Photo[len(photo.Photo)-1]
		if err := database.PutTelegramCoverFile(ctx, b.Ref, bookID, largest.FileID, largest.FileUniqueID); err != nil {
			logging.Warnf("Telegram bot %d: remembering the cover of book %d: %v", b.Ref, bookID, err)
		}
		return
	}
}

// cover is a book's cover for a rich card: the remembered file_id with the
// bytes kept for a retry, or the bytes alone; nil without a cover file.
func (r *Router) cover(ctx context.Context, b *Bot, book *models.Book) *screens.Photo {
	if !book.Cover {
		return nil
	}
	base := r.cfg.PostersPath()
	if base == "" {
		return nil
	}
	path := posters.FilePath(base, book.Path, book.FileName)
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 || info.Size() > maxCoverBytes {
		return nil
	}
	data, err := os.ReadFile(path) // #nosec G304 -- built from the posters directory and the book's own path
	if err != nil {
		return nil
	}
	fileID, err := database.GetTelegramCoverFile(ctx, b.Ref, book.ID)
	if err != nil {
		fileID = ""
	}
	return &screens.Photo{BookID: book.ID, FileID: fileID, Data: data}
}

// encoder encodes the envelopes of buttons shown in chatID: bound to the
// bot, its owner and the chat, signed with the bot's key.
func (r *Router) encoder(b *Bot, chatID int64) screens.Encoder {
	bind := cbv2.BindCtx{TGBotID: b.TGBotID, OwnerTGID: b.Binding.TGUserID, ChatID: chatID}
	return func(e *cbv2.Envelope) (string, error) { return cbv2.Encode(e, b.NavKey, bind) }
}

// markup encodes a screen's buttons.
func (r *Router) markup(b *Bot, chatID int64, rows [][]screens.Button) (*tgbot.InlineKeyboardMarkup, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	encode := r.encoder(b, chatID)
	out := &tgbot.InlineKeyboardMarkup{InlineKeyboard: make([][]tgbot.InlineKeyboardButton, 0, len(rows))}
	for _, row := range rows {
		line := make([]tgbot.InlineKeyboardButton, 0, len(row))
		for _, button := range row {
			if button.Disabled {
				line = append(line, tgbot.InlineKeyboardButton{Text: button.Text, Disabled: &tgbot.DisabledButton{}})
				continue
			}
			btn := tgbot.InlineKeyboardButton{Text: button.Text, Style: button.Style, URL: button.URL}
			if button.CopyText != "" {
				btn.CopyText = &tgbot.CopyTextButton{Text: button.CopyText}
			}
			if button.SwitchInline {
				empty := ""
				btn.SwitchInlineQueryCurrentChat = &empty
			}
			if button.Env != nil {
				data, err := encode(button.Env)
				if err != nil {
					return nil, err
				}
				btn.CallbackData = data
			}
			line = append(line, btn)
		}
		out.InlineKeyboard = append(out.InlineKeyboard, line)
	}
	return out, nil
}

// stripKeyboard takes the buttons off a panel that a new one replaces.
func (r *Router) stripKeyboard(ctx context.Context, b *Bot, panel *models.TelegramPanel) {
	_, _ = b.Client.EditMessageReplyMarkup(ctx, &bot.EditMessageReplyMarkupParams{
		ChatID: panel.ChatID, MessageID: int(panel.MessageID),
		ReplyMarkup: &tgbot.InlineKeyboardMarkup{InlineKeyboard: [][]tgbot.InlineKeyboardButton{}},
	})
}

// sendPlainText sends a message that is not a panel.
func (r *Router) sendPlainText(ctx context.Context, b *Bot, chatID int64, text string) {
	if _, err := b.Client.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: text, LinkPreviewOptions: noPreview()}); err != nil {
		logging.Warnf("Telegram bot %d: sending a message: %v", b.Ref, tgapi.RedactURL(err))
	}
}

func noPreview() *tgbot.LinkPreviewOptions {
	off := true
	return &tgbot.LinkPreviewOptions{IsDisabled: &off}
}

func upsertNode(ctx context.Context, n *models.TelegramNode) error {
	return database.UpsertTelegramNode(ctx, n)
}
