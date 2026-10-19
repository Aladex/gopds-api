package database

import (
	"context"
	"errors"
	"time"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// Managed Bots onboarding (plan.md P1-1, concept §C.3): the reader's waiting
// connection, the manager's events, and the bots it manages. None of this
// binds a bot to an account: that is still only the one-time link, whose
// /start must come from the bot's creator (bindByLink).

// ErrManagedBotNotFound is a bot that is not one of the manager's, or not
// waiting for a link.
var ErrManagedBotNotFound = errors.New("managed bot not found")

// StartTelegramPendingConnection replaces the account's open connection, if
// any, with a new waiting one under label.
func StartTelegramPendingConnection(ctx context.Context, userID int64, label string, expiresAt time.Time) error {
	return db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			UPDATE telegram_pending_connection SET state = ?, updated_at = now()
			WHERE user_id = ? AND state IN (?, ?)`,
			models.PendingConnectionCanceled, userID, models.PendingConnectionWaiting, models.PendingConnectionFound); err != nil {
			return err
		}
		_, err := tx.ModelContext(ctx, &models.TelegramPendingConnection{
			UserID: userID, DiscoveryLabel: label, State: models.PendingConnectionWaiting, ExpiresAt: expiresAt,
		}).Insert()
		return err
	})
}

// OpenTelegramPendingConnection is the account's connection in progress: one
// waiting for its bot and not expired, or one that found its bot; nil when
// there is none.
func OpenTelegramPendingConnection(ctx context.Context, userID int64) (*models.TelegramPendingConnection, error) {
	pc := new(models.TelegramPendingConnection)
	err := db.ModelContext(ctx, pc).
		Where("user_id = ?", userID).
		Where("(state = ? AND expires_at > now()) OR state = ?", models.PendingConnectionWaiting, models.PendingConnectionFound).
		Order("created_at DESC").Limit(1).Select()
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	return pc, err
}

// CancelTelegramPendingConnection ends the account's connection in progress.
// A managed bot it had found stops waiting for this account's link; the bot
// itself stays in Telegram, and in the manager's registry.
func CancelTelegramPendingConnection(ctx context.Context, userID int64) error {
	return db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			UPDATE telegram_bot b SET added_by_user_id = NULL, updated_at = now()
			FROM telegram_pending_connection pc
			WHERE pc.user_id = ? AND pc.state = ? AND pc.bot_ref = b.id AND b.kind = ? AND b.added_by_user_id = ?
			  AND NOT EXISTS (SELECT 1 FROM telegram_binding x WHERE x.bot_ref = b.id AND x.status = 'active')`,
			userID, models.PendingConnectionFound, models.TelegramBotManaged, userID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE telegram_pending_connection SET state = ?, updated_at = now()
			WHERE user_id = ? AND state IN (?, ?)`,
			models.PendingConnectionCanceled, userID, models.PendingConnectionWaiting, models.PendingConnectionFound)
		return err
	})
}

// RecordTelegramManagedEvent keeps a managed_bot update once. It reports
// whether the event still has to be handled: new, or kept earlier and not
// handled yet.
func RecordTelegramManagedEvent(ctx context.Context, ev *models.TelegramManagedEvent) (bool, error) {
	if _, err := db.ModelContext(ctx, ev).OnConflict("(update_id) DO NOTHING").Insert(); err != nil {
		return false, err
	}
	kept := &models.TelegramManagedEvent{UpdateID: ev.UpdateID}
	if err := db.ModelContext(ctx, kept).WherePK().Select(); err != nil {
		return false, err
	}
	return kept.ProcessedAt == nil, nil
}

// MarkTelegramManagedEventProcessed records an event as handled.
func MarkTelegramManagedEventProcessed(ctx context.Context, updateID int64) error {
	_, err := db.ExecContext(ctx, `UPDATE telegram_managed_event SET processed_at = now() WHERE update_id = ?`, updateID)
	return err
}

// GetTelegramBotByTGID finds a bot by its Telegram id; nil when unknown.
func GetTelegramBotByTGID(ctx context.Context, tgBotID int64) (*models.TelegramBot, error) {
	bot := new(models.TelegramBot)
	err := db.ModelContext(ctx, bot).Where("tg_bot_id = ?", tgBotID).Select()
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	return bot, err
}

// GetManagedTelegramBotByUsername finds one of the manager's bots by its
// username, case-insensitively; nil when the manager has none by that name.
func GetManagedTelegramBotByUsername(ctx context.Context, username string) (*models.TelegramBot, error) {
	bot := new(models.TelegramBot)
	err := db.ModelContext(ctx, bot).
		Where("kind = ?", models.TelegramBotManaged).
		Where("lower(username) = lower(?)", username).
		Order("id DESC").Limit(1).Select()
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	return bot, err
}

// RegisterManagedTelegramBot records a bot the manager just made or got the
// token of: a new row, or the known one with its token, name and creator.
func RegisterManagedTelegramBot(ctx context.Context, bot *models.TelegramBot) (*models.TelegramBot, error) {
	result := bot
	err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		existing := new(models.TelegramBot)
		err := tx.ModelContext(ctx, existing).Where("tg_bot_id = ?", bot.TGBotID).For("UPDATE").Select()
		if errors.Is(err, pg.ErrNoRows) {
			_, err = tx.ModelContext(ctx, bot).Returning("*").Insert()
			return err
		}
		if err != nil {
			return err
		}
		existing.Kind = models.TelegramBotManaged
		existing.Username = bot.Username
		existing.DisplayName = bot.DisplayName
		existing.TokenEnc = bot.TokenEnc
		existing.TokenSHA256 = bot.TokenSHA256
		existing.KeyID = bot.KeyID
		existing.CreatorTGID = bot.CreatorTGID
		existing.Status = models.TelegramBotActive
		existing.UpdatedAt = time.Now()
		_, err = tx.ModelContext(ctx, existing).
			Column("kind", "username", "display_name", "token_enc", "token_sha256", "key_id", "creator_tg_id",
				"status", "updated_at").
			WherePK().Update()
		result = existing
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// SetTelegramManagedBot records what the manager confirmed about a bot: its
// access restriction, and whether it is ready.
func SetTelegramManagedBot(ctx context.Context, botRef int64, restricted, ready bool) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO telegram_managed_bot (bot_ref, access_restricted, ready_at, checked_at)
		VALUES (?, ?, CASE WHEN ? THEN now() END, now())
		ON CONFLICT (bot_ref) DO UPDATE SET access_restricted = EXCLUDED.access_restricted,
			ready_at = CASE WHEN ? THEN coalesce(telegram_managed_bot.ready_at, now()) END, checked_at = now()`,
		botRef, restricted, ready, ready)
	return err
}

