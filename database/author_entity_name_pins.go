package database

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"gopds-api/internal/authornorm"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// A name pin (design/author-display, name pins): an administrator chooses the
// name a live entity is shown under, out of the names its own credits are
// written with, and pins it. The build keeps a pinned row's names as it finds
// them and matches the entity by the name it computes, so a pin changes what
// readers see and no merge input (internal/authorentity/plan.go). Unpinning
// hands the name back to the build, which shows the computed name again on
// its next pass. Each pin and unpin is logged with its actor
// (author_entity_name_pin_log, migration 87).

// NamePinMaxBatch bounds one call: pins are chosen one entity at a time, a
// reviewed list at most.
const NamePinMaxBatch = 200

// ErrNamePinBatch is a batch with no entity, too many, or one entity twice.
var ErrNamePinBatch = errors.New("database: a name pin batch must name 1 to 200 distinct entities")

// ErrNamePinEntity names an entity that is not live: unknown, or merged.
var ErrNamePinEntity = errors.New("database: the entity is not a live entity")

// ErrNamePinName is a name none of the entity's credits is written with.
var ErrNamePinName = errors.New("database: the entity's credits use no such name")

// ErrNamePinNotPinned is an unpin of an entity whose name is not pinned.
var ErrNamePinNotPinned = errors.New("database: the entity's name is not pinned")

// The actions of the pin log.
const (
	namePinActionPin   = "pin"
	namePinActionUnpin = "unpin"
)

// NamePin is one entity and the name to pin it to.
type NamePin struct {
	Entity uuid.UUID `json:"entity_id"`
	Name   string    `json:"name"`
}

// EntityNameForm is one name an entity's credits are written with: the
// selected result's display name, its sort name (of the result with the
// lowest id, as the build takes it) and the books it is written on.
type EntityNameForm struct {
	Name     string `pg:"display_name" json:"name"`
	SortName string `pg:"sort_name" json:"sort_name"`
	Books    int    `pg:"books" json:"books"`
}

// EntityNames is what the pin dialog shows of an entity: its row and the
// forms a pin may choose from.
type EntityNames struct {
	ID          uuid.UUID        `json:"id"`
	DisplayName string           `json:"display_name"`
	NamePinned  bool             `json:"name_pinned"`
	Forms       []EntityNameForm `json:"forms"`
	// PinnedBy and PinnedAt are the last pin's, for a pinned name.
	PinnedBy *int64     `json:"pinned_by_user_id,omitempty"`
	PinnedAt *time.Time `json:"pinned_at,omitempty"`
}

// entityNameFormsSQL reads the forms of entity ?0: the person results
// selected for the current author credits the entity holds — through a hand
// or decision row of the credit itself, or else through its fingerprint's
// assignment — counted by distinct books, the most used first. Ordinal 0
// only: a segment of a list has no name of its own to choose.
const entityNameFormsSQL = `
	WITH mine AS (
		SELECT c.id AS credit_id, s.book_id, cs.result_id
		FROM public.author_entity_assignment a
		JOIN public.book_contributor_credit c ON c.source_fingerprint = a.source_fingerprint AND c.role = 'author'
		JOIN public.book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
			AND s.extractor_version = a.extractor_version
		JOIN public.book_contributor_credit_selection cs ON cs.credit_id = c.id AND cs.state = 'selected'
		WHERE a.entity_id = ?0 AND a.ordinal = 0
			AND NOT EXISTS (SELECT 1 FROM public.author_entity_credit_assignment ca
				WHERE ca.credit_id = c.id AND ca.ordinal = 0)
		UNION
		SELECT c.id, s.book_id, cs.result_id
		FROM public.author_entity_credit_assignment ca
		JOIN public.book_contributor_credit c ON c.id = ca.credit_id AND c.role = 'author'
		JOIN public.book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
		JOIN public.book_contributor_credit_selection cs ON cs.credit_id = c.id AND cs.state = 'selected'
		WHERE ca.entity_id = ?0 AND ca.ordinal = 0)
	SELECT r.display_name, (array_agg(r.sort_name ORDER BY r.id))[1] AS sort_name,
		count(DISTINCT m.book_id) AS books
	FROM mine m
	JOIN public.contributor_normalization_result r ON r.id = m.result_id AND r.kind = 'person'
	WHERE btrim(r.display_name) <> ''
	GROUP BY r.display_name
	ORDER BY books DESC, r.display_name`

// EntityNameForms reads the names an entity's credits are written with.
func EntityNameForms(ctx context.Context, db pg.DBI, entity uuid.UUID) ([]EntityNameForm, error) {
	var forms []EntityNameForm
	if _, err := db.QueryContext(ctx, &forms, entityNameFormsSQL, entity); err != nil {
		return nil, fmt.Errorf("reading the names of entity %s: %w", entity, err)
	}
	return forms, nil
}

// ReadEntityNames reads a live entity's row, its forms and its last pin, or
// ErrNamePinEntity.
func ReadEntityNames(ctx context.Context, db pg.DBI, entity uuid.UUID) (*EntityNames, error) {
	out := &EntityNames{ID: entity}
	res, err := db.QueryContext(ctx, pg.Scan(&out.DisplayName, &out.NamePinned), `
		SELECT display_name, name_pinned FROM public.author_entity WHERE id = ? AND merged_into IS NULL`, entity)
	if err != nil {
		return nil, fmt.Errorf("reading entity %s: %w", entity, err)
	}
	if res.RowsReturned() == 0 {
		return nil, ErrNamePinEntity
	}
	if out.Forms, err = EntityNameForms(ctx, db, entity); err != nil {
		return nil, err
	}
	if out.NamePinned {
		var last []struct {
			By *int64    `pg:"actor_user_id"`
			At time.Time `pg:"created_at"`
		}
		if _, err = db.QueryContext(ctx, &last, `
			SELECT actor_user_id, created_at FROM public.author_entity_name_pin_log
			WHERE entity_id = ? AND action = ? ORDER BY created_at DESC, id DESC LIMIT 1`,
			entity, namePinActionPin); err != nil {
			return nil, fmt.Errorf("reading the pin of entity %s: %w", entity, err)
		}
		if len(last) > 0 {
			out.PinnedBy, out.PinnedAt = last[0].By, &last[0].At
		}
	}
	return out, nil
}

