package database

// Phase 11 repository tests: manual overrides as immutable corrections, the
// review actions over the one phase-10 resolver, review pagination with the
// (created_at, id) cursor, linked books for the review screen, and the
// lock-order rule against the resolver. The suite runs in its own scratch
// database; rolled-back cases share it, concurrency cases open their own.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"gopds-api/internal/authornorm"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reviewDB is the suite's scratch database while TestAuthorMetadataReview
// runs.
var reviewDB *pg.DB

// testRetryBudget is the worker attempt budget the retry cases decide under;
// it matches the MaxAttempts their claims use.
const testRetryBudget = 5

func TestAuthorMetadataReview(t *testing.T) {
	reviewDB = jobsDB(t)
	t.Cleanup(func() { reviewDB = nil })
	cases := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"OverrideScopeValidation", reviewOverrideScopeValidation},
		{"CreditOverrideBeatsFingerprintAndAutomatic", reviewCreditOverrideBeatsFingerprintAndAutomatic},
		{"FingerprintOverrideCoversFutureCredits", reviewFingerprintOverrideCoversFutureCredits},
		{"ChangedFingerprintDoesNotInherit", reviewChangedFingerprintDoesNotInherit},
		{"EditKeepsTheFirstResultAndOverride", reviewEditKeepsTheFirstResultAndOverride},
		{"ReviewTransitionsClosed", reviewTransitionsClosed},
		{"IncompatibleManualSchemaReview", reviewIncompatibleManualSchemaReview},
		{"ReviewPaginationStableUnderDuplicateTimestamps", reviewPaginationStableUnderDuplicateTimestamps},
		{"LinkedBooksStayOnTheReviewScreen", reviewLinkedBooksStayOnTheReviewScreen},
	}
	for _, c := range cases {
		t.Run(c.name, c.fn)
	}
}

func withReviewTx(t *testing.T) *authorSchemaFixture {
	t.Helper()
	tx, err := reviewDB.Begin()
	require.NoError(t, err, "beginning the review test transaction")
	t.Cleanup(func() { _ = tx.Rollback() })
	next := authorSchemaIDBase
	return &authorSchemaFixture{t: t, tx: tx, next: &next}
}

// correctionOf builds a valid manual correction for a person.
func correctionOf(display string) *ManualCorrection {
	return &ManualCorrection{
		GivenName: "Имя", FamilyName: "Фамилия", DisplayName: display, SortName: display,
		SearchKey: display, Script: string(authornorm.ScriptCyrillic), Kind: models.NormalizationPerson,
	}
}

// rowJSON renders one row exactly as stored, for byte-stable comparisons.
func rowJSON(t *testing.T, tx *pg.Tx, table string, id int64) string {
	t.Helper()
	var raw json.RawMessage
	_, err := tx.QueryOne(pg.Scan(&raw), `SELECT to_jsonb(x) FROM `+table+` x WHERE x.id = ?`, id)
	require.NoError(t, err)
	return string(raw)
}

func rowJSONSelection(t *testing.T, tx *pg.Tx, credit int64) string {
	t.Helper()
	var raw json.RawMessage
	_, err := tx.QueryOne(pg.Scan(&raw),
		`SELECT to_jsonb(s) FROM book_contributor_credit_selection s WHERE s.credit_id = ?`, credit)
	require.NoError(t, err)
	return string(raw)
}

// RED 1: empty, both and negative scopes are rejected before any SQL — the
// transaction is nil, so a statement would panic.
func reviewOverrideScopeValidation(t *testing.T) {
	valid := *correctionOf("Проверка")
	for _, scope := range []OverrideScope{
		{},
		{CreditID: 5, Fingerprint: fingerprint(32, 1)},
		{CreditID: -3},
		{CreditID: -3, Fingerprint: fingerprint(32, 1)},
		{Fingerprint: fingerprint(16, 1)},
		{Fingerprint: fingerprint(33, 1)},
	} {
		_, err := ApplyOverride(context.Background(), nil, scope, 1, &valid)
		assert.ErrorIs(t, err, ErrInvalidOverrideScope, "scope %+v", scope)
	}
	for _, admin := range []int64{0, -1} {
		_, err := ApplyOverride(context.Background(), nil, OverrideScope{CreditID: 7}, admin, &valid)
		assert.ErrorIs(t, err, ErrInvalidReviewActor)
	}
	for _, broken := range []*ManualCorrection{
		{Kind: models.NormalizationMalformed, DisplayName: "x", SearchKey: "x", Script: "Cyrl"},
		{Kind: models.NormalizationPerson, DisplayName: "", SearchKey: "x", Script: "Cyrl"},
		{Kind: models.NormalizationPerson, DisplayName: "x", SearchKey: " ", Script: "Cyrl"},
		{Kind: models.NormalizationPerson, DisplayName: "x", SearchKey: "x", Script: "Klingon"},
		{Kind: "editor", DisplayName: "x", SearchKey: "x", Script: "Cyrl"},
	} {
		_, err := ApplyOverride(context.Background(), nil, OverrideScope{CreditID: 7}, 1, broken)
		assert.ErrorIs(t, err, ErrInvalidManualCorrection, "correction %+v", broken)
	}

	for _, decision := range []ReviewDecision{
		{Action: "lost"},
		{Action: ReviewEdit},
		{Action: ReviewClassify, Kind: "editor"},
		{Action: ReviewRetry},
		{Action: ReviewRetry, RetryNormalizerVersion: authornorm.NormalizerVersion},
		{Action: ReviewRetry, RetryNormalizerVersion: authornorm.NormalizerVersion, RetryMaxAttempts: -1},
	} {
		_, err := ApplyReviewAction(context.Background(), nil, 1, 1, decision)
		assert.ErrorIs(t, err, ErrInvalidReviewAction, "decision %+v", decision)
	}
	for _, item := range []int64{0, -2} {
		_, err := ApplyReviewAction(context.Background(), nil, item, 1, ReviewDecision{Action: ReviewAccept})
		assert.ErrorIs(t, err, ErrReviewNotFound)
	}
	_, err := ApplyReviewAction(context.Background(), nil, 1, 0, ReviewDecision{Action: ReviewAccept})
	assert.ErrorIs(t, err, ErrInvalidReviewActor)

	_, err = LinkedBooksOfFingerprint(context.Background(), nil, fingerprint(16, 1), 10)
	assert.ErrorIs(t, err, ErrInvalidOverrideScope)
}

// RED 2: a credit override wins the fingerprint override and the automatic
// selection, through the one resolver.
func reviewCreditOverrideBeatsFingerprintAndAutomatic(t *testing.T) {
	f := withReviewTx(t)
	source := structuredSource(t)
	fp := authornorm.SourceFingerprint(source)
	_, ids := f.persistBook(authorCredit(source))
	credit := ids[0]

	r := normalizedAs(t, source, authornorm.ClassStructuredPerson)
	resultID := f.storeResult(&r)
	f.finishJob(&r, resultID)
	report := f.resolve(&r, decide(t, registeredPolicy(t, f), &r, resultID))
	require.Equal(t, 1, report.States[models.CreditSelectionSelected])
	automatic := f.selection(credit)
	require.NotNil(t, automatic)
	require.NotNil(t, automatic.Basis)
	assert.Equal(t, models.CreditSelectionAutomatic, *automatic.Basis)

	byFingerprint, err := ApplyOverride(context.Background(), f.tx,
		OverrideScope{Fingerprint: fp[:]}, 1, correctionOf("Первый Правка"))
	require.NoError(t, err)
	selected := f.selection(credit)
	require.NotNil(t, selected.Basis)
	assert.Equal(t, models.CreditSelectionFingerprintOverride, *selected.Basis)
	require.NotNil(t, selected.ResultID)
	assert.Equal(t, byFingerprint.ResultID, *selected.ResultID)

	byCredit, err := ApplyOverride(context.Background(), f.tx,
		OverrideScope{CreditID: credit}, 2, correctionOf("Вторая Правка"))
	require.NoError(t, err)
	selected = f.selection(credit)
	require.NotNil(t, selected.Basis)
	assert.Equal(t, models.CreditSelectionCreditOverride, *selected.Basis, "the credit override wins")
	require.NotNil(t, selected.OverrideID)
	assert.Equal(t, byCredit.OverrideID, *selected.OverrideID)
	require.NotNil(t, selected.ResultID)
	assert.Equal(t, byCredit.ResultID, *selected.ResultID)
}

