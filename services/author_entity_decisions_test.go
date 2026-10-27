package services_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"gopds-api/database"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Merge decisions as build input (migration 64, plan E4b): a decision on a
// stored candidate merges its pair in the next pass, a revoke splits it with
// the absorbed ID revived, and a pass that needs its census and cannot seal
// it writes nothing.

// decideSwap inserts a same decision on the stored swap candidate, the way
// a merge run will, and returns its id.
func (c *entityCatalog) decideSwap() int64 {
	c.t.Helper()
	var id int64
	_, err := c.db.QueryOne(pg.Scan(&id), `INSERT INTO author_entity_merge_decision (digest, kind, script, item_kind,
			credit_id, credit_ordinal, members_sha256, input_sha256, outcome, source)
		SELECT digest, kind, script, item_kind, credit_id, credit_ordinal, members_sha256, input_sha256, 'same', 'model'
		FROM author_entity_merge_candidate WHERE kind = 'swap' RETURNING id`)
	require.NoError(c.t, err)
	c.exec(`INSERT INTO author_entity_merge_decision_member (decision_id, side, source_fingerprint, extractor_version,
			ordinal)
		SELECT d.id, m.side, m.source_fingerprint, m.extractor_version, m.ordinal
		FROM author_entity_merge_candidate_member m JOIN author_entity_merge_decision d ON d.digest = m.digest
		WHERE d.id = ?`, id)
	require.NoError(c.t, database.RecordMergeDecisionBooks(context.Background(), c.db, id))
	return id
}

// decideBetween inserts a same decision on the stored entity candidate
// whose sides hold the fingerprints of fills x and y, and returns its id.
func (c *entityCatalog) decideBetween(x, y byte) int64 {
	c.t.Helper()
	var digest []byte
	_, err := c.db.QueryOne(pg.Scan(&digest), `SELECT a.digest FROM author_entity_merge_candidate_member a
		JOIN author_entity_merge_candidate_member b ON b.digest = a.digest AND b.side <> a.side
		WHERE a.source_fingerprint = ? AND b.source_fingerprint = ?`,
		bytes.Repeat([]byte{x}, 32), bytes.Repeat([]byte{y}, 32))
	require.NoError(c.t, err)
	var id int64
	_, err = c.db.QueryOne(pg.Scan(&id), `INSERT INTO author_entity_merge_decision (digest, kind, script, item_kind,
			members_sha256, input_sha256, outcome, source)
		SELECT digest, kind, script, item_kind, members_sha256, input_sha256, 'same', 'model'
		FROM author_entity_merge_candidate WHERE digest = ? RETURNING id`, digest)
	require.NoError(c.t, err)
	c.exec(`INSERT INTO author_entity_merge_decision_member (decision_id, side, source_fingerprint, extractor_version,
			ordinal)
		SELECT ?, side, source_fingerprint, extractor_version, ordinal
		FROM author_entity_merge_candidate_member WHERE digest = ?`, id, digest)
	require.NoError(c.t, database.RecordMergeDecisionBooks(context.Background(), c.db, id))
	return id
}

// entityOf is the entity of a fingerprint's stored row.
func (c *entityCatalog) entityOf(fill byte) string {
	c.t.Helper()
	var id string
	_, err := c.db.QueryOne(pg.Scan(&id), `SELECT entity_id::text FROM author_entity_assignment
		WHERE source_fingerprint = ?`, bytes.Repeat([]byte{fill}, 32))
	require.NoError(c.t, err)
	return id
}

// mergedInto is an entity's merged_into, empty when it is live.
func (c *entityCatalog) mergedInto(id string) string {
	c.t.Helper()
	var into string
	_, err := c.db.QueryOne(pg.Scan(&into), `SELECT coalesce(merged_into::text, '') FROM author_entity WHERE id = ?`, id)
	require.NoError(c.t, err)
	return into
}

