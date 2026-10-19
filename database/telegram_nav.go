package database

import (
	"context"
	"errors"
	"time"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// The v2 bot's navigation state: views, nodes and panels (plan.md §2.4,
// plan-ui.md §5.1). Writes made while handling an update go through
// guardInboxLease, so a process that lost the update writes nothing.

// ErrPanelMoved means a panel's revision was not the expected one: another
// press got there first.
var ErrPanelMoved = errors.New("telegram panel moved on")

// maxPanelRevision is the last revision a panel may reach; 65535 is the
// documents' revision in buttons.
const maxPanelRevision = 65534

// CreateTelegramView records a view and returns it with its id.
func CreateTelegramView(ctx context.Context, v *models.TelegramView) error {
	_, err := db.ModelContext(ctx, v).Returning("*").Insert()
	return err
}

// GetTelegramView loads a view of userID; pg.ErrNoRows when it is gone or
// someone else's.
func GetTelegramView(ctx context.Context, userID, id int64) (*models.TelegramView, error) {
	v := new(models.TelegramView)
	err := db.ModelContext(ctx, v).Where("id = ? AND user_id = ?", id, userID).Select()
	if err != nil {
		return nil, err
	}
	_, _ = db.ExecContext(ctx, `UPDATE telegram_view SET last_used_at = now() WHERE id = ?`, id)
	return v, nil
}

// UpsertTelegramNode records a node, or finds the identical one, and returns
// its id.
func UpsertTelegramNode(ctx context.Context, n *models.TelegramNode) error {
	_, err := db.QueryOneContext(ctx, pg.Scan(&n.ID), `
		INSERT INTO telegram_node (user_id, view_id, screen, entity_id, page, parent_id)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id, view_id, screen, entity_id, page, parent_id)
		DO UPDATE SET last_used_at = now()
		RETURNING id`, n.UserID, n.ViewID, n.Screen, n.EntityID, n.Page, n.ParentID)
	return err
}

// GetTelegramNode loads a node of userID; pg.ErrNoRows when it is gone.
func GetTelegramNode(ctx context.Context, userID, id int64) (*models.TelegramNode, error) {
	n := new(models.TelegramNode)
	err := db.ModelContext(ctx, n).Where("id = ? AND user_id = ?", id, userID).Select()
	return n, err
}

// InsertTelegramPanel records a message just sent as a panel. A home panel
// replaces the chat's previous home.
func InsertTelegramPanel(ctx context.Context, p *models.TelegramPanel) error {
	return db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if err := guardInboxLease(ctx, tx); err != nil {
			return err
		}
		if p.IsHome {
			if _, err := tx.ExecContext(ctx, `
				UPDATE telegram_panel SET is_home = false WHERE bot_ref = ? AND chat_id = ? AND is_home`,
				p.BotRef, p.ChatID); err != nil {
				return err
			}
		}
		_, err := tx.ModelContext(ctx, p).Returning("*").Insert()
		return err
	})
}

// GetTelegramPanel loads one panel; pg.ErrNoRows when the message is not one.
func GetTelegramPanel(ctx context.Context, botRef, chatID, messageID int64) (*models.TelegramPanel, error) {
	p := &models.TelegramPanel{BotRef: botRef, ChatID: chatID, MessageID: messageID}
	err := db.ModelContext(ctx, p).WherePK().Select()
	return p, err
}

// MoveTelegramPanel points a panel at nodeID if its revision is still
// expected, and returns the new revision. ErrPanelMoved when another press
// moved it first, or when it has run out of revisions.
func MoveTelegramPanel(ctx context.Context, p *models.TelegramPanel, expected int32, nodeID int64) (int32, error) {
	var rev int32
	err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if err := guardInboxLease(ctx, tx); err != nil {
			return err
		}
		_, err := tx.QueryOneContext(ctx, pg.Scan(&rev), `
			UPDATE telegram_panel SET node_id = ?, revision = revision + 1, updated_at = now()
			WHERE bot_ref = ? AND chat_id = ? AND message_id = ? AND revision = ? AND revision < ?
			RETURNING revision`, nodeID, p.BotRef, p.ChatID, p.MessageID, expected, maxPanelRevision)
		if errors.Is(err, pg.ErrNoRows) {
			return ErrPanelMoved
		}
		return err
	})
	return rev, err
}

