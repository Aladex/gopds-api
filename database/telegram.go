package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// ErrTelegramBotTaken means the bot already serves another library account.
var ErrTelegramBotTaken = errors.New("telegram bot is connected to another account")

// maxGeneration is the last generation a binding can carry; the bot's
// callback signatures hold it in 16 bits.
const maxGeneration = 65535

// LegacyTelegramBot is a bot connected the old way, in auth_user.
type LegacyTelegramBot struct {
	UserID      int64
	Token       string
	WebhookUUID string
	TelegramID  *int64
}

// ListLegacyTelegramBots returns every auth_user row with a bot token.
func ListLegacyTelegramBots(ctx context.Context) ([]LegacyTelegramBot, error) {
	var rows []LegacyTelegramBot
	_, err := db.QueryContext(ctx, &rows, `
		SELECT id AS user_id, bot_token AS token, coalesce(webhook_uuid, '') AS webhook_uuid, telegram_id
		FROM auth_user WHERE bot_token IS NOT NULL AND bot_token <> '' ORDER BY id`)
	return rows, err
}

// AdoptLegacyTelegramBot copies one legacy bot into telegram_bot and, when it
// had an owner, gives that owner an active binding. A bot already present is
// left alone, so running it again — or in two processes at once — changes
// nothing. It reports whether this call adopted the bot.
func AdoptLegacyTelegramBot(ctx context.Context, bot *models.TelegramBot, ownerTG *int64) (bool, error) {
	adopted := false
	err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		res, err := tx.ModelContext(ctx, bot).OnConflict("DO NOTHING").Returning("id").Insert()
		if errors.Is(err, pg.ErrNoRows) {
			// Adopted before: the conflict returns no id, and go-pg reports
			// that as no rows.
			return nil
		}
		if err != nil {
			return err
		}
		if res.RowsAffected() == 0 {
			return nil
		}
		adopted = true
		if ownerTG == nil || bot.AddedByUserID == nil {
			return nil
		}
		generation, err := nextGeneration(ctx, tx, bot.ID, nil)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO telegram_binding (user_id, bot_ref, tg_user_id, generation, status)
			VALUES (?, ?, ?, ?, 'active') ON CONFLICT DO NOTHING`,
			*bot.AddedByUserID, bot.ID, *ownerTG, generation)
		return err
	})
	return adopted, err
}

// nextGeneration takes the bot's next binding generation. At the end of the
// 16-bit range it starts again at 1 with a fresh navigation key — wrapping
// with the old key would make long-revoked buttons valid again — and refuses
// when no fresh key was given.
func nextGeneration(ctx context.Context, tx *pg.Tx, botRef int64, freshNavKey []byte) (int32, error) {
	var generation int32
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&generation),
		`SELECT next_generation FROM telegram_bot WHERE id = ? FOR NO KEY UPDATE`, botRef); err != nil {
		return 0, err
	}
	if generation > maxGeneration {
		if freshNavKey == nil {
			return 0, fmt.Errorf("telegram bot %d ran out of generations", botRef)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE telegram_bot SET nav_key_enc = ? WHERE id = ?`,
			freshNavKey, botRef); err != nil {
			return 0, err
		}
		generation = 1
	}
	_, err := tx.ExecContext(ctx, `UPDATE telegram_bot SET next_generation = ?, updated_at = now() WHERE id = ?`,
		generation+1, botRef)
	return generation, err
}

// ListServableTelegramBots returns the bots the service should answer for.
func ListServableTelegramBots(ctx context.Context) ([]models.TelegramBot, error) {
	var bots []models.TelegramBot
	err := db.ModelContext(ctx, &bots).
		Where("status = ?", models.TelegramBotActive).
		Where("token_enc IS NOT NULL").
		Order("id").
		Select()
	return bots, err
}

// GetTelegramBotByWebhookPath finds a bot by its webhook path; pg.ErrNoRows
// when there is none.
func GetTelegramBotByWebhookPath(ctx context.Context, path uuid.UUID) (*models.TelegramBot, error) {
	bot := new(models.TelegramBot)
	err := db.ModelContext(ctx, bot).Where("webhook_path = ?", path).Select()
	if err != nil {
		return nil, err
	}
	return bot, nil
}