// RED 3: a fingerprint override covers the credits that exist and the ones
// persisted after it.
func reviewFingerprintOverrideCoversFutureCredits(t *testing.T) {
	f := withReviewTx(t)
	source := structuredSource(t)
	fp := authornorm.SourceFingerprint(source)
	_, ids := f.persistBook(authorCredit(source))
	present := ids[0]

	report, err := ApplyOverride(context.Background(), f.tx,
		OverrideScope{Fingerprint: fp[:]}, 1, correctionOf("Будущая Правка"))
	require.NoError(t, err)
	assert.Equal(t, 1, report.Resolved[models.CreditSelectionSelected])

	_, future := f.persistBook(authorCredit(source))
	require.NoError(t, ResolveCreditSelection(context.Background(), f.tx, future[0], nil))
	selected := f.selection(future[0])
	require.NotNil(t, selected)
	assert.Equal(t, models.CreditSelectionSelected, selected.State)
	require.NotNil(t, selected.Basis)
	assert.Equal(t, models.CreditSelectionFingerprintOverride, *selected.Basis)
	require.NotNil(t, selected.OverrideID)
	assert.Equal(t, report.OverrideID, *selected.OverrideID)

	presentSelected := f.selection(present)
	require.NotNil(t, presentSelected)
	assert.Equal(t, models.CreditSelectionSelected, presentSelected.State)
}

// RED 4: a changed source is another fingerprint and inherits nothing.
func reviewChangedFingerprintDoesNotInherit(t *testing.T) {
	f := withReviewTx(t)
	overridden := structuredSource(t)
	fp := authornorm.SourceFingerprint(overridden)
	_, err := ApplyOverride(context.Background(), f.tx,
		OverrideScope{Fingerprint: fp[:]}, 1, correctionOf("Правка Источника"))
	require.NoError(t, err)

	changed := initialsSource(t)
	_, ids := f.persistBook(authorCredit(changed))
	assert.Nil(t, f.selection(ids[0]), "a changed fingerprint inherits no override")

	require.NoError(t, ResolveCreditSelection(context.Background(), f.tx, ids[0], nil))
	assert.Nil(t, f.selection(ids[0]), "resolving without an outcome or override changes nothing")

	// With an automatic outcome the automatic path decides the changed
	// credit, never the other fingerprint's override.
	r := normalizedAs(t, changed, authornorm.ClassInitials)
	resultID := f.storeResult(&r)
	f.finishJob(&r, resultID)
	require.NoError(t, ResolveCreditSelection(context.Background(), f.tx, ids[0],
		decide(t, emptyPolicy(t, f), &r, resultID)))
	selected := f.selection(ids[0])
	require.NotNil(t, selected)
	assert.Equal(t, models.CreditSelectionReview, selected.State)
	assert.Nil(t, selected.OverrideID)
}

// RED 5: an edit is a second immutable result and override; the first rows
// keep their bytes and timestamps.
func reviewEditKeepsTheFirstResultAndOverride(t *testing.T) {
	f := withReviewTx(t)
	_, ids := f.persistBook(authorCredit(structuredSource(t)))
	credit := ids[0]

	first, err := ApplyOverride(context.Background(), f.tx,
		OverrideScope{CreditID: credit}, 1, correctionOf("Первая Правка"))
	require.NoError(t, err)
	firstResult := rowJSON(t, f.tx, "contributor_normalization_result", first.ResultID)
	firstOverride := rowJSON(t, f.tx, "contributor_manual_override", first.OverrideID)
	audits := f.auditCount(credit)

	second, err := ApplyOverride(context.Background(), f.tx,
		OverrideScope{CreditID: credit}, 2, correctionOf("Вторая Правка"))
	require.NoError(t, err)
	assert.NotEqual(t, first.ResultID, second.ResultID, "the edit writes a second result")
	assert.NotEqual(t, first.OverrideID, second.OverrideID, "the edit writes a second override")
	assert.Equal(t, firstResult, rowJSON(t, f.tx, "contributor_normalization_result", first.ResultID),
		"the first result keeps its bytes")
	assert.Equal(t, firstOverride, rowJSON(t, f.tx, "contributor_manual_override", first.OverrideID),
		"the first override keeps its bytes")
	assert.Greater(t, f.auditCount(credit), audits, "the edit leaves an audit transition")

	selected := f.selection(credit)
	require.NotNil(t, selected)
	assert.Equal(t, models.CreditSelectionSelected, selected.State)
	require.NotNil(t, selected.OverrideID)
	assert.Equal(t, second.OverrideID, *selected.OverrideID)
	require.NotNil(t, selected.ResultID)
	assert.Equal(t, second.ResultID, *selected.ResultID)
	assert.Equal(t, 2, f.count(`SELECT count(*) FROM contributor_manual_override WHERE scope_credit_id = ?`, credit))
}

// seedReviewItem prepares one ambiguous input behind one open review item:
// the empty policy routes the initials class to review, which opens the
// fingerprint's item with the proposal attached.
func seedReviewItem(t *testing.T, f *authorSchemaFixture) (itemID, credit int64, r authornorm.Result) {
	f.t.Helper()
	_, ids := f.persistBook(authorCredit(initialsSource(t)))
	r = normalizedAs(t, initialsSource(t), authornorm.ClassInitials)
	resultID := f.storeResult(&r)
	f.finishJob(&r, resultID)
	report := f.resolve(&r, decide(t, emptyPolicy(t, f), &r, resultID))
	require.Equal(t, 1, report.States[models.CreditSelectionReview])
	items := f.openReviewItems(r.SourceFingerprint)
	require.Len(t, items, 1)
	return items[0].ID, ids[0], r
}

