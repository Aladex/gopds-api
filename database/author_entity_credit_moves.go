package database

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"gopds-api/internal/authorentity"
	"gopds-api/internal/authornorm"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// A hand move (D5): an administrator puts single credits on another entity,
// new or existing, when a card holds two people and no name tells them apart
// («Алексей Толстой»). The row is a manual credit row, so every build keeps
// it; deleting it hands the credit back to the build.

// CreditMoveMaxBatch bounds one move: a hand move is for books read one by
// one, not for a bulk rewrite.
const CreditMoveMaxBatch = 200

// ErrCreditMoveTarget is a move to no live entity, or to no valid new one.
var ErrCreditMoveTarget = errors.New("database: the credits' target is not a live entity")

// ErrCreditMoveCredits names a credit that cannot move: unknown, or not the
// current selected person a build reads as one entity. A list of several
// people is under review or unresolved, and no credit row of ordinal 0
// decides its segments.
var ErrCreditMoveCredits = errors.New("database: a credit cannot be moved by hand")

// ErrCreditMoveNotLive is a revoke of a credit with no hand move.
var ErrCreditMoveNotLive = errors.New("database: the credit has no hand move")

// CreditMove is a move of credits to Entity, or to a new entity NewName names.
type CreditMove struct {
	Credits []int64
	Entity  *uuid.UUID
	NewName string
	Actor   int64
}

// CreditMoveRow is one live hand move.
type CreditMoveRow struct {
	CreditID   int64     `pg:"credit_id" json:"credit_id"`
	BookID     int64     `pg:"book_id" json:"book_id"`
	Name       string    `pg:"name" json:"name"`
	EntityID   uuid.UUID `pg:"entity_id,type:uuid" json:"entity_id"`
	EntityName string    `pg:"entity_name" json:"entity_name"`
	ActorID    *int64    `pg:"created_by_user_id" json:"created_by_user_id,omitempty"`
	UpdatedAt  time.Time `pg:"updated_at" json:"updated_at"`
}

// distinctCredits is the credits sorted, without repeats, or nil when there
// are none or too many.
func distinctCredits(ids []int64) []int64 {
	out := slices.Clone(ids)
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) == 0 || len(out) > CreditMoveMaxBatch {
		return nil
	}
	return out
}