// GetTelegramBot loads one bot.
func GetTelegramBot(ctx context.Context, id int64) (*models.TelegramBot, error) {
	bot := &models.TelegramBot{ID: id}
	if err := db.ModelContext(ctx, bot).WherePK().Select(); err != nil {
		return nil, err
	}
	return bot, nil
}

// GetActiveTelegramBinding returns the bot's active binding, or nil.
func GetActiveTelegramBinding(ctx context.Context, botRef int64) (*models.TelegramBinding, error) {
	binding := new(models.TelegramBinding)
	err := db.ModelContext(ctx, binding).
		Where("bot_ref = ?", botRef).
		Where("status = ?", models.TelegramBindingActive).
		Select()
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return binding, nil
}

// GetActiveTelegramBindingOfUser returns the account's active binding, or nil.
func GetActiveTelegramBindingOfUser(ctx context.Context, userID int64) (*models.TelegramBinding, error) {
	binding := new(models.TelegramBinding)
	err := db.ModelContext(ctx, binding).
		Where("user_id = ?", userID).
		Where("status = ?", models.TelegramBindingActive).
		Select()
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return binding, nil
}

// RegisterManualTelegramBot records a bot whose token the user entered. A bot
// already known gets the new token and becomes this user's pending bot,
// keeping its webhook path, secrets and generations; ErrTelegramBotTaken when
// it serves someone else.
func RegisterManualTelegramBot(ctx context.Context, userID int64, bot *models.TelegramBot) (*models.TelegramBot, error) {
	result := bot
	err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		existing := new(models.TelegramBot)
		err := tx.ModelContext(ctx, existing).Where("tg_bot_id = ?", bot.TGBotID).For("UPDATE").Select()
		if errors.Is(err, pg.ErrNoRows) {
			bot.AddedByUserID = &userID
			_, err = tx.ModelContext(ctx, bot).Returning("*").Insert()
			return err
		}
		if err != nil {
			return err
		}
		var otherOwner int
		if _, err = tx.QueryOneContext(ctx, pg.Scan(&otherOwner), `
			SELECT count(*) FROM telegram_binding WHERE bot_ref = ? AND status = 'active' AND user_id <> ?`,
			existing.ID, userID); err != nil {
			return err
		}
		if otherOwner > 0 {
			return ErrTelegramBotTaken
		}
		existing.Username = bot.Username
		existing.DisplayName = bot.DisplayName
		existing.TokenEnc = bot.TokenEnc
		existing.TokenSHA256 = bot.TokenSHA256
		existing.KeyID = bot.KeyID
		existing.AddedByUserID = &userID
		existing.Status = models.TelegramBotActive
		existing.UpdatedAt = time.Now()
		_, err = tx.ModelContext(ctx, existing).
			Column("username", "display_name", "token_enc", "token_sha256", "key_id", "added_by_user_id", "status", "updated_at").
			WherePK().Update()
		result = existing
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// CreateTelegramLinkToken stores a one-time link for userID to bind botRef.
func CreateTelegramLinkToken(ctx context.Context, userID, botRef int64, secretHash []byte, expiresAt time.Time) error {
	_, err := db.ModelContext(ctx, &models.TelegramLinkToken{
		UserID: userID, BotRef: botRef, SecretSHA256: secretHash, ExpiresAt: expiresAt,
	}).Insert()
	return err
}

// LatestTelegramLinkExpiry is when the newest unused link of userID for botRef
// expires, or nil when there is no live one.
func LatestTelegramLinkExpiry(ctx context.Context, userID, botRef int64) (*time.Time, error) {
	var expires *time.Time
	_, err := db.QueryOneContext(ctx, pg.Scan(&expires), `
		SELECT max(expires_at) FROM telegram_link_token
		WHERE user_id = ? AND bot_ref = ? AND consumed_at IS NULL AND expires_at > now()`, userID, botRef)
	return expires, err
}

// TelegramLinkIsOpen reports, without writing anything, whether hash names a
// link of botRef that nobody has used yet. Only such a link lets a stranger's
// /start reach the bot.
func TelegramLinkIsOpen(ctx context.Context, botRef int64, hash []byte) (bool, error) {
	var n int
	_, err := db.QueryOneContext(ctx, pg.Scan(&n), `
		SELECT count(*) FROM telegram_link_token
		WHERE secret_sha256 = ? AND bot_ref = ? AND consumed_at IS NULL`, hash, botRef)
	return n > 0, err
}

// SetTelegramBotIdentity records the bot's public username and name.
func SetTelegramBotIdentity(ctx context.Context, botRef int64, username, displayName string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE telegram_bot SET username = ?, display_name = ?, updated_at = now() WHERE id = ?`,
		username, displayName, botRef)
	return err
}

// LinkOutcome is what a /start link_… did.
type LinkOutcome int

const (
	// LinkCreated: the binding was made now.
	LinkCreated LinkOutcome = iota
	// LinkAlreadyLinked: the same Telegram user used this link before and the
	// binding it made still stands.
	LinkAlreadyLinked
	// LinkExpired: no such link for this bot, or it is out of date.
	LinkExpired
	// LinkUsedByOther: another Telegram user consumed it.
	LinkUsedByOther
	// LinkTelegramTaken: this Telegram user serves another library account.
	LinkTelegramTaken
	// LinkBotTaken: the bot serves another library account now. A link
	// handed out before another account confirmed the bot does not move it.
	LinkBotTaken
)

// ConsumeTelegramLinkToken binds botRef to the link's account and to tgUserID,
// in one transaction: the account's previous binding, any other binding of
// the bot and of the Telegram user are revoked, the bot's next generation is
// taken, the link is consumed. freshNavKey is used only if the generations
// wrap.
func ConsumeTelegramLinkToken(
	ctx context.Context, botRef int64, secretHash []byte, tgUserID int64, tgDisplay string, freshNavKey []byte,
) (LinkOutcome, *models.TelegramBinding, error) {
	outcome := LinkExpired
	var binding *models.TelegramBinding
	err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if err := guardInboxLease(ctx, tx); err != nil {
			return err
		}
		// One order for every link and binding change: the bot row first,
		// then its links. Locking the link first and the bot second would let
		// two confirmations each hold what the other waits for. NO KEY: the
		// bot's key does not change, so the bot's incoming updates — whose
		// inbox rows reference it — are not held up meanwhile.
		if _, err := tx.ExecContext(ctx, `SELECT 1 FROM telegram_bot WHERE id = ? FOR NO KEY UPDATE`, botRef); err != nil {
			return err
		}
		link := new(models.TelegramLinkToken)
		err := tx.ModelContext(ctx, link).Where("secret_sha256 = ? AND bot_ref = ?", secretHash, botRef).For("UPDATE").Select()
		if errors.Is(err, pg.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if link.ConsumedAt != nil {
			outcome, binding, err = replayedLink(ctx, tx, link, tgUserID)
			return err
		}
		if !link.ExpiresAt.After(time.Now()) {
			// Say what is true and useful: a link that stopped working because
			// another account took the bot cannot be fixed by a new link.
			var otherAccount int
			if _, err = tx.QueryOneContext(ctx, pg.Scan(&otherAccount), `
				SELECT count(*) FROM telegram_binding
				WHERE bot_ref = ? AND status = 'active' AND user_id <> ?`, botRef, link.UserID); err != nil {
				return err
			}
			if otherAccount > 0 {
				outcome = LinkBotTaken
			}
			return nil
		}

		outcome, binding, err = bindByLink(ctx, tx, link, tgUserID, tgDisplay, freshNavKey)
		return err
	})
	if err != nil {
		return LinkExpired, nil, err
	}
	return outcome, binding, nil
}

// bindByLink makes the binding a live link asks for, inside the caller's
// transaction.
func bindByLink(
	ctx context.Context, tx *pg.Tx, link *models.TelegramLinkToken, tgUserID int64, tgDisplay string, freshNavKey []byte,
) (LinkOutcome, *models.TelegramBinding, error) {
	botRef := link.BotRef
	// The caller holds the bot row: it serializes every binding change of
	// this bot, and every link change after it.
	var otherAccount int
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&otherAccount), `
		SELECT count(*) FROM telegram_binding
		WHERE bot_ref = ? AND status = 'active' AND user_id <> ?`, botRef, link.UserID); err != nil {
		return LinkExpired, nil, err
	}
	if otherAccount > 0 {
		return LinkBotTaken, nil, nil
	}
	var elsewhere int
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&elsewhere), `
		SELECT count(*) FROM telegram_binding
		WHERE tg_user_id = ? AND status = 'active' AND user_id <> ?`, tgUserID, link.UserID); err != nil {
		return LinkExpired, nil, err
	}
	if elsewhere > 0 {
		return LinkTelegramTaken, nil, nil
	}
	generation, err := nextGeneration(ctx, tx, botRef, freshNavKey)
	if err != nil {
		return LinkExpired, nil, err
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE telegram_binding SET status = 'revoked', revoked_at = now()
		WHERE status = 'active' AND (user_id = ? OR bot_ref = ? OR tg_user_id = ?)`,
		link.UserID, botRef, tgUserID); err != nil {
		return LinkExpired, nil, err
	}
	binding := &models.TelegramBinding{
		UserID: link.UserID, BotRef: botRef, TGUserID: tgUserID, TGDisplay: tgDisplay,
		Generation: generation, Status: models.TelegramBindingActive,
	}
	if _, err = tx.ModelContext(ctx, binding).Returning("*").Insert(); err != nil {
		return LinkExpired, nil, err
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE telegram_link_token SET consumed_at = now(), consumed_by_tg = ?, binding_id = ? WHERE id = ?`,
		tgUserID, binding.ID, link.ID); err != nil {
		return LinkExpired, nil, err
	}
	// The bot is this account's now: links other accounts still hold for it
	// stop working.
	if _, err = tx.ExecContext(ctx, `
		UPDATE telegram_link_token SET expires_at = least(expires_at, now())
		WHERE bot_ref = ? AND user_id <> ? AND consumed_at IS NULL`, botRef, link.UserID); err != nil {
		return LinkExpired, nil, err
	}
	return LinkCreated, binding, nil
}

// replayedLink answers a link that was consumed before.
func replayedLink(
	ctx context.Context, tx *pg.Tx, link *models.TelegramLinkToken, tgUserID int64,
) (LinkOutcome, *models.TelegramBinding, error) {
	if link.ConsumedByTG == nil || *link.ConsumedByTG != tgUserID {
		return LinkUsedByOther, nil, nil
	}
	if link.BindingID == nil {
		return LinkExpired, nil, nil
	}
	binding := &models.TelegramBinding{ID: *link.BindingID}
	if err := tx.ModelContext(ctx, binding).WherePK().Select(); err != nil {
		if errors.Is(err, pg.ErrNoRows) {
			return LinkExpired, nil, nil
		}
		return LinkExpired, nil, err
	}
	if binding.Status != models.TelegramBindingActive {
		return LinkExpired, nil, nil
	}
	return LinkAlreadyLinked, binding, nil
}

// TelegramUserState is everything the site shows about a user's bot.
type TelegramUserState struct {
	// Binding is the active binding and Bot its bot, when connected.
	Binding *models.TelegramBinding
	Bot     *models.TelegramBot
	// Pending is a bot the user entered and has not linked since — a first
	// connection, or a change of connection while Binding still works.
	Pending *models.TelegramBot
}

// GetTelegramUserState reads the account's bot connection.
func GetTelegramUserState(ctx context.Context, userID int64) (TelegramUserState, error) {
	var state TelegramUserState
	binding, err := GetActiveTelegramBindingOfUser(ctx, userID)
	if err != nil {
		return state, err
	}
	if binding != nil {
		state.Binding = binding
		if state.Bot, err = GetTelegramBot(ctx, binding.BotRef); err != nil {
			return state, err
		}
	}
	pending := new(models.TelegramBot)
	q := db.ModelContext(ctx, pending).
		Where("added_by_user_id = ?", userID).
		Where("token_enc IS NOT NULL").
		Where("status = ?", models.TelegramBotActive).
		// Not bound now, and entered after its last binding ended: a bot
		// left behind by a change of connection is not waiting for a link.
		Where(`NOT EXISTS (SELECT 1 FROM telegram_binding b WHERE b.bot_ref = telegram_bot.id
			AND (b.status = 'active' OR b.revoked_at >= telegram_bot.updated_at))`).
		Order("updated_at DESC").
		Limit(1)
	err = q.Select()
	switch {
	case errors.Is(err, pg.ErrNoRows):
	case err != nil:
		return state, err
	default:
		state.Pending = pending
	}
	return state, nil
}

// DisconnectTelegram ends the account's connection: its active binding is
// revoked, its bots — the bound one and any pending — lose their tokens and
// stop being served, its open links stop working, and the old auth_user
// columns are cleared so nothing brings the bot back at the next start. It
// returns the bots as they were, tokens included, so the caller can remove
// their webhooks.
func DisconnectTelegram(ctx context.Context, userID int64) ([]models.TelegramBot, error) {
	var bots []models.TelegramBot
	err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if _, err := tx.QueryContext(ctx, &bots, `
			SELECT * FROM telegram_bot b
			WHERE b.token_enc IS NOT NULL AND (
				b.id IN (SELECT bot_ref FROM telegram_binding WHERE user_id = ? AND status = 'active')
				OR (b.added_by_user_id = ? AND NOT EXISTS (
					SELECT 1 FROM telegram_binding o WHERE o.bot_ref = b.id AND o.status = 'active' AND o.user_id <> ?)))
			FOR UPDATE`, userID, userID, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE telegram_binding SET status = 'revoked', revoked_at = now()
			WHERE user_id = ? AND status = 'active'`, userID); err != nil {
			return err
		}
		for i := range bots {
			if _, err := tx.ExecContext(ctx, `
				UPDATE telegram_bot SET status = 'disabled', token_enc = NULL, token_sha256 = NULL, updated_at = now()
				WHERE id = ?`, bots[i].ID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE telegram_link_token SET expires_at = now()
			WHERE user_id = ? AND consumed_at IS NULL AND expires_at > now()`, userID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE auth_user SET bot_token = NULL, telegram_id = NULL, webhook_uuid = '' WHERE id = ?`, userID)
		return err
	})
	return bots, err
}

