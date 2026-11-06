package services_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"strings"
	"sync"
	"testing"

	"gopds-api/database"
	"gopds-api/internal/authornorm/llmreq"
	"gopds-api/models"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The build's conflict rule needs an explicit different from the
// production pair (E4c): a pair blocks the component that would join its
// two sides only when production_a or production_b answered it different,
// and neither answered it same on any answer (E4f): a pair the production
// side split on is no evidence of two people. The Asimov shape on prod —
// one pair of a big author answered cannot_tell by one participant, every
// other pair all four same — no longer holds the whole component, and a
// judge's different alone never did and does not.

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
	return triangleAnswers(map[string][]string{model: {answer}})
}

// triangleAnswers answers as mergeStub, except on the pair of sides A and C:
// each model of answers gives its answers there in turn, the last one to
// every later ask (a repeat asks again).
func triangleAnswers(answers map[string][]string) func(*fakeRequest) fakeReply {
	var mu sync.Mutex
	asked := map[string]int{}
	return func(req *fakeRequest) fakeReply {
		reply := mergeStub(req)
		turns, ok := answers[req.Model]
		if !ok || !strings.Contains(req.User, "Владимир Геннадьевич Поселягин") ||
			!strings.Contains(req.User, "Владимир Поселягин Геннадьевич") {
			return reply
		}
		mu.Lock()
		answer := turns[min(asked[req.Model], len(turns)-1)]
		asked[req.Model]++
		mu.Unlock()
		reply.Content = strings.ReplaceAll(reply.Content, `"`+llmreq.MergeSame+`"`, `"`+answer+`"`)
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

// A different on A–C that no production answer contradicts still keeps A
// and C apart — both production participants different, or one different
// and the other cannot_tell, or a production different only when the pair
// was asked again, or a repeat's different in a different majority after a
// production same: of the two decisions that together would join them, the
// first in order (B–C, the lower id) applies and the second, which would
// bridge the pair, is held (not_same, E4c) — never the whole component.
func TestADifferentPairStillBlocksItsComponent(t *testing.T) {
	d, s, c := llmreq.MergeDifferent, llmreq.MergeSame, llmreq.MergeCannotTell
	for name, answers := range map[string]map[string][]string{
		"both different":            {"gpt-6-luna": {d}, "deepseek-v4-pro": {d}},
		"different and cannot_tell": {"gpt-6-luna": {c}, "deepseek-v4-pro": {d}},
		"a production different only on a repeat": {"gpt-6-luna": {c, d}, "deepseek-v4-pro": {c},
			"gpt-6-sol": {c}, "claude-opus-5": {c}},
		"a repeat different in a different majority": {"gpt-6-luna": {s, d}, "deepseek-v4-pro": {c, d},
			"gpt-6-sol": {d}, "claude-opus-5": {d}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newTriangleFixture(t, triangleAnswers(answers))
			registration, _ := f.registerSwap()
			_, err := f.admin.ApplyMergeEval(context.Background(), registration, 0)
			require.NoError(t, err)
			f.c.build()

			assert.Equal(t, map[[2]byte]string{
				{fillPoselyaginA, fillPoselyaginB}: "conflict/not_same",
				{fillPoselyaginB, fillPoselyaginC}: "applied/",
			}, f.triangleStates())
			assert.True(t, f.together(fillPoselyaginB, fillPoselyaginC))
			assert.False(t, f.together(fillPoselyaginA, fillPoselyaginB))
			assert.False(t, f.together(fillPoselyaginA, fillPoselyaginC))
		})
	}
}

// heldTriangle is the triangle with A–B and B–C applied, and a different the
// production pair gave on A–C after that: what an unattended merge run leaves
// for the next pass.
func heldTriangle(t *testing.T) *applyFixture {
	t.Helper()
	c := llmreq.MergeCannotTell
	f := newTriangleFixture(t, triangleAnswers(map[string][]string{"gpt-6-luna": {c}, "deepseek-v4-pro": {c}}))
	registration, _ := f.registerSwap()
	_, err := f.admin.ApplyMergeEval(context.Background(), registration, 0)
	require.NoError(t, err)
	f.c.build()
	require.Equal(t, "applied/", f.triangleStates()[[2]byte{fillPoselyaginA, fillPoselyaginB}])
	require.True(t, f.together(fillPoselyaginA, fillPoselyaginC))
	different := sha256.Sum256(llmreq.MergeCanonical(llmreq.MergeDifferent))
	cannotTell := sha256.Sum256(llmreq.MergeCanonical(c))
	f.c.exec(`ALTER TABLE author_llm_attempt DISABLE TRIGGER USER`)
	f.c.exec(`UPDATE author_llm_attempt SET canonical_sha256 = ? WHERE canonical_sha256 = ?`, different[:], cannotTell[:])
	f.c.exec(`ALTER TABLE author_llm_attempt ENABLE TRIGGER USER`)
	notSame, err := database.LoadMergeNotSame(context.Background(), f.e.db)
	require.NoError(t, err)
	require.Len(t, notSame, 1)
	return f
}

// A not-same answer that would take an applied decision apart stops the
// autopilot's pass before it writes: the join stands, the marks go back and
// the pass names the decision (D5).
func TestAnAutopilotPassLeavesAnAppliedDecisionToAnAdministrator(t *testing.T) {
	f := heldTriangle(t)
	ctx := context.Background()
	before := f.triangleStates()
	run, err := database.StartAuthorEntityBuild(ctx, f.e.db, 0)
	require.NoError(t, err)
	worked, err := services.NewAuthorEntityBuildLoop(f.e.db, services.AuthorEntityBuildConfig{}).RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, worked)

	held, err := database.ReadAuthorAutopilotDecisionsHeld(ctx, f.e.db, services.AuthorEntityBuildErrorDecisionsHeld)
	require.NoError(t, err)
	assert.Equal(t, run.ID, held.RunID)
	assert.Len(t, held.Decisions, 1)
	assert.Equal(t, before, f.triangleStates(), "a held pass wrote a state")
	assert.True(t, f.together(fillPoselyaginA, fillPoselyaginC), "a held pass took the join apart")
}

