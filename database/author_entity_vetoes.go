package database

import (
	"context"
	"errors"
	"fmt"

	"gopds-api/internal/authorentity"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// The durable veto of a revoke (migration 64, plan §4.11, E4b-3): the
// revoke records each side's (book, raw fingerprint) at revoke time; every
// build reads the live vetoes and the current credits of their recorded
// books, keeps the sides apart at every level and stores what it found
// (author_entity_merge_veto_state). Only an administrator releases a veto:
// a retire, or a re-anchor that retires it and records the two entities
// that are its sides now.

// ErrMergeVetoNotLive is a retire or re-anchor of a veto that is retired
// or does not exist.
var ErrMergeVetoNotLive = errors.New("database: the merge veto is not live")

// ErrMergeVetoSides is a re-anchor whose two entities are one, or one of
// which holds no fingerprint of its widened side.
var ErrMergeVetoSides = errors.New("database: the entities are not the veto's two sides")

// currentAuthorCredits are the author credits of the current snapshots,
// selected or not: what continuity reads of a book.
//
// #nosec G101 -- a query naming credit columns, not a credential
const currentAuthorCredits = `
	SELECT s.book_id, c.id AS credit_id, c.source_fingerprint, s.extractor_version
	FROM public.book_contributor_credit c
	JOIN public.book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
	WHERE c.role = 'author'`

// LoadMergeVetoes reads the live vetoes with their members, and the raw
// fingerprints of every current author credit of each recorded book.
func LoadMergeVetoes(ctx context.Context, db pg.DBI) ([]authorentity.MergeVeto, map[int64][]string, error) {
	var vetoes []struct {
		ID       int64  `pg:"id"`
		ItemKind string `pg:"item_kind"`
		BookID   *int64 `pg:"book_id"`
	}
	if _, err := db.QueryContext(ctx, &vetoes, `
		SELECT id, item_kind, book_id FROM public.author_entity_merge_veto WHERE retired_at IS NULL ORDER BY id`); err != nil {
		return nil, nil, fmt.Errorf("reading the merge vetoes: %w", err)
	}
	if len(vetoes) == 0 {
		return nil, nil, nil
	}
	var members []struct {
		VetoID      int64  `pg:"veto_id"`
		Side        string `pg:"side"`
		BookID      int64  `pg:"book_id"`
		Fingerprint []byte `pg:"source_fingerprint"`
	}
	if _, err := db.QueryContext(ctx, &members, `
		SELECT m.veto_id, m.side, m.book_id, m.source_fingerprint
		FROM public.author_entity_merge_veto_member m
		JOIN public.author_entity_merge_veto v ON v.id = m.veto_id AND v.retired_at IS NULL
		ORDER BY m.veto_id, m.side, m.book_id, m.source_fingerprint`); err != nil {
		return nil, nil, fmt.Errorf("reading the merge veto members: %w", err)
	}
	var books []struct {
		BookID      int64  `pg:"book_id"`
		Fingerprint []byte `pg:"source_fingerprint"`
	}
	if _, err := db.QueryContext(ctx, &books, `
		SELECT DISTINCT c.book_id, c.source_fingerprint FROM (`+currentAuthorCredits+`) c
		WHERE c.book_id IN (SELECT m.book_id FROM public.author_entity_merge_veto_member m
			JOIN public.author_entity_merge_veto v ON v.id = m.veto_id AND v.retired_at IS NULL)
		ORDER BY c.book_id, c.source_fingerprint`); err != nil {
		return nil, nil, fmt.Errorf("reading the current credits of the vetoes' books: %w", err)
	}
	out := make([]authorentity.MergeVeto, len(vetoes))
	at := map[int64]int{}
	for i, v := range vetoes {
		out[i] = authorentity.MergeVeto{ID: v.ID, ItemKind: v.ItemKind}
		if v.BookID != nil {
			out[i].BookID = *v.BookID
		}
		at[v.ID] = i
	}
	for _, m := range members {
		v := &out[at[m.VetoID]]
		v.Members = append(v.Members, authorentity.VetoMember{Side: m.Side, BookID: m.BookID, Fingerprint: string(m.Fingerprint)})
	}
	current := map[int64][]string{}
	for _, b := range books {
		current[b.BookID] = append(current[b.BookID], string(b.Fingerprint))
	}
	return out, current, nil
}

// ErrMergeVetoSideUnrecorded is a revoke whose veto would record no book
// for a side — the decision has no recorded books for it (a decision written
// without them, as before migration 65) — and nothing else names one. Such
// a veto could hold nothing apart, so the revoke is refused whole (fail
// closed) instead of writing an empty side.
var ErrMergeVetoSideUnrecorded = errors.New("database: a side of the revoke's veto has no recorded book")

// ErrMergeDecisionSideUnrecorded is a decision a side of which is credited
// on no current book: there is nothing it is about.
var ErrMergeDecisionSideUnrecorded = errors.New("database: a side of the merge decision is credited on no book")

// vetoSides is how many sides a veto, or a decision's books, record: a and b.
const vetoSides = 2

// RecordMergeDecisionBooks writes the books a decision is about, in its
// transaction (migration 65): per side, every (book, raw fingerprint) of the
// current author credits of the side's member keys — their fingerprint under
// their extractor version — and a book item's side a is its book and credit
// alone. A side credited on no book refuses the decision
// (ErrMergeDecisionSideUnrecorded). InsertMergeDecision calls it; a test
// that inserts a decision row by hand calls it after.
func RecordMergeDecisionBooks(ctx context.Context, tx pg.DBI, decisionID int64) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO public.author_entity_merge_decision_book (decision_id, side, book_id, source_fingerprint)
		SELECT DISTINCT d.id, m.side, s.book_id, c.source_fingerprint
		FROM public.author_entity_merge_decision d
		JOIN public.author_entity_merge_decision_member m ON m.decision_id = d.id
		JOIN public.book_contributor_credit c ON c.source_fingerprint = m.source_fingerprint AND c.role = 'author'
		JOIN public.book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current
			AND s.extractor_version = m.extractor_version
		WHERE d.id = ?0 AND (d.item_kind = 'entity' OR m.side = 'b')
		UNION
		SELECT d.id, 'a', s.book_id, c.source_fingerprint
		FROM public.author_entity_merge_decision d
		JOIN public.book_contributor_credit c ON c.id = d.credit_id
		JOIN public.book_metadata_snapshot s ON s.id = c.snapshot_id
		WHERE d.id = ?0 AND d.item_kind = 'book'`, decisionID); err != nil {
		return fmt.Errorf("recording the merge decision's books: %w", err)
	}
	var sides int
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&sides), `
		SELECT count(DISTINCT side) FROM public.author_entity_merge_decision_book WHERE decision_id = ?`,
		decisionID); err != nil {
		return fmt.Errorf("checking the merge decision's books: %w", err)
	}
	if sides != vetoSides {
		return ErrMergeDecisionSideUnrecorded
	}
	return nil
}

// recordMergeVeto records the veto of a revoke, in its transaction (§4.11):
// per side, the books its decision was about (migration 65, copied to the
// revoke) and every current author credit of the side's member
// fingerprints. A book rewritten since the decision stays recorded, so the
// next build finds it lost and widens the side to the rewritten credits; a
// book that carried a member fingerprint only in a snapshot already
// replaced before the decision is not recorded. A book item's side a is its
// recorded book alone. A revoke whose decision has no books recorded for a
// side refuses (ErrMergeVetoSideUnrecorded): fail closed, never a guess.
func recordMergeVeto(ctx context.Context, tx pg.DBI, revokeID, actor int64) (int64, error) {
	var recorded int
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&recorded), `
		SELECT count(DISTINCT side) FROM public.author_entity_merge_decision_book WHERE decision_id = ?`,
		revokeID); err != nil {
		return 0, fmt.Errorf("reading the revoked decision's books: %w", err)
	}
	if recorded != vetoSides {
		return 0, ErrMergeVetoSideUnrecorded
	}
	var vetoID int64
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&vetoID), `
		INSERT INTO public.author_entity_merge_veto (revoke_decision_id, item_kind, book_id, created_by_user_id)
		SELECT d.id, d.item_kind, CASE WHEN d.item_kind = 'book' THEN (SELECT s.book_id
				FROM public.book_contributor_credit c JOIN public.book_metadata_snapshot s ON s.id = c.snapshot_id
				WHERE c.id = d.credit_id) END, nullif(?, 0)
		FROM public.author_entity_merge_decision d WHERE d.id = ? RETURNING id`, actor, revokeID); err != nil {
		return 0, fmt.Errorf("recording the veto: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO public.author_entity_merge_veto_member (veto_id, side, book_id, source_fingerprint)
		SELECT ?0, side, book_id, source_fingerprint
		FROM public.author_entity_merge_decision_book WHERE decision_id = ?1
		UNION
		SELECT ?0, m.side, c.book_id, c.source_fingerprint
		FROM public.author_entity_merge_decision_member m
		JOIN public.author_entity_merge_veto v ON v.id = ?0
		JOIN (`+currentAuthorCredits+`) c ON c.source_fingerprint = m.source_fingerprint
		WHERE m.decision_id = ?1 AND (v.item_kind = 'entity' OR m.side = 'b')`, vetoID, revokeID); err != nil {
		return 0, fmt.Errorf("recording the veto's members: %w", err)
	}
	return vetoID, nil
}