// SetTelegramBotStatus records what the health check learned about a bot.
func SetTelegramBotStatus(ctx context.Context, botRef int64, status string) error {
	_, err := db.ExecContext(ctx, `UPDATE telegram_bot SET status = ?, updated_at = now() WHERE id = ?`, status, botRef)
	return err
}

// SetTelegramBindingBlocked marks whether the owner has blocked the bot.
func SetTelegramBindingBlocked(ctx context.Context, botRef int64, blocked bool) error {
	_, err := db.ExecContext(ctx, `
		UPDATE telegram_binding SET blocked_at = CASE WHEN ? THEN coalesce(blocked_at, now()) END
		WHERE bot_ref = ? AND status = 'active'`, blocked, botRef)
	return err
}

// InboxLease is a process's hold on one inbox row. Fence moves on with every
// takeover, so a holder that lost the row cannot act on it any more.
type InboxLease struct {
	BotRef   int64
	UpdateID int64
	Owner    uuid.UUID
	Fence    int64
}

// ErrInboxLeaseLost means the row is no longer held by this lease.
var ErrInboxLeaseLost = errors.New("telegram inbox lease lost")

// inboxLeaseKey carries the lease of the update a handler is working on.
type inboxLeaseKey struct{}

// WithInboxLease marks ctx as a handler's, working for lease. Every durable
// write made under such a context checks the lease in its own transaction.
func WithInboxLease(ctx context.Context, lease InboxLease) context.Context {
	return context.WithValue(ctx, inboxLeaseKey{}, lease)
}