// GetTelegramHomePanel loads the chat's home panel; nil when there is none.
func GetTelegramHomePanel(ctx context.Context, botRef, chatID int64) (*models.TelegramPanel, error) {
	p := new(models.TelegramPanel)
	err := db.ModelContext(ctx, p).Where("bot_ref = ? AND chat_id = ? AND is_home", botRef, chatID).Select()
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

// MarkTelegramHomePinned records that the home panel was pinned, and checked,
// now.
func MarkTelegramHomePinned(ctx context.Context, p *models.TelegramPanel) error {
	_, err := db.ExecContext(ctx, `
		UPDATE telegram_panel SET pinned_at = now(), pin_checked_at = now()
		WHERE bot_ref = ? AND chat_id = ? AND message_id = ?`, p.BotRef, p.ChatID, p.MessageID)
	return err
}

// MarkTelegramHomeChecked records a pin check.
func MarkTelegramHomeChecked(ctx context.Context, p *models.TelegramPanel) error {
	_, err := db.ExecContext(ctx, `
		UPDATE telegram_panel SET pin_checked_at = now()
		WHERE bot_ref = ? AND chat_id = ? AND message_id = ?`, p.BotRef, p.ChatID, p.MessageID)
	return err
}

// DropTelegramHome forgets a home panel whose message is gone.
func DropTelegramHome(ctx context.Context, p *models.TelegramPanel) error {
	_, err := db.ExecContext(ctx, `
		UPDATE telegram_panel SET is_home = false, pinned_at = NULL
		WHERE bot_ref = ? AND chat_id = ? AND message_id = ?`, p.BotRef, p.ChatID, p.MessageID)
	return err
}

// defaultTelegramFormat is the format of an account without a setting.
const defaultTelegramFormat = "epub"

// TelegramUserUI is what the v2 bot needs of the account's preferences.
type TelegramUserUI struct {
	DefaultFormat string
	CardStyle     string
	// SearchLang is the search language: the bot setting, else the site
	// profile's books language, else "" for all languages.
	SearchLang string
}

// GetTelegramUserUI reads the account's preferences, with their defaults.
func GetTelegramUserUI(ctx context.Context, userID int64) (TelegramUserUI, error) {
	ui := TelegramUserUI{DefaultFormat: defaultTelegramFormat, CardStyle: models.TelegramPanelRich}
	var format, style, lang *string
	var booksLang string
	_, err := db.QueryOneContext(ctx, pg.Scan(&format, &style, &lang, &booksLang), `
		SELECT s.default_format, s.card_style, s.search_lang, COALESCE(u.books_lang, '')
		FROM auth_user u LEFT JOIN telegram_user_settings s ON s.user_id = u.id
		WHERE u.id = ?`, userID)
	if err != nil {
		return ui, err
	}
	if format != nil {
		ui.DefaultFormat = *format
	}
	if style != nil {
		ui.CardStyle = *style
	}
	switch {
	case lang != nil:
		ui.SearchLang = *lang
	default:
		ui.SearchLang = booksLang
	}
	return ui, nil
}

// TelegramBotUI is a bot's interface facts.
type TelegramBotUI struct {
	CommandsClearedAt *time.Time
	RichRejectedAt    *time.Time
}

// GetTelegramBotUI reads a bot's interface facts; zero when none are known.
func GetTelegramBotUI(ctx context.Context, botRef int64) (TelegramBotUI, error) {
	var ui TelegramBotUI
	_, err := db.QueryContext(ctx, pg.Scan(&ui.CommandsClearedAt, &ui.RichRejectedAt), `
		SELECT commands_cleared_at, rich_rejected_at FROM telegram_bot_ui WHERE bot_ref = ?`, botRef)
	return ui, err
}

// MarkTelegramCommandsCleared records that the bot's command menu is empty.
func MarkTelegramCommandsCleared(ctx context.Context, botRef int64) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO telegram_bot_ui (bot_ref, commands_cleared_at) VALUES (?, now())
		ON CONFLICT (bot_ref) DO UPDATE SET commands_cleared_at = now()`, botRef)
	return err
}

// ResetTelegramCommandsCleared forgets that the bot's menu was cleared: the
// legacy router has just registered its commands again.
func ResetTelegramCommandsCleared(ctx context.Context, botRef int64) error {
	_, err := db.ExecContext(ctx, `UPDATE telegram_bot_ui SET commands_cleared_at = NULL WHERE bot_ref = ?`, botRef)
	return err
}

// MarkTelegramRichRejected records that Telegram refused the bot a rich
// message.
func MarkTelegramRichRejected(ctx context.Context, botRef int64) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO telegram_bot_ui (bot_ref, rich_rejected_at) VALUES (?, now())
		ON CONFLICT (bot_ref) DO UPDATE SET rich_rejected_at = now()`, botRef)
	return err
}

