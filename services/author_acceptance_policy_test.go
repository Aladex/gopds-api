package services

import (
	"context"
	"strings"
	"testing"

	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/logging"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	logrustest "github.com/sirupsen/logrus/hooks/test" //nolint:depguard // tests assert on the structured log entries
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Automatic acceptance end to end on a scratch database: the start-up pass
// selects the credits waiting for a registered pair, the worker picks up what a
// registration covers, new credits are selected by the normal pipeline, and a
// rerun changes nothing. resetPipeline empties the class table, so each case
// registers exactly the pairs it is about.

func doyle(t *testing.T) authornorm.SourceValue {
	return source(t, "first", "Arthur", "last", "Doyle")
}

func dostoevsky(t *testing.T) authornorm.SourceValue {
	return source(t, "first", "Фёдор", "last", "Достоевский")
}

func TestAuthorAcceptancePolicy(t *testing.T) {
	s := localWorkerDB(t)
	cases := []struct {
		name string
		fn   func(t *testing.T, f *workerFixture)
	}{
		{"the start-up pass selects the waiting credits of a registered pair only", acceptanceStartUpPassSelectsWaitingCredits},
		{"the worker selects credits a registration covers", acceptanceWorkerSelectsCoveredCredits},
		{"new credits of a registered pair are selected by the pipeline", acceptanceNewCreditsAreSelected},
		{"the pass pages through every input", acceptancePassPagesThroughEveryInput},
		{"an input that turned ambiguous after its result is not accepted", acceptanceSkipsAnInputThatTurnedAmbiguous},
		{"a late duplicate credit without a resolution blocks the input too", acceptanceSkipsAnInputWithAnUnresolvedLateDuplicate},
		{"a duplicate credit after the pass is reconciled to review", acceptanceSelectionsAreReconciledWhenADuplicateArrives},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resetPipeline(t, s)
			c.fn(t, &workerFixture{t: t, db: s})
		})
	}
}

// waitingCredits persists the books and drains the worker under the empty
// policy: every eligible credit ends unresolved with policy_not_registered.
func (f *workerFixture) waitingCredits(credits ...database.ExtractionCredit) []int64 {
	f.t.Helper()
	ids := f.persistBook(credits...)
	f.drain(f.worker(nil))
	for _, id := range ids {
		s := f.selection(id)
		if s != nil && s.State == models.CreditSelectionUnresolved {
			require.Equal(f.t, models.UnresolvedPolicyNotRegistered, *s.UnresolvedReason)
		}
	}
	return ids
}

// The start-up pass selects the waiting credits of a registered pair and
// nothing else, logs only a count and a version, and a second start changes
// nothing.
func acceptanceStartUpPassSelectsWaitingCredits(t *testing.T, f *workerFixture) {
	ctx := context.Background()
	hook := logrustest.NewLocal(logging.GetLogger())
	defer hook.Reset()
	cyrillic := append(f.waitingCredits(author(tolstoy(t))), f.waitingCredits(author(tolstoy(t)))...)
	latin := f.waitingCredits(author(doyle(t)))
	initials := f.waitingCredits(author(initialed(t)))
	require.Equal(t, models.CreditSelectionReview, f.selection(initials[0]).State)
	f.registerPair("2", authornorm.ClassStructuredPerson, authornorm.ScriptCyrillic)

	pass := NewAuthorAcceptancePass(f.db)
	assert.Equal(t, "acceptance", pass.Name())
	require.NoError(t, pass.Run(ctx))

	for _, id := range cyrillic {
		s := f.selection(id)
		assert.Equal(t, models.CreditSelectionSelected, s.State)
		assert.Equal(t, models.CreditSelectionAutomatic, *s.Basis)
		assert.Equal(t, "2", *s.PolicyVersion)
	}
	assert.Equal(t, models.CreditSelectionUnresolved, f.selection(latin[0]).State, "Latin is another pair")
	assert.Equal(t, models.CreditSelectionReview, f.selection(initials[0]).State)

	audits := map[int64]int{}
	for _, id := range append(append(append([]int64{}, cyrillic...), latin...), initials...) {
		audits[id] = f.audits(id)
	}
	require.NoError(t, pass.Run(ctx))
	for id, n := range audits {
		assert.Equal(t, n, f.audits(id), "a second start changed credit %d", id)
	}

	applied := eventsNamed(hook, AuthorMetadataEventAcceptanceApplied)
	require.Len(t, applied, 1, "only the pass that selected something is logged")
	assert.Equal(t, 2, applied[0].Data["count"])
	assert.Equal(t, 2, applied[0].Data["policy_version"])
	assert.Equal(t, "acceptance", applied[0].Data["stage"])
	for _, e := range hook.AllEntries() {
		line, _ := e.String()
		for _, name := range []string{"Толстой", "Лев", "Doyle", "Arthur", "Петров"} {
			assert.False(t, strings.Contains(line, name), "a name reached the log: %s", e.Message)
		}
	}
}

