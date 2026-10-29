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
	// The sides are credited on a book each: what the decision is about.
	for _, fill := range []byte{0x10, 0x11} {
		f.displayCredit(f.snapshot(&snapshotSpec{book: f.book(), extractor: "v1"}), "author", 0, "fixture", fill)
	}
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
	restrict(f.refused(`UPDATE author_entity_merge_decision_book SET side = 'b' WHERE decision_id = ?`, id))
	restrict(f.refused(`DELETE FROM author_entity_merge_decision_book WHERE decision_id = ?`, id))
	assert.Equal(t, 2, f.count(`SELECT count(*) FROM author_entity_merge_decision_book WHERE decision_id = ?`, id),
		"one book a side")
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
		MembersA: c.MembersA, MembersB: c.MembersB, Withheld: MergeWithheldUnregistered}, live[0])

	// A model decision of a merge configuration (E4d writes it); an
	// administrator's revoke carries no configuration version.
	f.exec(`ALTER TABLE author_entity_merge_decision DISABLE TRIGGER author_entity_merge_decision_history`)
	mergeVersion, _, _ := mergeEvalReportOn(t, f.tx, 'a')
	f.exec(`UPDATE author_entity_merge_decision SET config_version = ? WHERE id = ?`, mergeVersion, id)
	f.exec(`ALTER TABLE author_entity_merge_decision ENABLE TRIGGER author_entity_merge_decision_history`)
	revoke, err := RevokeMergeDecision(ctx, f.tx, id, 0)
	require.NoError(t, err)
	var version *string
	_, err = f.tx.QueryOne(pg.Scan(&version), `SELECT config_version FROM author_entity_merge_decision WHERE id = ?`, revoke)
	require.NoError(t, err)
	assert.Nil(t, version, "the revoke kept the decision's configuration version")
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

// A decision a side of which is credited on no book is refused at insert;
// one written without its books (by hand, as before migration 65) cannot be
// revoked into a veto: the revoke is refused whole, the decision stays live
// and no veto is written (fail closed).
func TestADecisionWithoutBooksIsRefused(t *testing.T) {
	scratch := jobsDB(t)
	ctx := context.Background()
	setup, err := scratch.Begin()
	require.NoError(t, err)
	next := authorSchemaIDBase
	f := &authorSchemaFixture{t: t, tx: setup, next: &next}
	// Side a is credited; side b's fingerprint is credited nowhere.
	f.displayCredit(f.snapshot(&snapshotSpec{book: f.book(), extractor: "v1"}), "author", 0, "fixture", 0x20)
	f.exec(`SAVEPOINT refused`)
	_, err = InsertMergeDecision(ctx, setup, &MergeDecisionInput{Candidate: decisionCandidate(0x20),
		Source: authorentity.MethodModel})
	require.ErrorIs(t, err, ErrMergeDecisionSideUnrecorded)
	f.exec(`ROLLBACK TO SAVEPOINT refused`)
	// The same decision by hand, without its books.
	c := decisionCandidate(0x20)
	id := f.returningID(`INSERT INTO author_entity_merge_decision (digest, kind, script, item_kind, members_sha256,
		input_sha256, outcome, source) VALUES (?, ?, ?, ?, ?, ?, 'same', 'model') RETURNING id`,
		c.Digest[:], c.Kind, c.Script, c.ItemKind, c.MembersSHA256[:], c.InputSHA256[:])
	require.NoError(t, insertDecisionMembers(ctx, setup, id, c.MembersA, c.MembersB))
	f.displayCredit(f.snapshot(&snapshotSpec{book: f.book(), extractor: "v1"}), "author", 0, "fixture", 0x21)
	require.NoError(t, setup.Commit())

	_, err = RevokeMergeDecision(ctx, scratch, id, 0)
	require.ErrorIs(t, err, ErrMergeVetoSideUnrecorded)
	var live, vetoes int
	_, err = scratch.QueryOne(pg.Scan(&live), `SELECT count(*) FROM author_entity_merge_decision
		WHERE id = ? AND revoked_at IS NULL`, id)
	require.NoError(t, err)
	assert.Equal(t, 1, live, "the refused revoke revoked the decision")
	_, err = scratch.QueryOne(pg.Scan(&vetoes), `SELECT count(*) FROM author_entity_merge_veto`)
	require.NoError(t, err)
	assert.Zero(t, vetoes)
}

