package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gopds-api/internal/authorentity"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// The hand join (migration 77, E4o): an administrator pins two entities as
// one person. A side records the raw fingerprints of its entity's
// fingerprint rows — the keys it owns, not the books other keys' buckets
// moved into it — or the fingerprints the administrator named; every build
// joins the components they are in, unless a veto keeps them apart.

// ErrMergePinSides is a pin whose sides are one entity, share a
// fingerprint, or one of which holds none.
var ErrMergePinSides = errors.New("database: the pin's sides are not two sets of fingerprints")

// ErrMergePinNotLive is a revoke of a pin that is revoked or does not exist.
var ErrMergePinNotLive = errors.New("database: the merge pin is not live")

// MergePinSide is one side of a pin to register: an entity, or raw
// fingerprints.
type MergePinSide struct {
	Entity       *uuid.UUID
	Fingerprints [][]byte
}

// fingerprints are the side's raw fingerprints: the named ones, else its
// entity's fingerprint rows.
func (s MergePinSide) fingerprints(ctx context.Context, tx pg.DBI) ([][]byte, error) {
	if s.Entity == nil {
		return s.Fingerprints, nil
	}
	var rows []struct {
		Fingerprint []byte `pg:"source_fingerprint"`
	}
	if _, err := tx.QueryContext(ctx, &rows, `
		SELECT DISTINCT source_fingerprint FROM public.author_entity_assignment WHERE entity_id = ?
		ORDER BY source_fingerprint`, *s.Entity); err != nil {
		return nil, fmt.Errorf("reading the pin side's fingerprints: %w", err)
	}
	out := make([][]byte, len(rows))
	for i, r := range rows {
		out[i] = r.Fingerprint
	}
	return out, nil
}