// RED 6: the transitions are closed — every action decides an open item
// once, and a second decision, concurrent or repeated, is a conflict.
func reviewTransitionsClosed(t *testing.T) {
	ctx := context.Background()

	t.Run("accept copies the proposal into a manual selection", func(t *testing.T) {
		f := withReviewTx(t)
		item, credit, _ := seedReviewItem(t, f)
		report, err := ApplyReviewAction(ctx, f.tx, item, 1, ReviewDecision{Action: ReviewAccept})
		require.NoError(t, err)
		assert.NotZero(t, report.ResultID)

		selected := f.selection(credit)
		require.NotNil(t, selected)
		assert.Equal(t, models.CreditSelectionSelected, selected.State)
		require.NotNil(t, selected.Basis)
		assert.Equal(t, models.CreditSelectionFingerprintOverride, *selected.Basis)
		require.NotNil(t, selected.ResultID)
		assert.Equal(t, report.ResultID, *selected.ResultID)

		var manual models.ContributorNormalizationResult
		require.NoError(t, f.tx.Model(&manual).Where("id = ?", report.ResultID).Select())
		assert.Equal(t, models.NormalizationManual, manual.Method)
		assert.Equal(t, "И.", *manual.GivenName, "the proposal's name carries into the manual copy")
		assert.Nil(t, manual.NormalizationKey)
		assert.Nil(t, manual.ExtractorVersion)

		var decided models.ContributorReviewItem
		require.NoError(t, f.tx.Model(&decided).Where("id = ?", item).Select())
		assert.Equal(t, models.ReviewClosed, decided.Status)
		require.NotNil(t, decided.Resolution)
		assert.Equal(t, models.ReviewAccepted, *decided.Resolution)
		require.NotNil(t, decided.ResolutionResultID)
		assert.Equal(t, report.ResultID, *decided.ResolutionResultID)
	})

	t.Run("edit selects the correction", func(t *testing.T) {
		f := withReviewTx(t)
		item, credit, _ := seedReviewItem(t, f)
		correction := *correctionOf("Ручная Правка")
		report, err := ApplyReviewAction(ctx, f.tx, item, 1,
			ReviewDecision{Action: ReviewEdit, Correction: &correction})
		require.NoError(t, err)
		selected := f.selection(credit)
		require.NotNil(t, selected)
		assert.Equal(t, models.CreditSelectionSelected, selected.State)
		require.NotNil(t, selected.ResultID)
		assert.Equal(t, report.ResultID, *selected.ResultID)
		var manual models.ContributorNormalizationResult
		require.NoError(t, f.tx.Model(&manual).Where("id = ?", report.ResultID).Select())
		assert.Equal(t, "Ручная Правка", *manual.DisplayName)

		var decided models.ContributorReviewItem
		require.NoError(t, f.tx.Model(&decided).Where("id = ?", item).Select())
		require.NotNil(t, decided.Resolution)
		assert.Equal(t, models.ReviewEdited, *decided.Resolution)
	})

	t.Run("classify re-kinds the proposal", func(t *testing.T) {
		f := withReviewTx(t)
		item, credit, _ := seedReviewItem(t, f)
		report, err := ApplyReviewAction(ctx, f.tx, item, 1,
			ReviewDecision{Action: ReviewClassify, Kind: models.NormalizationCollective})
		require.NoError(t, err)
		var manual models.ContributorNormalizationResult
		require.NoError(t, f.tx.Model(&manual).Where("id = ?", report.ResultID).Select())
		assert.Equal(t, models.NormalizationCollective, manual.Kind)
		assert.Equal(t, "И.", *manual.GivenName, "the proposal's name stays")
		selected := f.selection(credit)
		require.NotNil(t, selected)
		assert.Equal(t, models.CreditSelectionSelected, selected.State)

		var decided models.ContributorReviewItem
		require.NoError(t, f.tx.Model(&decided).Where("id = ?", item).Select())
		require.NotNil(t, decided.Resolution)
		assert.Equal(t, models.ReviewClassified, *decided.Resolution)
	})

	t.Run("unresolved is a terminal human decision", func(t *testing.T) {
		f := withReviewTx(t)
		item, credit, _ := seedReviewItem(t, f)
		_, err := ApplyReviewAction(ctx, f.tx, item, 1, ReviewDecision{Action: ReviewLeaveUnresolved})
		require.NoError(t, err)
		selected := f.selection(credit)
		require.NotNil(t, selected)
		assert.Equal(t, models.CreditSelectionUnresolved, selected.State)
		require.NotNil(t, selected.UnresolvedReason)
		assert.Equal(t, models.UnresolvedReviewLeftUnresolved, *selected.UnresolvedReason)
		assert.Nil(t, selected.Basis)
		assert.Nil(t, selected.OverrideID)
		require.NotNil(t, selected.DecidedByUserID)
		assert.Equal(t, int64(1), *selected.DecidedByUserID)

		// A later automatic resolution does not replace the admin's verdict.
		r := normalizedAs(t, initialsSource(t), authornorm.ClassInitials)
		resultID := f.storeResult(&r)
		require.NoError(t, ResolveCreditSelection(ctx, f.tx, credit,
			decide(t, registeredPolicy(t, f), &r, resultID)))
		selected = f.selection(credit)
		assert.Equal(t, models.CreditSelectionUnresolved, selected.State,
			"an automatic outcome keeps the admin's decision")

		var decided models.ContributorReviewItem
		require.NoError(t, f.tx.Model(&decided).Where("id = ?", item).Select())
		assert.Equal(t, models.ReviewClosed, decided.Status)
		require.NotNil(t, decided.Resolution)
		assert.Equal(t, models.ReviewLeftUnresolved, *decided.Resolution)
	})

	t.Run("retry re-queues the local normalizer, never more", func(t *testing.T) {
		f := withReviewTx(t)
		item, _, r := seedReviewItem(t, f)

		resultsBefore := f.count(`SELECT count(*) FROM contributor_normalization_result`)
		overridesBefore := f.count(`SELECT count(*) FROM contributor_manual_override`)

		report, err := ApplyReviewAction(ctx, f.tx, item, 1, ReviewDecision{
			Action: ReviewRetry, RetryNormalizerVersion: authornorm.NormalizerVersion, RetryMaxAttempts: testRetryBudget})
		require.NoError(t, err)
		assert.False(t, report.JobQueued, "the same normalizer version finds the stored job")

		var job models.ContributorNormalizationJob
		require.NoError(t, f.tx.Model(&job).Where("normalization_key = ?", r.NormalizationKey[:]).Select())
		assert.Equal(t, models.NormalizationJobPending, job.Status, "the completed job is re-queued")
		assert.Nil(t, job.ResultID)
		assert.Nil(t, job.FinishedAt)

		assert.Equal(t, resultsBefore, f.count(`SELECT count(*) FROM contributor_normalization_result`),
			"a retry writes no result")
		assert.Equal(t, overridesBefore, f.count(`SELECT count(*) FROM contributor_manual_override`),
			"a retry writes no override")

		// A changed normalizer version is a new key: a new pending job. The
		// first item is closed by its retry, so a second input supplies the
		// second decision.
		fresh := nameValue(t,
			nameComponent(authornorm.ComponentFirst, "Б."),
			nameComponent(authornorm.ComponentLast, "Другой"))
		freshR := normalizedAs(t, fresh, authornorm.ClassInitials)
		_, ids := f.persistBook(authorCredit(fresh))
		freshID := f.storeResult(&freshR)
		f.finishJob(&freshR, freshID)
		report2 := f.resolve(&freshR, decide(t, emptyPolicy(t, f), &freshR, freshID))
		require.Equal(t, 1, report2.States[models.CreditSelectionReview])
		freshItems := f.openReviewItems(freshR.SourceFingerprint)
		require.Len(t, freshItems, 1)

		nextKey, keyErr := authornorm.NormalizationKey(freshR.SourceFingerprint, resolverExtractor, "authornorm-local-v2")
		require.NoError(t, keyErr)
		_, err = ApplyReviewAction(ctx, f.tx, freshItems[0].ID, 1, ReviewDecision{
			Action: ReviewRetry, RetryNormalizerVersion: "authornorm-local-v2", RetryMaxAttempts: testRetryBudget})
		require.NoError(t, err)
		assert.Equal(t, 1, f.jobCountByKey(nextKey), "a changed version queues a new job")
		_ = ids

		var decided models.ContributorReviewItem
		require.NoError(t, f.tx.Model(&decided).Where("id = ?", item).Select())
		assert.Equal(t, models.ReviewClosed, decided.Status)
		require.NotNil(t, decided.Resolution)
		assert.Equal(t, models.ReviewRetried, *decided.Resolution)
	})

	t.Run("a decided item refuses the next decision", func(t *testing.T) {
		f := withReviewTx(t)
		item, _, _ := seedReviewItem(t, f)
		_, err := ApplyReviewAction(ctx, f.tx, item, 1, ReviewDecision{Action: ReviewAccept})
		require.NoError(t, err)
		_, err = ApplyReviewAction(ctx, f.tx, item, 1, ReviewDecision{Action: ReviewAccept})
		assert.ErrorIs(t, err, ErrReviewConflict)
		_, err = ApplyReviewAction(ctx, f.tx, item, 1, ReviewDecision{Action: ReviewLeaveUnresolved})
		assert.ErrorIs(t, err, ErrReviewConflict)
	})

	t.Run("unknown items", func(t *testing.T) {
		f := withReviewTx(t)
		_, err := ApplyReviewAction(ctx, f.tx, 999_999, 1, ReviewDecision{Action: ReviewAccept})
		assert.ErrorIs(t, err, ErrReviewNotFound)
	})
}