// MoveCredits writes the manual rows of a move in tx and returns the target.
// All of it moves or none does. An active build refuses it with
// ErrAuthorEntityBuildActive: a plan made without the move could merge its
// target away, and only a pass that applies a decision fences its plan. The
// start lock, held to the commit, keeps a pass from starting until the move
// is visible to it, and a pass that sees the manual row anchors the target.
func MoveCredits(ctx context.Context, tx pg.DBI, m *CreditMove) (uuid.UUID, error) {
	credits := distinctCredits(m.Credits)
	if credits == nil {
		return uuid.Nil, ErrCreditMoveCredits
	}
	if err := refuseActiveBuild(ctx, tx); err != nil {
		return uuid.Nil, err
	}
	if err := checkMovableCredits(ctx, tx, credits); err != nil {
		return uuid.Nil, err
	}
	target, err := creditMoveTarget(ctx, tx, m)
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO public.author_entity_credit_assignment (credit_id, ordinal, entity_id, method, rule, created_by_user_id)
		SELECT id, 0, ?, ?, ?, nullif(?, 0) FROM unnest(?::bigint[]) AS id
		ON CONFLICT (credit_id, ordinal) DO UPDATE SET entity_id = EXCLUDED.entity_id, method = EXCLUDED.method,
			rule = EXCLUDED.rule, evidence_report_id = NULL, created_by_user_id = EXCLUDED.created_by_user_id,
			updated_at = now()`, target, authorentity.MethodManual, authorentity.RuleCreditMove, m.Actor,
		pg.Array(credits)); err != nil {
		return uuid.Nil, fmt.Errorf("writing the hand moves: %w", err)
	}
	return target, nil
}

// refuseActiveBuild takes the start lock to tx's commit and refuses while a
// pass is pending or running.
func refuseActiveBuild(ctx context.Context, tx pg.DBI) error {
	if err := lockEntityBuildStart(ctx, tx); err != nil {
		return err
	}
	active, err := ActiveAuthorEntityBuild(ctx, tx)
	if err != nil {
		return err
	}
	if active != 0 {
		return ErrAuthorEntityBuildActive
	}
	return nil
}

// checkMovableCredits refuses a credit that is not a current selected
// person: the input a build reads as one entity, so a row of ordinal 0
// decides all of it. It reads the input alone, never what a build wrote, and
// keeps the rows it read as they are until the move commits: a selection or
// a snapshot changing now is waited for, then read as it ended.
func checkMovableCredits(ctx context.Context, tx pg.DBI, credits []int64) error {
	var movable []int64
	if _, err := tx.QueryContext(ctx, &movable, `
		SELECT c.id FROM public.book_contributor_credit c
		JOIN public.book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
		JOIN public.book_contributor_credit_selection cs ON cs.credit_id = c.id AND cs.state = 'selected'
		JOIN public.contributor_normalization_result r ON r.id = cs.result_id
		WHERE c.id = ANY (?::bigint[]) AND c.role = 'author' AND r.kind = 'person'
		ORDER BY c.id FOR SHARE OF s, cs`, pg.Array(credits)); err != nil {
		return fmt.Errorf("reading the credits to move: %w", err)
	}
	if len(movable) != len(credits) {
		return ErrCreditMoveCredits
	}
	return nil
}

// creditMoveTarget is the live entity a move names, or the card it creates:
// the build gives that card its names from its credits.
func creditMoveTarget(ctx context.Context, tx pg.DBI, m *CreditMove) (uuid.UUID, error) {
	if (m.Entity != nil) == (m.NewName != "") {
		return uuid.Nil, ErrCreditMoveTarget
	}
	if m.Entity != nil {
		var live []uuid.UUID
		if _, err := tx.QueryContext(ctx, &live, `SELECT id FROM public.author_entity
			WHERE id = ? AND merged_into IS NULL FOR KEY SHARE`, *m.Entity); err != nil {
			return uuid.Nil, fmt.Errorf("reading the credits' target: %w", err)
		}
		if len(live) == 0 {
			return uuid.Nil, ErrCreditMoveTarget
		}
		return *m.Entity, nil
	}
	key := authornorm.SearchKey(m.NewName)
	if key == "" {
		return uuid.Nil, ErrCreditMoveTarget
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO public.author_entity (id, kind, display_name, sort_name, search_key)
		VALUES (?, 'person', ?, ?, ?)`, id, m.NewName, m.NewName, key); err != nil {
		return uuid.Nil, fmt.Errorf("creating the credits' target: %w", err)
	}
	return id, nil
}

// RevokeCreditMoves deletes hand moves in tx: the next build decides those
// credits again. All of them are hand moves or nothing changes. A card only
// they held goes with the next build.
func RevokeCreditMoves(ctx context.Context, tx pg.DBI, ids []int64) (int, error) {
	credits := distinctCredits(ids)
	if credits == nil {
		return 0, ErrCreditMoveNotLive
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM public.author_entity_credit_assignment
		WHERE credit_id = ANY (?::bigint[]) AND ordinal = 0 AND method = ? AND rule = ?`, pg.Array(credits),
		authorentity.MethodManual, authorentity.RuleCreditMove)
	if err != nil {
		return 0, fmt.Errorf("revoking the hand moves: %w", err)
	}
	if res.RowsAffected() != len(credits) {
		return 0, ErrCreditMoveNotLive
	}
	return len(credits), nil
}

// ListCreditMoves reads the live hand moves, newest first.
func ListCreditMoves(ctx context.Context, db pg.DBI) ([]CreditMoveRow, error) {
	var rows []CreditMoveRow
	if _, err := db.QueryContext(ctx, &rows, `
		SELECT ca.credit_id, s.book_id, c.source_display_name AS name, ca.entity_id, e.display_name AS entity_name,
			ca.created_by_user_id, ca.updated_at
		FROM public.author_entity_credit_assignment ca
		JOIN public.book_contributor_credit c ON c.id = ca.credit_id
		JOIN public.book_metadata_snapshot s ON s.id = c.snapshot_id
		JOIN public.author_entity e ON e.id = ca.entity_id
		WHERE ca.method = ? AND ca.rule = ?
		ORDER BY ca.updated_at DESC, ca.credit_id`, authorentity.MethodManual, authorentity.RuleCreditMove); err != nil {
		return nil, fmt.Errorf("reading the hand moves: %w", err)
	}
	return rows, nil
}
