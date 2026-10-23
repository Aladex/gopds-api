package database

import (
	"context"
	"fmt"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// TelegramAuthorHandleBase is the first handle the bot gives an author entity
// (migration 58): every handle is at least this, every legacy author ID far
// below it, so a bot ID tells which identity it was made under.
const TelegramAuthorHandleBase int64 = 1 << 40

// TelegramAuthorHandles returns the bot's handle of each entity, making the
// ones it has none for.
func TelegramAuthorHandles(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]int64, error) {
	out := make(map[uuid.UUID]int64, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	texts := make([]string, len(ids))
	for i, id := range ids {
		texts[i] = id.String()
	}
	var rows []struct {
		ID       int64     `pg:"id"`
		EntityID uuid.UUID `pg:"entity_id,type:uuid"`
	}
	err := db.RunInTransaction(ctx, func(tx *pg.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO telegram_author_entity (entity_id)
			SELECT unnest(?::uuid[]) ON CONFLICT (entity_id) DO NOTHING`, pg.Array(texts)); err != nil {
			return err
		}
		_, err := tx.QueryContext(ctx, &rows, `SELECT id, entity_id FROM telegram_author_entity
			WHERE entity_id = ANY (?::uuid[])`, pg.Array(texts))
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("giving %d author entities their bot handles: %w", len(ids), err)
	}
	for _, r := range rows {
		out[r.EntityID] = r.ID
	}
	return out, nil
}

// TelegramAuthorByHandle is the entity a bot handle names; pg.ErrNoRows when
// it names none.
func TelegramAuthorByHandle(ctx context.Context, handle int64) (uuid.UUID, error) {
	var id uuid.UUID
	_, err := db.QueryOneContext(ctx, pg.Scan(&id), `SELECT entity_id FROM telegram_author_entity WHERE id = ?`, handle)
	return id, err
}
