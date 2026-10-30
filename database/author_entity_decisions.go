package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"gopds-api/internal/authorentity"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// Merge decisions as entity build input (migration 64, plan E4b): the live
// decisions and the model merges a pass reads, and what it writes back —
// each decision's state and each revival's outcome — in its completing
// transaction.

// mergeDecisionRow is one live decision.
type mergeDecisionRow struct {
	ID            int64  `pg:"id"`
	Digest        []byte `pg:"digest"`
	Kind          string `pg:"kind"`
	Script        string `pg:"script"`
	ItemKind      string `pg:"item_kind"`
	CreditID      *int64 `pg:"credit_id"`
	CreditOrdinal *int   `pg:"credit_ordinal"`
	MembersSHA256 []byte `pg:"members_sha256"`
	Outcome       string `pg:"outcome"`
	ReportID      int64  `pg:"report_id,use_zero"`
	Withheld      string `pg:"withheld"`
}

// mergeMemberRow is one member key of a decision side.
type mergeMemberRow struct {
	DecisionID       int64  `pg:"decision_id"`
	Side             string `pg:"side"`
	Fingerprint      []byte `pg:"source_fingerprint"`
	ExtractorVersion string `pg:"extractor_version"`
	Ordinal          int    `pg:"ordinal,use_zero"`
}

