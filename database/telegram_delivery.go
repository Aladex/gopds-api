package database

import (
	"context"
	"errors"
	"time"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// Admission is what Enqueue made of a delivery request.
type Admission int

const (
	// Admitted: a new job was created.
	Admitted Admission = iota
	// AlreadyTapped: this request was made before — the same press
	// redelivered, or another press of the same button while its job runs or
	// within DeliveryRepeatWindow after it ended; that job is returned.
	AlreadyTapped
	// InProgress: the same book in the same format is already on its way to
	// this chat; that job is returned.
	InProgress
	// BusyAccount: the account has as many unfinished jobs as it may.
	BusyAccount
	// BusyService: the whole service has as many unfinished jobs as it may.
	BusyService
)

// DeliveryRepeatWindow is how long after its job ended a press of the same
// button still counts as the same request. A second press that only reached
// the library late is not a second file; a deliberate request once the file
// has arrived is.
const DeliveryRepeatWindow = 10 * time.Second

// The advisory locks that serialize admissions, so the limits hold with two
// processes enqueueing at once: one account's, then the service's, always in
// that order.
const (
	deliveryUserLock = 7_317_014
	deliveryAllLock  = 7_317_015
)

// EnqueueTelegramDelivery records a delivery request, unless it was made
// already (see AlreadyTapped), the same file is already on its way, or a limit
// is reached: at most perUser unfinished jobs per account and total overall.
//
// A request from a button is identified twice: by its press (tap_key), which
// a redelivery repeats, and by its button — bot, chat, message, revision,
// book and format — which another press of the same button repeats. A press
// answered with an existing job is recorded with it (telegram_delivery_press),
// in this transaction, so its own replay finds that job at any later time.
func EnqueueTelegramDelivery(
	ctx context.Context, d *models.TelegramDelivery, perUser, total int,
) (models.TelegramDelivery, Admission, error) {
	var job models.TelegramDelivery
	admission := Admitted
	err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if err := guardInboxLease(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(?, ?)`, deliveryUserLock, d.UserID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(?, 0)`, deliveryAllLock); err != nil {
			return err
		}
		pressed, err := findPress(ctx, tx, d, &job)
		if err != nil || pressed {
			if pressed {
				admission = AlreadyTapped
			}
			return err
		}
		repeated, err := findRepeat(ctx, tx, d, &job)
		if err != nil {
			return err
		}
		if repeated {
			admission = AlreadyTapped
			return recordPress(ctx, tx, d, job.ID)
		}
		err = tx.ModelContext(ctx, &job).
			Where("binding_id = ? AND book_id = ? AND format = ? AND finished_at IS NULL", d.BindingID, d.BookID, d.Format).
			Select()
		if err == nil {
			admission = InProgress
			return recordPress(ctx, tx, d, job.ID)
		}
		if !errors.Is(err, pg.ErrNoRows) {
			return err
		}
		var mine, all int
		if _, err = tx.QueryOneContext(ctx, pg.Scan(&mine, &all), `
			SELECT count(*) FILTER (WHERE user_id = ?), count(*)
			FROM telegram_delivery WHERE finished_at IS NULL`, d.UserID); err != nil {
			return err
		}
		if mine >= perUser {
			admission = BusyAccount
			return nil
		}
		if all >= total {
			admission = BusyService
			return nil
		}
		d.State = models.DeliveryQueued
		if _, err = tx.ModelContext(ctx, d).Returning("*").Insert(); err != nil {
			return err
		}
		job = *d
		return nil
	})
	return job, admission, err
}

