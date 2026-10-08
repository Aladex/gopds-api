package database

// Phase 10 repository tests: the local result store, the acceptance policy as
// the database holds it, the one selection resolver (credit override >
// fingerprint override > automatic, scope A3 for the automatic part) and the
// completion accounting. The suite runs in its own scratch database and every
// case in a rolled-back transaction on it: an open review item or a result
// committed in the integration database for the same fingerprint would
// otherwise change what a case observes.

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/internal/authornorm"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resolverExtractor is the extractor version every resolver fixture uses.
const resolverExtractor = "extractor-v1"

// The sources below are golden cases of the phase-3 normalizer, one per
// outcome the resolver routes.
func structuredSource(t *testing.T) authornorm.SourceValue {
	return nameValue(t,
		nameComponent(authornorm.ComponentFirst, "Лев"),
		nameComponent(authornorm.ComponentMiddle, "Николаевич"),
		nameComponent(authornorm.ComponentLast, "Толстой"))
}

func initialsSource(t *testing.T) authornorm.SourceValue {
	return nameValue(t,
		nameComponent(authornorm.ComponentFirst, "И."),
		nameComponent(authornorm.ComponentLast, "Петров"))
}

func malformedSource(t *testing.T) authornorm.SourceValue {
	return nameValue(t, nameComponent(authornorm.ComponentLast, "***"))
}

func authorCredit(v authornorm.SourceValue) ExtractionCredit {
	return ExtractionCredit{Role: models.ContributorRoleAuthor, Source: v}
}

func translatorCredit(v authornorm.SourceValue) ExtractionCredit {
	return ExtractionCredit{Role: models.ContributorRoleTranslator, Source: v}
}

// normalizedAs runs the real normalizer and pins the class the case is about,
// so a normalizer change reports here instead of as a routing failure.
func normalizedAs(t *testing.T, v authornorm.SourceValue, class authornorm.DecisionClass) authornorm.Result {
	t.Helper()
	r, err := authornorm.Normalize(v, resolverExtractor)
	require.NoError(t, err)
	require.Equal(t, class, r.DecisionClass, "fixture source no longer has the class the case needs")
	return r
}

// persistBook writes one current extraction of a new book with the credits,
// under the shipped normalizer version, and returns the credit IDs in input
// order.
func (f *authorSchemaFixture) persistBook(credits ...ExtractionCredit) (book int64, ids []int64) {
	f.t.Helper()
	book = f.book()
	return book, f.persistVersion(book, bookMD5(book), credits...)
}

// persistVersion writes another file version of an existing book.
func (f *authorSchemaFixture) persistVersion(book int64, md5 string, credits ...ExtractionCredit) []int64 {
	f.t.Helper()
	in := f.extraction(&extractionSpec{
		book: book, md5: md5, extractor: resolverExtractor, normalizer: authornorm.NormalizerVersion,
		credits: credits,
	})
	out, err := PersistExtraction(f.tx, in)
	require.NoError(f.t, err)
	var ids []int64
	_, err = f.tx.Query(&ids, `SELECT id FROM book_contributor_credit WHERE snapshot_id = ? ORDER BY id`, out.SnapshotID)
	require.NoError(f.t, err)
	return ids
}

// storeResult inserts the local result and returns its ID.
func (f *authorSchemaFixture) storeResult(r *authornorm.Result) int64 {
	f.t.Helper()
	id, err := InsertLocalResult(context.Background(), f.tx, r)
	require.NoError(f.t, err)
	return id
}

// finishJob completes the pending job of a result's key directly: the lease
// path is the lease suite's concern, here only the terminal state matters.
func (f *authorSchemaFixture) finishJob(r *authornorm.Result, resultID int64) {
	f.t.Helper()
	f.exec(`UPDATE contributor_normalization_job
		SET status = 'completed', result_id = ?, finished_at = now()
		WHERE normalization_key = ?`, resultID, r.NormalizationKey[:])
}

// registerClass writes one acceptance-class row.
func (f *authorSchemaFixture) registerClass(policyVersion, class, configVersion string) {
	f.t.Helper()
	f.exec(`INSERT INTO author_acceptance_class
		(policy_version, decision_class, config_version, evidence_report_sha256, registered_by_user_id)
		VALUES (?, ?, ?, ?, 1)`, policyVersion, class, configVersion, fingerprint(32, 0xee))
}

// selection reads a credit's resolution, nil when it has none.
func (f *authorSchemaFixture) selection(credit int64) *models.BookContributorCreditSelection {
	f.t.Helper()
	s := new(models.BookContributorCreditSelection)
	err := f.tx.Model(s).Where("credit_id = ?", credit).Select()
	if err == pg.ErrNoRows {
		return nil
	}
	require.NoError(f.t, err)
	return s
}