// E4b-3a review r3 (P2): a raw fingerprint current under two extractor
// versions — v1 on a book of component A, v2 on a book normalized into an
// unrelated component C. A decision whose member is (F, v1) is about the v1
// book only: the v2 book is not among its books.
func TestADecisionsBooksAreItsMembersVersion(t *testing.T) {
	f := withAuthorSchemaTx(t)
	ctx := context.Background()
	v1 := f.book()
	f.displayCredit(f.snapshot(&snapshotSpec{book: v1, extractor: "v1"}), "author", 0, "fixture", 0x30)
	v2 := f.book()
	f.displayCredit(f.snapshot(&snapshotSpec{book: v2, extractor: "v2"}), "author", 0, "fixture", 0x30)
	f.displayCredit(f.snapshot(&snapshotSpec{book: f.book(), extractor: "v1"}), "author", 0, "fixture", 0x31)
	id, err := InsertMergeDecision(ctx, f.tx, &MergeDecisionInput{Candidate: decisionCandidate(0x30),
		Source: authorentity.MethodModel})
	require.NoError(t, err)
	var books []int64
	_, err = f.tx.Query(&books, `SELECT book_id FROM author_entity_merge_decision_book
		WHERE decision_id = ? AND side = 'a' ORDER BY book_id`, id)
	require.NoError(t, err)
	assert.Equal(t, []int64{v1}, books, "the v2 book of another component is a book of the decision")
}

// E4b-3b review (P2): the display name of a decision's side never crosses
// its member key. One raw fingerprint is current under v1 and v2 and
// selected to two names; the side whose member is (F, v1) shows the v1
// name, though the v2 one sorts first.
func TestADecisionSideIsNamedByItsMembersVersion(t *testing.T) {
	f := withAuthorSchemaTx(t)
	f.registerPair("4", "structured_person", "normalizer-v1")
	ctx := context.Background()
	selectUnder := func(extractor, name string) {
		credit := f.displayCredit(f.snapshot(&snapshotSpec{book: f.book(), extractor: extractor}), "author", 0,
			"fixture", 0x30)
		row := autoRow(fingerprint(32, 0x30), "structured_person", "normalizer-v1")
		row.extractor, row.key, row.display = &extractor, keyFor(string(fingerprint(32, 0x30)), extractor,
			"normalizer-v1"), &name
		f.exec(selectionInsert, credit, fingerprint(32, 0x30), "selected", f.result(row), "automatic", nil, "4", nil,
			nil)
	}
	selectUnder("extractor-v1", "Омега Первая")
	selectUnder("extractor-v2", "Альфа Вторая")
	f.displayCredit(f.snapshot(&snapshotSpec{book: f.book(), extractor: "extractor-v1"}), "author", 0, "fixture", 0x31)
	c := decisionCandidate(0x30)
	c.MembersA[0].ExtractorVersion, c.MembersB[0].ExtractorVersion = "extractor-v1", "extractor-v1"
	_, err := InsertMergeDecision(ctx, f.tx, &MergeDecisionInput{Candidate: c, Source: authorentity.MethodModel})
	require.NoError(t, err)
	v, err := ReadMergeDecisions(ctx, f.tx)
	require.NoError(t, err)
	require.Len(t, v.Decisions, 1)
	assert.Equal(t, "Омега Первая", v.Decisions[0].SideA.DisplayName)
	assert.Empty(t, v.Registrations)
}
