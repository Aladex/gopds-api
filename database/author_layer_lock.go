package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-pg/pg/v10"
)

// The author layer's advisory lock ('layr', 0), in the two-key form that keeps
// it apart from the per-input locks and the migration lock. The deletion of a
// book's layer (author_layer_delete_books, migration 26 — the SQL spells the
// same numbers) holds it exclusively; a source writer holds it shared while it
// adds credits, so a deletion never decides a fingerprint is orphaned while a
// credit of it is being written. Writers do not wait for each other.
const (
	authorLayerLockClass int32 = 0x6c617972
	authorLayerLockKey   int32 = 0
)

// ErrSourceBookMissing is returned when the book a source is written for no
// longer exists: it was deleted, with its layer, while the source was read.
var ErrSourceBookMissing = errors.New("database: the book of the author metadata source no longer exists")

// lockSourceBook locks the book a source is written for, before any of its
// snapshots: the first step of the lock order the layer deletion shares. FOR
// KEY SHARE lets other writers and legacy updates of the book through and only
// conflicts with deleting it.
func lockSourceBook(ctx context.Context, conn pg.DBI, bookID int64) error {
	var id int64
	_, err := conn.QueryOneContext(ctx, pg.Scan(&id), `SELECT id FROM opds_catalog_book WHERE id = ? FOR KEY SHARE`, bookID)
	if errors.Is(err, pg.ErrNoRows) {
		return ErrSourceBookMissing
	}
	if err != nil {
		return fmt.Errorf("locking the source's book: %w", err)
	}
	return nil
}

// lockAuthorLayerShared takes the layer lock shared, for a writer about to add
// credits.
func lockAuthorLayerShared(ctx context.Context, conn pg.DBI) error {
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(?, ?)`,
		authorLayerLockClass, authorLayerLockKey); err != nil {
		return fmt.Errorf("locking the author layer: %w", err)
	}
	return nil
}