// WithoutInboxLease is ctx no longer a handler's: work an update leaves
// running past its end, whose lease is gone by then.
func WithoutInboxLease(ctx context.Context) context.Context {
	return context.WithValue(ctx, inboxLeaseKey{}, nil)
}

// InboxLeaseFrom returns the lease a context carries, if it is a handler's.
func InboxLeaseFrom(ctx context.Context) (InboxLease, bool) {
	lease, ok := ctx.Value(inboxLeaseKey{}).(InboxLease)
	return lease, ok
}

// guardInboxLease is the fence of a handler's transaction: when ctx is a
// handler's, the update must still be held — same owner, same fence, lease
// live — or the transaction fails with ErrInboxLeaseLost and commits nothing.
// The row is locked FOR SHARE, so a takeover waits for this transaction
// rather than slipping in before it commits. Outside a handler it does
// nothing.
func guardInboxLease(ctx context.Context, tx *pg.Tx) error {
	l, ok := InboxLeaseFrom(ctx)
	if !ok {
		return nil
	}
	var held int
	if _, err := tx.QueryContext(ctx, pg.Scan(&held), `
		SELECT 1 FROM telegram_update_inbox WHERE `+leaseHeld+` FOR SHARE`,
		l.BotRef, l.UpdateID, l.Owner, l.Fence); err != nil {
		return err
	}
	if held != 1 {
		return ErrInboxLeaseLost
	}
	return nil
}

