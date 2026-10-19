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
	// AlreadyTapped: this very tap was recorded before; the job it made is
	// returned.
	AlreadyTapped
	// InProgress: the same book in the same format is already on its way to
	// this chat; that job is returned.
	InProgress
	// Busy: the account, or the service, has as many unfinished jobs as it
	// may.
	Busy
)

// deliveryUserLock namespaces the advisory lock that serialises one account's
// admissions, so its limit holds with two processes enqueueing at once.
const deliveryUserLock = 7_317_014

// EnqueueTelegramDelivery records a delivery request, unless this tap was
// recorded already, the same file is already on its way, or a limit is
// reached: at most perUser unfinished jobs per account and total overall.
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
		if d.TapKey != nil {
			err := tx.ModelContext(ctx, &job).Where("tap_key = ?", *d.TapKey).Select()
			if err == nil {
				admission = AlreadyTapped
				return nil
			}
			if !errors.Is(err, pg.ErrNoRows) {
				return err
			}
		}
		err := tx.ModelContext(ctx, &job).
			Where("binding_id = ? AND book_id = ? AND format = ? AND finished_at IS NULL", d.BindingID, d.BookID, d.Format).
			Select()
		if err == nil {
			admission = InProgress
			return nil
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
		if mine >= perUser || all >= total {
			admission = Busy
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

// heldUpdate runs an UPDATE of a held job and reports a lost lease.
func heldUpdate(ctx context.Context, set string, l DeliveryLease, extra string, args ...any) error {
	query := `UPDATE telegram_delivery SET ` + set + ` WHERE ` + deliveryHeld + extra
	all := append(args, l.ID, l.Owner, l.Fence)
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

// StartTelegramDeliverySend moves a prepared job to sending, committed before
// the upload begins: from here on the job is never sent again automatically.
func StartTelegramDeliverySend(ctx context.Context, l DeliveryLease, fileBytes int64) error {
	return heldUpdate(ctx, `state = 'sending', send_started_at = now(), file_bytes = ?`, l,
		` AND state = 'preparing'`, fileBytes)
}

// RequeueTelegramDelivery returns a held job to the queue, to run after
// delay. It is only for jobs whose request provably did not reach Telegram.
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
