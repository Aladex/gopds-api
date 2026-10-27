package database

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"gopds-api/internal/authorentity"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Merge decisions as build input (migration 64, plan E4b).

// decisionCandidate is a swap candidate of two one-fingerprint sides.
func decisionCandidate(fill byte) authorentity.MergeCandidate {
	h := func(b byte) [32]byte {
		var out [32]byte
		copy(out[:], bytes.Repeat([]byte{b}, 32))
		return out
	}
	return authorentity.MergeCandidate{Kind: authorentity.CandidateSwap, Script: "Cyrl", ItemKind: authorentity.ItemEntity,
		MembersA:      []authorentity.FPKey{{Fingerprint: string(bytes.Repeat([]byte{fill}, 32)), ExtractorVersion: "v1"}},
		MembersB:      []authorentity.FPKey{{Fingerprint: string(bytes.Repeat([]byte{fill + 1}, 32)), ExtractorVersion: "v1"}},
		MembersSHA256: h(fill + 2), InputSHA256: h(fill + 3), Digest: h(fill)}
}

// refused runs query under a savepoint and returns its error.
func (f *authorSchemaFixture) refused(query string, params ...interface{}) error {
	f.t.Helper()
	f.exec(`SAVEPOINT refused`)
	_, err := f.tx.Exec(query, params...)
	f.exec(`ROLLBACK TO SAVEPOINT refused`)
	return err
}

// A decision is history: never deleted, changed only by its revoke and only
// once; its members never change. A revoke replaces it as the live row of
// its digest, with its members.
func TestMergeDecisionsAreHistory(t *testing.T) {
	f := withAuthorSchemaTx(t)
	ctx := context.Background()
	id, err := InsertMergeDecision(ctx, f.tx, &MergeDecisionInput{Candidate: decisionCandidate(0x10),
		Source: authorentity.MethodModel})
	require.NoError(t, err)
	restrict := func(err error) {
		t.Helper()
		var pgErr pg.Error
		require.True(t, errors.As(err, &pgErr), "%v", err)
		assert.Equal(t, "23001", pgErr.Field('C'), "%v", err)
	}
	restrict(f.refused(`DELETE FROM author_entity_merge_decision WHERE id = ?`, id))
	restrict(f.refused(`UPDATE author_entity_merge_decision SET kind = 'initials' WHERE id = ?`, id))
	restrict(f.refused(`UPDATE author_entity_merge_decision_member SET side = 'b' WHERE decision_id = ?`, id))
	restrict(f.refused(`DELETE FROM author_entity_merge_decision_member WHERE decision_id = ?`, id))
	// A second live decision of one digest is refused.
	f.exec(`SAVEPOINT duplicate`)
	_, err = InsertMergeDecision(ctx, f.tx, &MergeDecisionInput{Candidate: decisionCandidate(0x10), Source: "model"})
	assert.Error(t, err)
	f.exec(`ROLLBACK TO SAVEPOINT duplicate`)

	live, err := LoadMergeDecisions(ctx, f.tx)
	require.NoError(t, err)
	require.Len(t, live, 1)
	c := decisionCandidate(0x10)
	assert.Equal(t, authorentity.MergeDecision{ID: id, Digest: c.Digest, Kind: c.Kind, Script: c.Script,
		ItemKind: c.ItemKind, Outcome: authorentity.DecisionSame, MembersSHA256: c.MembersSHA256,
		MembersA: c.MembersA, MembersB: c.MembersB}, live[0])

	revoke, err := RevokeMergeDecision(ctx, f.tx, id, 0)
	require.NoError(t, err)
	_, err = RevokeMergeDecision(ctx, f.tx, id, 0)
	assert.ErrorIs(t, err, ErrMergeDecisionNotLive)
	_, err = RevokeMergeDecision(ctx, f.tx, revoke, 0)
	assert.ErrorIs(t, err, ErrMergeDecisionNotLive, "a revoke is not revoked")
	restrict(f.refused(`UPDATE author_entity_merge_decision SET revoked_at = now() WHERE id = ?`, id))
	live, err = LoadMergeDecisions(ctx, f.tx)
	require.NoError(t, err)
	require.Len(t, live, 1)
	assert.Equal(t, revoke, live[0].ID)
	assert.Equal(t, authorentity.DecisionRevoked, live[0].Outcome)
	assert.Equal(t, c.MembersA, live[0].MembersA)
	assert.Equal(t, c.MembersB, live[0].MembersB)
}

// The owning generation of a merged entity is its newest merge-log row:
// only a model one makes it revivable, with what it recorded.
func TestTheOwningGenerationIsTheNewestMerge(t *testing.T) {
	f := withAuthorSchemaTx(t)
	ctx := context.Background()
	x, s := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{x, s} {
		f.exec(`INSERT INTO author_entity (id, kind, display_name, sort_name, search_key)
			VALUES (?, 'person', 'Стивен Кинг', 'Кинг Стивен', 'стивен кинг')`, id)
	}
	f.exec(`UPDATE author_entity SET merged_into = ? WHERE id = ?`, s, x)
	logRow := func(method, evidence string) int64 {
		return f.returningID(`INSERT INTO author_entity_merge_log (from_id, to_id, method, evidence)
			VALUES (?, ?, ?, ?::jsonb) RETURNING id`, x, s, method, evidence)
	}
	model := logRow("model", `{"decision_ids": [7], "stratum": "swap"}`)
	fp := bytes.Repeat([]byte{0x41}, 32)
	f.exec(`INSERT INTO author_entity_merge_member (merge_log_id, source_fingerprint, extractor_version, ordinal)
		VALUES (?, ?, 'v1', 0)`, model, fp)
	f.exec(`INSERT INTO author_entity_merge_member (merge_log_id, credit_id, credit_ordinal) VALUES (?, 99, 0)`, model)
	state := func() map[uuid.UUID]authorentity.ModelMerge {
		st := authorentity.NewState()
		require.NoError(t, loadStoredModelMerges(ctx, f.tx, st))
		return st.ModelMerges
	}
	assert.Equal(t, map[uuid.UUID]authorentity.ModelMerge{x: {LogID: model, Entity: x, Into: s, DecisionIDs: []int64{7},
		Members: []authorentity.FPKey{{Fingerprint: string(fp), ExtractorVersion: "v1"}},
		Credits: []authorentity.CreditKey{{CreditID: 99}},
	}}, state())
	logRow("deterministic", `{}`)
	assert.Empty(t, state(), "a newer deterministic merge owns the entity")
	f.exec(`UPDATE author_entity SET merged_into = NULL WHERE id = ?`, x)
	logRow("model", `{"decision_ids": [8]}`)
	assert.Empty(t, state(), "a live entity is no merge")
}