func (f *authorSchemaFixture) auditCount(credit int64) int {
	return f.count(`SELECT count(*) FROM book_contributor_credit_selection_audit WHERE credit_id = ?`, credit)
}

func (f *authorSchemaFixture) openReviewItems(fp [32]byte) []models.ContributorReviewItem {
	f.t.Helper()
	var items []models.ContributorReviewItem
	err := f.tx.Model(&items).Where("source_fingerprint = ? AND status = 'open'", fp[:]).Select()
	require.NoError(f.t, err)
	return items
}

// decide applies a policy to a result the way the worker does.
func decide(t *testing.T, p authornorm.AcceptancePolicy, r *authornorm.Result, resultID int64) *AutomaticOutcome {
	t.Helper()
	d, err := p.Decide(r)
	require.NoError(t, err)
	return &AutomaticOutcome{ResultID: resultID, DecisionClass: r.DecisionClass, Decision: d}
}

// resolve applies one automatic outcome to every current author credit of the
// result's input.
func (f *authorSchemaFixture) resolve(r *authornorm.Result, out *AutomaticOutcome) ResolveReport {
	f.t.Helper()
	report, err := ResolveCredits(context.Background(), f.tx, r.SourceFingerprint[:], r.ExtractorVersion, out)
	require.NoError(f.t, err)
	return report
}

// registeredPolicy registers structured_person in policy version 1 and loads it.
func registeredPolicy(t *testing.T, f *authorSchemaFixture) authornorm.AcceptancePolicy {
	t.Helper()
	f.registerClass("1", string(authornorm.ClassStructuredPerson), authornorm.NormalizerVersion)
	p, err := LoadAcceptancePolicy(context.Background(), f.tx, 1, authornorm.NormalizerVersion)
	require.NoError(t, err)
	return p
}

func emptyPolicy(t *testing.T, f *authorSchemaFixture) authornorm.AcceptancePolicy {
	t.Helper()
	p, err := LoadAcceptancePolicy(context.Background(), f.tx, 1, authornorm.NormalizerVersion)
	require.NoError(t, err)
	require.True(t, p.Empty())
	return p
}

// storeDB is the suite's scratch database while TestLocalNormalizationStore
// runs.
var storeDB *pg.DB

// TestLocalNormalizationStore runs the store cases over one scratch database:
// migrating one costs over a second, and each case rolls its transaction back.
func TestLocalNormalizationStore(t *testing.T) {
	storeDB = jobsDB(t)
	t.Cleanup(func() { storeDB = nil })
	cases := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"InsertLocalResultSharesOneRowPerKey", storeInsertLocalResultSharesOneRowPerKey},
		{"InsertLocalResultRefusesAnInvalidResult", storeInsertLocalResultRefusesAnInvalidResult},
		{"LoadAcceptancePolicy", storeLoadAcceptancePolicy},
		{"ResolveCreditsRoutesEveryOutcome", storeResolveCreditsRoutesEveryOutcome},
		{"ResolverPrecedence", storeResolverPrecedence},
		{"AutomaticOutcomeKeepsAnAdminDecision", storeAutomaticOutcomeKeepsAnAdminDecision},
		{"AuthorCreditAccounting", storeAuthorCreditAccounting},
	}
	for _, c := range cases {
		t.Run(c.name, c.fn)
	}
}

// withStoreTx is withAuthorSchemaTx on the suite's scratch database.
func withStoreTx(t *testing.T) *authorSchemaFixture {
	t.Helper()
	tx, err := storeDB.Begin()
	require.NoError(t, err, "beginning the store test transaction")
	t.Cleanup(func() { _ = tx.Rollback() })
	next := authorSchemaIDBase
	return &authorSchemaFixture{t: t, tx: tx, next: &next}
}