// A registration that arrived while a worker decided its batch under the
// previous version, or after a pass stopped, is finished by the worker's next
// batch.
func acceptanceWorkerSelectsCoveredCredits(t *testing.T, f *workerFixture) {
	cyrillic := f.waitingCredits(author(tolstoy(t)))
	latin := f.waitingCredits(author(doyle(t)))
	f.registerPair("2", authornorm.ClassStructuredPerson, authornorm.ScriptCyrillic)

	w := f.worker(nil)
	report, err := w.RunOnce(context.Background())
	require.NoError(t, err)
	assert.Zero(t, report.Claimed)
	assert.Equal(t, 1, report.Accepted)
	s := f.selection(cyrillic[0])
	assert.Equal(t, models.CreditSelectionSelected, s.State)
	assert.Equal(t, "2", *s.PolicyVersion)
	assert.Equal(t, models.CreditSelectionUnresolved, f.selection(latin[0]).State)

	before := f.audits(cyrillic[0])
	report, err = w.RunOnce(context.Background())
	require.NoError(t, err)
	assert.Zero(t, report.Accepted)
	assert.Equal(t, before, f.audits(cyrillic[0]))
}

// After a registration the normal pipeline selects the pair's new credits:
// a new input under the newest version, and a late credit of a completed
// input from its stored result.
func acceptanceNewCreditsAreSelected(t *testing.T, f *workerFixture) {
	f.waitingCredits(author(tolstoy(t)))
	f.registerPair("2", authornorm.ClassStructuredPerson, authornorm.ScriptCyrillic)
	f.registerPair("3", authornorm.ClassStructuredPerson, authornorm.ScriptLatin)

	late := f.persistBook(author(tolstoy(t)))
	fresh := f.persistBook(author(chekhov(t)), author(doyle(t)))
	f.drain(f.worker(nil))

	for _, id := range append(late, fresh...) {
		s := f.selection(id)
		require.NotNil(t, s, "credit %d", id)
		assert.Equal(t, models.CreditSelectionSelected, s.State, "credit %d", id)
		assert.Equal(t, "3", *s.PolicyVersion, "credit %d is decided under the newest version", id)
	}
}

func acceptancePassPagesThroughEveryInput(t *testing.T, f *workerFixture) {
	credits := f.waitingCredits(author(tolstoy(t)), author(chekhov(t)), author(dostoevsky(t)))
	credits = append(credits, f.waitingCredits(author(chekhov(t)))...)
	f.registerPair("2", authornorm.ClassStructuredPerson, authornorm.ScriptCyrillic)

	selected, err := ApplyAcceptancePolicy(context.Background(), f.db, authornorm.NormalizerVersion, 1)
	require.NoError(t, err)
	assert.Equal(t, 4, selected)
	for _, id := range credits {
		assert.Equal(t, models.CreditSelectionSelected, f.selection(id).State, "credit %d", id)
	}

	_, err = ApplyAcceptancePolicy(context.Background(), f.db, authornorm.NormalizerVersion, 0)
	assert.ErrorIs(t, err, ErrInvalidAuthorAcceptanceConfig)
}