// distinctEntities reports whether ids are 1 to NamePinMaxBatch entities,
// none twice.
func distinctEntities(ids []uuid.UUID) bool {
	if len(ids) == 0 || len(ids) > NamePinMaxBatch {
		return false
	}
	seen := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		if id == uuid.Nil || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

// lockedEntity is a live entity row locked to the commit of a pin.
type lockedEntity struct {
	DisplayName string `pg:"display_name"`
	NamePinned  bool   `pg:"name_pinned"`
}

// lockLiveEntity locks a live entity's row in tx, or answers
// ErrNamePinEntity.
func lockLiveEntity(ctx context.Context, tx pg.DBI, id uuid.UUID) (*lockedEntity, error) {
	var rows []lockedEntity
	if _, err := tx.QueryContext(ctx, &rows, `SELECT display_name, name_pinned FROM public.author_entity
		WHERE id = ? AND merged_into IS NULL FOR UPDATE`, id); err != nil {
		return nil, fmt.Errorf("reading entity %s: %w", id, err)
	}
	if len(rows) == 0 {
		return nil, ErrNamePinEntity
	}
	return &rows[0], nil
}

// PinEntityNames pins each entity's name in tx, all or none. The name must be
// one its credits are written with (EntityNameForms), as written; the sort
// name and the search key follow the chosen form as the build makes them. An
// active build refuses it with ErrAuthorEntityBuildActive: a pass that read
// the row before the pin would write its own name over it. The start lock,
// held to the commit, keeps a pass from starting until the pin is visible.
func PinEntityNames(ctx context.Context, tx pg.DBI, pins []NamePin, actor int64) error {
	ids := make([]uuid.UUID, len(pins))
	for i := range pins {
		ids[i] = pins[i].Entity
		pins[i].Name = strings.TrimSpace(pins[i].Name)
	}
	if !distinctEntities(ids) {
		return ErrNamePinBatch
	}
	if err := refuseActiveBuild(ctx, tx); err != nil {
		return err
	}
	// Every pin is checked before any is written: all or none.
	type checked struct {
		form     EntityNameForm
		previous string
	}
	ok := make([]checked, len(pins))
	for i, pin := range pins {
		row, err := lockLiveEntity(ctx, tx, pin.Entity)
		if err != nil {
			return err
		}
		forms, err := EntityNameForms(ctx, tx, pin.Entity)
		if err != nil {
			return err
		}
		at := slices.IndexFunc(forms, func(f EntityNameForm) bool { return f.Name == pin.Name })
		if at < 0 || pin.Name == "" {
			return fmt.Errorf("%w: entity %s", ErrNamePinName, pin.Entity)
		}
		ok[i] = checked{form: forms[at], previous: row.DisplayName}
	}
	for i, pin := range pins {
		form := ok[i].form
		if _, err := tx.ExecContext(ctx, `UPDATE public.author_entity
			SET display_name = ?, sort_name = ?, search_key = ?, name_pinned = true
			WHERE id = ?`, form.Name, form.SortName, authornorm.SearchKey(form.Name), pin.Entity); err != nil {
			return fmt.Errorf("pinning the name of entity %s: %w", pin.Entity, err)
		}
		if err := logNamePin(ctx, tx, pin.Entity, namePinActionPin, form.Name, ok[i].previous, actor); err != nil {
			return err
		}
	}
	return nil
}

// UnpinEntityNames hands each entity's name back to the build in tx, all or
// none: the name stays until the next pass shows the name it computes. Every
// entity must be live and pinned (ErrNamePinNotPinned); an active build
// refuses it as a pin does.
func UnpinEntityNames(ctx context.Context, tx pg.DBI, ids []uuid.UUID, actor int64) error {
	if !distinctEntities(ids) {
		return ErrNamePinBatch
	}
	if err := refuseActiveBuild(ctx, tx); err != nil {
		return err
	}
	previous := make([]string, len(ids))
	for i, id := range ids {
		row, err := lockLiveEntity(ctx, tx, id)
		if err != nil {
			return err
		}
		if !row.NamePinned {
			return fmt.Errorf("%w: entity %s", ErrNamePinNotPinned, id)
		}
		previous[i] = row.DisplayName
	}
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE public.author_entity SET name_pinned = false WHERE id = ?`,
			id); err != nil {
			return fmt.Errorf("unpinning the name of entity %s: %w", id, err)
		}
		if err := logNamePin(ctx, tx, id, namePinActionUnpin, previous[i], previous[i], actor); err != nil {
			return err
		}
	}
	return nil
}

// logNamePin records one pin or unpin with its actor (none for 0).
func logNamePin(ctx context.Context, tx pg.DBI, id uuid.UUID, action, name, previous string, actor int64) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO public.author_entity_name_pin_log
		(entity_id, action, display_name, previous_display_name, actor_user_id)
		VALUES (?, ?, ?, ?, nullif(?, 0))`, id, action, name, previous, actor); err != nil {
		return fmt.Errorf("logging the %s of entity %s: %w", action, id, err)
	}
	return nil
}