// RED 1 (repository half): one local result per normalization key, whoever
// asks, stored with every field of the domain result.
func storeInsertLocalResultSharesOneRowPerKey(t *testing.T) {
	f := withStoreTx(t)
	r := normalizedAs(t, structuredSource(t), authornorm.ClassStructuredPerson)

	first := f.storeResult(&r)
	again := f.storeResult(&r)
	assert.Equal(t, first, again, "the same key must converge on one row")
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM contributor_normalization_result WHERE normalization_key = ?`,
		r.NormalizationKey[:]))

	stored := new(models.ContributorNormalizationResult)
	require.NoError(t, f.tx.Model(stored).Where("id = ?", first).Select())
	assert.Equal(t, r.SourceFingerprint[:], stored.SourceFingerprint)
	assert.Equal(t, r.NormalizationKey[:], stored.NormalizationKey)
	assert.Equal(t, resolverExtractor, *stored.ExtractorVersion)
	assert.Equal(t, authornorm.NormalizerVersion, *stored.NormalizerVersion)
	assert.Equal(t, "1", stored.ResultSchemaVersion)
	assert.Equal(t, models.NormalizationStructured, stored.Method)
	assert.Equal(t, models.NormalizationPerson, stored.Kind)
	assert.Equal(t, models.NormalizationNormalized, stored.Status)
	assert.Equal(t, "structured_person", *stored.DecisionClass)
	assert.Equal(t, "Лев", *stored.GivenName)
	assert.Equal(t, []string{"Николаевич"}, stored.AdditionalNames)
	assert.Equal(t, "Толстой", *stored.FamilyName)
	assert.Nil(t, stored.Nickname)
	assert.Equal(t, "Лев Николаевич Толстой", *stored.DisplayName)
	assert.Equal(t, "Толстой, Лев Николаевич", *stored.SortName)
	assert.Equal(t, "лев николаевич толстой", *stored.SearchKey)
	assert.Equal(t, "Cyrl", *stored.Script)
	assert.Empty(t, stored.QualityFlags)
	assert.Nil(t, stored.CreatedByUserID)

	flagged := normalizedAs(t, initialsSource(t), authornorm.ClassInitials)
	flaggedID := f.storeResult(&flagged)
	require.NoError(t, f.tx.Model(stored).Where("id = ?", flaggedID).Select())
	want := make([]string, 0, len(flagged.QualityFlags))
	for _, flag := range flagged.QualityFlags {
		want = append(want, string(flag))
	}
	assert.Equal(t, want, stored.QualityFlags)
	assert.NotEmpty(t, stored.QualityFlags)
}

func storeInsertLocalResultRefusesAnInvalidResult(t *testing.T) {
	f := withStoreTx(t)
	r := normalizedAs(t, structuredSource(t), authornorm.ClassStructuredPerson)
	r.SchemaVersion = 0
	_, err := InsertLocalResult(context.Background(), f.tx, &r)
	assert.ErrorIs(t, err, authornorm.ErrInvalidSchemaVersion)

	manual := normalizedAs(t, structuredSource(t), authornorm.ClassStructuredPerson)
	manual.Method = authornorm.MethodManual
	_, err = InsertLocalResult(context.Background(), f.tx, &manual)
	assert.ErrorIs(t, err, ErrNotLocalResult)
	assert.Equal(t, 0, f.count(`SELECT count(*) FROM contributor_normalization_result WHERE normalization_key = ?`,
		r.NormalizationKey[:]))
}

// The acceptance policy is what the class table registers for one exact
// policy version and normalizer configuration — and nothing when it is empty.
func storeLoadAcceptancePolicy(t *testing.T) {
	t.Run("empty table is the empty production policy", func(t *testing.T) {
		f := withStoreTx(t)
		p := emptyPolicy(t, f)
		assert.Equal(t, 1, p.Version())
	})
	t.Run("registered class selects", func(t *testing.T) {
		f := withStoreTx(t)
		p := registeredPolicy(t, f)
		r := normalizedAs(t, structuredSource(t), authornorm.ClassStructuredPerson)
		d, err := p.Decide(&r)
		require.NoError(t, err)
		assert.True(t, d.Selected())
		assert.Equal(t, [32]byte(fingerprint(32, 0xee)), d.EvidenceSHA256)
	})
	t.Run("other policy version or configuration does not count", func(t *testing.T) {
		f := withStoreTx(t)
		f.registerClass("2", "structured_person", authornorm.NormalizerVersion)
		f.registerClass("1", "structured_person", "another-normalizer")
		emptyPolicy(t, f)
	})
	t.Run("an ambiguous registration fails closed", func(t *testing.T) {
		f := withStoreTx(t)
		f.registerClass("1", "initials", authornorm.NormalizerVersion)
		_, err := LoadAcceptancePolicy(context.Background(), f.tx, 1, authornorm.NormalizerVersion)
		assert.ErrorIs(t, err, authornorm.ErrClassNotSelectable)
	})
	t.Run("an unknown registered class fails closed", func(t *testing.T) {
		f := withStoreTx(t)
		f.registerClass("1", "no_such_class", authornorm.NormalizerVersion)
		_, err := LoadAcceptancePolicy(context.Background(), f.tx, 1, authornorm.NormalizerVersion)
		assert.ErrorIs(t, err, authornorm.ErrUnknownDecisionClass)
	})
}

// RED 2/3 (repository half), scope A3: every outcome lands its credits in the
// state the amendment names — review with an open item and a proposal,
// invalid, unresolved with the closed reason and a proposal, or selected with
// the policy that allowed it — and each change is audited. Translator credits
// of the same source are never resolved.
func storeResolveCreditsRoutesEveryOutcome(t *testing.T) {
	ctx := context.Background()

	t.Run("empty policy", func(t *testing.T) {
		f := withStoreTx(t)
		p := emptyPolicy(t, f)
		_, ids := f.persistBook(
			authorCredit(structuredSource(t)), authorCredit(initialsSource(t)), authorCredit(malformedSource(t)),
			translatorCredit(structuredSource(t)))
		structured := normalizedAs(t, structuredSource(t), authornorm.ClassStructuredPerson)
		initials := normalizedAs(t, initialsSource(t), authornorm.ClassInitials)
		malformed := normalizedAs(t, malformedSource(t), authornorm.ClassMalformed)
		structuredID, initialsID, malformedID := f.storeResult(&structured), f.storeResult(&initials), f.storeResult(&malformed)

		f.resolve(&structured, decide(t, p, &structured, structuredID))
		f.resolve(&initials, decide(t, p, &initials, initialsID))
		f.resolve(&malformed, decide(t, p, &malformed, malformedID))

		unresolved := f.selection(ids[0])
		require.NotNil(t, unresolved)
		assert.Equal(t, models.CreditSelectionUnresolved, unresolved.State)
		assert.Equal(t, models.UnresolvedPolicyNotRegistered, *unresolved.UnresolvedReason)
		assert.Equal(t, structuredID, *unresolved.ResultID, "the proposal is kept")
		assert.Nil(t, unresolved.Basis)
		assert.Empty(t, f.openReviewItems(structured.SourceFingerprint), "unresolved opens no review item")

		review := f.selection(ids[1])
		require.NotNil(t, review)
		assert.Equal(t, models.CreditSelectionReview, review.State)
		assert.Equal(t, initialsID, *review.ResultID)
		items := f.openReviewItems(initials.SourceFingerprint)
		require.Len(t, items, 1)
		assert.Equal(t, initials.SourceFingerprint[:], items[0].ScopeFingerprint)
		assert.Nil(t, items[0].ScopeCreditID)
		assert.Equal(t, models.ReviewAmbiguousDecision, items[0].Reason)
		assert.Equal(t, "initials", *items[0].DecisionClass)
		assert.Equal(t, initialsID, *items[0].ProposalResultID)

		invalid := f.selection(ids[2])
		require.NotNil(t, invalid)
		assert.Equal(t, models.CreditSelectionInvalid, invalid.State)
		assert.Equal(t, malformedID, *invalid.ResultID)
		assert.Empty(t, f.openReviewItems(malformed.SourceFingerprint))

		assert.Nil(t, f.selection(ids[3]), "a translator credit is never resolved")
		for _, id := range ids[:3] {
			assert.Equal(t, 1, f.auditCount(id), "credit %d", id)
		}
	})

	t.Run("registered class selects every applicable credit", func(t *testing.T) {
		f := withStoreTx(t)
		p := registeredPolicy(t, f)
		_, first := f.persistBook(authorCredit(structuredSource(t)))
		_, second := f.persistBook(authorCredit(structuredSource(t)), translatorCredit(structuredSource(t)))
		r := normalizedAs(t, structuredSource(t), authornorm.ClassStructuredPerson)
		id := f.storeResult(&r)

		report := f.resolve(&r, decide(t, p, &r, id))
		assert.Equal(t, map[models.CreditSelectionState]int{models.CreditSelectionSelected: 2}, report.States)

		for _, credit := range []int64{first[0], second[0]} {
			s := f.selection(credit)
			require.NotNil(t, s)
			assert.Equal(t, models.CreditSelectionSelected, s.State)
			assert.Equal(t, models.CreditSelectionAutomatic, *s.Basis)
			assert.Equal(t, "1", *s.PolicyVersion)
			assert.Equal(t, id, *s.ResultID)
			assert.Nil(t, s.DecidedByUserID)

			var audit models.BookContributorCreditSelectionAudit
			require.NoError(t, f.tx.Model(&audit).Where("credit_id = ?", credit).Select())
			assert.Equal(t, models.CreditSelectionSelected, audit.State)
			assert.Equal(t, models.CreditSelectionAutomatic, *audit.Basis)
			assert.Equal(t, "1", *audit.PolicyVersion)
		}
		assert.Nil(t, f.selection(second[1]))
	})

	t.Run("failed normalization is unresolved without a proposal", func(t *testing.T) {
		f := withStoreTx(t)
		_, ids := f.persistBook(authorCredit(structuredSource(t)))
		fp := authornorm.SourceFingerprint(structuredSource(t))
		_, err := ResolveCredits(ctx, f.tx, fp[:], resolverExtractor, &AutomaticOutcome{Failed: true})
		require.NoError(t, err)

		s := f.selection(ids[0])
		require.NotNil(t, s)
		assert.Equal(t, models.CreditSelectionUnresolved, s.State)
		assert.Equal(t, models.UnresolvedNormalizerFailed, *s.UnresolvedReason)
		assert.Nil(t, s.ResultID)
		assert.Empty(t, f.openReviewItems(fp))
	})

	t.Run("another extractor version or a superseded snapshot is not resolved", func(t *testing.T) {
		f := withStoreTx(t)
		p := emptyPolicy(t, f)
		book, old := f.persistBook(authorCredit(structuredSource(t)))
		current := f.persistVersion(book, bookMD5(book+1_000_000), authorCredit(structuredSource(t)))
		r := normalizedAs(t, structuredSource(t), authornorm.ClassStructuredPerson)
		id := f.storeResult(&r)

		_, err := ResolveCredits(ctx, f.tx, r.SourceFingerprint[:], "extractor-v2", decide(t, p, &r, id))
		require.NoError(t, err)
		assert.Nil(t, f.selection(current[0]), "another extractor version's outcome")

		f.resolve(&r, decide(t, p, &r, id))
		assert.Nil(t, f.selection(old[0]), "a superseded snapshot's credit")
		assert.NotNil(t, f.selection(current[0]))
	})

	t.Run("resolving again changes nothing", func(t *testing.T) {
		f := withStoreTx(t)
		p := emptyPolicy(t, f)
		_, ids := f.persistBook(authorCredit(initialsSource(t)))
		r := normalizedAs(t, initialsSource(t), authornorm.ClassInitials)
		id := f.storeResult(&r)
		f.resolve(&r, decide(t, p, &r, id))
		f.resolve(&r, decide(t, p, &r, id))

		assert.Equal(t, 1, f.auditCount(ids[0]))
		assert.Len(t, f.openReviewItems(r.SourceFingerprint), 1)
	})
}

// RED 5 (repository half): credit override > fingerprint override > automatic,
// in one resolver, whichever comes first.
func storeResolverPrecedence(t *testing.T) {
	ctx := context.Background()
	f := withStoreTx(t)
	p := registeredPolicy(t, f)
	admin := f.user()
	_, a := f.persistBook(authorCredit(structuredSource(t)))
	_, b := f.persistBook(authorCredit(structuredSource(t)))
	_, c := f.persistBook(authorCredit(structuredSource(t)))
	r := normalizedAs(t, structuredSource(t), authornorm.ClassStructuredPerson)
	fp := r.SourceFingerprint[:]
	automatic := f.storeResult(&r)

	byFingerprint := f.manualResultFor(fp, admin)
	fingerprintOverride := f.fingerprintOverride(fp, byFingerprint, admin)
	byCredit := f.manualResultFor(fp, admin)
	creditOverride := f.creditOverride(a[0], byCredit, fp, admin)

	f.resolve(&r, decide(t, p, &r, automatic))

	sa := f.selection(a[0])
	require.NotNil(t, sa)
	assert.Equal(t, models.CreditSelectionSelected, sa.State)
	assert.Equal(t, models.CreditSelectionCreditOverride, *sa.Basis)
	assert.Equal(t, creditOverride, *sa.OverrideID)
	assert.Equal(t, byCredit, *sa.ResultID)

	for _, credit := range []int64{b[0], c[0]} {
		s := f.selection(credit)
		require.NotNil(t, s)
		assert.Equal(t, models.CreditSelectionFingerprintOverride, *s.Basis)
		assert.Equal(t, fingerprintOverride, *s.OverrideID)
		assert.Equal(t, byFingerprint, *s.ResultID)
	}

	// An override that arrives after the automatic selection takes over
	// through the same resolver; without one, a re-resolve changes nothing.
	_, d := f.persistBook(authorCredit(initialsSource(t)))
	ri := normalizedAs(t, initialsSource(t), authornorm.ClassInitials)
	f.resolve(&ri, decide(t, p, &ri, f.storeResult(&ri)))
	require.NoError(t, ResolveCreditSelection(ctx, f.tx, d[0], nil))
	assert.Equal(t, models.CreditSelectionReview, f.selection(d[0]).State)
	assert.Equal(t, 1, f.auditCount(d[0]))

	late := f.manualResultFor(ri.SourceFingerprint[:], admin)
	lateOverride := f.creditOverride(d[0], late, ri.SourceFingerprint[:], admin)
	require.NoError(t, ResolveCreditSelection(ctx, f.tx, d[0], nil))
	sd := f.selection(d[0])
	assert.Equal(t, models.CreditSelectionSelected, sd.State)
	assert.Equal(t, models.CreditSelectionCreditOverride, *sd.Basis)
	assert.Equal(t, lateOverride, *sd.OverrideID)

	// A later automatic outcome never displaces the override.
	f.resolve(&ri, decide(t, p, &ri, f.storeResult(&ri)))
	assert.Equal(t, models.CreditSelectionCreditOverride, *f.selection(d[0]).Basis)
}

// An admin's decision without an override (review left unresolved) is not
// overwritten by the pipeline re-resolving the same credit.
func storeAutomaticOutcomeKeepsAnAdminDecision(t *testing.T) {
	f := withStoreTx(t)
	p := emptyPolicy(t, f)
	admin := f.user()
	_, ids := f.persistBook(authorCredit(initialsSource(t)))
	r := normalizedAs(t, initialsSource(t), authornorm.ClassInitials)
	id := f.storeResult(&r)
	f.exec(`INSERT INTO book_contributor_credit_selection
		(credit_id, source_fingerprint, state, result_id, unresolved_reason, decided_by_user_id)
		VALUES (?, ?, 'unresolved', ?, 'review_left_unresolved', ?)`, ids[0], r.SourceFingerprint[:], id, admin)

	f.resolve(&r, decide(t, p, &r, id))

	s := f.selection(ids[0])
	assert.Equal(t, models.CreditSelectionUnresolved, s.State)
	assert.Equal(t, models.UnresolvedReviewLeftUnresolved, *s.UnresolvedReason)
	assert.Equal(t, admin, *s.DecidedByUserID)
	assert.Equal(t, 1, f.auditCount(ids[0]))
	assert.Empty(t, f.openReviewItems(r.SourceFingerprint))
}

// RED 9 (repository half): a run is settled when every current author credit
// of its books is selected, invalid, unresolved or in open review — an open
// review backlog is allowed, a credit with no resolution, a pending local job
// or a review state without an open item is not.
func storeAuthorCreditAccounting(t *testing.T) {
	ctx := context.Background()
	f := withStoreTx(t)
	p := emptyPolicy(t, f)
	run := f.run(&runSpec{status: "running", extractor: resolverExtractor, normalizer: authornorm.NormalizerVersion})

	structuredBook, structured := f.persistBook(authorCredit(structuredSource(t)), translatorCredit(initialsSource(t)))
	initialsBook, initials := f.persistBook(authorCredit(initialsSource(t)))
	malformedBook, _ := f.persistBook(authorCredit(malformedSource(t)))
	f.persistBook(authorCredit(structuredSource(t))) // outside the run
	for _, book := range []int64{structuredBook, initialsBook, malformedBook} {
		f.runItem(run, book)
	}

	account := func() CreditAccounting {
		t.Helper()
		a, err := AuthorCreditAccountingForRun(ctx, f.tx, run)
		require.NoError(t, err)
		return a
	}

	a := account()
	assert.Equal(t, 3, a.Credits, "author credits of current snapshots of the run's books")
	assert.Equal(t, 3, a.Pending)
	assert.False(t, a.Settled())

	rs := normalizedAs(t, structuredSource(t), authornorm.ClassStructuredPerson)
	ri := normalizedAs(t, initialsSource(t), authornorm.ClassInitials)
	rm := normalizedAs(t, malformedSource(t), authornorm.ClassMalformed)
	for _, r := range []*authornorm.Result{&rs, &ri, &rm} {
		id := f.storeResult(r)
		f.resolve(r, decide(t, p, r, id))
	}
	a = account()
	assert.Equal(t, 3, a.Pending, "resolved credits whose local jobs are still pending stay pending")

	for _, r := range []*authornorm.Result{&rs, &ri, &rm} {
		f.finishJob(r, f.storeResult(r))
	}
	a = account()
	assert.Equal(t, CreditAccounting{
		Credits:    3,
		Invalid:    1,
		Review:     1,
		Unresolved: map[models.UnresolvedReason]int{models.UnresolvedPolicyNotRegistered: 1},
	}, a)
	assert.True(t, a.Settled(), "an open review backlog does not block completion")

	// A review state whose item was closed without a new decision is hidden.
	admin := f.user()
	f.exec(`UPDATE contributor_review_item
		SET status = 'closed', resolution = 'left_unresolved', resolved_by_user_id = ?, finished_at = now()
		WHERE source_fingerprint = ? AND status = 'open'`, admin, ri.SourceFingerprint[:])
	a = account()
	assert.Equal(t, 1, a.Pending)
	assert.Equal(t, 0, a.Review)
	assert.False(t, a.Settled())
	assert.Equal(t, models.CreditSelectionReview, f.selection(initials[0]).State)

	// A new file version replaces the counted credits with its own.
	f.persistVersion(structuredBook, bookMD5(structuredBook+1_000_000), authorCredit(structuredSource(t)))
	a = account()
	assert.Equal(t, 3, a.Credits)
	assert.Equal(t, 2, a.Pending, "the new version's credit has no resolution yet")
	assert.NotNil(t, f.selection(structured[0]))

	all, err := AuthorCreditAccounting(ctx, f.tx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, all.Credits, 4, "the catalog-wide count includes the book outside the run")
}

// Fix round 1 (review B2): precedence must hold when an automatic and a manual
// resolution of the same credit run concurrently, not only one after another.

// selectionWriteGate pauses a resolution right before it writes the
// selection, after it has read whether an override exists.
type selectionWriteGate struct {
	*pg.Tx
	ready, release chan struct{}
}

func (g *selectionWriteGate) ExecContext(ctx context.Context, q interface{}, p ...interface{}) (pg.Result, error) {
	if sql, ok := q.(string); ok && sql == upsertSelectionSQL {
		close(g.ready)
		<-g.release
	}
	return g.Tx.ExecContext(ctx, q, p...)
}

// concurrentResolution runs a resolution in its own goroutine and reports
// its end on done; finished flips once it has returned.
type concurrentResolution struct {
	done     chan error
	finished atomic.Bool
}

func resolveConcurrently(ctx context.Context, tx *pg.Tx, conn ResolutionTx, credit int64, out *AutomaticOutcome) *concurrentResolution {
	r := &concurrentResolution{done: make(chan error, 1)}
	go func() {
		err := ResolveCreditSelection(ctx, conn, credit, out)
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		r.finished.Store(true)
		r.done <- err
	}()
	return r
}

// waitBlockedOrFinished waits until the resolution has returned or some
// session of the scratch database waits on a lock, whichever comes first.
func waitBlockedOrFinished(t *testing.T, s *pg.DB, r *concurrentResolution) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r.finished.Load() {
			return
		}
		var waiting int
		_, err := s.QueryOne(pg.Scan(&waiting), `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`)
		require.NoError(t, err)
		if waiting > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the concurrent resolution neither finished nor waited on a lock")
}

// precedenceRace seeds one committed author credit whose input has a stored
// result under a registered class, so the automatic outcome would select it.
func precedenceRace(t *testing.T, s *pg.DB) (credit int64, r authornorm.Result, auto *AutomaticOutcome, next *int64) {
	t.Helper()
	seed, err := s.Begin()
	require.NoError(t, err)
	n := authorSchemaIDBase
	f := &authorSchemaFixture{t: t, tx: seed, next: &n}
	p := registeredPolicy(t, f)
	_, ids := f.persistBook(authorCredit(structuredSource(t)))
	r = normalizedAs(t, structuredSource(t), authornorm.ClassStructuredPerson)
	id := f.storeResult(&r)
	f.finishJob(&r, id)
	auto = decide(t, p, &r, id)
	require.NoError(t, seed.Commit())
	return ids[0], r, auto, &n
}

func requireOverrideSelected(t *testing.T, s *pg.DB, credit, manual, override int64) {
	t.Helper()
	var got models.BookContributorCreditSelection
	require.NoError(t, s.Model(&got).Where("credit_id = ?", credit).Select())
	require.NotNil(t, got.Basis)
	assert.Equal(t, models.CreditSelectionCreditOverride, *got.Basis, "an active credit override must win")
	require.NotNil(t, got.ResultID)
	assert.Equal(t, manual, *got.ResultID)
	require.NotNil(t, got.OverrideID)
	assert.Equal(t, override, *got.OverrideID)
}

func TestResolverPrecedenceUnderConcurrency(t *testing.T) {
	ctx := context.Background()

	// The reviewer's reproduction: the automatic resolution has read that no
	// override exists and is about to write when the manual one commits an
	// override and resolves the credit through the same resolver.
	t.Run("automatic paused before its write", func(t *testing.T) {
		s := jobsDB(t)
		credit, r, auto, next := precedenceRace(t, s)

		autoTx, err := s.Begin()
		require.NoError(t, err)
		gate := &selectionWriteGate{Tx: autoTx, ready: make(chan struct{}), release: make(chan struct{})}
		automatic := resolveConcurrently(ctx, autoTx, gate, credit, auto)
		select {
		case <-gate.ready:
		case err = <-automatic.done:
			t.Fatalf("the automatic resolution ended before its write: %v", err)
		}

		manualTx, err := s.Begin()
		require.NoError(t, err)
		fm := &authorSchemaFixture{t: t, tx: manualTx, next: next}
		manual := fm.manualResultFor(r.SourceFingerprint[:], 1)
		override := fm.creditOverride(credit, manual, r.SourceFingerprint[:], 1)
		resolution := resolveConcurrently(ctx, manualTx, manualTx, credit, nil)
		waitBlockedOrFinished(t, s, resolution)

		close(gate.release)
		require.NoError(t, <-automatic.done)
		require.NoError(t, <-resolution.done)
		requireOverrideSelected(t, s, credit, manual, override)
	})

	// The manual resolution has written its override selection but not yet
	// committed when the automatic one starts.
	t.Run("manual uncommitted when automatic starts", func(t *testing.T) {
		s := jobsDB(t)
		credit, r, auto, next := precedenceRace(t, s)

		manualTx, err := s.Begin()
		require.NoError(t, err)
		fm := &authorSchemaFixture{t: t, tx: manualTx, next: next}
		manual := fm.manualResultFor(r.SourceFingerprint[:], 1)
		override := fm.creditOverride(credit, manual, r.SourceFingerprint[:], 1)
		require.NoError(t, ResolveCreditSelection(ctx, manualTx, credit, nil))

		autoTx, err := s.Begin()
		require.NoError(t, err)
		automatic := resolveConcurrently(ctx, autoTx, autoTx, credit, auto)
		waitBlockedOrFinished(t, s, automatic)

		require.NoError(t, manualTx.Commit())
		require.NoError(t, <-automatic.done)
		requireOverrideSelected(t, s, credit, manual, override)
	})
}

// Fix round 2 (re-review round 1): the reviewer's settlement reproduction at
// the repository boundary. A failed input is settled through the resolver in
// the settling transaction, paused before its write; a manual transaction
// writes a credit override and resolves the credit; the override stays.
func TestFailedSettlementResolutionKeepsConcurrentOverride(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	seed, err := s.Begin()
	require.NoError(t, err)
	next := authorSchemaIDBase
	f := &authorSchemaFixture{t: t, tx: seed, next: &next}
	_, ids := f.persistBook(authorCredit(structuredSource(t)))
	r := normalizedAs(t, structuredSource(t), authornorm.ClassStructuredPerson)
	f.exec(`UPDATE contributor_normalization_job
		SET status = 'failed', last_error_class = 'normalizer_failed', finished_at = now()
		WHERE normalization_key = ?`, r.NormalizationKey[:])
	require.NoError(t, seed.Commit())

	inputs, err := FailedInputsToSettle(ctx, s, authornorm.NormalizerVersion, 10)
	require.NoError(t, err)
	require.Len(t, inputs, 1)

	settleTx, err := s.Begin()
	require.NoError(t, err)
	gate := &selectionWriteGate{Tx: settleTx, ready: make(chan struct{}), release: make(chan struct{})}
	settled := make(chan error, 1)
	go func() {
		_, resolveErr := ResolveCredits(ctx, gate, inputs[0].SourceFingerprint, inputs[0].ExtractorVersion,
			&AutomaticOutcome{Failed: true})
		if resolveErr != nil {
			_ = settleTx.Rollback()
			settled <- resolveErr
			return
		}
		settled <- settleTx.Commit()
	}()
	select {
	case <-gate.ready:
	case err = <-settled:
		t.Fatalf("the settlement ended before its write: %v", err)
	}

	manualTx, err := s.Begin()
	require.NoError(t, err)
	fm := &authorSchemaFixture{t: t, tx: manualTx, next: &next}
	manual := fm.manualResultFor(r.SourceFingerprint[:], 1)
	override := fm.creditOverride(ids[0], manual, r.SourceFingerprint[:], 1)
	resolution := resolveConcurrently(ctx, manualTx, manualTx, ids[0], nil)
	waitBlockedOrFinished(t, s, resolution)

	close(gate.release)
	require.NoError(t, <-settled)
	require.NoError(t, <-resolution.done)
	requireOverrideSelected(t, s, ids[0], manual, override)
}

// A pooled connection is not a resolution transaction: the resolver's credit
// lock would end with its own statement.
func TestResolutionNeedsATransaction(t *testing.T) {
	var conn interface{} = &pg.DB{}
	_, ok := conn.(ResolutionTx)
	assert.False(t, ok, "*pg.DB must not satisfy ResolutionTx")
	var tx interface{} = &pg.Tx{}
	_, ok = tx.(ResolutionTx)
	assert.True(t, ok)
}
