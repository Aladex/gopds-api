package database

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"gopds-api/internal/authornorm"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The acceptance policy store: registrations under cumulative versions, and
// the selection of the credits a registration covers. It runs on a scratch
// database of its own, emptied of the shipped registrations per case
// (withStoreTx), so the counts see only these fixtures.

func TestAcceptancePolicyStore(t *testing.T) {
	storeDB = jobsDB(t)
	t.Cleanup(func() { storeDB = nil })
	cases := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"VersionsAreCumulative", acceptanceVersionsAreCumulative},
		{"SelectionTakesOnlyCreditsWaitingForTheRegisteredPair", acceptanceSelectionTakesOnlyWaitingCredits},
		{"SelectionIsBoundedPerCall", acceptanceSelectionIsBoundedPerCall},
		{"InputDuplicateReading", acceptanceInputDuplicateReading},
		{"DemotionTakesBackOnlyAutomaticSelections", acceptanceDemotionTakesBackOnlyAutomaticSelections},
	}
	for _, c := range cases {
		t.Run(c.name, c.fn)
	}
}

func dostoevskySource(t *testing.T) authornorm.SourceValue {
	return nameValue(t,
		nameComponent(authornorm.ComponentFirst, "Фёдор"),
		nameComponent(authornorm.ComponentLast, "Достоевский"))
}

func doyleSource(t *testing.T) authornorm.SourceValue {
	return nameValue(t,
		nameComponent(authornorm.ComponentFirst, "Arthur"),
		nameComponent(authornorm.ComponentLast, "Doyle"))
}

// waitingInput persists books whose author credits share one structured
// source, stores
// the source's local result and resolves the credits under the empty policy:
// unresolved with policy_not_registered.
func (f *authorSchemaFixture) waitingInput(
	t *testing.T, v authornorm.SourceValue, books int,
) (r authornorm.Result, resultID int64, credits []int64) {
	t.Helper()
	for range books {
		_, ids := f.persistBook(authorCredit(v))
		credits = append(credits, ids[0])
	}
	r = normalizedAs(t, v, authornorm.ClassStructuredPerson)
	resultID = f.storeResult(&r)
	f.resolve(&r, decide(t, emptyPolicy(t, f), &r, resultID))
	return r, resultID, credits
}

// Version 1 is the empty policy; a version's policy holds every pair
// registered in it or in an earlier version, for its own configuration.
func acceptanceVersionsAreCumulative(t *testing.T) {
	ctx := context.Background()
	f := withStoreTx(t)
	cyrillic, _, _ := f.waitingInput(t, structuredSource(t), 1)
	latin, _, _ := f.waitingInput(t, doyleSource(t), 1)

	current, err := CurrentAcceptancePolicyVersion(ctx, f.tx)
	require.NoError(t, err)
	assert.Equal(t, authornorm.BasePolicyVersion, current)

	f.registerClass("2", "structured_person", authornorm.NormalizerVersion)
	latinProof := sha256.Sum256([]byte("latin"))
	f.exec(`INSERT INTO author_acceptance_class
		(policy_version, decision_class, script, config_version, evidence_report_sha256, evidence_ref, source)
		VALUES ('3', 'structured_person', 'Latn', ?, ?, 'internal/authornorm/policy_evidence/fixture.json', 'shipped')`,
		authornorm.NormalizerVersion, latinProof[:])
	// Another configuration's registration is not this policy's.
	f.registerClass("4", "placeholder", "authornorm-local-v0")
	current, err = CurrentAcceptancePolicyVersion(ctx, f.tx)
	require.NoError(t, err)
	assert.Equal(t, 4, current)

	decided := func(version int, r *authornorm.Result) authornorm.Decision {
		t.Helper()
		p, loadErr := LoadAcceptancePolicy(ctx, f.tx, version, authornorm.NormalizerVersion)
		require.NoError(t, loadErr)
		d, decideErr := p.Decide(r)
		require.NoError(t, decideErr)
		return d
	}
	assert.False(t, decided(1, &cyrillic).Selected(), "version 1 stays the empty policy")
	assert.True(t, decided(2, &cyrillic).Selected())
	assert.False(t, decided(2, &latin).Selected(), "the Latin pair arrived in version 3")
	assert.True(t, decided(3, &cyrillic).Selected())
	assert.True(t, decided(3, &latin).Selected())

	p, err := LoadCurrentAcceptancePolicy(ctx, f.tx, authornorm.NormalizerVersion)
	require.NoError(t, err)
	assert.Equal(t, 4, p.Version())
	d, err := p.Decide(&latin)
	require.NoError(t, err)
	assert.Equal(t, latinProof, d.EvidenceSHA256)
}