// RED 7: a manual result of another result schema version keeps its history,
// flags the scope for review and changes nothing else.
func reviewIncompatibleManualSchemaReview(t *testing.T) {
	f := withReviewTx(t)
	source := structuredSource(t)
	fp := authornorm.SourceFingerprint(source)
	_, ids := f.persistBook(authorCredit(source))
	credit := ids[0]

	// A manual result written under result schema version 0 — a version the
	// pipeline no longer speaks.
	oldResult := f.returningID(`INSERT INTO contributor_normalization_result
		(source_fingerprint, result_schema_version, method, kind, status, display_name, search_key, script,
		 quality_flags, created_by_user_id)
		VALUES (?, '0', 'manual', 'person', 'normalized', 'Старая Правка', 'старая правка', 'Cyrl', '{}', 1)
		RETURNING id`, fp[:])
	oldOverride := f.creditOverride(credit, oldResult, fp[:], 1)
	require.NoError(t, ResolveCreditSelection(context.Background(), f.tx, credit, nil))
	selected := f.selection(credit)
	require.NotNil(t, selected)
	assert.Equal(t, models.CreditSelectionSelected, selected.State)

	beforeResult := rowJSON(t, f.tx, "contributor_normalization_result", oldResult)
	beforeOverride := rowJSON(t, f.tx, "contributor_manual_override", oldOverride)
	beforeSelection := rowJSONSelection(t, f.tx, credit)

	opened, err := FlagManualSchemaIncompatibility(context.Background(), f.tx, authornorm.ResultSchemaVersion)
	require.NoError(t, err)
	assert.Equal(t, 1, opened)

	var items []models.ContributorReviewItem
	require.NoError(t, f.tx.Model(&items).
		Where("scope_credit_id = ?", credit).
		Where("reason = ?", string(models.ReviewIncompatibleManualSchema)).Select())
	require.Len(t, items, 1)
	assert.Equal(t, models.ReviewOpen, items[0].Status)

	assert.Equal(t, beforeResult, rowJSON(t, f.tx, "contributor_normalization_result", oldResult),
		"the old manual result stays as history")
	assert.Equal(t, beforeOverride, rowJSON(t, f.tx, "contributor_manual_override", oldOverride),
		"the old override stays as history")
	assert.Equal(t, beforeSelection, rowJSONSelection(t, f.tx, credit),
		"the selection keeps the manual decision")

	// Idempotent where explicitly safe: the scope is already under review.
	opened, err = FlagManualSchemaIncompatibility(context.Background(), f.tx, authornorm.ResultSchemaVersion)
	require.NoError(t, err)
	assert.Zero(t, opened)

	_, err = FlagManualSchemaIncompatibility(context.Background(), f.tx, 0)
	assert.ErrorIs(t, err, authornorm.ErrInvalidSchemaVersion)
}

// RED 8: the queue pages stably when items share a timestamp — one
// transaction seeds several items, so now() is identical for all of them.
func reviewPaginationStableUnderDuplicateTimestamps(t *testing.T) {
	f := withReviewTx(t)
	var seeded []byte
	seed := func(n int) {
		for i := 0; i < n; i++ {
			seeded = append(seeded, 1)
			fp := fingerprint(32, byte(len(seeded)))
			f.exec(`INSERT INTO contributor_review_item
				(scope_fingerprint, source_fingerprint, reason)
				VALUES (?, ?, 'ambiguous_decision')`, fp, fp)
		}
	}
	seed(3)
	time.Sleep(10 * time.Millisecond)
	seed(2)
	var total int
	_, err := f.tx.QueryOne(pg.Scan(&total), `SELECT count(*) FROM contributor_review_item WHERE status = 'open'`)
	require.NoError(t, err)
	require.Equal(t, 5, total)

	var page []int64
	cursor := ReviewListFilter{Limit: 2}
	for {
		items, listErr := ListOpenReviewItems(context.Background(), f.tx, cursor)
		require.NoError(t, listErr)
		require.NotEmpty(t, items, "a full walk never returns an empty page before the end")
		for i := range items {
			page = append(page, items[i].ID)
		}
		if len(page) >= total {
			break
		}
		cursor = ReviewListFilter{
			Limit: 2, AfterCreatedAt: items[len(items)-1].CreatedAt, AfterID: items[len(items)-1].ID,
		}
	}
	require.Len(t, page, total)
	assert.Equal(t, total, len(uniqueIDs(page)), "no row appears twice or is skipped")

	// The order is (created_at, id): the first three share a timestamp and
	// still come out in id order.
	firstThree := page[:3]
	var createdAts []time.Time
	for _, id := range firstThree {
		var item models.ContributorReviewItem
		require.NoError(t, f.tx.Model(&item).Where("id = ?", id).Select())
		createdAts = append(createdAts, item.CreatedAt)
	}
	assert.True(t, createdAts[0].Equal(createdAts[1]) && createdAts[1].Equal(createdAts[2]),
		"the fixture really shares one timestamp")
	assert.Equal(t, firstThree[0]+1, firstThree[1])
	assert.Equal(t, firstThree[1]+1, firstThree[2])

	// Bad cursors and limits are typed errors before SQL.
	_, err = ListOpenReviewItems(context.Background(), f.tx, ReviewListFilter{Limit: -1})
	assert.ErrorIs(t, err, ErrInvalidReviewAction)
	_, err = ListOpenReviewItems(context.Background(), f.tx, ReviewListFilter{Limit: 101})
	assert.ErrorIs(t, err, ErrInvalidReviewAction)
	_, err = ListOpenReviewItems(context.Background(), f.tx, ReviewListFilter{AfterID: 5})
	assert.ErrorIs(t, err, ErrInvalidReviewAction)
}

func uniqueIDs(ids []int64) []int64 {
	seen := map[int64]bool{}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// RED 9: linked books are what the review screen shows — and nothing the
// decision paths consume: a retry touches only the local job queue.
func reviewLinkedBooksStayOnTheReviewScreen(t *testing.T) {
	f := withReviewTx(t)
	source := structuredSource(t)
	fp := authornorm.SourceFingerprint(source)
	books := make([]int64, 0, 3)
	for i := 0; i < 3; i++ {
		book, _ := f.persistBook(authorCredit(source), translatorCredit(source))
		books = append(books, book)
	}

	linked, err := LinkedBooksOfFingerprint(context.Background(), f.tx, fp[:], 10)
	require.NoError(t, err)
	require.Len(t, linked, len(books))
	for i, book := range books {
		assert.Equal(t, book, linked[i].BookID)
		assert.Equal(t, "fixture", linked[i].Title, "the legacy book's title")
	}

	empty, err := LinkedBooksOfFingerprint(context.Background(), f.tx, fingerprint(32, 0x77), 10)
	require.NoError(t, err)
	assert.Empty(t, empty)

	// The retry decision enqueues the local normalizer only: nothing derived
	// from the linked books is written anywhere.
	item, _, _ := seedReviewItem(t, f)
	tables := []string{
		"contributor_normalization_result", "contributor_manual_override",
		"book_contributor_credit_selection",
	}
	before := map[string]int{}
	for _, table := range tables {
		before[table] = f.count(`SELECT count(*) FROM ` + table)
	}
	_, err = ApplyReviewAction(context.Background(), f.tx, item, 1, ReviewDecision{
		Action: ReviewRetry, RetryNormalizerVersion: "authornorm-local-v3", RetryMaxAttempts: testRetryBudget})
	require.NoError(t, err)
	for _, table := range tables {
		assert.Equal(t, before[table], f.count(`SELECT count(*) FROM `+table),
			"the retry wrote nothing into %s", table)
	}
	assert.Equal(t, 1,
		f.count(`SELECT count(*) FROM contributor_normalization_job WHERE normalizer_version = 'authornorm-local-v3'`),
		"the retry queues exactly one local job")
}

// RED 10: two concurrent reviewers — one transition wins, the other reports
// the conflict. Each reviewer owns a real transaction on its own scratch
// database.
func TestConcurrentReviewersFirstWins(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()

	seed, err := s.Begin()
	require.NoError(t, err)
	next := authorSchemaIDBase
	f := &authorSchemaFixture{t: t, tx: seed, next: &next}
	item, _, _ := seedReviewItem(t, f)
	require.NoError(t, seed.Commit())

	type outcome struct {
		err error
	}
	ready := make(chan struct{}, 2)
	start := make(chan struct{})
	results := make(chan outcome, 2)
	reviewer := func(admin int64) {
		go func() {
			tx, txErr := s.Begin()
			if txErr != nil {
				results <- outcome{err: txErr}
				return
			}
			ready <- struct{}{}
			<-start
			_, applyErr := ApplyReviewAction(ctx, tx, item, admin, ReviewDecision{Action: ReviewAccept})
			if applyErr != nil {
				_ = tx.Rollback()
				results <- outcome{err: applyErr}
				return
			}
			results <- outcome{err: tx.Commit()}
		}()
	}
	reviewer(1)
	reviewer(2)
	<-ready
	<-ready
	close(start)

	wins, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		o := <-results
		if o.err == nil {
			wins++
			continue
		}
		assert.ErrorIs(t, o.err, ErrReviewConflict, "the losing reviewer reports the conflict: %v", o.err)
		conflicts++
	}
	assert.Equal(t, 1, wins)
	assert.Equal(t, 1, conflicts)

	var decided models.ContributorReviewItem
	require.NoError(t, s.Model(&decided).Where("id = ?", item).Select())
	assert.Equal(t, models.ReviewClosed, decided.Status)
	require.NotNil(t, decided.Resolution)
	assert.Equal(t, models.ReviewAccepted, *decided.Resolution)

	var overrides int
	_, err = s.QueryOne(pg.Scan(&overrides), `SELECT count(*) FROM contributor_manual_override`)
	require.NoError(t, err)
	assert.Equal(t, 1, overrides, "exactly the winner's override survived")
}