// InsertTelegramInboxUpdate records an accepted update, leased to owner for
// lease, and returns the lease. It reports false when the update was already
// recorded — a redelivery, which must not be handled again.
func InsertTelegramInboxUpdate(
	ctx context.Context, botRef, updateID int64, kind, payload string, owner uuid.UUID, lease time.Duration,
) (InboxLease, bool, error) {
	res, err := db.ExecContext(ctx, `
		INSERT INTO telegram_update_inbox (bot_ref, update_id, kind, payload, lease_owner, lease_expires_at, attempts, fence)
		VALUES (?, ?, ?, ?::jsonb, ?, now() + make_interval(secs => ?), 1, 1)
		ON CONFLICT DO NOTHING`, botRef, updateID, kind, payload, owner, lease.Seconds())
	if err != nil {
		return InboxLease{}, false, err
	}
	held := InboxLease{BotRef: botRef, UpdateID: updateID, Owner: owner, Fence: 1}
	return held, res.RowsAffected() == 1, nil
}

// leaseHeld is the condition every step of a holder checks: its own owner,
// fence and a live lease, on a row still pending.
const leaseHeld = `bot_ref = ? AND update_id = ? AND lease_owner = ? AND fence = ?
	AND state = 'pending' AND lease_expires_at > now()`