// The pass selects the credits still waiting for the registered pair and
// nothing else: not another script, not a credit an override decides, not an
// administrator's decision, not an open review with a selectable proposal.
func acceptanceSelectionTakesOnlyWaitingCredits(t *testing.T) {
	ctx := context.Background()
	f := withStoreTx(t)
	admin := f.user()

	waiting, waitingID, waitingCredits := f.waitingInput(t, structuredSource(t), 2)
	_, _, latinCredits := f.waitingInput(t, doyleSource(t), 1)

	// Dostoevsky: one credit under a credit override, one under an
	// administrator's "leave unresolved", one in review with the selectable
	// parse as its proposal (a duplicate-component reading joined later).
	_, dIDs := f.persistBook(authorCredit(dostoevskySource(t)))
	_, leftIDs := f.persistBook(authorCredit(dostoevskySource(t)))
	_, reviewIDs := f.persistBook(authorCredit(dostoevskySource(t)))
	d := normalizedAs(t, dostoevskySource(t), authornorm.ClassStructuredPerson)
	dID := f.storeResult(&d)
	dfp := d.SourceFingerprint[:]
	f.resolve(&d, decide(t, emptyPolicy(t, f), &d, dID))
	creditOverride := f.creditOverride(dIDs[0], f.manualResultFor(dfp, admin), dfp, admin)
	require.NoError(t, ResolveCreditSelection(ctx, f.tx, dIDs[0], nil))
	f.exec(`UPDATE book_contributor_credit_selection
		SET unresolved_reason = 'review_left_unresolved', decided_by_user_id = ? WHERE credit_id = ?`, admin, leftIDs[0])
	require.NoError(t, ResolveCreditSelection(ctx, f.tx, reviewIDs[0], &AutomaticOutcome{
		ResultID: dID, DecisionClass: authornorm.ClassDuplicateComponent,
		Decision: authornorm.Decision{Outcome: authornorm.OutcomeReview, PolicyVersion: authornorm.BasePolicyVersion},
	}))

	// A fingerprint override over a waiting-looking input of its own.
	tw := nameValue(t, nameComponent(authornorm.ComponentFirst, "Марк"), nameComponent(authornorm.ComponentLast, "Твен"))
	_, _, twCredits := f.waitingInput(t, tw, 1)
	twResult := normalizedAs(t, tw, authornorm.ClassStructuredPerson)
	twfp := twResult.SourceFingerprint[:]
	f.fingerprintOverride(twfp, f.manualResultFor(twfp, admin), admin)
	require.NoError(t, ResolveCreditSelection(ctx, f.tx, twCredits[0], nil))

	untouched := append(append(append([]int64{}, latinCredits...), dIDs[0], leftIDs[0], reviewIDs[0]), twCredits...)
	before := map[int64]models.BookContributorCreditSelection{}
	audits := map[int64]int{}
	for _, id := range untouched {
		before[id] = *f.selection(id)
		audits[id] = f.auditCount(id)
	}

	f.registerClass("2", "structured_person", authornorm.NormalizerVersion)
	policy, err := LoadCurrentAcceptancePolicy(ctx, f.tx, authornorm.NormalizerVersion)
	require.NoError(t, err)
	candidates, err := AcceptanceCandidates(ctx, f.tx, authornorm.NormalizerVersion, policy.Version(), nil, 100)
	require.NoError(t, err)
	require.Len(t, candidates, 1, "only Tolstoy's input still waits for the registered pair")
	assert.Equal(t, waitingID, candidates[0].ResultID)

	decision, err := policy.Decide(&waiting)
	require.NoError(t, err)
	out := &AutomaticOutcome{ResultID: waitingID, DecisionClass: waiting.DecisionClass, Decision: decision}
	page, err := SelectRegisteredCredits(ctx, f.tx, &candidates[0], out, 0, 100)
	require.NoError(t, err)
	assert.Equal(t, 2, page.Selected)

	for _, id := range waitingCredits {
		s := f.selection(id)
		assert.Equal(t, models.CreditSelectionSelected, s.State)
		assert.Equal(t, models.CreditSelectionAutomatic, *s.Basis)
		assert.Equal(t, "2", *s.PolicyVersion)
		assert.Equal(t, waitingID, *s.ResultID)
		assert.Equal(t, 2, f.auditCount(id), "one audited transition")
	}
	for _, id := range untouched {
		assert.Equal(t, before[id], *f.selection(id), "credit %d changed", id)
		assert.Equal(t, audits[id], f.auditCount(id), "credit %d gained an audit row", id)
	}
	assert.Equal(t, creditOverride, *f.selection(dIDs[0]).OverrideID)

	// A second pass finds nothing and changes nothing.
	again, err := AcceptanceCandidates(ctx, f.tx, authornorm.NormalizerVersion, policy.Version(), nil, 100)
	require.NoError(t, err)
	assert.Empty(t, again)
	page, err = SelectRegisteredCredits(ctx, f.tx, &candidates[0], out, 0, 100)
	require.NoError(t, err)
	assert.Zero(t, page.Selected)
	for _, id := range waitingCredits {
		assert.Equal(t, 2, f.auditCount(id))
	}

	// An outcome that is not a selection of the candidate's result is refused.
	_, err = SelectRegisteredCredits(ctx, f.tx, &candidates[0], &AutomaticOutcome{
		ResultID: waitingID, DecisionClass: waiting.DecisionClass,
		Decision: authornorm.Decision{Outcome: authornorm.OutcomeUnresolved, PolicyVersion: 2},
	}, 0, 100)
	assert.ErrorIs(t, err, ErrInvalidAutomaticOutcome)
}