// duplicateRace builds review finding B1's input: a plain credit whose result
// was stored and resolved under the empty policy, then a credit of the same
// fingerprint carrying duplicate_component (a repeated empty first-name
// child). With resolveLate the completion race resolves the late credit from
// the stale plain result too, so both wait with policy_not_registered.
func duplicateRace(t *testing.T, f *workerFixture, resolveLate bool) (first, late []int64) {
	t.Helper()
	plain := source(t, "first", "Иван", "last", "Петров")
	repeated := source(t, "first", "Иван", "first", "", "last", "Петров")
	fp := authornorm.SourceFingerprint(plain)
	first = f.waitingCredits(author(plain))
	stored := f.selection(first[0])
	require.Equal(t, models.CreditSelectionUnresolved, stored.State)
	late = f.persistBook(author(repeated))
	if resolveLate {
		ctx := context.Background()
		r, err := database.LoadLocalResult(ctx, f.db, *stored.ResultID)
		require.NoError(t, err)
		d, err := authornorm.ProductionAcceptancePolicy().Decide(&r)
		require.NoError(t, err)
		require.NoError(t, f.db.RunInTransaction(ctx, func(tx *pg.Tx) error {
			_, resolveErr := database.ResolveCredits(ctx, tx, fp[:], workerExtractor,
				&database.AutomaticOutcome{ResultID: *stored.ResultID, DecisionClass: r.DecisionClass, Decision: d})
			return resolveErr
		}))
		require.Equal(t, models.CreditSelectionUnresolved, f.selection(late[0]).State, "the race left the plain reading")
	}
	f.registerPair("2", authornorm.ClassStructuredPerson, authornorm.ScriptCyrillic)
	return first, late
}

// B1: the stored parse is structured_person, but the input now carries a
// duplicated child, so the input is ambiguous — the local reconciliation would
// send it to review. Neither the batch pass nor the start-up stage (workers
// off: nothing would repair it afterwards) may select it.
func acceptanceSkipsAnInputThatTurnedAmbiguous(t *testing.T, f *workerFixture) {
	first, late := duplicateRace(t, f, true)
	credits := append(append([]int64{}, first...), late...)
	audits := map[int64]int{}
	for _, id := range credits {
		audits[id] = f.audits(id)
	}

	selected, err := ApplyAcceptancePolicy(context.Background(), f.db, authornorm.NormalizerVersion, 1)
	require.NoError(t, err)
	assert.Zero(t, selected)
	require.NoError(t, NewAuthorAcceptancePass(f.db).Run(context.Background()))
	for _, id := range credits {
		s := f.selection(id)
		assert.Equal(t, models.CreditSelectionUnresolved, s.State, "credit %d", id)
		assert.Equal(t, audits[id], f.audits(id), "credit %d was written", id)
	}
}

// The variant without the race: the late flagged credit has no resolution at
// all, and the earlier plain credit still waits. The input is ambiguous all
// the same, so the earlier credit is not selected either.
func acceptanceSkipsAnInputWithAnUnresolvedLateDuplicate(t *testing.T, f *workerFixture) {
	first, late := duplicateRace(t, f, false)
	require.NoError(t, NewAuthorAcceptancePass(f.db).Run(context.Background()))
	assert.Equal(t, models.CreditSelectionUnresolved, f.selection(first[0]).State)
	assert.Nil(t, f.selection(late[0]))
}

// A duplicated credit that arrives after the pass selected an input is the
// local worker's reconciliation case, exactly as for its own selections: the
// pass's automatic selections go to review with the rest of the input.
func acceptanceSelectionsAreReconciledWhenADuplicateArrives(t *testing.T, f *workerFixture) {
	plain := source(t, "first", "Иван", "last", "Петров")
	repeated := source(t, "first", "Иван", "first", "", "last", "Петров")
	first := f.waitingCredits(author(plain))
	f.registerPair("2", authornorm.ClassStructuredPerson, authornorm.ScriptCyrillic)
	require.NoError(t, NewAuthorAcceptancePass(f.db).Run(context.Background()))
	require.Equal(t, models.CreditSelectionSelected, f.selection(first[0]).State)

	late := f.persistBook(author(repeated))
	f.drain(f.worker(nil))
	for _, id := range append(first, late...) {
		assert.Equal(t, models.CreditSelectionReview, f.selection(id).State, "credit %d", id)
	}
}