// ExtendTelegramInboxLease renews a lease still held; ErrInboxLeaseLost when
// it is not.
func ExtendTelegramInboxLease(ctx context.Context, l InboxLease, lease time.Duration) error {
	res, err := db.ExecContext(ctx, `
		UPDATE telegram_update_inbox SET lease_expires_at = now() + make_interval(secs => ?)
		WHERE `+leaseHeld, lease.Seconds(), l.BotRef, l.UpdateID, l.Owner, l.Fence)
	if err != nil {
		return err
	}
	if res.RowsAffected() != 1 {
		return ErrInboxLeaseLost
	}
	return nil
}

// MarkTelegramInboxReplyStarted is called before every outgoing call of a
// handler. In one statement it proves the lease is still held and records
// that a reply may be on its way; ErrInboxLeaseLost when the lease is gone,
// and then the call must not be made.
func MarkTelegramInboxReplyStarted(ctx context.Context, l InboxLease) error {
	res, err := db.ExecContext(ctx, `
		UPDATE telegram_update_inbox SET reply_started_at = coalesce(reply_started_at, now())
		WHERE `+leaseHeld, l.BotRef, l.UpdateID, l.Owner, l.Fence)
	if err != nil {
		return err
	}
	if res.RowsAffected() != 1 {
		return ErrInboxLeaseLost
	}
	return nil
}

// ClearTelegramInboxReplyStarted takes back the reply mark of an outgoing call
// that provably never reached Telegram, so the update stays safe to run again.
// Only the same holder (owner and fence) may; a live lease is not required —
// the mark is its own, and nobody else has acted on the row.
func ClearTelegramInboxReplyStarted(ctx context.Context, l InboxLease) error {
	_, err := db.ExecContext(ctx, `
		UPDATE telegram_update_inbox SET reply_started_at = NULL
		WHERE bot_ref = ? AND update_id = ? AND lease_owner = ? AND fence = ? AND state = 'pending'`,
		l.BotRef, l.UpdateID, l.Owner, l.Fence)
	return err
}