// RetireMergeVeto retires a live veto, in the caller's transaction: the
// administrator's decision to let its sides be joined again.
func RetireMergeVeto(ctx context.Context, tx pg.DBI, vetoID, actor int64) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE public.author_entity_merge_veto SET retired_at = now(), retired_by_user_id = nullif(?, 0)
		WHERE id = ? AND retired_at IS NULL`, actor, vetoID)
	if err != nil {
		return fmt.Errorf("retiring the merge veto: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrMergeVetoNotLive
	}
	return nil
}

// creditEntities are the current author credits with the entity each is
// in: its credit row's, else its fingerprint row's.
const creditEntities = `
	SELECT c.book_id, c.source_fingerprint, coalesce(ca.entity_id, fa.entity_id) AS entity_id
	FROM (` + currentAuthorCredits + `) c
	LEFT JOIN public.author_entity_credit_assignment ca ON ca.credit_id = c.credit_id
	LEFT JOIN public.author_entity_assignment fa ON fa.source_fingerprint = c.source_fingerprint
		AND fa.extractor_version = c.extractor_version AND fa.ordinal = 0`

// widenedSide are the fingerprints of a veto's side as a build widens it:
// the recorded ones, and every current one of a recorded book none of whose
// current credits carries a recorded fingerprint (§4.11).
const widenedSide = `
	SELECT m.source_fingerprint FROM public.author_entity_merge_veto_member m WHERE m.veto_id = ?0 AND m.side = ?1
	UNION
	SELECT c.source_fingerprint FROM (` + currentAuthorCredits + `) c
	WHERE c.book_id IN (SELECT m.book_id FROM public.author_entity_merge_veto_member m
		WHERE m.veto_id = ?0 AND m.side = ?1
			AND NOT EXISTS (SELECT 1 FROM (` + currentAuthorCredits + `) h
				WHERE h.book_id = m.book_id AND h.source_fingerprint = m.source_fingerprint))`

// ReanchorMergeVeto re-anchors a live veto on two current entities, in the
// caller's transaction (§4.11): each must hold a fingerprint of the
// corresponding widened side, and they must differ. The old veto is
// retired and a new one, replacing it, records the two entities' current
// (book, fingerprint). It returns the new veto's id.
func ReanchorMergeVeto(ctx context.Context, tx pg.DBI, vetoID int64, sideA, sideB uuid.UUID, actor int64) (int64, error) {
	if sideA == sideB {
		return 0, ErrMergeVetoSides
	}
	for side, entity := range map[string]uuid.UUID{authorentity.VetoSideA: sideA, authorentity.VetoSideB: sideB} {
		var holds bool
		if _, err := tx.QueryOneContext(ctx, pg.Scan(&holds), `
			SELECT EXISTS (SELECT 1 FROM (`+creditEntities+`) e
				WHERE e.entity_id = ?2 AND e.source_fingerprint IN (`+widenedSide+`))`,
			vetoID, side, entity); err != nil {
			return 0, fmt.Errorf("checking the re-anchor's side %s: %w", side, err)
		}
		if !holds {
			return 0, ErrMergeVetoSides
		}
	}
	if err := RetireMergeVeto(ctx, tx, vetoID, actor); err != nil {
		return 0, err
	}
	var newID int64
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&newID), `
		INSERT INTO public.author_entity_merge_veto (revoke_decision_id, item_kind, book_id, created_by_user_id,
			replaces_veto_id)
		SELECT revoke_decision_id, item_kind, book_id, nullif(?, 0), id FROM public.author_entity_merge_veto
		WHERE id = ? RETURNING id`, actor, vetoID); err != nil {
		return 0, fmt.Errorf("recording the re-anchored veto: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO public.author_entity_merge_veto_member (veto_id, side, book_id, source_fingerprint)
		SELECT DISTINCT ?0, CASE WHEN e.entity_id = ?1 THEN 'a' ELSE 'b' END, e.book_id, e.source_fingerprint
		FROM (`+creditEntities+`) e WHERE e.entity_id IN (?1, ?2)`, newID, sideA, sideB); err != nil {
		return 0, fmt.Errorf("recording the re-anchored veto's members: %w", err)
	}
	return newID, nil
}