// findPress loads into job the job d's press was answered with before — the
// job it made, or the one it was found to repeat — whenever that was: a
// replayed update is the same press, not a new one.
func findPress(ctx context.Context, tx *pg.Tx, d, job *models.TelegramDelivery) (bool, error) {
	if d.TapKey == nil {
		return false, nil
	}
	err := tx.ModelContext(ctx, job).
		Where("tap_key = ? OR id = (SELECT job_id FROM telegram_delivery_press WHERE tap_key = ?)", *d.TapKey, *d.TapKey).
		Limit(1).Select()
	if errors.Is(err, pg.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// recordPress remembers that d's press was answered with job jobID, so a
// replay of it finds that job after the repeat window too.
func recordPress(ctx context.Context, tx *pg.Tx, d *models.TelegramDelivery, jobID int64) error {
	if d.TapKey == nil {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO telegram_delivery_press (tap_key, job_id) VALUES (?, ?) ON CONFLICT (tap_key) DO NOTHING`,
		*d.TapKey, jobID)
	return err
}

// findRepeat loads into job an earlier request d repeats: the same button
// while its job runs or within DeliveryRepeatWindow after.
func findRepeat(ctx context.Context, tx *pg.Tx, d, job *models.TelegramDelivery) (bool, error) {
	if d.OriginMessageID == nil {
		return false, nil
	}
	err := tx.ModelContext(ctx, job).
		Where("bot_ref = ? AND chat_id = ? AND origin_message_id = ?", d.BotRef, d.ChatID, *d.OriginMessageID).
		Where("origin_revision IS NOT DISTINCT FROM ? AND book_id = ? AND format = ?", d.OriginRevision, d.BookID, d.Format).
		Where("finished_at IS NULL OR finished_at > now() - make_interval(secs => ?)", DeliveryRepeatWindow.Seconds()).
		Order("id DESC").Limit(1).Select()
	if errors.Is(err, pg.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// DeliveryLease is a process's hold on one job. Fence moves on with every
// claim and every takeover, so a holder that lost the job cannot act on it.
type DeliveryLease struct {
	ID    int64
	Owner uuid.UUID
	Fence int64
}

// ErrDeliveryLeaseLost means the job is no longer held by this lease.
var ErrDeliveryLeaseLost = errors.New("telegram delivery lease lost")

// LeaseOf is the lease a claimed job carries.
func LeaseOf(job *models.TelegramDelivery) DeliveryLease {
	lease := DeliveryLease{ID: job.ID, Fence: job.Fence}
	if job.LeaseOwner != nil {
		lease.Owner = *job.LeaseOwner
	}
	return lease
}

// deliveryHeld is the condition every step of a holder checks.
const deliveryHeld = `id = ? AND lease_owner = ? AND fence = ? AND lease_expires_at > now()`

// ClaimTelegramDeliveries takes up to limit runnable jobs for owner: queued
// ones whose time has come, and preparing ones whose holder's lease ran out
// (preparing is safe to repeat). Each claim moves the fence on. Rows another
// process is claiming are skipped, not waited for.
func ClaimTelegramDeliveries(ctx context.Context, owner uuid.UUID, lease time.Duration, limit int) ([]models.TelegramDelivery, error) {
	var jobs []models.TelegramDelivery
	_, err := db.QueryContext(ctx, &jobs, `
		WITH runnable AS (
			SELECT id FROM telegram_delivery
			WHERE state IN ('queued', 'preparing') AND next_attempt_at <= now()
			  AND (lease_expires_at IS NULL OR lease_expires_at < now())
			ORDER BY next_attempt_at, id LIMIT ?
			FOR UPDATE SKIP LOCKED)
		UPDATE telegram_delivery d
		SET state = 'preparing', lease_owner = ?, lease_expires_at = now() + make_interval(secs => ?),
		    attempt = d.attempt + 1, fence = d.fence + 1
		FROM runnable WHERE d.id = runnable.id
		RETURNING d.*`, limit, owner, lease.Seconds())
	return jobs, err
}

// leaseArgs is how many arguments deliveryHeld takes.
const leaseArgs = 3

// heldUpdate runs an UPDATE of a held job and reports a lost lease.
func heldUpdate(ctx context.Context, set string, l DeliveryLease, extra string, args ...any) error {
	query := `UPDATE telegram_delivery SET ` + set + ` WHERE ` + deliveryHeld + extra
	all := make([]any, 0, len(args)+leaseArgs)
	all = append(all, args...)
	all = append(all, l.ID, l.Owner, l.Fence)
	res, err := db.ExecContext(ctx, query, all...)
	if err != nil {
		return err
	}
	if res.RowsAffected() != 1 {
		return ErrDeliveryLeaseLost
	}
	return nil
}

// HeartbeatTelegramDelivery renews a held lease.
func HeartbeatTelegramDelivery(ctx context.Context, l DeliveryLease, lease time.Duration) error {
	return heldUpdate(ctx, `lease_expires_at = now() + make_interval(secs => ?)`, l,
		` AND state IN ('preparing', 'sending')`, lease.Seconds())
}

// Why a send may not start, besides a lost lease.
var (
	ErrDeliveryUnbound   = errors.New("telegram delivery: the chat is no longer bound")
	ErrDeliveryWithdrawn = errors.New("telegram delivery: the book is no longer available")
	// ErrDeliveryBotSuspended: the binding stands, but its bot may not send
	// now — its owner changed in Telegram, or its token was refused.
	ErrDeliveryBotSuspended = errors.New("telegram delivery: the bot is suspended")
)

// StartTelegramDeliverySend is the gate every upload attempt passes right
// before it begins. In one transaction it proves the job is still this
// holder's, its binding still active and its book still visible, and moves
// the job to sending: from here on it is never sent again automatically. When
// a check fails nothing is recorded, and the error says which.
func StartTelegramDeliverySend(ctx context.Context, l DeliveryLease, fileBytes int64) error {
	return db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		var bindingID, bookID int64
		_, err := tx.QueryOneContext(ctx, pg.Scan(&bindingID, &bookID), `
			SELECT binding_id, book_id FROM telegram_delivery
			WHERE `+deliveryHeld+` AND state = 'preparing' FOR UPDATE`, l.ID, l.Owner, l.Fence)
		if errors.Is(err, pg.ErrNoRows) {
			return ErrDeliveryLeaseLost
		}
		if err != nil {
			return err
		}
		// FOR SHARE: an unbinding, a change of the bot's owner or a
		// withdrawal waits for this transaction, rather than slipping in
		// between the check and the start.
		var botStatus string
		_, err = tx.QueryOneContext(ctx, pg.Scan(&botStatus), `
			SELECT t.status FROM telegram_binding b JOIN telegram_bot t ON t.id = b.bot_ref
			WHERE b.id = ? AND b.status = 'active' FOR SHARE OF b, t`, bindingID)
		if errors.Is(err, pg.ErrNoRows) {
			return ErrDeliveryUnbound
		}
		if err != nil {
			return err
		}
		if botStatus != models.TelegramBotActive {
			return ErrDeliveryBotSuspended
		}
		_, err = tx.ExecOneContext(ctx, `
			SELECT 1 FROM opds_catalog_book WHERE id = ? AND approved AND NOT duplicate_hidden FOR SHARE`, bookID)
		if errors.Is(err, pg.ErrNoRows) {
			return ErrDeliveryWithdrawn
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE telegram_delivery SET state = 'sending', send_started_at = now(), file_bytes = ? WHERE id = ?`,
			fileBytes, l.ID)
		return err
	})
}