// The lock-order rule: a review action and a concurrent resolution of the
// same input both finish — the action locks every affected credit before it
// touches the item, so the resolver never waits on an item the action holds
// while the action waits on a credit the resolver holds.
func TestReviewActionKeepsLockOrderWithResolver(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()

	seed, err := s.Begin()
	require.NoError(t, err)
	next := authorSchemaIDBase
	f := &authorSchemaFixture{t: t, tx: seed, next: &next}
	item, credit, r := seedReviewItem(t, f)
	require.NoError(t, seed.Commit())

	var resultID int64
	_, err = s.QueryOne(pg.Scan(&resultID), `SELECT id FROM contributor_normalization_result
		WHERE normalization_key = ?`, r.NormalizationKey[:])
	require.NoError(t, err)
	auto := &AutomaticOutcome{
		ResultID: resultID, DecisionClass: r.DecisionClass,
		Decision: authornorm.Decision{Outcome: authornorm.OutcomeReview},
	}

	reviewTx, err := s.Begin()
	require.NoError(t, err)
	// Gate the reviewer just before it closes the item: by then it holds
	// every affected credit's lock, which is exactly what the order rule
	// guarantees.
	gate := &reviewCloseGate{Tx: reviewTx, ready: make(chan struct{}), release: make(chan struct{})}
	reviewDone := make(chan error, 1)
	go func() {
		_, applyErr := ApplyReviewAction(ctx, gate, item, 1, ReviewDecision{Action: ReviewLeaveUnresolved})
		if applyErr != nil {
			_ = reviewTx.Rollback()
			reviewDone <- applyErr
			return
		}
		reviewDone <- reviewTx.Commit()
	}()
	select {
	case <-gate.ready:
	case err = <-reviewDone:
		t.Fatalf("the review action ended before closing the item: %v", err)
	}

	resolveTx, err := s.Begin()
	require.NoError(t, err)
	resolution := resolveConcurrently(ctx, resolveTx, resolveTx, credit, auto)
	waitBlockedOrFinished(t, s, resolution)

	close(gate.release)
	require.NoError(t, <-reviewDone)
	require.NoError(t, <-resolution.done, "the resolver finishes without a deadlock")

	var after models.ContributorReviewItem
	require.NoError(t, s.Model(&after).Where("id = ?", item).Select())
	assert.Equal(t, models.ReviewClosed, after.Status)
	selected, err := selectionOn(s, credit)
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, models.CreditSelectionUnresolved, selected.State,
		"the review decision holds; the resolver ran after it and kept it")
}

func selectionOn(s *pg.DB, credit int64) (*models.BookContributorCreditSelection, error) {
	selected := new(models.BookContributorCreditSelection)
	err := s.Model(selected).Where("credit_id = ?", credit).Select()
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return selected, nil
}

// reviewCloseGate pauses the review action exactly before it closes the
// item, after every earlier statement ran.
type reviewCloseGate struct {
	*pg.Tx
	ready, release chan struct{}
}

func (g *reviewCloseGate) ExecContext(ctx context.Context, q interface{}, p ...interface{}) (pg.Result, error) {
	if sql, ok := q.(string); ok &&
		strings.Contains(sql, "UPDATE contributor_review_item") && strings.Contains(sql, "closed") {
		close(g.ready)
		<-g.release
	}
	return g.Tx.ExecContext(ctx, q, p...)
}

// reviewerProbeOwner is a valid lease owner UUID for the probes.
const reviewerProbeOwner = "6f1c1c3e-8a0b-4b7e-9d36-3f4f0c6b9a11"

// seedAmbiguousInput seeds one committed ambiguous input: book, current
// author credit, stored local result, pending job (the extraction's), and the
// open review item the empty policy's review outcome opens. Returns the
// credit, the result and the item.
func seedAmbiguousInput(t *testing.T, s *pg.DB) (credit int64, r authornorm.Result, item int64) {
	t.Helper()
	return seedAmbiguousInputAt(t, s, authorSchemaIDBase)
}

// seedAmbiguousInputAt is seedAmbiguousInput with an explicit fixture ID
// base, so several seeds can share one scratch database.
func seedAmbiguousInputAt(t *testing.T, s *pg.DB, base int64) (credit int64, r authornorm.Result, item int64) {
	t.Helper()
	ctx := context.Background()
	seed, err := s.Begin()
	require.NoError(t, err)
	next := base
	f := &authorSchemaFixture{t: t, tx: seed, next: &next}
	_, ids := f.persistBook(authorCredit(initialsSource(t)))
	credit = ids[0]
	r = normalizedAs(t, initialsSource(t), authornorm.ClassInitials)
	resultID := f.storeResult(&r)
	report := f.resolve(&r, decide(t, emptyPolicy(t, f), &r, resultID))
	require.Equal(t, 1, report.States[models.CreditSelectionReview])
	items := f.openReviewItems(r.SourceFingerprint)
	require.Len(t, items, 1)
	item = items[0].ID
	require.NoError(t, seed.Commit())
	_ = ctx
	return credit, r, item
}

// Probe for review finding 1: a same-version retry must not collide with the
// immutable attempt history — the next claim appends a new attempt instead of
// hitting (job_id, attempt_no) uniqueness.
func TestReviewRetryAfterRealAttemptsCanBeClaimed(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	credit, r, item := seedAmbiguousInput(t, s)

	claimOpts := LeaseClaimOptions{Limit: 10, Lease: time.Minute, MaxAttempts: 5}
	claims, err := ClaimLocalNormalizationJobs(ctx, s, reviewerProbeOwner, claimOpts)
	require.NoError(t, err)
	require.Len(t, claims, 1)
	require.Equal(t, 1, claims[0].AttemptNo)

	// A real failed attempt: the job ends failed with its attempt kept as
	// history.
	exhausted, err := FailLocalNormalizationJob(ctx, s, claims[0].ID, reviewerProbeOwner, LeaseFailure{
		ErrorClass: "normalizer_failed", RetryAfter: time.Minute, MaxAttempts: 1,
	})
	require.NoError(t, err)
	require.True(t, exhausted, "the failure used the last attempt")

	// The same-version retry reopens the failed job without touching its
	// attempt identities.
	tx, err := s.Begin()
	require.NoError(t, err)
	_, err = ApplyReviewAction(ctx, tx, item, 1, ReviewDecision{
		Action: ReviewRetry, RetryNormalizerVersion: authornorm.NormalizerVersion,
		RetryMaxAttempts: claimOpts.MaxAttempts})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	// The next claim appends attempt 2 instead of colliding with attempt 1.
	claims, err = ClaimLocalNormalizationJobs(ctx, s, reviewerProbeOwner, claimOpts)
	require.NoError(t, err, "the reopened job must be claimable without a duplicate key")
	require.Len(t, claims, 1)
	require.Equal(t, 2, claims[0].AttemptNo, "the attempt identity continues the history")

	resultID, err := InsertLocalResult(ctx, s, &r)
	require.NoError(t, err)
	err = s.RunInTransaction(ctx, func(tx *pg.Tx) error {
		return CompleteLocalNormalizationJob(ctx, tx, claims[0].ID, reviewerProbeOwner, resultID)
	})
	require.NoError(t, err)

	var job models.ContributorNormalizationJob
	require.NoError(t, s.Model(&job).Where("id = ?", claims[0].ID).Select())
	assert.Equal(t, models.NormalizationJobCompleted, job.Status)
	assert.Equal(t, 2, job.AttemptCount)
	var attempts int
	_, err = s.QueryOne(pg.Scan(&attempts),
		`SELECT count(*) FROM contributor_normalization_job_attempt WHERE job_id = ?`, claims[0].ID)
	require.NoError(t, err)
	assert.Equal(t, 2, attempts, "both attempts stay as history")

	// The credit's review decision was already closed by the retry; the
	// completion's resolution decides it now.
	require.NoError(t, s.RunInTransaction(ctx, func(tx *pg.Tx) error {
		return ResolveCreditSelection(ctx, tx, credit, &AutomaticOutcome{
			ResultID: resultID, DecisionClass: r.DecisionClass,
			Decision: authornorm.Decision{Outcome: authornorm.OutcomeReview},
		})
	}))
}