// A decision marks a build; the pass merges its pair by method model, logs
// the decision path and what the absorbed entity held, and records the
// decision applied — and writes no mark of its own. The next pass changes
// nothing and writes no state.
func TestADecisionIsBuildInputInTheDatabase(t *testing.T) {
	c := newEntityCatalog(t)
	small := c.seedCandidates()
	c.build()
	a, b := c.entityOf(0x61), c.entityOf(small)
	require.NotEqual(t, a, b)
	id := c.decideSwap()
	assert.Positive(t, c.marks(), "a decision is build input")

	rep := c.build()
	require.NotNil(t, rep.Model)
	assert.Equal(t, 1, rep.Model.Applied)
	assert.Equal(t, 1, rep.Model.Unions)
	assert.Equal(t, &database.MergeDecisionWrite{States: 1}, rep.DecisionWrite)
	assert.Equal(t, a, c.entityOf(small))
	assert.Equal(t, a, c.mergedInto(b))
	var method, rule string
	_, err := c.db.QueryOne(pg.Scan(&method, &rule), `SELECT method, rule FROM author_entity_assignment
		WHERE source_fingerprint = ?`, bytes.Repeat([]byte{small}, 32))
	require.NoError(t, err)
	assert.Equal(t, "model", method)
	assert.Equal(t, "model:swap", rule)
	var logID int64
	var evidence string
	_, err = c.db.QueryOne(pg.Scan(&logID, &method, &evidence), `SELECT id, method, evidence::text
		FROM author_entity_merge_log WHERE from_id = ? ORDER BY id DESC LIMIT 1`, b)
	require.NoError(t, err)
	assert.Equal(t, "model", method)
	var ev struct {
		DecisionIDs []int64 `json:"decision_ids"`
		Stratum     string  `json:"stratum"`
	}
	require.NoError(t, json.Unmarshal([]byte(evidence), &ev))
	assert.Equal(t, []int64{id}, ev.DecisionIDs)
	assert.Equal(t, "swap", ev.Stratum)
	assert.Equal(t, 1, c.count(`SELECT count(*) FROM author_entity_merge_member
		WHERE source_fingerprint = decode(repeat('62', 32), 'hex') AND credit_id IS NULL`))
	assert.Equal(t, 1, c.count(`SELECT count(*) FROM author_entity_merge_decision_state WHERE state = 'applied'`))
	assert.Zero(t, c.marks(), "the pass marked a pass to do")

	again := c.build()
	assert.Zero(t, again.Changes)
	assert.Nil(t, again.DecisionWrite, "an unchanged state is not rewritten")
	assert.Equal(t, 1, c.count(`SELECT count(*) FROM author_entity_merge_decision_state
		WHERE build_run_id = (SELECT min(id) + 1 FROM author_entity_build_run)`))
	assert.Equal(t, a, c.entityOf(small))
}

// A revoke marks a build, and the pass splits the pair: the absorbed entity
// is live again under its own ID. Its revival is recorded once.
func TestARevokeRevivesTheEntityInTheDatabase(t *testing.T) {
	c := newEntityCatalog(t)
	small := c.seedCandidates()
	c.build()
	b := c.entityOf(small)
	id := c.decideSwap()
	c.build()
	require.NotEqual(t, b, c.entityOf(small))
	_, err := database.RevokeMergeDecision(context.Background(), c.db, id, 0)
	require.NoError(t, err)
	assert.Positive(t, c.marks(), "a revoke is build input")

	rep := c.build()
	assert.Equal(t, b, c.entityOf(small))
	assert.Empty(t, c.mergedInto(b))
	assert.Equal(t, 1, rep.Model.Revivals.Exact)
	assert.Equal(t, 1, c.count(`SELECT count(*) FROM author_entity_revival WHERE outcome = 'exact'`))
	again := c.build()
	assert.Zero(t, again.Changes)
	require.NotNil(t, again.Model)
	assert.Zero(t, again.Model.Revivals.Exact, "a live entity is revived no more")
	assert.Equal(t, 1, c.count(`SELECT count(*) FROM author_entity_revival`))
}