// Review finding N1: one call — one transaction of the pass — takes at most
// limit credits of an input, in ID order after the given one, however many
// credits the input has; consecutive pages cover every credit once.
func acceptanceSelectionIsBoundedPerCall(t *testing.T) {
	ctx := context.Background()
	f := withStoreTx(t)
	waiting, waitingID, credits := f.waitingInput(t, structuredSource(t), 5)
	f.registerClass("2", "structured_person", authornorm.NormalizerVersion)
	policy, err := LoadCurrentAcceptancePolicy(ctx, f.tx, authornorm.NormalizerVersion)
	require.NoError(t, err)
	d, err := policy.Decide(&waiting)
	require.NoError(t, err)
	out := &AutomaticOutcome{ResultID: waitingID, DecisionClass: waiting.DecisionClass, Decision: d}
	candidate := &AcceptanceCandidate{
		SourceFingerprint: waiting.SourceFingerprint[:], ExtractorVersion: waiting.ExtractorVersion, ResultID: waitingID,
	}

	var pages []AcceptancePage
	var after int64
	for range 4 {
		page, selectErr := SelectRegisteredCredits(ctx, f.tx, candidate, out, after, 2)
		require.NoError(t, selectErr)
		pages = append(pages, page)
		after = page.Last
	}
	assert.Equal(t, []AcceptancePage{
		{Listed: 2, Selected: 2, SelectedIDs: credits[0:2], Last: credits[1]},
		{Listed: 2, Selected: 2, SelectedIDs: credits[2:4], Last: credits[3]},
		{Listed: 1, Selected: 1, SelectedIDs: credits[4:5], Last: credits[4]},
		{Listed: 0, Selected: 0, Last: 0},
	}, pages)
	for _, id := range credits {
		assert.Equal(t, models.CreditSelectionSelected, f.selection(id).State, "credit %d", id)
	}
	_, err = SelectRegisteredCredits(ctx, f.tx, candidate, out, 0, 0)
	assert.ErrorIs(t, err, ErrInvalidAutomaticOutcome, "a page needs a positive bound")
}