// GetTelegramManagedBot reads what the manager confirmed about a bot; nil
// when it never looked.
func GetTelegramManagedBot(ctx context.Context, botRef int64) (*models.TelegramManagedBot, error) {
	mb := &models.TelegramManagedBot{BotRef: botRef}
	err := db.ModelContext(ctx, mb).WherePK().Select()
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	return mb, err
}

// FindTelegramPendingConnection gives a bot the reader whose waiting
// connection's label its username carries: the connection has found its bot,
// and the bot waits for that account's link. It reports the account, or 0.
func FindTelegramPendingConnection(ctx context.Context, label string, botRef int64) (int64, error) {
	var userID int64
	err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		pc := new(models.TelegramPendingConnection)
		err := tx.ModelContext(ctx, pc).
			Where("discovery_label = ? AND state = ? AND expires_at > now()", label, models.PendingConnectionWaiting).
			For("UPDATE").Select()
		if errors.Is(err, pg.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		userID = pc.UserID
		return claimInTx(ctx, tx, pc.UserID, botRef, pc)
	})
	return userID, err
}

// ClaimTelegramManagedBot is «Я уже создал бота»: one of the manager's bots,
// not bound to anyone, becomes the bot this account's connection waits on.
// Claiming proves nothing — the link's /start must still come from the bot's
// creator — so a claim by the wrong account only hands it a useless link.
func ClaimTelegramManagedBot(ctx context.Context, userID int64, username string) (*models.TelegramBot, error) {
	bot, err := GetManagedTelegramBotByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	if bot == nil || bot.TokenEnc == nil || bot.Status != models.TelegramBotActive {
		return nil, ErrManagedBotNotFound
	}
	err = db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		return claimNamedInTx(ctx, tx, userID, bot.ID)
	})
	if err != nil {
		return nil, err
	}
	return bot, nil
}