// Probe for review finding 2: a review retry and a real worker completion of
// the same input both finish — the documented lock order (credits ascending,
// then review items, then jobs) holds in both paths. The worker is gated at
// its first credit lock, which it reaches only after whatever job lock its
// path takes; the retry then locks the credits and waits at its job write.
func TestReviewRetryAndWorkerCompletionDoNotDeadlock(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	_, r, item := seedAmbiguousInput(t, s)

	// A pending job of the same fingerprint under a new normalizer version:
	// the retry will target it. The extraction's own v1 job steps aside so
	// the claim below is exactly the v2 one.
	fp := r.SourceFingerprint
	v2 := "authornorm-local-v2"
	key, err := authornorm.NormalizationKey(fp, resolverExtractor, v2)
	require.NoError(t, err)
	_, err = s.Exec(`INSERT INTO contributor_normalization_job
		(normalization_key, source_fingerprint, extractor_version, normalizer_version)
		VALUES (?, ?, ?, ?)`, key[:], fp[:], resolverExtractor, v2)
	require.NoError(t, err)
	_, err = s.Exec(`UPDATE contributor_normalization_job SET next_attempt_at = now() + interval '1 hour'
		WHERE normalizer_version = ?`, authornorm.NormalizerVersion)
	require.NoError(t, err)

	claims, err := ClaimLocalNormalizationJobs(ctx, s, reviewerProbeOwner,
		LeaseClaimOptions{Limit: 10, Lease: time.Minute, MaxAttempts: 5})
	require.NoError(t, err)
	require.Len(t, claims, 1)

	v2Result := r
	v2Result.NormalizerVersion = v2
	v2Result.ExtractorVersion = resolverExtractor
	v2Key, keyErr := authornorm.NormalizationKey(v2Result.SourceFingerprint, resolverExtractor, v2)
	require.NoError(t, keyErr)
	v2Result.NormalizationKey = v2Key
	decision := authornorm.Decision{Outcome: authornorm.OutcomeReview}

	// The worker runs the real completion, gated at its first credit lock —
	// the point where every lock its path takes earlier is already held.
	workerTx, err := s.Begin()
	require.NoError(t, err)
	workerGate := &creditLockGate{Tx: workerTx, ready: make(chan struct{}), release: make(chan struct{})}
	workerDone := make(chan error, 1)
	go func() {
		_, recordErr := RecordLocalNormalization(ctx, workerGate, claims[0].ID, reviewerProbeOwner,
			&v2Result, decision)
		if recordErr != nil {
			_ = workerTx.Rollback()
			workerDone <- recordErr
			return
		}
		workerDone <- workerTx.Commit()
	}()
	select {
	case <-workerGate.ready:
	case err = <-workerDone:
		t.Fatalf("the worker completion ended before its first credit lock: %v", err)
	}

	// The review retry runs concurrently, ungated: it locks the credits,
	// closes the item and then writes the job.
	reviewDone := make(chan error, 1)
	go func() {
		tx, txErr := s.Begin()
		if txErr != nil {
			reviewDone <- txErr
			return
		}
		if _, applyErr := ApplyReviewAction(ctx, tx, item, 1, ReviewDecision{
			Action: ReviewRetry, RetryNormalizerVersion: v2, RetryMaxAttempts: testRetryBudget}); applyErr != nil {
			_ = tx.Rollback()
			reviewDone <- applyErr
			return
		}
		reviewDone <- tx.Commit()
	}()

	// The review runs concurrently with the gated worker. On the documented
	// order it simply finishes (the worker holds nothing at its gate point);
	// a reversed order — the job completed before the credits resolve —
	// blocks it on the job's row lock, and releasing the worker then closes
	// the cycle into the deadlock PostgreSQL reports as 40P01.
	reviewErr, finished := waitForReviewOrLockWait(t, s, reviewDone)
	close(workerGate.release)
	if finished {
		require.NoError(t, reviewErr, "the review retry finished while the worker was gated")
	} else {
		require.NoError(t, <-reviewDone, "the review retry finishes")
	}
	require.NoError(t, <-workerDone, "the worker completion finishes")

	var job models.ContributorNormalizationJob
	require.NoError(t, s.Model(&job).Where("normalization_key = ?", key[:]).Select())
	assert.Equal(t, models.NormalizationJobCompleted, job.Status)
}

// waitForReviewOrLockWait waits until the review outcome arrived — returned
// with finished true — or a session of the scratch database waits on a lock.
func waitForReviewOrLockWait(t *testing.T, s *pg.DB, review chan error) (error, bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-review:
			return err, true
		default:
		}
		var waiting int
		_, err := s.QueryOne(pg.Scan(&waiting), `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`)
		require.NoError(t, err)
		if waiting > 0 {
			return nil, false
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the review neither finished nor contended")
	return nil, false
}

// creditLockGate pauses the worker completion exactly at its first credit
// lock statement.
type creditLockGate struct {
	*pg.Tx
	ready, release chan struct{}
}

func (g *creditLockGate) ExecContext(ctx context.Context, q interface{}, p ...interface{}) (pg.Result, error) {
	if sql, ok := q.(string); ok && strings.Contains(sql, "FOR NO KEY UPDATE") {
		close(g.ready)
		<-g.release
	}
	return g.Tx.ExecContext(ctx, q, p...)
}

// Probe for review finding 4: a terminal leave-unresolved decision keeps an
// older override from being revived by a later automatic resolution; an
// explicit later manual correction still supersedes it.
func TestReviewUnresolvedKeepsOlderOverrideDown(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	credit, r, item := seedAmbiguousInput(t, s)

	// An older override by another admin; the item stays open.
	tx, err := s.Begin()
	require.NoError(t, err)
	_, err = ApplyOverride(ctx, tx, OverrideScope{CreditID: credit}, 7,
		correctionOf("Старая Правка"))
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	selected, err := selectionOn(s, credit)
	require.NoError(t, err)
	require.NotNil(t, selected)
	require.NotNil(t, selected.OverrideID)

	// The terminal human decision, newer than the override.
	tx, err = s.Begin()
	require.NoError(t, err)
	_, err = ApplyReviewAction(ctx, tx, item, 8, ReviewDecision{Action: ReviewLeaveUnresolved})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	// A later automatic resolution of the same input must not revive the
	// older override.
	tx, err = s.Begin()
	require.NoError(t, err)
	resultID, err := InsertLocalResult(ctx, tx, &r)
	require.NoError(t, err)
	_, err = ResolveCredits(ctx, tx, r.SourceFingerprint[:], resolverExtractor, &AutomaticOutcome{
		ResultID: resultID, DecisionClass: r.DecisionClass,
		Decision: authornorm.Decision{Outcome: authornorm.OutcomeReview},
	})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	selected, selErr := selectionOn(s, credit)
	require.NoError(t, selErr)
	require.NotNil(t, selected)
	assert.Equal(t, models.CreditSelectionUnresolved, selected.State,
		"the terminal human decision survives the automatic resolution")
	require.NotNil(t, selected.UnresolvedReason)
	assert.Equal(t, models.UnresolvedReviewLeftUnresolved, *selected.UnresolvedReason)
	require.NotNil(t, selected.DecidedByUserID)
	assert.Equal(t, int64(8), *selected.DecidedByUserID)

	// An explicit later manual correction still supersedes it.
	tx, err = s.Begin()
	require.NoError(t, err)
	newer, err := ApplyOverride(ctx, tx, OverrideScope{CreditID: credit}, 9,
		correctionOf("Новая Правка"))
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	selected, selErr = selectionOn(s, credit)
	require.NoError(t, selErr)
	require.NotNil(t, selected)
	assert.Equal(t, models.CreditSelectionSelected, selected.State)
	require.NotNil(t, selected.OverrideID)
	assert.Equal(t, newer.OverrideID, *selected.OverrideID)
}

