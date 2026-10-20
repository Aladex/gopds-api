package database

import (
	"context"
	"errors"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
)

// GetTelegramFileCache is the bot's file_id for the book in format, when it
// stands for the same file: the same source and converter revisions. Nil
// when there is none, or sourceRev is empty — a book with no md5 is never
// cached.
func GetTelegramFileCache(
	ctx context.Context, botRef, bookID int64, format, sourceRev string, converterRev int,
) (*models.TelegramFileCache, error) {
	if sourceRev == "" {
		return nil, nil
	}
	entry := new(models.TelegramFileCache)
	err := db.ModelContext(ctx, entry).
		Where("bot_ref = ? AND book_id = ? AND format = ?", botRef, bookID, format).
		Where("source_rev = ? AND converter_rev = ?", sourceRev, converterRev).
		Select()
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return entry, nil
}

// CachedTelegramFormats are the formats of the book the bot holds a current
// file_id for.
func CachedTelegramFormats(ctx context.Context, botRef, bookID int64, sourceRev string, converterRev int) ([]string, error) {
	if sourceRev == "" {
		return nil, nil
	}
	var formats []string
	_, err := db.QueryContext(ctx, &formats, `
		SELECT format FROM telegram_file_cache
		WHERE bot_ref = ? AND book_id = ? AND source_rev = ? AND converter_rev = ?`,
		botRef, bookID, sourceRev, converterRev)
	return formats, err
}

// TelegramFileCached reports whether the bot holds a current file_id for the
// book in format: one of the book's present md5.
func TelegramFileCached(ctx context.Context, botRef, bookID int64, format string, converterRev int) (bool, error) {
	var cached bool
	_, err := db.QueryOneContext(ctx, pg.Scan(&cached), `
		SELECT EXISTS (SELECT 1 FROM telegram_file_cache c JOIN opds_catalog_book b ON b.id = c.book_id
			WHERE c.bot_ref = ? AND c.book_id = ? AND c.format = ? AND c.converter_rev = ?
			  AND b.md5 <> '' AND c.source_rev = b.md5)`, botRef, bookID, format, converterRev)
	return cached, err
}

// PutTelegramFileCache records the file_id of an upload, replacing what the
// bot had for the book in that format. An entry without a source revision
// is not recorded.
func PutTelegramFileCache(ctx context.Context, entry *models.TelegramFileCache) error {
	if entry.SourceRev == "" || entry.FileID == "" {
		return nil
	}
	_, err := db.ModelContext(ctx, entry).
		OnConflict("(bot_ref, book_id, format) DO UPDATE").
		Set("source_rev = EXCLUDED.source_rev, converter_rev = EXCLUDED.converter_rev, file_id = EXCLUDED.file_id").
		Set("file_unique_id = EXCLUDED.file_unique_id, bytes = EXCLUDED.bytes, created_at = now()").
		Insert()
	return err
}

// DropTelegramFileCache forgets a file_id Telegram refused. Only that
// file_id: an entry another upload has replaced meanwhile stays.
func DropTelegramFileCache(ctx context.Context, botRef, bookID int64, format, fileID string) error {
	_, err := db.ExecContext(ctx, `
		DELETE FROM telegram_file_cache WHERE bot_ref = ? AND book_id = ? AND format = ? AND file_id = ?`,
		botRef, bookID, format, fileID)
	return err
}

// ReopenTelegramDeliverySend takes a held job back from sending to
// preparing. It is only for a send Telegram refused outright, before any
// file went out — a cached file_id it does not know — so the job prepares
// and uploads the file instead, through the send gate again.
func ReopenTelegramDeliverySend(ctx context.Context, l DeliveryLease) error {
	return heldUpdate(ctx, `state = 'preparing', send_started_at = NULL`, l, ` AND state = 'sending'`)
}