// The input's duplicate reading is the local worker's: any author credit of
// the fingerprint under the extractor, current or superseded, with the flag.
func acceptanceInputDuplicateReading(t *testing.T) {
	ctx := context.Background()
	f := withStoreTx(t)
	plain := nameValue(t, nameComponent(authornorm.ComponentFirst, "Иван"), nameComponent(authornorm.ComponentLast, "Петров"))
	repeated := nameValue(t, nameComponent(authornorm.ComponentFirst, "Иван"),
		nameComponent(authornorm.ComponentFirst, ""), nameComponent(authornorm.ComponentLast, "Петров"))
	fp := authornorm.SourceFingerprint(plain)
	require.Equal(t, fp, authornorm.SourceFingerprint(repeated), "the fixture must share the input")
	f.persistBook(authorCredit(plain))
	flagged := func(c ExtractionCredit) ExtractionCredit {
		c.QualityFlags = []string{duplicateComponentFlag}
		return c
	}
	f.persistBook(flagged(translatorCredit(repeated)), authorCredit(doyleSource(t)))

	duplicate, err := InputHasDuplicateComponent(ctx, f.tx, fp[:], resolverExtractor)
	require.NoError(t, err)
	assert.False(t, duplicate, "a translator's flag is not the author input's")

	f.persistBook(flagged(authorCredit(repeated)))
	duplicate, err = InputHasDuplicateComponent(ctx, f.tx, fp[:], resolverExtractor)
	require.NoError(t, err)
	assert.True(t, duplicate)
	duplicate, err = InputHasDuplicateComponent(ctx, f.tx, fp[:], "another-extractor")
	require.NoError(t, err)
	assert.False(t, duplicate, "another extractor version is another input")
}

// The input lock (review finding B2): a credit write into an input waits while
// another transaction holds that input's lock — the acceptance pass's check
// and page — and a write into another input does not.
func TestPersistExtractionTakesTheInputLock(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	plain := structuredSource(t)
	fp := authornorm.SourceFingerprint(plain)

	holder, err := s.Begin()
	require.NoError(t, err)
	defer func() { _ = holder.Rollback() }()
	require.NoError(t, LockNormalizationInput(ctx, holder, fp[:], resolverExtractor))

	write := func(book int64, v authornorm.SourceValue) <-chan error {
		done := make(chan error, 1)
		go func() {
			done <- s.RunInTransaction(ctx, func(tx *pg.Tx) error {
				next := authorSchemaIDBase + book
				f := &authorSchemaFixture{t: t, tx: tx, next: &next}
				id := f.book()
				_, persistErr := PersistExtraction(tx, f.extraction(&extractionSpec{
					book: id, extractor: resolverExtractor, normalizer: authornorm.NormalizerVersion,
					credits: []ExtractionCredit{authorCredit(v)},
				}))
				return persistErr
			})
		}()
		return done
	}

	other := write(1000, doyleSource(t))
	select {
	case err = <-other:
		require.NoError(t, err, "another input does not wait")
	case <-time.After(10 * time.Second):
		t.Fatal("a write into another input waited for this input's lock")
	}

	same := write(2000, plain)
	select {
	case err = <-same:
		t.Fatalf("a write into the locked input did not wait (err %v)", err)
	case <-time.After(300 * time.Millisecond):
	}
	require.NoError(t, holder.Rollback())
	select {
	case err = <-same:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the write never finished after the lock was released")
	}
}