// An administrator's pass is the person looking: it goes through and its
// report lists what it took apart.
func TestAnAdministratorsPassTakesTheDecisionApartAndSaysSo(t *testing.T) {
	f := heldTriangle(t)
	ctx := context.Background()
	f.c.exec(`INSERT INTO auth_user (id, password, is_superuser, username, email, date_joined)
		VALUES (41, 'x', true, 'looking', 'looking@example.test', now())`)
	run, err := database.StartAuthorEntityBuild(ctx, f.e.db, 41)
	require.NoError(t, err)
	_, err = services.NewAuthorEntityBuildLoop(f.e.db, services.AuthorEntityBuildConfig{}).RunOnce(ctx)
	require.NoError(t, err)

	var count int
	_, err = f.e.db.QueryOne(pg.Scan(&count), `SELECT jsonb_array_length(report->'model'->'applied_not_same')
		FROM author_entity_build_run WHERE id = ? AND status = 'completed'`, run.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	assert.False(t, f.together(fillPoselyaginA, fillPoselyaginC))
}

// Both judges' different on A–C, the production pair cannot_tell, blocks
// nothing either: only a production participant's different is evidence
// of two people, however many judges join it.
func TestTheJudgesDifferentAloneDoesNotBlockItsComponent(t *testing.T) {
	f := newTriangleFixture(t, triangleAnswers(map[string][]string{"gpt-6-luna": {llmreq.MergeCannotTell},
		"deepseek-v4-pro": {llmreq.MergeCannotTell}, "gpt-6-sol": {llmreq.MergeDifferent},
		"claude-opus-5": {llmreq.MergeDifferent}}))
	registration, _ := f.registerSwap()
	_, err := f.admin.ApplyMergeEval(context.Background(), registration, 0)
	require.NoError(t, err)
	f.c.build()

	assert.True(t, f.together(fillPoselyaginA, fillPoselyaginC))
	notSame, err := database.LoadMergeNotSame(context.Background(), f.e.db)
	require.NoError(t, err)
	assert.Empty(t, notSame, "the judges' different is no conflict")
}

// The variant shape on prod (E4f): one production participant answered A–C
// different and the other same — or both different the first time and one
// of them same when the pair was asked again. The production side split
// on the pair, so it blocks nothing: A–B and B–C apply and the three are
// one entity, and A–C itself is never a decision.
func TestADifferentAProductionAnswerContradictsBlocksNothing(t *testing.T) {
	for name, answers := range map[string]map[string][]string{
		"the other same": {"deepseek-v4-pro": {llmreq.MergeDifferent}},
		"same on a repeat": {"gpt-6-luna": {llmreq.MergeDifferent},
			"deepseek-v4-pro": {llmreq.MergeDifferent, llmreq.MergeSame}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newTriangleFixture(t, triangleAnswers(answers))
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
			assert.Empty(t, notSame, "a different a production answer contradicts is no conflict")
		})
	}
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

// A pair the production side split on still blocks when its different
// answers outnumber its same ones (E4f), every answer counted — the
// judges' and every repeat's too: three of four participants different
// against one same keep A and C apart, and so do a judge's repeat that
// tips a tie. A tie itself blocks nothing.
func TestASplitPairOutnumberedByDifferentStillBlocks(t *testing.T) {
	d, s := llmreq.MergeDifferent, llmreq.MergeSame
	for name, c := range map[string]struct {
		answers map[string][]string
		blocks  bool
	}{
		"judges different": {map[string][]string{"gpt-6-luna": {s}, "deepseek-v4-pro": {d}, "gpt-6-sol": {d},
			"claude-opus-5": {d}}, true},
		"a judge's repeat tips a tie": {map[string][]string{"gpt-6-luna": {s}, "deepseek-v4-pro": {d},
			"gpt-6-sol": {d}, "claude-opus-5": {s, d}}, true},
		"a tie": {map[string][]string{"gpt-6-luna": {s}, "deepseek-v4-pro": {d}, "gpt-6-sol": {d},
			"claude-opus-5": {s}}, false},
	} {
		t.Run(name, func(t *testing.T) {
			f := newTriangleFixture(t, triangleAnswers(c.answers))
			registration, _ := f.registerSwap()
			_, err := f.admin.ApplyMergeEval(context.Background(), registration, 0)
			require.NoError(t, err)
			f.c.build()

			ac := "applied/"
			if c.blocks {
				ac = "conflict/not_same"
			}
			assert.Equal(t, map[[2]byte]string{
				{fillPoselyaginA, fillPoselyaginB}: ac,
				{fillPoselyaginB, fillPoselyaginC}: "applied/",
			}, f.triangleStates())
			assert.Equal(t, !c.blocks, f.together(fillPoselyaginA, fillPoselyaginC))
		})
	}
}