// ReleaseTelegramInboxUpdate hands a held update back unfinished — every
// reply it tried provably never left — for the next sweep to run again. The
// reply mark goes in the same statement: a mark left behind by a failed
// per-call clear would otherwise close the update as uncertain, its reply lost.
func ReleaseTelegramInboxUpdate(ctx context.Context, l InboxLease) error {
	_, err := db.ExecContext(ctx, `
		UPDATE telegram_update_inbox SET lease_expires_at = now(), reply_started_at = NULL
		WHERE bot_ref = ? AND update_id = ? AND lease_owner = ? AND fence = ? AND state = 'pending'`,
		l.BotRef, l.UpdateID, l.Owner, l.Fence)
	return err
}

// FinishTelegramInboxUpdate closes a held update as state and forgets its
// payload; ErrInboxLeaseLost when the lease is gone — expired included: a
// holder past its lease cannot vouch that its work is complete, and the row
// stays for the next one.
func FinishTelegramInboxUpdate(ctx context.Context, l InboxLease, state string) error {
	res, err := db.ExecContext(ctx, `
		UPDATE telegram_update_inbox
		SET state = ?, payload = NULL, lease_owner = NULL, lease_expires_at = NULL, finished_at = now()
		WHERE `+leaseHeld, state, l.BotRef, l.UpdateID, l.Owner, l.Fence)
	if err != nil {
		return err
	}
	if res.RowsAffected() != 1 {
		return ErrInboxLeaseLost
	}
	return nil
}

// ClaimStaleTelegramInboxUpdates takes over updates whose lease ran out —
// their process died, stalled or lost the database — for owner, moving each
// fence on. An update whose holder had begun replying is closed 'uncertain'
// rather than handled again: its reply may already be in the chat. Updates
// older than maxAge, or tried maxAttempts times, are closed too: an answer
// that late is worse than none. Rows another process is claiming are skipped,
// not waited for.
func ClaimStaleTelegramInboxUpdates(
	ctx context.Context, owner uuid.UUID, lease, maxAge time.Duration, maxAttempts, limit int,
) ([]models.TelegramInboxUpdate, error) {
	if _, err := db.ExecContext(ctx, `
		UPDATE telegram_update_inbox
		SET state = CASE WHEN reply_started_at IS NOT NULL THEN 'uncertain'
		                 WHEN attempts >= ? THEN 'failed' ELSE 'skipped' END,
		    payload = NULL, lease_owner = NULL, lease_expires_at = NULL, finished_at = now(), fence = fence + 1
		WHERE state = 'pending' AND lease_expires_at < now()
		  AND (reply_started_at IS NOT NULL OR received_at < now() - make_interval(secs => ?) OR attempts >= ?)`,
		maxAttempts, maxAge.Seconds(), maxAttempts); err != nil {
		return nil, err
	}
	var claimed []models.TelegramInboxUpdate
	_, err := db.QueryContext(ctx, &claimed, `
		WITH stale AS (
			SELECT bot_ref, update_id FROM telegram_update_inbox
			WHERE state = 'pending' AND lease_expires_at < now() AND reply_started_at IS NULL
			ORDER BY received_at LIMIT ?
			FOR UPDATE SKIP LOCKED)
		UPDATE telegram_update_inbox i
		SET lease_owner = ?, lease_expires_at = now() + make_interval(secs => ?),
		    attempts = i.attempts + 1, fence = i.fence + 1
		FROM stale WHERE i.bot_ref = stale.bot_ref AND i.update_id = stale.update_id
		RETURNING i.*`, limit, owner, lease.Seconds())
	return claimed, err
}

// PruneTelegramInbox deletes finished updates older than age, and link
// tokens that expired more than age ago.
func PruneTelegramInbox(ctx context.Context, age time.Duration) error {
	if _, err := db.ExecContext(ctx, `
		DELETE FROM telegram_update_inbox
		WHERE state <> 'pending' AND finished_at < now() - make_interval(secs => ?)`, age.Seconds()); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `
		DELETE FROM telegram_link_token WHERE expires_at < now() - make_interval(secs => ?)`, age.Seconds())
	return err
}