// Fix round 2, finding (b): the recency of human decisions is the order in
// which they were applied under the credit lock, not the start time of the
// transactions that applied them. A leave-unresolved applied after an
// override committed — from a transaction that began before the override's —
// is the newer decision, and a later automatic pass keeps it.
func TestReviewUnresolvedFromAnOlderTransactionKeepsOverrideDown(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	credit, r, item := seedAmbiguousInput(t, s)

	// The leave-unresolved transaction begins first and fixes its now().
	leaveTx, err := s.Begin()
	require.NoError(t, err)
	defer func() { _ = leaveTx.Rollback() }()
	var leaveBegan time.Time
	_, err = leaveTx.QueryOne(pg.Scan(&leaveBegan), `SELECT now()`)
	require.NoError(t, err)

	// A later transaction writes and commits an override by another admin.
	overrideTx, err := s.Begin()
	require.NoError(t, err)
	defer func() { _ = overrideTx.Rollback() }()
	written, err := ApplyOverride(ctx, overrideTx, OverrideScope{CreditID: credit}, 7,
		correctionOf("Старая Правка"))
	require.NoError(t, err)
	require.NoError(t, overrideTx.Commit())

	// Only then does the first transaction apply the decision.
	_, err = ApplyReviewAction(ctx, leaveTx, item, 8, ReviewDecision{Action: ReviewLeaveUnresolved})
	require.NoError(t, err)
	require.NoError(t, leaveTx.Commit())

	var overrideCreated time.Time
	_, err = s.QueryOne(pg.Scan(&overrideCreated),
		`SELECT created_at FROM contributor_manual_override WHERE id = ?`, written.OverrideID)
	require.NoError(t, err)
	require.True(t, leaveBegan.Before(overrideCreated),
		"the case needs the decision's transaction to have begun before the override's")

	// A later automatic resolution of the same input.
	tx, err := s.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	resultID, err := InsertLocalResult(ctx, tx, &r)
	require.NoError(t, err)
	_, err = ResolveCredits(ctx, tx, r.SourceFingerprint[:], resolverExtractor, &AutomaticOutcome{
		ResultID: resultID, DecisionClass: r.DecisionClass,
		Decision: authornorm.Decision{Outcome: authornorm.OutcomeReview},
	})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	selected, err := selectionOn(s, credit)
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, models.CreditSelectionUnresolved, selected.State,
		"the decision applied last under the credit lock survives the automatic pass")
	require.NotNil(t, selected.UnresolvedReason)
	assert.Equal(t, models.UnresolvedReviewLeftUnresolved, *selected.UnresolvedReason)
	require.NotNil(t, selected.DecidedByUserID)
	assert.Equal(t, int64(8), *selected.DecidedByUserID)
	assert.Nil(t, selected.OverrideID)
}

// The same ordering the other way round: an override applied after the
// leave-unresolved — from a transaction that began before the decision's —
// is the newer decision and stays selected through a later automatic pass.
func TestReviewOverrideFromAnOlderTransactionSupersedesUnresolved(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	credit, r, item := seedAmbiguousInput(t, s)

	overrideTx, err := s.Begin()
	require.NoError(t, err)
	defer func() { _ = overrideTx.Rollback() }()
	_, err = overrideTx.Exec(`SELECT now()`)
	require.NoError(t, err)

	leaveTx, err := s.Begin()
	require.NoError(t, err)
	defer func() { _ = leaveTx.Rollback() }()
	_, err = ApplyReviewAction(ctx, leaveTx, item, 8, ReviewDecision{Action: ReviewLeaveUnresolved})
	require.NoError(t, err)
	require.NoError(t, leaveTx.Commit())

	written, err := ApplyOverride(ctx, overrideTx, OverrideScope{CreditID: credit}, 9,
		correctionOf("Новая Правка"))
	require.NoError(t, err)
	require.NoError(t, overrideTx.Commit())

	tx, err := s.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	resultID, err := InsertLocalResult(ctx, tx, &r)
	require.NoError(t, err)
	_, err = ResolveCredits(ctx, tx, r.SourceFingerprint[:], resolverExtractor, &AutomaticOutcome{
		ResultID: resultID, DecisionClass: r.DecisionClass,
		Decision: authornorm.Decision{Outcome: authornorm.OutcomeReview},
	})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	selected, err := selectionOn(s, credit)
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, models.CreditSelectionSelected, selected.State)
	require.NotNil(t, selected.OverrideID)
	assert.Equal(t, written.OverrideID, *selected.OverrideID)
	assert.Nil(t, selected.DecidedByUserID)
}

// Fix round 2, finding (a), at the repository: the retry's budget is the
// worker's, compared with the same immutable attempt counter the claim uses.
// A stored job that has used it is refused — the caller's rollback leaves
// the item open and the job as it was — and one attempt left is enough.
func TestReviewRetryRefusedAtExhaustedBudget(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	_, r, item := seedAmbiguousInput(t, s)

	const budget = 1
	claims, err := ClaimLocalNormalizationJobs(ctx, s, reviewerProbeOwner,
		LeaseClaimOptions{Limit: 1, Lease: time.Minute, MaxAttempts: budget})
	require.NoError(t, err)
	require.Len(t, claims, 1)
	resultID, err := InsertLocalResult(ctx, s, &r)
	require.NoError(t, err)
	require.NoError(t, s.RunInTransaction(ctx, func(tx *pg.Tx) error {
		return CompleteLocalNormalizationJob(ctx, tx, claims[0].ID, reviewerProbeOwner, resultID)
	}))
	jobBefore := jobJSON(t, s, claims[0].ID)

	tx, err := s.Begin()
	require.NoError(t, err)
	_, err = ApplyReviewAction(ctx, tx, item, 1, ReviewDecision{
		Action: ReviewRetry, RetryNormalizerVersion: authornorm.NormalizerVersion, RetryMaxAttempts: budget})
	require.ErrorIs(t, err, ErrReviewRetryExhausted)
	require.NoError(t, tx.Rollback())

	var status models.ReviewStatus
	_, err = s.QueryOne(pg.Scan(&status), `SELECT status FROM contributor_review_item WHERE id = ?`, item)
	require.NoError(t, err)
	assert.Equal(t, models.ReviewOpen, status, "the refused retry leaves the item open")
	assert.Equal(t, jobBefore, jobJSON(t, s, claims[0].ID),
		"the refused retry leaves the job as it was")

	// One attempt left under a larger budget: the retry is acknowledged and
	// the next claim under that budget takes the job.
	tx, err = s.Begin()
	require.NoError(t, err)
	_, err = ApplyReviewAction(ctx, tx, item, 1, ReviewDecision{
		Action: ReviewRetry, RetryNormalizerVersion: authornorm.NormalizerVersion, RetryMaxAttempts: budget + 1})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	claims, err = ClaimLocalNormalizationJobs(ctx, s, reviewerProbeOwner,
		LeaseClaimOptions{Limit: 1, Lease: time.Minute, MaxAttempts: budget + 1})
	require.NoError(t, err)
	require.Len(t, claims, 1, "the acknowledged retry is claimable under its budget")
	assert.Equal(t, budget+1, claims[0].AttemptNo)
}

// jobJSON is one local job's full content, for byte comparisons outside a
// fixture transaction.
func jobJSON(t *testing.T, s *pg.DB, id int64) string {
	t.Helper()
	var row string
	_, err := s.QueryOne(pg.Scan(&row), `SELECT to_jsonb(r)::text FROM contributor_normalization_job r WHERE id = ?`, id)
	require.NoError(t, err)
	return row
}