// A chain A–B (d1), B–C (d2) with d1 revoked: B takes its ID back with
// C's members, and C, already merged into A, is merged into B again with
// d2 alone as its path. The next pass changes nothing.
func TestARevokeInsideAChainRepointsInTheDatabase(t *testing.T) {
	c := newEntityCatalog(t)
	for range 3 {
		c.credit(c.book("Роман"), 0x71, "Поселягин", "Владимир", "Геннадьевич")
	}
	for range 2 {
		c.credit(c.book("Повесть"), 0x72, "Владимир", "Поселягин", "Геннадьевич")
	}
	c.credit(c.book("Рассказ"), 0x73, "Геннадьевич", "Владимир", "Поселягин")
	c.build()
	a, b, x := c.entityOf(0x71), c.entityOf(0x72), c.entityOf(0x73)
	d1, d2 := c.decideBetween(0x71, 0x72), c.decideBetween(0x72, 0x73)
	c.build()
	require.Equal(t, a, c.mergedInto(b))
	require.Equal(t, a, c.mergedInto(x))
	_, err := database.RevokeMergeDecision(context.Background(), c.db, d1, 0)
	require.NoError(t, err)

	rep := c.build()
	assert.Equal(t, a, c.entityOf(0x71))
	assert.Equal(t, b, c.entityOf(0x72))
	assert.Equal(t, b, c.entityOf(0x73))
	assert.Empty(t, c.mergedInto(b))
	assert.Equal(t, b, c.mergedInto(x), "the revived entity no group inherited is merged again")
	assert.Equal(t, 1, rep.Model.Revivals.Lapsed["still_merged"])
	var evidence string
	_, err = c.db.QueryOne(pg.Scan(&evidence), `SELECT evidence::text FROM author_entity_merge_log
		WHERE from_id = ? ORDER BY id DESC LIMIT 1`, x)
	require.NoError(t, err)
	var ev struct {
		DecisionIDs []int64 `json:"decision_ids"`
	}
	require.NoError(t, json.Unmarshal([]byte(evidence), &ev))
	assert.Equal(t, []int64{d2}, ev.DecisionIDs)
	again := c.build()
	assert.Zero(t, again.Changes)
	assert.Nil(t, again.DecisionWrite)
}

// A revoked merge whose absorbed entity has no current credit left stays
// merged, lapsed no_live_member; its revival is recorded once, not once a
// pass.
func TestARevivalWithNoLiveMemberIsRecordedOnce(t *testing.T) {
	c := newEntityCatalog(t)
	small := c.seedCandidates()
	c.build()
	a, b := c.entityOf(0x61), c.entityOf(small)
	id := c.decideSwap()
	c.build()
	require.Equal(t, a, c.mergedInto(b))
	c.exec(`UPDATE book_metadata_snapshot SET is_current = false WHERE id IN (SELECT snapshot_id
		FROM book_contributor_credit WHERE source_fingerprint = ?)`, bytes.Repeat([]byte{small}, 32))
	_, err := database.RevokeMergeDecision(context.Background(), c.db, id, 0)
	require.NoError(t, err)

	rep := c.build()
	assert.Equal(t, a, c.mergedInto(b))
	assert.Equal(t, 1, rep.Model.Revivals.Lapsed["no_live_member"])
	assert.Equal(t, 1, c.count(`SELECT count(*) FROM author_entity_revival WHERE outcome = 'lapsed'
		AND reason = 'no_live_member'`))
	again := c.build()
	assert.Equal(t, 1, again.Model.Revivals.Lapsed["no_live_member"], "the pass still reports it")
	assert.Equal(t, 1, c.count(`SELECT count(*) FROM author_entity_revival`), "an unchanged revival is not rewritten")
}