// RegisterMergePin records a pin of two sides in the caller's transaction
// and returns its id. Sides that are one entity, share a fingerprint or
// hold none refuse it (ErrMergePinSides).
func RegisterMergePin(ctx context.Context, tx pg.DBI, a, b MergePinSide, note string, actor int64) (int64, error) {
	if a.Entity != nil && b.Entity != nil && *a.Entity == *b.Entity {
		return 0, ErrMergePinSides
	}
	sides := map[string][][]byte{}
	seen := map[string]string{}
	for side, s := range map[string]MergePinSide{authorentity.VetoSideA: a, authorentity.VetoSideB: b} {
		fps, err := s.fingerprints(ctx, tx)
		if err != nil {
			return 0, err
		}
		if len(fps) == 0 {
			return 0, ErrMergePinSides
		}
		for _, fp := range fps {
			if other, ok := seen[string(fp)]; ok && other != side {
				return 0, ErrMergePinSides
			}
			seen[string(fp)] = side
		}
		sides[side] = fps
	}
	var id int64
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&id), `
		INSERT INTO public.author_entity_merge_pin (note, created_by_user_id) VALUES (?, nullif(?, 0)) RETURNING id`,
		note, actor); err != nil {
		return 0, fmt.Errorf("recording the merge pin: %w", err)
	}
	for _, side := range []string{authorentity.VetoSideA, authorentity.VetoSideB} {
		for _, fp := range sides[side] {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO public.author_entity_merge_pin_member (pin_id, side, source_fingerprint) VALUES (?, ?, ?)
				ON CONFLICT DO NOTHING`, id, side, fp); err != nil {
				return 0, fmt.Errorf("recording the merge pin's members: %w", err)
			}
		}
	}
	return id, nil
}

// RevokeMergePin revokes a live pin in the caller's transaction: the next
// build no longer joins its sides.
func RevokeMergePin(ctx context.Context, tx pg.DBI, id, actor int64) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE public.author_entity_merge_pin SET revoked_at = now(), revoked_by_user_id = nullif(?, 0)
		WHERE id = ? AND revoked_at IS NULL`, actor, id)
	if err != nil {
		return fmt.Errorf("revoking the merge pin: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrMergePinNotLive
	}
	return nil
}

// LoadMergePins reads the live pins with their members.
func LoadMergePins(ctx context.Context, db pg.DBI) ([]authorentity.MergePin, error) {
	var members []struct {
		PinID       int64  `pg:"pin_id"`
		Side        string `pg:"side"`
		Fingerprint []byte `pg:"source_fingerprint"`
	}
	if _, err := db.QueryContext(ctx, &members, `
		SELECT m.pin_id, m.side, m.source_fingerprint
		FROM public.author_entity_merge_pin_member m
		JOIN public.author_entity_merge_pin p ON p.id = m.pin_id AND p.revoked_at IS NULL
		ORDER BY m.pin_id, m.side, m.source_fingerprint`); err != nil {
		return nil, fmt.Errorf("reading the merge pins: %w", err)
	}
	var out []authorentity.MergePin
	for _, m := range members {
		if len(out) == 0 || out[len(out)-1].ID != m.PinID {
			out = append(out, authorentity.MergePin{ID: m.PinID})
		}
		p := &out[len(out)-1]
		if m.Side == authorentity.VetoSideA {
			p.A = append(p.A, string(m.Fingerprint))
		} else {
			p.B = append(p.B, string(m.Fingerprint))
		}
	}
	return out, nil
}

// MergePinRow is a pin as the admin list shows it: its sides' fingerprint
// counts and the entities their fingerprint rows are in now (one entity
// across both sides once a build joined them).
type MergePinRow struct {
	ID        int64      `json:"id"`
	Note      string     `json:"note"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	SideA     int        `json:"side_a_fingerprints"`
	SideB     int        `json:"side_b_fingerprints"`
	EntitiesA []string   `json:"side_a_entities"`
	EntitiesB []string   `json:"side_b_entities"`
}

// ListMergePins reads every pin, live or revoked, newest first.
func ListMergePins(ctx context.Context, db pg.DBI) ([]MergePinRow, error) {
	var rows []struct {
		ID        int64      `pg:"id"`
		Note      string     `pg:"note"`
		CreatedAt time.Time  `pg:"created_at"`
		RevokedAt *time.Time `pg:"revoked_at"`
		SideA     int        `pg:"side_a"`
		SideB     int        `pg:"side_b"`
		EntitiesA []string   `pg:"entities_a,array"`
		EntitiesB []string   `pg:"entities_b,array"`
	}
	if _, err := db.QueryContext(ctx, &rows, `
		SELECT p.id, p.note, p.created_at, p.revoked_at,
			(SELECT count(*) FROM public.author_entity_merge_pin_member m WHERE m.pin_id = p.id AND m.side = 'a') AS side_a,
			(SELECT count(*) FROM public.author_entity_merge_pin_member m WHERE m.pin_id = p.id AND m.side = 'b') AS side_b,
			coalesce((SELECT array_agg(DISTINCT a.entity_id::text) FROM public.author_entity_merge_pin_member m
				JOIN public.author_entity_assignment a ON a.source_fingerprint = m.source_fingerprint
				WHERE m.pin_id = p.id AND m.side = 'a'), '{}') AS entities_a,
			coalesce((SELECT array_agg(DISTINCT a.entity_id::text) FROM public.author_entity_merge_pin_member m
				JOIN public.author_entity_assignment a ON a.source_fingerprint = m.source_fingerprint
				WHERE m.pin_id = p.id AND m.side = 'b'), '{}') AS entities_b
		FROM public.author_entity_merge_pin p ORDER BY p.id DESC`); err != nil {
		return nil, fmt.Errorf("listing the merge pins: %w", err)
	}
	out := make([]MergePinRow, len(rows))
	for i, r := range rows {
		out[i] = MergePinRow{ID: r.ID, Note: r.Note, CreatedAt: r.CreatedAt, RevokedAt: r.RevokedAt, SideA: r.SideA,
			SideB: r.SideB, EntitiesA: r.EntitiesA, EntitiesB: r.EntitiesB}
	}
	return out, nil
}