// Fix round 2, finding (a), at the settlement: a failed job's credit that is
// in review with no open item — the retry closed it and handed the input back
// to the job — is settled like a credit without a resolution; a credit under
// an open item stays with that item.
func TestFailedInputsToSettleTakesCreditsOrphanedInReview(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	credit, r, item := seedAmbiguousInput(t, s)

	claims, err := ClaimLocalNormalizationJobs(ctx, s, reviewerProbeOwner,
		LeaseClaimOptions{Limit: 1, Lease: time.Minute, MaxAttempts: 1})
	require.NoError(t, err)
	require.Len(t, claims, 1)
	exhausted, err := FailLocalNormalizationJob(ctx, s, claims[0].ID, reviewerProbeOwner, LeaseFailure{
		ErrorClass: "normalizer_failed", RetryAfter: time.Minute, MaxAttempts: 1,
	})
	require.NoError(t, err)
	require.True(t, exhausted)

	inputs, err := FailedInputsToSettle(ctx, s, authornorm.NormalizerVersion, 10)
	require.NoError(t, err)
	assert.Empty(t, inputs, "a credit under an open review item is accounted by the item")

	_, err = s.Exec(`UPDATE contributor_review_item
		SET status = 'closed', resolution = 'retried', finished_at = now() WHERE id = ?`, item)
	require.NoError(t, err)
	inputs, err = FailedInputsToSettle(ctx, s, authornorm.NormalizerVersion, 10)
	require.NoError(t, err)
	require.Len(t, inputs, 1, "the orphaned review credit is settled")
	assert.Equal(t, r.SourceFingerprint[:], inputs[0].SourceFingerprint)
	assert.Equal(t, resolverExtractor, inputs[0].ExtractorVersion)

	// An open item scoped to the credit itself accounts for it as well.
	var scoped int64
	_, err = s.QueryOne(pg.Scan(&scoped), `INSERT INTO contributor_review_item
		(scope_credit_id, source_fingerprint, reason) VALUES (?, ?, 'ambiguous_decision') RETURNING id`,
		credit, r.SourceFingerprint[:])
	require.NoError(t, err)
	inputs, err = FailedInputsToSettle(ctx, s, authornorm.NormalizerVersion, 10)
	require.NoError(t, err)
	assert.Empty(t, inputs, "a credit under its own open review item is accounted by the item")
}

// A retry acknowledged while the job's attempt is in flight leaves the job
// alone: the lease stays the worker's, which completes it and resolves the
// credits — the retry never takes a running attempt's lease away.
func TestReviewRetryLeavesAnInFlightAttemptAlone(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	_, r, item := seedAmbiguousInput(t, s)

	claimOpts := LeaseClaimOptions{Limit: 1, Lease: time.Minute, MaxAttempts: testRetryBudget}
	claims, err := ClaimLocalNormalizationJobs(ctx, s, reviewerProbeOwner, claimOpts)
	require.NoError(t, err)
	require.Len(t, claims, 1)
	jobBefore := jobJSON(t, s, claims[0].ID)

	tx, err := s.Begin()
	require.NoError(t, err)
	report, err := ApplyReviewAction(ctx, tx, item, 1, ReviewDecision{
		Action: ReviewRetry, RetryNormalizerVersion: authornorm.NormalizerVersion,
		RetryMaxAttempts: claimOpts.MaxAttempts})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	assert.False(t, report.JobQueued)
	assert.Equal(t, jobBefore, jobJSON(t, s, claims[0].ID),
		"the in-flight attempt keeps its lease")

	resultID, err := InsertLocalResult(ctx, s, &r)
	require.NoError(t, err)
	require.NoError(t, s.RunInTransaction(ctx, func(tx *pg.Tx) error {
		return CompleteLocalNormalizationJob(ctx, tx, claims[0].ID, reviewerProbeOwner, resultID)
	}), "the worker that holds the lease still completes the job")
}

// Classifying a source as malformed is the terminal-invalid path the admin
// API's contract offers: an immutable manual result of kind malformed and
// status invalid, the credits marked invalid on it, the item closed — no
// override, because an invalid result is never selected.
func TestReviewClassifyMalformed(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()

	seed, err := s.Begin()
	require.NoError(t, err)
	next := authorSchemaIDBase
	f := &authorSchemaFixture{t: t, tx: seed, next: &next}
	item, credit, r := seedReviewItem(t, f)
	require.NoError(t, seed.Commit())

	tx, err := s.Begin()
	require.NoError(t, err)
	report, err := ApplyReviewAction(ctx, tx, item, 1, ReviewDecision{
		Action: ReviewClassify, Kind: models.NormalizationMalformed})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	assert.NotZero(t, report.ResultID)

	var manual models.ContributorNormalizationResult
	require.NoError(t, s.Model(&manual).Where("id = ?", report.ResultID).Select())
	assert.Equal(t, models.NormalizationManual, manual.Method)
	assert.Equal(t, models.NormalizationMalformed, manual.Kind)
	assert.Equal(t, models.NormalizationInvalid, manual.Status)
	assert.Nil(t, manual.NormalizationKey)

	selected, err := selectionOn(s, credit)
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, models.CreditSelectionInvalid, selected.State)
	require.NotNil(t, selected.ResultID)
	assert.Equal(t, report.ResultID, *selected.ResultID)
	assert.Nil(t, selected.Basis)
	assert.Nil(t, selected.OverrideID)
	require.NotNil(t, selected.DecidedByUserID)

	// A later automatic resolution keeps the admin's verdict.
	resultID, err := InsertLocalResult(ctx, s, &r)
	require.NoError(t, err)
	require.NoError(t, s.RunInTransaction(ctx, func(tx *pg.Tx) error {
		return ResolveCreditSelection(ctx, tx, credit, &AutomaticOutcome{
			ResultID: resultID, DecisionClass: r.DecisionClass,
			Decision: authornorm.Decision{Outcome: authornorm.OutcomeReview},
		})
	}))
	selected, err = selectionOn(s, credit)
	require.NoError(t, err)
	assert.Equal(t, models.CreditSelectionInvalid, selected.State)

	var decided models.ContributorReviewItem
	require.NoError(t, s.Model(&decided).Where("id = ?", item).Select())
	assert.Equal(t, models.ReviewClosed, decided.Status)
	require.NotNil(t, decided.Resolution)
	assert.Equal(t, models.ReviewClassified, *decided.Resolution)
	require.NotNil(t, decided.ResolutionResultID)
	assert.Equal(t, report.ResultID, *decided.ResolutionResultID)

	// An edit into malformed takes the same terminal path with the admin's
	// own fields.
	fresh, _, freshCredit := seedAmbiguousInputAt(t, s, authorSchemaIDBase+50_000)
	correction := correctionOf("Мусор")
	correction.Kind = models.NormalizationMalformed
	tx, err = s.Begin()
	require.NoError(t, err)
	editReport, err := ApplyReviewAction(ctx, tx, fresh, 2, ReviewDecision{
		Action: ReviewEdit, Correction: correction})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	edited, err := selectionOn(s, freshCredit)
	require.NoError(t, err)
	require.NotNil(t, edited)
	assert.Equal(t, models.CreditSelectionInvalid, edited.State)
	require.NotNil(t, edited.ResultID)
	assert.Equal(t, editReport.ResultID, *edited.ResultID)
}

// A contract-shaped malformed edit — punctuation-only display, no search key
// sent — is the terminal-invalid decision: the derived search key is empty,
// and an invalid manual result owes none (the schema's non-empty display and
// search key rule applies to status=normalized only).
func TestReviewMalformedEditWithoutSearchLetters(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()

	seed, err := s.Begin()
	require.NoError(t, err)
	next := authorSchemaIDBase
	f := &authorSchemaFixture{t: t, tx: seed, next: &next}
	item, credit, _ := seedReviewItem(t, f)
	require.NoError(t, seed.Commit())

	correction := correctionOf("...")
	correction.Kind = models.NormalizationMalformed
	correction.SearchKey = ""
	tx, err := s.Begin()
	require.NoError(t, err)
	report, applyErr := ApplyReviewAction(ctx, tx, item, 4, ReviewDecision{
		Action: ReviewEdit, Correction: correction})
	require.NoError(t, applyErr)
	require.NoError(t, tx.Commit())

	var manual models.ContributorNormalizationResult
	require.NoError(t, s.Model(&manual).Where("id = ?", report.ResultID).Select())
	assert.Equal(t, models.NormalizationMalformed, manual.Kind)
	assert.Equal(t, models.NormalizationInvalid, manual.Status)
	assert.Equal(t, "...", *manual.DisplayName)
	assert.Nil(t, manual.SearchKey, "an invalid manual result owes no search key")

	selected, err := selectionOn(s, credit)
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, models.CreditSelectionInvalid, selected.State)
	require.NotNil(t, selected.DecidedByUserID)
	assert.Equal(t, int64(4), *selected.DecidedByUserID)

	var decided models.ContributorReviewItem
	require.NoError(t, s.Model(&decided).Where("id = ?", item).Select())
	require.NotNil(t, decided.Resolution)
	assert.Equal(t, models.ReviewEdited, *decided.Resolution)
}