// runUntilSettled steps the loop until the pass completes or fails.
func runUntilSettled(t *testing.T, c *entityCatalog, run int64) *database.AuthorEntityBuildRun {
	t.Helper()
	ctx := context.Background()
	loop := services.NewAuthorEntityBuildLoop(c.db, services.AuthorEntityBuildConfig{})
	for range 100 {
		_, err := loop.RunOnce(ctx)
		require.NoError(t, err)
		latest, _, err := database.LatestAuthorEntityBuilds(ctx, c.db)
		require.NoError(t, err)
		if latest.ID == run && latest.Status != database.AuthorEntityRunRunning && latest.Status != database.AuthorEntityRunPending {
			return latest
		}
	}
	t.Fatal("the pass did not settle")
	return nil
}

// With a decision live, a pass whose census cannot be sealed fails closed:
// it writes nothing, fails census_failed, and gives its marks back; the
// next pass does the work. Without decisions the census never holds a
// pass (TestTheCensusNeverHoldsTheBuild).
func TestADecisionPassFailsClosedWithoutItsCensus(t *testing.T) {
	for _, step := range []string{services.AuthorEntityCensusCardLoad, services.AuthorEntityCensusSeal} {
		t.Run(step, func(t *testing.T) {
			c := newEntityCatalog(t)
			small := c.seedCandidates()
			c.build()
			b := c.entityOf(small)
			id := c.decideSwap()
			c.build()
			merged := c.entityOf(small)
			_, err := database.RevokeMergeDecision(context.Background(), c.db, id, 0)
			require.NoError(t, err)
			rows := c.count(`SELECT count(*) FROM author_entity_merge_log`) +
				c.count(`SELECT count(*) FROM author_entity_revival`) +
				c.count(`SELECT count(*) FROM author_entity_merge_decision_state`)

			restore := services.SetAuthorEntityCensusHook(func(at string) error {
				if at == step {
					return errors.New("a census step that fails")
				}
				return nil
			})
			run, err := services.NewAuthorEntityAdmin(c.db).Start(context.Background(), 0)
			require.NoError(t, err)
			done := runUntilSettled(t, c, run.ID)
			restore()
			assert.Equal(t, database.AuthorEntityRunFailed, done.Status)
			require.NotNil(t, done.LastErrorClass)
			assert.Equal(t, services.AuthorEntityBuildErrorCensus, *done.LastErrorClass)
			assert.Equal(t, merged, c.entityOf(small), "the failed pass wrote")
			assert.Equal(t, merged, c.mergedInto(b))
			assert.Equal(t, rows, c.count(`SELECT count(*) FROM author_entity_merge_log`)+
				c.count(`SELECT count(*) FROM author_entity_revival`)+
				c.count(`SELECT count(*) FROM author_entity_merge_decision_state`))
			require.NoError(t, database.SettleAuthorAutopilotMarks(context.Background(), c.db))
			assert.Positive(t, c.marks(), "the failed pass kept its marks")

			c.build()
			assert.Equal(t, b, c.entityOf(small))
		})
	}
}

// A pass with no decision and no model merge has no decision step at all:
// its report carries no model section, no decision timing and no decision
// write, and the decision tables stay empty.
func TestAPassWithoutDecisionsHasNoDecisionStep(t *testing.T) {
	c := newEntityCatalog(t)
	c.seedCandidates()
	c.build()
	c.build()
	var raw string
	_, err := c.db.QueryOne(pg.Scan(&raw), `SELECT report::text FROM author_entity_build_run ORDER BY id DESC LIMIT 1`)
	require.NoError(t, err)
	var rep map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(raw), &rep))
	assert.NotContains(t, rep, "model")
	assert.NotContains(t, rep, "merge_decision_write")
	assert.NotContains(t, string(rep["timings"]), "decide_ms")
	for _, table := range []string{"author_entity_merge_decision_state", "author_entity_revival", "author_entity_merge_member"} {
		assert.Zero(t, c.count(`SELECT count(*) FROM `+table), table)
	}
}
