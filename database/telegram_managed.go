package database

import (
	"context"
	"errors"
	"time"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
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
		if err := closeOpenConnections(ctx, tx, userID); err != nil {
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
		return closeOpenConnections(ctx, tx, userID)
	})
}

// closeOpenConnections cancels the account's connections in progress. The
// links already given for the bots they found stop working: a canceled
// candidate is never bound by a link from an older window.
func closeOpenConnections(ctx context.Context, tx *pg.Tx, userID int64) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE telegram_link_token SET expires_at = least(expires_at, now())
		WHERE user_id = ?0 AND consumed_at IS NULL AND bot_ref IN (
			SELECT bot_ref FROM telegram_pending_connection
			WHERE user_id = ?0 AND state = ?1 AND bot_ref IS NOT NULL)`,
		userID, models.PendingConnectionFound); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE telegram_pending_connection SET state = ?, updated_at = now()
		WHERE user_id = ? AND state IN (?, ?)`,
		models.PendingConnectionCanceled, userID, models.PendingConnectionWaiting, models.PendingConnectionFound)
	return err
}

// ClaimTelegramManagedEvent keeps a managed_bot update once, and claims it
// for owner for lease. It reports whether owner is to handle it: false when
// it was handled already, or another process is handling it now.
func ClaimTelegramManagedEvent(
	ctx context.Context, ev *models.TelegramManagedEvent, owner uuid.UUID, lease time.Duration,
) (bool, error) {
	if _, err := db.ModelContext(ctx, ev).OnConflict("(update_id) DO NOTHING").Insert(); err != nil {
		return false, err
	}
	res, err := db.ExecContext(ctx, `
		UPDATE telegram_managed_event SET claimed_by = ?, claim_expires_at = now() + make_interval(secs => ?)
		WHERE update_id = ? AND processed_at IS NULL AND (claim_expires_at IS NULL OR claim_expires_at < now())`,
		owner, lease.Seconds(), ev.UpdateID)
	if err != nil {
		return false, err
	}
	return res.RowsAffected() == 1, nil
}

// ClaimStaleTelegramManagedEvents takes up to limit events left unhandled:
// claimed by a process that stopped before it was done, or never claimed
// for longer than lease.
func ClaimStaleTelegramManagedEvents(
	ctx context.Context, owner uuid.UUID, lease time.Duration, limit int,
) ([]models.TelegramManagedEvent, error) {
	var events []models.TelegramManagedEvent
	_, err := db.QueryContext(ctx, &events, `
		WITH stale AS (
			SELECT update_id FROM telegram_managed_event
			WHERE processed_at IS NULL
			  AND coalesce(claim_expires_at, received_at + make_interval(secs => ?0)) < now()
			ORDER BY update_id LIMIT ?1
			FOR UPDATE SKIP LOCKED)
		UPDATE telegram_managed_event e SET claimed_by = ?2, claim_expires_at = now() + make_interval(secs => ?0)
		FROM stale WHERE e.update_id = stale.update_id
		RETURNING e.*`, lease.Seconds(), limit, owner)
	return events, err
}

// ReleaseTelegramManagedEvent gives up owner's claim of an event it could
// not handle: Telegram's next delivery of it is handled at once.
func ReleaseTelegramManagedEvent(ctx context.Context, updateID int64, owner uuid.UUID) error {
	_, err := db.ExecContext(ctx, `
		UPDATE telegram_managed_event SET claimed_by = NULL, claim_expires_at = NULL
		WHERE update_id = ? AND claimed_by = ? AND processed_at IS NULL`, updateID, owner)
	return err
}

// MarkTelegramManagedEventProcessed records an event as handled.
func MarkTelegramManagedEventProcessed(ctx context.Context, updateID int64) error {
	_, err := db.ExecContext(ctx, `
		UPDATE telegram_managed_event SET processed_at = now(), claimed_by = NULL, claim_expires_at = NULL
		WHERE update_id = ?`, updateID)
	return err
}

// advanceManagedEvent applies event updateID to the bot's order, inside the
// caller's transaction, which holds the bot row: false when a newer event
// was applied already, and this one must change nothing.
func advanceManagedEvent(ctx context.Context, tx *pg.Tx, botRef, updateID int64) (bool, error) {
	res, err := tx.ExecContext(ctx, `
		INSERT INTO telegram_managed_bot (bot_ref, last_update_id) VALUES (?, ?)
		ON CONFLICT (bot_ref) DO UPDATE SET last_update_id = EXCLUDED.last_update_id
		WHERE telegram_managed_bot.last_update_id < EXCLUDED.last_update_id`, botRef, updateID)
	if err != nil {
		return false, err
	}
	return res.RowsAffected() == 1, nil
}