// ResumeTelegramDelivery takes back a job its process put in the queue to
// wait out a 429, once the wait is over — unless another process took it
// first. The fence moves on, as with any claim.
func ResumeTelegramDelivery(ctx context.Context, id int64, owner uuid.UUID, lease time.Duration) (DeliveryLease, error) {
	l := DeliveryLease{ID: id, Owner: owner}
	_, err := db.QueryOneContext(ctx, pg.Scan(&l.Fence), `
		UPDATE telegram_delivery
		SET state = 'preparing', lease_owner = ?, lease_expires_at = now() + make_interval(secs => ?), fence = fence + 1
		WHERE id = ? AND state = 'queued' AND next_attempt_at <= now()
		RETURNING fence`, owner, lease.Seconds(), id)
	if errors.Is(err, pg.ErrNoRows) {
		return l, ErrDeliveryLeaseLost
	}
	return l, err
}

// RequeueTelegramDelivery returns a held job to the queue, to run after
// delay, clearing the start of its send. It is only for jobs whose request
// provably did not reach Telegram — none was made, the connection was
// refused, or Telegram answered 429.
func RequeueTelegramDelivery(ctx context.Context, l DeliveryLease, delay time.Duration) error {
	return heldUpdate(ctx, `state = 'queued', lease_owner = NULL, lease_expires_at = NULL, send_started_at = NULL,
		next_attempt_at = now() + make_interval(secs => ?)`, l, ` AND state IN ('preparing', 'sending')`, delay.Seconds())
}