// A demotion takes back exactly the pass's kind of selection — automatic, on
// the candidate's result, with no override or administrator behind it — and
// puts it back where the pass found it; anything decided otherwise since
// stands.
func acceptanceDemotionTakesBackOnlyAutomaticSelections(t *testing.T) {
	ctx := context.Background()
	f := withStoreTx(t)
	admin := f.user()
	waiting, waitingID, credits := f.waitingInput(t, structuredSource(t), 4)
	f.registerClass("2", "structured_person", authornorm.NormalizerVersion)
	policy, err := LoadCurrentAcceptancePolicy(ctx, f.tx, authornorm.NormalizerVersion)
	require.NoError(t, err)
	d, err := policy.Decide(&waiting)
	require.NoError(t, err)
	out := &AutomaticOutcome{ResultID: waitingID, DecisionClass: waiting.DecisionClass, Decision: d}
	candidate := &AcceptanceCandidate{
		SourceFingerprint: waiting.SourceFingerprint[:], ExtractorVersion: waiting.ExtractorVersion, ResultID: waitingID,
	}
	page, err := SelectRegisteredCredits(ctx, f.tx, candidate, out, 0, 10)
	require.NoError(t, err)
	require.Equal(t, credits, page.SelectedIDs)

	// Since selected: one credit went to review on the same result, as the
	// local reconciliation sends an input that turned ambiguous.
	require.NoError(t, ResolveCreditSelection(ctx, f.tx, credits[3], &AutomaticOutcome{
		ResultID: waitingID, DecisionClass: authornorm.ClassDuplicateComponent,
		Decision: authornorm.Decision{Outcome: authornorm.OutcomeReview, PolicyVersion: 2},
	}))

	// Also since selected: one credit got an override, one an administrator's
	// decision recorded on its row. The resolver protects these two as well.
	fp := waiting.SourceFingerprint[:]
	f.creditOverride(credits[1], f.manualResultFor(fp, admin), fp, admin)
	require.NoError(t, ResolveCreditSelection(ctx, f.tx, credits[1], nil))
	f.exec(`UPDATE book_contributor_credit_selection SET decided_by_user_id = ? WHERE credit_id = ?`, admin, credits[2])
	audits := f.auditCount(credits[1])

	undo := &AutomaticOutcome{ResultID: waitingID, DecisionClass: waiting.DecisionClass, Decision: authornorm.Decision{
		Outcome: authornorm.OutcomeUnresolved, Reason: authornorm.ReasonPolicyNotRegistered, PolicyVersion: 2,
	}}
	demoted, err := DemoteAcceptanceSelections(ctx, f.tx, candidate, credits, undo)
	require.NoError(t, err)
	assert.Equal(t, 1, demoted)
	back := f.selection(credits[0])
	assert.Equal(t, models.CreditSelectionUnresolved, back.State)
	assert.Equal(t, models.UnresolvedPolicyNotRegistered, *back.UnresolvedReason)
	assert.Equal(t, waitingID, *back.ResultID)
	assert.Equal(t, models.CreditSelectionCreditOverride, *f.selection(credits[1]).Basis)
	assert.Equal(t, audits, f.auditCount(credits[1]), "an override's selection is not touched")
	assert.Equal(t, models.CreditSelectionSelected, f.selection(credits[2]).State, "an administrator's decision stands")
	assert.Equal(t, models.CreditSelectionReview, f.selection(credits[3]).State, "a review routing stands")

	_, err = DemoteAcceptanceSelections(ctx, f.tx, candidate, credits, out)
	assert.ErrorIs(t, err, ErrInvalidAutomaticOutcome, "a demotion only ever puts a credit back to waiting")
}