// WriteMergeVetoStates upserts what the pass found of each live veto, in
// its completing transaction; an unchanged state is not rewritten. It
// returns how many rows it wrote.
func WriteMergeVetoStates(ctx context.Context, tx pg.DBI, runID int64, plan *authorentity.Plan) (int, error) {
	n := 0
	for _, s := range plan.VetoStates() {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO public.author_entity_merge_veto_state (veto_id, build_run_id, state, held_books, lost_books,
				gone_books, widened_fingerprints)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (veto_id) DO UPDATE SET build_run_id = EXCLUDED.build_run_id, state = EXCLUDED.state,
				held_books = EXCLUDED.held_books, lost_books = EXCLUDED.lost_books, gone_books = EXCLUDED.gone_books,
				widened_fingerprints = EXCLUDED.widened_fingerprints
			WHERE (author_entity_merge_veto_state.state, author_entity_merge_veto_state.held_books,
				author_entity_merge_veto_state.lost_books, author_entity_merge_veto_state.gone_books,
				author_entity_merge_veto_state.widened_fingerprints)
				IS DISTINCT FROM (EXCLUDED.state, EXCLUDED.held_books, EXCLUDED.lost_books, EXCLUDED.gone_books,
				EXCLUDED.widened_fingerprints)`,
			s.VetoID, runID, s.State, s.HeldBooks, s.LostBooks, s.GoneBooks, s.WidenedFingerprints)
		if err != nil {
			return n, fmt.Errorf("writing the veto states: %w", err)
		}
		n += res.RowsAffected()
	}
	return n, nil
}

// AuthorEntityVetoUnanchored reports a live veto the last completed build
// found unanchored: until an administrator retires or re-anchors it, no
// merge verdict is to be asked or inserted (E4d reads it, §4.11).
func AuthorEntityVetoUnanchored(ctx context.Context, db pg.DBI) (bool, error) {
	var unanchored bool
	if _, err := db.QueryOneContext(ctx, pg.Scan(&unanchored), `
		SELECT EXISTS (SELECT 1 FROM public.author_entity_merge_veto v
			JOIN public.author_entity_merge_veto_state s ON s.veto_id = v.id
			WHERE v.retired_at IS NULL AND s.state = ?)`, authorentity.VetoUnanchored); err != nil {
		return false, fmt.Errorf("reading whether a veto is unanchored: %w", err)
	}
	return unanchored, nil
}
