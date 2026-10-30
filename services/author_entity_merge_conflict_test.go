package services_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The build's conflict rule needs an explicit different from the
// production pair (E4c): a pair blocks the component that would join its
// two sides only when production_a or production_b answered it different.
// The Asimov shape on prod — one pair of a big author answered cannot_tell
// by one participant, every other pair all four same — no longer holds the
// whole component, and a judge's different alone never did and does not.

// poselyagins are three orders of one name in one genre: the build pairs
// each two of them as swap candidates, a triangle.
var poselyagins = []mergePerson{
	{"Поселягин", "Владимир", "Геннадьевич", "Cyrl", "sf_action", 2, 0},
	{"Владимир", "Поселягин", "Геннадьевич", "Cyrl", "sf_action", 1, 0},
	{"Геннадьевич", "Владимир", "Поселягин", "Cyrl", "sf_action", 1, 0},
}

// The fills of the triangle's sides (seedMergePersons, 0x80 + i).
const (
	fillPoselyaginA = 0x80
	fillPoselyaginB = 0x81
	fillPoselyaginC = 0x82
)

// triangleStub answers as mergeStub, except that slot's model answers
// answer on the pair of sides A and C — one pair of the triangle.
func triangleStub(model, answer string) func(*fakeRequest) fakeReply {
	return func(req *fakeRequest) fakeReply {
		reply := mergeStub(req)
		if req.Model == model && strings.Contains(req.User, "Владимир Геннадьевич Поселягин") &&
			strings.Contains(req.User, "Владимир Поселягин Геннадьевич") {
			reply.Content = strings.ReplaceAll(reply.Content, `"`+llmreq.MergeSame+`"`, `"`+answer+`"`)
		}
		return reply
	}
}

// newTriangleFixture is newApplyFixture with the triangle in the catalog,
// merge1 answered by respond.
func newTriangleFixture(t *testing.T, respond func(*fakeRequest) fakeReply) *applyFixture {
	t.Helper()
	e := newLLMEnv(t)
	c := &entityCatalog{t: t, db: e.db, next: 1000}
	c.exec(`INSERT INTO author_acceptance_class
		(policy_version, decision_class, script, config_version, evidence_report_sha256, evidence_ref, source)
		VALUES ('4', 'structured_person', 'Cyrl', 'normalizer-v1', ?, 'internal/authornorm/policy_evidence/fixture.json',
		 'shipped')`, bytes.Repeat([]byte{0xee}, 32))
	c.seedMergeFixture()
	c.seedMergePersons(poselyagins, fillPoselyaginA)
	c.build()
	m := e.mergeConfig()
	e.fake.setRespond(respond)
	run, err := services.StartAuthorLLMMergeEval(context.Background(), e.db, m.version,
		models.AuthorLLMEvalModeMerge1, nil)
	require.NoError(t, err)
	stepMergeEval(t, e, m, run, stageEvalSizes, false)
	return &applyFixture{e: e, m: m, c: c, run: run, admin: services.NewAuthorEntityAdmin(e.db)}
}

// triangleStates are the states the latest build gave the triangle's
// decisions, as "state/reason", by the fills of their two sides.
func (f *applyFixture) triangleStates() map[[2]byte]string {
	f.c.t.Helper()
	var rows []struct {
		A, B  byte
		State string
	}
	_, err := f.e.db.Query(&rows, `SELECT min(get_byte(m.source_fingerprint, 0)) AS a,
			max(get_byte(m.source_fingerprint, 0)) AS b, s.state || '/' || s.reason AS state
		FROM author_entity_merge_decision d
		JOIN author_entity_merge_decision_member m ON m.decision_id = d.id
		JOIN author_entity_merge_decision_state s ON s.decision_id = d.id
		WHERE get_byte(m.source_fingerprint, 0) IN (?, ?, ?)
		GROUP BY d.id, s.state, s.reason`, fillPoselyaginA, fillPoselyaginB, fillPoselyaginC)
	require.NoError(f.c.t, err)
	out := map[[2]byte]string{}
	for _, r := range rows {
		out[[2]byte{r.A, r.B}] = r.State
	}
	return out
}

// The Asimov shape: production_b answered cannot_tell on A–C, every other
// answer of the triangle is same. A–B and B–C are applied as one entity
// with C — the cannot_tell pair blocks nothing — and A–C itself is never
// a decision.
func TestACannotTellPairNoLongerBlocksItsComponent(t *testing.T) {
	f := newTriangleFixture(t, triangleStub("deepseek-v4-pro", llmreq.MergeCannotTell))
	registration, _ := f.registerSwap()
	rep, err := f.admin.ApplyMergeEval(context.Background(), registration, 0)
	require.NoError(t, err)
	require.Positive(t, rep.Inserted)
	f.c.build()

	assert.Equal(t, map[[2]byte]string{
		{fillPoselyaginA, fillPoselyaginB}: "applied/",
		{fillPoselyaginB, fillPoselyaginC}: "applied/",
	}, f.triangleStates())
	assert.True(t, f.together(fillPoselyaginA, fillPoselyaginB))
	assert.True(t, f.together(fillPoselyaginA, fillPoselyaginC))
	notSame, err := database.LoadMergeNotSame(context.Background(), f.e.db)
	require.NoError(t, err)
	assert.Empty(t, notSame, "cannot_tell is no conflict")
}

// production_b's different on A–C still blocks the whole component: A–B
// and B–C are conflicts (not_same) and the three stay apart.
func TestADifferentPairStillBlocksItsComponent(t *testing.T) {
	f := newTriangleFixture(t, triangleStub("deepseek-v4-pro", llmreq.MergeDifferent))
	registration, _ := f.registerSwap()
	_, err := f.admin.ApplyMergeEval(context.Background(), registration, 0)
	require.NoError(t, err)
	f.c.build()

	assert.Equal(t, map[[2]byte]string{
		{fillPoselyaginA, fillPoselyaginB}: "conflict/not_same",
		{fillPoselyaginB, fillPoselyaginC}: "conflict/not_same",
	}, f.triangleStates())
	assert.False(t, f.together(fillPoselyaginA, fillPoselyaginB))
	assert.False(t, f.together(fillPoselyaginA, fillPoselyaginC))
}

// A judge's different alone — judge_b on A–C, the production pair and
// judge_a same — blocks nothing: A–C is not applied (the judges did not
// both confirm it), but A–B and B–C are, and the three are one entity.
func TestAJudgeOnlyDifferentDoesNotBlockItsComponent(t *testing.T) {
	f := newTriangleFixture(t, triangleStub("claude-opus-5", llmreq.MergeDifferent))
	registration, _ := f.registerSwap()
	_, err := f.admin.ApplyMergeEval(context.Background(), registration, 0)
	require.NoError(t, err)
	f.c.build()

	assert.Equal(t, map[[2]byte]string{
		{fillPoselyaginA, fillPoselyaginB}: "applied/",
		{fillPoselyaginB, fillPoselyaginC}: "applied/",
	}, f.triangleStates())
	assert.True(t, f.together(fillPoselyaginA, fillPoselyaginC))
	notSame, err := database.LoadMergeNotSame(context.Background(), f.e.db)
	require.NoError(t, err)
	assert.Empty(t, notSame, "a judge's different is no conflict")
}