// FinishTelegramDelivery closes a held job as state with outcome.
func FinishTelegramDelivery(ctx context.Context, l DeliveryLease, state, outcome string) error {
	return heldUpdate(ctx, `state = ?, outcome = ?, lease_owner = NULL, lease_expires_at = NULL, finished_at = now()`, l,
		` AND state IN ('preparing', 'sending')`, state, outcome)
}

// MarkTelegramDeliverySent records Telegram's confirmation. It holds even when
// the job was meanwhile closed uncertain — the confirmation is the truth — but
// never for a job some other holder runs now.
func MarkTelegramDeliverySent(ctx context.Context, l DeliveryLease, tgMessageID int64) error {
	res, err := db.ExecContext(ctx, `
		UPDATE telegram_delivery
		SET state = 'sent', outcome = 'sent', tg_message_id = ?, lease_owner = NULL, lease_expires_at = NULL,
		    finished_at = now()
		WHERE id = ? AND (
			(state = 'sending' AND lease_owner = ? AND fence = ?)
			OR (state = 'uncertain' AND fence = ? + 1))`, tgMessageID, l.ID, l.Owner, l.Fence, l.Fence)
	if err != nil {
		return err
	}
	if res.RowsAffected() != 1 {
		return ErrDeliveryLeaseLost
	}
	return nil
}

// SetTelegramDeliveryStatusMessage remembers the job's status message.
func SetTelegramDeliveryStatusMessage(ctx context.Context, id, messageID int64) error {
	_, err := db.ExecContext(ctx, `UPDATE telegram_delivery SET status_message_id = ? WHERE id = ?`, messageID, id)
	return err
}

// SweepTelegramDeliveries closes the jobs whose holder vanished at a point
// where the job may not simply run again: one that was sending is closed
// uncertain — the file may be in the chat — and one that kept failing to be
// prepared is closed failed. Each move takes the fence on. It returns the
// jobs it closed, so their chats can be told.
func SweepTelegramDeliveries(ctx context.Context, maxAttempts int) ([]models.TelegramDelivery, error) {
	var closed []models.TelegramDelivery
	_, err := db.QueryContext(ctx, &closed, `
		UPDATE telegram_delivery
		SET state = CASE WHEN state = 'sending' THEN 'uncertain' ELSE 'failed' END,
		    outcome = CASE WHEN state = 'sending' THEN 'uncertain' ELSE 'conversion_failed' END,
		    lease_owner = NULL, lease_expires_at = NULL, finished_at = now(), fence = fence + 1
		WHERE lease_expires_at < now()
		  AND (state = 'sending' OR (state = 'preparing' AND attempt >= ?))
		RETURNING *`, maxAttempts)
	return closed, err
}

// PruneTelegramDeliveries deletes finished jobs older than age.
func PruneTelegramDeliveries(ctx context.Context, age time.Duration) error {
	_, err := db.ExecContext(ctx, `
		DELETE FROM telegram_delivery WHERE finished_at < now() - make_interval(secs => ?)`, age.Seconds())
	return err
}

// GetTelegramDelivery loads one job.
func GetTelegramDelivery(ctx context.Context, id int64) (*models.TelegramDelivery, error) {
	job := &models.TelegramDelivery{ID: id}
	if err := db.ModelContext(ctx, job).WherePK().Select(); err != nil {
		return nil, err
	}
	return job, nil
}

// GetBookWithAuthors loads a book and its authors, as a delivery names them.
func GetBookWithAuthors(ctx context.Context, id int64) (*models.Book, error) {
	book := &models.Book{ID: id}
	if err := db.ModelContext(ctx, book).Relation("Authors").WherePK().Select(); err != nil {
		return nil, err
	}
	return book, nil
}

// GetTelegramDefaultFormat is the format the account's books are sent in
// when it does not name one: its setting, EPUB without one.
func GetTelegramDefaultFormat(ctx context.Context, userID int64) (string, error) {
	var format string
	_, err := db.QueryOneContext(ctx, pg.Scan(&format),
		`SELECT default_format FROM telegram_user_settings WHERE user_id = ?`, userID)
	if errors.Is(err, pg.ErrNoRows) {
		return defaultTelegramFormat, nil
	}
	return format, err
}