// GetTelegramCoverFile is the file_id a bot has for a book's cover, "" when
// none.
func GetTelegramCoverFile(ctx context.Context, botRef, bookID int64) (string, error) {
	var fileID string
	_, err := db.QueryContext(ctx, pg.Scan(&fileID), `
		SELECT file_id FROM telegram_cover_file WHERE bot_ref = ? AND book_id = ?`, botRef, bookID)
	return fileID, err
}

// PutTelegramCoverFile remembers the file_id of an uploaded cover.
func PutTelegramCoverFile(ctx context.Context, botRef, bookID int64, fileID, uniqueID string) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO telegram_cover_file (bot_ref, book_id, file_id, file_unique_id) VALUES (?, ?, ?, ?)
		ON CONFLICT (bot_ref, book_id) DO UPDATE SET file_id = EXCLUDED.file_id,
			file_unique_id = EXCLUDED.file_unique_id, created_at = now()`, botRef, bookID, fileID, uniqueID)
	return err
}

// DropTelegramCoverFile forgets a file_id Telegram refused.
func DropTelegramCoverFile(ctx context.Context, botRef, bookID int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM telegram_cover_file WHERE bot_ref = ? AND book_id = ?`, botRef, bookID)
	return err
}

// GetBookCard loads a book with what its card shows: authors, series with
// their numbers, genres.
func GetBookCard(ctx context.Context, id int64) (*models.Book, error) {
	book := &models.Book{ID: id}
	err := db.ModelContext(ctx, book).Relation("Authors").Relation("Series").Relation("Genres").WherePK().Select()
	if err != nil {
		return nil, err
	}
	books := []models.Book{*book}
	populateSeriesNumbersWithDB(db, books)
	return &books[0], nil
}

// IsFavoriteBook reports whether the account keeps the book in favorites.
func IsFavoriteBook(ctx context.Context, userID, bookID int64) (bool, error) {
	n, err := db.ModelContext(ctx, (*models.UserToBook)(nil)).Where("user_id = ? AND book_id = ?", userID, bookID).Count()
	return n > 0, err
}

