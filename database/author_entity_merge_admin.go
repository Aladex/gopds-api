package database

import (
	"context"
	"fmt"
	"time"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// What the «Сканирование» card shows of the model merges (E4b-3b): the live
// decisions with what the newest pass did with them, the live vetoes with
// what it found of them and the entities that hold their sides now, and
// whether a live veto is unanchored. IDs, counts and display names only.

// MergeSideView is one side of a decision: the entity its member keys are
// assigned to now, a display name its credits go by, the books it was
// about.
type MergeSideView struct {
	Entity      *uuid.UUID `pg:"entity,type:uuid" json:"entity,omitempty"`
	DisplayName string     `pg:"display_name" json:"display_name"`
	Books       int        `pg:"books,use_zero" json:"books"`
}

// MergeDecisionView is a live decision.
type MergeDecisionView struct {
	ID        int64     `pg:"id" json:"id"`
	Kind      string    `pg:"kind" json:"kind"`
	Script    string    `pg:"script" json:"script"`
	ItemKind  string    `pg:"item_kind" json:"item_kind"`
	Outcome   string    `pg:"outcome" json:"outcome"`
	CreatedAt time.Time `pg:"created_at" json:"created_at"`
	// State and Reason are what the newest pass did with it; absent before
	// a pass read it.
	State  *string       `pg:"state" json:"state,omitempty"`
	Reason *string       `pg:"reason" json:"reason,omitempty"`
	SideA  MergeSideView `pg:"-" json:"side_a"`
	SideB  MergeSideView `pg:"-" json:"side_b"`
}

// MergeVetoEntity is an entity holding a fingerprint of a veto's side.
type MergeVetoEntity struct {
	ID          uuid.UUID `pg:"id,type:uuid" json:"id"`
	DisplayName string    `pg:"display_name" json:"display_name"`
}

// MergeVetoView is a live veto with its state in the newest pass that
// changed it.
type MergeVetoView struct {
	ID               int64     `pg:"id" json:"id"`
	RevokeDecisionID int64     `pg:"revoke_decision_id" json:"revoke_decision_id"`
	ItemKind         string    `pg:"item_kind" json:"item_kind"`
	BookID           *int64    `pg:"book_id" json:"book_id,omitempty"`
	ReplacesVetoID   *int64    `pg:"replaces_veto_id" json:"replaces_veto_id,omitempty"`
	CreatedAt        time.Time `pg:"created_at" json:"created_at"`
	// State is absent until a pass read the veto.
	State               *string `pg:"state" json:"state,omitempty"`
	HeldBooks           int     `pg:"held_books,use_zero" json:"held_books"`
	LostBooks           int     `pg:"lost_books,use_zero" json:"lost_books"`
	GoneBooks           int     `pg:"gone_books,use_zero" json:"gone_books"`
	WidenedFingerprints int     `pg:"widened_fingerprints,use_zero" json:"widened_fingerprints"`
	// SideA and SideB are the entities that hold a fingerprint of each
	// widened side now: the choices of a re-anchor (at most
	// mergeVetoSideEntities each).
	SideA []MergeVetoEntity `pg:"-" json:"side_a"`
	SideB []MergeVetoEntity `pg:"-" json:"side_b"`
}

// MergeDecisionsView is the card's merge block.
type MergeDecisionsView struct {
	// Decisions are the newest live same decisions (at most
	// mergeDecisionsShown); DecisionsTotal counts them all.
	Decisions      []MergeDecisionView `json:"decisions"`
	DecisionsTotal int                 `json:"decisions_total"`
	Vetoes         []MergeVetoView     `json:"vetoes"`
	// Unanchored: a live veto the last completed build found unanchored.
	Unanchored bool `json:"unanchored"`
	// Registrations are the merge strata registered (E4d), newest first.
	Registrations []MergeRegistration `json:"registrations"`
}

const (
	mergeDecisionsShown   = 50
	mergeVetoSideEntities = 10
)

// ReadMergeDecisions reads the card's merge block.
func ReadMergeDecisions(ctx context.Context, db pg.DBI) (*MergeDecisionsView, error) {
	v := &MergeDecisionsView{Decisions: []MergeDecisionView{}, Vetoes: []MergeVetoView{}}
	if _, err := db.QueryOneContext(ctx, pg.Scan(&v.DecisionsTotal), `
		SELECT count(*) FROM public.author_entity_merge_decision WHERE outcome = 'same' AND revoked_at IS NULL`); err != nil {
		return nil, fmt.Errorf("counting the merge decisions: %w", err)
	}
	if _, err := db.QueryContext(ctx, &v.Decisions, `
		SELECT d.id, d.kind, d.script, d.item_kind, d.outcome, d.created_at, s.state, s.reason
		FROM public.author_entity_merge_decision d
		LEFT JOIN public.author_entity_merge_decision_state s ON s.decision_id = d.id
		WHERE d.outcome = 'same' AND d.revoked_at IS NULL
		ORDER BY d.id DESC LIMIT ?`, mergeDecisionsShown); err != nil {
		return nil, fmt.Errorf("reading the merge decisions: %w", err)
	}
	for i := range v.Decisions {
		d := &v.Decisions[i]
		for side, out := range map[string]*MergeSideView{"a": &d.SideA, "b": &d.SideB} {
			if err := readDecisionSide(ctx, db, d.ID, side, out); err != nil {
				return nil, err
			}
		}
	}
	if _, err := db.QueryContext(ctx, &v.Vetoes, `
		SELECT v.id, v.revoke_decision_id, v.item_kind, v.book_id, v.replaces_veto_id, v.created_at, s.state,
			coalesce(s.held_books, 0) AS held_books, coalesce(s.lost_books, 0) AS lost_books,
			coalesce(s.gone_books, 0) AS gone_books, coalesce(s.widened_fingerprints, 0) AS widened_fingerprints
		FROM public.author_entity_merge_veto v
		LEFT JOIN public.author_entity_merge_veto_state s ON s.veto_id = v.id
		WHERE v.retired_at IS NULL ORDER BY v.id DESC`); err != nil {
		return nil, fmt.Errorf("reading the merge vetoes: %w", err)
	}
	for i := range v.Vetoes {
		w := &v.Vetoes[i]
		for side, out := range map[string]*[]MergeVetoEntity{"a": &w.SideA, "b": &w.SideB} {
			*out = []MergeVetoEntity{}
			if _, err := db.QueryContext(ctx, out, `
				SELECT e.id, e.display_name FROM public.author_entity e
				WHERE e.merged_into IS NULL AND e.id IN (SELECT ce.entity_id FROM (`+creditEntities+`) ce
					WHERE ce.source_fingerprint IN (`+widenedSide+`))
				ORDER BY e.display_name, e.id LIMIT ?2`, w.ID, side, mergeVetoSideEntities); err != nil {
				return nil, fmt.Errorf("reading the entities of veto %d's side %s: %w", w.ID, side, err)
			}
		}
	}
	unanchored, err := AuthorEntityVetoUnanchored(ctx, db)
	if err != nil {
		return nil, err
	}
	v.Unanchored = unanchored
	if v.Registrations, err = MergeRegistrations(ctx, db); err != nil {
		return nil, err
	}
	if v.Registrations == nil {
		v.Registrations = []MergeRegistration{}
	}
	return v, nil
}

// readDecisionSide reads one side of a decision: the entity of its member
// keys, the smallest display name its current credits of the member's own
// extractor version are selected to, its books. The name never crosses a
// member key: one raw fingerprint current under two versions may be
// selected to two names.
func readDecisionSide(ctx context.Context, db pg.DBI, decisionID int64, side string, out *MergeSideView) error {
	if _, err := db.QueryOneContext(ctx, out, `
		SELECT (SELECT a.entity_id FROM public.author_entity_merge_decision_member m
				JOIN public.author_entity_assignment a ON a.source_fingerprint = m.source_fingerprint
					AND a.extractor_version = m.extractor_version AND a.ordinal = m.ordinal
				WHERE m.decision_id = ?0 AND m.side = ?1 ORDER BY a.entity_id LIMIT 1) AS entity,
			coalesce((SELECT min(r.display_name) FROM public.author_entity_merge_decision_member m
				JOIN public.book_contributor_credit c ON c.source_fingerprint = m.source_fingerprint
					AND c.role = 'author'
				JOIN public.book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
					AND s.extractor_version = m.extractor_version
				JOIN public.book_contributor_credit_selection sel ON sel.credit_id = c.id AND sel.state = 'selected'
				JOIN public.contributor_normalization_result r ON r.id = sel.result_id
				WHERE m.decision_id = ?0 AND m.side = ?1), '') AS display_name,
			(SELECT count(DISTINCT b.book_id) FROM public.author_entity_merge_decision_book b
				WHERE b.decision_id = ?0 AND b.side = ?1) AS books`, decisionID, side); err != nil {
		return fmt.Errorf("reading side %s of decision %d: %w", side, decisionID, err)
	}
	return nil
}