// ApplyTelegramManagedOwner applies event updateID's creator to a managed
// bot, in event order. applied is false for an event older than one already
// applied: it changes nothing. changed is a new owner: the bot is
// suspended (owner_changed) until its new owner links it.
func ApplyTelegramManagedOwner(ctx context.Context, botRef, updateID, creatorTG int64) (applied, changed bool, err error) {
	err = db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		var txErr error
		applied, changed, txErr = applyOwnerInTx(ctx, tx, botRef, updateID, creatorTG)
		return txErr
	})
	return applied, changed, err
}

// applyOwnerInTx is ApplyTelegramManagedOwner's transaction.
func applyOwnerInTx(ctx context.Context, tx *pg.Tx, botRef, updateID, creatorTG int64) (applied, changed bool, err error) {
	var creator *int64
	if _, err = tx.QueryOneContext(ctx, pg.Scan(&creator),
		`SELECT creator_tg_id FROM telegram_bot WHERE id = ? FOR UPDATE`, botRef); err != nil {
		return false, false, err
	}
	if applied, err = advanceManagedEvent(ctx, tx, botRef, updateID); err != nil || !applied {
		return applied, false, err
	}
	if creator == nil || *creator == creatorTG {
		return true, false, nil
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE telegram_bot SET status = ?, creator_tg_id = ?, updated_at = now() WHERE id = ?`,
		models.TelegramBotOwnerChanged, creatorTG, botRef)
	return true, true, err
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

// RegisterManagedTelegramBot records a bot the manager just made, by event
// updateID: a new row. A row registered meanwhile by another event is
// updated in event order: an older event changes nothing (applied false),
// and a newer one with another creator suspends the bot as owner_changed.
func RegisterManagedTelegramBot(ctx context.Context, bot *models.TelegramBot, updateID int64) (
	result *models.TelegramBot, applied bool, err error) {
	err = db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		var txErr error
		result, applied, txErr = registerInTx(ctx, tx, bot, updateID)
		return txErr
	})
	if err != nil {
		return nil, false, err
	}
	return result, applied, nil
}

// registerInTx is RegisterManagedTelegramBot's transaction.
func registerInTx(ctx context.Context, tx *pg.Tx, bot *models.TelegramBot, updateID int64) (
	*models.TelegramBot, bool, error) {
	existing := new(models.TelegramBot)
	err := tx.ModelContext(ctx, existing).Where("tg_bot_id = ?", bot.TGBotID).For("UPDATE").Select()
	if errors.Is(err, pg.ErrNoRows) {
		if _, err = tx.ModelContext(ctx, bot).Returning("*").Insert(); err != nil {
			return nil, false, err
		}
		fresh, advErr := advanceManagedEvent(ctx, tx, bot.ID, updateID)
		return bot, fresh, advErr
	}
	if err != nil {
		return nil, false, err
	}
	applied, err := advanceManagedEvent(ctx, tx, existing.ID, updateID)
	if err != nil || !applied {
		return existing, applied, err
	}
	if existing.CreatorTGID != nil && bot.CreatorTGID != nil && *existing.CreatorTGID != *bot.CreatorTGID {
		existing.Status = models.TelegramBotOwnerChanged
	}
	existing.Kind = models.TelegramBotManaged
	existing.Username = bot.Username
	existing.DisplayName = bot.DisplayName
	existing.TokenEnc = bot.TokenEnc
	existing.TokenSHA256 = bot.TokenSHA256
	existing.KeyID = bot.KeyID
	existing.CreatorTGID = bot.CreatorTGID
	existing.UpdatedAt = time.Now()
	_, err = tx.ModelContext(ctx, existing).
		Column("kind", "username", "display_name", "token_enc", "token_sha256", "key_id", "creator_tg_id",
			"status", "updated_at").
		WherePK().Update()
	return existing, true, err
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

// ReplaceTelegramBotToken stores a bot's new token, after the manager issued
// one at its owner's request, and makes the bot active again.
func ReplaceTelegramBotToken(ctx context.Context, botRef int64, keyID int16, tokenEnc, tokenSHA []byte) error {
	_, err := db.ExecContext(ctx, `
		UPDATE telegram_bot SET key_id = ?, token_enc = ?, token_sha256 = ?, status = ?, updated_at = now()
		WHERE id = ?`, keyID, tokenEnc, tokenSHA, models.TelegramBotActive, botRef)
	return err
}