// SetFavoriteBook puts the book in or out of the account's favorites — the
// state asked for, so a repeated press changes nothing.
func SetFavoriteBook(ctx context.Context, userID, bookID int64, want bool) error {
	return db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if err := guardInboxLease(ctx, tx); err != nil {
			return err
		}
		if want {
			_, err := tx.ExecContext(ctx, `
				INSERT INTO favorite_books (user_id, book_id)
				SELECT ?, ? WHERE NOT EXISTS (SELECT 1 FROM favorite_books WHERE user_id = ? AND book_id = ?)`,
				userID, bookID, userID, bookID)
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM favorite_books WHERE user_id = ? AND book_id = ?`, userID, bookID)
		return err
	})
}

// PreparingFormats are the formats of a book whose delivery jobs to a
// binding have not ended.
func PreparingFormats(ctx context.Context, bindingID, bookID int64) ([]string, error) {
	var formats []string
	_, err := db.QueryContext(ctx, &formats, `
		SELECT format FROM telegram_delivery WHERE binding_id = ? AND book_id = ? AND finished_at IS NULL`,
		bindingID, bookID)
	return formats, err
}

// PruneTelegramNavigation deletes views and nodes unused for age.
func PruneTelegramNavigation(ctx context.Context, age time.Duration) error {
	if _, err := db.ExecContext(ctx, `
		DELETE FROM telegram_node WHERE last_used_at < now() - make_interval(secs => ?)`, age.Seconds()); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `
		DELETE FROM telegram_view WHERE last_used_at < now() - make_interval(secs => ?)`, age.Seconds())
	return err
}

// GetTelegramAuthor loads an author by id.
func GetTelegramAuthor(ctx context.Context, id int64) (*models.Author, error) {
	a := new(models.Author)
	err := db.ModelContext(ctx, a).Where("id = ?", id).Select()
	return a, err
}

// GetTelegramSeries loads a series by id.
func GetTelegramSeries(ctx context.Context, id int64) (*models.Series, error) {
	s := new(models.Series)
	err := db.ModelContext(ctx, s).Where("id = ?", id).Select()
	return s, err
}

// The account's bot settings the v2 bot writes; a row is made on first use.
const (
	setDefaultFormat = `INSERT INTO telegram_user_settings (user_id, default_format) VALUES (?, ?)
		ON CONFLICT (user_id) DO UPDATE SET default_format = EXCLUDED.default_format, updated_at = now()`
	setSearchLang = `INSERT INTO telegram_user_settings (user_id, search_lang) VALUES (?, ?)
		ON CONFLICT (user_id) DO UPDATE SET search_lang = EXCLUDED.search_lang, updated_at = now()`
	setCardStyle = `INSERT INTO telegram_user_settings (user_id, card_style) VALUES (?, ?)
		ON CONFLICT (user_id) DO UPDATE SET card_style = EXCLUDED.card_style, updated_at = now()`
)

func setTelegramSetting(ctx context.Context, query string, userID int64, value string) error {
	return db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if err := guardInboxLease(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, query, userID, value)
		return err
	})
}

// SetTelegramDefaultFormat stores the format books are sent in by default —
// the bot's and the site's.
func SetTelegramDefaultFormat(ctx context.Context, userID int64, format string) error {
	return setTelegramSetting(ctx, setDefaultFormat, userID, format)
}

// SetTelegramSearchLang stores the language of new searches; "" for all.
func SetTelegramSearchLang(ctx context.Context, userID int64, lang string) error {
	return setTelegramSetting(ctx, setSearchLang, userID, lang)
}

// SetTelegramCardStyle stores rich or plain cards.
func SetTelegramCardStyle(ctx context.Context, userID int64, style string) error {
	return setTelegramSetting(ctx, setCardStyle, userID, style)
}

// CreateTelegramPrompt records a question just sent.
func CreateTelegramPrompt(ctx context.Context, p *models.TelegramPrompt) error {
	return db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if err := guardInboxLease(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ModelContext(ctx, p).Insert()
		return err
	})
}

// GetTelegramPrompt loads the question a message is; pg.ErrNoRows when it is
// none.
func GetTelegramPrompt(ctx context.Context, botRef, chatID, messageID int64) (*models.TelegramPrompt, error) {
	p := &models.TelegramPrompt{BotRef: botRef, ChatID: chatID, PromptMessageID: messageID}
	err := db.ModelContext(ctx, p).WherePK().Select()
	return p, err
}

// CloseTelegramPrompt moves an open, unexpired question to state; false when
// it was not open any more.
func CloseTelegramPrompt(ctx context.Context, p *models.TelegramPrompt, state string) (bool, error) {
	var closed bool
	err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if err := guardInboxLease(ctx, tx); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE telegram_prompt SET state = ?
			WHERE bot_ref = ? AND chat_id = ? AND prompt_message_id = ? AND state = 'open' AND expires_at > now()`,
			state, p.BotRef, p.ChatID, p.PromptMessageID)
		if err != nil {
			return err
		}
		closed = res.RowsAffected() == 1
		return nil
	})
	return closed, err
}