// claimNamedInTx is ClaimTelegramManagedBot's transaction.
func claimNamedInTx(ctx context.Context, tx *pg.Tx, userID, botRef int64) error {
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM telegram_bot WHERE id = ? FOR NO KEY UPDATE`, botRef); err != nil {
		return err
	}
	// Bound to another account, or found by another account's connection:
	// two connections never share a bot.
	var bound int
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&bound), `
		SELECT (SELECT count(*) FROM telegram_binding WHERE bot_ref = ?0 AND status = 'active' AND user_id <> ?1)
		     + (SELECT count(*) FROM telegram_pending_connection WHERE bot_ref = ?0 AND state = ?2 AND user_id <> ?1)`,
		botRef, userID, models.PendingConnectionFound); err != nil {
		return err
	}
	if bound > 0 {
		return ErrTelegramBotTaken
	}
	pc := new(models.TelegramPendingConnection)
	err := tx.ModelContext(ctx, pc).
		Where("user_id = ?", userID).
		Where("(state = ? AND expires_at > now()) OR state = ?", models.PendingConnectionWaiting, models.PendingConnectionFound).
		Order("created_at DESC").Limit(1).For("UPDATE").Select()
	if errors.Is(err, pg.ErrNoRows) {
		return ErrManagedBotNotFound
	}
	if err != nil {
		return err
	}
	return claimInTx(ctx, tx, userID, botRef, pc)
}

// claimInTx makes botRef the bot pc found, and the bot waiting for userID's
// link (GetTelegramUserState's pending bot).
func claimInTx(ctx context.Context, tx *pg.Tx, userID, botRef int64, pc *models.TelegramPendingConnection) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE telegram_pending_connection SET state = ?, bot_ref = ?, updated_at = now() WHERE id = ?`,
		models.PendingConnectionFound, botRef, pc.ID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE telegram_bot SET added_by_user_id = ?, updated_at = now() WHERE id = ?`, userID, botRef)
	return err
}

// SetTelegramBotOwnerChanged records that Telegram moved a managed bot to
// another owner: the bot is suspended until its new owner links it again.
func SetTelegramBotOwnerChanged(ctx context.Context, botRef, creatorTG int64) error {
	_, err := db.ExecContext(ctx, `
		UPDATE telegram_bot SET status = ?, creator_tg_id = ?, updated_at = now() WHERE id = ?`,
		models.TelegramBotOwnerChanged, creatorTG, botRef)
	return err
}

// ReplaceTelegramBotToken stores a bot's new token, after the manager issued
// one at its owner's request, and makes the bot active again.
func ReplaceTelegramBotToken(ctx context.Context, botRef int64, keyID int16, tokenEnc, tokenSHA []byte) error {
	_, err := db.ExecContext(ctx, `
		UPDATE telegram_bot SET key_id = ?, token_enc = ?, token_sha256 = ?, status = ?, updated_at = now()
		WHERE id = ?`, keyID, tokenEnc, tokenSHA, models.TelegramBotActive, botRef)
	return err
}
