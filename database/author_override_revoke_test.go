package database

// Review triage, Task 1: a manual override can be revoked, once, and a
// revoked override is history — it selects nothing, and every reader of the
// active override skips it. Each case runs on a scratch database of its own,
// so the global readers (swap sources, the schema flag) see only its rows.

import (
	"context"
	"strconv"
	"testing"

	"gopds-api/internal/authornorm"
	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withScratchTx opens one rolled-back transaction on a scratch database of
// the test's own, starting from the empty acceptance policy like withReviewTx.
func withScratchTx(t *testing.T) *authorSchemaFixture {
	t.Helper()
	scratch := jobsDB(t)
	tx, err := scratch.Begin()
	require.NoError(t, err, "beginning the scratch test transaction")
	t.Cleanup(func() { _ = tx.Rollback() })
	_, err = tx.Exec(`TRUNCATE author_acceptance_class`)
	require.NoError(t, err)
	next := authorSchemaIDBase
	return &authorSchemaFixture{t: t, tx: tx, next: &next}
}

// revoke revokes one override the way the undo does.
func (f *authorSchemaFixture) revoke(override, admin int64) {
	f.t.Helper()
	f.exec(`UPDATE contributor_manual_override SET revoked_at = now(), revoked_by_user_id = ?
		WHERE id = ?`, admin, override)
}

func TestOverrideRevokeIsTheOnlyChange(t *testing.T) {
	f := withScratchTx(t)
	admin := f.user()
	fp := fingerprint(32, 0x51)
	result := f.result(manualRow(fp, admin))
	other := f.result(manualRow(fp, admin))
	override := f.returningID(overrideInsert, nil, fp, fp, result, admin)

	// Before the revoke: every other column stays frozen, also beside a revoke.
	f.reject(sqlstateRestrict, "immutable",
		`UPDATE contributor_manual_override SET result_id = ? WHERE id = ?`, other, override)
	f.reject(sqlstateRestrict, "immutable",
		`UPDATE contributor_manual_override SET revoked_at = now(), revoked_by_user_id = ?, created_by_user_id = ?
		WHERE id = ?`, admin, admin+1, override)
	// The two columns go together, and the actor is a real one.
	f.reject(sqlstateCheck, "contributor_manual_override_revoke_check",
		`UPDATE contributor_manual_override SET revoked_at = now() WHERE id = ?`, override)
	f.reject(sqlstateCheck, "contributor_manual_override_revoke_check",
		`UPDATE contributor_manual_override SET revoked_by_user_id = ? WHERE id = ?`, admin, override)
	f.reject(sqlstateCheck, "contributor_manual_override_revoke_check",
		`UPDATE contributor_manual_override SET revoked_at = now(), revoked_by_user_id = 0 WHERE id = ?`, override)

	f.revoke(override, admin)
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM contributor_manual_override
		WHERE id = ? AND revoked_at IS NOT NULL AND revoked_by_user_id = ?`, override, admin))

	// After it: a revoke is never taken back, moved or repeated.
	for _, update := range []string{
		`UPDATE contributor_manual_override SET revoked_at = now() - interval '1 hour' WHERE id = ?`,
		`UPDATE contributor_manual_override SET revoked_at = NULL, revoked_by_user_id = NULL WHERE id = ?`,
		`UPDATE contributor_manual_override SET revoked_by_user_id = revoked_by_user_id + 1 WHERE id = ?`,
		`UPDATE contributor_manual_override SET revoked_at = now(), revoked_by_user_id = revoked_by_user_id WHERE id = ?`,
	} {
		f.reject(sqlstateRestrict, "already revoked", update, override)
	}
	f.reject(sqlstateRestrict, "immutable", `DELETE FROM contributor_manual_override WHERE id = ?`, override)
}

func TestActiveOverrideSkipsRevoked(t *testing.T) {
	f := withScratchTx(t)
	admin := f.user()
	source := structuredSource(t)
	fp := authornorm.SourceFingerprint(source)
	_, ids := f.persistBook(authorCredit(source))
	credit := ids[0]

	older := f.fingerprintOverride(fp[:], f.result(manualRow(fp[:], admin)), admin)
	newer := f.fingerprintOverride(fp[:], f.result(manualRow(fp[:], admin)), admin)
	byCredit := f.creditOverride(credit, f.result(manualRow(fp[:], admin)), fp[:], admin)
	require.Less(t, older, newer)

	active := func() (fingerprintScope, creditScope int64) {
		t.Helper()
		got, err := ActiveOverrideForFingerprint(f.tx, fp[:])
		require.NoError(t, err)
		if got != nil {
			fingerprintScope = got.ID
		}
		got, err = ActiveOverrideForCredit(f.tx, credit)
		require.NoError(t, err)
		if got != nil {
			creditScope = got.ID
		}
		return fingerprintScope, creditScope
	}

	fpScope, creditScope := active()
	assert.Equal(t, newer, fpScope, "the newest fingerprint override is active")
	assert.Equal(t, byCredit, creditScope, "the credit override wins")

	f.revoke(byCredit, admin)
	fpScope, creditScope = active()
	assert.Equal(t, newer, fpScope)
	assert.Equal(t, newer, creditScope, "a revoked credit override falls through to the fingerprint override")

	f.revoke(newer, admin)
	fpScope, creditScope = active()
	assert.Equal(t, older, fpScope, "a revoked newer override gives way to the older one")
	assert.Equal(t, older, creditScope)

	f.revoke(older, admin)
	fpScope, creditScope = active()
	assert.Zero(t, fpScope, "every override revoked: none is active")
	assert.Zero(t, creditScope)
}

func TestSelectionThroughARevokedOverrideIsRefused(t *testing.T) {
	f := withScratchTx(t)
	admin := f.user()
	fp := fingerprint(32, 0x52)
	credit := f.authorCredit(fp)
	manual := f.result(manualRow(fp, admin))
	byFingerprint := f.returningID(overrideInsert, nil, fp, fp, manual, admin)
	byCredit := f.returningID(overrideInsert, credit, nil, fp, manual, admin)

	// Unrevoked, both select.
	f.accept(selectionInsert, credit, fp, "selected", manual, "fingerprint_override", byFingerprint, nil, nil, admin)
	f.accept(selectionInsert, credit, fp, "selected", manual, "credit_override", byCredit, nil, nil, admin)

	f.revoke(byFingerprint, admin)
	f.revoke(byCredit, admin)
	f.reject(sqlstateCheck, "a revoked override selects nothing",
		selectionInsert, credit, fp, "selected", manual, "fingerprint_override", byFingerprint, nil, nil, admin)
	f.reject(sqlstateCheck, "a revoked override selects nothing",
		selectionInsert, credit, fp, "selected", manual, "credit_override", byCredit, nil, nil, admin)
}

func TestLayerDeleteStillRemovesOverrides(t *testing.T) {
	f := withScratchTx(t)
	admin := f.user()
	source := structuredSource(t)
	fp := authornorm.SourceFingerprint(source)
	book, ids := f.persistBook(authorCredit(source))
	override := f.creditOverride(ids[0], f.manualResultFor(fp[:], admin), fp[:], admin)
	f.revoke(override, admin)

	f.exec(`SELECT author_layer_delete_books(?::bigint[])`, pg.Array([]int64{book}))
	assert.Zero(t, f.count(`SELECT count(*) FROM contributor_manual_override WHERE id = ?`, override),
		"the revoked credit override goes with its book")
}

func TestSwapSourcesIgnoreRevokedOverrides(t *testing.T) {
	f := withScratchTx(t)
	admin := f.user()
	source := structuredSource(t)
	fp := authornorm.SourceFingerprint(source)
	f.persistBook(authorCredit(source))
	r := normalizedAs(t, source, authornorm.ClassStructuredPerson)
	resultID := f.storeResult(&r)
	f.finishJob(&r, resultID)
	f.resolve(&r, decide(t, registeredPolicy(t, f), &r, resultID))

	swapSources := f.swapSourceFingerprints
	require.Equal(t, [][]byte{fp[:]}, swapSources(), "an automatic input with no manual decision is a source")

	override := f.fingerprintOverride(fp[:], f.manualResultFor(fp[:], admin), admin)
	assert.Empty(t, swapSources(), "an override on the fingerprint keeps it out")

	f.revoke(override, admin)
	assert.Equal(t, [][]byte{fp[:]}, swapSources(), "a revoked override counts as none")
}

// A superseded snapshot keeps its credit's selection through the override
// after the override is revoked: the undo re-resolves only current credits.
// That historical row must not keep the fingerprint out of the swap sources
// once the current credit is selected automatically again.
func TestSwapSourcesIgnoreAHistoricalSelectionThroughARevokedOverride(t *testing.T) {
	f := withScratchTx(t)
	admin := f.user()
	source := structuredSource(t)
	fp := authornorm.SourceFingerprint(source)
	book, old := f.persistBook(authorCredit(source))
	r := normalizedAs(t, source, authornorm.ClassStructuredPerson)
	resultID := f.storeResult(&r)
	f.finishJob(&r, resultID)
	policy := registeredPolicy(t, f)

	override := f.fingerprintOverride(fp[:], f.manualResultFor(fp[:], admin), admin)
	f.resolve(&r, decide(t, policy, &r, resultID))
	oldSel := f.selection(old[0])
	require.NotNil(t, oldSel)
	require.NotNil(t, oldSel.OverrideID)
	require.Equal(t, override, *oldSel.OverrideID, "the first snapshot's credit is selected through the override")

	current := f.persistVersion(book, bookMD5(book+1_000_000), authorCredit(source))
	require.Zero(t, f.count(`SELECT count(*) FROM book_contributor_credit c
		JOIN book_metadata_snapshot s ON s.id = c.snapshot_id AND s.is_current WHERE c.id = ?`, old[0]),
		"the rescan superseded the first snapshot")
	f.revoke(override, admin)
	f.resolve(&r, decide(t, policy, &r, resultID))
	cur := f.selection(current[0])
	require.NotNil(t, cur)
	require.Equal(t, models.CreditSelectionSelected, cur.State)
	require.Nil(t, cur.OverrideID, "the current credit is selected automatically")
	oldSel = f.selection(old[0])
	require.NotNil(t, oldSel.OverrideID, "the superseded selection still names the revoked override")

	assert.Equal(t, [][]byte{fp[:]}, f.swapSourceFingerprints(),
		"a historical selection through a revoked override counts as none")
}

// The admin's own decision stays independent of revocation: left unresolved
// on a superseded snapshot, it keeps the fingerprint out even though the
// current credit is selected automatically and no override exists.
func TestSwapSourcesKeepOutAHistoricalAdminDecision(t *testing.T) {
	f := withScratchTx(t)
	admin := f.user()
	source := structuredSource(t)
	fp := authornorm.SourceFingerprint(source)
	book, old := f.persistBook(authorCredit(source))
	r := normalizedAs(t, source, authornorm.ClassStructuredPerson)
	resultID := f.storeResult(&r)
	f.finishJob(&r, resultID)
	policy := registeredPolicy(t, f)
	f.exec(leaveUnresolvedSQL, old[0], fp[:], admin)

	current := f.persistVersion(book, bookMD5(book+1_000_000), authorCredit(source))
	f.resolve(&r, decide(t, policy, &r, resultID))
	cur := f.selection(current[0])
	require.NotNil(t, cur)
	require.Equal(t, models.CreditSelectionSelected, cur.State)
	require.Nil(t, cur.DecidedByUserID, "the current credit is selected automatically")

	assert.Empty(t, f.swapSourceFingerprints(), "a superseded admin decision still keeps the fingerprint out")
}

// swapSourceFingerprints lists the fingerprints AuthorLLMSwapSources returns.
func (f *authorSchemaFixture) swapSourceFingerprints() [][]byte {
	f.t.Helper()
	rows, err := AuthorLLMSwapSources(context.Background(), f.tx)
	require.NoError(f.t, err)
	var fps [][]byte
	for i := range rows {
		fps = append(fps, rows[i].SourceFingerprint)
	}
	return fps
}

func TestIncompatibleSchemaFlagsSkipRevoked(t *testing.T) {
	ctx := context.Background()
	f := withScratchTx(t)
	admin := f.user()
	source := structuredSource(t)
	fp := authornorm.SourceFingerprint(source)
	_, ids := f.persistBook(authorCredit(source))
	credit := ids[0]

	oldResult := func() int64 {
		return f.returningID(`INSERT INTO contributor_normalization_result
			(source_fingerprint, result_schema_version, method, kind, status, display_name, search_key, script,
			 quality_flags, created_by_user_id)
			VALUES (?, '0', 'manual', 'person', 'normalized', 'Старая Правка', 'старая правка', 'Cyrl', '{}', ?)
			RETURNING id`, fp[:], admin)
	}
	current := f.returningID(`INSERT INTO contributor_normalization_result
		(source_fingerprint, result_schema_version, method, kind, status, display_name, search_key, script,
		 quality_flags, created_by_user_id)
		VALUES (?, ?, 'manual', 'person', 'normalized', 'Новая Правка', 'новая правка', 'Cyrl', '{}', ?)
		RETURNING id`, fp[:], strconv.Itoa(authornorm.ResultSchemaVersion), admin)

	// The credit scope: an old-schema override under a newer, revoked one.
	// The old one is active again, so its scope is flagged.
	f.creditOverride(credit, oldResult(), fp[:], admin)
	f.revoke(f.creditOverride(credit, current, fp[:], admin), admin)
	// The fingerprint scope: its only override is old-schema and revoked, so
	// it has none to flag.
	f.revoke(f.fingerprintOverride(fp[:], oldResult(), admin), admin)

	opened, err := FlagManualSchemaIncompatibility(ctx, f.tx, authornorm.ResultSchemaVersion)
	require.NoError(t, err)
	assert.Equal(t, 1, opened)
	assert.Equal(t, 1, f.count(`SELECT count(*) FROM contributor_review_item
		WHERE scope_credit_id = ? AND reason = 'incompatible_manual_schema' AND status = 'open'`, credit))
	assert.Zero(t, f.count(`SELECT count(*) FROM contributor_review_item
		WHERE scope_fingerprint = ? AND reason = 'incompatible_manual_schema'`, fp[:]))
}