// LoadMergeDecisions reads the live decisions — same ones and revokes — with
// their members. A model decision whose registration is missing or
// withdrawn, or whose script it blocks, is withheld with that reason
// (§4.7): the build lapses it whatever its candidate.
func LoadMergeDecisions(ctx context.Context, db pg.DBI) ([]authorentity.MergeDecision, error) {
	var rows []mergeDecisionRow
	if _, err := db.QueryContext(ctx, &rows, `
		SELECT d.id, d.digest, d.kind, d.script, d.item_kind, d.credit_id, d.credit_ordinal, d.members_sha256,
			d.outcome, coalesce(d.eval_report_id, 0) AS report_id,
			CASE WHEN d.source <> 'model' THEN ''
				WHEN r.id IS NULL THEN ?0
				WHEN r.withdrawn_at IS NOT NULL THEN ?1
				WHEN EXISTS (SELECT 1 FROM public.author_entity_merge_script_block b
					WHERE b.registration_id = r.id AND b.script = d.script) THEN ?2
				ELSE '' END AS withheld
		FROM public.author_entity_merge_decision d
		LEFT JOIN public.author_entity_merge_registration r ON r.id = d.registration_id AND r.kind = d.kind
			AND r.config_version = d.config_version
		WHERE d.revoked_at IS NULL ORDER BY d.id`,
		MergeWithheldUnregistered, MergeWithheldWithdrawn, MergeWithheldScriptBlocked); err != nil {
		return nil, fmt.Errorf("reading the merge decisions: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	ids := make([]int64, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	var members []mergeMemberRow
	if _, err := db.QueryContext(ctx, &members, `
		SELECT decision_id, side, source_fingerprint, extractor_version, ordinal
		FROM public.author_entity_merge_decision_member WHERE decision_id = ANY(?::bigint[])
		ORDER BY decision_id, side, source_fingerprint, extractor_version, ordinal`, pg.Array(ids)); err != nil {
		return nil, fmt.Errorf("reading the merge decision members: %w", err)
	}
	out := make([]authorentity.MergeDecision, len(rows))
	at := make(map[int64]int, len(rows))
	for i := range rows {
		r := &rows[i]
		d := authorentity.MergeDecision{ID: r.ID, Kind: r.Kind, Script: r.Script, ItemKind: r.ItemKind,
			Outcome: r.Outcome, ReportID: r.ReportID, Withheld: r.Withheld}
		copy(d.Digest[:], r.Digest)
		copy(d.MembersSHA256[:], r.MembersSHA256)
		if r.CreditID != nil {
			d.Credit = &authorentity.CreditKey{CreditID: *r.CreditID, Ordinal: *r.CreditOrdinal}
		}
		out[i], at[r.ID] = d, i
	}
	for _, m := range members {
		d := &out[at[m.DecisionID]]
		k := authorentity.FPKey{Fingerprint: string(m.Fingerprint), ExtractorVersion: m.ExtractorVersion, Ordinal: m.Ordinal}
		if m.Side == "a" {
			d.MembersA = append(d.MembersA, k)
		} else {
			d.MembersB = append(d.MembersB, k)
		}
	}
	return out, nil
}

// LoadMergeNotSame reads the digests of the merge candidates the production
// pair answered different (either of the two: a first answer of a merge
// eval, or an answer of a merge run): the build's conflict rule (§4.2)
// refuses a union that would join the two sides of such a pair. A
// cannot_tell, an invalid or a missing answer blocks nothing — it is no
// evidence of two people — and neither does a judge's different: a judge
// gates what is applied, not what other decisions may join.
func LoadMergeNotSame(ctx context.Context, db pg.DBI) ([][32]byte, error) {
	different := sha256.Sum256(llmreq.MergeCanonical(llmreq.MergeDifferent))
	var digests [][]byte
	if _, err := db.QueryContext(ctx, &digests, `
		SELECT DISTINCT i.source_fingerprint
		FROM public.author_llm_eval_item i
		JOIN public.author_llm_job j ON j.eval_item_id = i.id AND j.purpose = ?0 AND j.repeat_no = 0
			AND j.status = 'answered'
		JOIN public.author_llm_attempt a ON a.id = j.answer_attempt_id
		WHERE i.extractor_version = ?1 AND a.canonical_sha256 = ?2
		UNION
		SELECT j.source_fingerprint
		FROM public.author_llm_job j
		JOIN public.author_llm_attempt a ON a.id = j.answer_attempt_id
		WHERE j.purpose = ?3 AND j.slot IN (?4, ?5) AND j.status = 'answered' AND j.eval_item_id IS NULL
			AND j.extractor_version = ?1 AND a.canonical_sha256 = ?2
		ORDER BY 1`, string(models.AuthorLLMPurposeEvalCandidate), authorentity.MergeCandidateVersion,
		different[:], string(models.AuthorLLMPurposeMergeCheck), string(llmreq.SlotProductionA),
		string(llmreq.SlotProductionB)); err != nil {
		return nil, fmt.Errorf("reading the production pair's merge answers different: %w", err)
	}
	out := make([][32]byte, 0, len(digests))
	for _, d := range digests {
		var k [32]byte
		if copy(k[:], d) == len(k) {
			out = append(out, k)
		}
	}
	return out, nil
}

// MergeDecisionInput is a decision to insert: a candidate of a pass and
// where the decision came from.
type MergeDecisionInput struct {
	Candidate authorentity.MergeCandidate
	Source    string
	ReportID  int64
	ActorID   int64
}

// InsertMergeDecision inserts a same decision on a candidate with its
// members and the books it is about (RecordMergeDecisionBooks), in the
// caller's transaction, and returns its id. Merge runs insert theirs through the
// compare-and-insert of E4d; this is the plain insert under it.
func InsertMergeDecision(ctx context.Context, tx pg.DBI, in *MergeDecisionInput) (int64, error) {
	c := &in.Candidate
	var creditID *int64
	var creditOrdinal *int
	if c.ItemKind == authorentity.ItemBook {
		creditID, creditOrdinal = &c.CreditID, &c.CreditOrdinal
	}
	var id int64
	if _, err := tx.QueryOneContext(ctx, pg.Scan(&id), `
		INSERT INTO public.author_entity_merge_decision (digest, kind, script, item_kind, credit_id, credit_ordinal,
			members_sha256, input_sha256, outcome, source, eval_report_id, created_by_user_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'same', ?, nullif(?, 0), nullif(?, 0)) RETURNING id`,
		c.Digest[:], c.Kind, c.Script, c.ItemKind, creditID, creditOrdinal, c.MembersSHA256[:], c.InputSHA256[:],
		in.Source, in.ReportID, in.ActorID); err != nil {
		return 0, fmt.Errorf("inserting a merge decision: %w", err)
	}
	if err := insertDecisionMembers(ctx, tx, id, c.MembersA, c.MembersB); err != nil {
		return 0, err
	}
	if err := RecordMergeDecisionBooks(ctx, tx, id); err != nil {
		return 0, err
	}
	return id, nil
}

func insertDecisionMembers(ctx context.Context, tx pg.DBI, id int64, a, b []authorentity.FPKey) error {
	var sides, fps, versions []string
	var ordinals []int
	for i, keys := range [][]authorentity.FPKey{a, b} {
		side := []string{"a", "b"}[i]
		for _, k := range keys {
			sides = append(sides, side)
			fps = append(fps, hex.EncodeToString([]byte(k.Fingerprint)))
			versions = append(versions, k.ExtractorVersion)
			ordinals = append(ordinals, k.Ordinal)
		}
	}
	if len(sides) == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO public.author_entity_merge_decision_member (decision_id, side, source_fingerprint,
			extractor_version, ordinal)
		SELECT ?, s, decode(f, 'hex'), v, o FROM unnest(?::text[], ?::text[], ?::text[], ?::smallint[]) AS u(s, f, v, o)`,
		id, pg.Array(sides), pg.Array(fps), pg.Array(versions), pg.Array(ordinals)); err != nil {
		return fmt.Errorf("inserting the merge decision members: %w", err)
	}
	return nil
}

// ErrMergeDecisionNotLive is a revoke of a decision that is not a live same
// one.
var ErrMergeDecisionNotLive = errors.New("database: the merge decision is not a live same decision")

// RevokeMergeDecision is an administrator's revoke of a same decision, in
// the caller's transaction: the decision is revoked and a revoke of its
// digest, with its members, takes its place as the live row. It returns the
// revoke's id. The next pass splits the pair (§5.2), and the revoke's veto
// keeps it apart (§4.11).
func RevokeMergeDecision(ctx context.Context, db pg.DBI, decisionID, actor int64) (int64, error) {
	// The revoke, its members and its veto land together or not at all: a
	// caller without a transaction gets one.
	if conn, ok := db.(*pg.DB); ok {
		var id int64
		err := conn.RunInTransaction(ctx, func(tx *pg.Tx) error {
			var err error
			id, err = revokeMergeDecision(ctx, tx, decisionID, actor)
			return err
		})
		return id, err
	}
	return revokeMergeDecision(ctx, db, decisionID, actor)
}

func revokeMergeDecision(ctx context.Context, tx pg.DBI, decisionID, actor int64) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE public.author_entity_merge_decision SET revoked_at = now(), revoked_by_user_id = nullif(?, 0)
		WHERE id = ? AND outcome = 'same' AND revoked_at IS NULL`, actor, decisionID)
	if err != nil {
		return 0, fmt.Errorf("revoking the merge decision: %w", err)
	}
	if res.RowsAffected() == 0 {
		return 0, ErrMergeDecisionNotLive
	}
	var revokeID int64
	if _, err = tx.QueryOneContext(ctx, pg.Scan(&revokeID), `
		INSERT INTO public.author_entity_merge_decision (digest, config_version, kind, script, item_kind, credit_id,
			credit_ordinal, members_sha256, input_sha256, outcome, source, created_by_user_id)
		SELECT digest, NULL, kind, script, item_kind, credit_id, credit_ordinal, members_sha256,
			input_sha256, 'revoked', 'manual', nullif(?, 0)
		FROM public.author_entity_merge_decision WHERE id = ? RETURNING id`, actor, decisionID); err != nil {
		return 0, fmt.Errorf("inserting the revoke: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO public.author_entity_merge_decision_member (decision_id, side, source_fingerprint,
			extractor_version, ordinal)
		SELECT ?, side, source_fingerprint, extractor_version, ordinal
		FROM public.author_entity_merge_decision_member WHERE decision_id = ?`, revokeID, decisionID); err != nil {
		return 0, fmt.Errorf("copying the revoked members: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO public.author_entity_merge_decision_book (decision_id, side, book_id, source_fingerprint)
		SELECT ?, side, book_id, source_fingerprint
		FROM public.author_entity_merge_decision_book WHERE decision_id = ?`, revokeID, decisionID); err != nil {
		return 0, fmt.Errorf("copying the revoked decision's books: %w", err)
	}
	// The revoke is a durable veto (§4.11): no pass joins the pair again.
	if _, err = recordMergeVeto(ctx, tx, revokeID, actor); err != nil {
		return 0, err
	}
	return revokeID, nil
}

// loadStoredModelMerges reads the owning generation of every merged entity
// whose newest merge is a model union, with what it held.
func loadStoredModelMerges(ctx context.Context, db pg.DBI, s *authorentity.State) error {
	var logs []struct {
		ID       int64     `pg:"id"`
		From     uuid.UUID `pg:"from_id,type:uuid"`
		To       uuid.UUID `pg:"to_id,type:uuid"`
		Method   string    `pg:"method"`
		Evidence string    `pg:"evidence"`
	}
	if _, err := db.QueryContext(ctx, &logs, `
		SELECT DISTINCT ON (l.from_id) l.id, l.from_id, l.to_id, l.method, l.evidence::text AS evidence
		FROM public.author_entity_merge_log l
		JOIN public.author_entity e ON e.id = l.from_id AND e.merged_into IS NOT NULL
		ORDER BY l.from_id, l.id DESC`); err != nil {
		return fmt.Errorf("reading the owning merges: %w", err)
	}
	byLog := map[int64]uuid.UUID{}
	for _, l := range logs {
		if l.Method != authorentity.MethodModel {
			continue
		}
		var ev struct {
			DecisionIDs []int64 `json:"decision_ids"`
		}
		if err := json.Unmarshal([]byte(l.Evidence), &ev); err != nil {
			return fmt.Errorf("reading the decision path of merge %d: %w", l.ID, err)
		}
		s.ModelMerges[l.From] = authorentity.ModelMerge{LogID: l.ID, Entity: l.From, Into: l.To, DecisionIDs: ev.DecisionIDs}
		byLog[l.ID] = l.From
	}
	return loadModelMergeMembers(ctx, db, s, byLog)
}

// loadModelMergeMembers reads what each model merge's absorbed entity held,
// by merge-log row.
func loadModelMergeMembers(ctx context.Context, db pg.DBI, s *authorentity.State, byLog map[int64]uuid.UUID) error {
	if len(byLog) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(byLog))
	for id := range byLog {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	var members []struct {
		LogID            int64  `pg:"merge_log_id"`
		Fingerprint      []byte `pg:"source_fingerprint"`
		ExtractorVersion string `pg:"extractor_version"`
		Ordinal          *int   `pg:"ordinal"`
		CreditID         *int64 `pg:"credit_id"`
		CreditOrdinal    *int   `pg:"credit_ordinal"`
	}
	if _, err := db.QueryContext(ctx, &members, `
		SELECT merge_log_id, source_fingerprint, extractor_version, ordinal, credit_id, credit_ordinal
		FROM public.author_entity_merge_member WHERE merge_log_id = ANY(?::bigint[])
		ORDER BY merge_log_id, credit_id NULLS FIRST, credit_ordinal, source_fingerprint, extractor_version, ordinal`,
		pg.Array(ids)); err != nil {
		return fmt.Errorf("reading what the merged entities held: %w", err)
	}
	for _, m := range members {
		id := byLog[m.LogID]
		mm := s.ModelMerges[id]
		if m.CreditID != nil {
			mm.Credits = append(mm.Credits, authorentity.CreditKey{CreditID: *m.CreditID, Ordinal: *m.CreditOrdinal})
		} else {
			mm.Members = append(mm.Members, authorentity.FPKey{Fingerprint: string(m.Fingerprint),
				ExtractorVersion: m.ExtractorVersion, Ordinal: *m.Ordinal})
		}
		s.ModelMerges[id] = mm
	}
	return nil
}

// MergeDecisionWrite is what a pass wrote of its decisions: the states it
// changed and the revivals it recorded.
type MergeDecisionWrite struct {
	States   int `json:"states"`
	Revivals int `json:"revivals"`
	// VetoStates are the veto states written (E4b-3).
	VetoStates int `json:"veto_states,omitempty"`
}

// WriteMergeDecisionStates stores what the pass did with each decision and
// with each revivable entity, in the caller's transaction: a state is
// written when it changed, a revival when its outcome differs from the one
// last recorded for that merge. A pass that changed nothing writes nothing.
func WriteMergeDecisionStates(
	ctx context.Context, tx pg.DBI, runID int64, plan *authorentity.Plan,
) (MergeDecisionWrite, error) {
	var w MergeDecisionWrite
	if states := plan.DecisionStates(); len(states) > 0 {
		ids := make([]int64, len(states))
		st, reasons := make([]string, len(states)), make([]string, len(states))
		for i, s := range states {
			ids[i], st[i], reasons[i] = s.DecisionID, s.State, s.Reason
		}
		res, err := tx.ExecContext(ctx, `
			INSERT INTO public.author_entity_merge_decision_state (decision_id, build_run_id, state, reason)
			SELECT d, ?, s, r FROM unnest(?::bigint[], ?::text[], ?::text[]) AS u(d, s, r)
			ON CONFLICT (decision_id) DO UPDATE SET build_run_id = EXCLUDED.build_run_id, state = EXCLUDED.state,
				reason = EXCLUDED.reason
			WHERE (author_entity_merge_decision_state.state, author_entity_merge_decision_state.reason)
				IS DISTINCT FROM (EXCLUDED.state, EXCLUDED.reason)`,
			runID, pg.Array(ids), pg.Array(st), pg.Array(reasons))
		if err != nil {
			return w, fmt.Errorf("writing the merge decision states: %w", err)
		}
		w.States = res.RowsAffected()
	}
	revivals := plan.Revivals()
	if len(revivals) == 0 {
		return w, nil
	}
	var entities, outcomes, reasons []string
	var logs []int64
	var recorded, live, restored []int
	for _, r := range revivals {
		entities = append(entities, r.Entity.String())
		logs = append(logs, r.LogID)
		outcomes = append(outcomes, r.Outcome)
		reasons = append(reasons, r.Reason)
		recorded, live, restored = append(recorded, r.Recorded), append(live, r.Live), append(restored, r.Restored)
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO public.author_entity_revival (build_run_id, entity_id, merge_log_id, outcome, reason, recorded, live,
			restored)
		SELECT ?, u.e, u.l, u.o, u.r, u.rec, u.live, u.res
		FROM unnest(?::uuid[], ?::bigint[], ?::text[], ?::text[], ?::int[], ?::int[], ?::int[])
			AS u(e, l, o, r, rec, live, res)
		WHERE NOT EXISTS (
			SELECT 1 FROM (SELECT outcome, reason FROM public.author_entity_revival v
				WHERE v.entity_id = u.e AND v.merge_log_id = u.l ORDER BY v.build_run_id DESC LIMIT 1) last
			WHERE last.outcome = u.o AND last.reason = u.r)`,
		runID, pg.Array(entities), pg.Array(logs), pg.Array(outcomes), pg.Array(reasons), pg.Array(recorded),
		pg.Array(live), pg.Array(restored))
	if err != nil {
		return w, fmt.Errorf("writing the revivals: %w", err)
	}
	w.Revivals = res.RowsAffected()
	return w, nil
}
